package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/version"
)

// matrixView renders phaseMatrix: the scrollable анализ table with the monitor
// footer, mirroring summaryView's chrome exactly (same outer titled box, switcher
// line, scroll region, hint line, bottom border, and monitor footer).
func (m model) matrixView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body := m.matrixBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	viewH := m.bodyViewH()
	off := clampScroll(m.matScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, kMatrixHint)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// matrixBodyLines renders the анализ audit: results grouped by step, each row
// "  <name> <leaders> <status>" right-aligned to innerW. Mirrors summaryBodyLines.
func (m model) matrixBodyLines(innerW int) []string {
	res := m.summary.Tweaks
	if len(res) == 0 {
		return []string{t(m.lang, kMatrixHint)}
	}

	applied := 0
	for _, r := range res {
		if r.Applied {
			applied++
		}
	}

	okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	noStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	var lines []string
	lines = append(lines, fmt.Sprintf(t(m.lang, kTweakSummary), applied, len(res)-applied), "")

	var curStep string
	for _, r := range res {
		if r.Probe.Step != curStep {
			curStep = r.Probe.Step
			lines = append(lines, localStepTitle(m.lang, curStep, curStep))
		}
		name := localTweakName(m.lang, r.Probe.ID, r.Probe.Name)
		statusTxt := t(m.lang, kTweakNotApplied)
		style := noStyle
		if r.Applied {
			statusTxt = t(m.lang, kTweakApplied)
			style = okStyle
		}
		// "  name <spaces> status" padded to innerW display cells.
		left := "  " + name
		gap := max(innerW-lipgloss.Width(left)-lipgloss.Width(statusTxt), 1)
		lines = append(lines, left+strings.Repeat(" ", gap)+style.Render(statusTxt))
	}
	return lines
}
