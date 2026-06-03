package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// firstRunes returns the first n runes of s (rune-safe substring for assertions).
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) < n {
		return s
	}
	return string(r[:n])
}

// hasActionRow reports whether wikiActionRows currently includes the given kind.
func hasActionRow(m model, kind wikiActionKind) bool {
	return slices.Contains(m.wikiActionRows(), kind)
}

// TestWikiRevertButtonShowsForAppliedRevertable: an APPLIED, non-informational probe
// whose step is engine-revertable (A4) shows the [Откатить] row and NOT the apply row.
func TestWikiRevertButtonShowsForAppliedRevertable(t *testing.T) {
	m := wikiProbeModel(100, 40, "a4.bbr_active", "A4", true, 0)
	if !hasActionRow(m, wikiRowRevertButton) {
		t.Fatalf("revert row missing for applied+revertable probe (A4)")
	}
	if hasActionRow(m, wikiRowApplyButton) {
		t.Fatalf("apply row must not show for an already-applied probe")
	}
	// The revert pill hit-test resolves at its rendered Y and misses one row off.
	ry, ok := m.wikiActionRowY(wikiRowRevertButton)
	if !ok {
		t.Fatalf("revert row not shown")
	}
	x := pillRanges([]string{t2(m.lang, kWikiRevertButton)}, wikiBackStartCol)[0][0] + 1
	if !m.wikiRevertAtClick(x, ry) {
		t.Fatalf("revert hit-test missed at x=%d y=%d", x, ry)
	}
	if m.wikiRevertAtClick(x, ry+1) {
		t.Fatalf("revert hit-test matched off its row")
	}
}

// TestWikiRevertButtonHiddenWhenNotApplied: a NOT-applied probe shows apply, not revert.
func TestWikiRevertButtonHiddenWhenNotApplied(t *testing.T) {
	m := wikiProbeModel(100, 40, "a4.bbr_active", "A4", false, 0)
	if hasActionRow(m, wikiRowRevertButton) {
		t.Fatalf("revert row must not show for a not-applied probe")
	}
	if !hasActionRow(m, wikiRowApplyButton) {
		t.Fatalf("apply row should show for a not-applied non-informational probe")
	}
	// Hidden revert hit-test must always miss.
	if m.wikiRevertAtClick(4, m.wikiBackRow()) {
		t.Fatalf("hidden revert pill hit-test must miss")
	}
}

// TestWikiRevertButtonHiddenForInformational: an informational probe (e.g. A2 access
// policy) never shows revert even when "applied".
func TestWikiRevertButtonHiddenForInformational(t *testing.T) {
	m := wikiProbeModel(100, 40, "a2.permitroot", "A2", true, 0)
	// The shared helper does not set Informational; flip it on for this probe.
	m.dashAuditRaw[0].Informational = true
	if hasActionRow(m, wikiRowRevertButton) {
		t.Fatalf("revert row must not show for an informational probe")
	}
}

// TestWikiRevertButtonHiddenForNonRevertableStep: an applied probe whose step is NOT
// revertable (A7 cleanup, A8 reboot) hides the revert row.
func TestWikiRevertButtonHiddenForNonRevertableStep(t *testing.T) {
	m7 := wikiProbeModel(100, 40, "a7.something", "A7", true, 0)
	if hasActionRow(m7, wikiRowRevertButton) {
		t.Fatalf("revert row must not show for a non-revertable step (A7)")
	}
	m8 := wikiProbeModel(100, 40, "a8.x", "A8", true, 0)
	if hasActionRow(m8, wikiRowRevertButton) {
		t.Fatalf("revert row must not show for A8 (non-revertable)")
	}
}

