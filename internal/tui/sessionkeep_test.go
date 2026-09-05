package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/steps"
	"github.com/UberMorgott/morgward/internal/tweaks"
)

// failedRunModel is a finished-with-error run screen on top of a live session (an
// audit already populated the Dashboard), which is the state a failed "apply tweaks"
// leaves behind.
func failedRunModel(err error) model {
	m := dashModel(100, 40)
	m.phase = phaseRun
	m.command = "step"
	m.cmds = []string{"A4"}
	m.finished = true
	m.finalErr = err
	m.inputs[fHost].SetValue("10.0.0.9")
	m.inputs[fUser].SetValue("root")
	m.inputs[fPass].SetValue("hunter2")
	return m
}

// TestStepErrorKeepsTheSession is U3: a step/engine error used to fall through to
// goBack(), tearing the session down and dumping the operator on the connect form to
// retype credentials. A non-transport failure must keep the session and land on the
// Dashboard with the failure detail on screen.
func TestStepErrorKeepsTheSession(t *testing.T) {
	m := failedRunModel(errors.New("A4: sysctl -p failed"))
	next, _ := m.runExitHome()
	mm := asModel(t, next)
	if mm.phase != phaseDashboard {
		t.Fatalf("failed step → phase %v, want phaseDashboard (session kept)", mm.phase)
	}
	if !strings.Contains(mm.dashNotice, "sysctl -p failed") {
		t.Fatalf("dashNotice = %q, want the failure detail", mm.dashNotice)
	}
	if !strings.Contains(stripBlanks(mm.dashboardView()), stripBlanks("sysctl -p failed")) {
		t.Fatal("the failure notice is not rendered on the Dashboard")
	}
	if mm.dashFacts == nil {
		t.Fatal("session facts dropped on a non-transport failure")
	}
}

// TestTransportLossReturnsToFormPrefilled is the other half of U3: a real transport /
// credential loss DOES need a new connection — but the form must come back filled in,
// not blank, so the operator does not retype host, user and password.
func TestTransportLossReturnsToFormPrefilled(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("reconnect after reboot: %w", sshx.ErrHostKeyChanged),
		sshx.ErrRebootAuthFailed,
	} {
		m := failedRunModel(err)
		next, _ := m.runExitHome()
		mm := asModel(t, next)
		if mm.phase != phaseForm {
			t.Fatalf("%v → phase %v, want phaseForm", err, mm.phase)
		}
		if got := mm.inputs[fHost].Value(); got != "10.0.0.9" {
			t.Fatalf("host field = %q after reconnect-required error, want it prefilled", got)
		}
		if got := mm.inputs[fUser].Value(); got != "root" {
			t.Fatalf("user field = %q, want it prefilled", got)
		}
	}
}

// TestErrorWithoutSessionGoesToForm: a failure BEFORE we ever had a session (bad
// password on the very first connect) still belongs on the form.
func TestErrorWithoutSessionGoesToForm(t *testing.T) {
	m := failedRunModel(errors.New("ssh: unable to authenticate"))
	m.dashFacts = nil
	next, _ := m.runExitHome()
	if p := asModel(t, next).phase; p != phaseForm {
		t.Fatalf("failure with no session → phase %v, want phaseForm", p)
	}
}

