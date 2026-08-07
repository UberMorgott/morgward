package steps

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/UberMorgott/morgward/internal/detect"
)

// A1Firewall implements §A1: iptables-nft INPUT/FORWARD lockdown with a
// systemd-run fail-safe timer, IPv6 mirror, and iptables-persistent. Greenfield
// applies the SSH-only fresh build (INPUT+FORWARD DROP); brownfield applies a
// coexistence build that ALSO opens every detected service port and leaves the
// FORWARD policy + chains entirely untouched, never flushing the existing
// docker/VPN rules (see coexistRuleset).
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

// dportOpen reports whether an iptables ruleset contains a rule for the exact
// destination port. The anchor `( |$)` (a space OR end-of-line) prevents a
// superset-prefix false match — e.g. a `--dport 2222` rule must NOT satisfy a
// query for port 22, and `--dport 80` must NOT match a `--dport 8080` rule.
// `(?m)` makes `$` match per-line end so a port at the end of any rule line counts.
func dportOpen(ruleset string, port int) bool {
	ok, _ := regexp.MatchString(fmt.Sprintf("(?m)--dport %d( |$)", port), ruleset)
	return ok
}

// allPortsOpenAndPersisted reports whether every port A1 would open is ALREADY
// present (anchored, boundary-correct) in BOTH the live `iptables -S` (cur) and the
// persisted rules.v4 (pers). Greenfield only opens SSH :cfg.Port, so it checks just
// that port. Brownfield ALSO opens every currently-detected ListenPortsTCP/UDP
// (deduped against cfg.Port, mirroring coexistRuleset's `p == port` skip), so the
// skip-if only fires when the persisted+live set is a SUPERSET of what we'd add —
// the moment a NEW listener appears it returns false and the run re-opens the gap.
func allPortsOpenAndPersisted(cur, pers string, port int, f *detect.Facts) bool {
	openAndPersisted := func(p int) bool {
		return dportOpen(cur, p) && dportOpen(pers, p)
	}
	if !openAndPersisted(port) {
		return false
	}
	if f == nil || f.Greenfield {
		return true
	}
	for _, p := range f.ListenPortsTCP {
		if p == port {
			continue // SSH port already checked above
		}
		if !openAndPersisted(p) {
			return false
		}
	}
	for _, p := range f.ListenPortsUDP {
		if p == port {
			continue
		}
		if !openAndPersisted(p) {
			return false
		}
	}
	return true
}

