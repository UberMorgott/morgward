package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// keyModel builds a phaseKey model with a multi-line PEM, sized for hit-test tests.
func keyModel(w, h int, pemLines int) model {
	m := newModel()
	m.w, m.h = w, h
	m.host = "1.2.3.4"
	m.phase = phaseKey
	rows := make([]string, pemLines)
	for i := range rows {
		rows[i] = "AAAAB3NzaC1lZDI1NTE5AAAA" // filler PEM-ish content
	}
	m.keyPEM = strings.Join(rows, "\n")
	return m
}

// TestKeyCopyHitTestVisible asserts the "Copy key" button resolves to a hit at its
// rendered row when it is within the visible region (tall window, body fits).
func TestKeyCopyHitTestVisible(t *testing.T) {
	m := keyModel(100, 40, 6)
	innerW := innerWidth(m.boxWidth())
	_, buttonIdx := m.keyBodyLines(innerW)
	if buttonIdx >= m.bodyViewH() {
		t.Fatalf("test precondition: button (idx %d) must be visible in viewH %d", buttonIdx, m.bodyViewH())
	}
	if got := m.hit(contentX0, keyBodyTopRow+buttonIdx); got != idKeyCopy {
		t.Fatalf("Copy-key click at visible button row hit %q, want %q", got, idKeyCopy)
	}
}

// TestKeyCopyHitTestClipped is the F17 guard: on a window too short to show the whole
// body the Copy-key button is clipped below the fold; a click at its absolute
// (unscrolled) Y must NOT trigger a copy. keyView renders at offset 0, so a button at
// buttonIdx >= viewH is not on screen and gets no layer at all.
func TestKeyCopyHitTestClipped(t *testing.T) {
	// h=12 → bodyViewH = max(12-7,1) = 5; a 6-line PEM pushes the button past row 5.
	m := keyModel(100, 12, 6)
	innerW := innerWidth(m.boxWidth())
	_, buttonIdx := m.keyBodyLines(innerW)
	if buttonIdx < m.bodyViewH() {
		t.Fatalf("test precondition: button (idx %d) must be clipped below viewH %d", buttonIdx, m.bodyViewH())
	}
	if got := m.hit(contentX0, keyBodyTopRow+buttonIdx); got == idKeyCopy {
		t.Fatalf("clipped Copy-key button registered a hit at its off-screen Y")
	}
}

// TestKeyBackHitTestClipped is the back-pill twin of TestKeyCopyHitTestClipped: on a
// window too short to show the whole body the "← Назад" pill is clipped below the fold,
// and its absolute (unrendered) row shows the hint line / bottom border / monitor box
// instead. A click there must NOT dismiss the screen. keyModel(120,20,4) puts the pill
// at body idx 13 with viewH 13 — one row past the fold.
func TestKeyBackHitTestClipped(t *testing.T) {
	for _, pem := range []int{4, 6, 8} {
		m := keyModel(120, 20, pem)
		m.keyPreRun = false
		m.keyReturn = phaseDashboard
		lines, _ := m.keyBodyLines(innerWidth(m.boxWidth()))
		if len(lines)-1 < m.bodyViewH() {
			t.Fatalf("test precondition: back pill (idx %d) must be clipped below viewH %d",
				len(lines)-1, m.bodyViewH())
		}
		// No row of the frame may resolve to the back pill once it is clipped.
		for y := range 20 {
			if got := m.hit(contentX0, y); got == idKeyBack {
				t.Fatalf("pem=%d: clipped back pill registered a hit at y=%d", pem, y)
			}
		}
		// And the real click path must leave the phase alone.
		row := keyPillRowY(m)
		next, _ := m.Update(tea.MouseClickMsg{X: contentX0, Y: row, Button: tea.MouseLeft})
		if got := next.(model).phase; got != phaseKey {
			t.Fatalf("pem=%d: click at the clipped back-pill row y=%d → phase %v, want phaseKey",
				pem, row, got)
		}
	}
}

