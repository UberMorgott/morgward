package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/UberMorgott/morgward/internal/ui"
	"github.com/UberMorgott/morgward/internal/version"
)

// sanitizeStreamLine cleans one chunk of streamed output (which may contain
// several "\n"-separated lines) so it can never break the box frame:
//   - carriage returns are collapsed: apt/dpkg redraw a progress line by emitting
//     "...30%\r...60%\r...100%" — keep only the LAST \r-segment of each line, which
//     is what a terminal would have shown after the redraws settled.
//   - tabs expand to a single space (the box has no tab stops; a raw \t would
//     advance the cursor unpredictably and overflow innerW).
//   - ALL ANSI escape / CSI sequences and other C0 control chars are stripped.
//     The viewport re-renders plain text through lipgloss, so colour here would be
//     lost on wrap anyway; stripping it removes every cursor-move/erase sequence
//     that would otherwise shift the frame. The raw log file is untouched.
//
// It is a pure function (no model state) so it is unit-testable; wrapping to the
// content width happens afterwards in wrapped().
//
// The implementation lives in internal/ui (ui.SanitizeStreamLine) so the CLI
// print path and the log file share one hardened stripper with the TUI pane;
// this thin wrapper keeps the package-local name the TUI already uses.
func sanitizeStreamLine(s string) string { return ui.SanitizeStreamLine(s) }

// wrapped soft-wraps the accumulated (already-sanitized) log text to the viewport
// width so long lines (e.g. SSH error messages or server output) hard-wrap inside
// the box instead of overflowing. The wrap width equals innerW (vp.Width()), and
// the per-line "  " indent added upstream (ui.Logger.Stream) is part of the wrapped
// text, so every wrapped segment — first or continuation — is ≤ innerW and never
// crosses the border.
func (m model) wrapped() string {
	w := m.vp.Width()
	if w < 1 {
		w = max(innerWidth(m.w), 1)
	}
	return lipgloss.NewStyle().Width(w).Render(m.content)
}

// vpWidth/vpHeight compute the bounded inner viewport size for the run-phase box
// so the log never overflows the box or overlaps the contextual hints.
func (m model) vpWidth() int { return max(innerWidth(m.w), 1) }

func (m model) vpHeight() int {
	// phaseRun hand-draws its frame (runView) instead of going through
	// framedScrollView, but it spends the SAME chrome — top border, switcher(nav),
	// hint, bottom, monitor (frameChromeRows) — plus two fixed rows above the
	// viewport: the progress line and the blank spacer. So the row budget comes from
	// the one shared arithmetic, not a second hand-rolled `m.h - 6 - 3`.
	base := m.chromeViewH(2, 0)
	if m.finished {
		base-- // reserve a row for the "Back to main" button line
		base -= m.finishedTailRows()
	}
	return max(base, 3)
}

// minBoxWidth clamps the box width so the hand-drawn border never goes negative.
const minBoxWidth = 40

// boxWidth is the outer width of both boxes (the full terminal width, clamped).
func (m model) boxWidth() int { return max(m.w, minBoxWidth) }

// innerWidth is the content width inside a box: total − 2 border − 2 padding.
func innerWidth(w int) int {
	if w < minBoxWidth {
		w = minBoxWidth
	}
	return w - 4
}

// wrap word-wraps s to at most w display cells per line (lipgloss.Width-aware so
// multibyte Cyrillic wraps correctly), returning the lines. A single word longer
// than w is hard-split. w<1 yields a single (unwrapped) line.
func wrap(s string, w int) []string {
	if w < 1 {
		return []string{s}
	}
	var lines []string
	for para := range strings.SplitSeq(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		cur := ""
		for _, word := range words {
			// Hard-split a word that alone exceeds the width.
			for lipgloss.Width(word) > w {
				head := truncDisplay(word, w)
				if cur != "" {
					lines = append(lines, cur)
					cur = ""
				}
				lines = append(lines, head)
				word = word[len(head):]
			}
			switch {
			case cur == "":
				cur = word
			case lipgloss.Width(cur)+1+lipgloss.Width(word) <= w:
				cur += " " + word
			default:
				lines = append(lines, cur)
				cur = word
			}
		}
		if cur != "" {
			lines = append(lines, cur)
		}
	}
	return lines
}

