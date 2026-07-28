package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/stats"
	"github.com/UberMorgott/morgward/internal/steps"
	"github.com/UberMorgott/morgward/internal/wiki"
)

// summaryBodyTopRow is the 0-based screen Y of the FIRST summary body line:
// top border (row 0) + switcher (row 1) → body starts at row 2. Mirrors
// formBodyTopRow; the fix-list hit-test derives each fix row's Y from this.
const summaryBodyTopRow = 2

// fixRowStyle renders a clickable fix-list row; a small status glyph + "[ID] title".
var (
	sumHeadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true) // section headers
	sumOKStyle   = monGreenStyle                                                    // OK glyph
	sumFailStyle = monRedStyle                                                      // FAIL glyph
)

// sumRowIndent is the leading indent of every metric row under a group header, and
// sumRowGap is the spacing between the (padded) label column and the value column.
const (
	sumRowIndent = "  "
	sumRowGap    = "  "
)

// sumOldStyle dims the pre-run ("old") value of a before→after pair so the eye lands
// on the new value; the arrow stays plain. Reuses the monitor's dim color (240).
var sumOldStyle = monDimStyle

// sumValue renders the value column of a metric row with the same suppression rules
// as the engine's formatDelta: "" when both sides are empty (caller drops the row),
// a lone value when only one side is known or both are equal, and a dimmed
// "old → new" pair when both are known and differ.
func sumValue(before, after string) string {
	switch {
	case before == "" && after == "":
		return ""
	case before == "":
		return after
	case after == "":
		return before
	case before == after:
		return before
	default:
		return sumOldStyle.Render(before) + " → " + after
	}
}

// sumRow lays out one aligned metric row: indent + label padded to labelW display
// cells + gap + value. The label pad is clamped so the value column never starts past
// innerW, and the whole line is truncated as a final guard so it can never overflow
// the box. Returns "" when value is empty so the caller skips the row.
func sumRow(label, value string, labelW, innerW int) string {
	if value == "" {
		return ""
	}
	// Clamp the label column so indent + label + gap + value fits innerW; never pad
	// the label narrower than its own text.
	rawW := lipgloss.Width(label)
	maxLabelW := innerW - lipgloss.Width(sumRowIndent) - lipgloss.Width(sumRowGap) - lipgloss.Width(value)
	if labelW > maxLabelW {
		labelW = maxLabelW
	}
	if labelW < rawW {
		labelW = rawW
	}
	line := sumRowIndent + padLabel(label, labelW) + sumRowGap + value
	return truncDisplay(line, innerW)
}

// summaryHeader builds the localized one-line tally:
// "applied X/Y · N skipped · N failed · reboots N · verify P/T".
func (m model) summaryHeader() string {
	s := m.summary
	verifyTotal := s.VerifyPassed + s.VerifyFailed
	return fmt.Sprintf("%s · %s · %s · %s · %s",
		fmt.Sprintf(t(m.lang, kSumApplied), s.Applied(), s.Total()),
		fmt.Sprintf(t(m.lang, kSumSkipped), s.Skip),
		fmt.Sprintf(t(m.lang, kSumFailed), s.Fail),
		fmt.Sprintf(t(m.lang, kSumReboots), s.Reboots),
		fmt.Sprintf(t(m.lang, kSumVerify), s.VerifyPassed, verifyTotal),
	)
}

// boolWordL renders a posture bool as the localized yes/no token.
func (m model) boolWordL(v bool) string {
	if v {
		return t(m.lang, kYesWord)
	}
	return t(m.lang, kNoWord)
}

