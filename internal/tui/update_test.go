package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestUpdateCheckMsgValueCopy confirms updateCheckMsg (and the model fields that
// store its result) are fully value-copyable — the model is copied by value every
// Update, so a leaked pointer/map would corrupt state. updateCheckMsg holds only
// bool/string/error, all value-safe.
func TestUpdateCheckMsgValueCopy(t *testing.T) {
	msg := updateCheckMsg{found: true, ver: "0.2.0", err: nil}
	cp := msg // copy by value
	cp.ver = "9.9.9"
	if msg.ver != "0.2.0" {
		t.Fatalf("updateCheckMsg copy not isolated: src mutated to %q", msg.ver)
	}

	m := newModel()
	m.updateState = updAvailable
	m.updateVer = "0.2.0"
	m.wantUpdate = true
	m2 := m // copy by value
	m2.updateState = updCurrent
	m2.updateVer = "1.0.0"
	m2.wantUpdate = false
	if m.updateState != updAvailable || m.updateVer != "0.2.0" || !m.wantUpdate {
		t.Fatalf("model copy not isolated: update fields shared (state=%d ver=%q want=%v)",
			m.updateState, m.updateVer, m.wantUpdate)
	}
}

// TestUpdateCheckMsgHandler drives Update() with each updateCheckMsg outcome and
// asserts the resulting updateState/updateVer. found==true → available; found==false
// → current (NOT an error); err!=nil → error.
func TestUpdateCheckMsgHandler(t *testing.T) {
	// found=true → updAvailable + version captured
	m := newModel()
	out, _ := m.Update(updateCheckMsg{found: true, ver: "0.2.0"})
	got := out.(model)
	if got.updateState != updAvailable {
		t.Fatalf("found=true: state=%d want updAvailable", got.updateState)
	}
	if got.updateVer != "0.2.0" {
		t.Fatalf("found=true: ver=%q want 0.2.0", got.updateVer)
	}

	// found=false → updCurrent (up-to-date), version cleared
	out, _ = got.Update(updateCheckMsg{found: false})
	got = out.(model)
	if got.updateState != updCurrent {
		t.Fatalf("found=false: state=%d want updCurrent", got.updateState)
	}
	if got.updateVer != "" {
		t.Fatalf("found=false: ver=%q want empty", got.updateVer)
	}

	// err != nil → updErr
	out, _ = got.Update(updateCheckMsg{err: errors.New("network")})
	got = out.(model)
	if got.updateState != updErr {
		t.Fatalf("err: state=%d want updErr", got.updateState)
	}
}

// TestInitReturnsCmd confirms Init() returns a non-nil batch (it now includes the
// one-shot self-update check alongside Blink + the resize poll).
func TestInitReturnsCmd(t *testing.T) {
	if newModel().Init() == nil {
		t.Fatal("Init() returned nil cmd; expected a batch including checkUpdateCmd")
	}
}

// TestUpdateStripStates renders the landing in each of the four strip states and
// asserts the View is non-empty and contains the expected localized status text.
func TestUpdateStripStates(t *testing.T) {
	cases := []struct {
		name  string
		state int
		ver   string
		want  string // a substring that must appear in the rendered strip
	}{
		{"checking", updChecking, "", t2(langRU, kUpdateChecking)},
		{"current", updCurrent, "", t2(langRU, kUpdateCurrent)},
		{"available", updAvailable, "0.2.0", "0.2.0"},
		{"error", updErr, "", t2(langRU, kUpdateError)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newModel()
			m.w, m.h = 80, 24
			m.phase = phaseForm
			m.updateState = c.state
			m.updateVer = c.ver
			view := m.viewString()
			if view == "" {
				t.Fatalf("state %s: empty View", c.name)
			}
			if !strings.Contains(view, c.want) {
				t.Fatalf("state %s: View missing %q", c.name, c.want)
			}
		})
	}
}

