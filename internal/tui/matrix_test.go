package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/tweaks"
)

// matrixModel builds a phaseMatrix model with a small two-step tweak audit, sized for
// layout/hit-test tests. The body structure mirrors matrixBodyLines: header, blank,
// then per-step a title separator followed by one row per result.
func matrixModel(w, h int) model {
	m := newModel()
	m.w, m.h = w, h
	m.host = "1.2.3.4"
	m.phase = phaseMatrix
	m.summary = engine.Summary{Tweaks: []tweaks.Result{
		{Probe: tweaks.Probe{ID: "A4-bbr", Step: "A4", Name: "BBR congestion control"}, Applied: true},
		{Probe: tweaks.Probe{ID: "A6.7-zram", Step: "A6.7", Name: "zram swap active"}, Applied: false},
		{Probe: tweaks.Probe{ID: "A6.7-eoom", Step: "A6.7", Name: "earlyoom active"}, Applied: false},
	}}
	return m
}

// TestMatrixRowAtClickResolvesResult asserts a click on each rendered tweak row maps
// to the correct tweaks.Result, accounting for the header/blank prefix and the
// interleaved per-step title separators (which have no backing result).
func TestMatrixRowAtClickResolvesResult(t *testing.T) {
	m := matrixModel(100, 40)
	innerW := innerWidth(m.boxWidth())
	body := m.matrixBodyLines(innerW)

	// Body layout: [0]=header, [1]="", [2]="A4" title, [3]=A4-bbr row, [4]="A6.7" title,
	// [5]=A6.7-zram row, [6]=A6.7-eoom row. Map each result-bearing body line.
	wantByBodyIdx := map[int]string{3: "A4-bbr", 5: "A6.7-zram", 6: "A6.7-eoom"}
	top := summaryBodyTopRow // body row 0 sits at screen Y = summaryBodyTopRow (scroll 0)
	const contentX0 = 2

	for bodyIdx, wantID := range wantByBodyIdx {
		y := top + bodyIdx
		r, ok := m.matrixRowAtClick(contentX0+1, y)
		if !ok || r.Probe.ID != wantID {
			t.Fatalf("body idx %d (screen y=%d) click → %q,%v want %s,true\nbody=%v", bodyIdx, y, r.Probe.ID, ok, wantID, body)
		}
	}
}

// TestMatrixRowAtClickMissesNonRows asserts clicks on the header, the blank line, and a
// per-step title separator (none backed by a result) do NOT resolve to a result.
func TestMatrixRowAtClickMissesNonRows(t *testing.T) {
	m := matrixModel(100, 40)
	top := summaryBodyTopRow
	const contentX0 = 2
	for _, bodyIdx := range []int{0 /*header*/, 1 /*blank*/, 2 /*A4 title*/, 4 /*A6.7 title*/} {
		if _, ok := m.matrixRowAtClick(contentX0+1, top+bodyIdx); ok {
			t.Fatalf("non-row body idx %d wrongly resolved to a result", bodyIdx)
		}
	}
	// A click far to the right (past the rendered row text) misses too.
	if _, ok := m.matrixRowAtClick(95, top+3); ok {
		t.Fatalf("click past the row text width wrongly resolved to a result")
	}
}

// TestMatrixRowClickOpensWiki drives the click through Update and asserts it opens the
// tweak's wiki detail with wikiReturn=phaseMatrix (so Esc comes back to the matrix).
func TestMatrixRowClickOpensWiki(t *testing.T) {
	m := matrixModel(100, 40)
	y := summaryBodyTopRow + 3 // the A4-bbr row
	next, _ := m.Update(tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.phase != phaseWiki {
		t.Fatalf("matrix row click → phase %v, want phaseWiki", mm.phase)
	}
	if mm.wikiStep != "A4" {
		t.Fatalf("wikiStep=%q want A4", mm.wikiStep)
	}
	if !strings.Contains(mm.wikiTweak, "A4-bbr") {
		t.Fatalf("wikiTweak=%q want a header containing A4-bbr", mm.wikiTweak)
	}
	if mm.wikiReturn != phaseMatrix {
		t.Fatalf("wikiReturn=%v want phaseMatrix", mm.wikiReturn)
	}
	// Esc from the wiki returns to the matrix (wikiReturn round-trip).
	n2, _ := mm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if n2.(model).phase != phaseMatrix {
		t.Fatalf("esc from wiki → phase %v, want phaseMatrix", n2.(model).phase)
	}
}

// TestMatrixBackPillHitTest asserts the rendered "← Назад" pill resolves at its drawn
// position and misses off-target, and that clicking it returns home (goBack → form).
func TestMatrixBackPillHitTest(t *testing.T) {
	m := matrixModel(100, 40)
	backY := m.matrixBackRow()
	backX := pillRanges([]string{t2(m.lang, kWikiBack)}, wikiBackStartCol)[0][0] + 1
	if !m.matrixBackAtClick(backX, backY) {
		t.Fatalf("back pill click at x=%d y=%d did not register", backX, backY)
	}
	// Off the row → miss.
	if m.matrixBackAtClick(backX, backY+1) {
		t.Fatalf("back pill matched one row off the pill row")
	}
	// Far right → miss.
	if m.matrixBackAtClick(95, backY) {
		t.Fatalf("back pill matched past its rendered width")
	}
	// The rendered view shows the back label.
	if !strings.Contains(m.matrixView(), t2(m.lang, kWikiBack)) {
		t.Fatalf("matrixView missing the back pill label")
	}
	// A click on the back pill via Update returns to the form (goBack).
	next, _ := m.Update(tea.MouseClickMsg{X: backX, Y: backY, Button: tea.MouseLeft})
	if next.(model).phase != phaseForm {
		t.Fatalf("back pill click → phase %v, want phaseForm", next.(model).phase)
	}
}
