// Package detect runs the §0.5 pre-flight discovery and §2 dynamic detection:
// OS version, egress interface, client IP, server IPs, and greenfield/brownfield
// classification. Everything is derived at runtime — nothing is hardcoded.
package detect

import (
	"fmt"
	"sort"
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

	// --- Brownfield coexistence facts (drive the service-preserving step paths) ---
	// All best-effort: a probe miss leaves the field zero/empty and never fails the
	// scan. They are populated even on a greenfield box (where they come out empty),
	// so the steps can branch purely on Greenfield/Forwarding without re-probing.
	ListenPortsTCP []int    // public (non-loopback) TCP listening ports, deduped/sorted
	ListenPortsUDP []int    // public (non-loopback) UDP listening ports, deduped/sorted
	WireguardSeen  bool     // wg interfaces / wg-quick|openvpn unit active / *.conf present
	NatRules       bool     // iptables nat table carries real MASQUERADE/SNAT/DNAT rules
	Forwarding     bool     // IPForward || DockerSeen || WireguardSeen || NatRules
	DiskSwap       []string // active swap devices whose TYPE != zram (e.g. /swapfile)
	SSHKeyUsers    []string // login users (incl root) with a non-empty authorized_keys

	// FirewallMgr is the active firewall manager A1 must not impose a conflicting
	// second layer on: "ufw" | "firewalld" | "nftables" | "iptables" | "none".
	// Classified conservatively — when unsure between nftables and iptables we pick
	// iptables (the round-1 path), so a docker/iptables box is never wrongly deferred.
	FirewallMgr string
	// ListenServices is the role-agnostic "what's found" map: every public listening
	// socket as (proto, port, process), deduped by (proto,port). Surfaced in the
	// inventory so the operator sees the box's de-facto role. ListenPortsTCP/UDP stay
	// the ruleset source of truth; this is presentation/diagnostics only.
	ListenServices []ListenService

	// PendingUpgrades is the count of packages a full-upgrade WOULD install/upgrade
	// (the "Inst" lines of `apt-get -s full-upgrade`, the same count A8 reports).
	// Best-effort: a parse/transport miss yields 0 — it never fails the scan.
	PendingUpgrades int

	AlreadyHardened bool     // box already carries this runbook's hardening markers
	HardenMarkers   []string // which markers were found
}

// ManagesIPTables reports whether morgward itself owns the box's raw iptables
// INPUT ruleset — i.e. A1 wrote (or will write) the INPUT-DROP + persisted
// rules.v4 build. True on a greenfield box (fresh deterministic build) and on a
// brownfield box whose firewall manager is plain "iptables" or "none" (the
// coexistRuleset path). FALSE when an external manager owns the firewall: "ufw"
// (A1 only adds additive `ufw allow` rules) or "firewalld"/"nftables" (A1 defers
// entirely and changes nothing). Downstream consumers (A8 reboot gates, the §V
// matrix, the A1 revert, A10's drop-log) use this so they only assert/flush
// iptables state that morgward actually owns. CONSERVATIVE: morgward owns the
// ruleset UNLESS an external manager is explicitly detected. Greenfield is always
// managed; otherwise only the three external managers ("ufw"/"firewalld"/"nftables")
// opt out. An empty/unknown FirewallMgr therefore reads as managed — matching both
// classifyFirewallMgr (which already collapses an ambiguous box to "iptables") and
// the CLAUDE.md rule "when FirewallMgr unknown, treat as iptables", so a detection
// miss degrades to the proven byte-identical round-1 iptables behavior, never to a
// wrong defer.
func (f *Facts) ManagesIPTables() bool {
	if f.Greenfield {
		return true
	}
	switch f.FirewallMgr {
	case "ufw", "firewalld", "nftables":
		return false
	default: // "iptables", "none", "" (unknown) → morgward owns the ruleset
		return true
	}
}

