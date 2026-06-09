package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/UberMorgott/morgward/internal/ui"
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
		w = maxi(innerWidth(m.w), 1)
	}
	return lipgloss.NewStyle().Width(w).Render(m.content)
}

// vpWidth/vpHeight compute the bounded inner viewport size for the run-phase box
// so the log never overflows the box or overlaps the contextual hints.
func (m model) vpWidth() int { return maxi(innerWidth(m.w), 1) }

func (m model) vpHeight() int {
	// main chrome: top border + switcher + progress + blank + hints + bottom = 6
	// monitor box: top border + content + bottom = 3
	base := m.h - 6 - 3
	if m.finished {
		base-- // reserve a row for the "Back to main" button line
		base -= m.finishedTailRows()
	}
	return maxi(base, 3)
}

// minBoxWidth clamps the box width so the hand-drawn border never goes negative.
const minBoxWidth = 40

// boxWidth is the outer width of both boxes (the full terminal width, clamped).
func (m model) boxWidth() int { return maxi(m.w, minBoxWidth) }

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

// bodyViewH is the height (in rows) of the scrollable middle region on the summary
// and wiki screens: the terminal height minus the fixed chrome — top border +
// switcher (2) + hint + main-box bottom (2) + the 3-row monitor box = 7 — floored at
// 1 so the region never vanishes on a tiny terminal.
func (m model) bodyViewH() int { return max(m.h-7, 1) }

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

	// Thumb extent in region rows [thumbStart, thumbEnd).
	thumbStart, thumbEnd := 0, 0
	if overflow {
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
		thumbStart, thumbEnd = pos, pos+thumb
	}

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
