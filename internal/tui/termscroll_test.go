package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// seedLines writes n newline-terminated numbered lines to the session (echoed by the
// fake shell into the emulator), then waits until the emulator has pushed at least
// `wantScrollback` lines into its scrollback buffer. Used to drive scrollback tests.
func seedLines(t *testing.T, m model, n, wantScrollback int) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < n; i++ {
		// \r\n so each line starts at column 0 (the pty would do this); plain "\n" only
		// moves down a row and the vt would not reset the column.
		fmt.Fprintf(&b, "line%02d\r\n", i)
	}
	m.term.write([]byte(b.String()))
	waitFor(t, 2*time.Second, func() bool {
		// Read scrollback length through cursorSnapshot() so it holds out.mu — the same
		// lock the fake-shell drain (termOut.Write) takes when appending to scrollback.
		// Reading m.term.emu.Scrollback().Len() directly races that drain goroutine.
		return m.term.cursorSnapshot().scrollbackLen >= wantScrollback
	}, "emulator to accumulate scrollback")
}

// TestTerminalBodyScrollbackPlusScreen proves the body assembled on the NORMAL screen
// is scrollback (oldest→newest) followed by the live screen rows, in that order.
func TestTerminalBodyScrollbackPlusScreen(t *testing.T) {
	// Small content area so a handful of lines overflow into scrollback.
	m, _ := termModel(t, 40, 12) // termContentSize → rows = h-7 = 5
	defer m.term.close()
	_, rows := m.termContentSize()

	seedLines(t, m, 20, 1) // 20 lines into a `rows`-tall screen → many scroll off

	sbLines := m.term.scrollbackLines()
	screen := m.term.screenLines()
	body := m.terminalBody()

	if len(body) != len(sbLines)+len(screen) {
		t.Fatalf("body len = %d, want scrollback(%d)+screen(%d)=%d",
			len(body), len(sbLines), len(screen), len(sbLines)+len(screen))
	}
	// Order: scrollback first.
	for i := range sbLines {
		if body[i] != sbLines[i] {
			t.Fatalf("body[%d] != scrollback[%d] — scrollback must come first", i, i)
		}
	}
	// Then the live screen.
	for i := range screen {
		if body[len(sbLines)+i] != screen[i] {
			t.Fatalf("body screen row %d misordered", i)
		}
	}
	if len(sbLines) == 0 {
		t.Fatal("expected non-empty scrollback after seeding 20 lines into a small screen")
	}
	_ = rows
}

// TestTerminalBodyAltScreenIsScreenOnly proves that on the alternate screen the body is
// JUST the screen (no scrollback), and scrolling is disabled.
func TestTerminalBodyAltScreenIsScreenOnly(t *testing.T) {
	m, _ := termModel(t, 40, 12)
	defer m.term.close()

	seedLines(t, m, 20, 1) // build some scrollback first

	// Enter the alternate screen (DECSET 1049). The fake echoes it into the emulator,
	// which fires the AltScreen callback.
	m.term.write([]byte("\x1b[?1049h"))
	waitFor(t, 2*time.Second, func() bool { return m.term.altScreen() }, "emulator to enter alt-screen")

	if m.terminalScrollable() {
		t.Fatal("terminalScrollable must be false on the alternate screen")
	}
	body := m.terminalBody()
	screen := m.term.screenLines()
	if len(body) != len(screen) {
		t.Fatalf("alt-screen body len = %d, want screen-only = %d", len(body), len(screen))
	}
}

// TestTerminalWheelScroll proves wheel-up moves the offset toward older output (and
// clamps at 0) while wheel-down returns to the bottom, via the MouseWheelMsg path.
func TestTerminalWheelScroll(t *testing.T) {
	m, _ := termModel(t, 40, 16) // rows = 9
	defer m.term.close()
	seedLines(t, m, 40, 5)

	_, rows := m.termContentSize()
	total := m.termBodyLen()
	bottom := total - rows
	if bottom <= 0 {
		t.Fatalf("test needs an overflowing body: total=%d rows=%d", total, rows)
	}

	// Pin to bottom (follow), then wheel up.
	m.termFollow = true
	m.termPinIfFollowing()
	if m.termScroll != bottom {
		t.Fatalf("follow pin: termScroll=%d, want bottom=%d", m.termScroll, bottom)
	}

	next, _ := m.Update(wheelMsg(tea.MouseWheelUp))
	mm := next.(model)
	if mm.termScroll >= bottom {
		t.Fatalf("wheel up did not move offset toward older output: %d (bottom=%d)", mm.termScroll, bottom)
	}
	if mm.termFollow {
		t.Fatal("scrolling up must drop follow mode")
	}

	// Wheel up repeatedly clamps at 0 (never negative).
	for i := 0; i < 50; i++ {
		n, _ := mm.Update(wheelMsg(tea.MouseWheelUp))
		mm = n.(model)
	}
	if mm.termScroll != 0 {
		t.Fatalf("wheel up should clamp at 0, got %d", mm.termScroll)
	}

	// Wheel down repeatedly returns to the bottom and re-arms follow.
	for i := 0; i < 100; i++ {
		n, _ := mm.Update(wheelMsg(tea.MouseWheelDown))
		mm = n.(model)
	}
	if mm.termScroll != bottom {
		t.Fatalf("wheel down should clamp at bottom=%d, got %d", bottom, mm.termScroll)
	}
	if !mm.termFollow {
		t.Fatal("reaching the bottom should re-arm follow mode")
	}
}

