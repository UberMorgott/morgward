package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/stats"
	"github.com/UberMorgott/morgward/internal/steps"
	"github.com/UberMorgott/morgward/internal/version"
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
	sumSkipStyle = monYellowStyle                                                   // SKIP glyph
)

// humanKB64 renders an int64 KB value via the float helper (engine snapshots use
// int64; the monitor footer uses float64). Empty string when total is unknown is
// handled by the caller — this always returns a value.
func humanKB64(kb int64) string { return humanKB(float64(kb)) }

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

// summaryStatLines builds the four before/after metric blocks (packages/kernel,
// disk/memory, network, security). Both snapshots may be nil — when both are nil
// it returns nil so summaryView shows only the header + fix list. Mirrors the
// engine's statsLines suppression: rows with unknown data are dropped.
func (m model) summaryStatLines(innerW int) []string {
	s := m.summary
	if s.Before == nil && s.After == nil {
		return nil
	}
	b, a := s.Before, s.After
	empty := stats0()
	if b == nil {
		b = empty
	}
	if a == nil {
		a = empty
	}

	// statRow is a label + already-rendered value column; empty values are dropped.
	type statRow struct{ label, value string }

	// emitGroup renders a header followed by its rows, aligning every row's value to
	// the group's widest label (display cells, Cyrillic-aware). A group with no
	// non-empty rows still emits its header (mirrors the prior always-on sections);
	// the network block decides on its own whether to call this at all.
	var out []string
	emitGroup := func(headK stringKey, rows []statRow) {
		if len(out) > 0 {
			out = append(out, "") // one blank line between groups
		}
		out = append(out, sumHeadStyle.Render(t(m.lang, headK)))
		labelW := 0
		for _, r := range rows {
			if r.value == "" {
				continue
			}
			if w := lipgloss.Width(r.label); w > labelW {
				labelW = w
			}
		}
		for _, r := range rows {
			if line := sumRow(r.label, r.value, labelW, innerW); line != "" {
				out = append(out, line)
			}
		}
	}

	// ПАКЕТЫ И ЯДРО.
	var pkg []statRow
	if s.UpgradedPkgs > 0 {
		pkg = append(pkg, statRow{t(m.lang, kRowUpgraded), strconv.Itoa(s.UpgradedPkgs)})
	}
	pkg = append(pkg, statRow{t(m.lang, kRowKernel), sumValue(b.KernelVer, a.KernelVer)})
	if s.PurgedPkgs > 0 {
		pkg = append(pkg, statRow{t(m.lang, kRowPurged), strconv.Itoa(s.PurgedPkgs)})
	}
	emitGroup(kSecPkgKernel, pkg)

	// ДИСК И ПАМЯТЬ. RAM before→after lives ONLY in the top stats strip (so the
	// operator sees it once) — do not repeat it here.
	disk := []statRow{{t(m.lang, kRowDiskUsed), sumValue(sumDiskStr(b), sumDiskStr(a))}}
	if !b.ZramActive && a.ZramActive {
		disk = append(disk, statRow{t(m.lang, kRowZram), t(m.lang, kZramAdded)})
	}
	emitGroup(kSecDiskMem, disk)

	// СЕТЬ (whole block dropped when there is no speed/ping data on either side).
	var net []statRow
	if b.SpeedMBs > 0 || a.SpeedMBs > 0 {
		net = append(net, statRow{t(m.lang, kRowSpeed), sumValue(sumSpeedStr(b.SpeedMBs), sumSpeedStr(a.SpeedMBs))})
	}
	if b.GatewayPingMs > 0 || a.GatewayPingMs > 0 {
		net = append(net, statRow{t(m.lang, kRowPingGW), sumValue(sumSpeedStr(b.GatewayPingMs), sumSpeedStr(a.GatewayPingMs))})
	}
	if b.InternetPingMs > 0 || a.InternetPingMs > 0 {
		net = append(net, statRow{t(m.lang, kRowPingNet), sumValue(sumSpeedStr(b.InternetPingMs), sumSpeedStr(a.InternetPingMs))})
	}
	if len(net) > 0 {
		emitGroup(kSecNetwork, net)
	}

	// БЕЗОПАСНОСТЬ.
	sec := []statRow{
		{t(m.lang, kRowPorts), sumValue(sumPortsStr(b.OpenPorts), sumPortsStr(a.OpenPorts))},
		{t(m.lang, kRowRootLogin), sumValue(b.RootLogin, a.RootLogin)},
		{t(m.lang, kRowKeyOnly), sumValue(m.boolWordL(b.KeyOnly), m.boolWordL(a.KeyOnly))},
		{t(m.lang, kRowFirewall), sumValue(m.boolWordL(b.FirewallActive), m.boolWordL(a.FirewallActive))},
		{t(m.lang, kRowFail2ban), sumValue(m.boolWordL(b.Fail2banActive), m.boolWordL(a.Fail2banActive))},
	}
	emitGroup(kSecSecurity, sec)

	return out
}

