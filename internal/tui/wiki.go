package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/version"
	"github.com/UberMorgott/morgward/internal/wiki"
)

// wikiBackStartCol is the absolute X where the wiki "← Назад" pill (and every
// other wiki action pill) begins: 2 (left border + the leading content space
// added by contentLine).
const wikiBackStartCol = 2

// wikiActionKind enumerates the fixed-chrome rows the wiki PROBE-detail screen can
// add BELOW the scrollable description and ABOVE the back pill. The back pill is
// always present; the others appear only on the per-PROBE path under their own
// conditions (see wikiActionRows).
type wikiActionKind int

const (
	wikiRowUpdateWarn   wikiActionKind = iota // non-clickable warning text line
	wikiRowUpdateButton                       // clickable "Обновить и перезагрузить" pill
	wikiRowApplyButton                        // clickable "Применить" pill
	wikiRowRevertButton                       // clickable "Откатить" pill (applied + revertable probes)
	wikiRowBack                               // clickable "← Назад" pill (always last)
)

// wikiActionRows is the SINGLE source of truth for the wiki fixed-chrome action
// rows and their screen order, shared by BOTH the render (wikiView) and the
// hit-tests (wikiBackAtClick / wikiApplyAtClick / wikiUpdateAtClick) so geometry
// can never drift. Order top→bottom matches the spec:
//
//	[update warning]  — only when m.dashFacts.PendingUpgrades > 0
//	[update button]   — only when m.dashFacts.PendingUpgrades > 0
//	[apply button]    — only when the probe is NOT applied and NOT informational
//	[back pill]       — always
//
// The apply/update rows appear ONLY on the per-PROBE path (m.wikiProbeID != "");
// the summary path (empty probe id) shows just the back pill, unchanged.
func (m model) wikiActionRows() []wikiActionKind {
	var rows []wikiActionKind
	if m.wikiProbeID != "" {
		if m.dashFacts != nil && m.dashFacts.PendingUpgrades > 0 {
			rows = append(rows, wikiRowUpdateWarn, wikiRowUpdateButton)
		}
		applied, info, ok := m.wikiProbeState()
		if ok && !applied && !info {
			rows = append(rows, wikiRowApplyButton)
		}
		// Revert is offered only for an APPLIED probe (not informational) whose step
		// has an engine revert snippet. Placed after the apply button, before back.
		if ok && applied && !info {
			if step, has := m.wikiProbeStep(); has && engine.IsRevertable(step) {
				rows = append(rows, wikiRowRevertButton)
			}
		}
	}
	rows = append(rows, wikiRowBack)
	return rows
}

// --- FEATURE B: action BUTTON pills collapse onto ONE horizontal row -----------
//
// The wiki action area is at most TWO fixed rows: an optional non-clickable update
// WARNING text line, then a SINGLE row carrying every present action button pill
// (update / apply / revert / back) side by side, gap-separated — like the Dashboard
// two-pill row. wikiButtonKinds is the SINGLE ordered source for both the render and
// the hit-tests, so a pill's index in the row matches between draw and click.

// wikiButtonKinds returns the present action-button kinds in left→right row order
// (update, apply, revert, back — only those currently shown). The warning is NOT a
// button and is excluded; the back pill is always present (last).
func (m model) wikiButtonKinds() []wikiActionKind {
	var kinds []wikiActionKind
	for _, k := range m.wikiActionRows() {
		if k == wikiRowUpdateWarn {
			continue
		}
		kinds = append(kinds, k)
	}
	return kinds
}

// wikiButtonLabel maps a button kind to its localized pill label.
func (m model) wikiButtonLabel(k wikiActionKind) string {
	switch k {
	case wikiRowUpdateButton:
		return t(m.lang, kWikiUpdateButton)
	case wikiRowApplyButton:
		return t(m.lang, kWikiApplyButton)
	case wikiRowRevertButton:
		return t(m.lang, kWikiRevertButton)
	case wikiRowBack:
		return t(m.lang, kWikiBack)
	}
	return ""
}

// wikiButtonLabels is the ordered label list for the single buttons row — the shared
// geometry source for wikiButtonsLine (render) and pillIndexAt (hit-test).
func (m model) wikiButtonLabels() []string {
	kinds := m.wikiButtonKinds()
	labels := make([]string, len(kinds))
	for i, k := range kinds {
		labels[i] = m.wikiButtonLabel(k)
	}
	return labels
}

// wikiHasUpdateWarn reports whether the update WARNING text row is shown (it precedes
// the buttons row when present).
func (m model) wikiHasUpdateWarn() bool {
	return slices.Contains(m.wikiActionRows(), wikiRowUpdateWarn)
}