func (a A1Firewall) Run(ctx *Context) (Status, string, error) {
	port := ctx.Cfg.Port

	// Firewall-manager-aware dispatch (brownfield only). Greenfield is always a
	// fresh box, so it takes the iptables build below regardless of FirewallMgr.
	// On a brownfield box we must not impose a conflicting second layer over the
	// operator's chosen manager:
	//   - ufw active           → make access explicit via `ufw allow` (additive).
	//   - firewalld / nftables → leave it untouched (StatusSkip, not an error).
	//   - iptables / none      → fall through to the round-1 INPUT-DROP build.
	if !ctx.Facts.Greenfield {
		switch ctx.Facts.FirewallMgr {
		case "ufw":
			return a.runUFW(ctx, port)
		case "firewalld", "nftables":
			return StatusSkip, fmt.Sprintf(
				"%s manages the firewall — morgward left it untouched; ensure SSH :%d and your service ports stay allowed there (see docs/BROWNFIELD.md §firewall-managers)",
				ctx.Facts.FirewallMgr, port), nil
		}
	}

	// Skip-if: INPUT DROP in place AND every port we would open is already open in
	// the live ruleset AND persisted. Read both the live `iptables -S` and the
	// persisted rules.v4 once, then check per-port presence with the SAME anchored
	// dportOpen match so the check is boundary-correct (port 22 must not be satisfied
	// by a 2222 rule, etc.). Greenfield only needs SSH :cfg.Port; brownfield ALSO
	// needs every currently-detected service port, so a re-run after a NEW listener
	// appears does NOT skip — it falls through and appends the missing port(s)
	// (docs/BROWNFIELD.md §8 "re-run step A1 … re-opens them").
	cur := ctx.Cli.Sudo("iptables -S 2>/dev/null").Stdout
	pers := ctx.Cli.Sudo("cat /etc/iptables/rules.v4 2>/dev/null").Stdout
	if strings.Contains(cur, "-P INPUT DROP") &&
		allPortsOpenAndPersisted(cur, pers, port, ctx.Facts) {
		return StatusSkip, "firewall already closed with SSH open and persisted", nil
	}

	// 1. Snapshot + rollback script + arm the 300s fail-safe timer.
	arm := fmt.Sprintf(`set -e
iptables-save  > /root/iptables-backup.v4
ip6tables-save > /root/iptables-backup.v6
%s
chmod +x /usr/local/sbin/fw-rollback.sh
%s`, putFile("/usr/local/sbin/fw-rollback.sh", fwRollbackScript, "0755"),
		armTimer("fw-rollback", "/usr/local/sbin/fw-rollback.sh"))
	if r := ctx.Cli.Sudo(arm); r.RC != 0 {
		return StatusFail, "failed to arm fail-safe: " + firstLine(r.Stderr), fmt.Errorf("arm fail-safe failed")
	}
	ctx.Log.Detail("fail-safe armed: fw-rollback timer fires in 300s if not disarmed")

	// 2. Build the ruleset. Greenfield gets the deterministic fresh build (SSH-only,
	// INPUT+FORWARD DROP); brownfield gets the coexistence build that ALSO opens
	// every detected service port and leaves the FORWARD policy + chains entirely
	// untouched, never flushing the existing docker/VPN rules.
	build := greenfieldRuleset(port)
	if !ctx.Facts.Greenfield {
		build = coexistRuleset(port, ctx.Facts.ListenPortsTCP, ctx.Facts.ListenPortsUDP)
	}
	if r := ctx.Cli.Sudo(build); r.RC != 0 {
		ctx.Cli.Sudo("/usr/local/sbin/fw-rollback.sh") // immediate self-heal
		return StatusFail, "ruleset build failed, rolled back: " + firstLine(r.Stderr), fmt.Errorf("firewall build failed")
	}

	// 3. Install persistence package (debconf decline auto-save; finalize later).
	persistInstall := `export DEBIAN_FRONTEND=noninteractive
echo 'iptables-persistent iptables-persistent/autosave_v4 boolean false' | debconf-set-selections
echo 'iptables-persistent iptables-persistent/autosave_v6 boolean false' | debconf-set-selections
` + aptInstall("iptables-persistent")
	persistInstallFailed := false
	if r := ctx.Cli.Sudo(persistInstall); r.RC != 0 {
		persistInstallFailed = true
		ctx.Log.Warn("iptables-persistent install rc=%d (continuing to verify)", r.RC)
	}

	// 4. Second-session [LOCAL] verify BEFORE disarming.
	ctx.Log.Detail("verifying reachability from an independent session…")
	if err := freshLogin(ctx, ctx.Cli.User); err != nil {
		ctx.Cli.Sudo("/usr/local/sbin/fw-rollback.sh")
		return StatusFail, "second-session verify FAILED, firewall rolled back: " + err.Error(), fmt.Errorf("firewall locked out the session")
	}

	// 5. Disarm timer + persist.
	ctx.Cli.Sudo(disarmTimer("fw-rollback") + "netfilter-persistent save >/dev/null 2>&1\n")

	// Confirm disarmed + persisted opens the SSH port.
	timers := ctx.Cli.Sudo("systemctl list-timers --all 2>/dev/null | grep fw-rollback || true").Out()
	pchk := ctx.Cli.Sudo(fmt.Sprintf(`grep -Eq -- '--dport %d( |$)' /etc/iptables/rules.v4 && echo ok`, port)).Out()
	if timers != "" {
		ctx.Log.Warn("fw-rollback timer still listed after disarm")
	}
	if pchk != "ok" {
		// Name the ACTUAL cause instead of a generic "not persisted": when the package
		// is genuinely absent, `netfilter-persistent save` never ran, so rules.v4 does
		// not exist and A8's pre-reboot gate would later abort with a far more cryptic
		// message. Say it here, at the step that owns it.
		detail := "firewall not persisted with SSH port open in rules.v4"
		if persistInstallFailed && ctx.Cli.Sudo(pkgInstalledCheck("iptables-persistent")).Out() != "yes" {
			detail = "iptables-persistent could not be installed (no install candidate for this release, or the apt/dpkg lock was held) — " +
				"firewall rules are LIVE but would NOT survive a reboot"
		}
		return StatusFail, detail, fmt.Errorf("persistence incomplete")
	}

	if ctx.Facts.Greenfield {
		return StatusOK, fmt.Sprintf("INPUT DROP with SSH :%d open, v6 mirrored, persisted, fail-safe disarmed", port), nil
	}
	nPorts := countServicePorts(port, ctx.Facts.ListenPortsTCP, ctx.Facts.ListenPortsUDP)
	return StatusOK, fmt.Sprintf(
		"coexist: INPUT DROP, SSH :%d + %d service port(s) kept open, FORWARD preserved, v6 mirrored, persisted",
		port, nPorts), nil
}

