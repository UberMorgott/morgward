package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/version"
)

// --- Terminal screen (phaseTerminal, 2a) --------------------------------------
//
// A vt-emulator-backed interactive terminal that reuses the phase-1 CLI shell core
// (sshx.Client.Shell). The screen frames the emulator's rendered screen in the same
// hand-drawn box chrome the other screens use, repaints on a render tick (so output
// that arrives WITHOUT a key event still shows up), and forwards key/paste/resize
// events to the remote shell. The terminal exit key is Ctrl+Q.
//
// Mouse forwarding to the remote app is DEFERRED for 2a (the spec marks it optional)
// — only keyboard + render + resize + teardown are implemented here.

// termExitKey is the keystroke that leaves the terminal screen (closes the session,
// returns to the previous screen). Ctrl+Q is chosen because Ctrl+C must pass through
// to the remote shell (SIGINT) and Esc is needed by vim/less.
const termExitKey = "ctrl+q"

// termReconnectKey redials from the dead-session / dial-error notice. openTerminal is
// the ONLY dial site and is otherwise reachable just through the nav bar, so without
// this key a session killed mid-use (the box rebooted under us) could only be revived
// by leaving the workspace and coming back.
const termReconnectKey = "r"

// termTickInterval drives the repaint while the terminal is open (~40fps). Each tick
// re-renders only when the emulator reports damage (see termSession.dirty), so an
// idle shell does not busy-repaint.
const termTickInterval = 25 * time.Millisecond

// termTickMsg is the terminal repaint heartbeat. It carries the session generation it
// was scheduled under so a tick left over from a CLOSED session (a new one may have a
// different gen) is dropped instead of repainting a stale/absent session.
type termTickMsg struct{ gen int }

// termTick schedules the next terminal repaint tick for the given generation.
func termTick(gen int) tea.Cmd {
	return tea.Tick(termTickInterval, func(time.Time) tea.Msg { return termTickMsg{gen: gen} })
}

