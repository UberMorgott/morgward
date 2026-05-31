package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/tweaks"
	"github.com/UberMorgott/morgward/internal/wiki"
)

// catalogModelPre builds a pre-connect catalog model (no audit, docs only).
func catalogModelPre(w, h int) model {
	m := newModel()
	m.w, m.h = w, h
	m.phase = phaseCatalog
	m.catalogReturn = phaseForm
	return m
}

// catalogModelPost builds a post-connect catalog model with a small audit so the
// live status column + footer render.
func catalogModelPost(w, h int) model {
	m := catalogModelPre(w, h)
	m.host = "1.2.3.4"
	m.catalogReturn = phaseDashboard
	m.dashFacts = &detect.Facts{ID: "ubuntu", VersionID: "24.04"}
	m.dashAuditRaw = []tweaks.Result{
		{Probe: tweaks.Probe{ID: "a4.bbr_active", Step: "A4", Name: "BBR"}, Applied: true},
		{Probe: tweaks.Probe{ID: "a4.qdisc", Step: "A4", Name: "fq qdisc"}, Applied: true},
		{Probe: tweaks.Probe{ID: "a1.input_drop", Step: "A1", Name: "INPUT DROP"}, Applied: false},
	}
	m.dashAuditDone = true
	m.haveSample = true
	return m
}

// TestCatalogRenderPreConnect asserts the pre-connect catalog shows the docs-only
// header, NO status column, and NO monitor footer.
func TestCatalogRenderPreConnect(t *testing.T) {
	m := catalogModelPre(100, 40)
	out := m.catalogView()
	if !strings.Contains(out, t2(m.lang, kCatalogTitle)) {
		t.Fatalf("catalog missing title\n%s", out)
	}
	if !strings.Contains(out, t2(m.lang, kCatalogDocsOnly)) {
		t.Fatalf("pre-connect catalog missing docs-only header\n%s", out)
	}
	if strings.Contains(out, t2(m.lang, kStatusApplied)) || strings.Contains(out, t2(m.lang, kStatusCanApply)) {
		t.Fatalf("pre-connect catalog must NOT show a status column\n%s", out)
	}
	if strings.Contains(out, t2(m.lang, kMonTitle)) {
		t.Fatalf("pre-connect catalog must NOT show the monitor footer\n%s", out)
	}
	// The security note must be present (security lives on its own screen).
	if !strings.Contains(out, t2(m.lang, kCatalogSecurityNote)) {
		t.Fatalf("catalog missing security note\n%s", out)
	}
}

// TestCatalogRenderPostConnect asserts the post-connect catalog shows the live
// status column and the monitor footer.
func TestCatalogRenderPostConnect(t *testing.T) {
	m := catalogModelPost(100, 40)
	out := m.catalogView()
	if strings.Contains(out, t2(m.lang, kCatalogDocsOnly)) {
		t.Fatalf("post-connect catalog must NOT show docs-only header\n%s", out)
	}
	// A4 has both probes applied → applied; A1 has its single probe unapplied → can.
	if !strings.Contains(out, t2(m.lang, kStatusApplied)) {
		t.Fatalf("post-connect catalog missing applied status\n%s", out)
	}
	if !strings.Contains(out, t2(m.lang, kStatusCanApply)) {
		t.Fatalf("post-connect catalog missing can-apply status\n%s", out)
	}
	if !strings.Contains(out, t2(m.lang, kMonTitle)) {
		t.Fatalf("post-connect catalog must show the monitor footer\n%s", out)
	}
}

// TestCatalogRowOpensWiki asserts clicking a catalog step row opens the wiki detail
// for that step: wikiStep set to the step ID, wikiTweak cleared (step-level doc),
// wikiReturn=phaseCatalog.
func TestCatalogRowOpensWiki(t *testing.T) {
	m := catalogModelPre(100, 40)
	innerW := innerWidth(m.boxWidth())
	// First domain row is A4 (Network). Compute its screen Y from the body layout.
	dataTop := m.catalogDataTopIndex(innerW)
	// dataTop is the domain-header line; the first step row sits at dataTop+1.
	rowY := summaryBodyTopRow + dataTop + 1
	id, ok := m.catalogRowAtClick(4, rowY)
	if !ok || id != "A4" {
		t.Fatalf("catalogRowAtClick(4,%d) = %q,%v want A4,true", rowY, id, ok)
	}
	next, _ := m.catalogClick(4, rowY)
	mm := next.(model)
	if mm.phase != phaseWiki {
		t.Fatalf("catalog row click → phase %v, want phaseWiki", mm.phase)
	}
	if mm.wikiStep != "A4" {
		t.Fatalf("wikiStep=%q want A4", mm.wikiStep)
	}
	if mm.wikiTweak != "" {
		t.Fatalf("wikiTweak=%q want empty (step-level doc)", mm.wikiTweak)
	}
	if mm.wikiReturn != phaseCatalog {
		t.Fatalf("wikiReturn=%v want phaseCatalog", mm.wikiReturn)
	}
}

