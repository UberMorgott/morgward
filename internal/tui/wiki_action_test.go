package tui

import (
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/tweaks"
)

// wikiProbeModel builds a phaseWiki model opened from the Dashboard per-PROBE path
// for the given probe id, with the supplied audit raw + pending-upgrade count.
func wikiProbeModel(w, h int, probeID, step string, applied bool, pending int) model {
	m := newModel()
	m.w, m.h = w, h
	m.phase = phaseWiki
	m.wikiReturn = phaseDashboard
	m.wikiStep = step
	m.wikiProbeID = probeID
	m.wikiTweak = "[" + probeID + "] name"
	m.dashAuditDone = true
	m.dashAuditRaw = []tweaks.Result{
		{Probe: tweaks.Probe{ID: probeID, Step: step, Name: "name"}, Applied: applied},
	}
	m.dashFacts = &detect.Facts{ID: "ubuntu", VersionID: "24.04", PendingUpgrades: pending}
	return m
}

// TestWikiApplyAndUpdateHitTest asserts that on a NOT-applied probe with pending
// upgrades, the [Применить] and [Обновить и перезагрузить] pills resolve at their
// rendered Y, the back pill still resolves, and an APPLIED probe shows no apply pill.
func TestWikiApplyAndUpdateHitTest(t *testing.T) {
	// NOT applied + pending>0 → both action pills present, plus back.
	m := wikiProbeModel(100, 40, "a3.installed", "A3", false, 5)

	rows := m.wikiActionRows()
	wantOrder := []wikiActionKind{wikiRowUpdateWarn, wikiRowUpdateButton, wikiRowApplyButton, wikiRowBack}
	if len(rows) != len(wantOrder) {
		t.Fatalf("action rows = %v, want %v", rows, wantOrder)
	}
	for i := range wantOrder {
		if rows[i] != wantOrder[i] {
			t.Fatalf("action row %d = %v, want %v", i, rows[i], wantOrder[i])
		}
	}

	// FEATURE B: every button pill shares ONE row (wikiButtonsRowY); pillXForKind
	// (package helper) gives a click X inside a given kind's pill from the multi-pill
	// ranges over wikiButtonLabels (the single geometry source).
	btnY := m.wikiButtonsRowY()

	// Apply pill hit-test at the shared row, inside the apply pill.
	applyX, _ := pillXForKind(m, wikiRowApplyButton)
	if !m.wikiApplyAtClick(applyX, btnY) {
		t.Fatalf("apply hit-test missed at x=%d y=%d", applyX, btnY)
	}
	// A click one row off must NOT register as apply.
	if m.wikiApplyAtClick(applyX, btnY+1) {
		t.Fatalf("apply hit-test matched off its row")
	}

	// Update pill hit-test on the same row, inside the update pill.
	upX, _ := pillXForKind(m, wikiRowUpdateButton)
	if !m.wikiUpdateAtClick(upX, btnY) {
		t.Fatalf("update hit-test missed at x=%d y=%d", upX, btnY)
	}

	// Back pill still resolves (also on the shared row), and the apply X must NOT
	// resolve as update (separate pills, separate X ranges).
	backX, _ := pillXForKind(m, wikiRowBack)
	if !m.wikiBackAtClick(backX, m.wikiBackRow()) {
		t.Fatalf("back hit-test missed")
	}
	if m.wikiUpdateAtClick(applyX, btnY) {
		t.Fatalf("apply-pill X wrongly resolved as the update pill — pills not separated")
	}

	// The view renders the action labels and the warning text.
	out := m.wikiView()
	for _, want := range []string{
		t2(m.lang, kWikiApplyButton),
		t2(m.lang, kWikiUpdateButton),
		t2(m.lang, kWikiUpdateWarn),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("wikiView missing %q", want)
		}
	}

	// APPLIED probe → no apply pill, and (with pending=0) no update pill either.
	// A3 IS engine-revertable, so an applied A3 probe now shows the [Откатить] pill
	// (added after the apply slot, before back).
	ma := wikiProbeModel(100, 40, "a3.installed", "A3", true, 0)
	if _, shown := ma.wikiActionRowY(wikiRowApplyButton); shown {
		t.Fatalf("applied probe must not show the apply pill")
	}
	if _, shown := ma.wikiActionRowY(wikiRowUpdateButton); shown {
		t.Fatalf("with no pending upgrades the update pill must be hidden")
	}
	if got := ma.wikiActionRows(); len(got) != 2 || got[0] != wikiRowRevertButton || got[1] != wikiRowBack {
		t.Fatalf("applied + revertable + no-pending action rows = %v, want [revert, back]", got)
	}
	// Hit-tests for the hidden pills must always miss.
	if ma.wikiApplyAtClick(4, ma.wikiBackRow()) {
		t.Fatalf("hidden apply pill hit-test must miss")
	}
	if ma.wikiUpdateAtClick(4, ma.wikiBackRow()) {
		t.Fatalf("hidden update pill hit-test must miss")
	}
}

// TestWikiSummaryPathNoActionButtons asserts that when the wiki is opened from the
// SUMMARY path (m.wikiProbeID == ""), only the back pill is shown — no apply/update
// rows — even if pending upgrades exist.
func TestWikiSummaryPathNoActionButtons(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseWiki
	m.wikiReturn = phaseSummary
	m.wikiStep = "A4"
	m.wikiProbeID = "" // summary path
	m.dashFacts = &detect.Facts{PendingUpgrades: 9}

	rows := m.wikiActionRows()
	if len(rows) != 1 || rows[0] != wikiRowBack {
		t.Fatalf("summary-path action rows = %v, want [back only]", rows)
	}
}
