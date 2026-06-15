package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/UberMorgott/morgward/internal/version"
)

// Files-tab (wsFiles) screen layout. The frame reuses the SAME box chrome as the
// terminal tab (titledTop / switcher / scroll region / hint / bottom border / pinned
// monitor footer). The fixed header rows above the scrollable listing are, by screen Y:
//
//	row 0               : top border (titledTop)
//	row 1 (switcherRow) : tab strip "[ Terminal ][▸Files ]" + RU|EN switcher (right)
//	row 2               : address bar (cwd)
//	row 3               : column header (Name / Size / Perms / Modified)
//	rows 4..4+viewH-1    : scrollable listing region  ← filesListTopRow
//	then                : action-bar pills, hint, bottom border, monitor footer (3)
//
// filesListTopRow is the FIXED screen Y of the first listing row; the row hit-test
// (filesRowAtClick) recovers an entry index from it + the scroll offset, exactly like
// dashAuditRowAtClick does off dashScrollTopRow.
const filesListTopRow = 4

// filesAddressRow / filesColHeaderRow are the fixed Y of the address bar and column
// header (between the tab strip and the listing).
const (
	filesAddressRow   = 2
	filesColHeaderRow = 3
)

// filesChromeRows is the number of NON-listing rows the Files frame spends: the 4 header
// rows above the listing (border, tab strip, address bar, column header) + the action
// bar (1) + hint (1) + bottom border (1) + the pinned 3-row monitor box = 10.
const filesChromeRows = filesListTopRow + 1 + 1 + 1 + 3

// filesListViewH is the height (rows) of the scrollable listing region: the terminal
// height minus the fixed chrome, floored at 1 so the region never vanishes.
func (m model) filesListViewH() int { return maxi(m.h-filesChromeRows, 1) }

// fileColWidths returns the fixed column widths (size / perms / mtime) for the listing;
// the name column takes the remaining inner width. Sizes are conservative so the columns
// align without overflowing a narrow box.
const (
	fileSizeColW  = 10 // right-aligned bytes
	filePermsColW = 10 // "rwxr-xr-x"
	fileMTimeColW = 16 // "2026-06-09 09:20"
	fileColGap    = 1  // one space between columns
)