// TestKeyPreRunHitTestClipped is the same guard for the pre-run pill's START/CANCEL
// halves: clipped below the fold, neither half may register a hit (a stray click must
// not launch the run from the chrome of a short window).
func TestKeyPreRunHitTestClipped(t *testing.T) {
	m := keyModel(120, 20, 6)
	m.keyPreRun = true
	lines, _ := m.keyBodyLines(innerWidth(m.boxWidth()))
	if len(lines)-1 < m.bodyViewH() {
		t.Fatalf("test precondition: pre-run pill (idx %d) must be clipped below viewH %d",
			len(lines)-1, m.bodyViewH())
	}
	for y := range 20 {
		if got := m.hit(contentX0, y); got == idKeyStart || got == idKeyCancel {
			t.Fatalf("clipped pre-run pill registered %q at y=%d", got, y)
		}
		// The cancel half sits further right within the same band.
		if got := m.hit(contentX0+20, y); got == idKeyStart || got == idKeyCancel {
			t.Fatalf("clipped pre-run pill registered %q at x=%d y=%d", got, contentX0+20, y)
		}
	}
}

// keyPillRowY returns the screen Y of the mode pill row (pre-run "[Enter]…[Esc]…" or
// post-run "← Назад"), derived from the SAME body layout keyView renders — keyBodyLines
// appends the pill last in both modes.
func keyPillRowY(m model) int {
	lines, _ := m.keyBodyLines(innerWidth(m.boxWidth()))
	return keyBodyTopRow + len(lines) - 1
}

// TestKeyPreRunStartCancelHitTest asserts the pre-run pill splits into a START half
// ("[Enter] …") and a CANCEL half ("[Esc] …"), each resolving to its own hit-test at
// the rendered row, and that the other half / off-row clicks miss.
func TestKeyPreRunStartCancelHitTest(t *testing.T) {
	m := keyModel(100, 40, 4)
	m.keyPreRun = true
	row := keyPillRowY(m)

	if got := m.hit(contentX0+1, row); got != idKeyStart {
		t.Fatalf("click at the [Enter] half hit %q, want %q", got, idKeyStart)
	}
	// The cancel half begins where the "[Esc]" token starts within the rendered pill.
	// strings.Index is a BYTE offset; the prefix is Cyrillic, so convert to display cells.
	full := t2(m.lang, kKeyPreRunButtons)
	escByte := strings.Index(full, "[Esc")
	if escByte < 0 {
		t.Fatalf("pre-run pill text %q has no [Esc] token", full)
	}
	// +1 for the pill's left padding (Padding(0,1)) added by pillOnStyle; the prefix
	// width is in display cells (lipgloss.Width).
	cancelX := contentX0 + 1 + lipgloss.Width(full[:escByte])
	if got := m.hit(cancelX, row); got != idKeyCancel {
		t.Fatalf("click at the [Esc] half (x=%d) hit %q, want %q", cancelX, got, idKeyCancel)
	}
	// Off the row → both miss.
	if got := m.hit(contentX0+1, row+1); got == idKeyStart || got == idKeyCancel {
		t.Fatalf("pre-run half %q matched one row off the pill row", got)
	}
	// Past the pill's right edge → miss (the layer spans exactly the rendered band).
	if got := m.hit(contentX0+lipgloss.Width(full)+2, row); got == idKeyStart || got == idKeyCancel {
		t.Fatalf("pre-run half %q matched past the pill's right edge", got)
	}
}

