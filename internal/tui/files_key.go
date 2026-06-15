package tui

import (
	tea "charm.land/bubbletea/v2"
)

// filesKey is the real Files-tab key handler (replaces the T4 stub). The workspace router
// (workspaceKey) has already consumed ctrl+q (exit), ctrl+1/ctrl+2 (tab switch) and a bare
// Tab (back to Terminal) before this is reached, so those are never seen here.
//
// Two modes: when the address bar is FOCUSED (addrFocus) keystrokes edit the path
// (Enter navigates to it, Esc cancels); otherwise the listing keys apply (↑/↓/k/j move the
// selection, Enter/Backspace navigate directories, ':' or '/' focuses the address bar,
// '.' toggles hidden files). Navigation does a blocking reload() — the same synchronous-SSH
// precedent openTerminal sets with its blocking Dial; a single ls is tens of ms.
func (m model) filesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.files == nil {
		return m, nil
	}
	if m.files.addrFocus {
		return m.filesAddrKey(msg)
	}
	return m.filesListKey(msg)
}

// filesAddrKey handles keys while the address bar is focused: Enter navigates to the typed
// path (cwd = trimmed value, reload, blur), Esc cancels (restore cwd into the field, blur),
// any other key edits the textinput.
func (m model) filesAddrKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		dest := trimSpaceField(m.files.addr.Value())
		if dest != "" {
			// navigateAndReload reverts cwd to the last-good dir if the typed path fails, so a
			// bad path never sticks; the error stays surfaced in f.err.
			m.files.navigateAndReload(dest)
		}
		m.files.addrFocus = false
		m.files.addr.Blur()
		m.files.addr.SetValue(m.files.cwd)
		return m, nil
	case "esc":
		m.files.addrFocus = false
		m.files.addr.Blur()
		m.files.addr.SetValue(m.files.cwd) // discard the edit
		return m, nil
	}
	var cmd tea.Cmd
	m.files.addr, cmd = m.files.addr.Update(msg)
	return m, cmd
}

// filesListKey handles keys while the listing is focused (address bar not focused).
func (m model) filesListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.files.moveSel(-1)
		m.files.keepSelVisible(m.filesListViewH())
		return m, nil
	case "down", "j":
		m.files.moveSel(1)
		m.files.keepSelVisible(m.filesListViewH())
		return m, nil
	case "enter":
		return m.filesActivateSelected()
	case "backspace":
		// Go to the parent directory (same as activating ".."); revert on a failed reload.
		m.files.navigateAndReload(parentPath(m.files.cwd))
		m.files.addr.SetValue(m.files.cwd)
		return m, nil
	case ":", "/":
		// Focus the address bar for a manual path entry, seeded to the current cwd.
		m.files.addrFocus = true
		m.files.addr.SetValue(m.files.cwd)
		cmd := m.files.addr.Focus()
		m.files.addr.CursorEnd()
		return m, cmd
	case ".":
		// Toggle hidden-file visibility; re-clamp sel into the new visible range. No reload
		// needed — visibleEntries filters the already-loaded list.
		m.files.showHidden = !m.files.showHidden
		m.files.clampSel()
		m.files.keepSelVisible(m.filesListViewH())
		return m, nil
	}
	return m, nil
}

// filesActivateSelected acts on the selected entry: descending into a directory (incl
// "..") sets cwd and reloads; a file is a no-op for now (Open lands in a later task).
func (m model) filesActivateSelected() (tea.Model, tea.Cmd) {
	vis := m.files.visibleEntries()
	if m.files.sel < 0 || m.files.sel >= len(vis) {
		return m, nil
	}
	e := vis[m.files.sel]
	if !e.isDir {
		return m, nil // Open is a later task
	}
	// Resolve the destination (".." → parent, else join) and navigate with revert-on-fail.
	dest := joinPath(m.files.cwd, e.name)
	if e.name == ".." {
		dest = parentPath(m.files.cwd)
	}
	m.files.navigateAndReload(dest)
	m.files.addr.SetValue(m.files.cwd)
	return m, nil
}

// trimSpaceField trims leading/trailing ASCII whitespace from a path typed into the
// address bar. Kept tiny + local so the import set stays minimal.
func trimSpaceField(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
