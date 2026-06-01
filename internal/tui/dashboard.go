package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/tweaks"
	"github.com/UberMorgott/morgward/internal/version"
)

// --- Dashboard (phaseDashboard) -----------------------------------------------
//
// dashboardView renders the post-connect Dashboard: a framed server card
// (OS/kernel/RAM/disk/ports/IPv6 from m.dashFacts + the live monitor sample), the
// live tweak-audit status line + a ✓/• grid (from m.dashAuditResults), and two
// action button pills. The monitor footer is pinned at the bottom. Chrome (titled
// top, switcher, scroll region, hint, bottom border, monitor box) mirrors
// summaryView/matrixView exactly so the footer never moves and the body scrolls.
//
// The clickable rows (audit grid rows → wiki detail, the three button pills) are
// resolved against the SAME ordered body slice the renderer iterates
// (dashBodyLines), so the hit-test geometry can never drift.
func (m model) dashboardView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body := m.dashBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	viewH := m.bodyViewH()
	off := clampScroll(m.dashScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	hint := t(m.lang, kDashHint)
	if m.dashApplyConfirm {
		hint = t(m.lang, kDashApplyConfirm)
	}
	sb.WriteString(contentLine(b, helpStyle.Render(hint), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// dashButtonNames is the ordered list of the two Dashboard action-button labels
// (Apply / Security). It is the SINGLE source consumed by both the render path
// (dashButtonsLine) and the hit-test (dashButtonAtClick), so their x-geometry
// cannot diverge.
func (m model) dashButtonNames() []string {
	return []string{
		t(m.lang, kDashApplyButton),
		t(m.lang, kDashSecButton),
	}
}

// dashButtonStartCol is the absolute X where the first button pill begins on the
// buttons row: 2 (left border + space) + 1 (the leading indent space in the row).
const dashButtonStartCol = 3

// dashButtonsLine renders the two action pills joined by a single space, with a
// one-space indent so the first pill begins at dashButtonStartCol. Both use
// the dim pill style; pillRanges over dashButtonNames recovers their x-geometry.
func (m model) dashButtonsLine() string {
	names := m.dashButtonNames()
	pills := make([]string, len(names))
	for i, n := range names {
		pills[i] = pillStyle.Render(n)
	}
	return " " + strings.Join(pills, " ")
}

// dashBodyLines builds the ordered Dashboard body slice — the single source of
// truth for BOTH dashboardView's render and the hit-tests (dashAuditRowAtClick /
// dashButtonRowIndex). Order: server card (framed), blank, audit status line,
// blank, audit grid lines (ceil(N/cols), see dashAuditGridLines), blank, buttons
// line. Every width uses lipgloss.Width.
func (m model) dashBodyLines(innerW int) []string {
	var body []string
	body = append(body, m.dashServerCard(innerW)...)
	body = append(body, "")

	// Live audit status line: "Анализ твиков ⠹  применено N из M · можно применить K".
	applied, total := m.dashAuditApplied, m.dashAuditTotal
	canApply := max(total-applied, 0)
	label := t(m.lang, kDashAuditLabel)
	if m.dashAuditRunning && !m.dashAuditDone {
		label += " " + string(spinnerFrames[m.spin%len(spinnerFrames)])
	}
	status := fmt.Sprintf("%s  %s · %s",
		label,
		fmt.Sprintf(t(m.lang, kDashAuditStatus), applied, total),
		fmt.Sprintf(t(m.lang, kDashCanApply), canApply),
	)
	body = append(body, truncDisplay(status, innerW))
	body = append(body, "")

	// Audit grid: a fluid 1- or 2-column grid of "  ✓/•  name" cells. The grid lines
	// are the clickable tail used by dashAuditRowAtClick (each cell maps to its
	// Probe.ID → wiki detail). dashAuditGridLines is the single source of truth for
	// the column geometry so render and hit-test cannot drift.
	body = append(body, m.dashAuditGridLines(innerW)...)

	body = append(body, "")
	body = append(body, m.dashButtonsLine())
	return body
}

// dashColGap is the number of spaces between the two audit columns.
const dashColGap = 2

// minDashColWidth is the smallest per-column cell width that still fits a useful
// "  ✓  name". Below this (each of the two columns would be narrower) the grid
// falls back to a single full-width column. Picked so a normal 80-col terminal
// (innerW ≈ 76) yields colWidth ≈ 37 → 2 columns, while a tiny terminal gets 1.
const minDashColWidth = 24

// dashAuditCols reports the audit grid column layout for the given content width:
// the number of columns (1 or 2) and each column's cell width. Two columns only
// when each would be at least minDashColWidth wide; otherwise one full-width
// column. The SINGLE source of truth shared by dashAuditGridLines (render) and
// dashAuditRowAtClick (hit-test), so their geometry can never drift.
func dashAuditCols(innerW int) (cols, colWidth int) {
	two := (innerW - dashColGap) / 2
	if two >= minDashColWidth {
		return 2, two
	}
	return 1, max(innerW, 1)
}

// dashAuditNumGridLines is the number of audit body lines actually emitted:
// ceil(N/cols) in 2-col mode, N in 1-col mode (N = number of display results).
func (m model) dashAuditNumGridLines(innerW int) int {
	n := len(m.dashAuditResults)
	if n == 0 {
		return 0
	}
	cols, _ := dashAuditCols(innerW)
	return (n + cols - 1) / cols
}

// dashAuditCellText is the PLAIN (uncolored) cell text "  ✓/•  name" truncated to
// colWidth — the single source for the cell's rendered display width, used by the
// hit-test (lipgloss.Width over this) so a click only lands within the real text.
func (m model) dashAuditCellText(r tweaks.Result, colWidth int) string {
	glyph := "•"
	if r.Applied {
		glyph = "✓"
	}
	name := localTweakName(m.lang, r.Probe.ID, r.Probe.Name)
	return truncDisplay("  "+glyph+" "+name, colWidth)
}

// dashAuditCell renders one COLORED audit cell, then right-pads (with plain spaces)
// to colWidth display cells so the next column starts at a fixed X. The glyph color
// does not change the display width — the pad is computed from the plain text.
func (m model) dashAuditCell(r tweaks.Result, colWidth int) string {
	glyph := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("•")
	if r.Applied {
		glyph = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("✓")
	}
	name := localTweakName(m.lang, r.Probe.ID, r.Probe.Name)
	colored := truncDisplay("  "+glyph+" "+name, colWidth)
	if pad := colWidth - lipgloss.Width(colored); pad > 0 {
		colored += strings.Repeat(" ", pad)
	}
	return colored
}

// dashAuditGridLines builds the fluid 1- or 2-column audit grid as body lines.
// Row-major pairing: grid line r holds results[cols*r .. cols*r+cols-1]. The left
// cell is padded to colWidth so the right column begins at a fixed X. In 1-column
// mode the (single) cell is rendered to the full innerW with no trailing pad.
func (m model) dashAuditGridLines(innerW int) []string {
	results := m.dashAuditResults
	cols, colWidth := dashAuditCols(innerW)
	lines := make([]string, 0, (len(results)+cols-1)/cols)
	for i := 0; i < len(results); i += cols {
		line := m.dashAuditCell(results[i], colWidth)
		if cols == 2 && i+1 < len(results) {
			line += strings.Repeat(" ", dashColGap) + m.dashAuditCell(results[i+1], colWidth)
		}
		lines = append(lines, line)
	}
	return lines
}

// dashServerCard renders the framed "Сервер: HOST" card with the OS/kernel/RAM/
// disk/ports/IPv6 facts (from m.dashFacts + the live monitor sample), as content
// lines fitted to innerW. Missing facts are omitted, never rendered as blanks.
func (m model) dashServerCard(innerW int) []string {
	bd := lipgloss.RoundedBorder()
	fw := max(innerW, minBoxWidth)
	finner := fw - 2 // cells between the card's border runes

	title := " " + t(m.lang, kDashTitle) + ": " + m.host + " "
	top := titledTop(bd, title, fw)
	bottom := borderLine(bd.BottomLeft, bd.Bottom, bd.BottomRight, fw)

	// Build the facts line(s) from whatever is known. Order mirrors the mockup.
	var parts []string
	f := m.dashFacts
	if f != nil {
		if f.ID != "" {
			osStr := f.ID
			if f.VersionID != "" {
				osStr += " " + f.VersionID
			}
			parts = append(parts, t(m.lang, kDashOS)+" "+osStr)
		}
		if f.Kernel != "" {
			parts = append(parts, t(m.lang, kDashKernel)+" "+f.Kernel)
		}
		if f.Virt != "" && f.Virt != "none" {
			parts = append(parts, t(m.lang, kDashVirt)+" "+f.Virt)
		}
		if f.HasIPv6 {
			parts = append(parts, t(m.lang, kDashIPv6)+" "+m.boolWordL(true))
		}
	}
	// RAM/disk from the live monitor sample (best-effort; the sampler fills them
	// once connected). The sample is the same source the footer uses.
	s := m.sample
	if m.haveSample {
		if s.RAMTotalKB > 0 {
			parts = append(parts, t(m.lang, kDashMemory)+" "+humanKB(s.RAMUsedKB)+"/"+humanKB(s.RAMTotalKB))
		}
		if s.DiskTotalKB > 0 {
			parts = append(parts, t(m.lang, kDashDisk)+" "+humanKB(s.DiskUsedKB)+"/"+humanKB(s.DiskTotalKB))
		}
	}

	mid := func(content string) string {
		content = " " + content
		content = truncDisplay(content, finner)
		if pad := finner - lipgloss.Width(content); pad > 0 {
			content += strings.Repeat(" ", pad)
		}
		return borderStyle.Render(bd.Left) + content + borderStyle.Render(bd.Right)
	}

	// One fact per card line so nothing is hidden on a narrow width. dashGridStartIndex
	// uses len(card) dynamically, so a taller card stays hit-test-correct.
	lines := []string{top}
	if len(parts) == 0 {
		lines = append(lines, mid(labelStyle.Render("…")))
	} else {
		for _, p := range parts {
			lines = append(lines, mid(p))
		}
	}
	lines = append(lines, bottom)
	return lines
}

// dashGridStartIndex is the body-slice index of the FIRST audit grid line. The body
// prefix is: server card (N lines) + blank + status + blank, so the grid begins at
// len(card)+3. dashButtonsIndex is the body index of the buttons row: grid start +
// the number of audit grid lines actually emitted (ceil(N/cols)) + 1 (the blank
// before the buttons line) — derived from the SAME column logic the renderer uses.
func (m model) dashGridStartIndex(innerW int) int {
	return len(m.dashServerCard(innerW)) + 3
}

func (m model) dashButtonsIndex(innerW int) int {
	return m.dashGridStartIndex(innerW) + m.dashAuditNumGridLines(innerW) + 1
}

// dashRowYToBodyIdx maps a screen Y to a Dashboard body-slice index, honoring the
// scroll offset, or returns ok=false when Y is in the chrome (switcher/hint/border/
// monitor) rather than the scrollable body region.
func (m model) dashRowYToBodyIdx(y int) (int, bool) {
	body := m.dashBodyLines(innerWidth(m.boxWidth()))
	viewH := m.bodyViewH()
	off := clampScroll(m.dashScroll, len(body), viewH)
	rowInRegion := y - summaryBodyTopRow
	if rowInRegion < 0 || rowInRegion >= viewH {
		return 0, false
	}
	idx := off + rowInRegion
	if idx < 0 || idx >= len(body) {
		return 0, false
	}
	return idx, true
}

// dashAuditRowAtClick maps a click at (x,y) to the audit Result whose cell was hit,
// or ok=false on a miss. It mirrors dashAuditGridLines EXACTLY: bodyIdx → grid row,
// then X → left/right column (compared against the fixed column boundary), then
// resIdx = cols*gridRow + col. Bounds-checked against the result count, and a hit
// is returned only when X falls within that cell's rendered text width.
func (m model) dashAuditRowAtClick(x, y int) (tweaks.Result, bool) {
	if m.phase != phaseDashboard || len(m.dashAuditResults) == 0 {
		return tweaks.Result{}, false
	}
	innerW := innerWidth(m.boxWidth())
	bodyIdx, ok := m.dashRowYToBodyIdx(y)
	if !ok {
		return tweaks.Result{}, false
	}
	gridRow := bodyIdx - m.dashGridStartIndex(innerW)
	if gridRow < 0 || gridRow >= m.dashAuditNumGridLines(innerW) {
		return tweaks.Result{}, false
	}
	cols, colWidth := dashAuditCols(innerW)

	// Content begins at absolute X=2 (left border + space). The left cell spans
	// [contentX0, contentX0+colWidth); after the gap the right cell begins at
	// rightX0 and spans colWidth. Resolve which column X landed in.
	const contentX0 = 2
	rightX0 := contentX0 + colWidth + dashColGap
	col := 0
	if cols == 2 && x >= rightX0 {
		col = 1
	}
	cellX0 := contentX0
	if col == 1 {
		cellX0 = rightX0
	}

	resIdx := cols*gridRow + col
	if resIdx < 0 || resIdx >= len(m.dashAuditResults) {
		return tweaks.Result{}, false
	}
	// X must fall within this cell's actual rendered text width (cells are padded to
	// colWidth, so only the non-pad prefix is hittable).
	r := m.dashAuditResults[resIdx]
	w := lipgloss.Width(m.dashAuditCellText(r, colWidth))
	if x >= cellX0 && x < cellX0+w {
		return r, true
	}
	return tweaks.Result{}, false
}

// tweakWikiHeader is the wiki page header for a tweak opened from the Dashboard:
// "[<id>] <localized name>". Pure function so it is testable without driving the TUI.
func tweakWikiHeader(lang Lang, p tweaks.Probe) string {
	return fmt.Sprintf("[%s] %s", p.ID, localTweakName(lang, p.ID, p.Name))
}

// dashButton enumerates the two Dashboard actions resolved by dashButtonAtClick.
type dashButton int

const (
	dashBtnNone dashButton = iota
	dashBtnApply
	dashBtnSecurity
)

// dashButtonAtClick maps a click at (x,y) to one of the two action buttons, using
// pillRanges over dashButtonNames (the same geometry dashButtonsLine renders), or
// dashBtnNone on a miss.
func (m model) dashButtonAtClick(x, y int) dashButton {
	if m.phase != phaseDashboard {
		return dashBtnNone
	}
	innerW := innerWidth(m.boxWidth())
	bodyIdx, ok := m.dashRowYToBodyIdx(y)
	if !ok || bodyIdx != m.dashButtonsIndex(innerW) {
		return dashBtnNone
	}
	switch pillIndexAt(m.dashButtonNames(), dashButtonStartCol, x) {
	case 0:
		return dashBtnApply
	case 1:
		return dashBtnSecurity
	}
	return dashBtnNone
}

// dashboardClick resolves a Dashboard click: an audit row opens its wiki detail; a
// button pill triggers its action. "Применить твики" first shows the A8 reboot
// warning (Enter to confirm). "Безопасность ▸" navigates to the security menu
// (phaseSecurity).
func (m model) dashboardClick(x, y int) (tea.Model, tea.Cmd) {
	// A pending apply-confirm swallows clicks (use Enter/esc on the hint to resolve).
	if m.dashApplyConfirm {
		return m, nil
	}
	// Button pills take priority over the (overlapping-Y-impossible) audit grid.
	switch m.dashButtonAtClick(x, y) {
	case dashBtnApply:
		ids := tweakBucketIDs()
		if bucketHasA8(ids) {
			// Show the explicit reboot warning; the apply launches on Enter (see the
			// phaseDashboard key handler), not on this click.
			m.dashApplyConfirm = true
			return m, nil
		}
		return m.launchApplyTweaks()
	case dashBtnSecurity:
		// Open the Security + access menu. Populate the access-state card from the
		// audit results we already have (read-only — no apply happens here).
		m.populateSecurityState()
		m.secDangerConfirm = false
		m.phase = phaseSecurity
		return m, nil
	}
	// Audit row → wiki detail for that tweak. Resolve the doc by the tweak's
	// Probe.Step (matches wiki.Doc keys, e.g. "A2"); keep the specific tweak's
	// name+ID as the page header so the body is never the empty fallback.
	if r, ok := m.dashAuditRowAtClick(x, y); ok {
		m.wikiStep = r.Probe.Step
		m.wikiTweak = tweakWikiHeader(m.lang, r.Probe)
		m.wikiReturn = phaseDashboard
		m.wikiScroll = 0
		m.phase = phaseWiki
		return m, nil
	}
	return m, nil
}