// ListenService is one public listening socket: its protocol, port, the owning
// process name (empty when ss could not attribute one), the owning PID, and a
// universal Origin tag classified by PROVENANCE (the listener's cgroup + a docker
// ps cross-ref) — never by an app whitelist. It is the role-agnostic surfacing
// record — observed, never matched against a service whitelist.
type ListenService struct {
	Proto   string // "tcp" | "udp"
	Port    int
	Process string // program name from users:(("NAME",pid=...)) or ""
	PID     int    // owning pid from users:(("NAME",pid=N,...)); 0 when unattributed
	// Origin is the provenance tag: "host" | "docker" | "docker: <name>" | "k8s" |
	// "podman" | "lxc" | "systemd: <unit>" — filled by a second cgroup/docker pass
	// (classifyServiceOrigins). "" when the pass could not run (best-effort).
	Origin string
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

	// Listening sockets, excluding loopback binds and sshd itself. The SAME output
	// also feeds the coexistence port sets (A1 opens these on the brownfield path).
	ss := c.Sudo(`ss -tulpnH 2>/dev/null`).Stdout
	f.ListenPortsTCP, f.ListenPortsUDP, f.ListenServices, f.Listeners = parseListeners(ss)

	// Universal origin tag per service (provenance by cgroup + docker ps cross-ref).
	// Surfacing-only and best-effort — never touches the ruleset/coexistence fields
	// above and never fails the scan. Requires f.DockerSeen (set earlier).
	classifyServiceOrigins(c, f)

	// WireGuard / OpenVPN presence: any of an up wg interface, an active
	// wg-quick@*/openvpn* unit, or a *.conf under /etc/wireguard.
	wgScript := `wg show interfaces 2>/dev/null | grep -q . && echo wg
systemctl is-active 'wg-quick@*' 2>/dev/null | grep -q '^active' && echo wgq
systemctl is-active 'openvpn*' 'openvpn-server@*' 2>/dev/null | grep -q '^active' && echo ovpn
ls /etc/wireguard/*.conf >/dev/null 2>&1 && echo wgconf`
	f.WireguardSeen = strings.TrimSpace(c.Sudo(wgScript).Stdout) != ""

	// Real NAT rules (beyond the empty default chains) imply this box routes/masqs.
	natS := c.Sudo("iptables -t nat -S 2>/dev/null").Stdout
	f.NatRules = strings.Contains(natS, "MASQUERADE") ||
		strings.Contains(natS, "-j SNAT") || strings.Contains(natS, "-j DNAT")

	f.Forwarding = f.IPForward || f.DockerSeen || f.WireguardSeen || f.NatRules

	// Active firewall manager (so A1 never imposes a conflicting second layer).
	// Gather the raw signals best-effort, then classify with a pure helper.
	ufwStatus := c.Sudo(`LANG=C ufw status 2>/dev/null | head -1`).Out()
	firewalldActive := c.Run("systemctl is-active firewalld 2>/dev/null").Out() == "active"
	nftablesActive := c.Run("systemctl is-active nftables 2>/dev/null").Out() == "active"
	nftRuleset := strings.TrimSpace(c.Sudo("nft list ruleset 2>/dev/null").Stdout)
	iptablesS := strings.TrimSpace(c.Sudo("iptables -S 2>/dev/null").Stdout)
	iptPersistent := c.Run("dpkg-query -W -f='${Status}' iptables-persistent 2>/dev/null | grep -q 'install ok installed' && echo yes").Out() == "yes"
	f.FirewallMgr = classifyFirewallMgr(ufwStatus, firewalldActive, nftablesActive, nftRuleset, iptablesS, iptPersistent)

	// Active disk swap (anything not zram) the operator may rely on — A6.7 keeps it
	// on a brownfield box instead of swapoff-ing it.
	for line := range strings.SplitSeq(c.Sudo(`swapon --show=NAME,TYPE --noheadings 2>/dev/null`).Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] == "zram" {
			continue
		}
		f.DiskSwap = append(f.DiskSwap, fields[0])
	}

	// Login users with a non-empty authorized_keys (root + every /home/* dir). A2
	// adds these to sshusers so AllowGroups does not lock out an existing key user.
	keyUsersScript := `for d in /root /home/*; do
  [ -d "$d" ] || continue
  ak="$d/.ssh/authorized_keys"
  if [ -s "$ak" ]; then
    [ "$d" = /root ] && echo root || basename "$d"
  fi
done`
	for line := range strings.SplitSeq(c.Sudo(keyUsersScript).Stdout, "\n") {
		if u := strings.TrimSpace(line); u != "" {
			f.SSHKeyUsers = append(f.SSHKeyUsers, u)
		}
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

	f.Inventory = buildInventory(c, f)
	return f
}

