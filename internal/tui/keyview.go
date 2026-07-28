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
	body, _ := m.keyBodyLines(innerWidth(m.boxWidth()))
	hintKey := kKeyHint
	if m.keyPreRun {
		hintKey = kKeyPreRunHint
	}
	// The shared frame with nothing fixed or pinned: the scroll region is the full
	// middle (bodyViewH). A PEM is ~10 lines (ed25519) to ~40 (RSA), so on a short
	// window the body overflows and keyScroll windows it — the same offset ↑↓/k/j, the
	// wheel and the scrollbar drag move (scrollBy / scrollGeom).
	return m.framedScrollView(frame{
		title:  " " + t(m.lang, kKeyTitle) + " ",
		nav:    m.switcherLine,
		body:   body,
		viewH:  m.bodyViewH(),
		scroll: m.keyScroll,
		hint:   helpStyle.Render(t(m.lang, hintKey)),
	})
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

// keyLayers returns the phaseKey click targets: the "Copy key" button plus, by mode,
// the START/CANCEL halves of the pre-run "[Enter] … [Esc] …" pill or the post-run
// "← Назад" pill. Rows come from the SAME keyBodyLines layout keyView renders.
//
// keyView windows the body at the CLAMPED keyScroll, so a target's screen row is its
// body index MINUS that offset and only rows [0, viewH) are on screen: a row scrolled
// off (above or below the fold) gets NO layer and is unhittable — without that clamp a
// click on the chrome of a short window could trigger copyKey.
func (m model) keyLayers() []*lipgloss.Layer {
	lines, buttonIdx := m.keyBodyLines(innerWidth(m.boxWidth()))
	viewH := m.bodyViewH()
	off := clampScroll(m.keyScroll, len(lines), viewH)
	var ls []*lipgloss.Layer
	add := func(id string, idx, x, w int) {
		if row := idx - off; row >= 0 && row < viewH {
			ls = append(ls, hitLayer(id, x, keyBodyTopRow+row, w, 1))
		}
	}
	add(idKeyCopy, buttonIdx, contentX0, lipgloss.Width(m.keyButtonLabel()))

	last := len(lines) - 1 // both modes render their pill as the LAST body line
	if !m.keyPreRun {
		// Same pillRanges geometry pillLayer would build (pillStyle's Padding(0,1)
		// included), but routed through add() so the below-the-fold clamp applies here
		// too: on a short window the pill is clipped and its row shows the hint / border
		// / monitor box instead, which must not dismiss the screen.
		r := pillRanges([]string{t(m.lang, kWikiBack)}, wikiBackStartCol)[0]
		add(idKeyBack, last, r[0], r[1]-r[0])
		return ls
	}
	// The pre-run pill is ONE pillOnStyle band split in two: START spans from the pill's
	// left edge to the column where the "[Esc" token begins, CANCEL from there to the
	// pill's right edge. strings.Index gives a BYTE offset and the prefix ("[Enter] начать
	// применение   ") is Cyrillic, so it is converted to display cells via lipgloss.Width.
	const pillPad = 1 // pillOnStyle's Padding(0,1)
	full := t(m.lang, kKeyPreRunButtons)
	pillW := lipgloss.Width(full) + 2*pillPad
	// No cancel token ⇒ the whole pill starts the run (defensive; the localized text
	// always contains "[Esc"), leaving CANCEL zero-width and thus unhittable.
	splitX := contentX0 + pillW
	if esc := strings.Index(full, "[Esc"); esc >= 0 {
		splitX = contentX0 + pillPad + lipgloss.Width(full[:esc])
	}
	add(idKeyStart, last, contentX0, splitX-contentX0)
	add(idKeyCancel, last, splitX, contentX0+pillW-splitX)
	return ls
}
