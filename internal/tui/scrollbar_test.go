package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/ui"
)

// --- scrollbar drag -----------------------------------------------------------
//
// The bar lives in the box's LAST column, over the rows of the current screen's
// scroll region. Every test here drives the REAL Update loop (like the existing
// click tests) so the grab test, the drag and the release all go through the same
// dispatch the running program uses.

// dragModel builds a phaseMatrix model whose body OVERFLOWS its region, so a
// scrollbar is drawn: body = 7 lines, viewH = 4, maxOff = 3. Returns the model plus
// the region geometry every assertion below is expressed in.
func dragModel(t *testing.T) (m model, barX, topRow, viewH, total, maxOff int) {
	t.Helper()
	m = matrixModel(100, 12)
	topRow, viewH, total, _, ok := m.scrollGeom()
	if !ok {
		t.Fatal("matrix model has no scroll region")
	}
	if total <= viewH {
		t.Fatalf("test needs an overflowing region: total=%d viewH=%d", total, viewH)
	}
	return m, m.boxWidth() - 1, topRow, viewH, total, total - viewH
}

// pressBar/motionTo/release drive one drag gesture through Update.
func pressBar(t *testing.T, m model, x, y int) model {
	t.Helper()
	next, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return next.(model)
}

func motionTo(t *testing.T, m model, x, y int) model {
	t.Helper()
	next, _ := m.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
	return next.(model)
}

// TestScrollbarThumbDragMovesOffset is the whole gesture: press the thumb, drag it to
// the bottom of the track, release. The offset follows the thumb and the drag ends.
func TestScrollbarThumbDragMovesOffset(t *testing.T) {
	m, barX, topRow, viewH, total, maxOff := dragModel(t)

	start, end := scrollThumb(viewH, total, 0)
	if start != 0 || end <= start {
		t.Fatalf("thumb at offset 0 = [%d,%d), want it to start at the top", start, end)
	}

	m = pressBar(t, m, barX, topRow+start)
	if !m.dragging {
		t.Fatal("pressing the thumb must arm the drag")
	}
	if m.dragGrab != 0 {
		t.Fatalf("dragGrab=%d, want 0 (grabbed the thumb's top row)", m.dragGrab)
	}
	if m.matScroll != 0 {
		t.Fatalf("grabbing the thumb must not move the offset, got %d", m.matScroll)
	}

	m = motionTo(t, m, barX, topRow+viewH-1)
	if m.matScroll != maxOff {
		t.Fatalf("drag to the track bottom: matScroll=%d, want %d", m.matScroll, maxOff)
	}

	next, _ := m.Update(tea.MouseReleaseMsg{Button: tea.MouseLeft})
	m = next.(model)
	if m.dragging {
		t.Fatal("release must end the drag")
	}
	// Post-release motion is inert.
	m = motionTo(t, m, barX, topRow)
	if m.matScroll != maxOff {
		t.Fatalf("motion after release moved the offset to %d, want %d", m.matScroll, maxOff)
	}
}

// TestScrollbarReleaseWithoutButtonEndsDrag pins the X10-fallback case: when the
// terminal refuses SGR, bubbletea falls back to X10, whose release event carries
// Button: MouseNone. Gating the drag-end on MouseLeft would leave the drag armed
// forever there, so ANY release must end it.
func TestScrollbarReleaseWithoutButtonEndsDrag(t *testing.T) {
	m, barX, topRow, viewH, total, _ := dragModel(t)
	start, _ := scrollThumb(viewH, total, 0)

	m = pressBar(t, m, barX, topRow+start)
	if !m.dragging {
		t.Fatal("pressing the thumb must arm the drag")
	}
	next, _ := m.Update(tea.MouseReleaseMsg{Button: tea.MouseNone})
	m = next.(model)
	if m.dragging {
		t.Fatal("a release carrying MouseNone (X10 encoding) must still end the drag")
	}
}