// wheelMsg builds a MouseWheelMsg carrying the given wheel button — mirrors how the
// update handler reads msg.Mouse().Button.
func wheelMsg(btn tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{Button: btn}
}

// TestTerminalShiftPgUpScrolls proves Shift+PgUp scrolls local scrollback (not
// forwarded), while PLAIN PgUp is forwarded to the remote (encodes to a CSI seq).
func TestTerminalShiftPgUpScrolls(t *testing.T) {
	m, _ := termModel(t, 40, 16)
	defer m.term.close()
	seedLines(t, m, 40, 5)

	m.termFollow = true
	m.termPinIfFollowing()
	_, rows := m.termContentSize()
	bottom := m.termBodyLen() - rows

	// Shift+PgUp → local scroll up, consumed (not forwarded).
	next, _ := m.terminalKey(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift})
	mm := next.(model)
	if mm.termScroll >= bottom {
		t.Fatalf("shift+pgup did not scroll up: termScroll=%d bottom=%d", mm.termScroll, bottom)
	}
	if mm.termFollow {
		t.Fatal("shift+pgup (scroll up) must drop follow mode")
	}
}

// TestTerminalPlainPgUpForwardsAndFollows proves a PLAIN (no-shift) PgUp is forwarded
// to the remote shell AND re-arms follow mode + snaps to the bottom (input behavior),
// i.e. it is NOT treated as a local scroll.
func TestTerminalPlainPgUpForwardsAndFollows(t *testing.T) {
	m, _ := termModel(t, 40, 16)
	defer m.term.close()
	seedLines(t, m, 40, 5)

	// Scroll up first (so we can observe the snap-back on input).
	m.termFollow = false
	m.termScroll = 0

	next, _ := m.terminalKey(tea.KeyPressMsg{Code: tea.KeyPgUp})
	mm := next.(model)
	if !mm.termFollow {
		t.Fatal("a forwarded keystroke (plain pgup) must re-arm follow mode")
	}
	_, rows := mm.termContentSize()
	wantBottom := maxi(mm.termBodyLen()-rows, 0)
	if mm.termScroll != wantBottom {
		t.Fatalf("forwarded key should snap to bottom=%d, got %d", wantBottom, mm.termScroll)
	}
}

// TestTerminalScrollKeyIgnoredOnAltScreen proves Shift+PgUp does NOT scroll on the
// alternate screen (it falls through to be forwarded instead).
func TestTerminalScrollKeyIgnoredOnAltScreen(t *testing.T) {
	m, _ := termModel(t, 40, 16)
	defer m.term.close()
	seedLines(t, m, 40, 5)

	m.term.write([]byte("\x1b[?1049h"))
	waitFor(t, 2*time.Second, func() bool { return m.term.altScreen() }, "alt-screen")

	before := m.termScroll
	next, _ := m.terminalKey(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift})
	mm := next.(model)
	// On alt-screen the gesture is not consumed as a local scroll; the offset is
	// unchanged by scroll logic (the key was forwarded instead).
	if mm.termScroll != before {
		t.Fatalf("scroll key should be inert on alt-screen: termScroll %d → %d", before, mm.termScroll)
	}
}

// TestTerminalFollowPinOnTick proves the repaint tick re-pins to the bottom while in
// follow mode as new output arrives, but holds position when the user has scrolled up.
func TestTerminalFollowPinOnTick(t *testing.T) {
	m, _ := termModel(t, 40, 16)
	seedLines(t, m, 20, 3)

	_, rows := m.termContentSize()

	// Follow mode: the tick pins to the bottom.
	m.termFollow = true
	next, _ := m.Update(termTickMsg{gen: m.termGen})
	mm := next.(model)
	wantBottom := maxi(mm.termBodyLen()-rows, 0)
	if mm.termScroll != wantBottom {
		t.Fatalf("follow tick: termScroll=%d, want bottom=%d", mm.termScroll, wantBottom)
	}

	// Scrolled up (not following): the tick must NOT move the offset.
	mm.termFollow = false
	mm.termScroll = 0
	n2, _ := mm.Update(termTickMsg{gen: mm.termGen})
	mm2 := n2.(model)
	if mm2.termScroll != 0 {
		t.Fatalf("non-follow tick moved the offset: %d, want 0 (held)", mm2.termScroll)
	}
	mm2.term.close()
}
