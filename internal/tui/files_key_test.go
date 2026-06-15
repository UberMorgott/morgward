package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// filesKeyModel builds a Files-tab model with a pre-loaded listing and no live SSH (nil
// cli) — selection/focus key paths don't touch the transport, so they are unit-testable.
func filesKeyModel(entries []fileEntry) model {
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseTerminal
	m.wsTab = wsFiles
	m.files = newFileSession(nil, "/etc")
	m.files.entry = entries
	return m
}

// ↓/↑ move the selection within the visible slice (clamped).
func TestFilesKeyMovesSelection(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "a"}, {name: "b"}})
	next, _ := m.filesKey(tea.KeyPressMsg{Code: 'j'})
	m = next.(model)
	if m.files.sel != 1 {
		t.Fatalf("j → sel=%d want 1", m.files.sel)
	}
	next, _ = m.filesKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(model)
	if m.files.sel != 2 {
		t.Fatalf("down → sel=%d want 2", m.files.sel)
	}
	next, _ = m.filesKey(tea.KeyPressMsg{Code: tea.KeyDown}) // past end → clamp
	m = next.(model)
	if m.files.sel != 2 {
		t.Fatalf("down past end → sel=%d want 2 (clamped)", m.files.sel)
	}
}

// ':' focuses the address bar; Esc cancels focus and discards the edit.
func TestFilesKeyAddressFocusToggle(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: "a"}})
	next, _ := m.filesKey(tea.KeyPressMsg{Code: ':'})
	m = next.(model)
	if !m.files.addrFocus {
		t.Fatal("':' must focus the address bar")
	}
	if m.files.addr.Value() != "/etc" {
		t.Fatalf("address bar seeded to cwd, got %q", m.files.addr.Value())
	}
	next, _ = m.filesKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)
	if m.files.addrFocus {
		t.Fatal("Esc must unfocus the address bar")
	}
}

// '.' toggles hidden files and re-clamps the selection into the new visible range.
func TestFilesKeyToggleHidden(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: ".dot"}, {name: "a"}})
	// Default: visible = [.., a]; select the last visible row.
	m.files.sel = 1
	next, _ := m.filesKey(tea.KeyPressMsg{Code: '.'})
	m = next.(model)
	if !m.files.showHidden {
		t.Fatal("'.' must toggle showHidden on")
	}
	if got := len(m.files.visibleEntries()); got != 3 {
		t.Fatalf("showHidden visible=%d want 3", got)
	}
	// Toggle back off; sel (was 1 → "a") must stay in range of the 2 visible rows.
	next, _ = m.filesKey(tea.KeyPressMsg{Code: '.'})
	m = next.(model)
	if m.files.sel >= len(m.files.visibleEntries()) {
		t.Fatalf("sel=%d out of visible range %d after toggle", m.files.sel, len(m.files.visibleEntries()))
	}
}