// wikiActionRowCount is the number of fixed-chrome action rows reserved below the
// scrollable description: the optional warning row plus the single buttons row (which
// is always present, since the back pill is always present).
func (m model) wikiActionRowCount() int {
	if m.wikiHasUpdateWarn() {
		return 2
	}
	return 1
}

// wikiButtonsRowY is the screen Y of the single buttons row: it follows the
// scrollable middle region and the optional warning row.
func (m model) wikiButtonsRowY() int {
	y := summaryBodyTopRow + m.wikiBodyViewH()
	if m.wikiHasUpdateWarn() {
		y++
	}
	return y
}

// wikiButtonsLine joins every present action pill on one row, gap-separated, starting
// at wikiBackStartCol. The back pill uses the dim style; the action pills (update/
// apply/revert) use the accent style — visually distinct. pillRanges over
// wikiButtonLabels recovers the x-geometry for the hit-tests.
func (m model) wikiButtonsLine() string {
	kinds := m.wikiButtonKinds()
	pills := make([]string, len(kinds))
	for i, k := range kinds {
		label := m.wikiButtonLabel(k)
		if k == wikiRowBack {
			pills[i] = pillStyle.Render(label)
		} else {
			pills[i] = pillOnStyle.Render(label)
		}
	}
	return strings.Join(pills, " ")
}

// wikiButtonAtClick reports whether (x,y) hit the given button kind on the single
// buttons row, using pillIndexAt over the SAME ordered labels wikiButtonsLine renders.
func (m model) wikiButtonAtClick(kind wikiActionKind, x, y int) bool {
	if m.phase != phaseWiki || y != m.wikiButtonsRowY() {
		return false
	}
	labels := m.wikiButtonLabels()
	idx := pillIndexAt(labels, wikiBackStartCol, x)
	if idx < 0 || idx >= len(labels) {
		return false
	}
	return m.wikiButtonKinds()[idx] == kind
}

// wikiProbeState looks up m.wikiProbeID in m.dashAuditRaw and returns the probe's
// Applied + Informational verdicts. ok=false when the probe id is empty or has no
// matching audit Result (e.g. pre-connect or the summary path).
func (m model) wikiProbeState() (applied, informational, ok bool) {
	if m.wikiProbeID == "" {
		return false, false, false
	}
	for _, r := range m.dashAuditRaw {
		if r.Probe.ID == m.wikiProbeID {
			return r.Applied, r.Informational, true
		}
	}
	return false, false, false
}

// wikiProbeStep returns the engine step ID of the clicked probe (from m.dashAuditRaw),
// used by the [Применить] action to launch just that probe's step. ok=false on a miss.
func (m model) wikiProbeStep() (string, bool) {
	if m.wikiProbeID == "" {
		return "", false
	}
	for _, r := range m.dashAuditRaw {
		if r.Probe.ID == m.wikiProbeID {
			return r.Probe.Step, true
		}
	}
	return "", false
}

// wikiActionRowY returns the screen Y of the given action-row kind, or ok=false when
// that row is not currently shown. With FEATURE B every BUTTON kind shares the single
// buttons row (wikiButtonsRowY); the warning kind, when present, sits on the row just
// above it. Used by the keyboard handlers only to test whether a button is SHOWN.
func (m model) wikiActionRowY(kind wikiActionKind) (int, bool) {
	if kind == wikiRowUpdateWarn {
		if m.wikiHasUpdateWarn() {
			return summaryBodyTopRow + m.wikiBodyViewH(), true
		}
		return 0, false
	}
	if slices.Contains(m.wikiButtonKinds(), kind) {
		return m.wikiButtonsRowY(), true
	}
	return 0, false
}

// wikiBackRow is the screen Y of the buttons row (the back pill always lives there).
func (m model) wikiBackRow() int { return m.wikiButtonsRowY() }

// wikiBackAtClick reports whether (x,y) hit the wiki "← Назад" pill on the shared
// buttons row.
func (m model) wikiBackAtClick(x, y int) bool {
	return m.wikiButtonAtClick(wikiRowBack, x, y)
}

// wikiApplyAtClick reports whether (x,y) hit the "[Применить]" pill — present only
// when wikiActionRows includes it (per-PROBE path, probe not applied + not informational).
func (m model) wikiApplyAtClick(x, y int) bool {
	return m.wikiButtonAtClick(wikiRowApplyButton, x, y)
}

// wikiRevertAtClick reports whether (x,y) hit the "[Откатить]" pill — present only
// when wikiActionRows includes it (per-PROBE path, probe APPLIED + not informational
// + step is engine-revertable).
func (m model) wikiRevertAtClick(x, y int) bool {
	return m.wikiButtonAtClick(wikiRowRevertButton, x, y)
}