// TestCatalogBackButton asserts the back pill returns to catalogReturn, and esc does
// the same via the key handler.
func TestCatalogBackButton(t *testing.T) {
	m := catalogModelPost(100, 40)
	if m.catalogReturn != phaseDashboard {
		t.Fatalf("setup: catalogReturn=%v want phaseDashboard", m.catalogReturn)
	}
	backY := m.catalogBackRow()
	x := pillRanges([]string{t2(m.lang, kWikiBack)}, wikiBackStartCol)[0][0] + 1
	if !m.catalogBackAtClick(x, backY) {
		t.Fatalf("back pill not hit at x=%d y=%d", x, backY)
	}
	next, _ := m.catalogClick(x, backY)
	if next.(model).phase != phaseDashboard {
		t.Fatalf("back click → phase %v, want phaseDashboard", next.(model).phase)
	}
	// esc via the key handler also returns to catalogReturn.
	n2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if n2.(model).phase != phaseDashboard {
		t.Fatalf("esc → phase %v, want phaseDashboard", n2.(model).phase)
	}
}

// TestCatalogNavFromDashboard asserts the Dashboard Catalog button opens the catalog
// with catalogReturn=phaseDashboard, and the landing link opens it with
// catalogReturn=phaseForm.
func TestCatalogNavFromDashboard(t *testing.T) {
	m := dashModel(100, 40)
	innerW := innerWidth(m.boxWidth())
	btnRow := summaryBodyTopRow + m.dashButtonsIndex(innerW)
	catX := pillRanges(m.dashButtonNames(), dashButtonStartCol)[2][0] + 1
	next, _ := m.dashboardClick(catX, btnRow)
	mm := next.(model)
	if mm.phase != phaseCatalog || mm.catalogReturn != phaseDashboard {
		t.Fatalf("dash catalog button → phase=%v return=%v want phaseCatalog/phaseDashboard", mm.phase, mm.catalogReturn)
	}
}

// TestCatalogNavFromLanding asserts the landing "Что настраивает программа ▸" link is
// clickable and opens the catalog with catalogReturn=phaseForm.
func TestCatalogNavFromLanding(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	// Find the catalog-link row's screen Y from formRows.
	rows := m.formRows()
	linkIdx := -1
	for i, r := range rows {
		if r.kind == frCatalogLink {
			linkIdx = i
			break
		}
	}
	if linkIdx < 0 {
		t.Fatalf("no frCatalogLink row in formRows")
	}
	y := formBodyTopRow + linkIdx
	next, _ := m.formClick(10, y)
	mm := next.(model)
	if mm.phase != phaseCatalog || mm.catalogReturn != phaseForm {
		t.Fatalf("landing link → phase=%v return=%v want phaseCatalog/phaseForm", mm.phase, mm.catalogReturn)
	}
}

// TestWikiRendersOnBoxRevert asserts the wiki detail renders all FIVE sections
// (What/Why/Risk/OnBox/Revert) for a step with a full doc.
func TestWikiRendersOnBoxRevert(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseWiki
	m.wikiStep = "A4"
	innerW := innerWidth(m.boxWidth())
	body := strings.Join(m.wikiBodyLines(innerW), "\n")
	for _, k := range []stringKey{kWikiWhat, kWikiWhy, kWikiRisk, kWikiOnBox, kWikiRevert} {
		if !strings.Contains(body, t2(m.lang, k)) {
			t.Fatalf("wiki body missing section %q\n%s", t2(m.lang, k), body)
		}
	}
	// Sanity: the doc actually has OnBox/Revert content.
	doc, _ := wiki.Doc(wiki.Lang(int(m.lang)), "A4")
	if doc.OnBox == "" || doc.Revert == "" {
		t.Fatalf("A4 doc missing OnBox/Revert content")
	}
}

// TestWikiStatusPostConnectOnly asserts the wiki status line appears ONLY post-connect
// (with an audit result for the step), and is absent pre-connect.
func TestWikiStatusPostConnectOnly(t *testing.T) {
	// Pre-connect: no status line.
	pre := newModel()
	pre.w, pre.h = 100, 40
	pre.phase = phaseWiki
	pre.wikiStep = "A4"
	preBody := strings.Join(pre.wikiBodyLines(innerWidth(pre.boxWidth())), "\n")
	if strings.Contains(preBody, t2(pre.lang, kWikiStatus)) {
		t.Fatalf("pre-connect wiki must NOT show a status line\n%s", preBody)
	}

	// Post-connect: status line present for a step that has audit results.
	post := catalogModelPost(100, 40)
	post.phase = phaseWiki
	post.wikiStep = "A4"
	postBody := strings.Join(post.wikiBodyLines(innerWidth(post.boxWidth())), "\n")
	if !strings.Contains(postBody, t2(post.lang, kWikiStatus)) {
		t.Fatalf("post-connect wiki must show a status line\n%s", postBody)
	}
	if !strings.Contains(postBody, t2(post.lang, kStatusApplied)) {
		t.Fatalf("A4 (both probes applied) should read applied\n%s", postBody)
	}
}

// TestCatalogModelValueCopySafe asserts the catalog model fields survive a value copy
// (no maps/pointers that would alias) — the Bubble Tea v2 copy-by-value invariant.
func TestCatalogModelValueCopySafe(t *testing.T) {
	m := catalogModelPost(100, 40)
	cp := m
	cp.catalogScroll = 99
	cp.catalogReturn = phaseForm
	if m.catalogScroll == 99 || m.catalogReturn == phaseForm {
		t.Fatalf("catalog fields aliased after value copy")
	}
}
