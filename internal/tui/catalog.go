package tui

import (
	tea "charm.land/bubbletea/v2"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/version"
)

// catalogDomain groups a domain header key with its ordered step IDs.
type catalogDomain struct {
	headKey stringKey
	steps   []string
}

// catalogDomains is the hardcoded domain → step grouping. A2/PRE
// are intentionally absent (Security screen); A2.5 (cloud-init neutralization) is
// likewise excluded as part of the SSH/access story surfaced on the Security screen,
// so its absence here is deliberate, not an oversight. If a step ID is added/
// recategorized in the engine, update this map (single source of truth for the
// catalog layout).
func catalogDomains() []catalogDomain {
	return []catalogDomain{
		{kCatalogNetwork, []string{"A4"}},
		{kCatalogMemory, []string{"A6.7"}},
		{kCatalogKernelMaint, []string{"A5", "A6", "A6.5"}},
		{kCatalogFwUpdates, []string{"A1", "A3", "A8"}},
		{kCatalogOther, []string{"A7", "A9", "A10"}},
	}
}

// catalogStepLabel is the rendered text of one catalog step row (without the status
// column): "  › <localized name>  <ID>". The single source shared by the render path
// and the row hit-test so their geometry cannot drift.
func (m model) catalogStepLabel(stepID string) string {
	name := localStepTitle(m.lang, stepID, stepID)
	return "  › " + name + "  " + stepID
}

// catalogBodyLines builds the ordered catalog body slice — the single source of
// truth for BOTH catalogView's render and the row hit-test (catalogRowAtClick).
// Order: title card (framed), blank, [docs-only header pre-connect | blank], then
// for each domain: a header line + its indented step rows, blank between domains,
// finally a blank + the security note. Status column only post-connect.
func (m model) catalogBodyLines(innerW int) []string {
	var body []string
	body = append(body, m.catalogTitleCard(innerW)...)
	body = append(body, "")

	if !m.catalogConnected() {
		body = append(body, helpStyle.Render(t(m.lang, kCatalogDocsOnly)))
		body = append(body, "")
	}

	for i, d := range catalogDomains() {
		if i > 0 {
			body = append(body, "")
		}
		body = append(body, sumHeadStyle.Render(t(m.lang, d.headKey)))
		for _, id := range d.steps {
			row := m.catalogStepLabel(id)
			if word, ok := m.stepStatusWord(id); ok {
				row += "  [" + word + "]"
			}
			body = append(body, truncDisplay(row, innerW))
		}
	}

	body = append(body, "")
	body = append(body, tipStyle.Render(t(m.lang, kCatalogSecurityNote)))
	return body
}

// catalogTitleCard renders the framed catalog title card (rounded frame like the
// dashboard server card) as content lines fitted to innerW.
func (m model) catalogTitleCard(innerW int) []string {
	bd := lipgloss.RoundedBorder()
	fw := max(innerW, minBoxWidth)
	title := " " + t(m.lang, kCatalogTitle) + " "
	top := titledTop(bd, title, fw)
	bottom := borderLine(bd.BottomLeft, bd.Bottom, bd.BottomRight, fw)
	return []string{top, bottom}
}

// catalogDataTopIndex is the body-slice index of the FIRST domain/row line — the
// prefix is the title card (N lines) + blank, plus the docs-only header + blank when
// pre-connect. Used by catalogRowAtClick to walk rows against the same layout.
func (m model) catalogDataTopIndex(innerW int) int {
	idx := len(m.catalogTitleCard(innerW)) + 1
	if !m.catalogConnected() {
		idx += 2 // docs-only header line + blank
	}
	return idx
}

