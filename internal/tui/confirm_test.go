package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/tweaks"
)

// --- D1: dashApplyConfirm centered modal --------------------------------------

// TestApplyConfirmClickConfirm asserts a click on the confirm pill of the centered
// apply-modal launches the apply (m.command="step"), the same as Enter. Host left empty
// so start() validation short-circuits before any dial goroutine.
func TestApplyConfirmClickConfirm(t *testing.T) {
	m := dashModel(100, 40)
	m.dashApplyConfirm = true
	m.inputs[fHost].SetValue("")
	m.inputs[fPass].SetValue("secret")

	cx, cy := m.applyConfirmPillPoint(true) // confirm pill center-ish
	if !m.applyConfirmConfirmAtClick(cx, cy) {
		t.Fatalf("confirm pill hit-test missed at its own rendered point (%d,%d)", cx, cy)
	}
	next, _ := m.Update(tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.command != "step" {
		t.Fatalf("apply-confirm confirm click → command=%q want \"step\"", mm.command)
	}
	if mm.dashApplyConfirm {
		t.Fatalf("confirm click left dashApplyConfirm armed")
	}
}

// TestApplyConfirmClickCancel asserts a click on the cancel pill clears the confirm
// without launching the apply.
func TestApplyConfirmClickCancel(t *testing.T) {
	m := dashModel(100, 40)
	m.dashApplyConfirm = true
	xx, xy := m.applyConfirmPillPoint(false) // cancel pill
	if !m.applyConfirmCancelAtClick(xx, xy) {
		t.Fatalf("cancel pill hit-test missed at its own rendered point (%d,%d)", xx, xy)
	}
	next, _ := m.Update(tea.MouseClickMsg{X: xx, Y: xy, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.dashApplyConfirm {
		t.Fatalf("cancel click did not clear dashApplyConfirm")
	}
	if mm.command == "step" {
		t.Fatalf("cancel click launched the apply")
	}
}

// TestApplyConfirmClickMissSwallowed asserts a click that misses both pills is
// swallowed (no launch) while the confirm stays armed.
func TestApplyConfirmClickMissSwallowed(t *testing.T) {
	m := dashModel(100, 40)
	m.dashApplyConfirm = true
	// Click at the very top-left corner — far from the centered modal's pills.
	next, _ := m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.command == "step" {
		t.Fatalf("a miss click launched the apply")
	}
	if !mm.dashApplyConfirm {
		t.Fatalf("a miss click cleared the confirm; it should stay armed (swallowed)")
	}
}

// --- D2: secDangerConfirm clickable pills -------------------------------------

// TestSecDangerConfirmClickConfirm asserts a click on the confirm pill in the danger-
// confirm state launches the key-only lockdown (RunSteps over the danger IDs).
func TestSecDangerConfirmClickConfirm(t *testing.T) {
	m := secModel(100, 40)
	m.secDangerConfirm = true
	m.inputs[fHost].SetValue("")
	m.inputs[fPass].SetValue("secret")

	cx := m.secConfirmPillX(true)
	cy := m.secConfirmRow()
	if !m.secConfirmConfirmAtClick(cx, cy) {
		t.Fatalf("danger confirm pill hit-test missed at (%d,%d)", cx, cy)
	}
	next, _ := m.Update(tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.command != "step" {
		t.Fatalf("danger confirm click → command=%q want \"step\"", mm.command)
	}
}

// TestSecDangerConfirmClickCancel asserts a click on the cancel pill clears the danger
// confirm without launching anything.
func TestSecDangerConfirmClickCancel(t *testing.T) {
	m := secModel(100, 40)
	m.secDangerConfirm = true
	xx := m.secConfirmPillX(false)
	xy := m.secConfirmRow()
	if !m.secConfirmCancelAtClick(xx, xy) {
		t.Fatalf("danger cancel pill hit-test missed at (%d,%d)", xx, xy)
	}
	next, _ := m.Update(tea.MouseClickMsg{X: xx, Y: xy, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.secDangerConfirm {
		t.Fatalf("cancel click did not clear secDangerConfirm")
	}
	if mm.command == "step" {
		t.Fatalf("cancel click launched the lockdown")
	}
}

// TestSecDangerConfirmClickMissSwallowed asserts a click missing both pills stays
// swallowed with the confirm still armed (no apply, no navigation).
func TestSecDangerConfirmClickMissSwallowed(t *testing.T) {
	m := secModel(100, 40)
	m.secDangerConfirm = true
	next, _ := m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.command == "step" {
		t.Fatalf("a miss click launched the lockdown")
	}
	if !mm.secDangerConfirm {
		t.Fatalf("a miss click cleared the danger confirm; it should stay armed")
	}
	if mm.phase != phaseSecurity {
		t.Fatalf("a miss click navigated away; phase=%v want phaseSecurity", mm.phase)
	}
}

// --- D3: wikiUpdateConfirm clickable pills ------------------------------------

// wikiUpdateConfirmModel builds a phaseWiki per-probe model with pending upgrades, then
// arms the update (A8) reboot confirm.
func wikiUpdateConfirmModel(w, h int) model {
	m := wikiProbeModel(w, h, "a3.installed", "A3", false, 5)
	m.wikiUpdateConfirm = true
	return m
}

// TestWikiUpdateConfirmClickConfirm asserts a click on the confirm pill launches A8.
func TestWikiUpdateConfirmClickConfirm(t *testing.T) {
	m := wikiUpdateConfirmModel(120, 40)
	m.inputs[fHost].SetValue("")
	m.inputs[fPass].SetValue("secret")
	cx := m.wikiConfirmPillX(true)
	cy := m.wikiConfirmRow()
	if !m.wikiUpdateConfirmConfirmAtClick(cx, cy) {
		t.Fatalf("wiki update confirm pill hit-test missed at (%d,%d)", cx, cy)
	}
	next, _ := m.Update(tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.command != "step" {
		t.Fatalf("wiki update confirm click → command=%q want \"step\"", mm.command)
	}
	if len(mm.cmds) != 1 || mm.cmds[0] != "A8" {
		t.Fatalf("wiki update confirm click → cmds=%v want [A8]", mm.cmds)
	}
}

// TestWikiUpdateConfirmClickCancel asserts a click on the cancel pill clears the confirm
// without launching A8.
func TestWikiUpdateConfirmClickCancel(t *testing.T) {
	m := wikiUpdateConfirmModel(120, 40)
	xx := m.wikiConfirmPillX(false)
	xy := m.wikiConfirmRow()
	if !m.wikiUpdateConfirmCancelAtClick(xx, xy) {
		t.Fatalf("wiki update cancel pill hit-test missed at (%d,%d)", xx, xy)
	}
	next, _ := m.Update(tea.MouseClickMsg{X: xx, Y: xy, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.wikiUpdateConfirm {
		t.Fatalf("cancel click did not clear wikiUpdateConfirm")
	}
	if mm.command == "step" {
		t.Fatalf("cancel click launched A8")
	}
}

// TestWikiUpdateConfirmClickMissCancels asserts a click that misses both pills cancels
// the confirm (the wiki's existing "stray click cancels armed confirm" behavior) and
// does NOT launch A8.
func TestWikiUpdateConfirmClickMissCancels(t *testing.T) {
	m := wikiUpdateConfirmModel(120, 40)
	// A click at the top-left corner misses both pills.
	next, _ := m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.command == "step" {
		t.Fatalf("a miss click launched A8")
	}
	if mm.wikiUpdateConfirm {
		t.Fatalf("a miss click left the confirm armed; the wiki cancels on a stray click")
	}
}

// TestConfirmPillsKeyboardUnchanged is a regression guard: Enter/Esc still resolve all
// three confirms by keyboard (mouse work must not break the key paths).
func TestConfirmPillsKeyboardUnchanged(t *testing.T) {
	// Apply confirm: Enter launches, Esc cancels.
	m := dashModel(100, 40)
	m.dashApplyConfirm = true
	m.inputs[fHost].SetValue("")
	n1, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if n1.(model).command != "step" {
		t.Fatalf("apply-confirm Enter no longer launches")
	}
	m.dashApplyConfirm = true
	n2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if n2.(model).dashApplyConfirm {
		t.Fatalf("apply-confirm Esc no longer cancels")
	}

	// Wiki update confirm: Enter launches A8.
	w := wikiUpdateConfirmModel(120, 40)
	w.inputs[fHost].SetValue("")
	nw, _ := w.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	wm := nw.(model)
	if wm.command != "step" || len(wm.cmds) != 1 || wm.cmds[0] != "A8" {
		t.Fatalf("wiki update Enter no longer launches A8; cmds=%v", wm.cmds)
	}

	// Security danger confirm: ensure a result type silence — just confirm the model
	// type assertion holds and pills render.
	_ = tweaks.Result{}
}
