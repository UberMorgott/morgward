package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// termCursorOf renders the terminal frame and returns its native cursor (nil = no
// cursor this frame) — the exact value View() puts on tea.View.Cursor.
func termCursorOf(m model) *tea.Cursor {
	_, cur := m.terminalFrame()
	return cur
}

// termSnap takes the snapshot for m's CURRENT scroll geometry — exactly the window the
// production frame renders — so a test can never read a scrollback row the frame itself
// would leave unrendered.
func termSnap(m model) termSnapshot {
	_, rows := m.termContentSize()
	return m.term.cursorSnapshot(m.termScroll, rows)
}

// TestCursorSnapshotConsistent proves the session snapshot returns an internally-
// consistent view: the scrollback length it reports matches the scrollback lines it
// returns, and the cursor Y is within the screen — all from ONE locked read, so the
// cursor maps against exactly the body it is placed over (no TOCTOU across separate
// reads).
func TestCursorSnapshotConsistent(t *testing.T) {
	m, _ := termModel(t, 40, 16)
	defer m.term.close()
	seedLines(t, m, 40, 5) // grow scrollback
	// A prompt on the current line so the cursor sits on a CONTENT row (a real shell
	// always shows a prompt where the cursor is) — not a trailing blank that Render
	// trims away.
	m.term.write([]byte("$ "))
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "prompt to render")

	snap := termSnap(m)
	// Core consistency invariant: the reported length matches the lines themselves, from
	// the SAME locked read (so the cursor's body math can't drift from the body).
	if snap.scrollbackLen != len(snap.scrollback) {
		t.Fatalf("snapshot inconsistent: scrollbackLen=%d but len(scrollback)=%d",
			snap.scrollbackLen, len(snap.scrollback))
	}
	if snap.scrollbackLen == 0 {
		t.Fatal("expected non-empty scrollback after seeding")
	}
	// With a prompt under it the cursor maps onto a real screen row.
	if snap.cursorY < 0 || snap.cursorY >= len(snap.screen) {
		t.Fatalf("snapshot cursorY=%d out of screen range [0,%d)", snap.cursorY, len(snap.screen))
	}
}

// TestCursorBodyRowMapping proves the emulator cursor (col x, row y) maps to the right
// BODY row index: normal screen = len(scrollback)+y, alt-screen = y.
func TestCursorBodyRowMapping(t *testing.T) {
	if got := cursorBodyRow(7, 3, false); got != 10 {
		t.Fatalf("normal-screen body row = %d, want scrollback(7)+y(3)=10", got)
	}
	if got := cursorBodyRow(7, 3, true); got != 3 {
		t.Fatalf("alt-screen body row = %d, want y=3", got)
	}
}

// TestTerminalCursorAtPrompt proves the live terminal frame places the native cursor at
// the screen cell the emulator cursor maps to: X = left chrome + cursorX, Y = top chrome
// + the cursor's row within the visible scroll region.
func TestTerminalCursorAtPrompt(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()

	m.term.write([]byte("$ "))
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "prompt to render")

	m.termFollow = true
	m.termPinIfFollowing()

	cur := termCursorOf(m)
	if cur == nil {
		t.Fatal("expected a native cursor at the prompt")
	}
	snap := termSnap(m)
	if want := termCursorLeftCol + snap.cursorX; cur.X != want {
		t.Fatalf("cursor X = %d, want %d (left chrome %d + cursorX %d)", cur.X, want, termCursorLeftCol, snap.cursorX)
	}
	// The prompt sits on the last body row, pinned to the bottom of the scroll region.
	_, rows := m.termContentSize()
	body := liveBodyFromSnapshot(snap)
	off := clampScroll(m.termScroll, len(body), rows)
	want := termCursorTopRow + cursorBodyRow(snap.scrollbackLen, snap.cursorY, snap.alt) - off
	if cur.Y != want {
		t.Fatalf("cursor Y = %d, want %d", cur.Y, want)
	}
}

// TestTerminalCursorPastTrimmedContent is the regression the native cursor actually
// fixes. ultraviolet's Line.Render trims trailing blank cells, so a cursor positioned to
// the RIGHT of a row's last non-blank cell (CUP/CHA on a mostly-empty line — vim, top,
// less) sat past the rendered string; the old splice then appended its block at the end
// of the CONTENT, i.e. many columns left of the true cursor. Placing by coordinate must
// land on the column the emulator reports, whatever the row renders as.
func TestTerminalCursorPastTrimmedContent(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()

	// "$", then CHA to column 41 (1-based) → cursorX 40, far right of the trimmed row.
	m.term.write([]byte("$\x1b[41G"))
	waitFor(t, time.Second, func() bool {
		return termSnap(m).cursorX == 40
	}, "cursor to park past the content")

	m.termFollow = true
	m.termPinIfFollowing()

	snap := termSnap(m)
	if w := lipgloss.Width(snap.screen[snap.cursorY]); w >= snap.cursorX {
		t.Fatalf("precondition: row renders %d wide, need it TRIMMED shorter than cursorX=%d", w, snap.cursorX)
	}

	cur := termCursorOf(m)
	if cur == nil {
		t.Fatal("no cursor when it sits past the row's trimmed content")
	}
	if want := termCursorLeftCol + snap.cursorX; cur.X != want {
		t.Fatalf("cursor X = %d, want %d — placement must follow the emulator column, not the rendered text", cur.X, want)
	}
}

