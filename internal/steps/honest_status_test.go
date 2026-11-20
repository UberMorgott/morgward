package steps

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		"pgrep -x '" + aptToolComms + "'",
		"MW_UU_RE='" + aptAutomationRegex + "'",
		"kill -TERM \"$mw_p\"",
		"mw_k=\"$mw_p:$(sed 's/.*) //' \"$MW_PROC/$mw_p/stat\" 2>/dev/null | cut -d' ' -f20)\"",
		": \"${MW_PROC:=/proc}\"",
		"  mw_apt_term\n  mw_h=$(mw_apt_holders)", // re-signal inside the wait loop
		"pgrep -f \"$MW_UU_RE\"",
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
	// The truncated comm also names the always-on shutdown helper: never match on it.
	if strings.Contains(aptQuiesce, "unattended-upgr'") || strings.Contains(aptQuiesce, "|unattended-upgr") {
		t.Errorf("aptQuiesce must not match unattended-upgrade by its truncated comm:\n%s", aptQuiesce)
	}
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

// aptInstall run for real with apt-get stubbed: a stale-index fetch failure (404 /
// "Unable to fetch some archives") refreshes once and retries once; any other
// failure is returned as-is with no retry; success needs no refresh.
func TestAptInstallRetriesFetchFailureOnce(t *testing.T) {
	bash, _ := exec.LookPath("bash")
	if bash == "" {
		t.Skip("no bash")
	}
	for name, tc := range map[string]struct {
		firstErr  string // stderr of the first failing install ("" = success)
		secondRC  string
		wantCalls string
		wantFail  bool
	}{
		"ok":              {"", "0", "install ", false},
		"404 then ok":     {"E: Failed to fetch http://x/pool/a.deb  404  Not Found [IP: 1.2.3.4 80]", "0", "install update install ", false},
		"unable then bad": {"E: Unable to fetch some archives, maybe run apt-get update or try with --fix-missing?", "100", "install update install ", true},
		"other error":     {"E: Sub-process /usr/bin/dpkg returned an error code (1)", "0", "install ", true},
	} {
		dir := t.TempDir()
		marker := filepath.ToSlash(filepath.Join(dir, "marker"))
		calls := filepath.ToSlash(filepath.Join(dir, "calls"))
		// The first install runs inside $(…), a subshell: count calls in the file.
		stub := "apt-get() { printf '%s ' \"$3\" >> '" + calls + "'; [ \"$3\" = update ] && return 0; mw_n=$(grep -o install '" + calls + "' | wc -l)\n" +
			"  if [ $mw_n -eq 1 ] && [ -n '" + tc.firstErr + "' ]; then echo '" + tc.firstErr + "' >&2; return 100; fi\n" +
			"  [ $mw_n -eq 1 ] && return 0; return " + tc.secondRC + "; }\n" +
			"apt-cache() { echo '  Candidate: 1.0'; }\n"
		p := filepath.Join(dir, "s.sh")
		s := strings.ReplaceAll(aptInstall("pkg"), AptUpdateMarker, marker)
		if err := os.WriteFile(p, []byte("set -e\n"+stub+s+"echo DONE\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// #nosec G204 -- bash comes from exec.LookPath and the arg is a temp file we wrote
		out, err := exec.CommandContext(t.Context(), bash, p).CombinedOutput()
		if (err != nil) != tc.wantFail || strings.Contains(string(out), "DONE") == tc.wantFail {
			t.Errorf("%s: err=%v wantFail=%v\n%s", name, err, tc.wantFail, out)
		}
		if tc.firstErr != "" && !strings.Contains(string(out), tc.firstErr) {
			t.Errorf("%s: captured stderr must be re-emitted:\n%s", name, out)
		}
		b, _ := os.ReadFile(filepath.FromSlash(calls)) // #nosec G304 -- t.TempDir file we wrote
		if string(b) != tc.wantCalls {
			t.Errorf("%s: apt-get calls %q, want %q", name, b, tc.wantCalls)
		}
	}
}

// A3 probed only fail2ban; a missing python3-systemd (mandatory for the systemd
// backend) then failed the install instead of healing/skipping with its name.
func TestA3CandidateProbeCoversPython3Systemd(t *testing.T) {
	if a3Packages != "fail2ban python3-systemd" {
		t.Fatalf("a3Packages = %q", a3Packages)
	}
	bash, _ := exec.LookPath("bash")
	if bash == "" {
		t.Skip("no bash")
	}
	stub := "apt-cache() { case $2 in fail2ban) echo '  Candidate: 1.1';; python3-systemd) echo '  Candidate: (none)';; esac; }\n"
	// #nosec G204 -- bash comes from exec.LookPath and the script is ours
	out, err := exec.CommandContext(t.Context(), bash, "-c", stub+aptCandidateProbeScript(a3Packages)).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "python3-systemd" {
		t.Errorf("missing = %q (err %v), want python3-systemd", out, err)
	}
}

// A6.5 installs dig through the shared apt path (prelude + candidate heal + lock
// timeout), only when dig is missing, bind9-dnsutils first, dnsutils as optional
// fallback; neither install failing may fail the script (defaults take over).
func TestDNSDigInstallScript(t *testing.T) {
	s := dnsDigInstallScript()
	if !strings.HasPrefix(s, "if ! command -v dig >/dev/null 2>&1; then\n"+aptPrelude()) || !strings.HasSuffix(s, "fi\n") {
		t.Fatalf("install must be gated on a missing dig and start with aptPrelude:\n%s", s)
	}
	if strings.Contains(dnsCalibrateScript, "apt-get") {
		t.Error("the calibration script must not carry its own apt-get path")
	}
	bash, _ := exec.LookPath("bash")
	if bash == "" {
		t.Skip("no bash")
	}
	body := s[strings.Index(s, "mw_nc_probe() {") : len(s)-len("fi\n")]
	for name, tc := range map[string]struct{ failRe, want string }{
		"real name works": {"^$", "bind9-dnsutils\n"},
		"fallback":        {"bind9-dnsutils", "bind9-dnsutils\ndnsutils\n"},
		"neither":         {".", "bind9-dnsutils\ndnsutils\n"},
	} {
		stub := "apt-cache() { echo '  Candidate: 1.0'; }\n" +
			"apt-get() { echo \"${@: -1}\" >&2; echo \"${@: -1}\" | grep -Eq '" + tc.failRe + "' && return 100; return 0; }\n"
		p := filepath.Join(t.TempDir(), "s.sh")
		if err := os.WriteFile(p, []byte("set -e\n"+stub+body), 0o600); err != nil {
			t.Fatal(err)
		}
		// #nosec G204 -- bash comes from exec.LookPath and the arg is a temp file we wrote
		out, err := exec.CommandContext(t.Context(), bash, p).CombinedOutput()
		if err != nil || string(out) != tc.want {
			t.Errorf("%s: err=%v out=%q want %q", name, err, out, tc.want)
		}
	}
}

// A stamped per-boot marker or an image without `universe` left A1's install with
// "no installation candidate". Every install must first heal that (enable universe,
// force a refresh that re-stamps the marker), never trip `set -e`, and still end in
// the install line itself.
func TestAptInstallEnsuresCandidates(t *testing.T) {
	s := aptInstall("iptables-persistent")
	g := strings.Index(s, "LC_ALL=C apt-cache policy \"$mw_pk\"")
	u := strings.Index(s, aptGet+" update >/dev/null && : > "+AptUpdateMarker+" || true")
	i := strings.Index(s, aptGet+" install -y")
	if g < 0 || u < 0 || i < 0 || g > u || u > i {
		t.Fatalf("install must probe candidates, then refresh, then install:\n%s", s)
	}
	if !strings.Contains(s, aptEnableUniverse) || strings.Contains(s, "add-apt-repository") {
		t.Errorf("must enable universe via the shared sed fragment (no software-properties dependency):\n%s", s)
	}
	if !strings.HasSuffix(s, " iptables-persistent\n") {
		t.Errorf("install line must stay last: %q", s)
	}
	if bash, _ := exec.LookPath("bash"); bash != "" {
		p := filepath.Join(t.TempDir(), "s.sh")
		if err := os.WriteFile(p, []byte("set -e\n"+aptPrelude()+s+aptInstallOptional("earlyoom")), 0o600); err != nil {
			t.Fatal(err)
		}
		// #nosec G204 -- bash comes from exec.LookPath and the arg is a temp file we wrote
		if out, err := exec.CommandContext(t.Context(), bash, "-n", p).CombinedOutput(); err != nil {
			t.Errorf("bash -n rejected the install script: %v\n%s", err, out)
		}
	}
}

// aptEnsureCandidates run for real with apt stubbed: a stale index is healed by a
// forced refresh alone (sources untouched); only a candidate still missing after it
// enables universe; a present candidate costs no refresh at all.
func TestAptEnsureCandidatesBehavior(t *testing.T) {
	bash, _ := exec.LookPath("bash")
	if bash == "" {
		t.Skip("no bash")
	}
	// needUpdates = how many `apt-get update` calls until the candidate appears.
	run := func(needUpdates int) (out, sources string) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "sources.list.d"), 0o750); err != nil {
			t.Fatal(err)
		}
		src := filepath.Join(dir, "sources.list.d", "ubuntu.sources")
		if err := os.WriteFile(src, []byte("Components: main restricted\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		stub := "MW_APT_ETC='" + filepath.ToSlash(dir) + "'\nmw_upd=0\n" +
			"apt-get() { echo \"CALL apt-get $*\" >&2; mw_upd=$((mw_upd + 1)); }\n" +
			"apt-cache() { if [ \"$mw_upd\" -ge " + strconv.Itoa(needUpdates) + " ]; then echo '  Candidate: 1.0'; else echo '  Candidate: (none)'; fi; }\n"
		p := filepath.Join(t.TempDir(), "s.sh")
		if err := os.WriteFile(p, []byte("set -e\n"+stub+aptEnsureCandidates("iptables-persistent")), 0o600); err != nil {
			t.Fatal(err)
		}
		// #nosec G204 -- bash comes from exec.LookPath and the arg is a temp file we wrote
		o, err := exec.CommandContext(t.Context(), bash, p).CombinedOutput()
		if err != nil {
			t.Fatalf("script failed: %v\n%s", err, o)
		}
		b, err := os.ReadFile(src) // #nosec G304 -- t.TempDir file we wrote
		if err != nil {
			t.Fatal(err)
		}
		return string(o), string(b)
	}
	cases := []struct {
		need, updates int
		universe      bool
	}{{0, 0, false}, {1, 1, false}, {2, 2, true}}
	for _, c := range cases {
		o, s := run(c.need)
		if n := strings.Count(o, "CALL apt-get"); n != c.updates {
			t.Errorf("need=%d: want %d refreshes, got %d:\n%s", c.need, c.updates, n, o)
		}
		if got := strings.Contains(s, "universe"); got != c.universe {
			t.Errorf("need=%d: universe enabled=%v, want %v (sources %q)", c.need, got, c.universe, s)
		}
	}
}

// aptEnableUniverse run for real against fixture sources: adds universe to the
// Ubuntu deb822 stanzas and legacy ubuntu `deb` lines, idempotently, and leaves
// comments, deb-src-less third-party lines and already-complete entries alone.
func TestAptEnableUniverseBehavior(t *testing.T) {
	bash, _ := exec.LookPath("bash")
	if bash == "" {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sources.list.d"), 0o750); err != nil {
		t.Fatal(err)
	}
	deb822 := "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu/\nSuites: resolute resolute-updates\nComponents: main restricted\n\n" +
		"Types: deb\nURIs: http://security.ubuntu.com/ubuntu/\nSuites: resolute-security\nComponents: main restricted universe\n"
	legacy := "# deb http://archive.ubuntu.com/ubuntu jammy main restricted\n" +
		"deb http://archive.ubuntu.com/ubuntu jammy main restricted\n" +
		"deb [arch=amd64] http://mirror.example.com/ubuntu jammy-security main\n" +
		"deb https://download.docker.com/linux/debian bookworm stable main\n"
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "sources.list.d", "ubuntu.sources"), deb822)
	write(filepath.Join(dir, "sources.list"), legacy)
	script := filepath.Join(t.TempDir(), "s.sh")
	write(script, "MW_APT_ETC='"+filepath.ToSlash(dir)+"'\n"+aptEnableUniverse+aptEnableUniverse)
	// #nosec G204 -- bash comes from exec.LookPath and the arg is a temp file we wrote
	if out, err := exec.CommandContext(t.Context(), bash, script).CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	read := func(p string) string {
		b, err := os.ReadFile(p) // #nosec G304 -- t.TempDir file we wrote
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got, want := read(filepath.Join(dir, "sources.list.d", "ubuntu.sources")),
		strings.Replace(deb822, "Components: main restricted\n", "Components: main restricted universe\n", 1); got != want {
		t.Errorf("deb822:\ngot  %q\nwant %q", got, want)
	}
	wantLegacy := "# deb http://archive.ubuntu.com/ubuntu jammy main restricted\n" +
		"deb http://archive.ubuntu.com/ubuntu jammy main universe restricted\n" +
		"deb [arch=amd64] http://mirror.example.com/ubuntu jammy-security main universe\n" +
		"deb https://download.docker.com/linux/debian bookworm stable main\n"
	if got := read(filepath.Join(dir, "sources.list")); got != wantLegacy {
		t.Errorf("legacy:\ngot  %q\nwant %q", got, wantLegacy)
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
