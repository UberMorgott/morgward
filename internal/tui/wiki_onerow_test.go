package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// pillXForKind returns a click X inside the given button kind's pill on the shared
// wiki buttons row, using the SAME ordered geometry the render path uses.
func pillXForKind(m model, kind wikiActionKind) (int, bool) {
	ranges := pillRanges(m.wikiButtonLabels(), wikiBackStartCol)
	for i, k := range m.wikiButtonKinds() {
		if k == kind {
			return ranges[i][0] + 1, true
		}
	}
	return 0, false
}

// TestWikiButtonsOnOneRow asserts (FEATURE B) that all present action pills render on
// a SINGLE row and each pill's hit-test resolves to its own kind at that row, with the
// warning text remaining on its own row ABOVE the buttons.
func TestWikiButtonsOnOneRow(t *testing.T) {
	// NOT-applied probe + pending upgrades → update + apply + back all present.
	m := wikiProbeModel(120, 40, "a3.installed", "A3", false, 5)

	kinds := m.wikiButtonKinds()
	// Expect update, apply, back on one row (no revert for a not-applied probe).
	want := []wikiActionKind{wikiRowUpdateButton, wikiRowApplyButton, wikiRowBack}
	if len(kinds) != len(want) {
		t.Fatalf("button kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("button kind %d = %v, want %v", i, kinds[i], want[i])
		}
	}

	btnY := m.wikiButtonsRowY()
	// The warning row sits just ABOVE the buttons row.
	if !m.wikiHasUpdateWarn() {
		t.Fatalf("expected the update warning row to be present")
	}
	if btnY != summaryBodyTopRow+m.wikiBodyViewH()+1 {
		t.Fatalf("buttons row Y=%d, want warn-row + 1", btnY)
	}

	// Each present pill resolves to ITS kind at the shared row, and NOT to a neighbor.
	for _, k := range kinds {
		x, ok := pillXForKind(m, k)
		if !ok {
			t.Fatalf("no pill X for kind %v", k)
		}
		if !m.wikiButtonAtClick(k, x, btnY) {
			t.Fatalf("pill kind %v did not hit at x=%d y=%d", k, x, btnY)
		}
		// A click on this pill must not resolve as a DIFFERENT kind.
		for _, other := range kinds {
			if other == k {
				continue
			}
			if m.wikiButtonAtClick(other, x, btnY) {
				t.Fatalf("pill %v's X wrongly resolved as %v — pills overlap", k, other)
			}
		}
	}

	// Off the row → every pill misses.
	for _, k := range kinds {
		x, _ := pillXForKind(m, k)
		if m.wikiButtonAtClick(k, x, btnY+1) {
			t.Fatalf("pill %v matched one row below the buttons row", k)
		}
	}

	// The rendered view shows all three button labels and the warning, and the three
	// pill labels appear on the SAME single rendered line (one row).
	out := m.wikiView()
	for _, label := range []string{
		t2(m.lang, kWikiUpdateButton),
		t2(m.lang, kWikiApplyButton),
		t2(m.lang, kWikiBack),
		t2(m.lang, kWikiUpdateWarn),
	} {
		if !strings.Contains(out, label) {
			t.Fatalf("wikiView missing %q", label)
		}
	}
	// All three button labels on one line: find the line containing the back label and
	// assert the other two labels are on that same line.
	var btnLine string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, t2(m.lang, kWikiBack)) {
			btnLine = ln
			break
		}
	}
	if btnLine == "" {
		t.Fatalf("could not find the buttons line in the rendered view")
	}
	if !strings.Contains(btnLine, t2(m.lang, kWikiApplyButton)) || !strings.Contains(btnLine, t2(m.lang, kWikiUpdateButton)) {
		t.Fatalf("apply/update pills not on the SAME row as back:\n%q", btnLine)
	}
}

// TestWikiBackClickStillNavigates asserts the back pill on the shared row still
// returns to wikiReturn when clicked (keyboard/mouse parity preserved).
func TestWikiBackClickStillNavigates(t *testing.T) {
	m := wikiProbeModel(120, 40, "a4.bbr_active", "A4", true, 0) // applied+revertable → [revert, back]
	backX, ok := pillXForKind(m, wikiRowBack)
	if !ok {
		t.Fatalf("no back pill X")
	}
	next, _ := m.Update(tea.MouseClickMsg{X: backX, Y: m.wikiBackRow(), Button: tea.MouseLeft})
	mm := next.(model)
	if mm.phase != phaseDashboard {
		t.Fatalf("back click → phase %v, want phaseDashboard (wikiReturn)", mm.phase)
	}
}
