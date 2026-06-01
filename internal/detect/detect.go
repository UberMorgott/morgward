// Package detect runs the §0.5 pre-flight discovery and §2 dynamic detection:
// OS version, egress interface, client IP, server IPs, and greenfield/brownfield
// classification. Everything is derived at runtime — nothing is hardcoded.
package detect

import (
	"strconv"
	"strings"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// Facts is the resolved environment the steps condition on.
type Facts struct {
	ID          string // os-release ID (expect "ubuntu")
	VersionID   string // "24.04" | "26.04"
	Codename    string // "noble" | "resolute"
	Is2604      bool
	Is2404      bool
	IsUbuntu    bool
	Virt        string // systemd-detect-virt (none = bare metal)
	Kernel      string // uname -r
	EgressIface string
	IfaceMAC    string
	ClientIP    string // controller source IP (for ignoreip / exempt lists)
	ServerIPv4  string
	ServerIPv6  string
	HasIPv6     bool
	IPForward   bool
	DockerSeen  bool
	Greenfield  bool
	Listeners   []string // raw ss lines for public-facing sockets
	Inventory   string   // full §0.5 inventory dump (written to /root/vps-inventory.md)

	// PendingUpgrades is the count of packages a full-upgrade WOULD install/upgrade
	// (the "Inst" lines of `apt-get -s full-upgrade`, the same count A8 reports).
	// Best-effort: a parse/transport miss yields 0 — it never fails the scan.
	PendingUpgrades int

	AlreadyHardened bool     // box already carries this runbook's hardening markers
	HardenMarkers   []string // which markers were found
}

// Run executes all detection probes against the connected box.
func Run(c *sshx.Client) *Facts {
	f := &Facts{}

	osr := c.Sudo(`. /etc/os-release 2>/dev/null; printf '%s|%s|%s' "$ID" "$VERSION_ID" "$VERSION_CODENAME"`).Out()
	parts := strings.SplitN(osr, "|", 3)
	if len(parts) == 3 {
		f.ID, f.VersionID, f.Codename = parts[0], parts[1], parts[2]
	}
	f.IsUbuntu = f.ID == "ubuntu"
	f.Is2604 = strings.HasPrefix(f.VersionID, "26.04")
	f.Is2404 = strings.HasPrefix(f.VersionID, "24.04")

	f.Virt = c.Run("systemd-detect-virt").Out()
	f.Kernel = c.Run("uname -r").Out()

	// Egress interface from the DEFAULT ROUTE, not the client IP (§2).
	f.EgressIface = c.Run(`ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1); exit}}'`).Out()
	if f.EgressIface != "" {
		f.IfaceMAC = c.Run("cat /sys/class/net/" + f.EgressIface + "/address 2>/dev/null").Out()
		f.ServerIPv4 = c.Run(`ip -4 addr show ` + f.EgressIface + ` | awk '/inet /{print $2}' | cut -d/ -f1 | head -1`).Out()
		v6 := c.Run(`ip -6 addr show ` + f.EgressIface + ` scope global | awk '/inet6 /{print $2}' | cut -d/ -f1 | head -1`).Out()
		if v6 != "" {
			f.ServerIPv6 = v6
			f.HasIPv6 = true
		}
	}

	f.ClientIP = c.Run(`echo "$SSH_CONNECTION" | awk '{print $1}'`).Out()

	fwd := c.Run("sysctl -n net.ipv4.ip_forward").Out()
	f.IPForward = fwd == "1"

	f.DockerSeen = c.Run("command -v docker >/dev/null 2>&1 && echo yes").Out() == "yes"

	// Pending-upgrade count: simulate a full-upgrade and count its "Inst" lines (the
	// exact command A8 uses). Best-effort — a parse/transport miss leaves it at 0 so
	// the scan never fails on it.
	if n, err := strconv.Atoi(c.Sudo(
		"DEBIAN_FRONTEND=noninteractive apt-get -s full-upgrade 2>/dev/null | grep -c '^Inst' || echo 0").Out()); err == nil {
		f.PendingUpgrades = n
	}

	// Listening sockets, excluding loopback binds and sshd itself.
	ss := c.Sudo(`ss -tulpnH 2>/dev/null`).Stdout
	for line := range strings.SplitSeq(ss, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		local := fields[4] // Local Address:Port
		if strings.HasPrefix(local, "127.") || strings.HasPrefix(local, "[::1]") {
			continue
		}
		if strings.Contains(line, "sshd") || strings.Contains(line, `"ssh"`) {
			continue
		}
		// systemd-resolved stub on 127.0.0.53 is loopback — already skipped above.
		f.Listeners = append(f.Listeners, line)
	}

	f.Greenfield = !f.IPForward && !f.DockerSeen && len(f.Listeners) == 0

	// Already-hardened detection: a box that already carries this runbook's
	// markers must NOT be treated as a fresh greenfield target (re-running
	// Phase A blind is pointless and the firewall/SSH state is already final).
	markerScript := `[ -f /etc/ssh/sshd_config.d/99-hardening.conf ] && echo m:99-hardening
iptables -S INPUT 2>/dev/null | grep -q -- '-P INPUT DROP' && echo m:input-drop
systemctl is-active fail2ban >/dev/null 2>&1 && echo m:fail2ban
[ "$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)" = bbr ] && echo m:bbr
[ -f /etc/sysctl.d/99-zz-kernel-harden.conf ] && echo m:kernel-harden`
	for line := range strings.SplitSeq(c.Sudo(markerScript).Stdout, "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(line), "m:"); ok && m != "" {
			f.HardenMarkers = append(f.HardenMarkers, m)
		}
	}
	if len(f.HardenMarkers) >= 2 {
		f.AlreadyHardened = true
		f.Greenfield = false // a hardened box is not a fresh target
	}

	f.Inventory = buildInventory(c)
	return f
}

// buildInventory dumps the full §0.5 read-only probe set for the on-box record.
func buildInventory(c *sshx.Client) string {
	script := `
echo "=== listening sockets ==="; ss -tulpnH 2>/dev/null
echo "=== running services ==="; systemctl list-units --type=service --state=running --no-legend 2>/dev/null
echo "=== timers ==="; systemctl list-timers --all --no-legend 2>/dev/null
echo "=== docker ==="; command -v docker >/dev/null && docker ps --format '{{.Names}} {{.Image}} {{.Ports}}' 2>/dev/null || echo "no docker"
echo "=== wireguard ==="; wg show 2>/dev/null || echo "no wg"
echo "=== forwarding ==="; sysctl net.ipv4.ip_forward net.ipv6.conf.all.forwarding 2>/dev/null
echo "=== nat rules ==="; iptables -t nat -S 2>/dev/null
echo "=== firewall ==="; ufw status 2>/dev/null; iptables -S 2>/dev/null | head -40
`
	return c.Sudo(script).Stdout
}
