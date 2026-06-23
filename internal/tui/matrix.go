package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/tweaks"
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

	viewH := m.matrixBodyViewH()
	off := clampScroll(m.matScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	// Pinned clickable "← Назад" pill row (mirrors summaryView's pinned home button):
	// one fixed row reserved below the scroll region, so the footer never moves and the
	// pill never scrolls away. matrixBackRow / matrixBackAtClick share this geometry.
	sb.WriteString(contentLine(b, pillStyle.Render(t(m.lang, kWikiBack)), innerW))
	sb.WriteByte('\n')

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

// matrixBodyViewH is the height of the scrollable анализ region: the shared bodyViewH
// minus the one fixed row reserved for the pinned "← Назад" pill, floored at 1. Used
// by BOTH matrixView's render and matrixRowAtClick so the row geometry never drifts.
func (m model) matrixBodyViewH() int { return max(m.bodyViewH()-1, 1) }

// matrixBackRow is the FIXED screen Y of the pinned "← Назад" pill: it follows the 2
// chrome rows and the scroll region, so it never moves with the scroll offset.
func (m model) matrixBackRow() int { return summaryBodyTopRow + m.matrixBodyViewH() }

// matrixBackAtClick reports whether (x,y) hit the pinned "← Назад" pill. X spans the
// rendered pill width from the content column (pillStyle adds Padding(0,1), so the pill
// is wider than the bare label); the pillRanges geometry matches the render exactly.
func (m model) matrixBackAtClick(x, y int) bool {
	if m.phase != phaseMatrix || y != m.matrixBackRow() {
		return false
	}
	return pillIndexAt([]string{t(m.lang, kWikiBack)}, wikiBackStartCol, x) == 0
}

// matrixRowAtClick maps a click at (x,y) to the tweaks.Result whose row was hit, or
// ok=false on a miss. It inverts the screen Y to a body index via the SAME scroll
// geometry matrixView uses, then REPLAYS matrixBodyLines' structure (skip line0 header,
// line1 blank, and the interleaved per-step title separators) to recover the index into
// m.summary.Tweaks. A hit requires X within the rendered row text width.
func (m model) matrixRowAtClick(x, y int) (tweaks.Result, bool) {
	res := m.summary.Tweaks
	if m.phase != phaseMatrix || len(res) == 0 {
		return tweaks.Result{}, false
	}
	innerW := innerWidth(m.boxWidth())
	body := m.matrixBodyLines(innerW)
	viewH := m.matrixBodyViewH()
	off := clampScroll(m.matScroll, len(body), viewH)
	rowInRegion := y - summaryBodyTopRow
	if rowInRegion < 0 || rowInRegion >= viewH {
		return tweaks.Result{}, false
	}
	bodyIdx := off + rowInRegion
	// Body layout: [0]=header, [1]=blank, then per-step a title separator line (no
	// backing result) followed by one row per result in res order. Replay the same walk
	// to map bodyIdx → result index; bail if bodyIdx lands on a non-row line.
	idx := 2 // first content line after header + blank
	var curStep string
	for i := range res {
		if res[i].Probe.Step != curStep {
			curStep = res[i].Probe.Step
			idx++ // the title separator for this step
		}
		if idx == bodyIdx {
			// X must fall within this row's rendered text width.
			name := localTweakName(m.lang, res[i].Probe.ID, res[i].Probe.Name)
			const contentX0 = 2
			w := lipgloss.Width("  " + name)
			if x >= contentX0 && x < contentX0+w {
				return res[i], true
			}
			return tweaks.Result{}, false
		}
		idx++ // this result's row
	}
	return tweaks.Result{}, false
}

// matrixClick resolves a phaseMatrix click: the pinned "← Назад" pill returns home; a
// tweak row opens its wiki detail (wikiReturn=phaseMatrix, so Esc comes back here).
func (m model) matrixClick(x, y int) (tea.Model, tea.Cmd) {
	if m.matrixBackAtClick(x, y) {
		return m.goBack()
	}
	if r, ok := m.matrixRowAtClick(x, y); ok {
		m.wikiStep = r.Probe.Step
		m.wikiTweak = tweakWikiHeader(m.lang, r.Probe)
		m.wikiProbeID = r.Probe.ID
		m.wikiReturn = phaseMatrix
		m.wikiScroll = 0
		m.wikiUpdateConfirm = false
		m.phase = phaseWiki
		return m, nil
	}
	return m, nil
}