// sumSectionKey / sumRowKey resolve the shared stats.Section / stats.RowKey to this
// package's localized string keys. The row SELECTION and suppression rules are
// shared with the engine's statsLines (stats.SummaryGroups); only the label lookup
// and the styled column layout are local.
var (
	sumSectionKey = [...]stringKey{
		stats.SecPkgKernel: kSecPkgKernel,
		stats.SecDiskMem:   kSecDiskMem,
		stats.SecNetwork:   kSecNetwork,
		stats.SecSecurity:  kSecSecurity,
	}
	sumRowKey = [...]stringKey{
		stats.RowUpgraded:  kRowUpgraded,
		stats.RowKernel:    kRowKernel,
		stats.RowPurged:    kRowPurged,
		stats.RowDiskUsed:  kRowDiskUsed,
		stats.RowZram:      kRowZram,
		stats.RowSpeed:     kRowSpeed,
		stats.RowPingGW:    kRowPingGW,
		stats.RowPingNet:   kRowPingNet,
		stats.RowPorts:     kRowPorts,
		stats.RowRootLogin: kRowRootLogin,
		stats.RowKeyOnly:   kRowKeyOnly,
		stats.RowFirewall:  kRowFirewall,
		stats.RowFail2ban:  kRowFail2ban,
	}
)

// statWords are the localized value tokens stats.SummaryGroups needs (yes/no
// posture, the zram "added" marker) — values, not labels, so i18n stays out of
// internal/stats.
func (m model) statWords() stats.Words {
	return stats.Words{
		Yes:       t(m.lang, kYesWord),
		No:        t(m.lang, kNoWord),
		ZramAdded: t(m.lang, kZramAdded),
	}
}

// summaryStatLines builds the four before/after metric blocks (packages/kernel,
// disk/memory, network, security). Both snapshots may be nil — when both are nil
// it returns nil so summaryView shows only the header + fix list. Rows are selected
// and suppressed by stats.SummaryGroups (shared with the engine's statsLines); this
// function only localizes the labels and lays out the aligned columns. RAM
// before→after lives ONLY in the top stats strip (so the operator sees it once).
func (m model) summaryStatLines(innerW int) []string {
	groups := stats.SummaryGroups(m.summary.Before, m.summary.After,
		m.summary.UpgradedPkgs, m.summary.PurgedPkgs, m.statWords())
	if groups == nil {
		return nil
	}

	// Each group renders a header followed by its rows, aligning every row's value to
	// the group's widest label (display cells, Cyrillic-aware). A group with no rows
	// still emits its header; SummaryGroups drops the network group entirely when it
	// has nothing to show.
	var out []string
	for _, g := range groups {
		if len(out) > 0 {
			out = append(out, "") // one blank line between groups
		}
		out = append(out, sumHeadStyle.Render(t(m.lang, sumSectionKey[g.Section])))
		labelW := 0
		for _, r := range g.Rows {
			if w := lipgloss.Width(t(m.lang, sumRowKey[r.Key])); w > labelW {
				labelW = w
			}
		}
		for _, r := range g.Rows {
			label := t(m.lang, sumRowKey[r.Key])
			if line := sumRow(label, sumValue(r.Before, r.After), labelW, innerW); line != "" {
				out = append(out, line)
			}
		}
	}
	return out
}

// fixListLines builds one rendered line per applied fix in m.summary.Results order:
// "<glyph> [ID] <localized title>". The slice index equals the fix's index in
// Results, so fixAtClick can recover each row's Y deterministically.
func (m model) fixListLines() []string {
	innerW := innerWidth(m.boxWidth())
	out := make([]string, 0, len(m.summary.Results))
	for _, r := range m.summary.Results {
		out = append(out, truncDisplay(m.fixRow(r), innerW))
	}
	return out
}

// fixRow composes a single fix-list row: glyph + "[ID] title", with a dim inline
// "· не требуется: <reason>" suffix for a benign StatusSkip so the neutral state and
// its reason live on ONE screen line (fixListLines truncates it to inner width — a
// wrap would desync every row's Y from its Results index). Shared with fixAtClick so
// the click width matches what is drawn.
func (m model) fixRow(r engine.StepResult) string {
	row := "  " + fixGlyph(r.Status) + " " + m.fixRowText(r)
	if r.Status == steps.StatusSkip && r.Detail != "" {
		row += monDimStyle.Render("  · " + t(m.lang, kFixNotNeeded) + ": " + localSkipReason(m.lang, r.Detail))
	}
	return row
}

