package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/version"
)

// labelColW is the form's left label-column width for the CURRENT language: the
// MAX display width (lipgloss.Width, NOT byte len — Cyrillic is 2 bytes/rune) over
// every label rendered in that column (the 5 inputs + Mode + Action). Computed
// once per render and threaded into labelPad / the indent / renderToggle so the
// whole form shares one left edge in both ru and en. Replaces the old fixed
// const labelW=9, which misaligned the longer localized RU labels.
func (m model) labelColW() int {
	keys := []stringKey{
		kLabelHost, kLabelPort, kLabelUser, kLabelPassword, kLabelKey,
		kSaveLogLabel,
	}
	w := 0
	for _, k := range keys {
		if lw := lipgloss.Width(t(m.lang, k)); lw > w {
			w = lw
		}
	}
	return w
}

// padLabel left-pads label to colW DISPLAY cells (lipgloss.Width, not byte len, so
// multibyte Cyrillic labels still align). Used by both labelPad and renderToggle.
func padLabel(label string, colW int) string {
	if pad := colW - lipgloss.Width(label); pad > 0 {
		return label + strings.Repeat(" ", pad)
	}
	return label
}

// saveLogToken maps the save-log bool to the canonical pill token ("on"/"off")
// renderToggle matches against, mirroring how mode/command use their string tokens.
func saveLogToken(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// field indices in the form.
const (
	fHost = iota
	fPort
	fUser
	fPass
	fKey
	nInputs // number of text inputs
)

// extra focusable rows after the inputs.
const (
	rowDisclosure   = nInputs     // "▸ Дополнительно" advanced-inputs toggle
	rowLog          = nInputs + 1 // save-log-to-file toggle
	rowStart        = nInputs + 2 // connect button
	rowUpdateButton = nInputs + 3 // "Обновить" pill (focusable ONLY when updAvailable)
	nRows           = nInputs + 4
)

// focusableRows returns the ordered list of currently-focusable row indices.
// Navigation runs over this slice so focus never lands on a hidden row (the
// advanced Port/User/Key inputs are included only when advancedOpen).
func (m model) focusableRows() []int {
	rows := make([]int, 0, nRows)
	// Visible inputs: Host + Password always; Port/User/Key only when advancedOpen,
	// so focus never strands on a hidden field.
	for i := range nInputs {
		if !m.advancedOpen && (i == fPort || i == fUser || i == fKey) {
			continue
		}
		rows = append(rows, i)
	}
	rows = append(rows, rowDisclosure)
	rows = append(rows, rowLog)
	// The update pill is focusable ONLY when a newer release was detected, so Tab
	// never lands on a hidden/disabled button.
	if m.updateState == updAvailable {
		rows = append(rows, rowUpdateButton)
	}
	rows = append(rows, rowStart)
	return rows
}

// lastFocusableInput returns the highest text-input row index currently in
// focusableRows() — the Password field normally, or the Key field when the
// advanced disclosure is open. Returns -1 if no input is focusable.
func (m model) lastFocusableInput() int {
	last := -1
	for _, r := range m.focusableRows() {
		if r < nInputs {
			last = r
		}
	}
	return last
}

// sanitizeField strips characters that don't belong in a given input. Newlines/
// control chars are always removed; Host keeps only IP/hostname chars, Port digits.
func sanitizeField(idx int, s string) string {
	keep := func(r rune) bool {
		if r < 0x20 { // control chars incl. \n \r \t
			return false
		}
		switch idx {
		case fHost:
			return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') || r == '.' || r == '-'
		case fPort:
			return r >= '0' && r <= '9'
		default:
			return true
		}
	}
	var b strings.Builder
	for _, r := range s {
		if keep(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

const switcherRow = 1

// switcherText returns the styled "RU | EN" with the active language highlighted,
// plus the plain (unstyled) RU and EN cell ranges relative to the start of the
// switcher text. ruLen/enLen are each 2 cells; sep " | " is 3 cells.
func (m model) switcherText() (styled string, ruStart, ruEnd, enStart, enEnd int) {
	on := focusStyle  // 213 focus pink — active language
	off := helpStyle  // 240 dim — inactive
	sep := helpStyle  // 240 dim separator
	ru, en := off, on // langEN active by default-branch below
	if m.lang == langRU {
		ru, en = on, off
	}
	styled = ru.Render("RU") + sep.Render(" | ") + en.Render("EN")
	ruStart, ruEnd = 0, 2 // "RU"
	enStart, enEnd = 5, 7 // after "RU" (2) + " | " (3)
	return styled, ruStart, ruEnd, enStart, enEnd
}

// switcherLine renders the first content line of a box: the RU/EN switcher
// right-aligned inside the innerW content area. It returns a full content line
// (already wrapped with borders) so callers emit it directly.
func (m model) switcherLine(b lipgloss.Border, innerW int) string {
	styled, _, _, _, _ := m.switcherText()
	const swWidth = 7 // "RU | EN"
	pad := max(innerW-swWidth, 0)
	content := strings.Repeat(" ", pad) + styled
	return contentLine(b, content, innerW)
}

// langZones computes the absolute on-screen cell ranges of the RU and EN labels
// for the current frame (pure function of m.w — layout is deterministic), so the
// mouse hit-test in Update matches exactly what View drew. Returns the row and the
// [start,end) X ranges for RU and EN.
func (m model) langZones() (row, ruX0, ruX1, enX0, enX1 int) {
	innerW := innerWidth(m.boxWidth())
	const swWidth = 7
	pad := max(innerW-swWidth, 0)
	// Absolute column where content begins: borderLeft(1) + space(1) = 2.
	base := 2 + pad
	_, ruS, ruE, enS, enE := m.switcherText()
	return switcherRow, base + ruS, base + ruE, base + enS, base + enE
}

// langAtClick returns which language label (if any) the click at (x,y) hit.
func (m model) langAtClick(x, y int) (Lang, bool) {
	row, ruX0, ruX1, enX0, enX1 := m.langZones()
	if y != row {
		return m.lang, false
	}
	switch {
	case x >= ruX0 && x < ruX1:
		return langRU, true
	case x >= enX0 && x < enX1:
		return langEN, true
	}
	return m.lang, false
}

// formHit is the resolved click target inside the form body.
type formHit struct {
	kind  formRowKind
	field int  // frInput: input index
	log   bool // frLog: true=on (save log), false=off
	pill  int  // frStart: 0=Connect, 1=Cancel
	ok    bool
}

// pillColStart is the absolute X where the value/pill column begins: 2 (frame) +
// colW + 1 (label column + one space).
func (m model) pillColStart() int { return 2 + m.labelColW() + 1 }

// formHitAtClick maps a click at (x,y) to a form element, iterating the same slice
// the renderer used. Returns ok=false when the click missed every target.
func (m model) formHitAtClick(x, y int) formHit {
	rows := m.formRows()
	idx := y - formBodyTopRow
	if idx < 0 || idx >= len(rows) {
		return formHit{}
	}
	r := rows[idx]
	switch r.kind {
	case frInput:
		// A whole input row is clickable (any X) to focus that field.
		return formHit{kind: frInput, field: r.field, ok: true}
	case frDisclosure:
		// The whole disclosure line is one click target (toggles advancedOpen).
		return formHit{kind: frDisclosure, ok: true}
	case frLog:
		names := []string{t(m.lang, kSaveLogOn), t(m.lang, kSaveLogOff)}
		if i := pillIndexAt(names, m.pillColStart(), x); i >= 0 {
			return formHit{kind: frLog, log: i == 0, ok: true} // pill 0 = on
		}
	case frStart:
		// Start + Cancel share this line; pillRanges uses the same labels the render
		// path rendered (startCancelLabels), so the hit ranges match exactly.
		if i := pillIndexAt(m.startCancelLabels(), m.pillColStart(), x); i >= 0 {
			return formHit{kind: frStart, pill: i, ok: true}
		}
	case frUpdate:
		// Only the "Обновить" pill is a click target, and only when a newer release
		// is available. The pill begins after the indent + status text + one space;
		// its [start,end) X-range is recovered from the same label the render path used.
		if m.updateState == updAvailable && x >= m.updateButtonColStart() {
			if r := pillRanges([]string{m.updateButtonLabel()}, m.updateButtonColStart()); x >= r[0][0] && x < r[0][1] {
				return formHit{kind: frUpdate, ok: true}
			}
		}
	}
	return formHit{}
}

// updateButtonColStart is the absolute X where the "Обновить" pill begins on the
// update strip row: indent (value column) + status text width + one space. Shares
// updateStripText / updateButtonLabel with the render path so geometry never drifts.
func (m model) updateButtonColStart() int {
	indentW := m.labelColW() + 1
	// +2 for the frame's left border + leading space (matches contentLine geometry,
	// same offset pillColStart uses via formBodyTopRow content rows).
	return 2 + indentW + lipgloss.Width(m.updateStripText()) + 1
}

// pillIndexAt returns the index of the pill containing absolute X x (pills starting
// at startCol, geometry from pillRanges), or -1 if x is outside every pill.
func pillIndexAt(names []string, startCol, x int) int {
	for i, r := range pillRanges(names, startCol) {
		if x >= r[0] && x < r[1] {
			return i
		}
	}
	return -1
}

// --- Run-phase "Back to main" mouse hit-test ----------------------------------
//
// Mirrors runView's exact line emission order to derive the button's screen Y.
// runView emits (0-based screen rows):
//
//	row 0            : main box top border (titledTop)
//	row 1            : switcher line
//	row 2            : progress line
//	row 3            : blank spacer
//	rows 4..4+V-1    : V viewport lines (V = m.vp.Height())
//	then, when finished:
//	  rows ..        : finishedTailRows() completion-tail lines
//	  row backRow    : the pillOn "Back to main" button  ← target
//	...              : hints, borders, monitor box
//
// So backRow = 4 + V + finishedTailRows().
func (m model) backToMainRow() int {
	return 4 + m.vp.Height() + m.finishedTailRows()
}

// backToMainAtClick reports whether the click at (x,y) hit the "Back to main"
// button (only shown when finished). X spans the rendered button width starting at
// the content column (absolute X = 2: left border + space).
func (m model) backToMainAtClick(x, y int) bool {
	if !m.finished {
		return false
	}
	if y != m.backToMainRow() {
		return false
	}
	const contentX0 = 2 // borderLeft(1) + space(1)
	w := lipgloss.Width(t(m.lang, kBackToMain))
	return x >= contentX0 && x < contentX0+w
}

// formClick applies a form-phase click: focus an input, flip a Mode/Action pill,
// press Start, or quit via Cancel. It reuses the SAME state transitions as the
// keyboard handlers (refocus / start / the detect→Mode focus guard) so mouse and
// key paths stay consistent.
func (m model) formClick(x, y int) (tea.Model, tea.Cmd) {
	hit := m.formHitAtClick(x, y)
	if !hit.ok {
		return m, nil
	}
	switch hit.kind {
	case frInput:
		m.focus = hit.field
		return m, m.refocus()
	case frDisclosure:
		m.advancedOpen = !m.advancedOpen
		m.focus = rowDisclosure
		return m, nil
	case frLog:
		m.saveLog = hit.log
		m.focus = rowLog
		return m, nil
	case frStart:
		if hit.pill == 1 { // Cancel
			return m, tea.Quit
		}
		m.focus = rowStart
		// The landing "Подключиться" button is READ-ONLY: it runs the audit
		// (dial → detect → tweaks audit → Dashboard), never the apply path.
		m.command = "audit"
		return m.start()
	case frUpdate:
		// Click on the "Обновить" pill: same effect as Enter on the focused button —
		// record intent + target version, then quit so the alt-screen tears down
		// before main() relaunches the updated binary.
		m.focus = rowUpdateButton
		m.wantUpdate = true
		return m, tea.Quit
	}
	return m, nil
}

// formRowKind tags each entry in the ordered formRows slice so the hit-test can
// map a click to the right element while iterating the SAME slice the renderer does.
type formRowKind int

const (
	frInput      formRowKind = iota // a text-input row; field holds the input index
	frBlank                         // spacer line (kept in the slice so Y math stays exact)
	frAction                        // run/detect/verify pill row (removed from the form; kept for the negative-assertion guard)
	frLog                           // save-log-to-file on/off pill row
	frStart                         // Start + Cancel button line
	frErr                           // validation error line
	frDisclosure                    // "▸ Дополнительно" toggle revealing Port/User/Key
	frVersion                       // version-frame line (titled top / tagline / bottom)
	frUpdate                        // self-update strip line (under the version header)
)

// formRow is one rendered body line plus its kind (+ field index for inputs). The
// formRows slice is the single source of truth for BOTH the renderer (formView)
// and the mouse hit-test (formRowAtClick): a row's screen Y is fixed by its index
// in this slice, so the two cannot diverge when the Mode row is hidden/shown.
type formRow struct {
	kind  formRowKind
	field int    // valid only for frInput
	text  string // the rendered content line (without borders)
}

// formBodyTopRow is the 0-based screen Y of the FIRST form body line: top border
// (row 0) + switcher (row 1) → body starts at row 2. A body row at slice index i
// renders at screen Y = formBodyTopRow + i. Each framed input now contributes THREE
// consecutive slice entries (border-top / label+value / border-bottom), all tagged
// frInput with the same field, so the hit-test maps any of those three Y's to that
// input. The disclosure/version frame rows shift this slice but not this constant.
const formBodyTopRow = 2

// framedInputWidth is the inner box width (between the border runes) of a framed
// input: the shared label column + one space + the textinput visible width (44),
// clamped so the whole 3-row frame (border adds 2 cells) never exceeds the box
// content width innerW. All widths are display cells (lipgloss.Width).
func (m model) framedInputWidth() int {
	innerW := innerWidth(m.boxWidth())
	want := m.labelColW() + 1 + 44
	if max := innerW - 2; want > max { // -2 for the left+right border cells
		want = max
	}
	if want < 1 {
		want = 1
	}
	return want
}

// framedInputRow renders ONE landing input as a 3-line rounded-border box:
//
//	line 0: ╭───────────────╮         (top border)
//	line 1: │ Label  value… │         (left border + label + space + input.View + right)
//	line 2: ╰───────────────╯         (bottom border)
//
// Unfocused → dim border (240); focused → accent border (57) + bold label (213).
// Every line's display width equals the frame outer width (inner + 2) so the
// caller (formRows → contentLine) pads/truncates to innerW without breaking the
// frame. All width math is via lipgloss.Width (Cyrillic-safe), never %-*s.
func (m model) framedInputRow(label string, input textinput.Model, focused bool) []string {
	bd := lipgloss.RoundedBorder()
	bs := inputBorderDim
	if focused {
		bs = inputBorderFocus
	}
	inner := m.framedInputWidth()
	colW := m.labelColW()

	top := bs.Render(bd.TopLeft + strings.Repeat(bd.Top, inner) + bd.TopRight)
	bottom := bs.Render(bd.BottomLeft + strings.Repeat(bd.Bottom, inner) + bd.BottomRight)

	ls := labelStyle
	if focused {
		ls = focusStyle
	}
	// content = " " + label(colW) + " " + input.View(), truncated/padded to inner.
	content := " " + ls.Render(padLabel(label, colW)) + " " + input.View()
	content = truncDisplay(content, inner)
	if pad := inner - lipgloss.Width(content); pad > 0 {
		content += strings.Repeat(" ", pad)
	}
	mid := bs.Render(bd.Left) + content + bs.Render(bd.Right)

	return []string{top, mid, bottom}
}

// versionFrame renders the landing version sub-box as three content lines (titled
// top "─ Morgward vX.Y ─", a tagline line, bottom border), each sized to fit the
// box content width innerW. It is emitted as the first rows of formRows so the
// switcher/formBodyTopRow geometry is unchanged and the hit-test stays aligned.
func (m model) versionFrame(innerW int) []string {
	bd := lipgloss.RoundedBorder()
	// Inner frame width: full content width, but bounded so titledTop/borderLine
	// (which clamp to minBoxWidth) never draw wider than the content area.
	fw := max(innerW, minBoxWidth)
	finner := fw - 2 // cells between the frame's border runes
	title := " " + version.Name + " v" + version.Version + " "
	top := titledTop(bd, title, fw)
	// Middle line: left border + " "+tagline padded to (finner) + right border, so
	// the line is exactly fw cells (2 border + finner content).
	tagline := " " + labelStyle.Render(t(m.lang, kVersionTagline))
	tagline = truncDisplay(tagline, finner)
	if pad := finner - lipgloss.Width(tagline); pad > 0 {
		tagline += strings.Repeat(" ", pad)
	}
	mid := borderStyle.Render(bd.Left) + tagline + borderStyle.Render(bd.Right)
	bottom := borderLine(bd.BottomLeft, bd.Bottom, bd.BottomRight, fw)
	return []string{top, mid, bottom}
}

// updateStripText returns the localized status text for the self-update strip,
// keyed on m.updateState. The "available" state interpolates m.updateVer.
func (m model) updateStripText() string {
	switch m.updateState {
	case updChecking:
		return t(m.lang, kUpdateChecking)
	case updCurrent:
		return t(m.lang, kUpdateCurrent)
	case updAvailable:
		return fmt.Sprintf(t(m.lang, kUpdateAvailable), m.updateVer)
	case updErr:
		return t(m.lang, kUpdateError)
	default:
		return t(m.lang, kUpdateChecking)
	}
}

// updateButtonLabel is the single source of the "Обновить ⬇" pill text, shared by
// the render path and the click hit-test so their x-geometry can never drift.
func (m model) updateButtonLabel() string { return t(m.lang, kUpdateButtonLabel) }

// updateStripRow renders the self-update strip line shown directly under the
// version header on the landing. It is the status text in an accent tint, plus —
// ONLY when a newer release is available — the focusable/clickable "Обновить" pill
// (pillOn when focused). The line content (sans frame) is indented to the value
// column so it aligns with the rest of the form. Width math is lipgloss.Width-safe.
func (m model) updateStripRow(indent string) string {
	text := lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Render(m.updateStripText())
	if m.updateState != updAvailable {
		return indent + text
	}
	label := m.updateButtonLabel()
	pill := pillStyle.Render(label)
	if m.focus == rowUpdateButton {
		pill = pillOnStyle.Render(label)
	}
	return indent + text + " " + pill
}

// formRows builds the ordered form body as a slice of row specs (INCLUDING blank
// rows) in exact render order. formView renders by iterating this; the hit-test
// iterates the same slice, so geometry can never drift.
func (m model) formRows() []formRow {
	colW := m.labelColW()
	// indent aligns the toggle/help/button content to the shared value column
	// (col colW+1), the same left edge the framed input labels use.
	indent := strings.Repeat(" ", colW+1)

	var rows []formRow
	// Version sub-frame (name + tagline) at the very top of the body, so the
	// switcher/formBodyTopRow geometry stays fixed and the hit-test slice still
	// includes every rendered line.
	for _, ln := range m.versionFrame(innerWidth(m.boxWidth())) {
		rows = append(rows, formRow{kind: frVersion, text: ln})
	}
	// Self-update strip directly under the version header: status text + (only when a
	// newer release exists) the clickable "Обновить" pill.
	rows = append(rows, formRow{kind: frUpdate, text: m.updateStripRow(indent)})
	rows = append(rows, formRow{kind: frBlank})

	labels := []stringKey{kLabelHost, kLabelPort, kLabelUser, kLabelPassword, kLabelKey}
	// appendFramedInput emits the 3 rows of one framed input; all three carry the
	// same field index so a click on any of them focuses that input (the hit-test
	// maps the whole 3-row block to one target).
	appendFramedInput := func(i int) {
		framed := m.framedInputRow(t(m.lang, labels[i]), m.inputs[i], i == m.focus)
		for _, ln := range framed {
			rows = append(rows, formRow{kind: frInput, field: i, text: ln})
		}
	}
	// Novice default: Host + Password only. The disclosure toggle reveals the
	// advanced Port/User/SSH-key inputs.
	appendFramedInput(fHost)
	appendFramedInput(fPass)
	if m.advancedOpen {
		appendFramedInput(fPort)
		appendFramedInput(fUser)
		appendFramedInput(fKey)
	}

	// "▸ Дополнительно" disclosure toggle: clicking/▶ toggles m.advancedOpen. The
	// caret reflects state (▸ closed, ▼ open). Focusable (rowDisclosure) and aligned
	// to the value column.
	disLabel := t(m.lang, kDisclosureLabel)
	if m.advancedOpen {
		// swap the leading "▸" for the open glyph "▼"
		disLabel = t(m.lang, kDisclosureOpen) + strings.TrimPrefix(disLabel, "▸")
	}
	disStyle := tipStyle
	if m.focus == rowDisclosure {
		disStyle = focusStyle
	}
	rows = append(rows, formRow{kind: frDisclosure, text: indent + disStyle.Render(disLabel)})

	rows = append(rows, formRow{kind: frBlank})
	// The soft/strict Mode selector and the run/detect/verify action selector are
	// intentionally NOT shown on the landing form: m.mode stays config.ModeSoft and
	// m.command stays "run" (engine tokens are unaffected). Access lockdown moves to
	// the Security menu in a later phase.
	// Save-log-to-file toggle: a session preference. Writes the full run log to
	// cfg.LogFile when on, off by default. Canonical tokens on/off; localized
	// yes/no pill names.
	rows = append(rows, formRow{kind: frLog, text: renderToggle(t(m.lang, kSaveLogLabel),
		[]string{"on", "off"},
		[]string{t(m.lang, kSaveLogOn), t(m.lang, kSaveLogOff)},
		saveLogToken(m.saveLog), m.focus == rowLog, colW)})
	rows = append(rows, formRow{kind: frBlank})

	// Start + Cancel buttons on one line, aligned to the value column (col colW+1).
	// Start uses pillOn when focused; Cancel always uses the dim pill (clickable, not
	// focusable). Both pill labels are wrapped by pillStyle/pillOnStyle, so their
	// x-geometry is recovered by pillRanges over the same names in the zone mapper.
	rows = append(rows, formRow{kind: frStart, text: indent + m.startCancelPills()})

	if m.errMsg != "" {
		rows = append(rows, formRow{kind: frBlank})
		rows = append(rows, formRow{kind: frErr, text: indent + errStyle.Render("✗ "+m.errMsg)})
	}
	return rows
}

// startCancelLabels returns the two pill display names (Connect, Cancel) — the single
// source the render path and pillRanges/the hit-test both consume. The Connect name
// carries the focus caret/leading space so its rendered width matches what the user
// clicks; Cancel is a plain padded label.
func (m model) startCancelLabels() []string {
	start := "  " + t(m.lang, kStart) + "  "
	if m.focus == rowStart {
		start = "▶" + start
	} else {
		start = " " + start
	}
	return []string{start, t(m.lang, kCancel)}
}

// startCancelPills renders the Connect + Cancel button line (pills joined by one
// space), matching the geometry pillRanges assumes.
func (m model) startCancelPills() string {
	names := m.startCancelLabels()
	startPill := pillStyle.Render(names[0])
	if m.focus == rowStart {
		startPill = pillOnStyle.Render(names[0])
	}
	cancelPill := pillStyle.Render(names[1])
	return startPill + " " + cancelPill
}

// formView builds the form-phase screen content (was View()'s body).
func (m model) formView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	bd := lipgloss.RoundedBorder()
	var out strings.Builder
	// Plain outer top border: the program name + version now lives in the inner
	// version frame (see versionFrame), so the outer box stays untitled per the
	// landing mockup.
	out.WriteString(borderLine(bd.TopLeft, bd.Top, bd.TopRight, bw) + "\n")

	// First content line (screen row 1): the RU/EN switcher, right-aligned. Drawn
	// before the form body so the click hit-test row (switcherRow=1) always matches.
	out.WriteString(m.switcherLine(bd, innerW) + "\n")

	// Main form content: iterate the SAME ordered slice the hit-test uses, so the
	// rendered Y of each row equals formBodyTopRow + its index.
	rows := m.formRows()
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = r.text
	}
	for _, line := range lines {
		out.WriteString(contentLine(bd, line, innerW) + "\n")
	}

	// Then pad the vertical space, THEN the control hint as the last content line
	// directly above the bottom border, pinning it to the bottom of the window.
	// Layout budget: 1 top border + 1 switcher + len(lines) content + pad + 1 hint
	// + 1 bottom border = m.h. So pad = m.h − 4 − len(lines), clamped ≥0 (maxi
	// guard) so when m.h is unset/too small we simply emit content then hint.
	hint := helpStyle.Render(t(m.lang, kFormHint))
	pad := maxi(m.h-4-len(lines), 0)
	for range pad {
		out.WriteString(contentLine(bd, "", innerW) + "\n")
	}
	out.WriteString(contentLine(bd, hint, innerW) + "\n")
	out.WriteString(borderLine(bd.BottomLeft, bd.Bottom, bd.BottomRight, bw))
	return out.String()
}

// renderToggle draws a labelled pill row. opts are the canonical (engine) tokens
// used for selection-matching against cur; names are the localized display
// strings shown in the pills (same order/length as opts). colW is the shared
// label-column width (see labelColW); the pills start at col colW+1.
func renderToggle(label string, opts, names []string, cur string, focused bool, colW int) string {
	s := labelStyle
	if focused {
		s = focusStyle
	}
	lbl := s.Render(padLabel(label, colW)) // same label column as the inputs
	var pills []string
	for i, o := range opts {
		name := o
		if i < len(names) {
			name = names[i]
		}
		if o == cur {
			pills = append(pills, pillOnStyle.Render(name)) // selected: accent bg 57
		} else {
			pills = append(pills, pillStyle.Render(name)) // unselected: dim
		}
	}
	// One space after the label (→ col colW+1, same as input values) and an even
	// single space between pills.
	return lbl + " " + strings.Join(pills, " ")
}

// pillRanges is the SINGLE source of pill x-geometry, used by BOTH the render path
// (renderToggle / the Start–Cancel line, indirectly via the same layout: label +
// space + pills joined by single spaces) and the mouse hit-test zone mappers, so
// they cannot drift. Given the localized pill display names and the absolute column
// where the first pill begins (startCol), it returns the absolute [start,end) X
// range of each pill. Accounts for pillStyle/pillOnStyle's Padding(0,1) (= +2 cells
// per pill, one each side) and the single-space separator between pills. Widths are
// display cells (lipgloss.Width), so multibyte localized names stay aligned.
func pillRanges(names []string, startCol int) [][2]int {
	const pad = 2 // Padding(0,1): one cell left + one cell right
	ranges := make([][2]int, len(names))
	x := startCol
	for i, n := range names {
		w := lipgloss.Width(n) + pad
		ranges[i] = [2]int{x, x + w}
		x += w + 1 // + single-space separator before the next pill
	}
	return ranges
}