// wikiUpdateAtClick reports whether (x,y) hit the "[Обновить и перезагрузить]" pill —
// present only when there are pending upgrades (m.dashFacts.PendingUpgrades > 0).
func (m model) wikiUpdateAtClick(x, y int) bool {
	return m.wikiButtonAtClick(wikiRowUpdateButton, x, y)
}

// tweakBucketIDs is the canonical Tweaks-bucket step ID set applied by the
// Dashboard "Применить твики" action (everything EXCEPT the security/access steps
// A2/A2.5, which live behind the Security menu). selectSteps re-orders these into
// the load-bearing apply order, so the literal order here is not significant.
func tweakBucketIDs() []string {
	return []string{"A1", "A3", "A4", "A5", "A6", "A6.5", "A6.7", "A7", "A8", "A9", "A10"}
}

// isSecurityStep reports whether an engine step belongs to the SECURITY bucket
// (SSH crypto/access), which is the EXACT complement of tweakBucketIDs: A2/A2.5 are
// applied via the Security menu, NOT the Dashboard "Применить твики" button. The
// Dashboard tweaks grid + counters force these probes to "satisfied" (the button can
// never drive them, so they must never count as "можно применить"); the Security
// screen keeps their TRUE state (it reads m.dashAuditRaw, untouched by this rule).
func isSecurityStep(step string) bool {
	return step == "A2" || step == "A2.5"
}

// bucketHasA8 reports whether the apply bucket includes A8 (full upgrade + reboot),
// which warrants the explicit reboot-warning confirm before launching.
func bucketHasA8(ids []string) bool {
	for _, id := range ids {
		if strings.EqualFold(id, "A8") {
			return true
		}
	}
	return false
}

// launchApplyTweaks starts the apply over the Tweaks-bucket IDs via the engine's
// "step" command (RunSteps, allowBrownfield=true). It reuses start() so the run
// streams into the same log view and lands on the summary on completion. The
// credentials are taken from the still-populated landing inputs.
func (m model) launchApplyTweaks() (tea.Model, tea.Cmd) {
	m.command = "step"
	m.cmds = tweakBucketIDs()
	return m.start()
}

// startSteps starts an apply over the given engine step IDs via the "step" command
// (RunSteps, allowBrownfield=true). It mirrors launchApplyTweaks: set the command +
// id list, then reuse start() so the run streams into the same log view and the
// existing Hooks (Sink/OnConnect/OnProgress) plumbing carries progress + the
// generated key. The Security-menu buttons funnel through here.
func (m model) startSteps(ids []string) (tea.Model, tea.Cmd) {
	m.command = "step"
	m.cmds = ids
	return m.start()
}

// startRevert starts a per-tweak revert over the given engine step IDs via the
// engine's "revert" command (RunRevert, allowBrownfield=true). It mirrors startSteps:
// set the command + id list, then reuse start() so the run streams into the same log
// view and lands on the summary on completion. The wiki [Откатить] button funnels here.
func (m model) startRevert(ids []string) (tea.Model, tea.Cmd) {
	m.command = "revert"
	m.cmds = ids
	return m.start()
}

// wikiView renders one fix's what/why/risk description inside the run frame: a
// "[ID] Title" header, then three word-wrapped labeled blocks (WHAT IT DOES / WHY /
// WITHOUT IT). On an unknown step it shows the localized no-description line. The
// monitor footer stays alive (sampler still running). Text reads m.lang every frame,
// so the RU|EN toggle re-renders the description in the other language.
func (m model) wikiView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body := m.wikiBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// Same fixed-chrome layout as summaryView: a scrollable middle region of exactly
	// bodyViewH rows (scrollbar drawn on overflow), then the hint + bottom border +
	// the 3-row monitor box, so the footer stays pinned at any terminal size.
	viewH := m.wikiBodyViewH()
	off := clampScroll(m.wikiScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	// Fixed-chrome action rows pinned just above the hint line (FEATURE B): an optional
	// update WARNING text line, then a SINGLE row carrying every present action button
	// pill side by side. wikiButtonsLine + wikiHasUpdateWarn are the SAME sources the
	// hit-tests use (wikiButtonsRowY / wikiButtonLabels), so render and click geometry
	// can never drift.
	if m.wikiHasUpdateWarn() {
		sb.WriteString(contentLine(b, errStyle.Render(t(m.lang, kWikiUpdateWarn)), innerW))
		sb.WriteByte('\n')
	}
	sb.WriteString(contentLine(b, m.wikiButtonsLine(), innerW))
	sb.WriteByte('\n')
	hintKey := kWikiHint
	if m.wikiUpdateConfirm {
		hintKey = kWikiUpdateConfirm
	}
	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, hintKey)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')
	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// wikiBodyLines builds the wiki page body: the "[ID] Title" header then the three
