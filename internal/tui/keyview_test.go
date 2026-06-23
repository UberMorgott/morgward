package tui

import (
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
	const contentX0 = 2
	if !m.keyCopyAtClick(contentX0, keyBodyTopRow+buttonIdx) {
		t.Fatalf("Copy-key click at visible button row did not register")
	}
}

// TestKeyCopyHitTestClipped is the F17 guard: on a window too short to show the whole
// body the Copy-key button is clipped below the fold; a click at its absolute
// (unscrolled) Y must NOT trigger a copy. keyView renders at offset 0, so a button at
// buttonIdx >= viewH is not on screen and must be unhittable.
func TestKeyCopyHitTestClipped(t *testing.T) {
	// h=12 → bodyViewH = max(12-7,1) = 5; a 6-line PEM pushes the button past row 5.
	m := keyModel(100, 12, 6)
	innerW := innerWidth(m.boxWidth())
	_, buttonIdx := m.keyBodyLines(innerW)
	if buttonIdx < m.bodyViewH() {
		t.Fatalf("test precondition: button (idx %d) must be clipped below viewH %d", buttonIdx, m.bodyViewH())
	}
	const contentX0 = 2
	if m.keyCopyAtClick(contentX0, keyBodyTopRow+buttonIdx) {
		t.Fatalf("clipped Copy-key button registered a hit at its off-screen Y")
	}
}

// keyPreRunButtonsRowY returns the screen Y of the pre-run "[Enter]…[Esc]…" pill row,
// derived from the SAME body layout keyView renders.
func keyPreRunButtonsRowY(m model) int {
	lines, _ := m.keyBodyLines(innerWidth(m.boxWidth()))
	// The pill is the LAST body line on the pre-run modal (keyBodyLines appends it last).
	return keyBodyTopRow + len(lines) - 1
}

// TestKeyPreRunStartCancelHitTest asserts the pre-run pill splits into a START half
// ("[Enter] …") and a CANCEL half ("[Esc] …"), each resolving to its own hit-test at
// the rendered row, and that the other half / off-row clicks miss.
func TestKeyPreRunStartCancelHitTest(t *testing.T) {
	m := keyModel(100, 40, 4)
	m.keyPreRun = true
	row := keyPreRunButtonsRowY(m)

	const contentX0 = 2
	if !m.keyStartAtClick(contentX0+1, row) {
		t.Fatalf("start click at the [Enter] half did not register")
	}
	if m.keyCancelAtClick(contentX0+1, row) {
		t.Fatalf("the [Enter] half wrongly resolved as cancel")
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
	if !m.keyCancelAtClick(cancelX, row) {
		t.Fatalf("cancel click at the [Esc] half (x=%d) did not register", cancelX)
	}
	if m.keyStartAtClick(cancelX, row) {
		t.Fatalf("the [Esc] half wrongly resolved as start")
	}
	// Off the row → both miss.
	if m.keyStartAtClick(contentX0+1, row+1) || m.keyCancelAtClick(cancelX, row+1) {
		t.Fatalf("pre-run halves matched one row off the pill row")
	}
}

// TestKeyPreRunStartClickLaunches asserts clicking the START half confirms the pre-run
// key (confirmPreRunKey path). Host left empty so start()/launchEngine validation
// short-circuits before any dial goroutine; confirmPreRunKey clears keyPreRun.
func TestKeyPreRunStartClickLaunches(t *testing.T) {
	m := keyModel(100, 40, 4)
	m.keyPreRun = true
	m.inputs[fHost].SetValue("") // validation short-circuits → no goroutine
	row := keyPreRunButtonsRowY(m)
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
	row := keyPreRunButtonsRowY(m)
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
	row := m.keyBackRow()
	backX := pillRanges([]string{t2(m.lang, kWikiBack)}, wikiBackStartCol)[0][0] + 1
	if !m.keyBackAtClick(backX, row) {
		t.Fatalf("post-run back pill click at x=%d y=%d did not register", backX, row)
	}
	if m.keyBackAtClick(backX, row+1) {
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