// fixRowText is the (unstyled-glyph) "[ID] title" portion of a fix row: the wiki
// doc title when present, else the localized short step title, else the engine Title.
func (m model) fixRowText(r engine.StepResult) string {
	var title string
	if d, ok := wiki.Doc(wiki.Lang(int(m.lang)), r.ID); ok && d.Title != "" {
		title = d.Title
	} else {
		title = localStepTitle(m.lang, r.ID, r.Title)
	}
	return fmt.Sprintf("[%s] %s", r.ID, title)
}

// fixGlyph returns a small colored status marker for a fix row.
func fixGlyph(st steps.Status) string {
	switch st {
	case steps.StatusOK:
		return sumOKStyle.Render("✓")
	case steps.StatusFail:
		return sumFailStyle.Render("✗")
	case steps.StatusSkip:
		// Benign "not needed" state — neutral dim dash, not a yellow ∅ that reads as
		// "not applied / incomplete". The reason is shown inline by fixListLines.
		return monDimStyle.Render("—")
	default:
		return " "
	}
}

// summaryView renders the post-finish stats summary + clickable fix list inside the
// same bordered frame as runView. Layout (0-based screen rows):
//
//	row 0                 : main box top border
//	row 1                 : RU/EN switcher
//	rows 2..2+viewH-1     : the scrollable middle region — header, blank, stat blocks,
//	                        blank, fix-list header, then one clickable row per fix,
//	                        windowed to body[sumScroll : sumScroll+viewH]
//	...                   : hint, bottom border, then the 3-row monitor box (pinned)
//
// The middle region always emits exactly viewH rows (blank-padded when the body is
// shorter), so the monitor footer is ALWAYS pinned to the bottom regardless of the
// terminal size. When the body overflows viewH a scrollbar is drawn in the right
// border (renderScrollRegion) and the wheel / ↑↓ scroll it; fixAtClick reproduces the
// windowed geometry so clicks stay accurate.
func (m model) summaryView() string {
	// One row below the scroll region is reserved for the pinned clickable
	// [ На главную ] button (CHANGE 4), styled like the run back-pill, so it never
	// scrolls away and the monitor footer never moves.
	return m.framedScrollView(frame{
		title:  frameTitle(),
		nav:    m.switcherLine,
		body:   m.summaryBodyLines(), // strip + header + two-column block
		viewH:  m.summaryBodyViewH(),
		scroll: m.sumScroll,
		pinned: []string{pillOnStyle.Render(t(m.lang, kSumHomeButton))},
		hint:   helpStyle.Render(t(m.lang, kSummaryHint2)),
	})
}

// summaryBodyViewH is the scrollable middle height on the summary screen: the shared
// chrome minus the one fixed row reserved for the pinned home button.
func (m model) summaryBodyViewH() int { return m.chromeViewH(0, 1) }

// summaryHomeRow is the FIXED screen Y of the pinned [ На главную ] button: it follows
// the 2 chrome rows and the scrollable region, so it never moves with the scroll offset.
func (m model) summaryHomeRow() int { return summaryBodyTopRow + m.summaryBodyViewH() }

