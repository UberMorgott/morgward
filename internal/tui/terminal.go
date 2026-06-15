package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/charmbracelet/x/ansi"

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

// termTickInterval drives the repaint while the terminal is open (~40fps). Each tick
// re-renders only when the emulator reports damage (see termSession.dirty), so an
// idle shell does not busy-repaint.
const termTickInterval = 25 * time.Millisecond

// termBlinkPeriod is the cursor blink half-cycle (~530ms, the classic VT/xterm rate):
// the cursor is solid for one period, hidden for the next. Driven by the render tick
// (termBlinkPeriod / termTickInterval ≈ 21 ticks per flip).
const termBlinkPeriod = 530 * time.Millisecond

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
	m.termReturn = from
	m.termErr = ""
	m.termGen++
	m.phase = phaseTerminal
	// Start pinned to the newest output (follow mode); the offset is recomputed to the
	// bottom on the first render once the body length is known.
	m.termScroll = 0
	m.termFollow = true
	// Cursor starts solid; the blink cycle advances on the render tick.
	m.termBlinkOn = true
	m.termBlinkTicks = 0

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

// wsSwitchTerminalKey / wsSwitchFilesKey select the workspace tab from EITHER tab.
// ctrl+1/ctrl+2 are reserved for tab switching (a shell rarely needs them; the FM never
// forwards keys), so they are intercepted before any per-tab routing.
const (
	wsSwitchTerminalKey = "ctrl+1"
	wsSwitchFilesKey    = "ctrl+2"
)

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
		m.files = newFileSession(m.termClient, "")
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
	// termExitKey (ctrl+q) closes the WHOLE workspace from EITHER tab — handled before any
	// per-tab routing so the Files tab can't trap the user (the filesKey stub would
	// otherwise swallow it). Esc is deliberately NOT a universal exit: the shell/vim needs
	// it on the Terminal tab, and the FM reserves it on the Files tab.
	if msg.String() == termExitKey {
		m = m.closeTerminal()
		return m, nil
	}
	// While an FM prompt/confirm is open, the tab-switch keys (ctrl+1/ctrl+2/bare-Tab) must
	// NOT silently flip tabs and abandon the half-entered prompt — route them into filesKey
	// (which ignores them, leaving the prompt intact) instead. ctrl+q above still exits.
	fmPrompting := m.wsTab == wsFiles && m.files != nil && m.files.prompting()
	if !fmPrompting {
		switch msg.String() {
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
		if msg.String() == "tab" && !fmPrompting {
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
	// When there is no live session to type into (dial failed, or the remote shell
	// already exited), Esc is a convenient "back" — otherwise keystrokes are dropped.
	if m.termErr != "" || m.term == nil {
		if msg.String() == "esc" {
			m = m.closeTerminal()
		}
		return m, nil
	}
	if done, _ := m.term.finished(); done {
		if msg.String() == "esc" {
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
		// Snap the cursor SOLID immediately on a keystroke (standard terminal feel — the
		// cursor doesn't blink-out mid-typing), restarting the blink cycle.
		m.termBlinkOn = true
		m.termBlinkTicks = 0
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
	page := maxi(rows-1, 1) // page step leaves one row of context, like a pager
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
	m.termFollow = m.termScroll >= maxi(total-rows, 0)
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
	m.termScroll = maxi(total-rows, 0)
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
// inner width and the middle region height, mirroring the other screens' chrome math
// so the emulator never overflows the frame. Floored at a usable minimum.
func (m model) termContentSize() (cols, rows int) {
	cols = maxi(innerWidth(m.boxWidth()), 1)
	// Chrome rows: top border + switcher (2) + hint + bottom border (2) = 4, plus the
	// pinned 3-row monitor box at the very bottom (titledTop + metrics + bottom) = 7.
	rows = maxi(m.h-7, 1)
	return cols, rows
}

// terminalView renders the terminal screen: the framed emulator body (or a dial
// error / "session ended" notice) under the standard titled top + switcher, with a
// control hint, bottom border, and the pinned monitor footer below. The body is
// windowed through renderScrollRegion (the same scroll-region pattern the dashboard/
// summary/wiki screens use), so scrollback scrolls and a proportional scrollbar shows
// in the right border when there is hidden content. The emulator's own ANSI styling is
// preserved inside the box.
func (m model) terminalView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()
	_, rows := m.termContentSize()

	// For a LIVE session take ONE snapshot and use it for BOTH the body assembly and the
	// cursor overlay, so the overlay maps the cursor against exactly the rows it splices
	// (no TOCTOU vs the concurrent drain). The error/"session ended" notices are static
	// (no cursor), so they skip the snapshot via terminalBody().
	var body []string
	if m.termLive() {
		snap := m.term.cursorSnapshot()
		body = liveBodyFromSnapshot(snap)
		off := clampScroll(m.termScroll, len(body), rows)
		// Overlay the blinking reverse-video cursor block (row count is unchanged, so `off`
		// computed above still holds). No-op when inactive/hidden/scrolled-off.
		body = m.applyCursorOverlay(body, snap)
		return m.renderTerminalFrame(bw, innerW, b, body, rows, off)
	}
	body = m.terminalBody()
	off := clampScroll(m.termScroll, len(body), rows)
	return m.renderTerminalFrame(bw, innerW, b, body, rows, off)
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

// renderTerminalFrame draws the framed terminal chrome (titled top, switcher, the
// windowed scroll region, hint, bottom border, monitor footer) around an already-
// assembled (and possibly cursor-overlaid) body. Shared by the live and notice paths so
// the chrome is identical.
func (m model) renderTerminalFrame(bw, innerW int, b lipgloss.Border, body []string, rows, off int) string {

	var sb strings.Builder
	title := " " + version.Name + " · " + t(m.lang, kTermTitle) + " "
	sb.WriteString(titledTop(b, title, bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// Window the body to exactly `rows` rows (blank-padded), drawing a scrollbar in the
	// right border when the body overflows — keeps the footer pinned.
	m.renderScrollRegion(&sb, b, body, innerW, rows, off)

	hint := helpStyle.Render(t(m.lang, kTermHint))
	sb.WriteString(contentLine(b, hint, innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	// Pinned monitor footer (sampler kept alive from the dashboard) — mirrors summaryView
	// so the live server metrics stay visible while on the terminal screen.
	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
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
	// overlay share one source of truth.
	return liveBodyFromSnapshot(m.term.cursorSnapshot())
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

// --- Cursor overlay -----------------------------------------------------------
//
// A blinking reverse-video BLOCK cursor drawn at the remote shell's cursor position.
// The overlay is spliced into the body slice (before renderScrollRegion windows it) so
// the cursor scrolls with the content and respects the scrollbar geometry for free.

// cursorBodyRow maps the emulator cursor row `y` to an index into the assembled body
// slice: on the NORMAL screen the body is scrollback ++ screen, so the live screen row
// y sits at scrollbackLen+y; on the ALT screen the body is screen-only, so it is just y.
func cursorBodyRow(scrollbackLen, y int, alt bool) int {
	if alt {
		return y
	}
	return scrollbackLen + y
}

// spliceCursorBlock overlays a reverse-video block at VISUAL column `col` of an
// ANSI-styled row, reversing the grapheme `cell` whose display width is `cellWidth`
// (a single space when empty). It is ANSI-aware: ansi.Cut slices by display column (not
// byte/rune offset), so the splice lands correctly even when the row contains SGR
// escapes. CRITICAL: the right segment is cut at col+cellWidth, NOT col+1 — a double-
// width glyph (CJK/emoji) occupies two cells, and cutting at col+1 would land MID-glyph,
// where ansi.Cut rounds outward and re-emits the whole glyph (duplicating it and growing
// the row's width). cellWidth<1 clamps to 1 (a blank/zero-width cell shows a 1-cell
// space block). A col at/past the row width clamps — the block is appended after the
// content (cursor at end-of-line). The result has the same visual width as the input
// (reverse video is zero-width chrome) and never duplicates the cursor glyph.
func spliceCursorBlock(row string, col int, cell string, cellWidth int, focused bool) string {
	if cell == "" {
		cell = " "
	}
	if cellWidth < 1 {
		cellWidth = 1
	}
	// Focused → a solid REVERSE-video block (the active cursor). Unfocused → an UNDERLINE
	// span, which reads as a hollow/outline "the window isn't focused" cursor in virtually
	// every terminal and is steady (the caller draws it every frame when unfocused). Chosen
	// over a true box-drawing hollow cell because underline composes cleanly with the cell's
	// existing SGR via one open/close pair — no width change, no glyph substitution.
	on, off := "\x1b[7m", "\x1b[27m"
	if !focused {
		on, off = "\x1b[4m", "\x1b[24m"
	}
	w := ansi.StringWidth(row)
	if col >= w {
		// At/over the end of the printable content → append the block (end-of-line cursor).
		// NOTE: if the row is already innerW-wide, the appended cell makes it innerW+1 and
		// truncDisplay later drops the last cell, so a last-column cursor can visually
		// vanish. Cosmetic, width-safe (no corruption) — accepted as a known minor.
		return row + on + cell + off
	}
	// left = columns [0,col); right = columns [col+cellWidth, end) so the block spans the
	// FULL (possibly 2-wide) cursor cell. ansi.Cut is display-aware and preserves the SGR
	// state of each segment, so styling around the cursor survives.
	left := ansi.Cut(row, 0, col)
	right := ansi.Cut(row, col+cellWidth, w)
	return left + on + cell + off + right
}

// terminalCursorActive reports whether the cursor overlay should be drawn this frame:
// a live (not errored / nil / finished) session, the remote wants a visible cursor
// (?25 on), AND — on the normal screen — we are pinned to the live bottom (termFollow).
// The cursor belongs to the live prompt, so it is suppressed while the user reads
// scrollback. On the alt screen follow does not apply (the app owns the screen), so only
// visibility gates it.
//
// BLINK is focus-aware: when the host window is FOCUSED the cursor is gated on the blink
// half (drawn only while termBlinkOn) so it blinks; when UNFOCUSED the blink gate is
// skipped so the cursor is STEADY (real terminals stop blinking when unfocused — the
// drawn-vs-hidden choice is the only blink mechanism, so unfocused = always drawn).
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
	if m.focused && !m.termBlinkOn {
		return false // focused: hide on the blink-off half (so it blinks)
	}
	if m.term.altScreen() {
		return true
	}
	return m.termFollow
}

// applyCursorOverlay returns body with the reverse-video cursor block spliced into the
// row under the emulator cursor, mapping the cursor against the SAME snapshot used to
// build body (so the row index can't drift from a concurrent drain — the TOCTOU the
// separate-reads version had). A no-op (returns body unchanged) when the model gating
// (terminalCursorActive) forbids it, the snapshot's cursor is hidden, or the cursor maps
// outside body. body is copied before mutation so the un-overlaid slice (scroll math) is
// untouched.
func (m model) applyCursorOverlay(body []string, snap termSnapshot) []string {
	if !m.terminalCursorActive() || !snap.cursorVisible {
		return body
	}
	scrollbackLen := 0
	if !snap.alt {
		scrollbackLen = snap.scrollbackLen
	}
	row := cursorBodyRow(scrollbackLen, snap.cursorY, snap.alt)
	if row < 0 || row >= len(body) {
		return body
	}
	out := make([]string, len(body))
	copy(out, body)
	out[row] = spliceCursorBlock(out[row], snap.cursorX, snap.cursorCell, snap.cursorWidth, m.focused)
	return out
}
