package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/version"
)

// runView hand-draws the titled main box (progress + viewport + hints) and the
// bottom monitor box, sized to the terminal and budgeted so nothing overflows.
func (m model) runView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	var sb strings.Builder

	// --- MAIN BOX ---
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')

	// First content line (screen row 1): the RU/EN switcher, right-aligned — so the
	// language is switchable during a run too, and the click hit-test row matches.
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// Progress / summary line.
	sb.WriteString(contentLine(b, m.progressLine(innerW), innerW))
	sb.WriteByte('\n')

	// Blank spacer line.
	sb.WriteString(contentLine(b, "", innerW))
	sb.WriteByte('\n')

	// Viewport (server output). Each rendered line padded/truncated to innerW.
	for ln := range strings.SplitSeq(m.vp.View(), "\n") {
		sb.WriteString(contentLine(b, ln, innerW))
		sb.WriteByte('\n')
	}

	// When finished, the localized completion tail rendered fresh from m.lang each
	// frame (NOT baked into m.content) so a post-finish language switch re-renders
	// it, then an explicit highlighted "Back to main" button above the hints.
	if m.finished {
		for ln := range strings.SplitSeq(m.finishedTail(), "\n") {
			sb.WriteString(contentLine(b, ln, innerW))
			sb.WriteByte('\n')
		}
		sb.WriteString(contentLine(b, pillOnStyle.Render(t(m.lang, kBackToMain)), innerW))
		sb.WriteByte('\n')
	}

	// Contextual hints.
	sb.WriteString(contentLine(b, helpStyle.Render(m.runHints()), innerW))
	sb.WriteByte('\n')

	// Main box bottom border.
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	// --- MONITOR BOX (bottom-most) --- shared with the summary + wiki screens.
	sb.WriteString(m.monitorBox(innerW))

	return sb.String()
}

