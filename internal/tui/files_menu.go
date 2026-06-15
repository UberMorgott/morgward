package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// buildMenuItems composes the context-menu rows for the CURRENTLY selected entry. Every row
// carries the SAME single-char key its keyboard shortcut uses, so a pick re-routes through
// the existing op dispatch (filesOpKey) — no op logic is duplicated here. Enable/disable
// rules: mutations (rename/delete/copy/cut/chmod/chown/properties/copy-path/download) need a
// real selected entry that is NOT the ".." parent marker; New folder/file and Upload are
// always available; Paste needs a non-empty clipboard; Download and Open both need a regular
// file (Open downloads it to a host temp and launches the local editor + sync-back).
func (m model) buildMenuItems() []fmMenuItem {
	f := m.files
	_, haveSel := f.selectedName() // ok=false on ".." or out-of-range
	e, haveEntry := f.selectedEntry()
	isRegularFile := haveEntry && !e.isDir && e.name != ".."

	return []fmMenuItem{
		{label: t(m.lang, kFmMenuNewDir), key: "n", enabled: true},
		{label: t(m.lang, kFmMenuNewFile), key: "N", enabled: true},
		{label: t(m.lang, kFmMenuOpen), key: "O", enabled: isRegularFile},
		{label: t(m.lang, kFmMenuRename), key: "r", enabled: haveSel},
		{label: t(m.lang, kFmMenuDelete), key: "d", enabled: haveSel},
		{label: t(m.lang, kFmMenuCopy), key: "c", enabled: haveSel},
		{label: t(m.lang, kFmMenuCut), key: "x", enabled: haveSel},
		{label: t(m.lang, kFmMenuPaste), key: "v", enabled: f.clip.path != ""},
		{label: t(m.lang, kFmMenuChmod), key: "g", enabled: haveSel},
		{label: t(m.lang, kFmMenuChown), key: "o", enabled: haveSel},
		{label: t(m.lang, kFmMenuProps), key: "p", enabled: haveSel},
		{label: t(m.lang, kFmMenuCopyPath), key: "y", enabled: haveSel},
		{label: t(m.lang, kFmMenuDownload), key: "w", enabled: isRegularFile},
		{label: t(m.lang, kFmMenuUpload), key: "u", enabled: true},
	}
}

// openMenu populates the menu for the selected entry, anchors it at (col,row), and places
// menuSel on the first ENABLED item. A no-op while a transfer is in flight or a prompt is
// open (those gates own the input).
func (m model) openMenu(col, row int) model {
	f := m.files
	if f == nil || f.transferring || f.prompting() {
		return m
	}
	f.menuItems = m.buildMenuItems()
	f.menuOpen = true
	f.menuRow, f.menuCol = row, col
	f.menuSel = firstEnabled(f.menuItems)
	return m
}

// firstEnabled returns the index of the first enabled item (0 if none, harmless).
func firstEnabled(items []fmMenuItem) int {
	for i, it := range items {
		if it.enabled {
			return i
		}
	}
	return 0
}

// cancelMenu closes the context menu.
func (f *fileSession) cancelMenu() {
	f.menuOpen = false
	f.menuItems = nil
	f.menuSel = 0
}

// menuMove shifts the menu selection by delta, SKIPPING disabled items, clamped to the
// item range (wrapping is avoided to keep it predictable).
func (f *fileSession) menuMove(delta int) {
	n := len(f.menuItems)
	if n == 0 {
		return
	}
	i := f.menuSel
	for step := 0; step < n; step++ {
		i += delta
		if i < 0 {
			i = 0
		}
		if i >= n {
			i = n - 1
		}
		if f.menuItems[i].enabled {
			f.menuSel = i
			return
		}
		// If we hit a clamp edge on a disabled item, stop (no enabled item that direction).
		if (delta < 0 && i == 0) || (delta > 0 && i == n-1) {
			return
		}
	}
}

// filesMenuKey routes keys while the context menu is open: ↑/↓/k/j move the selection
// (skipping disabled items), Enter picks the highlighted item, Esc dismisses.
func (m model) filesMenuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.files
	switch physKey(msg) {
	case "esc":
		f.cancelMenu()
		return m, nil
	case "up", "k":
		f.menuMove(-1)
		return m, nil
	case "down", "j":
		f.menuMove(1)
		return m, nil
	case "enter":
		return m.filesMenuPick()
	}
	return m, nil
}

// filesMenuPick dispatches the highlighted menu item to the EXISTING op handler by
// synthesizing its shortcut keypress and calling filesOpKey — so the menu and the keyboard
// share one dispatch path. A disabled item is a no-op. The menu is closed before dispatch
// so the op's own prompt/confirm can take over the input.
func (m model) filesMenuPick() (tea.Model, tea.Cmd) {
	f := m.files
	if f.menuSel < 0 || f.menuSel >= len(f.menuItems) {
		f.cancelMenu()
		return m, nil
	}
	it := f.menuItems[f.menuSel]
	f.cancelMenu()
	if !it.enabled || it.key == "" {
		return m, nil
	}
	// Synthesize the item's shortcut keypress and route through the SAME op dispatcher the
	// keyboard uses. key is a single rune (e.g. "r", "N"); its String() matches filesOpKey's
	// cases (verified: {Code:'N'}.String()=="N").
	return m.filesOpKey(tea.KeyPressMsg{Code: rune(it.key[0])})
}

// --- render -------------------------------------------------------------------

// filesMenuView renders the context menu as a CENTERED modal box over the full viewport
// (same compositing pattern as applyConfirmModalView — geometry-bug-free vs an arbitrary
// (x,y) overlay). The highlighted item uses the accent band (fileSelStyle); disabled items
// are dimmed.
func (m model) filesMenuView() string {
	f := m.files
	var rows []string
	rows = append(rows, sumHeadStyle.Render(t(m.lang, kFmMenuTitle)))
	rows = append(rows, "")
	for i, it := range f.menuItems {
		line := it.label
		switch {
		case i == f.menuSel:
			line = fileSelStyle.Render(" " + it.label + " ")
		case !it.enabled:
			line = helpStyle.Render(" " + it.label + " ")
		default:
			line = " " + it.label + " "
		}
		rows = append(rows, line)
	}
	rows = append(rows, "")
	rows = append(rows, helpStyle.Render(t(m.lang, kFmMenuHint)))

	box := modalBoxStyle.Render(strings.Join(rows, "\n"))
	cw, ch := m.boxWidth(), maxi(m.h, 1)
	return lipgloss.Place(cw, ch, lipgloss.Center, lipgloss.Center, box)
}