// TestKeyPreRunStartClickLaunches asserts clicking the START half confirms the pre-run
// key (confirmPreRunKey path). Host left empty so start()/launchEngine validation
// short-circuits before any dial goroutine; confirmPreRunKey clears keyPreRun.
func TestKeyPreRunStartClickLaunches(t *testing.T) {
	m := keyModel(100, 40, 4)
	m.keyPreRun = true
	m.inputs[fHost].SetValue("") // validation short-circuits → no goroutine
	row := keyPillRowY(m)
	next, _ := m.Update(tea.MouseClickMsg{X: 3, Y: row, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.keyPreRun {
		t.Fatalf("start click left keyPreRun=true; confirmPreRunKey should clear it")
	}
}

// TestKeyPreRunCancelClickAborts asserts clicking the CANCEL half aborts back to the
// form, clearing the staged key (mirrors Esc on the pre-run modal).
func TestKeyPreRunCancelClickAborts(t *testing.T) {
	m := keyModel(100, 40, 4)
	m.keyPreRun = true
	m.keyReturn = phaseForm
	row := keyPillRowY(m)
	full := t2(m.lang, kKeyPreRunButtons)
	cancelX := 2 + 1 + lipgloss.Width(full[:strings.Index(full, "[Esc")])
	next, _ := m.Update(tea.MouseClickMsg{X: cancelX, Y: row, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.keyPreRun {
		t.Fatalf("cancel click left keyPreRun=true; abort should clear it")
	}
	if mm.pendingKey != nil {
		t.Fatalf("cancel click left a staged key; abort should clear pendingKey")
	}
	if mm.phase != phaseForm {
		t.Fatalf("cancel click → phase %v, want phaseForm", mm.phase)
	}
}

// TestKeyPostRunDismissHitTest asserts the post-run (read-only) viewer renders a
// clickable "← Назад" pill that dismisses to keyReturn.
func TestKeyPostRunDismissHitTest(t *testing.T) {
	m := keyModel(100, 40, 4)
	m.keyPreRun = false
	m.keyReturn = phaseSummary
	// keyView renders the body at offset 0, so the back pill's screen Y is
	// keyBodyTopRow + its body index — the last body line in the read-only mode.
	row := keyPillRowY(m)
	backX := pillRanges([]string{t2(m.lang, kWikiBack)}, wikiBackStartCol)[0][0] + 1
	if got := m.hit(backX, row); got != idKeyBack {
		t.Fatalf("post-run back pill click at x=%d y=%d hit %q, want %q", backX, row, got, idKeyBack)
	}
	if got := m.hit(backX, row+1); got == idKeyBack {
		t.Fatalf("post-run back pill matched one row off the pill row")
	}
	if !strings.Contains(m.keyView(), t2(m.lang, kWikiBack)) {
		t.Fatalf("post-run keyView missing the back pill label")
	}
	next, _ := m.Update(tea.MouseClickMsg{X: backX, Y: row, Button: tea.MouseLeft})
	if next.(model).phase != phaseSummary {
		t.Fatalf("post-run back click → phase %v, want phaseSummary (keyReturn)", next.(model).phase)
	}
}

// --- scrolling the key viewer -------------------------------------------------
//
// A private-key PEM is ~10 lines (ed25519) to ~40 (RSA), so on a short terminal the
// body overflows its region and renderScrollRegion draws a scrollbar. Every test below
// drives the REAL Update loop, so all four input paths (↑↓, k/j, wheel, thumb drag /
// track click) are pinned against the same dispatch the running program uses.

// keyScrollModel builds a phaseKey model whose PEM body OVERFLOWS the region. Each PEM
// line is NUMBERED so a rendered frame proves WHICH window is on screen.
func keyScrollModel(t *testing.T) (m model, barX, topRow, viewH, total, maxOff int) {
	t.Helper()
	m = newModel()
	m.w, m.h = 100, 20
	m.host = "1.2.3.4"
	m.phase = phaseKey
	rows := make([]string, 40)
	for i := range rows {
		rows[i] = fmt.Sprintf("PEMLINE%02d", i)
	}
	m.keyPEM = strings.Join(rows, "\n")

	topRow, viewH, total, _, ok := m.scrollGeom()
	if !ok {
		t.Fatal("phaseKey exposes no scroll region (scrollGeom ok=false)")
	}
	if topRow != keyBodyTopRow {
		t.Fatalf("scrollGeom topRow=%d, want keyBodyTopRow=%d", topRow, keyBodyTopRow)
	}
	if total <= viewH {
		t.Fatalf("test needs an overflowing body: total=%d viewH=%d", total, viewH)
	}
	return m, m.boxWidth() - 1, topRow, viewH, total, total - viewH
}

func keyPress(m model, msg tea.KeyPressMsg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

// TestKeyViewScrollKeys asserts ↑↓ and k/j scroll the key viewer, clamp at both ends,
// and reach scrollBy through physKey — so the physical j/k keys scroll on a Cyrillic
// layout too (where they produce "о"/"л").
func TestKeyViewScrollKeys(t *testing.T) {
	m, _, _, _, total, maxOff := keyScrollModel(t)

	m = keyPress(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.keyScroll != 1 {
		t.Fatalf("↓ → keyScroll=%d, want 1", m.keyScroll)
	}
	m = keyPress(m, tea.KeyPressMsg{Text: "j", Code: 'j', BaseCode: 'j'})
	if m.keyScroll != 2 {
		t.Fatalf("j → keyScroll=%d, want 2", m.keyScroll)
	}
	m = keyPress(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.keyScroll != 1 {
		t.Fatalf("↑ → keyScroll=%d, want 1", m.keyScroll)
	}
	m = keyPress(m, tea.KeyPressMsg{Text: "k", Code: 'k', BaseCode: 'k'})
	if m.keyScroll != 0 {
		t.Fatalf("k → keyScroll=%d, want 0", m.keyScroll)
	}
	m = keyPress(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.keyScroll != 0 {
		t.Fatalf("↑ at the top → keyScroll=%d, want it clamped to 0", m.keyScroll)
	}
	// Cyrillic layout: the physical j/k keys carry BaseCode 'j'/'k' but produce "о"/"л".
	m = keyPress(m, tea.KeyPressMsg{Text: "о", Code: 'о', BaseCode: 'j'})
	if m.keyScroll != 1 {
		t.Fatalf("Cyrillic j → keyScroll=%d, want 1 (physKey must be layout-independent)", m.keyScroll)
	}
	m = keyPress(m, tea.KeyPressMsg{Text: "л", Code: 'л', BaseCode: 'k'})
	if m.keyScroll != 0 {
		t.Fatalf("Cyrillic k → keyScroll=%d, want 0", m.keyScroll)
	}
	for range total + 5 {
		m = keyPress(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.keyScroll != maxOff {
		t.Fatalf("↓ past the end → keyScroll=%d, want it clamped to %d", m.keyScroll, maxOff)
	}
}

// TestKeyViewScrollWheel asserts the mouse wheel scrolls the key viewer by the shared
// wheel step and clamps at the top.
func TestKeyViewScrollWheel(t *testing.T) {
	m, _, _, _, _, _ := keyScrollModel(t)
	const wheelStep = 3 // same constant the wheel handler uses

	next, _ := m.Update(wheelMsg(tea.MouseWheelDown))
	m = next.(model)
	if m.keyScroll != wheelStep {
		t.Fatalf("wheel down → keyScroll=%d, want %d", m.keyScroll, wheelStep)
	}
	next, _ = m.Update(wheelMsg(tea.MouseWheelUp))
	m = next.(model)
	if m.keyScroll != 0 {
		t.Fatalf("wheel up → keyScroll=%d, want 0", m.keyScroll)
	}
	next, _ = m.Update(wheelMsg(tea.MouseWheelUp))
	m = next.(model)
	if m.keyScroll != 0 {
		t.Fatalf("wheel up at the top → keyScroll=%d, want it clamped to 0", m.keyScroll)
	}
}

// TestKeyViewScrollRendersWindow proves the offset reaches the RENDERED frame
// (frame.scroll), not just the model field: at offset 0 the first PEM line is on screen
// and the last is not, and at the maximum offset it is the other way round.
func TestKeyViewScrollRendersWindow(t *testing.T) {
	m, _, _, _, _, maxOff := keyScrollModel(t)

	top := m.keyView()
	if !strings.Contains(top, "PEMLINE00") || strings.Contains(top, "PEMLINE39") {
		t.Fatalf("at offset 0 the frame must show the FIRST PEM line and not the last:\n%s", top)
	}
	m.keyScroll = maxOff
	bottom := m.keyView()
	if strings.Contains(bottom, "PEMLINE00") || !strings.Contains(bottom, "PEMLINE39") {
		t.Fatalf("at the max offset the frame must show the LAST PEM line and not the first:\n%s", bottom)
	}
}

// TestKeyViewScrollbarDrag is the whole thumb gesture on the key viewer: press the
// thumb, drag to the bottom of the track, release.
func TestKeyViewScrollbarDrag(t *testing.T) {
	m, barX, topRow, viewH, total, maxOff := keyScrollModel(t)

	start, _ := scrollThumb(viewH, total, 0)
	m = pressBar(t, m, barX, topRow+start)
	if !m.dragging {
		t.Fatal("pressing the key viewer's thumb must arm the drag")
	}
	if m.keyScroll != 0 {
		t.Fatalf("grabbing the thumb moved the offset to %d, want 0", m.keyScroll)
	}
	m = motionTo(t, m, barX, topRow+viewH-1)
	if m.keyScroll != maxOff {
		t.Fatalf("drag to the track bottom: keyScroll=%d, want %d", m.keyScroll, maxOff)
	}
	next, _ := m.Update(tea.MouseReleaseMsg{Button: tea.MouseLeft})
	m = next.(model)
	if m.dragging {
		t.Fatal("release must end the drag")
	}
	m = motionTo(t, m, barX, topRow)
	if m.keyScroll != maxOff {
		t.Fatalf("motion after release moved the offset to %d, want %d", m.keyScroll, maxOff)
	}
}

// TestKeyViewScrollbarTrackClick asserts a press on the bare track jumps the key
// viewer there instead of doing nothing.
func TestKeyViewScrollbarTrackClick(t *testing.T) {
	m, barX, topRow, viewH, total, maxOff := keyScrollModel(t)

	_, end := scrollThumb(viewH, total, 0)
	trackRow := viewH - 1
	if trackRow < end {
		t.Fatalf("thumb [.., %d) leaves no bare track row below it (viewH=%d)", end, viewH)
	}
	m = pressBar(t, m, barX, topRow+trackRow)
	if m.keyScroll != maxOff {
		t.Fatalf("track click at region row %d: keyScroll=%d, want %d", trackRow, m.keyScroll, maxOff)
	}
	if !m.dragging {
		t.Fatal("a track click must also arm the drag")
	}
}

// TestKeyCopyHitTestFollowsScroll is the drift guard for the scrolled body: the
// Copy-key button starts BELOW the fold (no layer), and once scrolled into view its
// layer must sit at the row it was DRAWN at, not at its unscrolled body index.
func TestKeyCopyHitTestFollowsScroll(t *testing.T) {
	m, _, _, viewH, _, maxOff := keyScrollModel(t)
	_, buttonIdx := m.keyBodyLines(innerWidth(m.boxWidth()))
	if buttonIdx < viewH {
		t.Fatalf("test precondition: button (idx %d) must start below viewH %d", buttonIdx, viewH)
	}
	m.keyScroll = maxOff
	row := keyBodyTopRow + buttonIdx - maxOff
	if row < keyBodyTopRow || row >= keyBodyTopRow+viewH {
		t.Fatalf("test precondition: at offset %d the button must be on screen (row %d)", maxOff, row)
	}
	if got := m.hit(contentX0, row); got != idKeyCopy {
		t.Fatalf("scrolled Copy-key button at its DRAWN row %d hit %q, want %q", row, got, idKeyCopy)
	}
	if got := m.hit(contentX0, keyBodyTopRow+buttonIdx); got == idKeyCopy {
		t.Fatalf("Copy-key button still hit at its UNSCROLLED row %d", keyBodyTopRow+buttonIdx)
	}
}

// TestKeyScrollResetsOnEntry asserts a stale offset from a previous visit never
// persists: both paths that open the viewer (the pre-run modal from start(), and the
// summary's read-only "ключ ‹показать›" row) reset it to the top.
func TestKeyScrollResetsOnEntry(t *testing.T) {
	m := formModel(100, 40)
	m.inputs[fHost].SetValue("1.2.3.4")
	m.inputs[fPass].SetValue("secret")
	m.command = "run"
	m.keyScroll = 7 // stale offset from an earlier visit
	next, _ := m.start()
	mm := next.(model)
	if mm.phase != phaseKey || !mm.keyPreRun {
		t.Fatalf("precondition: expected the pre-run key modal, got phase=%v keyPreRun=%v", mm.phase, mm.keyPreRun)
	}
	if mm.keyScroll != 0 {
		t.Fatalf("pre-run key modal opened at keyScroll=%d, want 0", mm.keyScroll)
	}

	s := summaryModel(120, 40)
	s.keyGenerated = true
	s.keyPEM = "PRIVATE-KEY-PEM"
	s.keyScroll = 7
	innerW := innerWidth(s.boxWidth())
	colW := (innerW - sumColGap) / 2
	rightX := 2 + colW + sumColGap + 1
	found := false
	for y := range s.h {
		if !s.summaryKeyShowAtClick(rightX, y) {
			continue
		}
		found = true
		n2, _ := s.Update(mouseClickAt(rightX, y))
		s2 := n2.(model)
		if s2.phase != phaseKey {
			t.Fatalf("key-show click → phase %v, want phaseKey", s2.phase)
		}
		if s2.keyScroll != 0 {
			t.Fatalf("key viewer opened from the summary at keyScroll=%d, want 0", s2.keyScroll)
		}
		break
	}
	if !found {
		t.Fatal("no clickable key-show row found in the summary")
	}
}
