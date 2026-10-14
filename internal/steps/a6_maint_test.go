package steps

import (
	"errors"
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

// aptQuiesce run for real (where bash exists) against shell-function stubs: a
// lock holder that clears after two probes, an interrupted dpkg run, and a script
// that exits 7. The timers must be stopped then restarted on EXIT, the exit status
// preserved, one status line printed (not one per probe), and dpkg finished.
func TestAptQuiesceBehavior(t *testing.T) {
	bash, _ := exec.LookPath("bash")
	if bash == "" {
		t.Skip("no bash")
	}
	stubs := "exec 3>&1\n" +
		"systemctl() { echo \"CALL systemctl $*\" >&3; [ \"$1\" = is-active ] && return 0; return 0; }\n" +
		"fuser() { n=$(cat cnt 2>/dev/null || echo 0); echo $((n+1)) > cnt; [ \"$n\" -lt 2 ] && echo ' 4242'; return 0; }\n" +
		"pgrep() { return 1; }\n" +
		"ps() { echo '4242 unattended-upgr'; }\n" +
		"sleep() { :; }\n" +
		// Git-for-Windows bash resolves bare `sort` to Windows sort.exe.
		"sort() { /usr/bin/sort \"$@\"; }\n" +
		"dpkg() { echo \"CALL dpkg $*\" >&3; [ \"$1\" = --audit ] && echo 'half-configured'; return 0; }\n"
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), bash, "-s") // #nosec G204 -- bash from LookPath, script is ours
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stubs + "set -e\n" + aptQuiesce + "echo BODY\nexit 7\n")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("exit status must be preserved through the EXIT trap (want 7): %v\n%s", err, out)
	}
	o := string(out)
	for _, want := range []string{
		"CALL systemctl stop apt-daily.timer apt-daily-upgrade.timer",
		"CALL systemctl stop --no-block apt-daily.service apt-daily-upgrade.service",
		"CALL dpkg --force-confdef --force-confold --configure -a",
		"BODY",
		"CALL systemctl start apt-daily.timer apt-daily-upgrade.timer",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("missing %q in:\n%s", want, o)
		}
	}
	if n := strings.Count(o, "waiting for apt automation"); n != 1 {
		t.Errorf("want exactly 1 status line, got %d:\n%s", n, o)
	}
	if strings.Index(o, "BODY") > strings.Index(o, "CALL systemctl start") {
		t.Errorf("timers must restart only on EXIT, after the script body:\n%s", o)
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