// TestTerminalCursorLastColumn proves the LAST visible column is a valid cursor cell and
// anything beyond innerW (only reachable in the frame between a window resize and the
// emulator being resized to match) draws nothing rather than a lie.
func TestTerminalCursorLastColumn(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()
	m.term.write([]byte("$ "))
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "prompt to render")

	m.termFollow = true
	m.termPinIfFollowing()

	innerW := innerWidth(m.boxWidth())
	_, rows := m.termContentSize()
	snap := termSnap(m)
	body := liveBodyFromSnapshot(snap)
	off := clampScroll(m.termScroll, len(body), rows)

	snap.cursorX = innerW - 1
	cur := m.terminalCursor(snap, len(body), innerW, rows, off)
	if cur == nil {
		t.Fatal("no cursor in the LAST visible column")
	}
	if want := termCursorLeftCol + innerW - 1; cur.X != want {
		t.Fatalf("last-column cursor X = %d, want %d", cur.X, want)
	}

	snap.cursorX = innerW
	if c := m.terminalCursor(snap, len(body), innerW, rows, off); c != nil {
		t.Fatalf("cursor placed past the content width: %+v", c)
	}
}

// TestTerminalCursorHiddenWhenScrolledUp proves no cursor while the user has scrolled up
// into scrollback (termFollow=false): the cursor belongs to the live bottom, not to
// history being read.
func TestTerminalCursorHiddenWhenScrolledUp(t *testing.T) {
	m, _ := termModel(t, 40, 16)
	defer m.term.close()
	seedLines(t, m, 40, 5)

	m.termFollow = false
	m.termScroll = 0 // scrolled to the very top

	if cur := termCursorOf(m); cur != nil {
		t.Fatalf("cursor shown while scrolled up: %+v — it must only show at the live bottom", cur)
	}
}

// TestTerminalCursorHiddenWhenAppHidesIt proves a remote app hiding the cursor (DECTCEM
// \x1b[?25l, delivered through the live drain → CursorVisibility callback) suppresses the
// cursor even when pinned (vim/password prompts hide the cursor).
func TestTerminalCursorHiddenWhenAppHidesIt(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()
	m.term.write([]byte("$ "))
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "prompt to render")

	// Cursor on by default.
	if !m.term.cursorShown() {
		t.Fatal("cursor should be visible by default (?25 on)")
	}
	// App hides the cursor.
	m.term.write([]byte("\x1b[?25l"))
	waitFor(t, time.Second, func() bool { return !m.term.cursorShown() }, "cursor-hide callback to fire")

	m.termFollow = true
	m.termPinIfFollowing()

	if cur := termCursorOf(m); cur != nil {
		t.Fatalf("cursor shown while the app hid it (?25l): %+v — must respect CursorVisibility", cur)
	}
}

// TestTerminalCursorHiddenWhenFinished proves no cursor on a finished session (the body
// is a "session ended" banner, not live content).
func TestTerminalCursorHiddenWhenFinished(t *testing.T) {
	m, fs := termModel(t, 80, 24)
	m.term.close()
	<-fs.returned
	waitFor(t, time.Second, func() bool { d, _ := m.term.finished(); return d }, "session to finish")

	m.termFollow = true
	if cur := termCursorOf(m); cur != nil {
		t.Fatalf("cursor shown on a finished session: %+v — must be suppressed", cur)
	}
}

// TestViewCursorOnlyOnTerminal proves View() exposes the native cursor ONLY on the
// terminal tab of the terminal workspace: every other screen (and the Files tab, which
// has no shell cursor) returns a nil Cursor, which hides it.
func TestViewCursorOnlyOnTerminal(t *testing.T) {
	m := newModel()
	m.w, m.h = 80, 24
	if v := m.View(); v.Cursor != nil {
		t.Fatalf("form phase exposed a cursor: %+v", v.Cursor)
	}
	// phaseTerminal with no session at all: still no cursor (nothing live to point at).
	m.phase = phaseTerminal
	if v := m.View(); v.Cursor != nil {
		t.Fatalf("terminal phase with no session exposed a cursor: %+v", v.Cursor)
	}
}

// TestViewCursorMatchesFrame proves View() hands tea.View exactly the cursor the
// terminal frame computed — the placement is not recomputed from a second snapshot.
func TestViewCursorMatchesFrame(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()
	m.term.write([]byte("$ "))
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "prompt to render")

	m.phase = phaseTerminal
	m.wsTab = wsTerminal
	m.termFollow = true
	m.termPinIfFollowing()

	v := m.View()
	if v.Cursor == nil {
		t.Fatal("View() dropped the terminal cursor")
	}
	want := termCursorOf(m)
	if want == nil || *v.Cursor != *want {
		t.Fatalf("View().Cursor = %+v, want %+v", v.Cursor, want)
	}
}
