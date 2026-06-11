package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
// from is the phase to return to on exit.
func (m model) openTerminal(from phase) (tea.Model, tea.Cmd) {
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
	return m, termTick(m.termGen)
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

	body := m.terminalBody()
	off := clampScroll(m.termScroll, len(body), rows)

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
	// Alt-screen: the app owns the whole screen; scrollback does not apply.
	if m.term.altScreen() {
		return m.term.screenLines()
	}
	// Normal screen: scrollback (oldest→newest) then the live screen below it.
	sbLines := m.term.scrollbackLines()
	screen := m.term.screenLines()
	body := make([]string, 0, len(sbLines)+len(screen))
	body = append(body, sbLines...)
	body = append(body, screen...)
	return body
}
