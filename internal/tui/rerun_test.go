package tui

import (
	"testing"

	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/monitor"
	"github.com/UberMorgott/morgward/internal/steps"
	"github.com/UberMorgott/morgward/internal/tweaks"
)

// The background dashboard audit runs while the alt-screen owns the terminal, so its
// hooks MUST carry a sink — a nil one makes ui.Logger print over the TUI frame.
func TestAuditHooksDiscardTheLogStream(t *testing.T) {
	var sum engine.Summary
	h := auditHooks(&sum)
	if h.Sink == nil {
		t.Fatal("bgAudit hooks must set a Sink (nil ⇒ ui.Logger prints over the TUI)")
	}
	h.Sink("noise") // must not panic and must not reach stdout
	if h.OnConnect != nil {
		t.Error("the background audit must not start a sampler via OnConnect")
	}
	h.OnProgress(engine.Progress{Done: true, Summary: engine.Summary{OK: 7}})
	if sum.OK != 7 {
		t.Errorf("OnProgress must capture the final summary, got %+v", sum)
	}
}

// Leaving to the connect form must retire every in-flight message of the session we
// are leaving: a cancelled engine.Execute still RETURNS, so its bgAuditMsg would
// otherwise pass the generation check and repopulate the old server's facts.
func TestGoBackRetiresInFlightBgAudit(t *testing.T) {
	m := dashModel(80, 24)
	gen := m.runGen
	back, _ := m.goBack()
	n := asModel(t, back)
	if n.runGen == gen {
		t.Fatalf("goBack must bump runGen (still %d)", gen)
	}
	if n.dashFacts != nil {
		t.Fatal("goBack must clear dashFacts")
	}
	late := genMsg{gen, bgAuditMsg{sum: engine.Summary{Facts: &detect.Facts{ID: "old-server"}}}}
	out, _ := n.Update(late)
	if got := asModel(t, out); got.dashFacts != nil {
		t.Errorf("a stale bgAuditMsg repopulated the old server's facts: %+v", got.dashFacts)
	}
}

// A re-run that skips goBack (retry via `r`, apply-tweaks, startSteps, startRevert)
// must get FRESH channels, not just a fresh generation: a listener parked on the
// shared channel from the previous run would otherwise eat the one-shot connMsg and
// tag it with the old generation, so Update drops it and the retry never starts the
// monitor or captures the generated key.
func TestLaunchEngineGivesEachRunItsOwnChannels(t *testing.T) {
	m := formModel(80, 24)
	m.inputs[fHost].SetValue("127.0.0.1")
	m.inputs[fPort].SetValue("1") // refused instantly: the engine goroutine fails fast
	m.inputs[fPass].SetValue("x")
	m.command = "audit"
	cfg, err := m.formConfig()
	if err != nil {
		t.Fatalf("formConfig: %v", err)
	}
	oldGen, oldConn, oldLogs, oldProg, oldDone := m.runGen, m.connCh, m.logs, m.progCh, m.done

	next, _ := m.launchEngine(cfg)
	n := asModel(t, next)
	defer n.engineCancel()

	if n.runGen == oldGen {
		t.Errorf("launchEngine must bump runGen (still %d)", oldGen)
	}
	if n.connCh == oldConn || n.logs == oldLogs || n.progCh == oldProg || n.done == oldDone {
		t.Fatal("launchEngine must not reuse the previous run's channels")
	}
	// The one-shot connMsg reaches the NEW generation's listener.
	n.connCh <- monitor.ConnInfo{Host: "127.0.0.1"}
	got, ok := n.listenConn()().(genMsg)
	if !ok || got.gen != n.runGen {
		t.Fatalf("connMsg arrived as %#v, want gen %d", got, n.runGen)
	}
	if _, ok := got.msg.(connMsg); !ok {
		t.Fatalf("payload = %T, want connMsg", got.msg)
	}
}

// RunRevert reports StatusOK for a tweak it REMOVED, so folding that OK in as
// Applied=true showed reverted tweaks as applied on the Dashboard. The sign must
// follow the command.
func TestMarkStepsAppliedFollowsTheCommand(t *testing.T) {
	base := dashModel(80, 24)
	base.dashAuditResults = []tweaks.Result{
		{Probe: tweaks.Probe{ID: "a4.bbr", Step: "A4"}, Applied: true},
		{Probe: tweaks.Probe{ID: "a6.zram", Step: "A6.7"}, Applied: false},
	}
	sum := engine.Summary{Results: []engine.StepResult{{ID: "A4", Status: steps.StatusOK}}}

	rev := base
	rev.command = "revert"
	if got := rev.markStepsApplied(sum).dashAuditResults[0].Applied; got {
		t.Error("a reverted step's probes must not stay applied")
	}
	app := base
	app.command = "step"
	if got := app.markStepsApplied(sum).dashAuditResults[0].Applied; !got {
		t.Error("an applied step's probes must be marked applied")
	}
	// Untouched steps keep whatever the last audit found, either way.
	if rev.markStepsApplied(sum).dashAuditResults[1].Applied {
		t.Error("a step the run never touched must be left alone")
	}
}