// labeled, word-wrapped blocks. Falls back to the localized no-description line when
// the step has no wiki entry.
func (m model) wikiBodyLines(innerW int) []string {
	// Per-PROBE path (Dashboard audit-row click): when a probe ID is set and it has
	// its own description, render that distinct text instead of the shared step doc,
	// so e.g. the three A3 probes no longer all show the identical "fail2ban" doc.
	if m.wikiProbeID != "" {
		if desc, ok := probeDesc(m.lang, m.wikiProbeID); ok {
			body := []string{
				sumHeadStyle.Render(m.wikiTweak), // "[id] name" header
				"",
				monLabelStyle.Render(t(m.lang, kWikiProbeWhat)),
			}
			body = append(body, wrap(desc, innerW)...)
			// Same live status line the step doc appends.
			if word, ok := m.stepStatusWord(m.wikiStep); ok {
				body = append(body, "")
				body = append(body, monLabelStyle.Render(t(m.lang, kWikiStatus))+" "+word)
			}
			return body
		}
	}

	doc, ok := wiki.Doc(wiki.Lang(int(m.lang)), m.wikiStep)
	// Header: when opened from a tweak (Dashboard) show that specific tweak's
	// "[id] name"; otherwise (summary path) show the step "[ID] Title".
	header := func(title string) string {
		if m.wikiTweak != "" {
			return sumHeadStyle.Render(m.wikiTweak)
		}
		return sumHeadStyle.Render(title)
	}
	if !ok {
		return []string{header("[" + m.wikiStep + "]"), "", t(m.lang, kWikiNoDoc)}
	}
	var body []string
	body = append(body, header(fmt.Sprintf("[%s] %s", m.wikiStep, doc.Title)))

	block := func(labelKey stringKey, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		body = append(body, "")
		body = append(body, monLabelStyle.Render(t(m.lang, labelKey)))
		body = append(body, wrap(text, innerW)...)
	}
	block(kWikiWhat, doc.What)
	block(kWikiWhy, doc.Why)
	block(kWikiRisk, doc.RiskWithout)
	block(kWikiOnBox, doc.OnBox)
	block(kWikiRevert, doc.Revert)
	// Live status line — ONLY post-connect, and only when the audit yielded a result
	// for this step. Pre-connect (or no result) shows no status line at all.
	if word, ok := m.stepStatusWord(m.wikiStep); ok {
		body = append(body, "")
		body = append(body, monLabelStyle.Render(t(m.lang, kWikiStatus))+" "+word)
	}
	return body
}

// stepStatusWord returns the localized live-status word for a step ID, derived from
// the audit results (m.dashAuditRaw, the unfiltered set). A step is "applied" only
// when EVERY non-informational probe for it is applied; if any is not yet applied it
// is "can apply"; if the step has no probe at all it is "unavailable". ok=false
// pre-connect (no status line should be shown then).
func (m model) stepStatusWord(stepID string) (string, bool) {
	if !m.auditConnected() || stepID == "" {
		return "", false
	}
	total, applied := 0, 0
	for _, r := range m.dashAuditRaw {
		if r.Informational {
			continue
		}
		if r.Probe.Step != stepID {
			continue
		}
		total++
		if r.Applied {
			applied++
		}
	}
	switch {
	case total == 0:
		return t(m.lang, kStatusUnavailable), true
	case applied == total:
		return t(m.lang, kStatusApplied), true
	default:
		return t(m.lang, kStatusCanApply), true
	}
}

// wikiBodyViewH is bodyViewH minus the fixed-chrome action rows the wiki screen
// carries above the hint: the always-present "← Назад" pill plus, on the per-PROBE
// path, the optional update-warning / update-button / apply-button rows (see
// wikiActionRows). Reserving EXACTLY len(wikiActionRows) rows keeps the footer
// pinned and the action-row screen Ys correct. Used for BOTH the wiki render and
// every wiki scroll clamp so geometry never drifts.
func (m model) wikiBodyViewH() int { return max(m.bodyViewH()-m.wikiActionRowCount(), 1) }

// auditConnected reports whether we are post-connect: an audit has completed and
// carried results. Gates the wiki live-status column (a step's status word is only
// meaningful once the audit has run). Mirrors how the Dashboard treats
// dashAuditDone as "connected".
func (m model) auditConnected() bool {
	return m.dashAuditDone && len(m.dashAuditRaw) > 0
}