// runUFW is the brownfield path for a box whose firewall is managed by ufw. ufw
// owns the raw iptables policy and already defaults incoming=deny, so morgward
// must NOT touch raw iptables / netfilter-persistent / the ufw default policy /
// enable-disable. Instead it makes the detected access EXPLICIT with `ufw allow`,
// which is idempotent + additive and therefore can never lock anyone out. A
// fresh-session verify is cheap insurance. No fw-rollback timer (purely additive).
func (a A1Firewall) runUFW(ctx *Context, port int) (Status, string, error) {
	tcp, udp := ctx.Facts.ListenPortsTCP, ctx.Facts.ListenPortsUDP

	// Light skip-if: if `ufw status` already lists SSH + every detected port, there
	// is nothing to add. ufw prints rules as "<port>/<proto>" (and "<port>" for the
	// "to any port" form); we check the conservative "<port>/<proto>" token.
	status := ctx.Cli.Sudo("LANG=C ufw status 2>/dev/null").Stdout
	if ufwAlreadyAllows(status, port, tcp, udp) {
		return StatusSkip, fmt.Sprintf("ufw-managed: SSH :%d + all detected service ports already allowed in ufw", port), nil
	}

	if r := ctx.Cli.Sudo(ufwAllowScript(port, tcp, udp)); r.RC != 0 {
		return StatusFail, "ufw allow failed: " + firstLine(r.Stderr), fmt.Errorf("ufw allow failed")
	}

	// Cheap insurance: prove the session still reaches us from an independent login.
	ctx.Log.Detail("verifying reachability from an independent session…")
	if err := freshLogin(ctx, ctx.Cli.User); err != nil {
		return StatusFail, "second-session verify FAILED after ufw allow: " + err.Error(), fmt.Errorf("ufw allow locked out the session")
	}

	nPorts := countServicePorts(port, tcp, udp)
	return StatusOK, fmt.Sprintf(
		"ufw-managed: SSH :%d + %d service port(s) allowed via ufw (firewall left under ufw control)",
		port, nPorts), nil
}

// ufwAllowScript builds the additive `ufw allow` sequence: SSH first, then each
// detected TCP and UDP service port (deduped against the SSH port). Every line is
// idempotent (`ufw allow` is a no-op when the rule already exists) and only ever
// GRANTS access — it changes no default policy and never enables/disables ufw.
// Ports are integers from detect, so the interpolation is injection-safe.
func ufwAllowScript(port int, tcp, udp []int) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	fmt.Fprintf(&b, "ufw allow %d/tcp\n", port)
	for _, p := range tcp {
		if p == port {
			continue // SSH already allowed above
		}
		fmt.Fprintf(&b, "ufw allow %d/tcp\n", p)
	}
	for _, p := range udp {
		if p == port {
			continue
		}
		fmt.Fprintf(&b, "ufw allow %d/udp\n", p)
	}
	return b.String()
}

// ufwAlreadyAllows reports whether `ufw status` output already carries an allow
// for SSH and every detected service port (as the "<port>/<proto>" token). A
// conservative check: a partial match returns false so the allow script runs and
// idempotently tops up the missing rules.
func ufwAlreadyAllows(status string, port int, tcp, udp []int) bool {
	if status == "" {
		return false
	}
	need := []string{fmt.Sprintf("%d/tcp", port)}
	for _, p := range tcp {
		if p != port {
			need = append(need, fmt.Sprintf("%d/tcp", p))
		}
	}
	for _, p := range udp {
		if p != port {
			need = append(need, fmt.Sprintf("%d/udp", p))
		}
	}
	for _, tok := range need {
		if !strings.Contains(status, tok) {
			return false
		}
	}
	return true
}

// countServicePorts returns how many DISTINCT non-SSH service ports coexistRuleset
// actually opens via its service ACCEPT loops — i.e. every tcp and udp port except
// cfg.Port (which is opened once via the SSH ACCEPT, never in the service loop). It
// mirrors the same `p == port` skip the ruleset builder uses, so the reported count
// matches the number of non-SSH ACCEPT rules emitted.
func countServicePorts(port int, tcp, udp []int) int {
	n := 0
	for _, p := range tcp {
		if p != port {
			n++
		}
	}
	for _, p := range udp {
		if p != port {
			n++
		}
	}
	return n
}

