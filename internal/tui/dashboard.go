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
// dashboardView renders the post-connect Dashboard. The top is a FIXED chrome
// prefix that never scrolls: a framed server card (OS/kernel/RAM/disk/IPv6 from
// m.dashFacts + the live monitor sample), the two action button pills, and the
// live tweak-audit status line. Below it the ONLY scrollable region is the ✓/•
// audit grid (from m.dashAuditResults). The monitor footer is pinned at the
// bottom. Chrome (titled top, switcher, hint, bottom border, monitor box) mirrors
// summaryView/matrixView so the footer never moves.
//
// Layout (screen rows, top→bottom):
//
//	row 0                         : titled top border
//	row 1                         : RU|EN switcher
//	rows [2, 2+len(fixed))        : FIXED prefix — card, buttons row, status line
//	rows [dashScrollTopRow, +viewH): SCROLL region — audit grid only
//	hint / bottom border / monitor: pinned footer chrome
//
// The clickable buttons sit at a FIXED screen Y (dashButtonsRowY); the audit grid
// rows are resolved against the scroll region (honoring the scroll offset). Both
// hit-tests reuse the SAME builders the renderer iterates so geometry cannot drift.
func (m model) dashboardView() string {
	// CHANGE 1: when the apply-confirm is armed, show a clearly-visible CENTERED MODAL
	// over the screen instead of merely swapping the bottom hint. Enter/Esc are still
	// handled by the existing phaseDashboard key handler (launchApplyTweaks / cancel).
	if m.dashApplyConfirm {
		return m.applyConfirmModalView()
	}
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	fixed := m.dashFixedLines(innerW)
	body := m.dashBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// FIXED prefix — plain content rows, never scrolled.
	for _, line := range fixed {
		sb.WriteString(contentLine(b, line, innerW))
		sb.WriteByte('\n')
	}

	// SCROLL region — only the audit grid, sized to fill the remaining middle.
	viewH := m.dashScrollViewH(innerW)
	off := clampScroll(m.dashScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	// dashApplyConfirm is handled by applyConfirmModalView (returned early above), so
	// the normal dashboard always shows the plain navigation hint here.
	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, kDashHint)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// modalBoxStyle is the bordered frame for a centered confirm modal (CHANGE 1): an
// accent rounded border with padding, so the box reads as an overlay distinct from
// the plain hand-drawn chrome.
var modalBoxStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("57")).
	Padding(1, 3)

