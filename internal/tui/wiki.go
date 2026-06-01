package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/version"
	"github.com/UberMorgott/morgward/internal/wiki"
)

// wikiBackStartCol is the absolute X where the wiki "← Назад" pill begins: 2
// (left border + the leading content space added by contentLine).
const wikiBackStartCol = 2

// wikiBackRow is the screen Y of the back-button row in wikiView: it sits right
// after the scrollable middle region (rows [summaryBodyTopRow, +viewH)).
func (m model) wikiBackRow() int {
	return summaryBodyTopRow + m.wikiBodyViewH()
}

// wikiBackAtClick reports whether (x,y) hit the wiki "← Назад" pill, mirroring
// dashButtonAtClick's pillRanges geometry for one pill.
func (m model) wikiBackAtClick(x, y int) bool {
	if m.phase != phaseWiki || y != m.wikiBackRow() {
		return false
	}
	return pillIndexAt([]string{t(m.lang, kWikiBack)}, wikiBackStartCol, x) == 0
}

// tweakBucketIDs is the canonical Tweaks-bucket step ID set applied by the
// Dashboard "Применить твики" action (everything EXCEPT the security/access steps
// A2/A2.5, which live behind the Security menu). selectSteps re-orders these into
// the load-bearing apply order, so the literal order here is not significant.
func tweakBucketIDs() []string {
	return []string{"A1", "A3", "A4", "A5", "A6", "A6.5", "A6.7", "A7", "A8", "A9", "A10"}
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

	// Clickable "← Назад" pill pinned just above the hint line (its own fixed-chrome
	// row, hit-tested by wikiBackAtClick). Styled like the dashboard action pills.
	sb.WriteString(contentLine(b, pillOnStyle.Render(t(m.lang, kWikiBack)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, kWikiHint)), innerW))
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

// wikiBodyViewH is bodyViewH minus one row, because the wiki screen carries an
// extra fixed-chrome row (the clickable "← Назад" pill) above the hint. Used for
// BOTH the wiki render and every wiki scroll clamp so geometry never drifts.
func (m model) wikiBodyViewH() int { return max(m.bodyViewH()-1, 1) }

// auditConnected reports whether we are post-connect: an audit has completed and
// carried results. Gates the wiki live-status column (a step's status word is only
// meaningful once the audit has run). Mirrors how the Dashboard treats
// dashAuditDone as "connected".
func (m model) auditConnected() bool {
	return m.dashAuditDone && len(m.dashAuditRaw) > 0
}