// fixListLines builds one rendered line per applied fix in m.summary.Results order:
// "<glyph> [ID] <localized title>". The slice index equals the fix's index in
// Results, so fixAtClick can recover each row's Y deterministically.
func (m model) fixListLines() []string {
	out := make([]string, 0, len(m.summary.Results))
	for _, r := range m.summary.Results {
		out = append(out, "  "+fixGlyph(r.Status)+" "+m.fixRowText(r))
	}
	return out
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
		return sumSkipStyle.Render("∅")
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
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body := m.summaryBodyLines() // strip + header + two-column block

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// Scrollable middle region (the only resizable part). One row is reserved below
	// it for the pinned [ На главную ] button, so the home button never scrolls away;
	// the chrome above (2 rows) and below (button + hint + bottom + 3-row monitor) is
	// fixed, so the footer never moves.
	viewH := m.summaryBodyViewH()
	off := clampScroll(m.sumScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	// Pinned clickable "На главную" button row (CHANGE 4), styled like the run back-pill.
	sb.WriteString(contentLine(b, pillOnStyle.Render(t(m.lang, kSumHomeButton)), innerW))
	sb.WriteByte('\n')

	// Hint + main box bottom border.
	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, kSummaryHint2)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	// Monitor box (kept alive — sampler still running on the summary screen).
	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// summaryBodyViewH is the scrollable middle height on the summary screen: the shared
// bodyViewH minus the one fixed row reserved for the pinned home button, floored at 1.
func (m model) summaryBodyViewH() int { return max(m.bodyViewH()-1, 1) }

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
	if ram := sumValue(sumMemStr(b), sumMemStr(a)); ram != "" {
		segs = append(segs, t(m.lang, kSumRAM)+" "+ram)
	}
	if p := sumValue(sumSpeedStr(b.GatewayPingMs), sumSpeedStr(a.GatewayPingMs)); p != "" {
		segs = append(segs, t(m.lang, kRowPingGW)+" "+p)
	}
	if p := sumValue(sumSpeedStr(b.InternetPingMs), sumSpeedStr(a.InternetPingMs)); p != "" {
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

// sumMemStr humanizes a snapshot's total RAM (MemKB), or "" when unknown.
func sumMemStr(s *stats.Snapshot) string {
	if s.MemKB <= 0 {
		return ""
	}
	return humanKB64(s.MemKB)
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
	for i := 0; i < n; i++ {
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
	row := "  " + fixGlyph(m.summary.Results[fixIdx].Status) + " " + m.fixRowText(m.summary.Results[fixIdx])
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
	body := m.summaryBodyLines()
	viewH := m.summaryBodyViewH()
	off := clampScroll(m.sumScroll, len(body), viewH)
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
		m.phase = phaseDashboard
		m.dashScroll = 0
		return m, nil
	}
	return m.goBack()
}

// stats0 returns a pointer to a zero Snapshot, used when one side is nil so the
// delta helpers see "unknown" rather than dereferencing nil.
func stats0() *stats.Snapshot { return &stats.Snapshot{} }

func sumDiskStr(s *stats.Snapshot) string {
	if s.DiskTotalKB <= 0 {
		return ""
	}
	return humanKB64(s.DiskUsedKB) + "/" + humanKB64(s.DiskTotalKB)
}

func sumSpeedStr(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func sumPortsStr(p []string) string {
	if len(p) == 0 {
		return ""
	}
	return strconv.Itoa(len(p))
}
