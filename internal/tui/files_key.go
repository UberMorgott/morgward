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
	// The context menu takes ALL keys while open (checked FIRST, before the prompt/addr/
	// listing gates, so listing keys are suppressed under the menu just like under a prompt).
	if m.files.menuOpen {
		return m.filesMenuKey(msg)
	}
	// A prompt/confirm takes ALL keys while open (listing keys suppressed), same gate as the
	// address bar. Checked before addrFocus/listing so a half-typed name can't leak into nav.
	if m.files.prompting() {
		return m.filesPromptKey(msg)
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
	case "m":
		// Open the context menu for the selected entry (keyboard fallback for right-click).
		// Anchor near the selected row; the v1 render is centered so the anchor is advisory.
		m = m.openMenu(0, filesListTopRow+(m.files.sel-m.files.scroll))
		return m, nil
	default:
		// Mutating-op shortcuts. None collide with the nav keys above (↑/↓/k/j, enter,
		// backspace, ':'/'/', '.') nor the workspace keys consumed upstream (ctrl+1/2/q, tab).
		return m.filesOpKey(msg)
	}
}

// filesOpKey dispatches the mutating-op keyboard shortcuts (only reached when not prompting
// and not address-focused). Ops needing a value open a text prompt; delete opens a confirm;
// copy/cut/paste/copy-path/properties act immediately. Unknown keys are swallowed.
func (m model) filesOpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.files
	switch msg.String() {
	case "n": // New folder
		f.openPrompt(fpNewDir, "", t(m.lang, kFmPromptNewDir))
		return m, nil
	case "N": // New file
		f.openPrompt(fpNewFile, "", t(m.lang, kFmPromptNewFile))
		return m, nil
	case "r": // Rename (seed with the current name)
		if name, ok := f.selectedName(); ok {
			f.openPrompt(fpRename, name, t(m.lang, kFmPromptRename))
		}
		return m, nil
	case "d", "delete": // Delete (yes/no confirm)
		if name, ok := f.selectedName(); ok {
			f.openConfirm(fpConfirmDelete, name, t(m.lang, kFmConfirmDelete)+" "+name+"?")
		}
		return m, nil
	case "c": // Copy
		if name, ok := f.selectedName(); ok {
			f.clip = fileClip{path: joinPath(f.cwd, name), cut: false}
			f.err = t(m.lang, kFmCopied) + " " + name
		}
		return m, nil
	case "x": // Cut
		if name, ok := f.selectedName(); ok {
			f.clip = fileClip{path: joinPath(f.cwd, name), cut: true}
			f.err = t(m.lang, kFmCut) + " " + name
		}
		return m, nil
	case "v": // Paste (overwrite gated behind a confirm)
		return m.filesPaste()
	case "g": // chmod
		if name, ok := f.selectedName(); ok {
			f.openPromptFor(fpChmod, name, t(m.lang, kFmPromptChmod))
		}
		return m, nil
	case "o": // chown
		if name, ok := f.selectedName(); ok {
			f.openPromptFor(fpChown, name, t(m.lang, kFmPromptChown))
		}
		return m, nil
	case "p": // Properties (stat)
		if name, ok := f.selectedName(); ok {
			f.opProperties(name)
		}
		return m, nil
	case "y": // Copy path to host clipboard
		if name, ok := f.selectedName(); ok {
			f.opCopyPath(name)
		}
		return m, nil
	case "w": // Download the selected file (Write to disk) — free key, no collision
		return m.filesOpenDownloadPrompt()
	case "u": // Upload a local file into cwd — free key, no collision
		return m.filesOpenUploadPrompt()
	}
	return m, nil
}

// filesOpenDownloadPrompt opens the download prompt for the selected file, seeded with the
// resolved local destination (downloads dir + basename) so the operator can edit it. A
// directory entry / ".." is not downloadable (no-op). Blocked while a transfer is in flight.
func (m model) filesOpenDownloadPrompt() (tea.Model, tea.Cmd) {
	f := m.files
	if f.transferring {
		return m, nil
	}
	e, ok := f.selectedEntry()
	if !ok || e.isDir || e.name == ".." {
		return m, nil // only regular files are downloadable here
	}
	f.openPrompt(fpDownload, downloadLocalDest(e.name), t(m.lang, kFmPromptDownload))
	f.promptArg = e.name // dispatch target is the REMOTE name; the value is the local dest
	return m, nil
}

// filesOpenUploadPrompt opens the upload prompt for a local source path (empty input; the
// operator types/pastes a host path). Blocked while a transfer is in flight.
func (m model) filesOpenUploadPrompt() (tea.Model, tea.Cmd) {
	f := m.files
	if f.transferring {
		return m, nil
	}
	f.openPrompt(fpUpload, "", t(m.lang, kFmPromptUpload))
	return m, nil
}