// filesView renders the full Files-tab screen with the shared box chrome + monitor
// footer. The listing is windowed through renderScrollRegion (same scroll/scrollbar
// pattern terminalView uses); the address bar and column header are fixed chrome above it.
func (m model) filesView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()
	viewH := m.filesListViewH()

	var sb strings.Builder
	title := " " + version.Name + " · " + t(m.lang, kFmTabFiles) + " "
	sb.WriteString(titledTop(b, title, bw))
	sb.WriteByte('\n')

	// Tab strip on the switcher row, with the RU|EN switcher right-aligned on the same
	// line (mirrors switcherLine's right-alignment so the two never collide on width).
	sb.WriteString(m.filesTabStripLine(b, innerW))
	sb.WriteByte('\n')

	// Address bar (cwd) and column header — fixed chrome above the listing. Size the
	// editable input to the content area minus the "◂ " prefix so its cursor view fits the
	// frame (contentLine still truncates as a backstop). SetWidth is deterministic +
	// idempotent, so doing it at render time keeps the geometry self-consistent.
	if m.files != nil {
		m.files.addr.SetWidth(maxi(innerW-2, 8))
	}
	sb.WriteString(contentLine(b, m.filesAddressText(), innerW))
	sb.WriteByte('\n')
	sb.WriteString(contentLine(b, helpStyle.Render(m.filesColHeader(innerW)), innerW))
	sb.WriteByte('\n')

	// Listing region (scroll-windowed, scrollbar in the right border on overflow).
	body := m.filesBody()
	off := clampScroll(m.filesScrollOff(), len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	// Action bar (pills) + status/prompt line + bottom border. The status line shows (in
	// priority order): an open prompt/confirm, else the last op notice/error (f.err), else
	// the ops shortcut hint.
	sb.WriteString(contentLine(b, m.filesActionBar(), innerW))
	sb.WriteByte('\n')
	sb.WriteString(contentLine(b, m.filesStatusLine(innerW), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	// Pinned monitor footer (same as terminalView/summaryView).
	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// filesScrollOff clamps the file session's scroll offset so the selected row stays in
// view; nil-safe (no session → 0).
func (m model) filesScrollOff() int {
	if m.files == nil {
		return 0
	}
	return m.files.scroll
}

// filesAddressText is the address-bar content: a dim "◂ " prefix + the cwd. When the
// address bar is FOCUSED it shows the editable textinput's view (cursor + typed path);
// otherwise it shows the current cwd as static accent text. Enter navigates, Esc cancels
// (see filesKey).
func (m model) filesAddressText() string {
	if m.files == nil {
		return helpStyle.Render("◂ ") + focusStyle.Render("/")
	}
	if m.files.addrFocus {
		return helpStyle.Render("◂ ") + m.files.addr.View()
	}
	cwd := "/"
	if m.files.cwd != "" {
		cwd = m.files.cwd
	}
	return helpStyle.Render("◂ ") + focusStyle.Render(cwd)
}

// filesColHeader is the dim column-header line aligned to the same columns the listing
// rows use (Name | Size | Perms | Modified).
func (m model) filesColHeader(innerW int) string {
	nameW := m.fileNameColW(innerW)
	return padRightDisplay(t(m.lang, kFmColName), nameW) + strings.Repeat(" ", fileColGap) +
		padLeftDisplay(t(m.lang, kFmColSize), fileSizeColW) + strings.Repeat(" ", fileColGap) +
		padRightDisplay(t(m.lang, kFmColPerms), filePermsColW) + strings.Repeat(" ", fileColGap) +
		padRightDisplay(t(m.lang, kFmColMTime), fileMTimeColW)
}

// fileNameColW is the width of the (flexible) name column: the inner width minus the
// fixed columns + gaps, floored so it never goes negative on a narrow box.
func (m model) fileNameColW(innerW int) int {
	fixed := fileSizeColW + filePermsColW + fileMTimeColW + 3*fileColGap
	return maxi(innerW-fixed, 8)
}

// filesBody assembles the (un-windowed) listing lines for the scroll region: one styled
// row per fileEntry, columns Name / Size / Perms / Modified, the selected row banded with
// fileSelStyle. An empty listing yields a single dim placeholder. Nil-safe.
func (m model) filesBody() []string {
	if m.files == nil {
		return []string{helpStyle.Render(t(m.lang, kFmEmpty))}
	}
	innerW := innerWidth(m.boxWidth())
	nameW := m.fileNameColW(innerW)
	vis := m.files.visibleEntries()
	if len(vis) == 0 {
		return []string{helpStyle.Render(t(m.lang, kFmEmpty))}
	}
	lines := make([]string, len(vis))
	for i, e := range vis {
		lines[i] = m.fileRow(e, i == m.files.sel, nameW)
	}
	return lines
}

// fileRow formats one listing row to exactly innerW-ish display cells (the name column is
// padded/truncated to nameW so the fixed columns align), highlighting the selected row.
// Directory names carry a trailing "/"; symlinks show "name → target".
func (m model) fileRow(e fileEntry, selected bool, nameW int) string {
	name := e.name
	if e.isDir {
		name += "/"
	} else if e.isLink && e.target != "" {
		name += " → " + e.target
	}
	row := padRightDisplay(name, nameW) + strings.Repeat(" ", fileColGap) +
		padLeftDisplay(humanFileSize(e.size), fileSizeColW) + strings.Repeat(" ", fileColGap) +
		padRightDisplay(e.mode, filePermsColW) + strings.Repeat(" ", fileColGap) +
		padRightDisplay(e.mtime, fileMTimeColW)
	if selected {
		return fileSelStyle.Render(row)
	}
	return row
}

// filesActionBar renders the bottom action-pill row (dim pills). Clicks resolve to ops via
// filesActionAtClick (same labels + start col).
func (m model) filesActionBar() string {
	return " " + strings.Join(m.filesActionLabels(), " ")
}

// filesStatusLine is the line below the action bar: an open text prompt (label + input),
// an open confirm (label + y/n hint), the last op notice/error (f.err), or the ops shortcut
// hint when idle. Priority: prompt > confirm > notice > hint.
func (m model) filesStatusLine(innerW int) string {
	f := m.files
	if f != nil && f.transferring {
		return tipStyle.Render("⟳ " + t(m.lang, kFmTransferring) + " " + f.xferLabel + "…")
	}
	if f != nil && f.prompting() {
		if f.isConfirm() {
			return tipStyle.Render(f.promptMsg) + "  " + helpStyle.Render(t(m.lang, kFmConfirmYesNo))
		}
		f.prompt.SetWidth(maxi(innerW-lipgloss.Width(f.promptMsg)-2, 8))
		return tipStyle.Render(f.promptMsg) + " " + f.prompt.View()
	}
	if f != nil && f.err != "" {
		return tipStyle.Render(truncDisplay(f.err, innerW))
	}
	return helpStyle.Render(t(m.lang, kFmOpsHint))
}

// filesActionNames is the ordered RAW action-bar pill labels (unstyled) — the SINGLE source
// for both the render path (filesActionLabels) and the click hit-test (filesActionAtClick),
// so their x-geometry cannot diverge.
func (m model) filesActionNames() []string {
	return []string{
		t(m.lang, kFmActNew),
		t(m.lang, kFmActOpen),
		t(m.lang, kFmActDownload),
		t(m.lang, kFmActUpload),
		t(m.lang, kFmActRename),
		t(m.lang, kFmActDelete),
	}
}

// filesActionLabels renders the raw names into dim pills for the action bar.
func (m model) filesActionLabels() []string {
	names := m.filesActionNames()
	pills := make([]string, len(names))
	for i, n := range names {
		pills[i] = pillStyle.Render(n)
	}
	return pills
}

// fmAction enumerates the action-bar pills resolved by filesActionAtClick.
type fmAction int

const (
	fmActNone fmAction = iota
	fmActNew
	fmActOpen
	fmActDownload
	fmActUpload
	fmActRename
	fmActDelete
)

// filesActionStartCol is the absolute X where the first action pill begins: 2 (left border +
// the leading space filesActionBar prepends). Matches the contentLine content origin.
const filesActionStartCol = 2

// filesActionRowY is the FIXED screen Y of the action-bar row: it follows the 4 fixed header
// rows + the listing region (filesListTopRow + viewH). Pinned chrome (does not scroll).
func (m model) filesActionRowY() int {
	return filesListTopRow + m.filesListViewH()
}

// filesActionAtClick maps a click at (x,y) to an action pill (pillRanges over the same raw
// labels + start col the action bar rendered), or fmActNone on a miss.
func (m model) filesActionAtClick(x, y int) fmAction {
	if m.wsTab != wsFiles || y != m.filesActionRowY() {
		return fmActNone
	}
	switch pillIndexAt(m.filesActionNames(), filesActionStartCol, x) {
	case 0:
		return fmActNew
	case 1:
		return fmActOpen
	case 2:
		return fmActDownload
	case 3:
		return fmActUpload
	case 4:
		return fmActRename
	case 5:
		return fmActDelete
	}
	return fmActNone
}

// --- tab strip ----------------------------------------------------------------

// filesTabStripLine renders the "[ Terminal ][▸Files ]" tab strip on the switcher row,
// the active tab marked, with the RU|EN switcher right-aligned on the same line.
func (m model) filesTabStripLine(b lipgloss.Border, innerW int) string {
	strip := m.wsTabStripText()
	styledSw, _, _, _, _ := m.switcherText()
	const swWidth = 7 // "RU | EN"
	pad := maxi(innerW-lipgloss.Width(strip)-swWidth, 0)
	content := strip + strings.Repeat(" ", pad) + styledSw
	return contentLine(b, content, innerW)
}

// wsTabLabels returns the two tab labels with a leading active marker ("▸") on the active
// one (a leading space on the inactive, so both labels are the same width and the click
// geometry is stable regardless of which is active).
func (m model) wsTabLabels() (term, files string) {
	mark := func(active bool, label string) string {
		if active {
			return "▸" + label
		}
		return " " + label
	}
	term = mark(m.wsTab == wsTerminal, t(m.lang, kFmTabTerminal))
	files = mark(m.wsTab == wsFiles, t(m.lang, kFmTabFiles))
	return term, files
}

// wsTabStripText is the rendered tab strip: each label wrapped in a pill (the active one
// accent-highlighted via pillOnStyle, the inactive dim via pillStyle).
func (m model) wsTabStripText() string {
	term, files := m.wsTabLabels()
	tp, fp := pillStyle, pillStyle
	if m.wsTab == wsTerminal {
		tp = pillOnStyle
	} else {
		fp = pillOnStyle
	}
	return tp.Render(term) + " " + fp.Render(files)
}

// wsTabStartCol is the absolute X where the first tab pill begins: 2 (left border + the
// leading space contentLine adds). The tab strip is the FIRST content on its line, so it
// starts at the content origin like the dashboard button row.
const wsTabStartCol = 2

// wsTabZones returns the [start,end) absolute X ranges of the Terminal and Files tab
// pills (pure function of width + labels, so the View draw and the click hit-test agree).
// It uses pillRanges over the SAME labels wsTabStripText renders.
func (m model) wsTabZones() (term, files [2]int) {
	tlabel, flabel := m.wsTabLabels()
	r := pillRanges([]string{tlabel, flabel}, wsTabStartCol)
	return r[0], r[1]
}

// wsTabAtClick maps a click at (x,y) on the tab-strip row to the tab it hit, or ok=false
// on a miss. Mirrors wsTabZones (same labels + start col the strip rendered).
func (m model) wsTabAtClick(x, y int) (wsTab, bool) {
	if y != switcherRow {
		return wsTerminal, false
	}
	term, files := m.wsTabZones()
	switch {
	case x >= term[0] && x < term[1]:
		return wsTerminal, true
	case x >= files[0] && x < files[1]:
		return wsFiles, true
	}
	return wsTerminal, false
}

// --- listing row hit-test -----------------------------------------------------

// filesRowAtClick maps a click at (x,y) to a listing entry index, honoring the scroll
// offset and the fixed header rows above the listing (filesListTopRow). ok=false when the
// click is in the header/footer chrome or past the last entry. Mirrors the listing
// geometry filesBody/renderScrollRegion draw (same fixed top + offset as
// dashAuditRowAtClick).
func (m model) filesRowAtClick(_, y int) (int, bool) {
	if m.files == nil {
		return 0, false
	}
	vis := m.files.visibleEntries()
	if len(vis) == 0 {
		return 0, false
	}
	viewH := m.filesListViewH()
	body := m.filesBody()
	off := clampScroll(m.files.scroll, len(body), viewH)
	rowInRegion := y - filesListTopRow
	if rowInRegion < 0 || rowInRegion >= viewH {
		return 0, false
	}
	idx := off + rowInRegion
	if idx < 0 || idx >= len(vis) {
		return 0, false
	}
	return idx, true
}

// --- column padding helpers (Cyrillic/ANSI-safe; never %-*s) ------------------

// padRightDisplay pads s with spaces on the RIGHT to exactly w display cells (truncating
// when longer). Uses lipgloss.Width so multi-byte/wide runes count correctly.
func padRightDisplay(s string, w int) string {
	s = truncDisplay(s, w)
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// padLeftDisplay pads s with spaces on the LEFT to exactly w display cells (right-aligned;
// truncating when longer).
func padLeftDisplay(s string, w int) string {
	s = truncDisplay(s, w)
	if pad := w - lipgloss.Width(s); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

// humanFileSize formats a byte count compactly (B/K/M/G), so the size column stays narrow.
func humanFileSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}