// openTerminal dials the box with the form's connection params (mirroring the CLI
// `shell` path: --key wins, else password; a single fast Dial, no provisioning
// retry) and constructs the termSession sized to the current content area, then
// switches to phaseTerminal. On a dial/setup failure it still switches to the
// screen but records termErr so the failure is shown in place (Esc/Ctrl+Q returns).
// from is the phase to return to on exit. initialTab selects which workspace tab is shown
// first (wsTerminal for the shell, wsFiles to land in the file manager); the terminal
// session is dialed + started regardless of the initial tab.
func (m model) openTerminal(from phase, initialTab wsTab) (tea.Model, tea.Cmd) {
	// Reuse a TRULY-LIVE session: when a connected, running session is already up
	// (termLive: termClient set, no dial error, term non-nil and NOT finished) re-entering
	// the workspace must NOT redial — that would leak a second TCP/SSH transport + keepalive
	// goroutine and bump termGen needlessly. Just switch to the requested tab (ensuring the
	// FM session for wsFiles) and return; no termGen bump (only closeTerminal invalidates the
	// running render tick).
	//
	// A FINISHED session (remote shell exited via `exit`) is NOT live even though termErr is
	// "" and termClient is non-nil — reusing it would land on the static "session ended"
	// banner with no fresh shell. So this branch keys off termLive(), not just termErr/nil.
	if m.termClient != nil && m.termLive() {
		// Coming back from ANOTHER phase (Главная keeps the session but parks the model on
		// phaseDashboard) means the repaint heartbeat has retired: the tick handler drops a
		// tick whose phase is not phaseTerminal WITHOUT rescheduling it. Restart it here, or
		// termPinIfFollowing never runs again and follow mode is dead for the rest of the
		// session (new output arrives, the view stays put). A tab switch WITHIN the workspace
		// (wsTerminal↔wsFiles, both phaseTerminal) never retired the chain — scheduling a
		// second one there would double the heartbeat on every self-switch.
		reentry := m.phase != phaseTerminal
		m.termReturn = from
		m.phase = phaseTerminal
		m.wsTab = initialTab
		if initialTab == wsFiles {
			m = m.ensureFiles()
			if m.files == nil {
				m.wsTab = wsTerminal // ensureFiles made nothing (shouldn't happen with a live client)
			}
		}
		if reentry {
			// Nothing re-pinned while we were parked, so the offset trails whatever output
			// arrived there. Pin NOW rather than leaving it to the tick we are about to
			// schedule: that is up to one interval away, and the frame rendered in between
			// would draw at the stale offset and then visibly jump.
			m.termPinIfFollowing()
			return m, termTick(m.termGen)
		}
		return m, nil
	}

	// Not live but a stale/finished/errored session is still hanging around (term/files/
	// termClient non-nil): tear it down BEFORE the fresh dial so the old transport + its 30s
	// keepalive goroutine don't leak when m.termClient is overwritten below. closeTerminal is
	// nil-safe + idempotent; it bumps termGen and sets phase=termReturn, both of which the
	// fresh-dial path re-establishes (termGen++ again, phase=phaseTerminal) — harmless.
	if m.term != nil || m.files != nil || m.termClient != nil {
		m = m.closeTerminal()
	}

	m.termReturn = from
	m.termErr = ""
	m.termGen++
	m.phase = phaseTerminal
	// Start pinned to the newest output (follow mode); the offset is recomputed to the
	// bottom on the first render once the body length is known.
	m.termScroll = 0
	m.termFollow = true

	host := strings.TrimSpace(m.inputs[fHost].Value())
	user := strings.TrimSpace(m.inputs[fUser].Value())
	port := atoiDefault(strings.TrimSpace(m.inputs[fPort].Value()), 22)
	password := m.inputs[fPass].Value()
	keyPath := strings.TrimSpace(m.inputs[fKey].Value())

	// Load --key if given (key wins), else fall back to password — identical to the
	// engine/CLI dial precedence.
	var keyPEM []byte
	if keyPath != "" {
		b, err := sshx.LoadKeyFile(keyPath)
		if err != nil {
			m.termErr = t(m.lang, kTermDialFail) + ": " + err.Error()
			return m, nil
		}
		keyPEM = b
	}

	cli, err := sshx.Dial(host, port, user, password, keyPEM)
	if err != nil {
		m.termErr = t(m.lang, kTermDialFail) + ": " + err.Error()
		return m, nil
	}
	// Own the transport: stash it on the model so closeTerminal/goBack can Close() it
	// (and its 30s keepalive goroutine). termSession.close() only ends the *ssh.Session,
	// so without this every open→close cycle would leak one TCP/SSH conn + goroutine.
	m.termClient = cli

	cols, rows := m.termContentSize()
	m.term = newTermSession(cli, cols, rows)
	// Make sure the emulator/pty match the actual content area immediately (the
	// WindowSizeMsg may not re-fire until a resize), then start the repaint tick.
	m.term.resize(cols, rows)
	// Select the initial workspace tab; when landing on Files, open the FM session over
	// the same shared transport (default cwd "/root") so the tab is ready to render.
	m.wsTab = initialTab
	if initialTab == wsFiles {
		m = m.ensureFiles()
	}
	return m, termTick(m.termGen)
}

// navHomeKey / wsSwitchTerminalKey / wsSwitchFilesKey select a global tab-bar cell from
// any hub screen (left→right matching the bar: Главная · Терминал · Файлы). ctrl+1/2/3
// are reserved for tab switching (a shell rarely needs them; the FM never forwards keys),
// so they are intercepted before any per-tab routing.
const (
	navHomeKey          = "ctrl+1"
	wsSwitchTerminalKey = "ctrl+2"
	wsSwitchFilesKey    = "ctrl+3"
)

// navTarget is a cell of the global 3-cell tab bar (Главная · Терминал · Файлы), the
// single notion the bar render + hit-test + navTo router share.
type navTarget int

const (
	navHome navTarget = iota
	navTerminal
	navFiles
)