// summaryStatStrip builds the compact one-line stats strip under the header:
// RAM before→after, datacenter + internet ping, and a posture token (BBR / firewall)
// when known. Each segment is dropped when its data is unavailable; "" when nothing
// is known (the caller then omits the strip). The whole line is clamped to innerW.
func (m model) summaryStatStrip(innerW int) string {
	b, a := m.summary.Before, m.summary.After
	if b == nil && a == nil {
		return ""
	}
	empty := stats0()
	if b == nil {
		b = empty
	}
	if a == nil {
		a = empty
	}
	var segs []string
	// RAM before→after (NEW): MemKB is the total RAM; show old→new humanized.
	if ram := sumValue(stats.MemStr(b), stats.MemStr(a)); ram != "" {
		segs = append(segs, t(m.lang, kSumRAM)+" "+ram)
	}
	if p := sumValue(stats.Num1(b.GatewayPingMs), stats.Num1(a.GatewayPingMs)); p != "" {
		segs = append(segs, t(m.lang, kRowPingGW)+" "+p)
	}
	if p := sumValue(stats.Num1(b.InternetPingMs), stats.Num1(a.InternetPingMs)); p != "" {
		segs = append(segs, t(m.lang, kRowPingNet)+" "+p)
	}
	// Posture token: firewall on/off after the run.
	if a.FirewallActive {
		segs = append(segs, t(m.lang, kRowFirewall)+" "+m.boolWordL(true))
	}
	if len(segs) == 0 {
		return ""
	}
	return truncDisplay(strings.Join(segs, "  ·  "), innerW)
}

// summaryAccessRows builds the right-column "SSH-ДОСТУП" rows from the After
// snapshot: root login, key-only, and (when a key was generated this run) a clickable
// "ключ ‹показать›" row. The second return value reports the row index of that
// clickable key row within the access-rows slice (-1 when absent), so the hit-test
// can map a click to "open the key viewer".
func (m model) summaryAccessRows() (rows []string, keyShowIdx int) {
	keyShowIdx = -1
	a := m.summary.After
	if a == nil {
		a = stats0()
	}
	rows = append(rows, "  "+labelStyle.Render(t(m.lang, kRowRootLogin)+": ")+valueOrDash(a.RootLogin))
	rows = append(rows, "  "+labelStyle.Render(t(m.lang, kRowKeyOnly)+": ")+m.boolWordL(a.KeyOnly))
	if m.keyGenerated && m.keyPEM != "" {
		keyShowIdx = len(rows)
		rows = append(rows, "  "+focusStyle.Render(t(m.lang, kSumKeyShow)))
	} else {
		rows = append(rows, "  "+labelStyle.Render(t(m.lang, kSumKeyAdded)+": ")+m.boolWordL(a.KeyOnly || a.RootLogin != ""))
	}
	return rows, keyShowIdx
}

// valueOrDash returns v, or "—" when v is empty, so a missing snapshot field shows a
// neutral placeholder rather than a blank.
func valueOrDash(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

// summaryTwoCol reports whether the terminal is wide enough for the two-column body.
// Below this, the layout stacks into a single column.
func (m model) summaryTwoCol(innerW int) bool {
	return innerW >= 2*minSummaryColW+sumColGap
}

const (
	// minSummaryColW is the smallest per-column width that still fits a useful fix /
	// access row; below it (each of two columns narrower) the body stacks to one column.
	minSummaryColW = 26
	// sumColGap is the spaces between the two summary columns.
	sumColGap = 3
)

// summaryBodyLines builds the ordered body line slice — the single source of truth for
// BOTH summaryView's render and the hit-tests. Layout: header (line 0), optional stats
// strip, blank, then the two-column block (LEFT = ФИКСЫ list, RIGHT = SSH-ДОСТУП) when
// wide enough, else the two columns stacked. fixAtClick / summaryKeyShowAtClick
// reconstruct the SAME geometry so clicks stay accurate.
func (m model) summaryBodyLines() []string {
	innerW := innerWidth(m.boxWidth())
	var body []string
	body = append(body, m.summaryHeader())
	if strip := m.summaryStatStrip(innerW); strip != "" {
		body = append(body, strip)
	}

	left := m.summaryLeftColumn()
	right := m.summaryRightColumn()

	body = append(body, "")
	if m.summaryTwoCol(innerW) {
		body = append(body, m.zipColumns(left, right, innerW)...)
	} else {
		// Stacked single column: LEFT block, blank, RIGHT block.
		body = append(body, left...)
		body = append(body, "")
		body = append(body, right...)
	}
	return body
}

// summaryLeftColumn is the "ФИКСЫ" column: a header then one clickable row per fix.
func (m model) summaryLeftColumn() []string {
	col := []string{sumHeadStyle.Render(t(m.lang, kSumColFixes))}
	col = append(col, m.fixListLines()...)
	return col
}

// summaryRightColumn is the "SSH-ДОСТУП" column: a header then the access rows
// (root login / key-only / key-added or clickable key-show), plus the compact stat
// blocks (disk/zram, packages) so the detailed metrics still have a home.
func (m model) summaryRightColumn() []string {
	col := []string{sumHeadStyle.Render(t(m.lang, kSecColTitle))}
	rows, _ := m.summaryAccessRows()
	col = append(col, rows...)
	if blocks := m.summaryStatLines(innerWidth(m.boxWidth())); len(blocks) > 0 {
		col = append(col, "")
		col = append(col, blocks...)
	}
	return col
}

// zipColumns renders two columns side by side: left cell padded to colW so the right
// cell begins at a fixed X. Rows beyond the shorter column render the longer one's
// remaining cells (left-padded for right-only rows). colW is half the inner width
// minus the gap. Display-width math is lipgloss-based (Cyrillic-safe).
func (m model) zipColumns(left, right []string, innerW int) []string {
	colW := (innerW - sumColGap) / 2
	n := max(len(left), len(right))
	out := make([]string, 0, n)
	pad := func(s string) string {
		s = truncDisplay(s, colW)
		if d := colW - lipgloss.Width(s); d > 0 {
			s += strings.Repeat(" ", d)
		}
		return s
	}
	for i := range n {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, pad(l)+strings.Repeat(" ", sumColGap)+truncDisplay(r, colW))
	}
	return out
}

