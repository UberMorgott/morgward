package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
)

// TestSpliceCursorBlockPlainRow proves the reverse-video block lands at the correct
// VISUAL column of a plain (un-styled) row and leaves the surrounding text intact: the
// stripped result is byte-identical to the input (reverse video adds no printable
// width), and the styled result carries the SGR reverse (7) around the target column.
func TestSpliceCursorBlockPlainRow(t *testing.T) {
	row := "hello world"
	out := spliceCursorBlock(row, 6, "w", 1, true) // column 6 = the 'w'

	// Visual width and printable content are unchanged (the cursor is an overlay).
	if got := ansi.Strip(out); got != row {
		t.Fatalf("stripped overlay = %q, want unchanged %q", got, row)
	}
	if ansi.StringWidth(out) != ansi.StringWidth(row) {
		t.Fatalf("overlay changed visual width: %d → %d", ansi.StringWidth(row), ansi.StringWidth(out))
	}
	// The reverse-video SGR must be present (the cursor block).
	if !strings.Contains(out, "\x1b[7m") {
		t.Fatalf("overlay missing reverse-video SGR: %q", out)
	}
	// The character left of the cursor column must survive ahead of the reverse span.
	if idx := strings.Index(out, "\x1b[7m"); idx >= 0 {
		if !strings.Contains(out[:idx], "hello ") {
			t.Fatalf("text before cursor not preserved: %q", out[:idx])
		}
	}
}

// TestSpliceCursorBlockStyledRow proves the splice is ANSI-aware: given a row whose
// cells carry SGR styling, the cursor lands at the right VISUAL column (not a byte
// offset that would fall inside an escape) and the rest of the row's styling is kept.
func TestSpliceCursorBlockStyledRow(t *testing.T) {
	// "AB" bold, then "CD" plain. Visual columns: A=0 B=1 C=2 D=3.
	row := "\x1b[1mAB\x1b[0mCD"
	out := spliceCursorBlock(row, 2, "C", 1, true) // cursor on 'C'

	if got := ansi.Strip(out); got != "ABCD" {
		t.Fatalf("stripped styled overlay = %q, want ABCD", got)
	}
	if ansi.StringWidth(out) != 4 {
		t.Fatalf("overlay visual width = %d, want 4", ansi.StringWidth(out))
	}
	// Reverse span present and the bold prefix preserved before it.
	rev := strings.Index(out, "\x1b[7m")
	if rev < 0 {
		t.Fatalf("no reverse span in styled overlay: %q", out)
	}
	if !strings.Contains(out[:rev], "\x1b[1m") {
		t.Fatalf("bold styling before cursor lost: %q", out[:rev])
	}
}

// TestSpliceCursorBlockEmptyCellAndClamp proves an empty cell renders a reverse SPACE
// block, and a column at/over the row end clamps gracefully (appends the block, no
// panic).
func TestSpliceCursorBlockEmptyCellAndClamp(t *testing.T) {
	// Empty cell content (a genuinely-blank cursor cell at the end of a short row) →
	// reverse a single space block appended after the content. col==row width clamps.
	out := spliceCursorBlock("ab", 2, "", 0, true)
	if !strings.Contains(out, "\x1b[7m") {
		t.Fatalf("empty-cell overlay missing reverse SGR: %q", out)
	}
	if got := ansi.Strip(out); got != "ab " {
		t.Fatalf("empty-cell stripped = %q, want \"ab \" (block space appended)", got)
	}

	// Column past the end → clamp (no panic), block appended after the content.
	out2 := spliceCursorBlock("ab", 9, " ", 1, true)
	if !strings.Contains(out2, "\x1b[7m") {
		t.Fatalf("clamped overlay missing reverse SGR: %q", out2)
	}
	if !strings.HasPrefix(ansi.Strip(out2), "ab") {
		t.Fatalf("clamped overlay dropped content: %q", ansi.Strip(out2))
	}
}