// titledTop draws a box top border with the title centered, breaking the border:
// TopLeft + left dashes + title + right dashes + TopRight, total width = w.
func titledTop(b lipgloss.Border, title string, w int) string {
	if w < minBoxWidth {
		w = minBoxWidth
	}
	tw := lipgloss.Width(title)
	dashTotal := w - 2 - tw // minus the two corner runes
	if dashTotal < 0 {
		// Title too wide for the border — clip it and use no dashes.
		title = truncDisplay(title, w-2)
		tw = lipgloss.Width(title)
		dashTotal = max(w-2-tw, 0)
	}
	leftN := dashTotal / 2
	rightN := dashTotal - leftN
	return borderStyle.Render(b.TopLeft) +
		borderStyle.Render(strings.Repeat(b.Top, leftN)) +
		title +
		borderStyle.Render(strings.Repeat(b.Top, rightN)) +
		borderStyle.Render(b.TopRight)
}

// borderLine draws a plain horizontal border edge: left + dashes + right, width w.
func borderLine(left, mid, right string, w int) string {
	w = max(w, minBoxWidth)
	n := max(w-2, 0)
	return borderStyle.Render(left + strings.Repeat(mid, n) + right)
}

// contentLine wraps one content line in the box: Left + " " + padded line + " " +
// Right, where the line is truncated/padded to exactly innerW display cells.
func contentLine(b lipgloss.Border, line string, innerW int) string {
	return contentLineR(b, line, innerW, borderStyle.Render(b.Right))
}

// contentLineR is contentLine with an explicit right-border cell, so a scrollable
// region can substitute a scrollbar thumb/track glyph there (see renderScrollRegion)
// while every other row keeps the plain border. right must be exactly one display
// cell wide (already styled).
func contentLineR(b lipgloss.Border, line string, innerW int, right string) string {
	if innerW < 0 {
		innerW = 0
	}
	line = truncDisplay(line, innerW)
	if pad := innerW - lipgloss.Width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return borderStyle.Render(b.Left) + " " + line + " " + right
}

// --- shared screen frame ------------------------------------------------------
//
// Every scrolling screen (dashboard, security, summary, matrix, wiki, key, files)
// emits the SAME chrome in the SAME order; only the slots below differ:
//
//	row 0                        : titled top border
//	row 1                        : nav line (RU|EN switcher or the global tab bar)
//	rows 2 .. 2+len(fixed)-1     : fixed content rows that never scroll
//	then viewH rows              : the scroll region (scrollbar in the right border)
//	then len(pinned) rows        : content rows pinned below the region
//	then                         : hint line, bottom border, the 3-row monitor box
//
// Filling a slot is the ONLY way a screen may differ, so the row budget stays in one
// place (chromeViewH) instead of being re-derived per screen.
type frame struct {
	title  string                            // titledTop text (centered in the top border)
	nav    func(lipgloss.Border, int) string // switcherLine or navTabStripLine — a whole rendered row
	fixed  []string                          // non-scrolling rows above the scroll region
	body   []string                          // the scrollable rows
	viewH  int                               // scroll-region height — the screen's own named *ViewH
	scroll int                               // scroll offset (clamped here)
	pinned []string                          // rows pinned below the scroll region
	hint   string                            // help/status line, ALREADY styled by the caller
}

