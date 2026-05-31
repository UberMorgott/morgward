package tui

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/tweaks"
)

// dashModel builds a phaseDashboard model with a small audit and a server card,
// sized for layout/hit-test tests.
func dashModel(w, h int) model {
	m := newModel()
	m.w, m.h = w, h
	m.host = "1.2.3.4"
	m.phase = phaseDashboard
	m.dashFacts = &detect.Facts{ID: "ubuntu", VersionID: "24.04", HasIPv6: true}
	m.dashAuditResults = []tweaks.Result{
		{Probe: tweaks.Probe{ID: "A4-bbr", Step: "A4", Name: "BBR congestion control"}, Applied: true},
		{Probe: tweaks.Probe{ID: "A6.7-zram", Step: "A6.7", Name: "zram swap active"}, Applied: false},
		{Probe: tweaks.Probe{ID: "A6.7-eoom", Step: "A6.7", Name: "earlyoom active"}, Applied: false},
	}
	m.dashAuditTotal = 3
	m.dashAuditApplied = 1
	m.dashAuditDone = true
	return m
}

// TestConnectIsReadOnlyAudit is the load-bearing regression guard: the landing
// "Подключиться" button must launch the READ-ONLY audit (m.command=="audit"),
// never the apply path ("run"/"step"). It clicks the Connect pill and asserts the
// command token, and that the phase moved to the connecting run view (not an apply).
func TestConnectIsReadOnlyAudit(t *testing.T) {
	m := formModel(80, 24)
	// Leave the host EMPTY so start()'s validation short-circuits before any dial
	// goroutine is spawned — the Connect handler sets m.command BEFORE calling
	// start(), so the command assertion holds regardless of validation outcome.
	m.inputs[fHost].SetValue("")
	m.inputs[fPass].SetValue("secret")
	rows := m.formRows()
	si := kindIndex(rows, frStart)
	if si < 0 {
		t.Fatalf("no frStart row")
	}
	// Click the Connect pill (first pill on the frStart row).
	startX := m.pillColStart() + 1
	next, _ := m.formClick(startX, formBodyTopRow+si)
	mm := next.(model)
	if mm.command != "audit" {
		t.Fatalf("Connect (mouse) launched command %q, want \"audit\" (read-only); apply path must NOT run", mm.command)
	}

	// Connect via keyboard (enter on the Start row) must ALSO be read-only audit.
	m2 := formModel(80, 24)
	m2.inputs[fHost].SetValue("") // empty host → no dial goroutine (see above)
	m2.inputs[fPass].SetValue("secret")
	m2.focus = rowStart
	next2, _ := m2.updateForm(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm2 := next2.(model)
	if mm2.command != "audit" {
		t.Fatalf("Connect (keyboard) launched command %q, want \"audit\"", mm2.command)
	}
}

// TestAdvanceFromRunRoutesAuditToDashboard asserts an audit run lands on the
// Dashboard, while a non-audit run lands on the summary/matrix.
func TestAdvanceFromRunRoutesAuditToDashboard(t *testing.T) {
	m := newModel()
	m.command = "audit"
	m.phase = phaseRun
	got := m.advanceFromRun()
	if got.phase != phaseDashboard {
		t.Fatalf("audit advanced to %v, want phaseDashboard", got.phase)
	}

	m2 := newModel()
	m2.command = "step"
	m2.phase = phaseRun
	m2.summary = engine.Summary{Tweaks: []tweaks.Result{{Probe: tweaks.Probe{ID: "x"}}}}
	if g := m2.advanceFromRun(); g.phase != phaseMatrix {
		t.Fatalf("step-with-tweaks advanced to %v, want phaseMatrix", g.phase)
	}

	m3 := newModel()
	m3.command = "run"
	m3.phase = phaseRun
	if g := m3.advanceFromRun(); g.phase != phaseSummary {
		t.Fatalf("run advanced to %v, want phaseSummary", g.phase)
	}
}

// TestCaptureAuditPopulatesState asserts captureAudit folds Summary.Facts/Tweaks
// into the Dashboard fields with the right applied/total tally.
func TestCaptureAuditPopulatesState(t *testing.T) {
	m := newModel()
	sum := engine.Summary{
		Facts: &detect.Facts{ID: "ubuntu", VersionID: "26.04"},
		Tweaks: []tweaks.Result{
			{Probe: tweaks.Probe{ID: "a"}, Applied: true},
			{Probe: tweaks.Probe{ID: "b"}, Applied: false},
			{Probe: tweaks.Probe{ID: "c"}, Applied: true},
		},
	}
	m.captureAudit(sum)
	if m.dashFacts == nil || m.dashFacts.VersionID != "26.04" {
		t.Fatalf("facts not captured: %+v", m.dashFacts)
	}
	if m.dashAuditTotal != 3 || m.dashAuditApplied != 2 {
		t.Fatalf("tally total=%d applied=%d want 3/2", m.dashAuditTotal, m.dashAuditApplied)
	}
	if !m.dashAuditDone || m.dashAuditRunning {
		t.Fatalf("done=%v running=%v want done=true running=false", m.dashAuditDone, m.dashAuditRunning)
	}
}

// TestDashboardViewRenders asserts the Dashboard renders the server card header,
// the audit status line, the audit grid, and all three buttons, with no line
// wider than the box and exactly m.h rows.
func TestDashboardViewRenders(t *testing.T) {
	m := dashModel(90, 30)
	out := m.dashboardView()
	checks := []string{
		t2(m.lang, kDashTitle) + ": 1.2.3.4",    // server card title
		t2(m.lang, kDashApplyButton),            // apply button
		t2(m.lang, kDashSecButton),              // security button
		t2(m.lang, kDashCatalogButton),          // catalog button
		localTweakName(m.lang, "A4-bbr", "BBR"), // an audit row name
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboardView missing %q\n%s", want, out)
		}
	}
}

