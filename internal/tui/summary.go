package tui

import (
	"fmt"
	"strconv"
	"strings"

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

	// ДИСК И ПАМЯТЬ.
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

	body := m.summaryBodyLines() // header + blocks + fix-list header + fix rows

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// Scrollable middle region (the only resizable part); the chrome above (2 rows)
	// and below (hint + bottom + 3-row monitor) is fixed, so the footer never moves.
	viewH := m.bodyViewH()
	off := clampScroll(m.sumScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	// Hint + main box bottom border.
	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, kSummaryHint)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	// Monitor box (kept alive — sampler still running on the summary screen).
	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// summaryBodyLines builds the ordered body line slice (the single source of truth
// for BOTH summaryView's render and fixAtClick's geometry): header, blank, stat
// blocks (possibly empty), blank, fix-list header, then one row per fix.
func (m model) summaryBodyLines() []string {
	var body []string
	body = append(body, m.summaryHeader())
	if blocks := m.summaryStatLines(innerWidth(m.boxWidth())); len(blocks) > 0 {
		body = append(body, "")
		body = append(body, blocks...)
	}
	if len(m.summary.Results) > 0 {
		body = append(body, "")
		body = append(body, sumHeadStyle.Render(t(m.lang, kSecFixes)))
		body = append(body, m.fixListLines()...)
	}
	return body
}

// fixAtClick maps a click at (x,y) to a fix-list row's step ID, accounting for the
// scroll offset. The middle region occupies screen rows [summaryBodyTopRow,
// summaryBodyTopRow+viewH); a click there maps to body index sumScroll+(y-top), and
// the fix rows are the tail of the body (after the header + stat blocks + fix-list
// header). X must fall within the rendered row width. Returns ok=false on a miss.
func (m model) fixAtClick(x, y int) (string, bool) {
	if m.phase != phaseSummary || len(m.summary.Results) == 0 {
		return "", false
	}
	body := m.summaryBodyLines()
	viewH := m.bodyViewH()
	off := clampScroll(m.sumScroll, len(body), viewH)

	rowInRegion := y - summaryBodyTopRow
	if rowInRegion < 0 || rowInRegion >= viewH {
		return "", false // click is in the chrome (switcher/hint/border/monitor), not the body
	}
	bodyIdx := off + rowInRegion
	fixStart := len(body) - len(m.summary.Results) // fix rows are the body tail
	idx := bodyIdx - fixStart
	if idx < 0 || idx >= len(m.summary.Results) {
		return "", false
	}
	const contentX0 = 2 // borderLeft(1) + space(1)
	row := "  " + fixGlyph(m.summary.Results[idx].Status) + " " + m.fixRowText(m.summary.Results[idx])
	w := lipgloss.Width(truncDisplay(row, innerWidth(m.boxWidth())))
	if x >= contentX0 && x < contentX0+w {
		return m.summary.Results[idx].ID, true
	}
	return "", false
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