// framedScrollView renders f: the single place the shared chrome is emitted.
func (m model) framedScrollView(f frame) string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	var sb strings.Builder
	sb.WriteString(titledTop(b, f.title, bw))
	sb.WriteByte('\n')
	sb.WriteString(f.nav(b, innerW))
	sb.WriteByte('\n')
	for _, line := range f.fixed {
		sb.WriteString(contentLine(b, line, innerW))
		sb.WriteByte('\n')
	}
	m.renderScrollRegion(&sb, b, f.body, innerW, f.viewH,
		clampScroll(f.scroll, len(f.body), f.viewH))
	for _, line := range f.pinned {
		sb.WriteString(contentLine(b, line, innerW))
		sb.WriteByte('\n')
	}
	sb.WriteString(contentLine(b, f.hint, innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')
	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// frameTitle is the shared titled-top text (" morgward vX.Y.Z ") used by every
// framed screen except Files, which titles itself after its tab.
func frameTitle() string { return " " + version.Name + " v" + version.Version + " " }

// frameChromeRows is the chrome EVERY framed screen always spends: top border + nav
// + hint + main-box bottom (4) plus the pinned 3-row monitor box.
const frameChromeRows = 7

// chromeViewH is the SINGLE arithmetic source for every framed screen's scroll-region
// height: the terminal height minus the always-present chrome, minus that screen's own
// fixed rows above the region and pinned rows below it, floored at 1 so the region never
// vanishes on a tiny terminal. Each screen keeps ONE named wrapper (matrixBodyViewH,
// secBodyViewH, filesListViewH, …) that BOTH its render and its hit-tests call, so a
// click target can never drift from the row that was drawn.
func (m model) chromeViewH(fixed, pinned int) int {
	return max(m.h-frameChromeRows-fixed-pinned, 1)
}

// bodyViewH is chromeViewH with no screen-specific rows: the full middle region.
func (m model) bodyViewH() int { return m.chromeViewH(0, 0) }

// clampScroll bounds a scroll offset to [0, max(0,total-viewH)] so it can never
// scroll past the end (or before the start). Recomputed on every use, so a resize
// that grows the window (raising viewH) automatically pulls the offset back.
func clampScroll(off, total, viewH int) int {
	maxOff := max(total-viewH, 0)
	if off < 0 {
		return 0
	}
	if off > maxOff {
		return maxOff
	}
	return off
}

// scrollBy moves the CURRENT screen's scroll region by d rows (negative = up), and is
// the SINGLE place every screen's ↑↓/k/j and mouse-wheel scrolling lands — the six
// phases previously repeated the same clamp call twice each (once per key, once per
// wheel). Body length and region height are re-derived here rather than stored, so the
// offset is clamped against the CURRENT content and terminal size on every scroll and
// can never go stale after a resize, a language switch or a fresh audit.
func (m model) scrollBy(d int) model {
	iw := innerWidth(m.boxWidth())
	switch m.phase {
	case phaseRun:
		if d < 0 {
			m.vp.ScrollUp(-d)
		} else {
			m.vp.ScrollDown(d)
		}
	case phaseSummary:
		m.sumScroll = clampScroll(m.sumScroll+d, len(m.summaryBodyLines()), m.summaryBodyViewH())
	case phaseWiki:
		m.wikiScroll = clampScroll(m.wikiScroll+d, len(m.wikiBodyLines(iw)), m.wikiBodyViewH())
	case phaseMatrix:
		m.matScroll = clampScroll(m.matScroll+d, len(m.matrixBodyLines(iw)), m.matrixBodyViewH())
	case phaseKey:
		body, _ := m.keyBodyLines(iw)
		m.keyScroll = clampScroll(m.keyScroll+d, len(body), m.bodyViewH())
	case phaseDashboard:
		m.dashScroll = clampScroll(m.dashScroll+d, len(m.dashBodyLines(iw)), m.dashScrollViewH(iw))
	case phaseSecurity:
		// The Security menu deliberately reuses dashScroll (see dashboard.go).
		m.dashScroll = clampScroll(m.dashScroll+d, len(m.securityBodyLines(iw)), m.secBodyViewH())
	case phaseTerminal:
		// The Files tab draws its OWN scroll region over the same phase (filesView), so
		// the workspace tab picks the region: scrolling must move what the user can see.
		if m.wsTab == wsFiles {
			if m.files != nil {
				m.files.scroll = clampScroll(m.files.scroll+d, len(m.filesBody()), m.filesListViewH())
			}
			break
		}
		// Wheel scrolls LOCAL scrollback (not forwarded to the remote app). Disabled
		// while on the alternate screen (vim/top own the screen) — see terminalScrollable.
		// Mouse click/drag forwarding to the remote app is still DEFERRED for 2a.
		if m.terminalScrollable() {
			m.termScrollBy(d)
		}
	}
	return m
}

// scrollGeom is the CURRENT screen's scroll-region geometry: the screen Y of the
// region's first row, its height, the body length, and the DRAWN (clamped) offset.
// ok=false when no scrollbar is on screen right now and there is nothing to grab: the
// form, a centered confirm modal or the FM context menu (both cover the region), a
// frozen alt-screen terminal, and phaseRun — whose bubbles viewport is hand-drawn by
// runView through plain contentLine, i.e. it has no scrollbar cell at all.
//
// It mirrors scrollBy's per-phase switch (each screen's own *ViewH / *BodyLines /
// *TopRow, no second mapping) so the bar's hit-test can never drift from the ↑↓/wheel
// scroll that moves it; the drag applies its result THROUGH scrollBy for the same
// reason. Sibling of scrollRowToBodyIdx.
func (m model) scrollGeom() (topRow, viewH, total, off int, ok bool) {
	// framedScrollView windows the body at the CLAMPED offset, so the geometry the user
	// sees — and therefore the hit-test — must use the same number.
	defer func() { off = clampScroll(off, total, viewH) }()

	iw := innerWidth(m.boxWidth())
	switch m.phase {
	case phaseSummary:
		return summaryBodyTopRow, m.summaryBodyViewH(), len(m.summaryBodyLines()), m.sumScroll, true
	case phaseWiki:
		return summaryBodyTopRow, m.wikiBodyViewH(), len(m.wikiBodyLines(iw)), m.wikiScroll, true
	case phaseMatrix:
		return summaryBodyTopRow, m.matrixBodyViewH(), len(m.matrixBodyLines(iw)), m.matScroll, true
	case phaseKey:
		body, _ := m.keyBodyLines(iw)
		return keyBodyTopRow, m.bodyViewH(), len(body), m.keyScroll, true
	case phaseDashboard:
		if m.dashApplyConfirm {
			return 0, 0, 0, 0, false
		}
		return m.dashScrollTopRow(iw), m.dashScrollViewH(iw), len(m.dashBodyLines(iw)), m.dashScroll, true
	case phaseSecurity:
		if m.secDangerConfirm {
			return 0, 0, 0, 0, false
		}
		// The Security menu deliberately reuses dashScroll (see dashboard.go).
		return summaryBodyTopRow, m.secBodyViewH(), len(m.securityBodyLines(iw)), m.dashScroll, true
	case phaseTerminal:
		if m.wsTab == wsFiles {
			if m.files == nil || m.files.menuOpen {
				return 0, 0, 0, 0, false
			}
			return filesListTopRow, m.filesListViewH(), len(m.filesBody()), m.files.scroll, true
		}
		if !m.terminalScrollable() {
			return 0, 0, 0, 0, false
		}
		_, rows := m.termContentSize()
		return summaryBodyTopRow, rows, m.termBodyLen(), m.termScroll, true
	}
	return 0, 0, 0, 0, false
}

// scrollbarClick handles a LEFT press at (x, y) and reports whether it landed on the
// current screen's scrollbar — the box's last column, inside the scroll region, while
// the body overflows. Pressing the thumb arms a drag; dragGrab remembers where INSIDE
// the thumb it was taken so the thumb does not jump under the pointer. Pressing the
// bare track jumps the thumb's top to that row and drags on from there (grab 0), which
// is how a track click behaves everywhere else.
func (m model) scrollbarClick(x, y int) (model, bool) {
	topRow, viewH, total, off, ok := m.scrollGeom()
	if !ok || x != m.boxWidth()-1 || total <= viewH {
		return m, false
	}
	row := y - topRow
	if row < 0 || row >= viewH {
		return m, false
	}
	start, end := scrollThumb(viewH, total, off)
	if row >= start && row < end {
		m.dragging, m.dragGrab = true, row-start
		return m, true
	}
	m.dragging, m.dragGrab = true, 0
	return m.scrollBy(offsetForThumbRow(row, viewH, total) - off), true
}

// scrollbarDragTo moves the offset so the dragged thumb's top follows the pointer at
// screen row y, honoring dragGrab. Rows outside the region are NOT rejected — dragging
// past either end clamps (scrollBy → clampScroll). Applying the result as a DELTA
// through scrollBy is deliberate: phaseTerminal must go through termScrollBy so a drag
// to the bottom re-arms termFollow.
func (m model) scrollbarDragTo(y int) model {
	topRow, viewH, total, off, ok := m.scrollGeom()
	if !ok || total <= viewH {
		return m
	}
	return m.scrollBy(offsetForThumbRow(y-topRow-m.dragGrab, viewH, total) - off)
}

// scrollRowToBodyIdx maps a screen Y to an index in a scrolled body region that starts
// at topRow, honoring the (clamped) scroll offset, or ok=false when Y lands in the
// chrome outside the region or past the end of the body. The SINGLE geometry source for
// every scrolled screen's hit-tests (Dashboard grid, Security menu, Summary), so a
// button hit-test can never drift from the ↑↓/wheel scroll that moved the row.
func scrollRowToBodyIdx(y, topRow, bodyLen, viewH, scroll int) (int, bool) {
	rowInRegion := y - topRow
	if rowInRegion < 0 || rowInRegion >= viewH {
		return 0, false
	}
	idx := clampScroll(scroll, bodyLen, viewH) + rowInRegion
	if idx < 0 || idx >= bodyLen {
		return 0, false
	}
	return idx, true
}

// renderScrollRegion emits exactly viewH box content rows showing body[off:off+viewH]
// (blank-padded when the body is shorter), so the caller's footer stays pinned. When
// the body overflows viewH it draws a proportional scrollbar in the RIGHT border —
// a bright thumb (█) over a dim track (│) whose size and position reflect viewH/total
// and off — so the user sees there is hidden content and where they are; when it all
// fits the plain border is drawn and there is no scrollbar. off is assumed already
// clamped (clampScroll).
func (m model) renderScrollRegion(sb *strings.Builder, b lipgloss.Border, body []string, innerW, viewH, off int) {
	total := len(body)
	overflow := total > viewH
	thumbStart, thumbEnd := scrollThumb(viewH, total, off)

	for i := range viewH {
		var line string
		if off+i < total {
			line = body[off+i]
		}
		right := borderStyle.Render(b.Right)
		if overflow {
			if i >= thumbStart && i < thumbEnd {
				right = borderStyle.Render("█") // thumb
			} else {
				right = monDimStyle.Render("│") // track
			}
		}
		sb.WriteString(contentLineR(b, line, innerW, right))
		sb.WriteByte('\n')
	}
}

// scrollThumb returns the scrollbar thumb extent, in region rows [start, end), for a
// viewH-row region windowing total body rows at (already clamped) offset off. An empty
// extent (0, 0) means the body fits and no bar is drawn. The SINGLE source of the thumb
// geometry for BOTH the drawn bar (renderScrollRegion) and the drag hit-test
// (scrollbarClick), so a grab can never miss the cell the user is looking at.
func scrollThumb(viewH, total, off int) (start, end int) {
	if viewH < 1 || total <= viewH {
		return 0, 0
	}
	thumb := max(viewH*viewH/total, 1) // proportion of content visible, ≥1 cell
	maxOff := total - viewH
	pos := 0
	if maxOff > 0 {
		pos = off * (viewH - thumb) / maxOff
	}
	if pos < 0 {
		pos = 0
	}
	if pos > viewH-thumb {
		pos = viewH - thumb
	}
	return pos, pos + thumb
}

// offsetForThumbRow is scrollThumb's inverse: the scroll offset that puts the thumb's
// TOP at region row. row may be out of range (a drag past either end) — the result is
// then out of range too and the caller's clampScroll pins it, which is exactly how a
// scrollbar should behave. A one-row region (or a thumb that fills the track) has no
// travel to map, so it yields 0.
//
// The division ROUNDS rather than truncating: scrollThumb floors on the way out, so a
// truncating inverse would floor twice and the thumb could settle one row above the
// grabbed cell — visibly slipping out from under the pointer mid-drag.
func offsetForThumbRow(row, viewH, total int) int {
	if viewH < 1 || total <= viewH {
		return 0
	}
	thumb := max(viewH*viewH/total, 1)
	span := viewH - thumb
	if span < 1 {
		return 0
	}
	return (row*(total-viewH) + span/2) / span
}

// truncDisplay truncates s to at most w display cells (ANSI/Unicode-safe). w<=0
// returns "".
func truncDisplay(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}