// TestScrollbarTrackClickJumps proves a press on the bare track (below the thumb)
// jumps the offset there instead of doing nothing.
func TestScrollbarTrackClickJumps(t *testing.T) {
	m, barX, topRow, viewH, total, maxOff := dragModel(t)

	_, end := scrollThumb(viewH, total, 0)
	trackRow := viewH - 1
	if trackRow < end {
		t.Fatalf("thumb [.., %d) leaves no bare track row below it (viewH=%d)", end, viewH)
	}

	m = pressBar(t, m, barX, topRow+trackRow)
	if m.matScroll != maxOff {
		t.Fatalf("track click at region row %d: matScroll=%d, want %d", trackRow, m.matScroll, maxOff)
	}
	if !m.dragging {
		t.Fatal("a track click must also arm the drag (press-and-drag from the jump)")
	}
}

// TestScrollbarDragClampsPastBothEnds proves dragging far above/below the region
// clamps instead of running the offset out of range.
func TestScrollbarDragClampsPastBothEnds(t *testing.T) {
	m, barX, topRow, viewH, total, maxOff := dragModel(t)
	start, _ := scrollThumb(viewH, total, 0)

	m = pressBar(t, m, barX, topRow+start)
	m = motionTo(t, m, barX, topRow+viewH+100)
	if m.matScroll != maxOff {
		t.Fatalf("drag far below the region: matScroll=%d, want the max %d", m.matScroll, maxOff)
	}
	m = motionTo(t, m, barX, topRow-100)
	if m.matScroll != 0 {
		t.Fatalf("drag far above the region: matScroll=%d, want 0", m.matScroll)
	}
}

// TestScrollbarIgnoresNonOverflowingRegion proves a screen whose body FITS draws no
// bar and therefore grabs nothing — the border cell keeps behaving as before.
func TestScrollbarIgnoresNonOverflowingRegion(t *testing.T) {
	m := matrixModel(100, 40) // tall terminal: the 7-line body fits
	topRow, viewH, total, _, ok := m.scrollGeom()
	if !ok || total > viewH {
		t.Fatalf("test needs a NON-overflowing region: ok=%v total=%d viewH=%d", ok, total, viewH)
	}
	if s, e := scrollThumb(viewH, total, 0); s != 0 || e != 0 {
		t.Fatalf("a fitting body must draw no thumb, got [%d,%d)", s, e)
	}

	m = pressBar(t, m, m.boxWidth()-1, topRow)
	if m.dragging {
		t.Fatal("no scrollbar is drawn, so the border cell must not arm a drag")
	}
	if m.matScroll != 0 {
		t.Fatalf("matScroll=%d, want 0", m.matScroll)
	}
}

// TestScrollbarIgnoresContentColumns proves the grab is confined to the bar's own
// column: a press one cell to its left still falls through to the per-phase hit-tests.
func TestScrollbarIgnoresContentColumns(t *testing.T) {
	m, barX, topRow, _, _, _ := dragModel(t)
	m = pressBar(t, m, barX-1, topRow)
	if m.dragging {
		t.Fatalf("a press at x=%d (not the bar column %d) must not arm a drag", barX-1, barX)
	}
}

// TestScrollbarDragTerminalRearmsFollow is the phaseTerminal exception: the drag must
// route through termScrollBy, so dragging to the bottom re-arms follow mode (new output
// sticks again) exactly like wheel-down does.
func TestScrollbarDragTerminalRearmsFollow(t *testing.T) {
	m, _ := termModel(t, 40, 16)
	defer m.term.close()
	seedLines(t, m, 40, 5)

	topRow, viewH, total, _, ok := m.scrollGeom()
	if !ok || total <= viewH {
		t.Fatalf("test needs an overflowing terminal body: ok=%v total=%d viewH=%d", ok, total, viewH)
	}
	barX := m.boxWidth() - 1

	// Scroll up first: follow drops and the thumb leaves the bottom.
	next, _ := m.Update(wheelMsg(tea.MouseWheelUp))
	m = next.(model)
	if m.termFollow {
		t.Fatal("wheel up should have dropped follow mode")
	}

	start, _ := scrollThumb(viewH, total, m.termScroll)
	m = pressBar(t, m, barX, topRow+start)
	m = motionTo(t, m, barX, topRow+viewH+50) // drag past the bottom
	if want := total - viewH; m.termScroll != want {
		t.Fatalf("drag to the bottom: termScroll=%d, want %d", m.termScroll, want)
	}
	if !m.termFollow {
		t.Fatal("dragging to the bottom must re-arm termFollow (drag routes through termScrollBy)")
	}
}