// TestSpliceCursorBlockWideGlyph proves a DOUBLE-WIDTH cursor cell (CJK/emoji) is
// handled by cell WIDTH, not col+1: the reverse block covers the WHOLE 2-wide glyph and
// the row's visual width + content are unchanged (no glyph duplication, no rightward
// shift). Before the width fix, cutting right at col+1 landed mid-glyph and ansi.Cut
// re-emitted the whole glyph → width grew by 2 and the glyph doubled.
func TestSpliceCursorBlockWideGlyph(t *testing.T) {
	// 界 is 2 cells wide. Visual columns: 界=0..1, a=2, b=3 → total width 4.
	const cjk = "界"
	cases := []struct {
		name string
		row  string
		col  int
		cell string
	}{
		{"head", cjk + "ab", 0, cjk},     // cursor on the wide glyph at col 0
		{"mid", "x" + cjk + "y", 1, cjk}, // wide glyph in the middle
		{"eol", "ab" + cjk, 2, cjk},      // wide glyph at end-of-line
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantW := ansi.StringWidth(c.row)
			out := spliceCursorBlock(c.row, c.col, c.cell, 2, true)

			if got := ansi.StringWidth(out); got != wantW {
				t.Fatalf("wide-glyph overlay changed width: %d → %d (glyph duplicated?)", wantW, got)
			}
			if got := ansi.Strip(out); got != c.row {
				t.Fatalf("wide-glyph overlay corrupted content: %q → %q", c.row, got)
			}
			// The reverse span must contain the FULL glyph (covers both its cells).
			rev := strings.Index(out, "\x1b[7m")
			unrev := strings.Index(out, "\x1b[27m")
			if rev < 0 || unrev < 0 || unrev < rev {
				t.Fatalf("malformed reverse span: %q", out)
			}
			if !strings.Contains(out[rev:unrev], cjk) {
				t.Fatalf("reverse span does not cover the wide glyph: %q", out[rev:unrev])
			}
		})
	}
}

// TestCursorSnapshotConsistent proves the session snapshot returns an internally-
// consistent view: the scrollback length it reports matches the scrollback lines it
// returns, and the cursor Y is within the screen — all from ONE locked read, so the
// overlay maps against exactly the body it splices (no TOCTOU across separate reads).
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

	snap := m.term.cursorSnapshot()
	// Core consistency invariant: the reported length matches the lines themselves, from
	// the SAME locked read (so the overlay's body math can't drift from the body).
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

// TestTerminalViewUsesSnapshotForOverlay proves terminalView derives the cursor body
// row from the SAME scrollback length it used to assemble the body. We seed scrollback,
// pin to bottom with the blink solid, render, and assert the reverse block lands on the
// row computed from the snapshot's scrollbackLen + cursorY — not a freshly re-read
// scrollback length (which could drift under the concurrent drain).
func TestTerminalViewUsesSnapshotForOverlay(t *testing.T) {
	m, _ := termModel(t, 40, 16)
	defer m.term.close()
	seedLines(t, m, 40, 5)
	m.term.write([]byte("$ ")) // prompt under the cursor (content row, not a trimmed blank)
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "prompt to render")

	m.termFollow = true
	m.termPinIfFollowing()
	m.termBlinkOn = true

	snap := m.term.cursorSnapshot()
	wantRow := cursorBodyRow(snap.scrollbackLen, snap.cursorY, snap.alt)

	// Build the body the same way terminalView does (from the SAME snapshot) and overlay
	// it; the reverse block must be on wantRow and nowhere else.
	body := liveBodyFromSnapshot(snap)
	overlaid := m.applyCursorOverlay(body, snap)
	revRows := 0
	for i, ln := range overlaid {
		base := 0
		if i < len(body) {
			base = strings.Count(body[i], "\x1b[7m")
		}
		if strings.Count(ln, "\x1b[7m") > base {
			revRows++
			if i != wantRow {
				t.Fatalf("cursor block on row %d, want snapshot-derived row %d", i, wantRow)
			}
		}
	}
	if revRows != 1 {
		t.Fatalf("expected exactly one cursor row, got %d", revRows)
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

// TestTerminalCursorDrawnAtPrompt proves the live terminal view draws a reverse-video
// cursor block while pinned to the bottom with the blink ON. We write a prompt, pin to
// follow, force the blink solid, and assert the rendered view contains the reverse SGR.
func TestTerminalCursorDrawnAtPrompt(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()

	m.term.write([]byte("$ "))
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "prompt to render")

	m.termFollow = true
	m.termPinIfFollowing()
	m.termBlinkOn = true

	if !strings.Contains(m.terminalView(), "\x1b[7m") {
		t.Fatal("expected a reverse-video cursor block in the view at the prompt")
	}
}

