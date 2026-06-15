package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// 'm' opens the context menu (items built, sel on the first ENABLED item); Esc closes.
func TestFilesMenuOpenClose(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "a.conf"}})
	m.files.sel = 1
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'm'})
	m = n.(model)
	if !m.files.menuOpen {
		t.Fatal("'m' must open the context menu")
	}
	if len(m.files.menuItems) == 0 {
		t.Fatal("menu must be populated")
	}
	if !m.files.menuItems[m.files.menuSel].enabled {
		t.Fatal("menuSel must land on an enabled item")
	}
	n, _ = m.filesKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = n.(model)
	if m.files.menuOpen {
		t.Fatal("Esc must close the menu")
	}
}

// Paste is disabled when the clipboard is empty and enabled once a clip is set.
func TestFilesMenuPasteEnablement(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: "a"}})
	m.files.sel = 0
	// Empty clipboard → Paste present but disabled.
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'm'})
	m = n.(model)
	if it, ok := findMenuItem(m.files.menuItems, "v"); !ok || it.enabled {
		t.Fatalf("Paste must be present+disabled with empty clip: ok=%v enabled=%v", ok, it.enabled)
	}
	m.files.cancelMenu()
	// Set a clip → Paste enabled.
	m.files.clip = fileClip{path: "/x/y"}
	n, _ = m.filesKey(tea.KeyPressMsg{Code: 'm'})
	m = n.(model)
	if it, ok := findMenuItem(m.files.menuItems, "v"); !ok || !it.enabled {
		t.Fatalf("Paste must be enabled with a clip set: ok=%v enabled=%v", ok, it.enabled)
	}
}

// On the ".." parent marker, the mutating items are disabled (can't rename/delete "..").
func TestFilesMenuDisabledOnParent(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "a"}})
	m.files.sel = 0 // ".."
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'm'})
	m = n.(model)
	for _, key := range []string{"r", "d", "c", "x"} {
		if it, ok := findMenuItem(m.files.menuItems, key); ok && it.enabled {
			t.Fatalf("menu item %q must be disabled on '..'", key)
		}
	}
}

// The menu suppresses listing keys: 'j' while the menu is open moves menuSel, not sel.
func TestFilesMenuSuppressesListing(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "a"}, {name: "b"}})
	m.files.sel = 0
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'm'})
	m = n.(model)
	selBefore := m.files.sel
	menuSelBefore := m.files.menuSel
	n, _ = m.filesKey(tea.KeyPressMsg{Code: 'j'})
	m = n.(model)
	if m.files.sel != selBefore {
		t.Fatal("listing sel must not move while the menu is open")
	}
	if m.files.menuSel == menuSelBefore {
		t.Fatal("'j' must move the menu selection")
	}
}

// Picking "New folder" from the menu opens the new-folder prompt (same dispatch as 'n').
func TestFilesMenuPickRoutesToOp(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: "a"}})
	m.files.sel = 0
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'm'})
	m = n.(model)
	// Move menuSel onto the New-folder item, then Enter.
	idx, ok := menuIndexOf(m.files.menuItems, "n")
	if !ok {
		t.Fatal("menu missing New-folder item")
	}
	m.files.menuSel = idx
	n, _ = m.filesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = n.(model)
	if m.files.menuOpen {
		t.Fatal("picking an item must close the menu")
	}
	if m.files.promptKind != fpNewDir {
		t.Fatalf("picking New folder must open the new-folder prompt: kind=%d", m.files.promptKind)
	}
}

// Picking "Delete" routes to the delete confirm (same dispatch as 'd').
func TestFilesMenuPickDelete(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "victim"}})
	m.files.sel = 1
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'm'})
	m = n.(model)
	idx, ok := menuIndexOf(m.files.menuItems, "d")
	if !ok {
		t.Fatal("menu missing Delete item")
	}
	m.files.menuSel = idx
	n, _ = m.filesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = n.(model)
	if m.files.promptKind != fpConfirmDelete || m.files.promptArg != "victim" {
		t.Fatalf("picking Delete must open the delete confirm: kind=%d arg=%q", m.files.promptKind, m.files.promptArg)
	}
}

// test helpers ----------------------------------------------------------------

func findMenuItem(items []fmMenuItem, key string) (fmMenuItem, bool) {
	for _, it := range items {
		if it.key == key {
			return it, true
		}
	}
	return fmMenuItem{}, false
}

func menuIndexOf(items []fmMenuItem, key string) (int, bool) {
	for i, it := range items {
		if it.key == key {
			return i, true
		}
	}
	return 0, false
}