// parseListeners parses `ss -tulpnH` output into the deduped/sorted public TCP
// and UDP listening-port sets, the role-agnostic (proto,port,process) service
// records, and the raw foreign-service listener lines. It skips loopback binds
// and the box's own sshd (the ssh port is excluded from Listeners — it is not a
// foreign service — but A1 always also opens cfg.Port).
func parseListeners(ssOut string) (tcp, udp []int, services []ListenService, listeners []string) {
	tcpSet := map[int]bool{}
	udpSet := map[int]bool{}
	seenSvc := map[string]bool{} // dedup ListenServices by "proto:port"
	for line := range strings.SplitSeq(ssOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		proto := fields[0]
		local := fields[4] // Local Address:Port
		if strings.HasPrefix(local, "127.") || strings.HasPrefix(local, "[::1]") {
			continue
		}
		// Parse the port for the coexistence sets even on sshd's line — A1 dedups
		// against cfg.Port, so including 22 here is harmless. sshd is still skipped
		// for the Listeners/Greenfield signal below.
		port, okPort := portFromLocal(local)
		if okPort {
			switch proto {
			case "tcp":
				tcpSet[port] = true
			case "udp":
				udpSet[port] = true
			}
			// Surface every public listening socket (incl sshd) as a service record,
			// deduped by proto:port across the v4/v6 mirror.
			if key := proto + ":" + strconv.Itoa(port); !seenSvc[key] {
				seenSvc[key] = true
				services = append(services, ListenService{
					Proto:   proto,
					Port:    port,
					Process: processFromSS(line),
					PID:     pidFromSS(line),
				})
			}
		}
		// systemd-resolved stub on 127.0.0.53 is loopback — already skipped above.
		if strings.Contains(line, "sshd") || strings.Contains(line, `"ssh"`) {
			continue
		}
		listeners = append(listeners, line)
	}
	sortServices(services)
	return sortedKeys(tcpSet), sortedKeys(udpSet), services, listeners
}

// processFromSS extracts the program name from an ss line's process column, i.e.
// the NAME in `users:(("NAME",pid=123,fd=4))`. Returns "" when ss omitted the
// attribution (no -p privilege, or a kernel socket).
func processFromSS(line string) string {
	i := strings.Index(line, `users:(("`)
	if i < 0 {
		return ""
	}
	rest := line[i+len(`users:(("`):]
	if j := strings.IndexByte(rest, '"'); j >= 0 {
		return rest[:j]
	}
	return ""
}

// pidFromSS extracts the FIRST pid from an ss line's process column, i.e. the N in
// `users:(("NAME",pid=N,fd=M))`. Returns 0 when ss omitted the attribution or the
// pid is unparsable (e.g. a kernel socket reported as pid=0).
func pidFromSS(line string) int {
	i := strings.Index(line, "pid=")
	if i < 0 {
		return 0
	}
	rest := line[i+len("pid="):]
	// The pid runs until the first non-digit (',', ')', etc.).
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	pid, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return pid
}

// sortServices orders service records by proto then port for deterministic
// inventory output and stable tests.
func sortServices(s []ListenService) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Proto != s[j].Proto {
			return s[i].Proto < s[j].Proto
		}
		return s[i].Port < s[j].Port
	})
}

// --- Service origin classification (provenance, NOT a whitelist) ---------------
//
// Each listening socket gets a universal Origin tag derived from the listener pid's
// cgroup (container/systemd provenance) plus a docker ps cross-ref for the container
// name. Everything is best-effort and role-agnostic: a missing /proc, no docker, or
// an unreadable cgroup leaves the tag at "host"/"" and NEVER fails the scan.