// navTo is the SINGLE navigation router for the global tab bar (bar clicks on every hub
// screen + ctrl+1/2/3). It is the one place that decides whether to keep or dial the
// terminal session:
//
//   - navHome → m.phase = phaseDashboard WITHOUT closeTerminal: term/termClient/files stay
//     alive so a return to Терминал/Файлы is instant (scrollback preserved). The terminal
//     render tick keeps its gen (no termGen bump) and harmlessly repaints in the
//     background; the Dashboard view simply doesn't draw it.
//   - navTerminal / navFiles → openTerminal, which REUSES a live session (no redial) or
//     dials fresh when none exists (or the prior dial failed). openTerminal also sets the
//     requested wsTab and ensures the FM session for navFiles.
//
// from is the phase the workspace returns to on a real exit (goBack / ctrl+q); on a
// keep-alive Home switch openTerminal re-stamps it on the next entry.
func (m model) navTo(target navTarget) (tea.Model, tea.Cmd) {
	switch target {
	case navHome:
		m.phase = phaseDashboard
		return m, nil
	case navFiles:
		return m.openTerminal(phaseDashboard, wsFiles)
	default: // navTerminal
		return m.openTerminal(phaseDashboard, wsTerminal)
	}
}

// ensureFiles lazily creates the Files session over the shared terminal transport
// (default cwd "/root") if it does not exist yet. Idempotent — a no-op once created so a
// tab round-trip preserves the browse state. The listing is loaded by a later op (T5/T6).
// When the terminal dial FAILED (openTerminal recorded termErr and left termClient nil)
// there is no live transport to open sftp over, so it creates NOTHING — a Files session
// over a nil client would nil-deref the moment a later op calls cli.SFTP().
func (m model) ensureFiles() model {
	if m.termClient == nil {
		return m // no live transport (dial failed) → no file session
	}
	if m.files == nil {
		m.files = newFileSession(m.termClient, "", m.lang)
		// Load the initial listing once, on first entry, so the tab isn't empty. A blocking
		// reload is fine here (single ls, ~tens of ms) — same synchronous-SSH precedent as
		// openTerminal's blocking Dial. Errors are surfaced inline via f.err, not fatal.
		_ = m.files.reload()
	}
	return m
}

// workspaceKey is the top-level keypress router for the terminal workspace (phaseTerminal).
// It FIRST handles the cross-tab switch keys (ctrl+1 → Terminal, ctrl+2 → Files), then
// routes the remaining keys to the active tab: the Files tab to filesKey (with a bare Tab
// switching back to Terminal), the Terminal tab to terminalKey (so the shell still gets Tab
// for completion). The terminal session keeps draining in the background regardless of tab.
func (m model) workspaceKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Workspace command keys (ctrl+q exit, ctrl+1/ctrl+2 tab switch, bare Tab) match via
	// physKey so they fire on any host layout; the per-tab routing below forwards the RAW
	// msg (terminalKey forwards native shell bytes; filesKey normalizes its own commands).
	pk := physKey(msg)
	// termExitKey (ctrl+q) closes the WHOLE workspace from EITHER tab — handled before any
	// per-tab routing so the Files tab can't trap the user (the filesKey stub would
	// otherwise swallow it). Esc is deliberately NOT a universal exit: the shell/vim needs
	// it on the Terminal tab, and the FM reserves it on the Files tab.
	if pk == termExitKey {
		m = m.closeTerminal()
		return m, nil
	}
	// While an FM prompt/confirm is open, the tab-switch keys (ctrl+1/ctrl+2/bare-Tab) must
	// NOT silently flip tabs and abandon the half-entered prompt — route them into filesKey
	// (which ignores them, leaving the prompt intact) instead. ctrl+q above still exits.
	fmPrompting := m.wsTab == wsFiles && m.files != nil && m.files.prompting()
	if !fmPrompting {
		switch pk {
		case navHomeKey:
			// Главная: return to the Dashboard via navTo, KEEPING the session alive (no
			// closeTerminal) so a return is instant.
			return m.navTo(navHome)
		case wsSwitchTerminalKey:
			m.wsTab = wsTerminal
			return m, nil
		case wsSwitchFilesKey:
			// Only switch to Files if a session actually exists after ensureFiles — when the
			// dial failed (nil termClient) ensureFiles creates nothing, so stay on Terminal
			// rather than flip to a Files tab that can't work.
			m = m.ensureFiles()
			if m.files != nil {
				m.wsTab = wsFiles
			}
			return m, nil
		}
	}
	if m.wsTab == wsFiles {
		// A bare Tab on the Files tab returns to the Terminal tab (the shell wants Tab for
		// completion, so this gesture is only meaningful while Files is shown) — but NOT while
		// a prompt is open (it would abandon the prompt); then the key falls through to
		// filesKey, which leaves the prompt untouched.
		if pk == "tab" && !fmPrompting {
			m.wsTab = wsTerminal
			return m, nil
		}
		return m.filesKey(msg)
	}
	return m.terminalKey(msg)
}