// monitorBox renders the bottom-most live monitor box (title + the full-width
// CPU/RAM/DISK footer): top border, one content line, bottom border. Shared by the
// run, summary, and wiki screens (every post-connect screen keeps the footer alive).
// No trailing newline — it is the last block on the screen.
func (m model) monitorBox(innerW int) string {
	bw := m.boxWidth()
	b := lipgloss.RoundedBorder()
	var sb strings.Builder
	sb.WriteString(titledTop(b, t(m.lang, kMonTitle), bw))
	sb.WriteByte('\n')
	sb.WriteString(contentLine(b, m.renderMonitor(innerW), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	return sb.String()
}

// finishedTail returns the localized completion banner shown below the viewport
// once the run ends: a success line, or an error line carrying the engine's error
// text. Rendered fresh from m.lang each frame (see runView) rather than baked into
// the frozen m.content, so it re-translates on a post-finish language switch. The
// quit/back hint is intentionally omitted here — runHints already shows it.
func (m model) finishedTail() string {
	if m.finalErr != nil {
		return "❌ " + t(m.lang, kFinishedErr) + m.finalErr.Error()
	}
	// Successful run → repeat the mode-aware SSH-login notice so the last thing the
	// operator sees is how to reconnect (strict: key-only; soft: password OR key).
	// Only a full `run` touches auth policy; detect/verify leave it untouched.
	tail := "✅ " + t(m.lang, kFinishedOK)
	// Internet benchmark (Feature G): only when A4 produced a comparable PRE→POST
	// pair (BenchOK); omitted cleanly for detect/verify or a skipped/no-sample A4.
	if m.haveSummary && m.summary.BenchOK {
		tail += "\n" + fmt.Sprintf(t(m.lang, kBenchLine),
			m.summary.BenchPreMBs, m.summary.BenchPostMBs, m.summary.BenchRatio)
	}
	// Skip reasons (Feature F): list WHY each step was skipped, not just a count.
	if m.haveSummary && len(m.summary.Skips) > 0 {
		tail += "\n" + t(m.lang, kSkipsHeader)
		for _, sk := range m.summary.Skips {
			tail += "\n  " + fmt.Sprintf(t(m.lang, kSkipLine), sk.ID, sk.Reason)
		}
	}
	if m.command == "run" {
		tail += "\n" + t(m.lang, kPwOnInfo)
	}
	return tail
}

// finishedTailRows is the number of content rows finishedTail occupies; runView
// emits one box line per "\n"-split segment, so vpHeight must reserve the same.
func (m model) finishedTailRows() int {
	if !m.finished {
		return 0
	}
	return strings.Count(m.finishedTail(), "\n") + 1
}

// runHints returns the contextual hint line: while running, auto-follow overrides
// manual scroll so only quit is shown; once finished/idle, enter/esc back + scroll + quit.
func (m model) runHints() string {
	if m.running && !m.finished {
		return t(m.lang, kRunHintRunning)
	}
	return t(m.lang, kRunHintIdle)
}

// progressLine renders the top progress line: a step counter + bar + percent +
// current step name while running, a summary once finished, or an action label
// when there is no step list (detect/verify pre-finish).
func (m model) progressLine(innerW int) string {
	switch {
	case m.haveSummary:
		return m.summaryLine(innerW)
	case m.total > 0:
		return m.barLine(innerW)
	default:
		// No step list yet (before the first step). Still show the live spinner +
		// elapsed timer so the view never looks frozen.
		label := strings.TrimSpace(m.inputs[fHost].Value())
		if p := m.livePrefix(); p != "" {
			label = p + label
		}
		return truncDisplay(label, innerW)
	}
}

// livePrefix returns "⠙ 1m23s · " (spinner + running elapsed) while the run is in
// flight, or "" once finished — used to keep the top progress line visibly alive.
func (m model) livePrefix() string {
	if !m.running || m.finished {
		return ""
	}
	return fmt.Sprintf("%c %s · ", spinnerFrames[m.spin], fmtElapsed(m.elapsed))
}

// fmtElapsed renders a duration compactly: "45s" under a minute, else "2m05s".
func fmtElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// barLine builds "Step N/M [bar] PP%  <name>" so the ENTIRE line (including the live
// "⠙ 1m23s · " prefix and the localized "Шаг/Step" word) is clamped to innerW. The
// step name is the flexible part: it is the localized SHORT title (RU when langRU),
// truncated with an ellipsis to whatever width is left after the counter+bar+percent,
// so it never crosses the border. The bar is capped so a reasonable name (≥12 cells,
// budget permitting) always fits beside it. All widths use lipgloss.Width (display
// cells, multibyte-safe), not len.
func (m model) barLine(innerW int) string {
	left := m.livePrefix() + fmt.Sprintf("%s %d/%d ", t(m.lang, kStepN), m.index, m.total)
	pct := 0
	if m.total > 0 {
		pct = m.index * 100 / m.total
	}
	pctStr := fmt.Sprintf("%3d%%", pct) // up to "100%"

	// localized short title — RU when the UI is Russian — with the engine Title as
	// the fallback for any unmapped ID.
	title := localStepTitle(m.lang, m.curID, m.curTitle)

	// Fixed (non-name, non-bar) cells: the counter prefix + " PPP%" + the two gaps
	// (one before the percent, two before the name) — measured in display cells.
	const gapBeforePct = 1  // " " between bar and percent
	const gapBeforeName = 2 // "  " between percent and name
	fixed := lipgloss.Width(left) + gapBeforePct + lipgloss.Width(pctStr) + gapBeforeName

	// Width left for bar + name together. Cap the bar so a reasonable name still fits.
	const (
		maxBarW     = 24 // don't let the bar hog the whole line
		minNameW    = 12 // try to keep room for at least this many name cells
		minBarW     = 3  // below this the bar reads as noise — drop it
		minNameSlot = 1
	)
	avail := max(innerW-fixed, 0)

	// Give the bar up to maxBarW, but never so much that the name slot drops below
	// minNameW (until the line is simply too narrow to honor both).
	barW := maxBarW
	barW = min(barW, avail-minNameW)
	barW = min(barW, avail)

	if barW < minBarW {
		// Too tight for a bar — drop it, keep counter + percent + truncated name.
		nameW := innerW - lipgloss.Width(left) - gapBeforePct - lipgloss.Width(pctStr) - gapBeforeName
		name := truncDisplay(title, maxi(nameW, 0))
		line := left + pctStr
		if name != "" {
			line += "  " + name
		}
		return truncDisplay(line, innerW)
	}

	nameW := max(avail-barW, minNameSlot)
	name := truncDisplay(title, nameW)

	filled := min(max(pct*barW/100, 0), barW)
	bar := monGreenStyle.Render(strings.Repeat("█", filled)) +
		monDimStyle.Render(strings.Repeat("░", barW-filled))
	out := left + bar + " " + pctStr
	if name != "" {
		out += "  " + name
	}
	// Final hard clamp (defensive): the math above keeps it within innerW, but a
	// multibyte rounding edge must never cross the border.
	return truncDisplay(out, innerW)
}

// summaryLine renders the finished-run summary that replaces the progress bar.
func (m model) summaryLine(innerW int) string {
	s := m.summary
	mark := "✓"
	if s.Fail > 0 {
		mark = "✗"
	}
	verifyTotal := s.VerifyPassed + s.VerifyFailed
	line := fmt.Sprintf("%s %s · %d OK · %d SKIP · %d FAIL · %s · %s %d/%d",
		mark, t(m.lang, kDoneWord), s.OK, s.Skip, s.Fail, s.Elapsed.Round(time.Second),
		t(m.lang, kVerifyTag), s.VerifyPassed, verifyTotal)
	return truncDisplay(line, innerW)
}
