package steps

import (
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/detect"
)

// TestAllPortsOpenAndPersistedGreenfield: greenfield only needs SSH :cfg.Port in
// both the live ruleset and rules.v4 — service ports are irrelevant.
func TestAllPortsOpenAndPersistedGreenfield(t *testing.T) {
	live := "-P INPUT DROP\n-A INPUT -p tcp --dport 22 -m conntrack --ctstate NEW -j ACCEPT"
	pers := "-A INPUT -p tcp --dport 22 -m conntrack --ctstate NEW -j ACCEPT"
	f := &detect.Facts{Greenfield: true}
	if !allPortsOpenAndPersisted(live, pers, 22, f) {
		t.Error("greenfield with SSH :22 open+persisted should skip (true)")
	}
	// SSH missing from persisted ⇒ must not skip.
	if allPortsOpenAndPersisted(live, "", 22, f) {
		t.Error("greenfield with SSH not persisted should not skip (false)")
	}
}

// TestAllPortsOpenAndPersistedBrownfieldRerun is the FA-0007 regression: a
// brownfield re-run after a NEW listener appears must NOT skip — every detected
// port has to be open+persisted before the skip-if fires, so a freshly-listening
// port (9099 here, not yet in the ruleset) forces a fall-through that re-opens it.
func TestAllPortsOpenAndPersistedBrownfieldRerun(t *testing.T) {
	// Round-1 ruleset: SSH + 8080 open & persisted; the operator later starts a 9099
	// listener that detect now reports but the firewall has not opened yet.
	live := "-P INPUT DROP\n" +
		"-A INPUT -p tcp --dport 22 -m conntrack --ctstate NEW -j ACCEPT\n" +
		"-A INPUT -p tcp --dport 8080 -m conntrack --ctstate NEW -j ACCEPT"
	pers := live
	f := &detect.Facts{
		Greenfield:     false,
		ListenPortsTCP: []int{22, 8080, 9099},
	}
	if allPortsOpenAndPersisted(live, pers, 22, f) {
		t.Error("brownfield re-run with a NEW detected port (9099) must NOT skip (want false)")
	}

	// Once 9099 is open AND persisted, the skip-if may fire again (idempotent re-run).
	live2 := live + "\n-A INPUT -p tcp --dport 9099 -m conntrack --ctstate NEW -j ACCEPT"
	pers2 := live2
	if !allPortsOpenAndPersisted(live2, pers2, 22, f) {
		t.Error("brownfield with every detected port open+persisted should skip (want true)")
	}
}

// TestAllPortsOpenAndPersistedPersistGap: a port open live but NOT in rules.v4 must
// not skip — otherwise a reboot would drop it.
func TestAllPortsOpenAndPersistedPersistGap(t *testing.T) {
	live := "-P INPUT DROP\n" +
		"-A INPUT -p tcp --dport 22 -m conntrack --ctstate NEW -j ACCEPT\n" +
		"-A INPUT -p udp --dport 51820 -j ACCEPT"
	pers := "-A INPUT -p tcp --dport 22 -m conntrack --ctstate NEW -j ACCEPT" // 51820 not persisted
	f := &detect.Facts{Greenfield: false, ListenPortsUDP: []int{51820}}
	if allPortsOpenAndPersisted(live, pers, 22, f) {
		t.Error("a detected UDP port open live but not persisted must NOT skip (want false)")
	}
}

// TestAllPortsOpenAndPersistedBoundary guards that the anchored dportOpen match is
// used: a 2222 rule must not satisfy a check for SSH :22.
func TestAllPortsOpenAndPersistedBoundary(t *testing.T) {
	live := "-P INPUT DROP\n-A INPUT -p tcp --dport 2222 -m conntrack --ctstate NEW -j ACCEPT"
	pers := live
	f := &detect.Facts{Greenfield: true}
	if allPortsOpenAndPersisted(live, pers, 22, f) {
		t.Error("a 2222 rule must not satisfy the SSH :22 presence check (want false)")
	}
}

// TestGreenfieldRulesetUnchanged pins the greenfield build to its exact original
// shape: SSH-only INPUT, INPUT+FORWARD DROP, no per-service port openings.
func TestGreenfieldRulesetUnchanged(t *testing.T) {
	got := greenfieldRuleset(22)
	for _, want := range []string{
		"iptables -A INPUT -p tcp --dport 22 -m conntrack --ctstate NEW -j ACCEPT",
		"iptables -P INPUT DROP",
		"iptables -P FORWARD DROP",
		"ip6tables -P FORWARD DROP",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("greenfield ruleset missing %q\n---\n%s", want, got)
		}
	}
	// Greenfield must NOT open any non-SSH port nor ever ACCEPT FORWARD.
	if strings.Contains(got, "--dport 8080") {
		t.Errorf("greenfield ruleset must not open service ports:\n%s", got)
	}
	if strings.Contains(got, "FORWARD ACCEPT") {
		t.Errorf("greenfield ruleset must never ACCEPT FORWARD:\n%s", got)
	}
}