// catalogRowAtClick maps a click at (x,y) to the step ID whose row was hit, or
// ok=false on a miss. It walks the SAME domain/row sequence catalogBodyLines emits,
// honoring the scroll offset, so a click resolves to exactly the rendered row.
func (m model) catalogRowAtClick(x, y int) (string, bool) {
	if m.phase != phaseCatalog {
		return "", false
	}
	innerW := innerWidth(m.boxWidth())
	body := m.catalogBodyLines(innerW)
	viewH := m.catalogBodyViewH()
	off := clampScroll(m.catalogScroll, len(body), viewH)
	rowInRegion := y - summaryBodyTopRow
	if rowInRegion < 0 || rowInRegion >= viewH {
		return "", false
	}
	bodyIdx := off + rowInRegion
	if bodyIdx < 0 || bodyIdx >= len(body) {
		return "", false
	}
	// Reconstruct which body indices are step rows by replaying the layout, mapping
	// each step-row body index to its step ID.
	dataTop := m.catalogDataTopIndex(innerW)
	cur := dataTop
	for i, d := range catalogDomains() {
		if i > 0 {
			cur++ // blank between domains
		}
		cur++ // domain header line
		for _, id := range d.steps {
			if cur == bodyIdx {
				// X must fall within the rendered row width (content starts at column 2).
				// Rebuild the row IDENTICALLY to catalogBodyLines (label + status bracket
				// post-connect) so the status portion is hittable, not just the label.
				const contentX0 = 2
				row := m.catalogStepLabel(id)
				if word, ok := m.stepStatusWord(id); ok {
					row += "  [" + word + "]"
				}
				w := lipgloss.Width(truncDisplay(row, innerW))
				if x >= contentX0 && x < contentX0+w {
					return id, true
				}
				return "", false
			}
			cur++
		}
	}
	return "", false
}

// catalogBodyViewH is bodyViewH minus one row (the catalog carries an extra
// fixed-chrome row: the "← Назад" pill above the hint, like the wiki screen). Used
// for BOTH the render and every catalog scroll clamp so geometry never drifts.
func (m model) catalogBodyViewH() int { return max(m.bodyViewH()-1, 1) }

// catalogBackRow is the screen Y of the catalog back-button row: right after the
// scrollable middle region. Mirrors wikiBackRow.
func (m model) catalogBackRow() int {
	return summaryBodyTopRow + m.catalogBodyViewH()
}

// catalogBackAtClick reports whether (x,y) hit the catalog "← Назад" pill, mirroring
// wikiBackAtClick's single-pill geometry.
func (m model) catalogBackAtClick(x, y int) bool {
	if m.phase != phaseCatalog || y != m.catalogBackRow() {
		return false
	}
	return pillIndexAt([]string{t(m.lang, kWikiBack)}, wikiBackStartCol, x) == 0
}

// catalogView renders the tweak-catalog screen. Chrome mirrors wikiView (title box,
// switcher, scroll region, pinned back pill, hint, bottom border) so the monitor
// footer stays pinned. The footer is appended ONLY post-connect.
func (m model) catalogView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body := m.catalogBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	viewH := m.catalogBodyViewH()
	off := clampScroll(m.catalogScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	// Clickable "← Назад" pill pinned just above the hint, hit-tested by
	// catalogBackAtClick (same chrome row as the wiki back pill).
	sb.WriteString(contentLine(b, pillOnStyle.Render(t(m.lang, kWikiBack)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, kCatalogHint)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))

	// Monitor footer ONLY post-connect (pre-connect the sampler isn't running and the
	// catalog is docs-only). renderScrollRegion already kept the body pinned, so we
	// only append the footer block here.
	if m.catalogConnected() {
		sb.WriteByte('\n')
		sb.WriteString(m.monitorBox(innerW))
	}
	return sb.String()
}

// catalogClick applies a catalog-phase click: the back pill returns to catalogReturn;
// a step row opens that step's wiki detail (step-level doc — wikiTweak cleared) with
// wikiReturn=phaseCatalog so esc/back returns here.
func (m model) catalogClick(x, y int) (tea.Model, tea.Cmd) {
	if m.catalogBackAtClick(x, y) {
		m.phase = m.catalogReturn
		return m, nil
	}
	if id, ok := m.catalogRowAtClick(x, y); ok {
		m.wikiStep = id
		m.wikiTweak = "" // step-level doc, plain step header
		m.wikiReturn = phaseCatalog
		m.wikiScroll = 0
		m.phase = phaseWiki
	}
	return m, nil
}

// catalogConnected reports whether we are post-connect: an audit has completed and
// carried results. Used to gate the wiki/catalog live-status column and the catalog
// monitor footer. Mirrors how the Dashboard treats dashAuditDone as "connected".
func (m model) catalogConnected() bool {
	return m.dashAuditDone && len(m.dashAuditRaw) > 0
}