// applyConfirmModalView renders the apply-tweaks confirm as a CENTERED modal box
// (CHANGE 1). It states plainly that this applies all tweaks and, when the bucket
// includes A8, WARNS that it performs a full upgrade + REBOOT (services bounce
// ~1-2 min). The buttons line restates the Enter/Esc controls. Enter/Esc remain
// handled by the phaseDashboard key handler (launchApplyTweaks / cancel) — this is
// purely the visual. The box is centered in the full terminal viewport.
func (m model) applyConfirmModalView() string {
	// Modal inner width: bounded so the box never spans the whole terminal but stays
	// readable on a narrow one.
	w := m.boxWidth()
	innerW := min(max(innerWidth(w)-8, 32), 64)

	title := sumHeadStyle.Render(t(m.lang, kApplyModalTitle))

	var parts []string
	parts = append(parts, title)
	parts = append(parts, "")
	parts = append(parts, wrap(labelStyle.Render(t(m.lang, kApplyModalBody)), innerW)...)
	if bucketHasA8(tweakBucketIDs()) {
		parts = append(parts, "")
		parts = append(parts, wrap(errStyle.Render(t(m.lang, kApplyModalReboot)), innerW)...)
	}
	parts = append(parts, "")
	// Two SEPARATE pills with a plain (un-styled) gap between them (BUG 2): a single
	// pillOnStyle over the whole "[Enter] … [Esc] …" string would share one background
	// and read as one merged button. Accent pill for confirm, dim pill for cancel.
	confirmPill := pillOnStyle.Render(t(m.lang, kApplyModalConfirm))
	cancelPill := pillStyle.Render(t(m.lang, kApplyModalCancel))
	parts = append(parts, confirmPill+"   "+cancelPill)

	box := modalBoxStyle.Render(strings.Join(parts, "\n"))

	// Center the box over a full-viewport canvas so it visibly floats in the middle.
	cw, ch := m.boxWidth(), max(m.h, 1)
	return lipgloss.Place(cw, ch, lipgloss.Center, lipgloss.Center, box)
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

// dashFixedLines builds the FIXED (non-scrolling) Dashboard prefix — the single
// source of truth for BOTH dashboardView's render and the fixed hit-tests
// (dashButtonAtClick). Order: server card (framed, N lines), the buttons row, the
// audit status line. The buttons row index within this slice is len(card); the
// status line is len(card)+1. Every width uses lipgloss.Width.
func (m model) dashFixedLines(innerW int) []string {
	var fixed []string
	fixed = append(fixed, m.dashServerCard(innerW)...)
	fixed = append(fixed, m.dashButtonsLine())
	fixed = append(fixed, m.dashStatusLine(innerW))
	return fixed
}

// dashStatusLine is the live audit status row:
// "Анализ твиков ⠹  применено N из M · можно применить K", truncated to innerW.
func (m model) dashStatusLine(innerW int) string {
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
	return truncDisplay(status, innerW)
}

// dashBodyLines builds the SCROLLABLE Dashboard body — ONLY the audit grid (a fluid
// 1- or 2-column grid of "  ✓/•  name" cells). It is the single source of truth for
// BOTH the render and the audit-row hit-test (dashAuditRowAtClick): grid line r maps
// to results[cols*r .. cols*r+cols-1] → Probe.ID → wiki detail. dashAuditGridLines is
// the single source of the column geometry so render and hit-test cannot drift.
func (m model) dashBodyLines(innerW int) []string {
	return m.dashAuditGridLines(innerW)
}

// dashScrollViewH is the height (rows) of the scrollable audit-grid region: the
// summary/matrix middle height (bodyViewH) minus the fixed prefix (card + buttons +
// status), floored at 1 so it never goes negative or overlaps the footer on a small
// terminal.
func (m model) dashScrollViewH(innerW int) int {
	return max(m.bodyViewH()-len(m.dashFixedLines(innerW)), 1)
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

	// One fact per card line so nothing is hidden on a narrow width. dashButtonsRowY /
	// dashScrollTopRow use len(card) dynamically, so a taller card stays hit-test-correct.
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

// dashButtonsRowY is the FIXED screen Y of the action-buttons row. The fixed prefix
// begins at summaryBodyTopRow with the server card (N lines), then the buttons row,
// so the buttons sit at summaryBodyTopRow + len(card). This Y does NOT move with the
// scroll offset — the buttons are pinned chrome.
func (m model) dashButtonsRowY(innerW int) int {
	return summaryBodyTopRow + len(m.dashServerCard(innerW))
}

// dashScrollTopRow is the screen Y of the FIRST row of the scrollable audit-grid
// region: it follows the entire fixed prefix (card + buttons + status).
func (m model) dashScrollTopRow(innerW int) int {
	return summaryBodyTopRow + len(m.dashFixedLines(innerW))
}

// dashRowYToBodyIdx maps a screen Y in the SCROLLABLE audit-grid region to a body
// (grid) index, honoring the scroll offset, or ok=false when Y is in the fixed prefix
// or footer chrome rather than the scroll region.
func (m model) dashRowYToBodyIdx(y int) (int, bool) {
	innerW := innerWidth(m.boxWidth())
	body := m.dashBodyLines(innerW)
	viewH := m.dashScrollViewH(innerW)
	off := clampScroll(m.dashScroll, len(body), viewH)
	rowInRegion := y - m.dashScrollTopRow(innerW)
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
	gridRow, ok := m.dashRowYToBodyIdx(y)
	if !ok {
		return tweaks.Result{}, false
	}
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
	if y != m.dashButtonsRowY(innerW) {
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
		m.dashScroll = 0 // security menu reuses dashScroll; start at the top
		return m, nil
	}
	// Audit row → wiki detail for that tweak. Resolve the doc by the tweak's
	// Probe.Step (matches wiki.Doc keys, e.g. "A2"); keep the specific tweak's
	// name+ID as the page header so the body is never the empty fallback.
	if r, ok := m.dashAuditRowAtClick(x, y); ok {
		m.wikiStep = r.Probe.Step
		m.wikiTweak = tweakWikiHeader(m.lang, r.Probe)
		m.wikiProbeID = r.Probe.ID // per-probe description path
		m.wikiReturn = phaseDashboard
		m.wikiScroll = 0
		m.wikiUpdateConfirm = false // fresh page never carries a stale reboot confirm
		m.phase = phaseWiki
		return m, nil
	}
	return m, nil
}
