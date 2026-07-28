package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// matrixView renders phaseMatrix: the scrollable анализ table with the monitor
// footer, mirroring summaryView's chrome exactly (same outer titled box, switcher
// line, scroll region, hint line, bottom border, and monitor footer).
func (m model) matrixView() string {
	// The pinned "← Назад" pill (mirrors summaryView's pinned home button) is one fixed
	// row reserved below the scroll region, so the footer never moves and the pill never
	// scrolls away. matrixBackRow / matrixBackAtClick share this geometry.
	return m.framedScrollView(frame{
		title:  frameTitle(),
		nav:    m.switcherLine,
		body:   m.matrixBodyLines(innerWidth(m.boxWidth())),
		viewH:  m.matrixBodyViewH(),
		scroll: m.matScroll,
		pinned: []string{pillStyle.Render(t(m.lang, kWikiBack))},
		hint:   helpStyle.Render(t(m.lang, kMatrixHint)),
	})
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

// matrixBodyViewH is the height of the scrollable анализ region: the shared chrome
// minus the one fixed row reserved for the pinned "← Назад" pill. Used by BOTH
// matrixView's render and matrixRowAtClick so the row geometry never drifts.
func (m model) matrixBodyViewH() int { return m.chromeViewH(0, 1) }

// matrixBackRow is the FIXED screen Y of the pinned "← Назад" pill: it follows the 2
// chrome rows and the scroll region, so it never moves with the scroll offset.
func (m model) matrixBackRow() int { return summaryBodyTopRow + m.matrixBodyViewH() }

// matrixLayers returns the phaseMatrix click targets: the pinned "← Назад" pill (whose
// pillRanges geometry matches the render, pillStyle's Padding(0,1) included) and one
// target per tweak row currently inside the scroll region.
//
// It REPLAYS matrixBodyLines' structure — [0]=header, [1]=blank, then per step a title
// separator line with no backing result followed by one row per result in res order — to
// place each result at its body index, then shifts by the SAME clamped scroll offset
// matrixView renders with. A row scrolled outside the region gets no layer.
func (m model) matrixLayers() []*lipgloss.Layer {
	ls := []*lipgloss.Layer{
		pillLayer(idMatrixBack, []string{t(m.lang, kWikiBack)}, wikiBackStartCol, 0, m.matrixBackRow(), 2),
	}
	res := m.summary.Tweaks
	if len(res) == 0 {
		return ls
	}
	viewH := m.matrixBodyViewH()
	off := clampScroll(m.matScroll, len(m.matrixBodyLines(innerWidth(m.boxWidth()))), viewH)

	idx := 2 // first content line after the header + blank
	var curStep string
	for i := range res {
		if res[i].Probe.Step != curStep {
			curStep = res[i].Probe.Step
			idx++ // the title separator for this step
		}
		if row := idx - off; row >= 0 && row < viewH {
			w := lipgloss.Width("  " + localTweakName(m.lang, res[i].Probe.ID, res[i].Probe.Name))
			ls = append(ls, hitLayer(idMatrixRowPfx+strconv.Itoa(i), contentX0, summaryBodyTopRow+row, w, 1))
		}
		idx++ // this result's row
	}
	return ls
}

// matrixClick resolves a phaseMatrix click: the pinned "← Назад" pill returns home; a
// tweak row opens its wiki detail (wikiReturn=phaseMatrix, so Esc comes back here).
func (m model) matrixClick(x, y int) (tea.Model, tea.Cmd) {
	id := m.hit(x, y)
	if id == idMatrixBack {
		return m.goBack()
	}
	// The row id carries its index into m.summary.Tweaks; the bounds check guards the
	// string round-trip the layer-id API forces on an otherwise typed lookup.
	rest, ok := strings.CutPrefix(id, idMatrixRowPfx)
	if !ok {
		return m, nil
	}
	i, err := strconv.Atoi(rest)
	if err != nil || i < 0 || i >= len(m.summary.Tweaks) {
		return m, nil
	}
	r := m.summary.Tweaks[i]
	m.wikiStep = r.Probe.Step
	m.wikiTweak = tweakWikiHeader(m.lang, r.Probe)
	m.wikiProbeID = r.Probe.ID
	m.wikiReturn = phaseMatrix
	m.wikiScroll = 0
	m.wikiUpdateConfirm = false
	m.phase = phaseWiki
	return m, nil
}