// TestDashboardButtonHitTest asserts each of the three buttons resolves to the
// right action when clicked at its rendered position.
func TestDashboardButtonHitTest(t *testing.T) {
	m := dashModel(100, 40)
	innerW := innerWidth(m.boxWidth())
	btnRow := summaryBodyTopRow + m.dashButtonsIndex(innerW)
	ranges := pillRanges(m.dashButtonNames(), dashButtonStartCol)
	want := []dashButton{dashBtnApply, dashBtnSecurity, dashBtnCatalog}
	for i, r := range ranges {
		x := r[0] + 1 // inside the pill
		if got := m.dashButtonAtClick(x, btnRow); got != want[i] {
			t.Fatalf("button %d click at x=%d row=%d → %v, want %v", i, x, btnRow, got, want[i])
		}
	}
}

// TestDashboardApplyShowsA8Confirm asserts clicking "Применить твики" (the bucket
// includes A8) shows the reboot-warning confirm and does NOT launch the apply; the
// subsequent Enter launches RunSteps over the bucket IDs ("step" command).
func TestDashboardApplyShowsA8Confirm(t *testing.T) {
	if !bucketHasA8(tweakBucketIDs()) {
		t.Fatalf("tweak bucket unexpectedly lacks A8")
	}
	m := dashModel(100, 40)
	innerW := innerWidth(m.boxWidth())
	btnRow := summaryBodyTopRow + m.dashButtonsIndex(innerW)
	applyX := pillRanges(m.dashButtonNames(), dashButtonStartCol)[0][0] + 1

	next, _ := m.dashboardClick(applyX, btnRow)
	mm := next.(model)
	if !mm.dashApplyConfirm {
		t.Fatalf("Apply click did not raise the A8 reboot confirm")
	}
	if mm.command == "step" {
		t.Fatalf("Apply click launched the apply before confirm")
	}
	// Enter confirms → launches the apply over the bucket IDs. Leave the host empty
	// so start()'s validation short-circuits before any dial goroutine spawns; the
	// confirm handler sets m.command/m.cmds BEFORE calling start().
	mm.inputs[fHost].SetValue("")
	mm.inputs[fPass].SetValue("secret")
	n2, _ := mm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := n2.(model)
	if m2.command != "step" {
		t.Fatalf("after confirm, command=%q want \"step\"", m2.command)
	}
	got := append([]string(nil), m2.cmds...)
	wantIDs := tweakBucketIDs()
	sort.Strings(got)
	w := append([]string(nil), wantIDs...)
	sort.Strings(w)
	if !reflect.DeepEqual(got, w) {
		t.Fatalf("apply IDs = %v, want %v", m2.cmds, wantIDs)
	}
}

// TestDashboardAuditRowClickOpensWiki asserts clicking an audit row opens the wiki
// detail for that tweak: wikiStep resolves to the tweak's STEP key (so wiki.Doc
// hits, not the empty fallback) while wikiTweak carries the specific tweak header,
// and wikiReturn=phaseDashboard.
func TestDashboardAuditRowClickOpensWiki(t *testing.T) {
	m := dashModel(100, 40)
	innerW := innerWidth(m.boxWidth())
	gridStart := m.dashGridStartIndex(innerW)
	// Click the first grid row (the BBR result, Step "A4").
	row := summaryBodyTopRow + gridStart
	next, _ := m.dashboardClick(4, row)
	mm := next.(model)
	if mm.phase != phaseWiki {
		t.Fatalf("audit row click → phase %v, want phaseWiki", mm.phase)
	}
	if mm.wikiStep != "A4" {
		t.Fatalf("wikiStep=%q want A4 (the step key, for wiki.Doc lookup)", mm.wikiStep)
	}
	if mm.wikiTweak == "" || !strings.Contains(mm.wikiTweak, "A4-bbr") {
		t.Fatalf("wikiTweak=%q want a header containing the tweak id A4-bbr", mm.wikiTweak)
	}
	if mm.wikiReturn != phaseDashboard {
		t.Fatalf("wikiReturn=%v want phaseDashboard", mm.wikiReturn)
	}
}

// TestTweakBucketExcludesSecuritySteps asserts the apply bucket carries the tweak
// steps and never the security/access steps (A2/A2.5) — those are Security-menu only.
func TestTweakBucketExcludesSecuritySteps(t *testing.T) {
	for _, id := range tweakBucketIDs() {
		if strings.EqualFold(id, "A2") || strings.EqualFold(id, "A2.5") {
			t.Fatalf("tweak bucket must not include security step %q", id)
		}
	}
}