// fixAtClick maps a click at (x,y) to a fix-list row's step ID, accounting for the
// scroll offset and the two-column layout. The middle region occupies screen rows
// [summaryBodyTopRow, summaryBodyTopRow+viewH); a click there maps to body index
// sumScroll+(y-top). The fix rows live in the LEFT column (after the ФИКСЫ header),
// so a hit requires X to fall in the left column AND the body row to map to a fix.
func (m model) fixAtClick(x, y int) (string, bool) {
	if m.phase != phaseSummary || len(m.summary.Results) == 0 {
		return "", false
	}
	innerW := innerWidth(m.boxWidth())
	bodyIdx, ok := m.summaryRowAtClick(y)
	if !ok {
		return "", false
	}
	twoCol := m.summaryTwoCol(innerW)
	colW := (innerW - sumColGap) / 2
	const contentX0 = 2 // borderLeft(1) + space(1)
	// In two-column mode the fixes are the LEFT column; require X within it.
	if twoCol && x >= contentX0+colW {
		return "", false
	}

	// Resolve which grid row of the body this is, then which left-column entry.
	gridStart := m.summaryColBlockStart()
	rowInBlock := bodyIdx - gridStart
	if rowInBlock < 0 {
		return "", false
	}
	left := m.summaryLeftColumn()
	// Stacked mode: the left block is contiguous from gridStart; two-col mode: each
	// grid row holds left[rowInBlock] in its left cell (the zip is row-aligned).
	var leftIdx int
	if twoCol {
		leftIdx = rowInBlock
	} else {
		leftIdx = rowInBlock // stacked: left block starts at gridStart too
		if leftIdx >= len(left) {
			return "", false // past the left block into the blank/right block
		}
	}
	// leftIdx 0 is the ФИКСЫ header; fix rows are leftIdx 1.. .
	fixIdx := leftIdx - 1
	if fixIdx < 0 || fixIdx >= len(m.summary.Results) {
		return "", false
	}
	row := m.fixRow(m.summary.Results[fixIdx])
	w := lipgloss.Width(truncDisplay(row, colW))
	if x >= contentX0 && x < contentX0+w {
		return m.summary.Results[fixIdx].ID, true
	}
	return "", false
}

