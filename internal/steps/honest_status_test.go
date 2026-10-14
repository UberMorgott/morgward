package steps

import (
	"errors"
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/verify"
)

// A10 must NEVER report OK when the pieces it claims to install are absent — the
// old code logged a Warn and returned StatusOK, so the run showed a green step
// next to red a10.auditd/a10.auditd_active probes.
func TestDetectionVerdictFailsWhenAptFailed(t *testing.T) {
	st, detail := detectionVerdict("", 100, true)
	if st != StatusFail {
		t.Fatalf("failed apt install must not report %v", st)
	}
	for _, want := range []string{"auditd not installed", "rc=100"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q must name %q", detail, want)
		}
	}
}

// A partial landing (auditd installed but rules never loaded) is still a failure,
// and the detail must say WHICH piece is missing.
func TestDetectionVerdictNamesMissingPiece(t *testing.T) {
	state := "AUDITD_BIN\nAUDITD_ACTIVE\nNOTIFY_SCRIPT\nNOTIFY_PAM\n"
	st, detail := detectionVerdict(state, 0, true)
	if st != StatusFail {
		t.Fatalf("missing audit rules must be a failure, got %v", st)
	}
	if !strings.Contains(detail, "99-vps.rules") {
		t.Errorf("detail %q must name the missing rules file", detail)
	}
	if strings.Contains(detail, "auditd not installed") {
		t.Errorf("detail %q must not report present pieces as missing", detail)
	}
}

func TestDetectionVerdictOK(t *testing.T) {
	state := "AUDITD_BIN\nAUDIT_RULES\nAUDITD_ACTIVE\nNOTIFY_SCRIPT\nNOTIFY_PAM\n"
	if st, _ := detectionVerdict(state, 0, true); st != StatusOK {
		t.Fatalf("all pieces present must be OK, got %v", st)
	}
	// The drop-log claim is only made where morgward owns the iptables ruleset.
	if _, d := detectionVerdict(state, 0, false); strings.Contains(d, "inbound-drop") {
		t.Errorf("detail %q must not claim a LOG rule we never wrote", d)
	}
}

// A6.7: earlyoom must not fail the step, but the detail must name what is down.
func TestMemVerdictDetail(t *testing.T) {
	if d := memVerdictDetail(false, false, "inactive"); !strings.Contains(d, "systemd-zram-generator is not installed") {
		t.Errorf("missing package must be named: %q", d)
	}
	if d := memVerdictDetail(false, true, "inactive"); !strings.Contains(d, "zram0 did not come up") {
		t.Errorf("installed-but-dead zram must be distinguished: %q", d)
	}
	if d := memVerdictDetail(true, true, "inactive"); !strings.Contains(d, "earlyoom NOT active") {
		t.Errorf("dead earlyoom must be stated, not glossed: %q", d)
	}
	if d := memVerdictDetail(true, true, "active"); strings.Contains(d, "NOT") {
		t.Errorf("all-good detail must be clean: %q", d)
	}
}

// Package names that were renamed across releases must be resilient to BOTH names,
// and an optional package must never take the required install down with it.
func TestOptionalInstallDoesNotFailTheScript(t *testing.T) {
	if !strings.HasSuffix(aptInstallOptional("audispd-plugins"), "|| true\n") {
		t.Fatalf("optional install must swallow its exit code: %q", aptInstallOptional("audispd-plugins"))
	}
	if !strings.Contains(aptInstallOptional("x"), "DPkg::Lock::Timeout=300") {
		t.Fatal("optional install must keep the dpkg lock timeout")
	}
}

// The shared apt contract: refresh the index once per boot BEFORE any install (a
// fresh image ships an empty index), keep dpkg off a pty, suspend needrestart, and
// never wire stdbuf (LD_PRELOAD spam on rust-coreutils boxes).
func TestAptPreludeContract(t *testing.T) {
	p := aptPrelude()
	for _, want := range []string{AptUpdateMarker, aptGet + " update", "NEEDRESTART_SUSPEND=1", "NEEDRESTART_MODE=a"} {
		if !strings.Contains(p, want) {
			t.Errorf("apt prelude must contain %q:\n%s", want, p)
		}
	}
	// The refresh must never trip a caller's `set -e` nor stamp the marker on failure.
	if !strings.Contains(p, "&& : > "+AptUpdateMarker+" || true") {
		t.Errorf("index refresh must stamp the marker only on success and swallow failure:\n%s", p)
	}
	if !strings.Contains(aptConfOpts, "Dpkg::Use-Pty=0") {
		t.Errorf("apt conf opts must keep dpkg off a pty: %s", aptConfOpts)
	}
	for _, s := range []string{p, aptInstall("x"), aptInstallOptional("x")} {
		if strings.Contains(s, "stdbuf") {
			t.Errorf("stdbuf must not be used (LD_PRELOAD spam on rust-coreutils): %q", s)
		}
	}
	// PRE runs its OWN hard-gate `apt-get update` right after the prelude, so it takes
	// the no-update variant — otherwise every first run refreshes the index twice.
	// The variant keeps the whole non-interactive contract, just not the refresh.
	nu := aptPreludeNoUpdate()
	if strings.Contains(nu, AptUpdateMarker) || strings.Contains(nu, aptGet+" update") {
		t.Errorf("aptPreludeNoUpdate must not refresh the index:\n%s", nu)
	}
	for _, want := range []string{"NEEDRESTART_SUSPEND=1", "NEEDRESTART_MODE=a", "DEBIAN_FRONTEND=noninteractive"} {
		if !strings.Contains(nu, want) {
			t.Errorf("aptPreludeNoUpdate must keep %q:\n%s", want, nu)
		}
	}
	if p != nu+aptUpdateOnce {
		t.Errorf("aptPrelude must be aptPreludeNoUpdate + the once-per-boot refresh:\n%s", p)
	}
}