// TestCoexistRulesetOpensPorts asserts the brownfield build opens SSH + every
// detected TCP/UDP service port on v4 and v6, locks INPUT, and dedups the SSH
// port.
func TestCoexistRulesetOpensPorts(t *testing.T) {
	got := coexistRuleset(22, []int{22, 8080, 8443}, []int{51820})

	mustContain := []string{
		// SSH on v4 + v6
		"iptables -A INPUT -p tcp --dport 22 -m conntrack --ctstate NEW -j ACCEPT",
		"ip6tables -A INPUT -p tcp --dport 22 -m conntrack --ctstate NEW -j ACCEPT",
		// detected TCP service ports, v4 + v6
		"iptables -A INPUT -p tcp --dport 8080 -m conntrack --ctstate NEW -j ACCEPT",
		"iptables -A INPUT -p tcp --dport 8443 -m conntrack --ctstate NEW -j ACCEPT",
		"ip6tables -A INPUT -p tcp --dport 8080 -m conntrack --ctstate NEW -j ACCEPT",
		"ip6tables -A INPUT -p tcp --dport 8443 -m conntrack --ctstate NEW -j ACCEPT",
		// detected UDP service port (no NEW state for udp), v4 + v6
		"iptables -A INPUT -p udp --dport 51820 -j ACCEPT",
		"ip6tables -A INPUT -p udp --dport 51820 -j ACCEPT",
		// INPUT locked on both families
		"iptables -P INPUT DROP",
		"ip6tables -P INPUT DROP",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("coexist ruleset missing %q\n---\n%s", want, got)
		}
	}

	// SSH port must appear EXACTLY once per family (deduped against the service set,
	// which also lists 22). Count whole lines so v4 (iptables) and v6 (ip6tables) are
	// tallied independently.
	if n := countLinesWithAll(got, "iptables -A INPUT -p tcp --dport 22 "); n != 1 {
		t.Errorf("SSH :22 v4 rule should appear once, got %d", n)
	}
	if n := countLinesWithAll(got, "ip6tables -A INPUT -p tcp --dport 22 "); n != 1 {
		t.Errorf("SSH :22 v6 rule should appear once, got %d", n)
	}
}

// countLinesWithAll counts how many lines of s contain sub.
func countLinesWithAll(s, sub string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			n++
		}
	}
	return n
}

// TestCoexistRulesetLeavesForwardUntouched asserts the coexist build emits NO
// `-P FORWARD` line on EITHER family: docker re-asserts its own FORWARD DROP +
// DOCKER-FORWARD rules on boot, and a router keeps bespoke FORWARD ACCEPT rules —
// touching the policy would either be overridden or loosen the operator's chosen
// isolation. True coexistence is non-mutating for FORWARD.
func TestCoexistRulesetLeavesForwardUntouched(t *testing.T) {
	got := coexistRuleset(2222, []int{9099}, nil)
	if strings.Contains(got, "-P FORWARD") {
		t.Errorf("coexist build must not set ANY FORWARD policy:\n%s", got)
	}
	// INPUT must still be locked and the custom SSH port opened.
	if !strings.Contains(got, "iptables -P INPUT DROP") ||
		!strings.Contains(got, "ip6tables -P INPUT DROP") {
		t.Errorf("coexist build must still DROP INPUT on both families:\n%s", got)
	}
	if !strings.Contains(got, "--dport 2222 -m conntrack --ctstate NEW -j ACCEPT") {
		t.Errorf("custom SSH port :2222 not opened:\n%s", got)
	}
}

// TestCountServicePorts guards the off-by-one: the reported service
// port count MUST equal the number of distinct non-SSH ACCEPT rules the ruleset
// actually emits per family. The SSH port is present in ListenPortsTCP but is
// opened once via the SSH ACCEPT and deduped out of the service loop, so it must
// NOT inflate the count.
func TestCountServicePorts(t *testing.T) {
	// SSH :22 listed among the tcp ports — must be excluded from the service count.
	tcp := []int{22, 8080, 8443, 9099}
	udp := []int{51820}
	if got := countServicePorts(22, tcp, udp); got != 4 {
		t.Errorf("countServicePorts = %d, want 4 (8080/8443/9099 tcp + 51820 udp, SSH :22 excluded)", got)
	}

	// Cross-check against the ruleset: count non-SSH ACCEPT lines emitted per family.
	rs := coexistRuleset(22, tcp, udp)
	v4, v6 := 0, 0
	for _, line := range strings.Split(rs, "\n") {
		if !strings.Contains(line, "-j ACCEPT") || strings.Contains(line, "--dport 22 ") {
			continue // skip SSH ACCEPT and non-ACCEPT (lo/conntrack/icmpv6) lines
		}
		if !strings.Contains(line, "--dport ") {
			continue // lo / conntrack / icmpv6 ACCEPTs carry no --dport
		}
		if strings.HasPrefix(line, "ip6tables") {
			v6++
		} else if strings.HasPrefix(line, "iptables") {
			v4++
		}
	}
	if v4 != 4 || v6 != 4 {
		t.Errorf("ruleset emitted v4=%d v6=%d non-SSH service ACCEPTs, want 4 each", v4, v6)
	}
	if countServicePorts(22, tcp, udp) != v4 {
		t.Errorf("countServicePorts (%d) must match per-family non-SSH ACCEPT count (%d)",
			countServicePorts(22, tcp, udp), v4)
	}
}