// classifyServiceOrigins fills f.ListenServices[i].Origin in place. It batch-reads the
// cgroup of every listener pid in ONE sudo command (pids are ints → injection-safe),
// classifies each via classifyCgroupOrigin, then — when docker is present — upgrades
// "docker" tags to "docker: <name>" by matching the service port to a docker ps
// published port. Pure helpers (classifyCgroupOrigin / dockerPortNames / parseCgroupBatch)
// carry the logic so this wiring stays trivially correct; this function only does I/O.
func classifyServiceOrigins(c *sshx.Client, f *Facts) {
	if len(f.ListenServices) == 0 {
		return
	}
	// Collect the distinct, valid (>0) pids to read.
	pidSet := map[int]bool{}
	for _, s := range f.ListenServices {
		if s.PID > 0 {
			pidSet[s.PID] = true
		}
	}
	cgroups := map[int]string{}
	if len(pidSet) > 0 {
		pids := make([]int, 0, len(pidSet))
		for p := range pidSet {
			pids = append(pids, p)
		}
		sort.Ints(pids)
		var sb strings.Builder
		sb.WriteString("for p in")
		for _, p := range pids {
			sb.WriteString(" ")
			sb.WriteString(strconv.Itoa(p))
		}
		// Emit "<pid>\t<first cgroup line>\n" per pid; missing /proc → empty cgroup col.
		sb.WriteString(`; do printf '%s\t' "$p"; cat /proc/$p/cgroup 2>/dev/null | head -1; printf '\n'; done`)
		cgroups = parseCgroupBatch(c.Sudo(sb.String()).Stdout)
	}

	// Docker container-name map (best-effort, only when docker is present).
	var dockerNames map[int]string
	if f.DockerSeen {
		dockerNames = dockerPortNames(c.Run(`docker ps --format '{{.Names}}\t{{.Ports}}' 2>/dev/null`).Stdout)
	}

	for i := range f.ListenServices {
		s := &f.ListenServices[i]
		origin := classifyCgroupOrigin(cgroups[s.PID])
		if origin == "docker" && dockerNames != nil {
			if name, ok := dockerNames[s.Port]; ok && name != "" {
				origin = "docker: " + name
			}
		}
		s.Origin = origin
	}
}

// parseCgroupBatch parses the "<pid>\t<cgroup line>" output of the batch cgroup read
// into a pid→cgroupPath map. A line with no cgroup column (unreadable /proc) maps the
// pid to "". Malformed lines are skipped.
func parseCgroupBatch(out string) map[int]string {
	m := map[int]string{}
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line[:tab]))
		if err != nil {
			continue
		}
		m[pid] = strings.TrimSpace(line[tab+1:])
	}
	return m
}

// classifyCgroupOrigin maps a /proc/<pid>/cgroup first-line path to a provenance tag,
// handling both cgroup v2 ("0::/system.slice/foo.service") and v1
// ("N:controller:/path"). Container classes win over the generic systemd slice; the
// generic container manager units (docker.service/containerd.service) are normalized
// to "docker" rather than "systemd: docker". An empty/unrecognized path → "host".
func classifyCgroupOrigin(cgroupLine string) string {
	line := strings.TrimSpace(cgroupLine)
	if line == "" {
		return "host"
	}
	// Reduce "N:controllers:/path" (v1) or "0::/path" (v2) to just the path part.
	path := line
	if i := strings.LastIndex(line, ":"); i >= 0 {
		path = line[i+1:]
	}
	low := strings.ToLower(path)

	// Container classes first (conservative substring checks).
	switch {
	case strings.Contains(low, "kubepods"):
		return "k8s"
	case strings.Contains(low, "libpod"):
		return "podman"
	case strings.Contains(low, "/lxc/") || strings.Contains(low, "lxc.payload"):
		return "lxc"
	case strings.Contains(low, "/docker/") || strings.Contains(low, "/moby/") ||
		strings.Contains(low, "containerd-") || strings.Contains(low, "docker-"):
		return "docker"
	}

	// systemd unit slice: /system.slice/<unit>.service (possibly nested). Take the
	// LAST path segment ending in ".service".
	if unit, ok := systemdUnitFromPath(path); ok {
		// The generic container-manager units are really "docker" provenance.
		if unit == "docker" || unit == "containerd" {
			return "docker"
		}
		return "systemd: " + unit
	}

	// /init.scope, /user.slice, root "/", anything else → host.
	return "host"
}