// terminalKey handles a keypress while the terminal screen is focused. termExitKey
// (Ctrl+Q) closes the session and returns to the previous screen; on the dial-error
// or session-ended notice, Esc also returns. Every other key is encoded to terminal
// input bytes and written to the remote shell's stdin. The model is returned by value
// (the *termSession pointer rides along unchanged).
func (m model) terminalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == termExitKey {
		m = m.closeTerminal()
		return m, nil
	}
	// No live session to type into — the dial failed, or the remote shell already exited
	// (the box rebooted out from under us). termLive() is exactly those cases, so the two
	// notices share one branch. Esc is a convenient "back"; termReconnectKey redials
	// through openTerminal, which tears the dead session down and dials fresh (its
	// live-reuse branch can't trigger — the session is not live). Every other keystroke is
	// dropped: there is nothing to type into.
	if !m.termLive() {
		switch {
		case physKey(msg) == termReconnectKey:
			return m.openTerminal(m.termReturn, wsTerminal)
		case msg.String() == "esc":
			m = m.closeTerminal()
		}
		return m, nil
	}
	// Shift-modified PgUp/PgDn/Up/Down scroll the LOCAL scrollback instead of being
	// forwarded — but only on the normal screen (alt-screen freezes scrollback). PLAIN
	// PgUp/PgDn/arrows still encode + forward to the remote (handled below). The Shift
	// modifier is the distinguisher.
	if m.terminalScrollable() {
		if handled, mm := m.terminalScrollKey(msg.Key()); handled {
			return mm, nil
		}
	}
	if b := encodeKey(msg.Key()); len(b) > 0 {
		m.term.write(b)
		// Real input → snap back to the bottom so the user sees what they're typing and
		// the response to it (standard terminal follow-on-input behavior).
		m.termFollow = true
		m.termPinIfFollowing()
	}
	return m, nil
}

// terminalScrollKey handles the Shift-modified scrollback gestures (Shift+PgUp/PgDn,
// Shift+Up/Down). It returns handled=true (and the updated model) when the key was a
// scroll gesture that was consumed locally; handled=false means the key should fall
// through to be encoded + forwarded to the remote shell. Caller guarantees a live,
// non-alt-screen session.
func (m model) terminalScrollKey(k tea.Key) (bool, model) {
	if k.Mod&tea.ModShift == 0 {
		return false, m // not a Shift gesture → forward to the remote
	}
	_, rows := m.termContentSize()
	page := max(rows-1, 1) // page step leaves one row of context, like a pager
	switch k.Code {
	case tea.KeyPgUp:
		m.termScrollBy(-page)
		return true, m
	case tea.KeyPgDown:
		m.termScrollBy(page)
		return true, m
	case tea.KeyUp:
		m.termScrollBy(-1)
		return true, m
	case tea.KeyDown:
		m.termScrollBy(1)
		return true, m
	}
	return false, m // a Shift+<other> key still forwards to the remote
}

// termBody returns the current full terminal body (scrollback ++ screen) length-bearing
// lines, used to clamp the scroll offset. Thin wrapper so the scroll helpers and the
// view share one body source.
func (m model) termBodyLen() int { return len(m.terminalBody()) }

