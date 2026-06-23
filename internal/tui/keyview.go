package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
)

const keyBodyTopRow = 2

// keyButtonLabel returns the rendered "Copy key" button text (with brackets), the
// SINGLE source shared by keyView (render) and keyCopyAtClick (hit-test) so their
// geometry cannot drift.
func (m model) keyButtonLabel() string { return "[ " + t(m.lang, kKeyCopyBtn) + " ]" }

// keyConnLine builds the localized connect hint: the label + an ssh command that
// uses the admin user the executor switched to (root SSH is blocked post-harden).
// The "<key-file>" is a placeholder — the key is saved nowhere, so the operator
// chooses a path when they paste the copied PEM.
func (m model) keyConnLine() string {
	host := m.host
	if host == "" {
		host = strings.TrimSpace(m.inputs[fHost].Value())
	}
	return t(m.lang, kKeyConnHint) + " ssh -i <key-file> " + defaultAdminUser + "@" + host
}

// keyBodyLines builds the ordered key-screen body (warning, PEM, connect hint,
// button, status) wrapped/clipped to innerW, and returns the slice index of the
// button line so keyCopyAtClick can recover its screen Y. Every PEM line is
// rendered (the OpenSSH PEM is multi-line, ~400 chars); long lines are clipped to
// innerW so they never cross the border.
func (m model) keyBodyLines(innerW int) (lines []string, buttonIdx int) {
	// Warning text differs by mode: the PRE-RUN modal (CHANGE 2) tells the operator to
	// save the key BEFORE the run starts; the post-run/read-only viewer keeps the
	// non-alarming "password login is kept" note. Both render the PEM + copy button.
	if m.keyPreRun {
		lines = append(lines, wrap(focusStyle.Render(t(m.lang, kKeyPreRunWarn)), innerW)...)
	} else {
		lines = append(lines, wrap(labelStyle.Render(t(m.lang, kKeyWarnSoft)), innerW)...)
	}
	lines = append(lines, "")
	for ln := range strings.SplitSeq(strings.TrimRight(m.keyPEM, "\n"), "\n") {
		lines = append(lines, truncDisplay(ln, innerW))
	}
	lines = append(lines, "")
	lines = append(lines, wrap(m.keyConnLine(), innerW)...)
	lines = append(lines, "")
	buttonIdx = len(lines)
	lines = append(lines, pillOnStyle.Render(m.keyButtonLabel()))
	switch {
	case m.keyCopied:
		lines = append(lines, monGreenStyle.Render(t(m.lang, kKeyCopied)))
	case m.keyCopyFailed:
		lines = append(lines, errStyle.Render(t(m.lang, kKeyCopyFail)))
	default:
		lines = append(lines, "")
	}
	// On the pre-run modal, the action line ("[Enter] начать применение  [Esc] отмена")
	// makes it explicit that Enter STARTS the run from here. On the post-run / read-only
	// viewer, a clickable "← Назад" pill dismisses back to keyReturn (keyboard parity
	// with Esc); keyBackAtClick recovers its row from this SAME layout.
	if m.keyPreRun {
		lines = append(lines, "")
		lines = append(lines, pillOnStyle.Render(t(m.lang, kKeyPreRunButtons)))
	} else {
		lines = append(lines, "")
		lines = append(lines, pillStyle.Render(t(m.lang, kWikiBack)))
	}
	return lines, buttonIdx
}

// keyView renders the SSH key screen inside the shared bordered frame, then the
// localized control hint and the live monitor box. Layout (0-based screen rows):
//
//	row 0              : main box top border
//	row 1              : RU/EN switcher
//	rows 2..2+viewH-1  : scrollable body (warning, PEM, connect hint, button, status)
//	...                : hint, bottom border, then the 3-row monitor box (pinned)
func (m model) keyView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body, _ := m.keyBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+t(m.lang, kKeyTitle)+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// Same fixed-chrome layout as summaryView/wikiView: a scrollable middle region of
	// exactly bodyViewH rows (no scroll state here — the PEM almost always fits, but
	// renderScrollRegion keeps the footer pinned regardless), then hint + bottom
	// border + the 3-row monitor box.
	viewH := m.bodyViewH()
	m.renderScrollRegion(&sb, b, body, innerW, viewH, 0)

	hintKey := kKeyHint
	if m.keyPreRun {
		hintKey = kKeyPreRunHint
	}
	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, hintKey)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')
	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// copyKey copies the private-key PEM to the system clipboard, recording success or
// failure for the on-screen status line. Pure value-receiver (model copied by value).
func (m model) copyKey() model {
	if err := clipboard.WriteAll(m.keyPEM); err != nil {
		m.keyCopied = false
		m.keyCopyFailed = true
		return m
	}
	m.keyCopied = true
	m.keyCopyFailed = false
	return m
}