// systemdUnitFromPath returns the systemd unit name (without ".service") from a cgroup
// path's last ".service" segment, e.g. "/system.slice/postgresql.service" → "postgresql".
// ok=false when no ".service" segment is present.
func systemdUnitFromPath(path string) (string, bool) {
	segs := strings.Split(path, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		seg := segs[i]
		if unit, ok := strings.CutSuffix(seg, ".service"); ok && unit != "" {
			return unit, true
		}
	}
	return "", false
}

// dockerPortNames parses `docker ps --format '{{.Names}}\t{{.Ports}}'` output into a
// published-port → container-name map. The Ports column lists comma-separated mappings
// like "0.0.0.0:443->443/tcp, :::443->443/tcp, 0.0.0.0:8000-8001->8000-8001/tcp"; it
// records the HOST published port (left of "->", after the last ":") for each, and
// expands "A-B" host ranges. Bare exposed ports (no "->") are ignored — they are not
// published. Best-effort: malformed entries are skipped.
func dockerPortNames(psOut string) map[int]string {
	m := map[int]string{}
	for line := range strings.SplitSeq(psOut, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		name := strings.TrimSpace(line[:tab])
		ports := line[tab+1:]
		if name == "" {
			continue
		}
		for _, mapping := range strings.Split(ports, ",") {
			mapping = strings.TrimSpace(mapping)
			arrow := strings.Index(mapping, "->")
			if arrow < 0 {
				continue // exposed-only, not published
			}
			hostPart := mapping[:arrow] // e.g. "0.0.0.0:8000-8001" or ":::443" or "[::]:443"
			// Host published port(s) are after the LAST ':'.
			colon := strings.LastIndex(hostPart, ":")
			if colon < 0 {
				continue
			}
			portTok := hostPart[colon+1:]
			for _, p := range expandPortRange(portTok) {
				if _, exists := m[p]; !exists {
					m[p] = name
				}
			}
		}
	}
	return m
}

// expandPortRange turns a "PORT" or "A-B" token into its list of ints. Invalid tokens
// yield nil. A descending or oversized range is bounded by simple sanity checks.
func expandPortRange(tok string) []int {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return nil
	}
	if dash := strings.IndexByte(tok, '-'); dash >= 0 {
		lo, err1 := strconv.Atoi(strings.TrimSpace(tok[:dash]))
		hi, err2 := strconv.Atoi(strings.TrimSpace(tok[dash+1:]))
		if err1 != nil || err2 != nil || lo <= 0 || hi < lo || hi-lo > 1024 {
			return nil
		}
		out := make([]int, 0, hi-lo+1)
		for p := lo; p <= hi; p++ {
			out = append(out, p)
		}
		return out
	}
	p, err := strconv.Atoi(tok)
	if err != nil || p <= 0 {
		return nil
	}
	return []int{p}
}

// classifyFirewallMgr resolves the active firewall manager from the raw probe
// signals, in priority order (§A). It is conservative about nftables: a native
// nftables verdict requires the nftables unit active, a non-empty nft ruleset,
// AND an effectively-empty iptables ruleset — otherwise (e.g. a docker box whose
// rules show up under iptables-nft) it falls through to "iptables". A false
// negative here is safe (we apply the proven iptables coexist build); a false
// positive would wrongly make A1 defer on a box we should harden.
func classifyFirewallMgr(ufwStatusLine string, firewalldActive, nftablesActive bool, nftRuleset, iptablesS string, iptPersistent bool) string {
	if strings.TrimSpace(ufwStatusLine) == "Status: active" {
		return "ufw"
	}
	if firewalldActive {
		return "firewalld"
	}
	if nftablesActive && strings.TrimSpace(nftRuleset) != "" && iptablesEffectivelyEmpty(iptablesS) {
		return "nftables"
	}
	if !iptablesEffectivelyEmpty(iptablesS) || iptPersistent {
		return "iptables"
	}
	return "none"
}

// iptablesEffectivelyEmpty reports whether an `iptables -S` dump carries no rule
// beyond the three default-ACCEPT chain policies. A fresh box prints exactly
// `-P INPUT ACCEPT` / `-P FORWARD ACCEPT` / `-P OUTPUT ACCEPT`; anything else
// (an appended `-A ...` rule, or a non-ACCEPT policy) means iptables is in use.
func iptablesEffectivelyEmpty(iptablesS string) bool {
	for line := range strings.SplitSeq(iptablesS, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch line {
		case "-P INPUT ACCEPT", "-P FORWARD ACCEPT", "-P OUTPUT ACCEPT":
			continue // default chain policy — not a real rule
		default:
			return false
		}
	}
	return true
}

