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
// live tweak-audit status line + a ✓/• grid (from m.dashAuditResults), and three
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

// dashButtonNames is the ordered list of the three Dashboard action-button labels
// (Apply / Security / Catalog). It is the SINGLE source consumed by both the
// render path (dashButtonsLine) and the hit-test (dashButtonAtClick), so their
// x-geometry cannot diverge.
func (m model) dashButtonNames() []string {
	return []string{
		t(m.lang, kDashApplyButton),
		t(m.lang, kDashSecButton),
		t(m.lang, kDashCatalogButton),
	}
}

// dashButtonStartCol is the absolute X where the first button pill begins on the
// buttons row: 2 (left border + space) + 1 (the leading indent space in the row).
const dashButtonStartCol = 3

// dashButtonsLine renders the three action pills joined by a single space, with a
// one-space indent so the first pill begins at dashButtonStartCol. All three use
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
// blank, audit grid rows (one per result), blank, buttons line. Every width uses
// lipgloss.Width.
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

	// Audit grid: one row per result, "  ✓/•  name". The rows are the clickable
	// tail used by dashAuditRowAtClick (each maps to its Probe.ID → wiki detail).
	okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	canStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	for _, r := range m.dashAuditResults {
		glyph := canStyle.Render("•")
		if r.Applied {
			glyph = okStyle.Render("✓")
		}
		name := localTweakName(m.lang, r.Probe.ID, r.Probe.Name)
		body = append(body, truncDisplay("  "+glyph+" "+name, innerW))
	}

	body = append(body, "")
	body = append(body, m.dashButtonsLine())
	return body
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

// dashGridStartIndex is the body-slice index of the FIRST audit grid row. The body
// prefix is: server card (N lines) + blank + status + blank, so the grid begins at
// len(card)+3. dashButtonsIndex is the body index of the buttons row: grid start +
// number of results + 1 (the blank before the buttons line).
func (m model) dashGridStartIndex(innerW int) int {
	return len(m.dashServerCard(innerW)) + 3
}

func (m model) dashButtonsIndex(innerW int) int {
	return m.dashGridStartIndex(innerW) + len(m.dashAuditResults) + 1
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

// dashAuditRowAtClick maps a click at (x,y) to the audit Result whose row was hit,
// or ok=false on a miss. The caller resolves the wiki doc key from the result's
// Probe.Step and the header label from its Probe.ID/Name. The grid rows are the
// contiguous block of len(results) lines starting at dashGridStartIndex.
func (m model) dashAuditRowAtClick(x, y int) (tweaks.Result, bool) {
	if m.phase != phaseDashboard || len(m.dashAuditResults) == 0 {
		return tweaks.Result{}, false
	}
	innerW := innerWidth(m.boxWidth())
	bodyIdx, ok := m.dashRowYToBodyIdx(y)
	if !ok {
		return tweaks.Result{}, false
	}
	gridStart := m.dashGridStartIndex(innerW)
	resIdx := bodyIdx - gridStart
	if resIdx < 0 || resIdx >= len(m.dashAuditResults) {
		return tweaks.Result{}, false
	}
	// X must fall within the rendered row width (rows are content from column 2).
	const contentX0 = 2
	r := m.dashAuditResults[resIdx]
	glyph := "•"
	if r.Applied {
		glyph = "✓"
	}
	row := "  " + glyph + " " + localTweakName(m.lang, r.Probe.ID, r.Probe.Name)
	w := lipgloss.Width(truncDisplay(row, innerW))
	if x >= contentX0 && x < contentX0+w {
		return r, true
	}
	return tweaks.Result{}, false
}

// tweakWikiHeader is the wiki page header for a tweak opened from the Dashboard:
// "[<id>] <localized name>". Pure function so it is testable without driving the TUI.
func tweakWikiHeader(lang Lang, p tweaks.Probe) string {
	return fmt.Sprintf("[%s] %s", p.ID, localTweakName(lang, p.ID, p.Name))
}

// dashButton enumerates the three Dashboard actions resolved by dashButtonAtClick.
type dashButton int

const (
	dashBtnNone dashButton = iota
	dashBtnApply
	dashBtnSecurity
	dashBtnCatalog
)

// dashButtonAtClick maps a click at (x,y) to one of the three action buttons, using
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
	case 2:
		return dashBtnCatalog
	}
	return dashBtnNone
}

// dashboardClick resolves a Dashboard click: an audit row opens its wiki detail; a
// button pill triggers its action. "Применить твики" first shows the A8 reboot
// warning (Enter to confirm). "Безопасность ▸" and "Каталог твиков" navigate to
// the security menu (phaseSecurity) and the tweak catalog (phaseCatalog).
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
	case dashBtnCatalog:
		// Open the tweak catalog (post-connect: status column + footer). Returning
		// from it lands back on the Dashboard.
		m.catalogReturn = phaseDashboard
		m.catalogScroll = 0
		m.phase = phaseCatalog
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
