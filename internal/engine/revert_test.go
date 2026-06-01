package engine

import (
	"strings"
	"testing"
)

// revertableIDs is the canonical set the brief mandates as revertable.
var revertableIDs = []string{"A1", "A2", "A3", "A4", "A5", "A6", "A6.5", "A6.7", "A9", "A10"}

// notRevertableIDs are the steps deliberately excluded (A8 reboot, A7 purge,
// A2.5 cloud-init).
var notRevertableIDs = []string{"A8", "A7", "A2.5"}

// TestRevertScriptEveryRevertableHasSnippet asserts every revertable id maps to a
// non-empty, heredoc-free single-logical-line snippet and that IsRevertable agrees.
func TestRevertScriptEveryRevertableHasSnippet(t *testing.T) {
	for _, id := range revertableIDs {
		snip, ok := revertScript[id]
		if !ok {
			t.Errorf("revertScript missing entry for %q", id)
			continue
		}
		if strings.TrimSpace(snip) == "" {
			t.Errorf("revertScript[%q] is empty", id)
		}
		if strings.Contains(snip, "<<") {
			t.Errorf("revertScript[%q] contains a heredoc (forbidden over stdin): %q", id, snip)
		}
		if strings.Contains(snip, "\n") {
			t.Errorf("revertScript[%q] must be a single logical line (no newline): %q", id, snip)
		}
		if !IsRevertable(id) {
			t.Errorf("IsRevertable(%q)=false, want true", id)
		}
	}
}

// TestRevertScriptCount guards against an accidental extra/missing entry.
func TestRevertScriptCount(t *testing.T) {
	if len(revertScript) != len(revertableIDs) {
		t.Fatalf("revertScript has %d entries, want %d (%v)", len(revertScript), len(revertableIDs), revertableIDs)
	}
}

// TestNotRevertable asserts A8/A7/A2.5 are NOT revertable (no snippet, IsRevertable
// false) — the lockout/irreversible-capable steps must never expose a revert.
func TestNotRevertable(t *testing.T) {
	for _, id := range notRevertableIDs {
		if _, ok := revertScript[id]; ok {
			t.Errorf("revertScript must NOT contain %q (not revertable)", id)
		}
		if IsRevertable(id) {
			t.Errorf("IsRevertable(%q)=true, want false", id)
		}
	}
}

// TestRevertSnippetPaths spot-checks that each revert removes the EXACT artifact
// paths the corresponding step writes (verified against the step source / probe
// Cmds). A drifted path would leave the tweak applied after a "successful" revert.
func TestRevertSnippetPaths(t *testing.T) {
	wantPaths := map[string][]string{
		"A1":   {"/etc/iptables/rules.v4", "/etc/iptables/rules.v6", "fw-rollback.timer"},
		"A2":   {"/etc/ssh/sshd_config.d/00-hardening.conf", "/etc/ssh/sshd_config.d/98-access.conf", "/etc/ssh/sshd_config.d/99-hardening.conf"},
		"A3":   {"/etc/fail2ban/jail.local"},
		"A4":   {"/etc/sysctl.d/99-net-tune.conf", "/etc/sysctl.d/99-bbr.conf", "/etc/udev/rules.d/60-io-scheduler.rules"},
		"A5":   {"/etc/sysctl.d/99-zz-kernel-harden.conf"},
		"A6":   {"/etc/systemd/journald.conf.d/99-vps-cap.conf", "/etc/needrestart/conf.d/50-autorestart.conf", "/etc/systemd/system.conf.d/limits.conf"},
		"A6.5": {"/etc/systemd/resolved.conf.d/99-morgward-dns.conf"},
		"A6.7": {"/etc/systemd/zram-generator.conf", "/etc/sysctl.d/99-zram.conf", "earlyoom"},
		"A9":   {"/etc/apt/apt.conf.d/20auto-upgrades", "/etc/apt/apt.conf.d/52-unattended-upgrades-local"},
		"A10":  {"/etc/audit/rules.d/99-vps.rules", "/usr/local/sbin/ssh-login-notify.sh", "ssh-login-notify"},
	}
	for id, paths := range wantPaths {
		snip := revertScript[id]
		for _, p := range paths {
			if !strings.Contains(snip, p) {
				t.Errorf("revertScript[%q] missing expected path/marker %q in %q", id, p, snip)
			}
		}
	}
}

// TestRevertA2OpensAccess guards the lockout-safety invariant: the A2 revert must
// only REMOVE hardening drop-ins / regenerate host keys / reload sshd — never write
// a tightening directive (PermitRootLogin no / PasswordAuthentication no / AllowGroups).
func TestRevertA2OpensAccess(t *testing.T) {
	snip := revertScript["A2"]
	for _, forbidden := range []string{"PermitRootLogin no", "PasswordAuthentication no", "AllowGroups"} {
		if strings.Contains(snip, forbidden) {
			t.Errorf("A2 revert must NOT contain tightening directive %q: %q", forbidden, snip)
		}
	}
	if !strings.Contains(snip, "rm -f") {
		t.Errorf("A2 revert must remove drop-ins (rm -f): %q", snip)
	}
}

// TestCanonicalStepID maps mixed-case ids to the canonical step IDs.
func TestCanonicalStepID(t *testing.T) {
	cases := map[string]string{
		"a6.5": "A6.5",
		"A6.7": "A6.7",
		"a1":   "A1",
		"a10":  "A10",
	}
	for in, want := range cases {
		if got := canonicalStepID(in); got != want {
			t.Errorf("canonicalStepID(%q)=%q, want %q", in, got, want)
		}
	}
}

// TestFirstStderrLine returns the first non-empty trimmed line.
func TestFirstStderrLine(t *testing.T) {
	if got := firstStderrLine("\n  boom \nsecond\n"); got != "boom" {
		t.Errorf("firstStderrLine=%q, want %q", got, "boom")
	}
	if got := firstStderrLine(""); got != "" {
		t.Errorf("firstStderrLine(empty)=%q, want empty", got)
	}
}

// TestRevertSelectStepsUnknownRejected confirms RunRevert's id-validation contract
// reuses selectSteps: a genuinely unknown id is reported, a known one is not.
func TestRevertSelectStepsUnknownRejected(t *testing.T) {
	_, unknown := selectSteps([]string{"A1", "BOGUS"})
	if len(unknown) != 1 || unknown[0] != "BOGUS" {
		t.Fatalf("unknown=%v, want [BOGUS]", unknown)
	}
}
