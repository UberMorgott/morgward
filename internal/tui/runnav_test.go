package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/ui"
)

// runNavModel returns a phaseRun model with a run IN FLIGHT and a live (stub) terminal
// session, so the nav-bar paths under test reuse it instead of dialing. cancelled reports
// whether the engine context was cancelled — a tab switch must never trip it.
func runNavModel() (m model, cancelled *bool) {
	m = liveWorkspaceModel(100, 40)
	m.phase = phaseRun
	m.running = true
	m.runGen = 7
	flag := false
	m.engineCancel = func() { flag = true }
	m.content = "step A8 starting\n"
	m.vp = viewport.New(viewport.WithWidth(m.vpWidth()), viewport.WithHeight(m.vpHeight()))
	m.vp.SetContent(m.wrapped())
	return m, &flag
}

// TestRunNavBarClickToTerminalKeepsRun asserts the global tab bar is LIVE during a run:
// a click on the Терминал cell switches phase without bumping runGen or cancelling the
// engine, and a click back on Главная restores the run screen with its scrollback.
func TestRunNavBarClickToTerminalKeepsRun(t *testing.T) {
	m, cancelled := runNavModel()
	gen0, content0 := m.runGen, m.content

	_, termRange, _ := m.navTabZones()
	next, _ := m.Update(tea.MouseClickMsg{X: termRange[0] + 1, Y: switcherRow, Button: tea.MouseLeft})
	mm := asModel(t, next)

	if mm.phase != phaseTerminal || mm.wsTab != wsTerminal {
		t.Fatalf("Терминал click during a run → phase=%v wsTab=%v, want phaseTerminal/wsTerminal", mm.phase, mm.wsTab)
	}
	if *cancelled {
		t.Fatal("switching tabs during a run cancelled the engine context")
	}
	if mm.runGen != gen0 {
		t.Fatalf("tab switch bumped runGen %d→%d — the run's listeners would be orphaned", gen0, mm.runGen)
	}
	if !mm.running || mm.finished {
		t.Fatalf("tab switch changed the run state: running=%v finished=%v", mm.running, mm.finished)
	}
	if mm.content != content0 {
		t.Fatalf("tab switch lost the run scrollback: %q", mm.content)
	}
	// The workspace's exit target is the RUN screen while the run is in flight.
	if mm.termReturn != phaseRun {
		t.Fatalf("termReturn=%v, want phaseRun (home is the run screen while in flight)", mm.termReturn)
	}

	// Click back on Главная → the run screen returns, scrollback intact. Routed through
	// the hit-test + navTo directly: the full Update path on phaseTerminal would touch the
	// stub term session's emulator (nil in this fixture) before reaching the bar.
	homeRange, _, _ := mm.navTabZones()
	target, ok := mm.navTabAtClick(homeRange[0]+1, switcherRow)
	if !ok || target != navHome {
		t.Fatalf("click on the Главная cell resolved to %v ok=%v, want navHome", target, ok)
	}
	back, _ := mm.navTo(target)
	bm := asModel(t, back)
	if bm.phase != phaseRun {
		t.Fatalf("Главная click during a run → phase=%v, want phaseRun", bm.phase)
	}
	if bm.content != content0 {
		t.Fatalf("returning to the run screen lost the scrollback: %q", bm.content)
	}
	if bm.runGen != gen0 || !bm.running || *cancelled {
		t.Fatalf("returning home disturbed the run: runGen=%d running=%v cancelled=%v", bm.runGen, bm.running, *cancelled)
	}
}

// TestRunViewDrawsNavBar asserts the run frame actually renders the 3-cell bar on the
// switcher row with Главная active (the hit-test above only means something if the bar
// is drawn there).
func TestRunViewDrawsNavBar(t *testing.T) {
	m, _ := runNavModel()
	rows := strings.Split(m.runView(), "\n")
	if len(rows) <= switcherRow {
		t.Fatalf("run frame has %d rows, no switcher row", len(rows))
	}
	row := ui.StripControlAndANSI(rows[switcherRow])
	for _, want := range []string{t2(m.lang, kNavHome), t2(m.lang, kFmTabTerminal), t2(m.lang, kFmTabFiles)} {
		if !strings.Contains(row, want) {
			t.Fatalf("run switcher row %q missing nav cell %q", row, want)
		}
	}
	if got := m.navActiveTab(); got != navHome {
		t.Fatalf("active nav cell during a run = %v, want navHome", got)
	}
}

// TestHomePhaseFollowsRun asserts homePhase (the Главная target + the workspace exit
// target) is the run screen only while a run is in flight.
func TestHomePhaseFollowsRun(t *testing.T) {
	m := newModel()
	if got := m.homePhase(); got != phaseDashboard {
		t.Fatalf("idle homePhase=%v, want phaseDashboard", got)
	}
	m.running = true
	if got := m.homePhase(); got != phaseRun {
		t.Fatalf("in-flight homePhase=%v, want phaseRun", got)
	}
	m.finished = true
	if got := m.homePhase(); got != phaseDashboard {
		t.Fatalf("finished homePhase=%v, want phaseDashboard", got)
	}
}
