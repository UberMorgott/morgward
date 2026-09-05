package steps

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The 26.04 branch installs nothing. An EMPTY `else` body is a bash SYNTAX ERROR
// that kills the WHOLE script, so the chrony drop-in and the timedatectl fallback
// would never run and a6.chrony would report a bare "na". Checked structurally and
// — where a bash is actually available — with the real parser (`bash -n`).
func TestTimeSyncScriptShellSyntax(t *testing.T) {
	bash, _ := exec.LookPath("bash")
	for name, s := range map[string]string{
		"26.04 (no install)": timeSyncScript(false),
		"24.04 (install)":    timeSyncScript(true),
	} {
		if strings.Contains(s, "else\nfi") || strings.Contains(s, "else\n\nfi") {
			t.Errorf("%s: empty else body is a bash syntax error:\n%s", name, s)
		}
		if bash == "" {
			continue
		}
		p := filepath.Join(t.TempDir(), "s.sh")
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		// #nosec G204 -- bash comes from exec.LookPath and the arg is a temp file we wrote
		if out, err := exec.CommandContext(t.Context(), bash, "-n", p).CombinedOutput(); err != nil {
			t.Errorf("%s: bash -n rejected the script: %v\n%s\n---\n%s", name, err, out, s)
		}
	}
}

// M06: chrony is INSTALLED only on 22.04/24.04 (26.04 ships it), and never when it
// is already active. Either way the script falls back to `timedatectl set-ntp true`,
// so time sync can never end up off, and the drop-in lands under conf.d so the
// distro chrony.conf and any operator sources.d/ pool stay untouched.
func TestTimeSyncScriptInstallGate(t *testing.T) {
	with := timeSyncScript(true)
	without := timeSyncScript(false)

	if !strings.Contains(with, " install -y") || !strings.Contains(with, "chrony") {
		t.Errorf("22.04/24.04 must install chrony:\n%s", with)
	}
	if strings.Contains(without, " install -y") {
		t.Errorf("26.04 already ships chrony — the script must not install it:\n%s", without)
	}
	// An absent install candidate must never fail A6: the install is optional.
	if !strings.Contains(with, "|| true") {
		t.Errorf("the chrony install must be optional (|| true):\n%s", with)
	}
	for _, s := range []string{with, without} {
		if !strings.Contains(s, "systemctl is-active chrony") {
			t.Errorf("an already-active chrony must be left alone:\n%s", s)
		}
		if !strings.Contains(s, "timedatectl set-ntp true") {
			t.Errorf("every branch must keep a time-sync fallback:\n%s", s)
		}
		if !strings.Contains(s, "/etc/chrony/conf.d/90-morgward.conf") {
			t.Errorf("the drop-in must land under conf.d, never in chrony.conf:\n%s", s)
		}
		if strings.Contains(s, "<<") {
			t.Errorf("no heredoc may appear in a remote script:\n%s", s)
		}
	}
}

// M05: fstrim is enabled only where a block device reports discard support, so a
// box on plain passthrough storage is not shown a timer we deliberately skipped.
func TestFstrimScriptGatedOnDiscard(t *testing.T) {
	if !strings.Contains(fstrimScript, "lsblk --discard") {
		t.Errorf("fstrim must be gated on real discard support:\n%s", fstrimScript)
	}
	if !strings.Contains(fstrimScript, "systemctl enable --now fstrim.timer") {
		t.Errorf("fstrim script must enable the timer:\n%s", fstrimScript)
	}
}

// A freshly installed chrony has not picked a source yet; the raw first line of
// `chronyc tracking` rendered as "chrony (Reference ID    : 00000000 ())".
func TestChronyNote(t *testing.T) {
	syncing := "Reference ID    : 00000000 ()\nStratum         : 0\nLeap status     : Not synchronised\n"
	synced := "Reference ID    : C0248F97 (time.cloudflare.com)\nStratum         : 3\nRef time (UTC)  : Fri Sep 05 06:00:37 2026\nLeap status     : Normal\n"
	for name, tc := range map[string]struct{ in, want string }{
		"not yet synced": {syncing, "chrony: installed, syncing"},
		"synced":         {synced, "chrony: synced (stratum 3)"},
		"no chrony":      {"", "ntp on"},
	} {
		if got := chronyNote(tc.in); got != tc.want {
			t.Errorf("%s: chronyNote = %q, want %q", name, got, tc.want)
		}
	}
}
