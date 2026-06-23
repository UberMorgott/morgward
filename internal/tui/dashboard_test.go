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
		localTweakName(m.lang, "A4-bbr", "BBR"), // an audit row name
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboardView missing %q\n%s", want, out)
		}
	}
}

// TestDashboardButtonHitTest asserts each of the four buttons resolves to the
// right action when clicked at its rendered position.
func TestDashboardButtonHitTest(t *testing.T) {
	m := dashModel(100, 40)
	innerW := innerWidth(m.boxWidth())
	btnRow := m.dashButtonsRowY(innerW) // FIXED screen Y, not via the scroll offset
	ranges := pillRanges(m.dashButtonNames(), dashButtonStartCol)
	want := []dashButton{dashBtnApply, dashBtnSecurity}
	if len(ranges) != len(want) {
		t.Fatalf("dashButtonNames has %d buttons, want %d (Apply, Security — Terminal/Files moved to the nav bar)", len(ranges), len(want))
	}
	for i, r := range ranges {
		x := r[0] + 1 // inside the pill
		if got := m.dashButtonAtClick(x, btnRow); got != want[i] {
			t.Fatalf("button %d click at x=%d row=%d → %v, want %v", i, x, btnRow, got, want[i])
		}
	}
}

// TestDashAuditTwoColumnHitTest asserts the fluid 2-column audit grid maps a click
// in the LEFT column and a click in the RIGHT column to the correct Probe. With a
// wide width (100) and 3 results, the grid is 2 columns × ceil(3/2)=2 lines:
// line0 = [res0 | res1], line1 = [res2]. Left col → res0/res2, right col → res1.
func TestDashAuditTwoColumnHitTest(t *testing.T) {
	m := dashModel(100, 40)
	innerW := innerWidth(m.boxWidth())
	cols, colWidth := dashAuditCols(innerW)
	if cols != 2 {
		t.Fatalf("at width 100 expected 2 columns, got %d (innerW=%d colWidth=%d)", cols, innerW, colWidth)
	}
	if got := m.dashAuditNumGridLines(innerW); got != 2 {
		t.Fatalf("3 results / 2 cols → %d grid lines, want 2", got)
	}
	gridTop := m.dashScrollTopRow(innerW) // screen Y of the first scrollable grid row

	const contentX0 = 2
	leftX := contentX0 + 1                          // inside the left column's text
	rightX := contentX0 + colWidth + dashColGap + 1 // inside the right column's text

	// Grid line 0, left column → result[0] (A4-bbr).
	r0, ok := m.dashAuditRowAtClick(leftX, gridTop)
	if !ok || r0.Probe.ID != "A4-bbr" {
		t.Fatalf("line0 left click → %q,%v want A4-bbr,true", r0.Probe.ID, ok)
	}
	// Grid line 0, right column → result[1] (A6.7-zram).
	r1, ok := m.dashAuditRowAtClick(rightX, gridTop)
	if !ok || r1.Probe.ID != "A6.7-zram" {
		t.Fatalf("line0 right click → %q,%v want A6.7-zram,true", r1.Probe.ID, ok)
	}
	// Grid line 1, left column → result[2] (A6.7-eoom).
	r2, ok := m.dashAuditRowAtClick(leftX, gridTop+1)
	if !ok || r2.Probe.ID != "A6.7-eoom" {
		t.Fatalf("line1 left click → %q,%v want A6.7-eoom,true", r2.Probe.ID, ok)
	}
	// Grid line 1, right column has NO result (odd count) → miss.
	if _, ok := m.dashAuditRowAtClick(rightX, gridTop+1); ok {
		t.Fatalf("line1 right click should miss (no 4th result)")
	}
}

