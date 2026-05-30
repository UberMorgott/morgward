package steps

import (
	"fmt"
	"strings"
)

// A1Firewall implements §A1: iptables-nft INPUT/FORWARD lockdown with a
// systemd-run fail-safe timer, IPv6 mirror, and iptables-persistent. Greenfield
// fresh-build path only; brownfield is gated at pre-flight.
type A1Firewall struct{}

func (A1Firewall) ID() string    { return "A1" }
func (A1Firewall) Title() string { return "Firewall + fail-safe (iptables-nft, v4+v6)" }

const fwRollbackScript = `#!/bin/sh
iptables  -P INPUT ACCEPT; iptables  -P FORWARD ACCEPT; iptables  -P OUTPUT ACCEPT
ip6tables -P INPUT ACCEPT; ip6tables -P FORWARD ACCEPT; ip6tables -P OUTPUT ACCEPT
iptables -F; iptables -X; ip6tables -F; ip6tables -X
iptables-restore  < /root/iptables-backup.v4
ip6tables-restore < /root/iptables-backup.v6
`

func (a A1Firewall) Run(ctx *Context) (Status, string, error) {
	port := ctx.Cfg.Port

	// Skip-if: SSH ACCEPT before DROP already present AND persisted.
	cur := ctx.Cli.Sudo("iptables -S 2>/dev/null").Stdout
	persisted := ctx.Cli.Sudo(fmt.Sprintf(`grep -q -- "--dport %d" /etc/iptables/rules.v4 2>/dev/null && echo yes`, port)).Out()
	if strings.Contains(cur, "-P INPUT DROP") &&
		strings.Contains(cur, fmt.Sprintf("--dport %d", port)) && persisted == "yes" {
		return StatusSkip, "firewall already closed with SSH open and persisted", nil
	}

	if !ctx.Facts.Greenfield {
		// Protect the session, then refuse to auto-merge an unknown ruleset.
		ctx.Cli.Sudo(fmt.Sprintf("iptables -C INPUT -p tcp --dport %d -j ACCEPT 2>/dev/null || iptables -I INPUT 1 -p tcp --dport %d -j ACCEPT", port, port))
		return StatusFail, "brownfield box: SSH ACCEPT inserted, NOT auto-merging existing firewall — adapt manually per §0.5", fmt.Errorf("brownfield firewall needs manual adaptation")
	}

	// 1. Snapshot + rollback script + arm the 300s fail-safe timer.
	arm := fmt.Sprintf(`set -e
iptables-save  > /root/iptables-backup.v4
ip6tables-save > /root/iptables-backup.v6
%s
chmod +x /usr/local/sbin/fw-rollback.sh
systemctl stop fw-rollback.timer 2>/dev/null || true
systemctl reset-failed 'fw-rollback.*' 2>/dev/null || true
systemd-run --on-active=300 --unit=fw-rollback /usr/local/sbin/fw-rollback.sh
`, putFile("/usr/local/sbin/fw-rollback.sh", fwRollbackScript, "0755"))
	if r := ctx.Cli.Sudo(arm); r.RC != 0 {
		return StatusFail, "failed to arm fail-safe: " + firstLine(r.Stderr), fmt.Errorf("arm fail-safe failed")
	}
	ctx.Log.Detail("fail-safe armed: fw-rollback timer fires in 300s if not disarmed")

	// 2. Build the deterministic fresh ruleset (v4 + v6 mirror).
	build := fmt.Sprintf(`set -e
# --- IPv4 ---
iptables -A INPUT -i lo -j ACCEPT
iptables -A INPUT -m conntrack --ctstate INVALID -j DROP
iptables -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A INPUT -p tcp --dport %[1]d -m conntrack --ctstate NEW -j ACCEPT
iptables -P INPUT DROP
iptables -P FORWARD DROP
# --- IPv6 mirror (ICMPv6 NDP before INVALID drop) ---
ip6tables -A INPUT -i lo -j ACCEPT
ip6tables -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
for t in 1 2 3 4 133 134 135 136; do ip6tables -A INPUT -p ipv6-icmp --icmpv6-type $t -j ACCEPT; done
ip6tables -A INPUT -m conntrack --ctstate INVALID -j DROP
ip6tables -A INPUT -p tcp --dport %[1]d -m conntrack --ctstate NEW -j ACCEPT
ip6tables -P INPUT DROP
ip6tables -P FORWARD DROP
`, port)
	if r := ctx.Cli.Sudo(build); r.RC != 0 {
		ctx.Cli.Sudo("/usr/local/sbin/fw-rollback.sh") // immediate self-heal
		return StatusFail, "ruleset build failed, rolled back: " + firstLine(r.Stderr), fmt.Errorf("firewall build failed")
	}

	// 3. Install persistence package (debconf decline auto-save; finalize later).
	persistInstall := `export DEBIAN_FRONTEND=noninteractive
echo 'iptables-persistent iptables-persistent/autosave_v4 boolean false' | debconf-set-selections
echo 'iptables-persistent iptables-persistent/autosave_v6 boolean false' | debconf-set-selections
stdbuf -oL -eL apt-get install -y iptables-persistent
`
	if r := ctx.Cli.Sudo(persistInstall); r.RC != 0 {
		ctx.Log.Warn("iptables-persistent install rc=%d (continuing to verify)", r.RC)
	}

	// 4. Second-session [LOCAL] verify BEFORE disarming.
	ctx.Log.Detail("verifying reachability from an independent session…")
	if err := freshLogin(ctx, ctx.Cli.User); err != nil {
		ctx.Cli.Sudo("/usr/local/sbin/fw-rollback.sh")
		return StatusFail, "second-session verify FAILED, firewall rolled back: " + err.Error(), fmt.Errorf("firewall locked out the session")
	}

	// 5. Disarm timer + persist.
	disarm := `systemctl stop fw-rollback.timer 2>/dev/null || true
systemctl reset-failed fw-rollback.timer fw-rollback.service 2>/dev/null || true
netfilter-persistent save >/dev/null 2>&1
`
	ctx.Cli.Sudo(disarm)

	// Confirm disarmed + persisted opens the SSH port.
	timers := ctx.Cli.Sudo("systemctl list-timers --all 2>/dev/null | grep fw-rollback || true").Out()
	pchk := ctx.Cli.Sudo(fmt.Sprintf(`grep -q -- "--dport %d" /etc/iptables/rules.v4 && echo ok`, port)).Out()
	if timers != "" {
		ctx.Log.Warn("fw-rollback timer still listed after disarm")
	}
	if pchk != "ok" {
		return StatusFail, "firewall not persisted with SSH port open in rules.v4", fmt.Errorf("persistence incomplete")
	}

	return StatusOK, fmt.Sprintf("INPUT DROP with SSH :%d open, v6 mirrored, persisted, fail-safe disarmed", port), nil
}