// TestDportOpen guards the superset-prefix false-match bug: an unanchored
// substring match of `--dport 22` wrongly matches a rule for port 2222 (and
// `--dport 80` matches `--dport 8080`). dportOpen anchors with `( |$)` so only the
// exact destination port satisfies the query — whether the port is mid-rule
// (followed by a space) or at end-of-line.
func TestDportOpen(t *testing.T) {
	// Superset-prefix rule must NOT satisfy the shorter port (the reported bug).
	if dportOpen("-A INPUT -p tcp -m tcp --dport 2222 -j ACCEPT", 22) {
		t.Error("dportOpen(2222-rule, 22) = true, want false (superset-prefix false match)")
	}
	if dportOpen("-A INPUT -p tcp -m tcp --dport 8080 -j ACCEPT", 80) {
		t.Error("dportOpen(8080-rule, 80) = true, want false (superset-prefix false match)")
	}
	// Exact match mid-rule (port followed by a space) must succeed.
	if !dportOpen("-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT", 22) {
		t.Error("dportOpen(22-rule, 22) = false, want true")
	}
	// Exact match at end-of-line ($ branch of the anchor) must succeed.
	if !dportOpen("-A INPUT -p tcp -m tcp --dport 22\n", 22) {
		t.Error("dportOpen(rule ending in --dport 22, 22) = false, want true (end-of-line anchor)")
	}
	// A real 2222 rule still matches a query FOR 2222 (anchor doesn't over-restrict).
	if !dportOpen("-A INPUT -p tcp -m tcp --dport 2222 -j ACCEPT", 2222) {
		t.Error("dportOpen(2222-rule, 2222) = false, want true")
	}
}

// TestUFWAllowScript asserts the ufw path emits an idempotent allow for SSH +
// every detected TCP/UDP service port (deduped against SSH), and NEVER touches
// the default policy or enables/disables ufw (allow-only ⇒ cannot lock out).
func TestUFWAllowScript(t *testing.T) {
	got := ufwAllowScript(22, []int{22, 8080, 9092}, []int{51820})
	for _, want := range []string{
		"ufw allow 22/tcp",
		"ufw allow 8080/tcp",
		"ufw allow 9092/tcp",
		"ufw allow 51820/udp",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ufw script missing %q\n---\n%s", want, got)
		}
	}
	// Allow-only: must never change default policy nor enable/disable ufw.
	for _, forbidden := range []string{"ufw default", "ufw enable", "ufw disable", "ufw deny", "iptables", "netfilter-persistent"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("ufw script must not contain %q (allow-only):\n%s", forbidden, got)
		}
	}
	// SSH :22 allowed exactly once (deduped against the service set that also lists 22).
	if n := countLinesWithAll(got, "ufw allow 22/tcp"); n != 1 {
		t.Errorf("SSH :22 should be allowed once, got %d", n)
	}
}

// TestUFWAllowScriptCustomPort checks a non-22 SSH port is allowed and the
// service loop still emits its own ports.
func TestUFWAllowScriptCustomPort(t *testing.T) {
	got := ufwAllowScript(2222, []int{9099}, nil)
	if !strings.Contains(got, "ufw allow 2222/tcp") || !strings.Contains(got, "ufw allow 9099/tcp") {
		t.Errorf("custom SSH port / service port not allowed:\n%s", got)
	}
}

// TestUFWAlreadyAllows guards the light skip-if: it is true only when SSH AND
// every detected port are already present in `ufw status`.
func TestUFWAlreadyAllows(t *testing.T) {
	full := "Status: active\n22/tcp ALLOW Anywhere\n8080/tcp ALLOW Anywhere\n51820/udp ALLOW Anywhere"
	if !ufwAlreadyAllows(full, 22, []int{22, 8080}, []int{51820}) {
		t.Error("expected true when SSH + all ports already allowed")
	}
	// Missing one port ⇒ false (script must run to top it up).
	partial := "Status: active\n22/tcp ALLOW Anywhere\n8080/tcp ALLOW Anywhere"
	if ufwAlreadyAllows(partial, 22, []int{22, 8080}, []int{51820}) {
		t.Error("expected false when a detected port is missing from ufw status")
	}
	// Empty status ⇒ false.
	if ufwAlreadyAllows("", 22, nil, nil) {
		t.Error("expected false for empty ufw status")
	}
}