// filesPaste pastes the clipboard into cwd; when a same-named entry already exists it opens
// an overwrite confirm instead of clobbering silently. No-op on an empty clipboard.
func (m model) filesPaste() (tea.Model, tea.Cmd) {
	f := m.files
	if f.clip.path == "" {
		return m, nil
	}
	if f.hasEntry(f.clipBaseName()) {
		f.openConfirm(fpConfirmPaste, f.clipBaseName(), t(m.lang, kFmConfirmOverwrite)+" "+f.clipBaseName()+"?")
		return m, nil
	}
	f.opPaste()
	return m, nil
}

// filesActionClick dispatches an action-bar pill click to the same op the keyboard
// shortcut runs. New opens the new-folder prompt; Rename/Delete act on the selected entry;
// Open/Download/Upload are byte-transfer ops that land in a later task (no-op for now).
func (m model) filesActionClick(act fmAction) (tea.Model, tea.Cmd) {
	f := m.files
	switch act {
	case fmActNew:
		f.openPrompt(fpNewDir, "", t(m.lang, kFmPromptNewDir))
	case fmActRename:
		if name, ok := f.selectedName(); ok {
			f.openPrompt(fpRename, name, t(m.lang, kFmPromptRename))
		}
	case fmActDelete:
		if name, ok := f.selectedName(); ok {
			f.openConfirm(fpConfirmDelete, name, t(m.lang, kFmConfirmDelete)+" "+name+"?")
		}
	case fmActDownload:
		return m.filesOpenDownloadPrompt()
	case fmActUpload:
		return m.filesOpenUploadPrompt()
	case fmActOpen:
		// In-TUI file open is a later task. No-op.
	}
	return m, nil
}

// filesPromptKey routes keys while a prompt/confirm is open: Esc cancels; a confirm reads
// y/Enter (yes) vs anything else (no); a text prompt reads Enter (dispatch) and forwards
// other keys to the input.
func (m model) filesPromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.files
	if msg.String() == "esc" {
		f.cancelPrompt()
		return m, nil
	}
	if f.isConfirm() {
		// A DESTRUCTIVE delete requires an EXPLICIT y/Y — a stray Enter (e.g. a mis-keyed
		// 'd' followed by Enter) must NOT delete. The non-destructive paste-overwrite confirm
		// also accepts Enter as yes. Any other key (incl 'n') cancels without acting.
		yes := msg.String() == "y" || msg.String() == "Y"
		if f.promptKind == fpConfirmPaste && msg.String() == "enter" {
			yes = true
		}
		var cmd tea.Cmd
		if yes {
			cmd = m.filesDispatchPrompt("")
		}
		f.cancelPrompt()
		return m, cmd
	}
	if msg.String() == "enter" {
		val := trimSpaceField(f.prompt.Value())
		cmd := m.filesDispatchPrompt(val)
		f.cancelPrompt()
		return m, cmd
	}
	var cmd tea.Cmd
	f.prompt, cmd = f.prompt.Update(msg)
	return m, cmd
}

// filesDispatchPrompt runs the op for the active promptKind with the entered value (text
// prompts) or the stashed promptArg (confirms). Called with the prompt still open; the
// caller closes it afterward. An empty text value is a no-op (nothing to create/rename to).
// Returns a non-nil tea.Cmd ONLY for the async transfer kinds (download/upload); the
// synchronous ops return nil.
func (m model) filesDispatchPrompt(val string) tea.Cmd {
	f := m.files
	switch f.promptKind {
	case fpNewDir:
		if val != "" {
			f.opNewDir(val)
		}
	case fpNewFile:
		if val != "" {
			f.opNewFile(val)
		}
	case fpRename:
		if val != "" && val != f.promptArg {
			f.opRename(f.promptArg, val)
		}
	case fpChmod:
		if val != "" {
			f.opChmod(f.promptArg, val)
		}
	case fpChown:
		if val != "" {
			f.opChown(f.promptArg, val)
		}
	case fpConfirmDelete:
		f.opDelete(f.promptArg)
	case fpConfirmPaste:
		f.opPaste()
	case fpDownload:
		// promptArg is the remote basename being downloaded; val is the (edited) local dest.
		// The download Cmd uses the resolved dest from the prompt value.
		if val != "" {
			return m.filesStartDownloadTo(f.promptArg, val)
		}
	case fpUpload:
		if val != "" {
			_, cmd := m.filesStartUpload(val)
			return cmd
		}
	}
	return nil
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
