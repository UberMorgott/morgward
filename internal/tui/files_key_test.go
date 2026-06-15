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

// 'r' opens a rename prompt seeded with the selected name; the listing keys are suppressed
// while it's open (a typed 'j' edits the input, doesn't move the selection).
func TestFilesKeyRenamePromptSuppressesListing(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "old.conf"}})
	m.files.sel = 1
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'r'})
	m = n.(model)
	if m.files.promptKind != fpRename || m.files.prompt.Value() != "old.conf" {
		t.Fatalf("'r' must open a rename prompt seeded with the name: kind=%d val=%q", m.files.promptKind, m.files.prompt.Value())
	}
	// A 'j' now edits the prompt (listing suppressed): sel stays put, input grows.
	selBefore := m.files.sel
	n, _ = m.filesKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = n.(model)
	if m.files.sel != selBefore {
		t.Fatal("listing key must be suppressed while prompting")
	}
	// Esc cancels back to no prompt.
	n, _ = m.filesKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = n.(model)
	if m.files.prompting() {
		t.Fatal("Esc must close the prompt")
	}
}

// 'd' opens a delete CONFIRM (not an immediate delete); 'n' (no) closes it WITHOUT acting.
func TestFilesKeyDeleteConfirmNoCancels(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "victim"}})
	m.files.sel = 1
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'd'})
	m = n.(model)
	if m.files.promptKind != fpConfirmDelete || m.files.promptArg != "victim" {
		t.Fatalf("'d' must open a delete confirm for the selected entry: kind=%d arg=%q", m.files.promptKind, m.files.promptArg)
	}
	if !m.files.isConfirm() {
		t.Fatal("delete prompt must be a yes/no confirm")
	}
	// 'n' = no → confirm closes, nothing deleted (nil cli would have set err on a real op).
	n, _ = m.filesKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = n.(model)
	if m.files.prompting() {
		t.Fatal("'n' must close the confirm")
	}
	if m.files.err != "" {
		t.Fatalf("'n' must not run the delete op (no err expected), got %q", m.files.err)
	}
}

// A bare Enter must NOT confirm a DESTRUCTIVE delete — only an explicit y/Y. A stray Enter
// after a mis-keyed 'd' cancels the confirm without deleting.
func TestFilesKeyDeleteRequiresExplicitY(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "victim"}})
	m.files.sel = 1
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'd'})
	m = n.(model)
	if m.files.promptKind != fpConfirmDelete {
		t.Fatalf("'d' must open a delete confirm: kind=%d", m.files.promptKind)
	}
	// A bare Enter must cancel (NOT run the delete) — nil cli would have set f.err if it ran.
	n, _ = m.filesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = n.(model)
	if m.files.prompting() {
		t.Fatal("Enter must close the delete confirm")
	}
	if m.files.err != "" {
		t.Fatalf("bare Enter must NOT run the delete op, got err=%q", m.files.err)
	}
}

// Tab pressed mid-prompt must NOT flip tabs and abandon the prompt; the prompt stays open
// and the tab is unchanged.
func TestFilesTabMidPromptKeepsPrompt(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "old"}})
	m.files.sel = 1
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'r'}) // open rename prompt
	m = n.(model)
	if !m.files.prompting() {
		t.Fatal("precondition: rename prompt should be open")
	}
	// Route through workspaceKey (the real entry point that intercepts Tab) with a bare Tab.
	n, _ = m.workspaceKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = n.(model)
	if m.wsTab != wsFiles {
		t.Fatal("Tab mid-prompt must NOT switch to the Terminal tab")
	}
	if !m.files.prompting() {
		t.Fatal("Tab mid-prompt must leave the prompt open")
	}
}

// '..' is never a mutation target: 'r'/'d' on the parent marker are no-ops.
func TestFilesKeyMutationRejectsParentMarker(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "a"}})
	m.files.sel = 0 // ".."
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'r'})
	m = n.(model)
	if m.files.prompting() {
		t.Fatal("rename on '..' must be a no-op")
	}
	n, _ = m.filesKey(tea.KeyPressMsg{Code: 'd'})
	m = n.(model)
	if m.files.prompting() {
		t.Fatal("delete on '..' must be a no-op")
	}
}

// Copy sets the clipboard (no SSH); a later Paste uses it.
func TestFilesKeyCopySetsClip(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "src"}})
	m.files.sel = 1
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'c'})
	m = n.(model)
	if m.files.clip.path != "/etc/src" || m.files.clip.cut {
		t.Fatalf("copy clip = %+v want /etc/src cut=false", m.files.clip)
	}
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
