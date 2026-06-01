package steps

import (
	"strings"
	"testing"
)

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