// summaryColBlockStart is the body index where the two-column (or stacked) block
// begins: after the header, the optional stats strip, and the one blank separator.
func (m model) summaryColBlockStart() int {
	innerW := innerWidth(m.boxWidth())
	idx := 1 // header
	if m.summaryStatStrip(innerW) != "" {
		idx++
	}
	idx++ // the blank separator before the block
	return idx
}

// summaryRowAtClick maps a screen Y to a body index in the scrollable region, honoring
// the scroll offset, or ok=false when Y is in the chrome (switcher/home/hint/monitor).
func (m model) summaryRowAtClick(y int) (int, bool) {
	return scrollRowToBodyIdx(y, summaryBodyTopRow,
		len(m.summaryBodyLines()), m.summaryBodyViewH(), m.sumScroll)
}

// summaryKeyShowAtClick reports whether the click at (x,y) hit the right-column
// "ключ ‹показать›" row (only present when a key was generated this run). It mirrors
// the two-column / stacked geometry of summaryBodyLines.
func (m model) summaryKeyShowAtClick(x, y int) bool {
	if m.phase != phaseSummary {
		return false
	}
	_, keyShowIdx := m.summaryAccessRows()
	if keyShowIdx < 0 {
		return false
	}
	innerW := innerWidth(m.boxWidth())
	bodyIdx, ok := m.summaryRowAtClick(y)
	if !ok {
		return false
	}
	twoCol := m.summaryTwoCol(innerW)
	colW := (innerW - sumColGap) / 2
	const contentX0 = 2
	gridStart := m.summaryColBlockStart()
	rowInBlock := bodyIdx - gridStart
	if rowInBlock < 0 {
		return false
	}
	// Right-column entry index (0 = SSH-ДОСТУП header; rows[i] is access-row i, so the
	// key-show row sits at rightIdx = 1+keyShowIdx).
	wantRightIdx := 1 + keyShowIdx
	if twoCol {
		// Two-column: right cell begins at contentX0+colW+sumColGap. Require X there.
		rightX0 := contentX0 + colW + sumColGap
		if x < rightX0 {
			return false
		}
		return rowInBlock == wantRightIdx
	}
	// Stacked: right block follows the left block + one blank. Left block length =
	// 1 header + N fixes; then a blank; then the right block.
	leftLen := 1 + len(m.summary.Results)
	rightStart := leftLen + 1
	return rowInBlock == rightStart+wantRightIdx && x >= contentX0
}

// summaryHomeAtClick reports whether the click at (x,y) hit the pinned [ На главную ]
// button. X spans the rendered button width from the content column (absolute X = 2).
func (m model) summaryHomeAtClick(x, y int) bool {
	if m.phase != phaseSummary {
		return false
	}
	if y != m.summaryHomeRow() {
		return false
	}
	const contentX0 = 2
	w := lipgloss.Width(t(m.lang, kSumHomeButton))
	return x >= contentX0 && x < contentX0+w
}

// summaryGoHome navigates from the summary to the post-connect home: the Dashboard
// when we have audit facts to render it (the hub reachable from a connected session),
// else back to the landing form. Mirrors the existing nav (goBack / phaseDashboard).
func (m model) summaryGoHome() (tea.Model, tea.Cmd) {
	if m.dashFacts != nil {
		// A mutating run since the last audit left the Dashboard checkmarks stale.
		// Re-run the read-only audit via the SAME machinery the connect path uses
		// (command="audit" → start()); its final Done lands back on the Dashboard
		// and captureAudit repopulates the dash fields with the post-apply state.
		if m.dashStale {
			m.dashStale = false
			m.command = "audit"
			return m.start()
		}
		m.phase = phaseDashboard
		m.dashScroll = 0
		return m, nil
	}
	return m.goBack()
}

// stats0 returns a pointer to a zero Snapshot, used when one side is nil so the
// delta helpers see "unknown" rather than dereferencing nil.
func stats0() *stats.Snapshot { return &stats.Snapshot{} }