// portFromLocal extracts the port from an `ss` Local Address:Port field. It
// handles `0.0.0.0:8080`, `[::]:8080`, `192.168.0.1:53`, `*:80`, and bracketed
// IPv6 forms by taking the substring after the LAST colon. Returns false when no
// numeric port can be parsed (the caller then skips the entry).
func portFromLocal(local string) (int, bool) {
	i := strings.LastIndex(local, ":")
	if i < 0 || i == len(local)-1 {
		return 0, false
	}
	p, err := strconv.Atoi(local[i+1:])
	if err != nil || p <= 0 || p > 65535 {
		return 0, false
	}
	return p, true
}

// sortedKeys returns the map keys as an ascending-sorted slice (deterministic
// port order for the ruleset and for tests).
func sortedKeys(m map[int]bool) []int {
	if len(m) == 0 {
		return nil
	}
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// buildInventory dumps the full §0.5 read-only probe set for the on-box record,
// then appends the parsed coexistence summary so /root/vps-inventory.md documents
// exactly what the coexistence steps will preserve.
func buildInventory(c *sshx.Client, f *Facts) string {
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
	return c.Sudo(script).Stdout + coexistSummary(f)
}

// coexistSummary renders the parsed coexistence facts as a markdown block so the
// on-box inventory records what the coexistence steps will keep open/preserve.
func coexistSummary(f *Facts) string {
	mode := "greenfield (fresh box — universal baseline applies)"
	if !f.Greenfield {
		mode = "brownfield (coexistence mode — existing services preserved)"
	}
	var b strings.Builder
	b.WriteString("\n=== coexistence (parsed) ===\n")
	fmt.Fprintf(&b, "mode: %s\n", mode)
	fmt.Fprintf(&b, "firewall manager: %s\n", fwMgrLabel(f.FirewallMgr))
	fmt.Fprintf(&b, "tcp ports kept open: %v\n", f.ListenPortsTCP)
	fmt.Fprintf(&b, "udp ports kept open: %v\n", f.ListenPortsUDP)
	fmt.Fprintf(&b, "forwarding: %v (ip_forward=%v docker=%v wireguard=%v nat=%v)\n",
		f.Forwarding, f.IPForward, f.DockerSeen, f.WireguardSeen, f.NatRules)
	fmt.Fprintf(&b, "disk swap preserved: %v\n", f.DiskSwap)
	fmt.Fprintf(&b, "ssh key users (added to sshusers): %v\n", f.SSHKeyUsers)

	// Role-agnostic "what's found" table: observed listening sockets, their owning
	// process, and the universal provenance origin tag, so the operator can read the
	// box's de-facto role.
	b.WriteString("\ndetected services (proto port -> process [origin]):\n")
	if len(f.ListenServices) == 0 {
		b.WriteString("  (none observed)\n")
	} else {
		for _, s := range f.ListenServices {
			proc := s.Process
			if proc == "" {
				proc = "(unknown)"
			}
			origin := s.Origin
			if origin == "" {
				origin = "host"
			}
			fmt.Fprintf(&b, "  %-3s %-6d -> %s [%s]\n", s.Proto, s.Port, proc, origin)
		}
	}
	return b.String()
}

// fwMgrLabel renders the FirewallMgr verdict with a short note on what A1 does
// for it, for the human-readable inventory.
func fwMgrLabel(mgr string) string {
	switch mgr {
	case "ufw":
		return "ufw (A1 allows SSH + detected ports via ufw; no policy change)"
	case "firewalld":
		return "firewalld (A1 leaves it untouched — operator owns the firewall)"
	case "nftables":
		return "nftables (A1 leaves it untouched — operator owns the firewall)"
	case "iptables":
		return "iptables (A1 applies the INPUT-DROP coexist build)"
	case "none":
		return "none (A1 applies the INPUT-DROP coexist build)"
	default:
		return mgr
	}
}
