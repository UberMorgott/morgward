package steps

import (
	"strings"
	"testing"

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
	if !strings.Contains(req, " install -y auditd\n") {
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