// TestTerminalCursorHiddenWhenBlinkOff proves the cursor is NOT drawn on the blink-off
// half of the cycle (so it actually blinks rather than showing solid).
func TestTerminalCursorHiddenWhenBlinkOff(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()
	m.term.write([]byte("$ "))
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "prompt to render")

	m.termFollow = true
	m.termPinIfFollowing()
	m.termBlinkOn = false

	if strings.Count(m.terminalView(), "\x1b[7m") != countReverseInBody(m) {
		t.Fatal("cursor block drawn while blink is OFF — it must be hidden on the off half")
	}
}

// TestTerminalCursorHiddenWhenScrolledUp proves no cursor is drawn while the user has
// scrolled up into scrollback (termFollow=false): the cursor belongs to the live
// bottom, not to history being read.
func TestTerminalCursorHiddenWhenScrolledUp(t *testing.T) {
	m, _ := termModel(t, 40, 16)
	defer m.term.close()
	seedLines(t, m, 40, 5)

	m.termBlinkOn = true
	m.termFollow = false
	m.termScroll = 0 // scrolled to the very top

	if strings.Count(m.terminalView(), "\x1b[7m") != countReverseInBody(m) {
		t.Fatal("cursor drawn while scrolled up — it must only show at the live bottom")
	}
}

// TestTerminalCursorHiddenWhenAppHidesIt proves a remote app hiding the cursor (DECTCEM
// \x1b[?25l, delivered through the live drain → CursorVisibility callback) suppresses
// the overlay even when pinned + blink-on (vim/password prompts hide the cursor).
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
	m.termBlinkOn = true

	if strings.Count(m.terminalView(), "\x1b[7m") != countReverseInBody(m) {
		t.Fatal("cursor drawn while the app hid it (?25l) — must respect CursorVisibility")
	}
}

// TestTerminalCursorHiddenWhenFinished proves no cursor on a finished session (the body
// is a "session ended" banner, not live content).
func TestTerminalCursorHiddenWhenFinished(t *testing.T) {
	m, fs := termModel(t, 80, 24)
	m.term.close()
	<-fs.returned
	waitFor(t, time.Second, func() bool { d, _ := m.term.finished(); return d }, "session to finish")

	m.termBlinkOn = true
	m.termFollow = true
	if strings.Contains(m.terminalView(), "\x1b[7m") {
		t.Fatal("cursor drawn on a finished session — must be suppressed")
	}
}

// TestBlinkToggleAtBoundary proves the blink flips at the ~530ms boundary: feeding the
// tick handler enough ticks (530ms / 25ms ≈ 22) flips termBlinkOn exactly once.
func TestBlinkToggleAtBoundary(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()
	m.termBlinkOn = true
	m.termBlinkTicks = 0

	// The flip fires on the first tick where ticks*interval >= period — i.e. ceil(period
	// / interval), not the floored division (530/25 = 21.2 → flips on tick 22).
	ticks := int(termBlinkPeriod / termTickInterval)
	if termBlinkPeriod%termTickInterval != 0 {
		ticks++
	}
	// One tick short of the boundary: no flip yet.
	for i := 0; i < ticks-1; i++ {
		next, _ := m.Update(termTickMsg{gen: m.termGen})
		m = next.(model)
	}
	if !m.termBlinkOn {
		t.Fatalf("blink flipped early after %d ticks (boundary is %d)", ticks-1, ticks)
	}
	// The tick that crosses the boundary flips it and resets the counter.
	next, _ := m.Update(termTickMsg{gen: m.termGen})
	m = next.(model)
	if m.termBlinkOn {
		t.Fatal("blink did not flip at the boundary")
	}
	if m.termBlinkTicks != 0 {
		t.Fatalf("blink tick counter not reset at boundary: %d", m.termBlinkTicks)
	}
}

// TestForwardedKeyResetsBlinkSolid proves typing a (forwarded) key snaps the cursor
// SOLID immediately: termBlinkOn=true, termBlinkTicks=0 — standard terminal feel.
func TestForwardedKeyResetsBlinkSolid(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()
	// Start mid-cycle on the OFF half.
	m.termBlinkOn = false
	m.termBlinkTicks = 7

	next, _ := m.terminalKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	mm := next.(model)
	if !mm.termBlinkOn {
		t.Fatal("a forwarded keypress must set the cursor solid (termBlinkOn=true)")
	}
	if mm.termBlinkTicks != 0 {
		t.Fatalf("a forwarded keypress must reset the blink counter, got %d", mm.termBlinkTicks)
	}
}

