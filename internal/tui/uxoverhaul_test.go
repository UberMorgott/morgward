package tui

import (
	"os"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/stats"
	"github.com/UberMorgott/morgward/internal/steps"
)

// --- CHANGE 1: apply-confirm centered modal -----------------------------------

// TestApplyConfirmModalRenders asserts that with dashApplyConfirm armed the
// dashboard renders the centered modal (title + body + reboot warning + buttons)
// instead of the normal dashboard chrome.
func TestApplyConfirmModalRenders(t *testing.T) {
	m := dashModel(100, 40)
	m.dashApplyConfirm = true
	out := m.dashboardView()
	for _, want := range []string{
		t2(m.lang, kApplyModalTitle),
		t2(m.lang, kApplyModalConfirm), // confirm pill label
		t2(m.lang, kApplyModalCancel),  // cancel pill label
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("apply-confirm modal missing %q\n%s", want, out)
		}
	}
	// BUG 2: the two pills must be SEPARATE, with a visible plain gap between them —
	// the confirm label's pill must end (reset) before the cancel label begins, so
	// there are >=2 plain spaces between the two labels on the buttons line.
	confirm := t2(m.lang, kApplyModalConfirm)
	cancel := t2(m.lang, kApplyModalCancel)
	ci := strings.Index(out, confirm)
	xi := strings.Index(out, cancel)
	if ci < 0 || xi <= ci {
		t.Fatalf("confirm/cancel labels not ordered in output")
	}
	between := out[ci+len(confirm) : xi]
	if !strings.Contains(between, "   ") {
		t.Fatalf("no plain 3-space gap between confirm and cancel pills; between=%q", between)
	}
	// The bucket includes A8, so the reboot warning must be present. The warning is
	// word-wrapped, so assert on a distinctive non-wrapping token (the ⚠ marker).
	if bucketHasA8(tweakBucketIDs()) && !strings.Contains(out, "⚠") {
		t.Fatalf("apply-confirm modal missing the A8 reboot warning marker")
	}
}

// --- BUG 1: stale completion state on a second run ----------------------------

// TestResetRunStateClearsCompletion asserts resetRunState() zeroes every field that
// gates the finished tail / summary line / progress bar, so a second run cannot
// inherit the first run's "✓ done / запуск завершён" indicators.
func TestResetRunStateClearsCompletion(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	// Simulate a just-finished prior run carrying all the stale completion flags.
	m.finished = true
	m.finalErr = nil
	m.haveSummary = true
	m.summary = engine.Summary{OK: 9, Results: []engine.StepResult{{ID: "A1", Status: steps.StatusOK}}}
	m.index, m.total = 9, 9
	m.curID, m.curTitle = "A1", "Firewall"
	m.dashAuditRunning, m.dashAuditDone = true, true
	m.dashAuditTotal, m.dashAuditApplied = 9, 9
	m.content = "old log line\n"

	m = m.resetRunState()

	if m.finished || m.haveSummary || m.finalErr != nil {
		t.Fatalf("resetRunState left completion flags set: finished=%v haveSummary=%v finalErr=%v", m.finished, m.haveSummary, m.finalErr)
	}
	if m.index != 0 || m.total != 0 || m.curID != "" || m.curTitle != "" {
		t.Fatalf("resetRunState left progress state: index=%d total=%d curID=%q curTitle=%q", m.index, m.total, m.curID, m.curTitle)
	}
	if m.summary.Total() != 0 {
		t.Fatalf("resetRunState left a non-empty summary: %d results", m.summary.Total())
	}
	if m.dashAuditRunning || m.dashAuditDone || m.dashAuditTotal != 0 || m.dashAuditApplied != 0 {
		t.Fatalf("resetRunState left dash audit flags set")
	}
	if m.content != "" {
		t.Fatalf("resetRunState left stale log content: %q", m.content)
	}
}