// termScrollBy adjusts the scrollback offset by delta (negative = toward older output),
// clamped to the body. Scrolling UP from the bottom drops follow mode (hold position);
// reaching the bottom re-arms follow mode so new output sticks again.
func (m *model) termScrollBy(delta int) {
	_, rows := m.termContentSize()
	total := m.termBodyLen()
	m.termScroll = clampScroll(m.termScroll+delta, total, rows)
	// Re-arm follow only when the offset is exactly at the bottom; any higher position
	// holds (the user is reading scrollback).
	m.termFollow = m.termScroll >= max(total-rows, 0)
}

// termPinIfFollowing re-pins the scroll offset to the bottom when in follow mode, so
// newly-arrived output stays visible. A no-op when the user has scrolled up. Called on
// each repaint tick and after a forwarded keystroke.
func (m *model) termPinIfFollowing() {
	if !m.termFollow {
		return
	}
	_, rows := m.termContentSize()
	total := m.termBodyLen()
	m.termScroll = max(total-rows, 0)
}

// closeTerminal tears down the terminal screen: it closes the live session (cancel +
// reap goroutines + close the input pipe via termSession.close — ends the *ssh.Session
// only) AND closes the underlying *sshx.Client (stopping its 30s keepalive goroutine +
// the TCP/SSH transport — which the session deliberately does NOT own). Both pointers
// are dropped so the emulator + transport are GC'd, then it returns to the screen the
// terminal was opened from. Nil-safe and idempotent (close once, guard nil). The render
// tick stops naturally: its gen no longer matches m.termGen after the next openTerminal,
// and with m.term==nil the tick handler is a no-op.
func (m model) closeTerminal() model {
	if m.term != nil {
		m.term.close()
		m.term = nil
	}
	// Close the Files session (its sftp client only — the shared transport is m.termClient,
	// closed above), then reset the workspace to the default Terminal tab so a later reopen
	// starts clean.
	if m.files != nil {
		m.files.close()
		m.files = nil
	}
	m.wsTab = wsTerminal
	if m.termClient != nil {
		m.termClient.Close()
		m.termClient = nil
	}
	m.termScroll = 0
	m.termFollow = true
	m.termGen++ // invalidate any in-flight tick from the just-closed session
	m.phase = m.termReturn
	return m
}

// termContentSize is the (cols, rows) of the terminal's inner content area — the box
// inner width and the middle region height. The row budget comes from the SHARED
// chrome arithmetic (bodyViewH → chromeViewH): the terminal frame spends the standard
// chrome and adds no fixed or pinned rows of its own, so the emulator can never
// overflow the frame and this can't drift from the other screens.
func (m model) termContentSize() (cols, rows int) {
	cols = max(innerWidth(m.boxWidth()), 1)
	return cols, m.bodyViewH()
}

// terminalView renders the terminal screen: the framed emulator body (or a dial
// error / "session ended" notice) under the standard titled top + switcher, with a
// control hint, bottom border, and the pinned monitor footer below. The body is
// windowed through renderScrollRegion (the same scroll-region pattern the dashboard/
// summary/wiki screens use), so scrollback scrolls and a proportional scrollbar shows
// in the right border when there is hidden content. The emulator's own ANSI styling is
// preserved inside the box.
func (m model) terminalView() string {
	s, _ := m.terminalFrame()
	return s
}