// TestWikiRevertClickLaunchesRevert: clicking the [Откатить] pill launches the engine
// "revert" command over this probe's step.
func TestWikiRevertClickLaunchesRevert(t *testing.T) {
	m := wikiProbeModel(100, 40, "a4.bbr_active", "A4", true, 0)
	ry, ok := m.wikiActionRowY(wikiRowRevertButton)
	if !ok {
		t.Fatalf("revert row not present")
	}
	// Leave host empty so start()'s validation short-circuits before any dial.
	m.inputs[fHost].SetValue("")
	m.inputs[fPass].SetValue("secret")
	x := pillRanges([]string{t2(m.lang, kWikiRevertButton)}, wikiBackStartCol)[0][0] + 1
	next, _ := m.Update(tea.MouseClickMsg{X: x, Y: ry, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.command != "revert" {
		t.Fatalf("revert click → command=%q, want \"revert\"", mm.command)
	}
	if len(mm.cmds) != 1 || mm.cmds[0] != "A4" {
		t.Fatalf("revert click → cmds=%v, want [A4]", mm.cmds)
	}
}

// TestWikiRevertKeyLaunchesRevert: pressing 'r' on an applied+revertable probe
// launches the revert (only when the row is shown).
func TestWikiRevertKeyLaunchesRevert(t *testing.T) {
	m := wikiProbeModel(100, 40, "a4.bbr_active", "A4", true, 0)
	m.inputs[fHost].SetValue("")
	m.inputs[fPass].SetValue("secret")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	mm := next.(model)
	if mm.command != "revert" || len(mm.cmds) != 1 || mm.cmds[0] != "A4" {
		t.Fatalf("'r' → command=%q cmds=%v, want revert [A4]", mm.command, mm.cmds)
	}

	// 'r' is a no-op when the revert row is NOT shown (probe not applied).
	mn := wikiProbeModel(100, 40, "a4.bbr_active", "A4", false, 0)
	n2, _ := mn.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if mn2 := n2.(model); mn2.command == "revert" {
		t.Fatalf("'r' launched a revert when the row was hidden")
	}
}

// TestWikiUpdateConfirmTwoStep: the A8 [Обновить и перезагрузить] button must NOT
// launch on the first activation — it arms wikiUpdateConfirm — and only launches A8
// on the second activation (Enter).
func TestWikiUpdateConfirmTwoStep(t *testing.T) {
	m := wikiProbeModel(100, 40, "a4.bbr_active", "A4", false, 5) // pending>0 → update row shown
	if !hasActionRow(m, wikiRowUpdateButton) {
		t.Fatalf("update row missing despite PendingUpgrades>0")
	}

	// First activation: press 'u' → arms the confirm, does NOT launch.
	n1, _ := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	m1 := n1.(model)
	if !m1.wikiUpdateConfirm {
		t.Fatalf("'u' did not arm the A8 reboot confirm")
	}
	if m1.command == "step" {
		t.Fatalf("'u' launched A8 before confirm")
	}
	// The hint switches to the confirm prompt (assert on a stable leading substring;
	// the full localized line may be width-truncated by contentLine).
	confirmHint := t2(m1.lang, kWikiUpdateConfirm)
	if !strings.Contains(m1.wikiView(), firstRunes(confirmHint, 20)) {
		t.Fatalf("armed confirm did not switch the hint to the confirm prompt")
	}
	// And the default hint is NOT shown while armed.
	if strings.Contains(m1.wikiView(), t2(m1.lang, kWikiHint)) {
		t.Fatalf("default hint still shown while confirm armed")
	}

	// Second activation: Enter → launches A8 (command "step", cmds [A8]).
	m1.inputs[fHost].SetValue("") // empty host → no dial goroutine
	m1.inputs[fPass].SetValue("secret")
	n2, _ := m1.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := n2.(model)
	if m2.command != "step" {
		t.Fatalf("after confirm Enter, command=%q want \"step\"", m2.command)
	}
	if len(m2.cmds) != 1 || m2.cmds[0] != "A8" {
		t.Fatalf("after confirm Enter, cmds=%v want [A8]", m2.cmds)
	}
}

// TestWikiUpdateConfirmEscCancels: with a pending confirm, Esc cancels it (does NOT
// navigate back) and does not launch A8.
func TestWikiUpdateConfirmEscCancels(t *testing.T) {
	m := wikiProbeModel(100, 40, "a4.bbr_active", "A4", false, 5)
	m.wikiUpdateConfirm = true

	n, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	mm := n.(model)
	if mm.wikiUpdateConfirm {
		t.Fatalf("Esc did not cancel the pending confirm")
	}
	if mm.phase != phaseWiki {
		t.Fatalf("Esc with pending confirm navigated to %v, want stay on phaseWiki", mm.phase)
	}
	if mm.command == "step" {
		t.Fatalf("Esc launched A8")
	}
}

// TestWikiUpdateFirstClickArmsConfirm: a mouse click on [Обновить] arms the confirm
// (does not launch); a second non-update click cancels it.
func TestWikiUpdateFirstClickArmsConfirm(t *testing.T) {
	m := wikiProbeModel(100, 40, "a4.bbr_active", "A4", false, 5)
	// FEATURE B: pills share one row; pillXForKind (package helper) gives a click X
	// inside a kind's pill.
	btnY := m.wikiButtonsRowY()
	upX, _ := pillXForKind(m, wikiRowUpdateButton)
	n1, _ := m.Update(tea.MouseClickMsg{X: upX, Y: btnY, Button: tea.MouseLeft})
	m1 := n1.(model)
	if !m1.wikiUpdateConfirm {
		t.Fatalf("update click did not arm the confirm")
	}
	if m1.command == "step" {
		t.Fatalf("update click launched A8 before confirm")
	}
	// A second, unrelated click (the back pill, NOT the update pill) cancels the
	// confirm (harmless): the update-pill hit-test misses, so the confirm-swallow
	// branch fires and clears it.
	backX, _ := pillXForKind(m1, wikiRowBack)
	n2, _ := m1.Update(tea.MouseClickMsg{X: backX, Y: m1.wikiBackRow(), Button: tea.MouseLeft})
	if m2 := n2.(model); m2.wikiUpdateConfirm {
		t.Fatalf("a second click did not cancel the pending confirm")
	}
}