// TestSecondRunNoStaleFinishedTail is the render-level guard for BUG 1: after a run
// finishes (finished + haveSummary + summary), the per-run reset must leave the run
// view with NO completion indicator — no finished tail ("запуск завершён"/"run
// finished"), no "✓ done" summary line — until this run's own completion lands. It
// renders runView in the running state AFTER resetRunState, mirroring exactly what
// launchEngine sets up (resetRunState → phaseRun → running=true), without spawning the
// engine goroutine (no network).
func TestSecondRunNoStaleFinishedTail(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseRun
	// Stale finished state from a prior run.
	m.finished = true
	m.haveSummary = true
	m.summary = engine.Summary{OK: 5, Results: []engine.StepResult{{ID: "A1", Status: steps.StatusOK}}}

	// Apply the same reset launchEngine performs, then enter the running state.
	m = m.resetRunState()
	m.running = true
	m.vp = viewportForTest(&m)

	if m.finished || m.haveSummary {
		t.Fatalf("reset left completion flags: finished=%v haveSummary=%v", m.finished, m.haveSummary)
	}
	out := m.runView()
	if strings.Contains(out, t2(m.lang, kFinishedOK)) {
		t.Fatalf("running view shows the stale finished tail %q\n%s", t2(m.lang, kFinishedOK), out)
	}
	if strings.Contains(out, t2(m.lang, kDoneWord)) {
		t.Fatalf("running view shows the stale summary 'done' word\n%s", out)
	}
}

// viewportForTest builds a viewport sized for the model so runView can render without
// a real WindowSizeMsg. It mirrors launchEngine's viewport construction.
func viewportForTest(m *model) viewport.Model {
	vp := viewport.New(viewport.WithWidth(m.vpWidth()), viewport.WithHeight(m.vpHeight()))
	vp.SetContent("")
	return vp
}

// --- CHANGE 2: pre-run generated-key modal ------------------------------------

// TestStartPasswordPathShowsPreRunKey asserts that a mutating command on the
// password path (no --key) pre-generates a key and shows the pre-run key modal
// (phaseKey, keyPreRun=true) BEFORE the engine launches — the run is not started yet.
func TestStartPasswordPathShowsPreRunKey(t *testing.T) {
	m := formModel(100, 40)
	m.inputs[fHost].SetValue("1.2.3.4")
	m.inputs[fPass].SetValue("secret")
	m.inputs[fKey].SetValue("")
	m.command = "run"

	next, _ := m.start()
	mm := next.(model)
	if mm.phase != phaseKey {
		t.Fatalf("password-path run did not show the pre-run key modal; phase=%v", mm.phase)
	}
	if !mm.keyPreRun {
		t.Fatalf("pre-run key modal: keyPreRun=false, want true")
	}
	if mm.pendingKey == nil || mm.keyPEM == "" {
		t.Fatalf("pre-run key modal: no pre-generated key staged (pendingKey=%v keyPEM empty=%v)", mm.pendingKey, mm.keyPEM == "")
	}
	if !mm.keyGenerated {
		t.Fatalf("pre-run key modal: keyGenerated=false, want true")
	}
	// The run must NOT have started yet: no engine cancel armed, still idle title.
	if mm.running {
		t.Fatalf("pre-run key modal must not start the run; running=true")
	}
}

// TestStartKeyPathSkipsPreRunKey asserts that on the --key path start() does NOT
// take the keygen / pre-run-key branch (never flash the operator their own key). To
// avoid a real network dial in the unit test, it points KeyPath at a NONEXISTENT
// file: start() then returns the key-not-found error early — which is reachable only
// because cfg.KeyPath != "" routed it down the --key path, NOT the password keygen
// branch. Either way the key modal must never appear and no key is staged.
func TestStartKeyPathSkipsPreRunKey(t *testing.T) {
	missing := t.TempDir() + "/does-not-exist-id_ed25519"
	_ = os.Remove(missing)
	m := formModel(100, 40)
	m.inputs[fHost].SetValue("1.2.3.4")
	m.inputs[fPass].SetValue("")
	m.inputs[fKey].SetValue(missing)
	m.command = "run"

	next, _ := m.start()
	mm := next.(model)
	if mm.phase == phaseKey {
		t.Fatalf("--key path wrongly showed the key modal")
	}
	if mm.keyPreRun || mm.pendingKey != nil {
		t.Fatalf("--key path staged a generated key; keyPreRun=%v pendingKey=%v", mm.keyPreRun, mm.pendingKey)
	}
	if mm.errMsg == "" {
		t.Fatalf("--key path with a missing key file should surface a key-not-found error")
	}
}