// TestBackAfterApplyIsInstantThenRefreshes is U1: pressing Back after applying tweaks
// used to re-run a full audit in the foreground (visible reconnect + progress screen).
// Now the Dashboard comes back IMMEDIATELY, marked up from the run's own step results
// (StatusOK steps only), and the honest audit lands later and overrides it.
func TestBackAfterApplyIsInstantThenRefreshes(t *testing.T) {
	m := dashModel(100, 40)
	m.phase = phaseSummary
	m.haveSummary = true
	m.dashStale = true
	m.summary = engine.Summary{Results: []engine.StepResult{
		{ID: "A6.7", Status: steps.StatusOK},
		{ID: "A4", Status: steps.StatusFail},
	}}
	// Pre-state: the A6.7 probes are not applied, A4's is.
	next, _ := m.summaryGoHome()
	mm := asModel(t, next)

	if mm.phase != phaseDashboard {
		t.Fatalf("Back → phase %v, want phaseDashboard with no visible audit run", mm.phase)
	}
	if mm.dashStale {
		t.Fatal("dashStale still set after the refresh was launched")
	}
	for _, r := range mm.dashAuditResults {
		if r.Probe.Step == "A6.7" && !r.Applied {
			t.Fatalf("probe %s: StatusOK step not marked applied", r.Probe.ID)
		}
	}
	if mm.dashAuditApplied != 3 {
		t.Fatalf("applied tally = %d, want 3 (1 pre-applied + 2 from the OK step)", mm.dashAuditApplied)
	}

	// The background audit lands: its verdicts REPLACE the optimistic marks.
	honest := engine.Summary{
		Facts: &detect.Facts{ID: "ubuntu", VersionID: "24.04"},
		Tweaks: []tweaks.Result{
			{Probe: tweaks.Probe{ID: "A4-bbr", Step: "A4", Name: "BBR"}, Applied: true},
			{Probe: tweaks.Probe{ID: "A6.7-zram", Step: "A6.7", Name: "zram"}, Applied: false},
		},
	}
	next2, _ := mm.Update(genMsg{mm.runGen, bgAuditMsg{sum: honest}})
	m2 := asModel(t, next2)
	if m2.dashAuditTotal != 2 || m2.dashAuditApplied != 1 {
		t.Fatalf("background audit did not override: applied %d of %d, want 1 of 2",
			m2.dashAuditApplied, m2.dashAuditTotal)
	}
	if m2.phase != phaseDashboard {
		t.Fatalf("background audit changed the phase to %v", m2.phase)
	}
}

// TestFailedStepDoesNotMarkItsProbes: markStepsApplied must stay grounded — only steps
// the engine reported StatusOK may flip a probe.
func TestFailedStepDoesNotMarkItsProbes(t *testing.T) {
	m := dashModel(100, 40)
	m.dashAuditResults[0].Applied = false // A4-bbr
	out := m.markStepsApplied(engine.Summary{Results: []engine.StepResult{
		{ID: "A4", Status: steps.StatusFail},
		{ID: "A6.7", Status: steps.StatusSkip},
	}})
	for _, r := range out.dashAuditResults {
		if r.Applied {
			t.Fatalf("probe %s marked applied off a non-OK step", r.Probe.ID)
		}
	}
}

// TestProbeErrorRendersUnknown covers the "?" state: a probe whose CHECK failed is
// neither applied nor "can apply", it is unknown — and the Security screen shows the
// raw failure instead of asserting an access posture nobody observed.
func TestProbeErrorRendersUnknown(t *testing.T) {
	m := dashModel(100, 40)
	m.dashAuditResults[1].Err = "ssh: command timed out"
	if got := m.dashAuditCellText(m.dashAuditResults[1], 40); !strings.Contains(got, "?") {
		t.Fatalf("cell = %q, want a ? glyph", got)
	}
	line := stripBlanks(m.dashStatusLine(120))
	if !strings.Contains(line, "?") {
		t.Fatalf("status line %q carries no unknown legend", line)
	}
	// 3 rows, 1 applied, 1 unknown ⇒ only 1 can be applied.
	if !strings.Contains(line, stripBlanks(fmt.Sprintf(t2(m.lang, kDashCanApply), 1))) {
		t.Fatalf("status line %q must not count the unknown row as applicable", line)
	}

	m.dashAuditRaw = []tweaks.Result{
		{Probe: tweaks.Probe{ID: "a2.permitroot", Step: "A2"}, Err: "sshd -T: exit status 255"},
	}
	m.populateSecurityState()
	if !strings.Contains(m.secRootLoginState, "sshd -T: exit status 255") {
		t.Fatalf("security row = %q, want the raw probe error", m.secRootLoginState)
	}
}
