package tui

import (
	"strings"
	"testing"
)

// TestVPHeightMatchesLegacyChrome pins vpHeight to the hand-rolled arithmetic it
// replaced (`m.h - 6 - 3`, floored at 3) across every terminal height and both
// finished states. phaseRun is NOT in the frames golden — it hand-draws its frame in
// runView rather than going through framedScrollView — so this is the proof that
// routing it through the shared chromeViewH left the rendered frame unchanged.
//
// The two differ only where chromeViewH's floor-at-1 bites (m.h < 10), and there the
// trailing max(base, 3) swallows the difference; the loop below covers that range.
func TestVPHeightMatchesLegacyChrome(t *testing.T) {
	for _, finished := range []bool{false, true} {
		for h := range 200 {
			m := newModel()
			m.w, m.h = 100, h
			m.finished = finished
			m.command = "run"

			legacy := m.h - 6 - 3
			if finished {
				legacy--
				legacy -= m.finishedTailRows()
			}
			legacy = max(legacy, 3)

			if got := m.vpHeight(); got != legacy {
				t.Fatalf("h=%d finished=%v: vpHeight()=%d, legacy chrome arithmetic=%d",
					h, finished, got, legacy)
			}
		}
	}
}

// TestRunViewRowsMatchChromeBudget asserts the frame runView actually DRAWS spends
// exactly the rows chromeViewH budgets: frameChromeRows of always-present chrome plus
// the two fixed rows (progress line + blank spacer) above the viewport. If runView
// ever gains or loses a chrome row, the shared budget stops describing it and this
// fails instead of silently pushing the monitor footer off-screen.
func TestRunViewRowsMatchChromeBudget(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	m.command = "run"
	m.vp = viewportForTest(&m)

	rows := len(strings.Split(m.runView(), "\n"))
	const fixedRows = 2 // progress line + blank spacer
	if want := m.vpHeight() + frameChromeRows + fixedRows; rows != want {
		t.Fatalf("runView drew %d rows, want vpHeight(%d) + chrome(%d) + fixed(%d) = %d",
			rows, m.vpHeight(), frameChromeRows, fixedRows, want)
	}
}