// keyCopyAtClick reports whether the click at (x,y) hit the "Copy key" button. It
// derives the button's screen row from the SAME body layout keyView renders
// (keyBodyTopRow + buttonIdx) and the X range from the rendered button width, so
// the hit-test matches the draw exactly.
func (m model) keyCopyAtClick(x, y int) bool {
	if m.phase != phaseKey {
		return false
	}
	_, buttonIdx := m.keyBodyLines(innerWidth(m.boxWidth()))
	// keyView renders the body at a fixed offset 0, so only body indices [0, viewH)
	// are on screen. A button clipped below the fold (buttonIdx >= viewH) is not
	// drawn, so a click at its absolute Y must NOT register — without this clamp a
	// click on the chrome of a short window could spuriously trigger copyKey.
	if buttonIdx >= m.bodyViewH() {
		return false
	}
	if y != keyBodyTopRow+buttonIdx {
		return false
	}
	const contentX0 = 2 // borderLeft(1) + space(1)
	w := lipgloss.Width(m.keyButtonLabel())
	return x >= contentX0 && x < contentX0+w
}

// keyPreRunButtonsIdx returns the body-slice index of the pre-run "[Enter]…[Esc]…" pill
// (the last body line on the pre-run modal), or -1 when not on the pre-run modal.
func (m model) keyPreRunButtonsIdx() int {
	if !m.keyPreRun {
		return -1
	}
	lines, _ := m.keyBodyLines(innerWidth(m.boxWidth()))
	return len(lines) - 1
}

// keyPreRunHalfAtClick reports whether (x,y) hit the pre-run pill, and whether the click
// landed on the START half ("[Enter] …") or the CANCEL half ("[Esc] …"). The pill is one
// pillOnStyle band; the split is the X where the "[Esc" token begins within the rendered
// pill (content X 2 + one cell of pillStyle left padding). Returns ok=false when off the
// pill / off its row / clipped below the fold (mirrors keyCopyAtClick's clamp).
func (m model) keyPreRunHalfAtClick(x, y int) (start, cancel bool) {
	idx := m.keyPreRunButtonsIdx()
	if m.phase != phaseKey || idx < 0 || idx >= m.bodyViewH() {
		return false, false
	}
	if y != keyBodyTopRow+idx {
		return false, false
	}
	const contentX0 = 2
	full := t(m.lang, kKeyPreRunButtons)
	// pillOnStyle adds Padding(0,1): one cell of left padding before the text, one after.
	const pillPad = 1
	pillStart := contentX0
	textStart := pillStart + pillPad
	pillW := lipgloss.Width(full) + 2*pillPad
	if x < pillStart || x >= pillStart+pillW {
		return false, false
	}
	// Split at the "[Esc" token's COLUMN within the rendered text. strings.Index gives a
	// BYTE offset; the prefix "[Enter] начать применение   " is Cyrillic, so convert it
	// to display cells via lipgloss.Width (byte offset would mis-split a multibyte line).
	escByte := strings.Index(full, "[Esc")
	if escByte < 0 {
		// No cancel token — treat the whole pill as start (defensive; the localized text
		// always contains "[Esc").
		return true, false
	}
	cancelX0 := textStart + lipgloss.Width(full[:escByte])
	if x < cancelX0 {
		return true, false
	}
	return false, true
}

// keyStartAtClick reports whether (x,y) hit the START ("[Enter] …") half of the pre-run
// pill.
func (m model) keyStartAtClick(x, y int) bool {
	start, _ := m.keyPreRunHalfAtClick(x, y)
	return start
}

// keyCancelAtClick reports whether (x,y) hit the CANCEL ("[Esc] …") half of the pre-run
// pill.
func (m model) keyCancelAtClick(x, y int) bool {
	_, cancel := m.keyPreRunHalfAtClick(x, y)
	return cancel
}

// keyBackBodyIdx returns the body-slice index of the post-run "← Назад" pill (the last
// body line on the read-only viewer), or -1 on the pre-run modal.
func (m model) keyBackBodyIdx() int {
	if m.keyPreRun {
		return -1
	}
	lines, _ := m.keyBodyLines(innerWidth(m.boxWidth()))
	return len(lines) - 1
}

// keyBackRow is the screen Y of the post-run "← Назад" pill (keyView renders the body at
// offset 0, so the row is keyBodyTopRow + its body index).
func (m model) keyBackRow() int {
	idx := m.keyBackBodyIdx()
	if idx < 0 {
		return -1
	}
	return keyBodyTopRow + idx
}

// keyBackAtClick reports whether (x,y) hit the post-run "← Назад" pill, using the same
// pillRanges geometry the render path draws. Clipped below the fold ⇒ no hit (mirrors
// keyCopyAtClick's clamp).
func (m model) keyBackAtClick(x, y int) bool {
	idx := m.keyBackBodyIdx()
	if m.phase != phaseKey || idx < 0 || idx >= m.bodyViewH() {
		return false
	}
	if y != keyBodyTopRow+idx {
		return false
	}
	return pillIndexAt([]string{t(m.lang, kWikiBack)}, wikiBackStartCol, x) == 0
}