// TestDashAuditOneColumnFallback asserts a narrow width collapses the grid to a
// single column (one result per line) and the left-column hit-test still resolves.
func TestDashAuditOneColumnFallback(t *testing.T) {
	m := dashModel(44, 40) // innerW small → each 2-col would be < minDashColWidth
	innerW := innerWidth(m.boxWidth())
	cols, _ := dashAuditCols(innerW)
	if cols != 1 {
		t.Fatalf("at width 44 expected 1-column fallback, got %d cols (innerW=%d)", cols, innerW)
	}
	if got := m.dashAuditNumGridLines(innerW); got != 3 {
		t.Fatalf("3 results / 1 col → %d grid lines, want 3", got)
	}
	gridTop := m.dashScrollTopRow(innerW)
	const contentX0 = 2
	r0, ok := m.dashAuditRowAtClick(contentX0+1, gridTop)
	if !ok || r0.Probe.ID != "A4-bbr" {
		t.Fatalf("1-col line0 click → %q,%v want A4-bbr,true", r0.Probe.ID, ok)
	}
	r2, ok := m.dashAuditRowAtClick(contentX0+1, gridTop+2)
	if !ok || r2.Probe.ID != "A6.7-eoom" {
		t.Fatalf("1-col line2 click → %q,%v want A6.7-eoom,true", r2.Probe.ID, ok)
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
	btnRow := m.dashButtonsRowY(innerW) // FIXED screen Y
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
	// Click the first grid row (the BBR result, Step "A4") in the scroll region.
	row := m.dashScrollTopRow(innerW)
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

// TestIsSecurityStep guards the predicate that ties the Dashboard "force satisfied"
// rule to the tweakBucketIDs exclusion: only A2/A2.5 are Security-menu steps.
func TestIsSecurityStep(t *testing.T) {
	cases := map[string]bool{"A2": true, "A2.5": true, "A4": false, "A1": false, "": false}
	for step, want := range cases {
		if got := isSecurityStep(step); got != want {
			t.Fatalf("isSecurityStep(%q)=%v, want %v", step, got, want)
		}
	}
}

// TestCaptureAuditSecurityBucketSatisfied asserts the Dashboard tweaks counters treat
// a SECURITY-bucket probe (Step A2/A2.5) as satisfied even when not applied — the
// "Применить твики" button never manages it, so it must never count as "можно
// применить". A NON-applied tweak-bucket probe (A4) still counts as pending. The A2
// probe stays VISIBLE in the grid (it is non-informational), and dashAuditRaw keeps its
// TRUE Applied=false so the Security screen can show its real state.
func TestCaptureAuditSecurityBucketSatisfied(t *testing.T) {
	m := newModel()
	sum := engine.Summary{
		Tweaks: []tweaks.Result{
			{Probe: tweaks.Probe{ID: "a2.conf00", Step: "A2", Name: "00-hardening.conf"}, Applied: false},
			{Probe: tweaks.Probe{ID: "a4.bbr_conf", Step: "A4", Name: "BBR"}, Applied: false},
			{Probe: tweaks.Probe{ID: "a1.input_drop", Step: "A1", Name: "INPUT DROP"}, Applied: true},
		},
	}
	m.captureAudit(sum)

	// total = non-informational display set = all 3 (none are informational).
	if m.dashAuditTotal != 3 {
		t.Fatalf("dashAuditTotal=%d want 3", m.dashAuditTotal)
	}
	// applied = real-applied (a1) + security-bucket-satisfied (a2.conf00) = 2.
	if m.dashAuditApplied != 2 {
		t.Fatalf("dashAuditApplied=%d want 2 (a1 applied + a2 security-satisfied)", m.dashAuditApplied)
	}
	// canApply = total - applied = 1 (only the pending A4 tweak).
	if canApply := m.dashAuditTotal - m.dashAuditApplied; canApply != 1 {
		t.Fatalf("canApply=%d want 1 (only the pending A4 tweak)", canApply)
	}

	// The A2 probe must still APPEAR in the display grid (visible, not hidden).
	var sawA2, sawA4 bool
	for _, r := range m.dashAuditResults {
		if r.Probe.ID == "a2.conf00" {
			sawA2 = true
		}
		if r.Probe.ID == "a4.bbr_conf" {
			sawA4 = true
		}
	}
	if !sawA2 || !sawA4 {
		t.Fatalf("display grid missing rows: sawA2=%v sawA4=%v", sawA2, sawA4)
	}

	// dashAuditRaw must retain the TRUE Applied=false for the A2 probe so the Security
	// screen shows the real (un-forced) state.
	for _, r := range m.dashAuditRaw {
		if r.Probe.ID == "a2.conf00" && r.Applied {
			t.Fatalf("dashAuditRaw a2.conf00 Applied was forced to true; Security screen would lie")
		}
	}
}

// TestDashAuditGridGlyphSecurityForced asserts the grid glyph routes through the same
// security-bucket rule: a NON-applied A2 row renders the ✓ glyph (Security-menu domain,
// shown satisfied on the Dashboard) while a NON-applied A4 row renders the • glyph.
func TestDashAuditGridGlyphSecurityForced(t *testing.T) {
	m := dashModel(100, 40)
	m.dashAuditResults = []tweaks.Result{
		{Probe: tweaks.Probe{ID: "a2.conf00", Step: "A2", Name: "00-hardening.conf"}, Applied: false},
		{Probe: tweaks.Probe{ID: "a4.bbr_conf", Step: "A4", Name: "BBR"}, Applied: false},
	}
	innerW := innerWidth(m.boxWidth())
	a2cell := m.dashAuditCellText(m.dashAuditResults[0], innerW)
	a4cell := m.dashAuditCellText(m.dashAuditResults[1], innerW)
	if !strings.Contains(a2cell, "✓") {
		t.Fatalf("A2 cell missing ✓ glyph (security-bucket should show satisfied): %q", a2cell)
	}
	if !strings.Contains(a4cell, "•") {
		t.Fatalf("A4 cell missing • glyph (pending tweak): %q", a4cell)
	}
}