// TestMutatingCmd asserts only the mutating commands (which generate an ephemeral
// key on the password path) trigger the pre-run key modal. The read-only audit/
// detect/verify never generate a key, so they must NOT.
func TestMutatingCmd(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		want bool
	}{
		{"run", true},
		{"step", true},
		{"revert", true},
		{"audit", false},
		{"detect", false},
		{"verify", false},
		{"", false},
	} {
		if got := mutatingCmd(c.cmd); got != c.want {
			t.Fatalf("mutatingCmd(%q)=%v want %v", c.cmd, got, c.want)
		}
	}
}

// TestPreRunKeyEscAborts asserts Esc on the pre-run key modal aborts back to the
// form without launching the engine.
func TestPreRunKeyEscAborts(t *testing.T) {
	m := formModel(100, 40)
	m.inputs[fHost].SetValue("1.2.3.4")
	m.inputs[fPass].SetValue("secret")
	m.command = "run"
	next, _ := m.start()
	mm := next.(model)
	if mm.phase != phaseKey || !mm.keyPreRun {
		t.Fatalf("precondition: expected pre-run key modal")
	}
	n2, _ := mm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m2 := n2.(model)
	if m2.phase != phaseForm {
		t.Fatalf("Esc on pre-run key modal → phase %v, want phaseForm", m2.phase)
	}
	if m2.pendingKey != nil {
		t.Fatalf("Esc on pre-run key modal left a staged key")
	}
	if m2.stopSample != nil {
		m2.stopSample()
	}
}

// --- CHANGE 4: two-column summary ---------------------------------------------

// summaryModel builds a phaseSummary model with a couple of fixes + an After
// snapshot, sized for two-column layout/hit-test tests.
func summaryModel(w, h int) model {
	m := newModel()
	m.w, m.h = w, h
	m.host = "1.2.3.4"
	m.phase = phaseSummary
	m.finished = true
	m.haveSummary = true
	m.summary = engine.Summary{
		Results: []engine.StepResult{
			{ID: "A1", Title: "Firewall", Status: steps.StatusOK},
			{ID: "A4", Title: "Network", Status: steps.StatusOK},
		},
		Before: &stats.Snapshot{MemKB: 2000000, GatewayPingMs: 1.2, InternetPingMs: 12.0, RootLogin: "yes"},
		After:  &stats.Snapshot{MemKB: 2000000, GatewayPingMs: 1.1, InternetPingMs: 11.0, RootLogin: "yes", FirewallActive: true},
	}
	return m
}

// TestSummaryTwoColumnRenders asserts the summary renders both column headers, the
// RAM stat, and the pinned home button, with exactly m.h rows.
func TestSummaryTwoColumnRenders(t *testing.T) {
	m := summaryModel(100, 40)
	if !m.summaryTwoCol(innerWidth(m.boxWidth())) {
		t.Fatalf("at width 100 the summary should be two-column")
	}
	out := m.summaryView()
	for _, want := range []string{
		t2(m.lang, kSumColFixes),   // left header
		t2(m.lang, kSecColTitle),   // right header
		t2(m.lang, kSumRAM),        // RAM in the stats strip / disk-mem group
		t2(m.lang, kSumHomeButton), // pinned home button
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summaryView missing %q\n%s", want, out)
		}
	}
	if got := strings.Count(out, "\n") + 1; got != m.h {
		t.Fatalf("summaryView rendered %d rows, want %d", got, m.h)
	}
}

// TestSummaryFixClickTwoColumn asserts a click on a left-column fix row resolves to
// that fix's ID in the two-column layout.
func TestSummaryFixClickTwoColumn(t *testing.T) {
	m := summaryModel(100, 40)
	// The first fix sits at the first grid row after the ФИКСЫ header.
	gridStart := m.summaryColBlockStart()
	// In two-column mode grid row r holds left[r]; left[0]=header, left[1]=fix0.
	y := summaryBodyTopRow + (gridStart - clampScroll(m.sumScroll, len(m.summaryBodyLines()), m.summaryBodyViewH())) + 1
	id, ok := m.fixAtClick(4, y)
	if !ok || id != "A1" {
		t.Fatalf("fix click → %q,%v want A1,true (y=%d)", id, ok, y)
	}
}