// TestUpdateButtonVisibleOnlyWhenAvailable asserts the "Обновить" pill is rendered
// AND focusable only in updAvailable, and absent/unfocusable otherwise.
func TestUpdateButtonVisibleOnlyWhenAvailable(t *testing.T) {
	label := t2(langRU, kUpdateButtonLabel)

	// Available: pill rendered + rowUpdateButton focusable.
	m := newModel()
	m.w, m.h = 80, 24
	m.updateState = updAvailable
	m.updateVer = "0.2.0"
	if !strings.Contains(m.viewString(), label) {
		t.Fatalf("updAvailable: View missing button label %q", label)
	}
	if !containsRow(m.focusableRows(), rowUpdateButton) {
		t.Fatal("updAvailable: rowUpdateButton not focusable")
	}

	// Every non-available state: no pill, not focusable.
	for _, st := range []int{updChecking, updCurrent, updErr} {
		mm := newModel()
		mm.w, mm.h = 80, 24
		mm.updateState = st
		if strings.Contains(mm.viewString(), label) {
			t.Fatalf("state %d: button label %q should be hidden", st, label)
		}
		if containsRow(mm.focusableRows(), rowUpdateButton) {
			t.Fatalf("state %d: rowUpdateButton should not be focusable", st)
		}
	}
}

// TestUpdateButtonActivation confirms Enter on the focused update button sets
// wantUpdate (and returns a Quit cmd), and that the click path does the same.
func TestUpdateButtonActivation(t *testing.T) {
	// Keyboard: Enter on the focused button.
	m := newModel()
	m.w, m.h = 80, 24
	m.updateState = updAvailable
	m.updateVer = "0.2.0"
	m.focus = rowUpdateButton
	out, cmd := m.updateForm(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := out.(model)
	if !got.wantUpdate {
		t.Fatal("Enter on update button did not set wantUpdate")
	}
	if cmd == nil {
		t.Fatal("Enter on update button returned nil cmd; expected tea.Quit")
	}
}

// TestUpdateButtonNotActivatedWhenUnavailable confirms Enter on rowUpdateButton is
// inert when the state is not updAvailable (defensive: focus should never land
// there, but the guard must hold regardless).
func TestUpdateButtonNotActivatedWhenUnavailable(t *testing.T) {
	m := newModel()
	m.w, m.h = 80, 24
	m.updateState = updCurrent
	m.focus = rowUpdateButton
	out, _ := m.updateForm(tea.KeyPressMsg{Code: tea.KeyEnter})
	if out.(model).wantUpdate {
		t.Fatal("Enter set wantUpdate while updateState != updAvailable")
	}
}

// TestUpdateButtonClick confirms a click on the "Обновить" pill (resolved via the
// same geometry the renderer uses) sets wantUpdate and returns a Quit cmd, mirroring
// the keyboard path. It locates the strip row's screen Y from formRows order.
func TestUpdateButtonClick(t *testing.T) {
	m := newModel()
	m.w, m.h = 80, 24
	m.updateState = updAvailable
	m.updateVer = "0.2.0"

	// Find the frUpdate row's slice index → screen Y = formBodyTopRow + index.
	rows := m.formRows()
	yIdx := -1
	for i, r := range rows {
		if r.kind == frUpdate {
			yIdx = i
			break
		}
	}
	if yIdx < 0 {
		t.Fatal("frUpdate row not present in formRows when updAvailable")
	}
	y := formBodyTopRow + yIdx
	x := m.updateButtonColStart() // first cell of the pill

	hit := m.formHitAtClick(x, y)
	if !hit.ok || hit.kind != frUpdate {
		t.Fatalf("click at (%d,%d) did not hit frUpdate: %+v", x, y, hit)
	}
	out, cmd := m.formClick(x, y)
	if !out.(model).wantUpdate {
		t.Fatal("click on update pill did not set wantUpdate")
	}
	if cmd == nil {
		t.Fatal("click on update pill returned nil cmd; expected tea.Quit")
	}
}

// TestRunResultType asserts the Result type carries the fields main() reads.
func TestRunResultType(t *testing.T) {
	var r Result
	r.DoUpdate = true
	r.TargetVer = "0.2.0"
	if !r.DoUpdate || r.TargetVer != "0.2.0" {
		t.Fatal("Result fields not assignable")
	}
}

// TestUpdateStripI18nParity confirms every update-strip key resolves to a non-empty
// string in BOTH languages (RU + EN), so no state renders blank after a switch.
func TestUpdateStripI18nParity(t *testing.T) {
	keys := []stringKey{kUpdateChecking, kUpdateCurrent, kUpdateAvailable, kUpdateError, kUpdateButtonLabel}
	for _, lang := range []Lang{langRU, langEN} {
		for _, k := range keys {
			if s := t2(lang, k); s == "" {
				t.Fatalf("lang %d key %d: empty translation", lang, k)
			}
		}
	}
}

func containsRow(rows []int, want int) bool {
	for _, r := range rows {
		if r == want {
			return true
		}
	}
	return false
}
