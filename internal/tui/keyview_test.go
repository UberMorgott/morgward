package tui

import (
	"strings"
	"testing"
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