// TestSummaryHomeButtonClick asserts the pinned home button hit-tests and navigates
// home (to the form when not connected to a dashboard).
func TestSummaryHomeButtonClick(t *testing.T) {
	m := summaryModel(100, 40)
	row := m.summaryHomeRow()
	if !m.summaryHomeAtClick(3, row) {
		t.Fatalf("home button click at its row did not hit")
	}
	if m.summaryHomeAtClick(3, row+1) {
		t.Fatalf("click one row below the home button wrongly hit it")
	}
	next, _ := m.summaryGoHome()
	mm := next.(model)
	if mm.phase != phaseForm {
		t.Fatalf("home (no dashboard facts) → phase %v, want phaseForm", mm.phase)
	}
	if mm.stopSample != nil {
		mm.stopSample()
	}
}

// TestSummaryKeyShowRow asserts that when a key was generated this run the right
// column carries a clickable "key show" row that opens the key viewer (read-only).
func TestSummaryKeyShowRow(t *testing.T) {
	m := summaryModel(120, 40)
	m.keyGenerated = true
	m.keyPEM = "PRIVATE-KEY-PEM"
	_, keyShowIdx := m.summaryAccessRows()
	if keyShowIdx < 0 {
		t.Fatalf("expected a clickable key-show row when a key was generated")
	}
	out := m.summaryView()
	if !strings.Contains(out, t2(m.lang, kSumKeyShow)) {
		t.Fatalf("summaryView missing the key-show row\n%s", out)
	}
	// Find the screen Y of the key-show row in the two-column block (right column,
	// rightIdx = 1 + keyShowIdx) and click it.
	gridStart := m.summaryColBlockStart()
	off := clampScroll(m.sumScroll, len(m.summaryBodyLines()), m.summaryBodyViewH())
	rightIdx := 1 + keyShowIdx
	y := summaryBodyTopRow + (gridStart + rightIdx - off)
	innerW := innerWidth(m.boxWidth())
	colW := (innerW - sumColGap) / 2
	rightX := 2 + colW + sumColGap + 1
	if !m.summaryKeyShowAtClick(rightX, y) {
		t.Fatalf("key-show row click did not hit (y=%d rightX=%d)", y, rightX)
	}
	next, _ := m.Update(mouseClickAt(rightX, y))
	mm := next.(model)
	if mm.phase != phaseKey {
		t.Fatalf("key-show click → phase %v, want phaseKey", mm.phase)
	}
	if mm.keyPreRun {
		t.Fatalf("key-show viewer must be read-only (keyPreRun=false)")
	}
	if mm.keyReturn != phaseSummary {
		t.Fatalf("key-show viewer keyReturn=%v want phaseSummary", mm.keyReturn)
	}
}

// TestSummarySingleColumnFallback asserts a narrow terminal stacks the summary into
// a single column and the fix click still resolves.
func TestSummarySingleColumnFallback(t *testing.T) {
	m := summaryModel(56, 40)
	if m.summaryTwoCol(innerWidth(m.boxWidth())) {
		t.Fatalf("at width 56 the summary should stack to one column")
	}
	out := m.summaryView()
	if got := strings.Count(out, "\n") + 1; got != m.h {
		t.Fatalf("narrow summaryView rendered %d rows, want %d", got, m.h)
	}
	// Stacked: grid row gridStart=header, gridStart+1 = fix0.
	gridStart := m.summaryColBlockStart()
	off := clampScroll(m.sumScroll, len(m.summaryBodyLines()), m.summaryBodyViewH())
	y := summaryBodyTopRow + (gridStart + 1 - off)
	id, ok := m.fixAtClick(4, y)
	if !ok || id != "A1" {
		t.Fatalf("stacked fix click → %q,%v want A1,true (y=%d)", id, ok, y)
	}
}
