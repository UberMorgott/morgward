package steps

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// Fake process table rows: pid|comm|cmdline. The shutdown helper runs on every
// Ubuntu box (unattended-upgrades.service) and shares the truncated comm.
const (
	procShutdownHelper = "900|unattended-upgr|/usr/bin/python3 /usr/share/unattended-upgrades/unattended-upgrade-shutdown --wait-for-signal\n"
	procUUMain         = "3152|unattended-upgr|/usr/bin/python3 /usr/bin/unattended-upgrade\n"
	procDailyChild     = "777|apt.systemd.dai|/bin/sh /usr/lib/apt/apt.systemd.daily lock_is_held install\n"
)

// runAptQuiesce runs aptQuiesce + a body that exits 7 under real bash, with
// systemctl/fuser/pgrep/kill/ps/sleep/dpkg stubbed as shell functions. pgrep applies
// the script's REAL patterns to the fake process table (-x on comm, -f on cmdline,
// like procps); kill removes what it signals. fuser reports PID 4242 as a lock holder
// for the first fuserHolds probes, and on its FIRST probe (after the initial signal
// pass) adds lateProcs — a process spawned after that pass. dpkg --audit reports an
// interrupted run.
func runAptQuiesce(t *testing.T, bash, procs, lateProcs string, fuserHolds int) string {
	t.Helper()
	stubs := "exec 3>&1\n" +
		"systemctl() { echo \"CALL systemctl $*\" >&3; return 0; }\n" +
		"mw_match() { while IFS='|' read -r p c a; do if [ \"$1\" = -x ]; then f=$c; else f=$a; fi; if [ \"$1\" = -x ]; then printf '%s\\n' \"$f\" | grep -Eqx \"$2\" && echo \"$p\"; else printf '%s\\n' \"$f\" | grep -Eq \"$2\" && echo \"$p\"; fi; done < procs; return 0; }\n" +
		"pgrep() { mw_match \"$@\"; }\n" +
		"kill() { echo \"KILL $1 $2\" >&3; grep -v \"^$2|\" procs > procs.n || true; mv procs.n procs; return 0; }\n" +
		"fuser() { n=$(cat cnt 2>/dev/null || echo 0); echo $((n+1)) > cnt; [ \"$n\" -eq 0 ] && cat late >> procs; [ \"$n\" -lt " + strconv.Itoa(fuserHolds) + " ] && echo ' 4242'; return 0; }\n" +
		"ps() { echo '4242 dpkg'; }\n" +
		"sleep() { echo 'CALL sleep' >&3; }\n" +
		// Git-for-Windows bash resolves bare `sort` to Windows sort.exe.
		"sort() { /usr/bin/sort \"$@\"; }\n" +
		"dpkg() { echo \"CALL dpkg $*\" >&3; [ \"$1\" = --audit ] && echo 'half-configured'; return 0; }\n"
	dir := t.TempDir()
	for name, body := range map[string]string{"procs": procs, "late": lateProcs} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), bash, "-s") // #nosec G204 -- bash from LookPath, script is ours
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stubs + "set -e\n" + aptQuiesce + "echo BODY\nexit 7\n")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("exit status must be preserved through the EXIT trap (want 7): %v\n%s", err, out)
	}
	return string(out)
}

// The always-on shutdown helper with free locks must cause NO wait and must not be
// signalled, and dpkg recovery still runs (regression: `pgrep -x unattended-upgr`
// matched the helper, so every apt script waited the full cap and skipped recovery).
// A running unattended-upgrade main process gets SIGTERM (KillMode=process means
// systemctl stop alone never reaches it).
func TestAptQuiesceUnattendedUpgradeTargeting(t *testing.T) {
	bash, _ := exec.LookPath("bash")
	if bash == "" {
		t.Skip("no bash")
	}
	for name, tc := range map[string]struct {
		procs    string
		wantKill bool
	}{
		"helper only, locks free": {procShutdownHelper, false},
		"u-u running":             {procShutdownHelper + procUUMain, true},
	} {
		o := runAptQuiesce(t, bash, tc.procs, "", 0)
		if strings.Contains(o, "CALL sleep") || strings.Contains(o, "waiting for apt automation") {
			t.Errorf("%s: must not wait with free locks:\n%s", name, o)
		}
		if strings.Contains(o, "KILL -TERM 900") {
			t.Errorf("%s: shutdown helper must never be signalled:\n%s", name, o)
		}
		if got := strings.Contains(o, "KILL -TERM 3152"); got != tc.wantKill {
			t.Errorf("%s: SIGTERM to unattended-upgrade main = %v, want %v:\n%s", name, got, tc.wantKill, o)
		}
		if !strings.Contains(o, "CALL dpkg --force-confdef --force-confold --configure -a") {
			t.Errorf("%s: dpkg recovery must run once the lock is free:\n%s", name, o)
		}
	}
}

// apt.systemd.daily's lock_is_held child survives KillMode=process and can spawn
// unattended-upgrade AFTER the first signal pass: the child is signalled up front and
// the late u-u is caught by the per-iteration re-signal (once per PID), so the wait
// ends instead of running into the cap.
func TestAptQuiesceResignalsLateUnattendedUpgrade(t *testing.T) {
	bash, _ := exec.LookPath("bash")
	if bash == "" {
		t.Skip("no bash")
	}
	o := runAptQuiesce(t, bash, procShutdownHelper+procDailyChild, procUUMain, 2)
	for _, want := range []string{"KILL -TERM 777", "KILL -TERM 3152", "CALL dpkg --force-confdef --force-confold --configure -a"} {
		if !strings.Contains(o, want) {
			t.Errorf("missing %q in:\n%s", want, o)
		}
	}
	if strings.Count(o, "KILL -TERM 3152") != 1 || strings.Contains(o, "KILL -TERM 900") {
		t.Errorf("late u-u must be signalled exactly once, helper never:\n%s", o)
	}
	if n := strings.Count(o, "CALL sleep"); n != 2 {
		t.Errorf("wait must end once holders are gone (want 2 sleeps, got %d):\n%s", n, o)
	}
}

// aptQuiesce run for real against stubs: a lock holder that clears after two
// probes, an interrupted dpkg run, and a script that exits 7. The timers must be
// stopped then restarted on EXIT, the exit status preserved, one status line printed
// (not one per probe), and dpkg finished.
func TestAptQuiesceBehavior(t *testing.T) {
	bash, _ := exec.LookPath("bash")
	if bash == "" {
		t.Skip("no bash")
	}
	o := runAptQuiesce(t, bash, procShutdownHelper, "", 2)
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