// greenfieldRuleset is the deterministic fresh build: SSH-only INPUT, INPUT and
// FORWARD DROP, v4 + v6 mirror. Byte-identical to the original A1 ruleset —
// pinned by TestGreenfieldRulesetGolden.
func greenfieldRuleset(port int) string {
	return inputRuleset(port, nil, nil, true)
}

// coexistRuleset is the brownfield service-preserving build. Same base hygiene as
// greenfield (lo / conntrack / ICMPv6) but ALSO opens every detected public TCP
// and UDP service port (deduped against the SSH port) on both v4 and v6. It
// deliberately emits NO `-P FORWARD` line: the existing FORWARD policy and chains
// are left entirely untouched so docker's own `-P FORWARD DROP` + DOCKER-FORWARD
// rules and a router's bespoke FORWARD ACCEPT rules both survive intact (forcing a
// policy here would either be re-asserted by docker on boot or loosen the
// isolation the operator chose). It only APPENDS to INPUT — the docker
// DOCKER/DOCKER-USER chains and the nat table are never flushed. The port slices
// are integers from detect, so the interpolation is injection-safe.
func coexistRuleset(port int, tcp, udp []int) string {
	return inputRuleset(port, tcp, udp, false)
}

// ipFamily is one address family's fixed part of the INPUT ruleset: the binary,
// the section comment (greenfield vs coexistence wording), and the base-hygiene
// prologue. The prologue ORDER differs between families on purpose — v6 accepts
// ICMPv6 NDP before the INVALID drop — so it is spelled out per family rather
// than generated.
type ipFamily struct{ bin, header, coexistHeader, prologue string }

var ipFamilies = []ipFamily{
	{
		bin:           "iptables",
		header:        "# --- IPv4 ---",
		coexistHeader: "# --- IPv4 (coexistence) ---",
		prologue: `iptables -A INPUT -i lo -j ACCEPT
iptables -A INPUT -m conntrack --ctstate INVALID -j DROP
iptables -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
`,
	},
	{
		bin:           "ip6tables",
		header:        "# --- IPv6 mirror (ICMPv6 NDP before INVALID drop) ---",
		coexistHeader: "# --- IPv6 mirror (coexistence) ---",
		prologue: `ip6tables -A INPUT -i lo -j ACCEPT
ip6tables -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
for t in 1 2 3 4 133 134 135 136; do ip6tables -A INPUT -p ipv6-icmp --icmpv6-type $t -j ACCEPT; done
ip6tables -A INPUT -m conntrack --ctstate INVALID -j DROP
`,
	},
}

// inputRuleset emits the shared INPUT lockdown for both families: base hygiene,
// the SSH ACCEPT, the detected service ACCEPTs (deduped against the SSH port),
// and `-P INPUT DROP`. greenfield ALSO sets `-P FORWARD DROP` per family;
// brownfield emits no FORWARD line at all (see coexistRuleset's doc for why).
// Both callers' output is pinned byte-for-byte by the goldens in
// a1_firewall_test.go — comment text included.
func inputRuleset(port int, tcp, udp []int, greenfield bool) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	for _, f := range ipFamilies {
		header := f.coexistHeader
		if greenfield {
			header = f.header
		}
		b.WriteString(header + "\n")
		b.WriteString(f.prologue)
		fmt.Fprintf(&b, "%s -A INPUT -p tcp --dport %d -m conntrack --ctstate NEW -j ACCEPT\n", f.bin, port)
		for _, p := range tcp {
			if p == port {
				continue // SSH port already opened above
			}
			fmt.Fprintf(&b, "%s -A INPUT -p tcp --dport %d -m conntrack --ctstate NEW -j ACCEPT\n", f.bin, p)
		}
		for _, p := range udp {
			if p == port {
				continue
			}
			fmt.Fprintf(&b, "%s -A INPUT -p udp --dport %d -j ACCEPT\n", f.bin, p)
		}
		fmt.Fprintf(&b, "%s -P INPUT DROP\n", f.bin)
		if greenfield {
			fmt.Fprintf(&b, "%s -P FORWARD DROP\n", f.bin)
		}
		// Brownfield: FORWARD policy/chains deliberately left untouched.
	}
	return b.String()
}