// TestBlurMsgClearsFocus proves a tea.BlurMsg sets focused=false and a tea.FocusMsg
// sets it back to true (DEC ?1004 focus reporting drives the cursor's blink behavior).
func TestBlurMsgClearsFocus(t *testing.T) {
	m := newModel()
	if !m.focused {
		t.Fatal("model should start focused (apps launch focused; ?1004 reports CHANGES)")
	}
	next, _ := m.Update(tea.BlurMsg{})
	m = next.(model)
	if m.focused {
		t.Fatal("BlurMsg must clear focused")
	}
	next, _ = m.Update(tea.FocusMsg{})
	m = next.(model)
	if !m.focused {
		t.Fatal("FocusMsg must set focused")
	}
}

// TestUnfocusedCursorIsSteady proves the cursor is drawn even on the blink-OFF half
// when the window is unfocused (steady, no blink-out — the user's complaint), and in
// the UNFOCUSED style (underline) rather than the focused reverse block.
func TestUnfocusedCursorIsSteady(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()
	m.term.write([]byte("$ "))
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "prompt to render")

	m.termFollow = true
	m.termPinIfFollowing()
	m.focused = false
	m.termBlinkOn = false // would HIDE the cursor if focused — must NOT hide when unfocused

	out := m.terminalView()
	// Steady: the cursor is shown despite blink-off → an unfocused-style marker appears
	// beyond whatever the body already had.
	if strings.Count(out, "\x1b[4m") <= countUnderlineInBody(m) {
		t.Fatal("unfocused cursor not drawn on the blink-off half — it must be STEADY")
	}
	// Distinct style: the unfocused cursor must NOT be the focused reverse block.
	if strings.Count(out, "\x1b[7m") > countReverseInBody(m) {
		t.Fatal("unfocused cursor used the focused reverse style — must be the hollow/underline style")
	}
}

// TestFocusedBlinkStillGates proves the focused behavior is unchanged: with the window
// focused and the blink on its OFF half, the cursor is hidden (it still blinks).
func TestFocusedBlinkStillGates(t *testing.T) {
	m, _ := termModel(t, 80, 24)
	defer m.term.close()
	m.term.write([]byte("$ "))
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "prompt to render")

	m.termFollow = true
	m.termPinIfFollowing()
	m.focused = true
	m.termBlinkOn = false

	out := m.terminalView()
	if strings.Count(out, "\x1b[7m") > countReverseInBody(m) {
		t.Fatal("focused cursor drawn on the blink-off half — focused must still blink")
	}
	if strings.Count(out, "\x1b[4m") > countUnderlineInBody(m) {
		t.Fatal("focused blink-off frame drew an underline cursor — should draw nothing")
	}
}

// TestViewReportsFocus proves View() enables terminal focus reporting (?1004) ONLY on
// the embedded-terminal phase (which consumes m.focused for the cursor) and leaves it
// off elsewhere, so ?1004 can't break mouse handling on legacy consoles.
func TestViewReportsFocus(t *testing.T) {
	m := newModel()
	m.w, m.h = 80, 24
	// Non-terminal phase (form): focus reporting must be OFF.
	if v := m.View(); v.ReportFocus {
		t.Fatal("View().ReportFocus must be false off the terminal phase (?1004 breaks mouse on legacy consoles)")
	}
	// Terminal phase: focus reporting must be ON so the cursor gets Focus/Blur.
	m.phase = phaseTerminal
	if v := m.View(); !v.ReportFocus {
		t.Fatal("View().ReportFocus must be true on phaseTerminal so focus/blur is reported")
	}
}

// countUnderlineInBody is the underline-SGR analogue of countReverseInBody — the
// unfocused-cursor tests assert the count vs this baseline so legitimate underline
// styling in the stream can't false-trigger them.
func countUnderlineInBody(m model) int {
	n := 0
	for _, ln := range m.terminalBody() {
		n += strings.Count(ln, "\x1b[4m")
	}
	return n
}

// countReverseInBody returns how many reverse-video SGRs appear in the body content
// independent of the cursor overlay (the emulator itself may emit reverse video, e.g.
// a selection or a prompt theme). The cursor-gating tests assert the count is UNCHANGED
// vs this baseline, so they don't false-fail on legitimate reverse video in the stream.
func countReverseInBody(m model) int {
	n := 0
	for _, ln := range m.terminalBody() {
		n += strings.Count(ln, "\x1b[7m")
	}
	return n
}