// terminalFrame is terminalView plus the frame's native cursor placement, produced
// TOGETHER from the SAME emulator snapshot — both for consistency (the cursor maps
// against exactly the rows drawn) and for cost: a second snapshot just for the cursor
// would double the per-frame emulator read at the 40fps repaint tick. A nil cursor
// means "no cursor this frame" (tea.View hides it).
func (m model) terminalFrame() (string, *tea.Cursor) {
	innerW := innerWidth(m.boxWidth())
	_, rows := m.termContentSize()

	// For a LIVE session take ONE snapshot and use it for BOTH the body assembly and the
	// cursor placement, so the cursor maps against exactly the rows that get drawn (no
	// TOCTOU vs the concurrent drain). The error/"session ended" notices are static (no
	// cursor), so they skip the snapshot via terminalBody().
	if m.termLive() {
		// snap.off is the clamped offset the snapshot rendered its scrollback window for
		// — use it, never a re-clamp, or the frame could window rows the snapshot left
		// unrendered.
		snap := m.term.cursorSnapshot(m.termScroll, rows)
		body := liveBodyFromSnapshot(snap)
		cur := m.terminalCursor(snap, len(body), innerW, rows, snap.off)
		return m.terminalFrameOf(body, rows, snap.off), cur
	}
	body := m.terminalBody()
	return m.terminalFrameOf(body, rows, clampScroll(m.termScroll, len(body), rows)), nil
}

// termLive reports whether the session is connected and running (not a dial error, not
// nil, not finished) — the only state that has a remote cursor to draw.
func (m model) termLive() bool {
	if m.termErr != "" || m.term == nil {
		return false
	}
	done, _ := m.term.finished()
	return !done
}

// terminalFrameOf draws the terminal chrome around an already-assembled body via the
// SHARED frame builder — the terminal is the 8th screen on framedScrollView: its own
// titled top (named after the tab, not frameTitle), the global nav tab strip, no fixed
// or pinned rows, the windowed scroll region and the control hint. Shared by the live
// and notice paths so the chrome is identical.
func (m model) terminalFrameOf(body []string, rows, off int) string {
	return m.framedScrollView(frame{
		title:  " " + version.Name + " · " + t(m.lang, kTermTitle) + " ",
		nav:    m.navTabStripLine,
		body:   body,
		viewH:  rows,
		scroll: off,
		hint:   helpStyle.Render(t(m.lang, kTermHint)),
	})
}

// terminalScrollable reports whether scrollback scrolling is active: a live (not
// dial-errored, not nil, not finished) session that is NOT on the alternate screen.
// Alt-screen apps (vim/top/less) own the screen and freeze scrollback, so wheel/Shift-
// PgUp gestures are ignored while alt is on.
func (m model) terminalScrollable() bool {
	if m.termErr != "" || m.term == nil {
		return false
	}
	if done, _ := m.term.finished(); done {
		return false
	}
	return !m.term.altScreen()
}

// terminalBody assembles the FULL (un-windowed) terminal body lines for the scroll
// region to window: the dial-error notice, a "session ended" banner, or the live
// content. Live content is the scrollback buffer followed by the live screen when NOT
// on the alternate screen; on the alternate screen it is just the screen (scrollback is
// frozen/irrelevant there). Returns the lines WITHOUT padding — renderScrollRegion pads
// to the view height and pins the footer.
func (m model) terminalBody() []string {
	switch {
	case m.termErr != "":
		return []string{
			errStyle.Render(m.termErr),
			"",
			helpStyle.Render(t(m.lang, kTermBackHint)),
		}
	case m.term == nil:
		return []string{helpStyle.Render("…")}
	}
	if done, err := m.term.finished(); done {
		banner := t(m.lang, kTermEnded)
		if err != nil {
			banner += " — " + err.Error()
		}
		return []string{
			tipStyle.Render(banner),
			"",
			helpStyle.Render(t(m.lang, kTermBackHint)),
		}
	}
	// Live content: build from a single consistent snapshot so the body and any cursor
	// overlay share one source of truth. Only the rows visible at the CURRENT scroll
	// offset are rendered (see cursorSnapshot) — the body is full-length either way, so
	// every length/offset caller (termBodyLen, the scroll clamp) is unaffected.
	_, rows := m.termContentSize()
	return liveBodyFromSnapshot(m.term.cursorSnapshot(m.termScroll, rows))
}

