package tui

import (
	"strconv"
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
// interleaved per-step title separators (which have no backing result). The layer id
// carries the index into m.summary.Tweaks, so both the id and the result it names are
// checked (matrixClick performs the same round-trip).
func TestMatrixRowAtClickResolvesResult(t *testing.T) {
	m := matrixModel(100, 40)
	body := m.matrixBodyLines(innerWidth(m.boxWidth()))

	// Body layout: [0]=header, [1]="", [2]="A4" title, [3]=A4-bbr row, [4]="A6.7" title,
	// [5]=A6.7-zram row, [6]=A6.7-eoom row. Map each result-bearing body line to its
	// index in m.summary.Tweaks and the probe id that index must hold.
	wantByBodyIdx := map[int]struct {
		res   int
		probe string
	}{3: {0, "A4-bbr"}, 5: {1, "A6.7-zram"}, 6: {2, "A6.7-eoom"}}
	top := summaryBodyTopRow // body row 0 sits at screen Y = summaryBodyTopRow (scroll 0)

	for bodyIdx, want := range wantByBodyIdx {
		y := top + bodyIdx
		got := m.hit(contentX0+1, y)
		if wantID := idMatrixRowPfx + strconv.Itoa(want.res); got != wantID {
			t.Fatalf("body idx %d (screen y=%d) click → %q want %q\nbody=%v", bodyIdx, y, got, wantID, body)
		}
		if id := m.summary.Tweaks[want.res].Probe.ID; id != want.probe {
			t.Fatalf("body idx %d resolved to result %d (%s), want probe %s", bodyIdx, want.res, id, want.probe)
		}
	}
}

// TestMatrixRowAtClickMissesNonRows asserts clicks on the header, the blank line, and a
// per-step title separator (none backed by a result) do NOT resolve to a result.
func TestMatrixRowAtClickMissesNonRows(t *testing.T) {
	m := matrixModel(100, 40)
	top := summaryBodyTopRow
	for _, bodyIdx := range []int{0 /*header*/, 1 /*blank*/, 2 /*A4 title*/, 4 /*A6.7 title*/} {
		if got := m.hit(contentX0+1, top+bodyIdx); strings.HasPrefix(got, idMatrixRowPfx) {
			t.Fatalf("non-row body idx %d wrongly resolved to result layer %q", bodyIdx, got)
		}
	}
	// A click far to the right (past the rendered row text) misses too.
	if got := m.hit(95, top+3); strings.HasPrefix(got, idMatrixRowPfx) {
		t.Fatalf("click past the row text width wrongly resolved to result layer %q", got)
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

// TestMatrixBackPillPinned covers the one matrix target that is NOT clamped: the back
// pill needs no below-the-fold guard because framedScrollView always emits the pinned
// row immediately after the viewH scroll rows, so it is drawn at exactly matrixBackRow
// whatever the scroll offset or window height. This asserts that construction holds —
// if the pill ever became a body row it would need the same clamp the tweak rows have.
func TestMatrixBackPillPinned(t *testing.T) {
	for _, h := range []int{40, 12, 9} {
		m := matrixModel(100, h)
		m.matScroll = 999 // clamped; the pill must not move with it
		backX := pillRanges([]string{t2(m.lang, kWikiBack)}, wikiBackStartCol)[0][0] + 1
		if got := m.hit(backX, m.matrixBackRow()); got != idMatrixBack {
			t.Fatalf("h=%d: back pill at its pinned row hit %q, want %q", h, got, idMatrixBack)
		}
		// The pinned row is the row framedScrollView actually draws it on: 2 chrome
		// rows + the whole scroll region.
		if want := summaryBodyTopRow + m.matrixBodyViewH(); m.matrixBackRow() != want {
			t.Fatalf("h=%d: matrixBackRow()=%d, want %d (2 chrome + viewH)", h, m.matrixBackRow(), want)
		}
	}
}

// TestMatrixRowHitTestScrolledOut is the matrix twin of TestKeyCopyHitTestClipped: a
// tweak row outside the scroll region gets NO layer, whether it is clipped below the
// fold (short window, scroll 0) or scrolled above it. Otherwise a click on the pinned
// pill / hint / border row would open some row's wiki.
func TestMatrixRowHitTestScrolledOut(t *testing.T) {
	m := matrixModel(100, 12) // viewH = 12-7-1 = 4; body is 7 lines
	viewH := m.matrixBodyViewH()
	body := m.matrixBodyLines(innerWidth(m.boxWidth()))
	if len(body) <= viewH {
		t.Fatalf("test precondition: body (%d lines) must overflow viewH %d", len(body), viewH)
	}
	// Clipped below the fold at scroll 0: body idx 5 and 6 are the two A6.7 rows.
	for _, bodyIdx := range []int{5, 6} {
		y := summaryBodyTopRow + bodyIdx
		if got := m.hit(contentX0+1, y); strings.HasPrefix(got, idMatrixRowPfx) {
			t.Fatalf("row clipped below the fold (body idx %d, y=%d) hit %q", bodyIdx, y, got)
		}
	}
	// Scrolled to the end, the A4 row (body idx 3) is above the region: its absolute
	// (unscrolled) screen Y must not resolve to a row either.
	m.matScroll = len(body)
	if got := m.hit(contentX0+1, summaryBodyTopRow+3); got == idMatrixRowPfx+"0" {
		t.Fatalf("row scrolled above the region still hit %q", got)
	}
	// No screen row anywhere may resolve to a row index outside the visible window.
	off := clampScroll(m.matScroll, len(body), viewH)
	for y := range 12 {
		got := m.hit(contentX0+1, y)
		rest, ok := strings.CutPrefix(got, idMatrixRowPfx)
		if !ok {
			continue
		}
		if row := y - summaryBodyTopRow; row < 0 || row >= viewH {
			t.Fatalf("y=%d is outside the scroll region yet hit row layer %q (off=%d)", y, rest, off)
		}
	}
}

// TestMatrixBackPillHitTest asserts the rendered "← Назад" pill resolves at its drawn
// position and misses off-target, and that clicking it returns home (goBack → form).
func TestMatrixBackPillHitTest(t *testing.T) {
	m := matrixModel(100, 40)
	backY := m.matrixBackRow()
	backX := pillRanges([]string{t2(m.lang, kWikiBack)}, wikiBackStartCol)[0][0] + 1
	if got := m.hit(backX, backY); got != idMatrixBack {
		t.Fatalf("back pill click at x=%d y=%d hit %q, want %q", backX, backY, got, idMatrixBack)
	}
	// Off the row → miss.
	if got := m.hit(backX, backY+1); got == idMatrixBack {
		t.Fatalf("back pill matched one row off the pill row")
	}
	// Far right → miss.
	if got := m.hit(95, backY); got == idMatrixBack {
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