// Boot-time apt-daily-upgrade (unattended-upgrade) outliving DPkg::Lock::Timeout
// killed A8's full-upgrade on a fresh box. Every apt prelude must park apt
// automation (runtime-only, restored on EXIT) and wait for lock holders BEFORE any
// lock-taking apt-get, and finish an interrupted dpkg run.
func TestAptQuiesceContract(t *testing.T) {
	for _, want := range []string{
		"systemctl is-active --quiet \"$mw_t\"",
		"apt-daily.timer apt-daily-upgrade.timer",
		"systemctl stop $MW_APT_TIMERS",
		"trap \"systemctl start$MW_APT_TIMERS",
		"systemctl stop --no-block apt-daily.service apt-daily-upgrade.service",
		"fuser " + aptLockFiles,
		"pgrep -x 'apt|apt-get|aptitude|dpkg|unattended-upgr'",
		"-lt 600 ]",
		"$((mw_w % 30)) -eq 0",
		"dpkg --audit",
		"dpkg --force-confdef --force-confold --configure -a",
	} {
		if !strings.Contains(aptQuiesce, want) {
			t.Errorf("aptQuiesce must contain %q:\n%s", want, aptQuiesce)
		}
	}
	// Runtime-only: nothing persistent for revert to undo.
	for _, bad := range []string{"systemctl disable", "systemctl mask", "systemctl enable", "<<", "stdbuf"} {
		if strings.Contains(aptQuiesce, bad) {
			t.Errorf("aptQuiesce must not contain %q:\n%s", bad, aptQuiesce)
		}
	}
	// Both preludes quiesce, and BEFORE any lock-taking apt-get.
	for name, p := range map[string]string{"aptPrelude": aptPrelude(), "aptPreludeNoUpdate": aptPreludeNoUpdate()} {
		q := strings.Index(p, aptQuiesce)
		if q < 0 {
			t.Fatalf("%s must include aptQuiesce:\n%s", name, p)
		}
		if a := strings.Index(p, aptGet); a >= 0 && a < q {
			t.Errorf("%s runs apt-get before quiescing apt automation:\n%s", name, p)
		}
		if strings.Index(p, "DEBIAN_FRONTEND=noninteractive") > q {
			t.Errorf("%s must export the non-interactive env before dpkg --configure -a:\n%s", name, p)
		}
	}
}

// The exact scripts PRE and A8 send: quiesce precedes their lock-taking apt-get.
func TestPreAndA8QuiesceBeforeApt(t *testing.T) {
	for name, s := range map[string]string{"PRE": preAptUpdateScript(), "A8": a8UpgradeScript()} {
		q := strings.Index(s, aptQuiesce)
		a := strings.Index(s, aptGet)
		if q < 0 || a < 0 || q > a {
			t.Errorf("%s: quiesce (at %d) must precede the first %q (at %d):\n%s", name, q, aptGet, a, s)
		}
	}
	if !strings.Contains(a8UpgradeScript(), aptGet+" full-upgrade -y"+aptConfOpts) {
		t.Errorf("A8 script lost its full-upgrade line:\n%s", a8UpgradeScript())
	}
}

// A lost SSH transport (rc=-1 / a transport error) must be told apart from a
// command that ran and exited non-zero — the two lead to different verdicts when a
// step follows a long command with a reachability verify (B3).
func TestTransportLost(t *testing.T) {
	cases := []struct {
		name string
		r    sshx.Result
		want bool
	}{
		{"clean exit", sshx.Result{RC: 0}, false},
		{"command failed", sshx.Result{RC: 100}, false},
		{"transport gone", sshx.Result{RC: -1}, true},
		{"transport error", sshx.Result{RC: 0, Err: errors.New("connection lost")}, true},
	}
	for _, c := range cases {
		if got := transportLost(c.r); got != c.want {
			t.Errorf("%s: transportLost = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDNSScriptTriesBothDnsutilsNames(t *testing.T) {
	i := strings.Index(dnsCalibrateScript, "bind9-dnsutils")
	j := strings.Index(dnsCalibrateScript, " dnsutils")
	if i < 0 || j < 0 {
		t.Fatalf("both package names must be attempted (bind9-dnsutils=%d dnsutils=%d)", i, j)
	}
	if i > j {
		t.Error("the real package bind9-dnsutils must be tried before the transitional stub")
	}
}

// A10 installs auditd and audispd-plugins on SEPARATE lines: a transitional package
// missing on a newer release must not abort the required install.
func TestA10SplitsTransitionalPackage(t *testing.T) {
	req := aptInstall("auditd")
	if !strings.HasSuffix(req, " auditd\n") || !strings.Contains(req, " install -y") {
		t.Fatalf("auditd must be installed alone, got %q", req)
	}
}

// The auto-updates probe must be locale-independent and shared by step A9 and the
// §V matrix (one string, no drift).
func TestAutoUpdatesCmdIsLocaleIndependent(t *testing.T) {
	if !strings.Contains(verify.AutoUpdatesCmd, "LC_ALL=C") {
		t.Error("auto-updates probe must force LC_ALL=C")
	}
	if strings.Contains(strings.ToLower(verify.AutoUpdatesCmd), "allowed origins") {
		t.Error("auto-updates probe must not depend on free-text English output")
	}
}