// liveBodyFromSnapshot assembles the live terminal body from a consistent snapshot: on
// the alternate screen the app owns the whole screen (scrollback is frozen/irrelevant)
// so the body is screen-only; on the normal screen it is scrollback (oldest→newest)
// followed by the live screen. Pure (no locked reads) — the caller passes ONE snapshot
// so the cursor overlay can map against exactly these rows.
func liveBodyFromSnapshot(snap termSnapshot) []string {
	if snap.alt {
		return snap.screen
	}
	body := make([]string, 0, len(snap.scrollback)+len(snap.screen))
	body = append(body, snap.scrollback...)
	body = append(body, snap.screen...)
	return body
}

// --- Cursor -------------------------------------------------------------------
//
// The remote shell's cursor is the HOST TERMINAL's own hardware cursor, placed via
// tea.View.Cursor at the screen cell the emulator cursor maps to. The host therefore
// owns the blink (including stopping it while its window is unfocused) and the shape,
// so there is no local blink state and nothing is spliced into the rendered text.

// termCursorTopRow / termCursorLeftCol are the screen coordinates of body cell (0,0)
// inside the terminal frame. They mirror renderTerminalFrame + contentLineR exactly:
// vertically the titled top border (1) + the nav tab strip (1) precede the scroll
// region; horizontally the left border (1) + its one-cell pad (1) precede the content.
const (
	termCursorTopRow  = 2
	termCursorLeftCol = 2
)

// terminalCursor maps the emulator cursor onto a screen cell of the rendered frame,
// or nil when no cursor should be shown this frame. bodyLen/rows/off describe the
// scroll region the caller is about to draw, innerW its content width; snap is the
// SAME snapshot the body was built from, so the row can't drift under the drain.
//
// Placement is by COORDINATE, independent of the row's rendered text. That is what the
// spliced cursor could not do: ultraviolet's Line.Render trims trailing blank cells, so
// a cursor parked to the RIGHT of a row's last non-blank cell (every app that positions
// with CUP/CHA on a mostly-empty line — vim, top, less) sat past the rendered string and
// the splice appended its block at the end of the CONTENT instead. Measured on an 80x24
// session (innerW=76): cursor at column 40 of a row rendering as "$" drew the block at
// visual column 1.
func (m model) terminalCursor(snap termSnapshot, bodyLen, innerW, rows, off int) *tea.Cursor {
	if !m.terminalCursorActive() || !snap.cursorVisible {
		return nil
	}
	scrollbackLen := 0
	if !snap.alt {
		scrollbackLen = snap.scrollbackLen
	}
	row := cursorBodyRow(scrollbackLen, snap.cursorY, snap.alt)
	if row < 0 || row >= bodyLen {
		return nil
	}
	// Windowed out of the visible scroll region (scrolled past it) → no cursor.
	y := row - off
	if y < 0 || y >= rows {
		return nil
	}
	// Past the visible content width → the cell is not drawn, so neither is the cursor.
	if snap.cursorX < 0 || snap.cursorX >= innerW {
		return nil
	}
	return tea.NewCursor(termCursorLeftCol+snap.cursorX, termCursorTopRow+y)
}

// cursorBodyRow maps the emulator cursor row `y` to an index into the assembled body
// slice: on the NORMAL screen the body is scrollback ++ screen, so the live screen row
// y sits at scrollbackLen+y; on the ALT screen the body is screen-only, so it is just y.
func cursorBodyRow(scrollbackLen, y int, alt bool) int {
	if alt {
		return y
	}
	return scrollbackLen + y
}

// terminalCursorActive reports whether a cursor should be shown this frame: a live
// (not errored / nil / finished) session, the remote wants a visible cursor (?25 on),
// AND — on the normal screen — we are pinned to the live bottom (termFollow). The
// cursor belongs to the live prompt, so it is suppressed while the user reads
// scrollback. On the alt screen follow does not apply (the app owns the screen), so
// only visibility gates it.
func (m model) terminalCursorActive() bool {
	if m.termErr != "" || m.term == nil {
		return false
	}
	if done, _ := m.term.finished(); done {
		return false
	}
	if !m.term.cursorShown() {
		return false
	}
	if m.term.altScreen() {
		return true
	}
	return m.termFollow
}
