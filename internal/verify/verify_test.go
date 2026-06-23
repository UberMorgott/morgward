package verify

import (
	"errors"
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// TestFirewallChecksDportAnchored is the regression guard for the dport-prefix
// false-match bug: the managed "SSH port open" matcher must anchor the port with
// `( |$)` so a rule for :2222 cannot satisfy a check for :22. The matcher runs
// remotely (grep over `iptables -S`), so the behavioral proof is the CLI repro
// documented in the branch (`echo '… --dport 2222 …' | grep -E -- '--dport 22( |$)'`
// exits 1); this Go test only pins the anchor into the generated Cmd so it can't
// silently regress.
func TestFirewallChecksDportAnchored(t *testing.T) {
	// nil facts ⇒ managed iptables path (the original byte-identical matcher).
	fw := firewallChecks(nil, 22)
	ssh := fw[1] // {Name: "SSH port open", ...}
	if ssh.Name != "SSH port open" {
		t.Fatalf("fw[1] = %q, want the SSH-port-open row", ssh.Name)
	}
	if !strings.Contains(ssh.Cmd, "( |$)") {
		t.Errorf("SSH-port-open Cmd lost its dport anchor (regression):\n%s", ssh.Cmd)
	}
}

// TestClassifyTransportError is the F21 guard: a transport/exec failure (RC==-1 or
// Err!=nil) on a NON-lockout row must read as StatusUnknown ("could not measure"),
// never collapse into a misleading WARN with got="".
func TestClassifyTransportError(t *testing.T) {
	ch := Check{Name: "BBR", Cmd: "x", Want: equals("bbr"), Lockout: false}

	got := classify(ch, sshx.Result{RC: -1, Err: errors.New("dial tcp: timeout")})
	if got.Status != StatusUnknown {
		t.Fatalf("transport error → %v, want StatusUnknown", got.Status)
	}
	if got.Detail == "" {
		t.Fatalf("StatusUnknown row left an empty detail — should explain why")
	}

	// Err set without RC==-1 (session failure that still reported an exit) also counts.
	got = classify(ch, sshx.Result{RC: 0, Err: errors.New("session: broken pipe")})
	if got.Status != StatusUnknown {
		t.Fatalf("Err!=nil → %v, want StatusUnknown", got.Status)
	}
}

// TestClassifyLockoutFailsClosed asserts a transport failure on a LOCKOUT row does NOT
// become StatusUnknown — it must still fail closed (StatusFail → Abort) so an
// unmeasured lockout-capable check can never be silently treated as "unknown/benign".
func TestClassifyLockoutFailsClosed(t *testing.T) {
	ch := Check{Name: "SSH syntax", Cmd: "x", Want: equals("ok"), Lockout: true}
	got := classify(ch, sshx.Result{RC: -1, Err: errors.New("dial tcp: timeout")})
	if got.Status != StatusFail {
		t.Fatalf("unmeasured lockout row → %v, want StatusFail (fail closed)", got.Status)
	}
}

// TestClassifyGenuineNegative asserts a clean run with a non-matching value (no
// transport error — e.g. grep no-match: RC=1, empty stdout, Err=nil) stays WARN on a
// non-lockout row, NOT StatusUnknown. RC!=-1 with Err==nil is a real measurement.
func TestClassifyGenuineNegative(t *testing.T) {
	ch := Check{Name: "BBR", Cmd: "x", Want: equals("bbr"), Lockout: false}
	got := classify(ch, sshx.Result{Stdout: "cubic", RC: 0})
	if got.Status != StatusWarn {
		t.Fatalf("genuine negative → %v, want StatusWarn", got.Status)
	}

	// grep no-match: RC=1, empty stdout, no transport error → still a real WARN.
	got = classify(ch, sshx.Result{Stdout: "", RC: 1})
	if got.Status != StatusWarn {
		t.Fatalf("RC=1 no-match → %v, want StatusWarn (not Unknown)", got.Status)
	}
}

// TestClassifyPass asserts a matching value passes.
func TestClassifyPass(t *testing.T) {
	ch := Check{Name: "BBR", Cmd: "x", Want: equals("bbr"), Lockout: false}
	if got := classify(ch, sshx.Result{Stdout: "bbr\n", RC: 0}); got.Status != StatusPass {
		t.Fatalf("matching value → %v, want StatusPass", got.Status)
	}
}

// TestPolicyKnown asserts the SSH-effective-policy matcher PASSes on any valid
// effective PermitRootLogin value (which varies by Ubuntu image — 24.04 ships
// prohibit-password or yes, 26.04 ships yes, lockdown sets no) and rejects only
// garbage or unreadable output (the real anomaly).
func TestPolicyKnown(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"permitrootlogin yes", true},                  // 26.04 default
		{"permitrootlogin prohibit-password", true},    // 24.04 default
		{"permitrootlogin no", true},                   // lockdown
		{"permitrootlogin without-password", true},     //
		{"permitrootlogin forced-commands-only", true}, //
		{"PermitRootLogin Yes", true},                  // case-insensitive
		{"permitrootlogin banana", false},              // garbage
		{"", false},                                    // unreadable
		{"permitrootlogin", false},                     // no value field
	}
	for _, c := range cases {
		if got := policyKnown(c.in); got != c.want {
			t.Errorf("policyKnown(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestClassifySSHPolicyPass proves a real classify() of the actual SSH-effective-policy
// row Want passes on a valid policy value and WARNs (non-lockout) on empty output.
func TestClassifySSHPolicyPass(t *testing.T) {
	ch := Check{Name: "SSH effective policy", Want: policyKnown, Lockout: false}
	if got := classify(ch, sshx.Result{Stdout: "permitrootlogin yes\n", RC: 0}); got.Status != StatusPass {
		t.Fatalf("valid policy → %v, want StatusPass", got.Status)
	}
	if got := classify(ch, sshx.Result{Stdout: "", RC: 1}); got.Status != StatusWarn {
		t.Fatalf("unreadable policy → %v, want StatusWarn", got.Status)
	}
}

// TestClassifyNASkip asserts the NA precondition path still yields StatusSkip with its
// reason, and is not shadowed by the transport-error branch on a clean result.
func TestClassifyNASkip(t *testing.T) {
	ch := Check{
		Name: "auditd", Cmd: "x", Want: equals("ok"), Lockout: false,
		NA: func(out string) (string, bool) {
			if out == "__noauditd__" {
				return "auditd not installed", true
			}
			return "", false
		},
	}
	got := classify(ch, sshx.Result{Stdout: "__noauditd__", RC: 0})
	if got.Status != StatusSkip {
		t.Fatalf("NA precondition → %v, want StatusSkip", got.Status)
	}
	if got.Detail != "auditd not installed" {
		t.Fatalf("skip detail = %q, want the NA reason", got.Detail)
	}
}