// TestScrollbarFrozenOnAltScreen proves the terminal's alt-screen freeze (vim/top own
// the screen) also freezes the bar: no grab while scrollback is frozen.
func TestScrollbarFrozenOnAltScreen(t *testing.T) {
	m, _ := termModel(t, 40, 16)
	defer m.term.close()
	seedLines(t, m, 40, 5)

	m.term.write([]byte("\x1b[?1049h"))
	waitFor(t, 2*time.Second, func() bool { return m.term.altScreen() }, "emulator to enter alt-screen")

	if _, _, _, _, ok := m.scrollGeom(); ok {
		t.Fatal("alt-screen must expose no draggable scroll region")
	}
	m = pressBar(t, m, m.boxWidth()-1, summaryBodyTopRow)
	if m.dragging {
		t.Fatal("no drag may be armed while the alternate screen is on")
	}
}

// TestScrollbarColumnMatchesRenderedThumb is the derivation check for the bar's column:
// the grab tests all key off m.boxWidth()-1, so that column must be where the RENDERED
// thumb glyph actually lands. It reads the real frame rather than trusting the
// arithmetic, which also pins the box's left edge at x=0.
func TestScrollbarColumnMatchesRenderedThumb(t *testing.T) {
	m, barX, topRow, viewH, total, maxOff := dragModel(t)
	m.matScroll = maxOff // thumb at the BOTTOM of the track
	start, end := scrollThumb(viewH, total, maxOff)

	lines := strings.Split(ui.SanitizeStreamLine(m.matrixView()), "\n")
	for row := 0; row < viewH; row++ {
		cells := []rune(lines[topRow+row])
		if len(cells)-1 != barX {
			t.Fatalf("region row %d is %d cells wide, so its last column is %d, not barX=%d",
				row, len(cells), len(cells)-1, barX)
		}
		want := '│' // track
		if row >= start && row < end {
			want = '█' // thumb
		}
		if got := cells[barX]; got != want {
			t.Fatalf("region row %d column %d = %q, want %q (thumb rows [%d,%d))",
				row, barX, got, want, start, end)
		}
	}
}

// TestScrollThumbInverse pins the three properties the drag actually depends on, across
// a range of geometries. Exact round-tripping is impossible (both directions divide in
// integers), so what is asserted is what the user can see: the track's ends map to the
// content's ends, the mapping never goes backwards, and re-drawing the thumb at the
// grabbed row leaves it within one row of the pointer (no visible slip mid-drag).
func TestScrollThumbInverse(t *testing.T) {
	for _, g := range [][2]int{{4, 7}, {5, 40}, {10, 11}, {2, 9}, {20, 21}, {3, 300}} {
		viewH, total := g[0], g[1]
		maxOff := total - viewH
		thumb := max(viewH*viewH/total, 1)
		span := viewH - thumb

		if got := offsetForThumbRow(0, viewH, total); got != 0 {
			t.Fatalf("viewH=%d total=%d: track top → offset %d, want 0", viewH, total, got)
		}
		if got := offsetForThumbRow(span, viewH, total); got != maxOff {
			t.Fatalf("viewH=%d total=%d: track bottom (row %d) → offset %d, want %d",
				viewH, total, span, got, maxOff)
		}
		prev := -1
		for row := 0; row <= span; row++ {
			off := offsetForThumbRow(row, viewH, total)
			if off < prev {
				t.Fatalf("viewH=%d total=%d: row %d → offset %d went backwards from %d",
					viewH, total, row, off, prev)
			}
			prev = off
			start, _ := scrollThumb(viewH, total, clampScroll(off, total, viewH))
			if start < row-1 || start > row+1 {
				t.Fatalf("viewH=%d total=%d: grabbed row %d redrew the thumb at %d (slipped)",
					viewH, total, row, start)
			}
		}
	}
}
