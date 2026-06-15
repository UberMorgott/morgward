package tui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// physKey returns the layout-INDEPENDENT string to match a COMMAND shortcut against,
// so a bare printable hotkey (e.g. 'n', '/', ':') fires regardless of the host
// keyboard layout.
//
// The layout problem: a tea.KeyPressMsg.String() returns the PRODUCED character
// (Key.Text) when the key represents a printable char. On a non-English layout the
// physical N key produces Cyrillic "т", so `case "n":` never matches and every bare
// letter/punctuation shortcut silently breaks on a Russian/Greek/etc. layout. Ctrl
// combos are unaffected (ctrl produces a control char → Text empty → String() already
// falls through to Keystroke(), which prefers the layout-independent BaseCode).
//
// The fix: when String() is NOT already a plain ASCII shortcut, fall back to
// Key.BaseCode — the US PC-101 PHYSICAL key, independent of the active layout (the
// physical N key has BaseCode 'n' on every layout). We derive the US shortcut string
// from BaseCode, applying ShiftedCode/ModShift so physical shift+N → "N" and a tiny
// US-shift punctuation map so physical shift+';' → ":" (matching the address-bar
// shortcut). Preferring the PHYSICAL key for punctuation is intentional: each physical
// key position then maps to its US shortcut consistently.
//
// CAVEAT: Key.BaseCode is populated ONLY by the Windows Console API and the Kitty
// keyboard protocol. On a plain Linux/macOS terminal WITHOUT Kitty it is 0 — the
// physical key is unrecoverable, so we fall back to String() (no worse than today).
//
// This is for COMMAND shortcuts ONLY. TEXT-ENTRY paths must keep the RAW msg so a
// non-Latin user can still type Cyrillic into fields/paths/the shell — never route a
// textinput.Update or the raw terminal forward through physKey.
func physKey(msg tea.KeyPressMsg) string {
	s := msg.String()
	// Fast path: String() is already a plain ASCII shortcut — either a single ASCII
	// printable rune ('n', '/', '1') or a named/combo key ("enter", "up", "ctrl+1").
	// Named/combo strings are pure ASCII too, so the ASCII test below covers both.
	if isASCIIShortcut(s) {
		return s
	}
	// String() produced a non-ASCII char (a layout-translated letter like "т"). Recover
	// the physical key from BaseCode if the platform populated it.
	k := msg.Key()
	bc := k.BaseCode
	if bc <= 0 || bc >= 0x80 || !unicode.IsPrint(bc) {
		// BaseCode unusable (non-Kitty Linux/macOS, or a non-printable code) → best effort:
		// return String() unchanged, exactly today's behavior.
		return s
	}
	// Letters: apply shift (physical shift / caps) to upper-case so shift+N → "N".
	if bc >= 'a' && bc <= 'z' {
		if k.Mod&tea.ModShift != 0 || (k.ShiftedCode >= 'A' && k.ShiftedCode <= 'Z') {
			return string(bc - 'a' + 'A')
		}
		return string(bc)
	}
	// Punctuation/digits: when Shift is held, map the physical key through the US PC-101
	// shifted layout so a shifted ';' yields ":" (the address-bar shortcut) etc. Without
	// Shift, BaseCode already IS the US char ('/', '.', ';', '1', ...).
	if k.Mod&tea.ModShift != 0 {
		if shifted, ok := usShiftPunct[bc]; ok {
			return string(shifted)
		}
	}
	return string(bc)
}

// usShiftPunct maps a US PC-101 punctuation/number key (unshifted BaseCode) to the
// character it produces WITH Shift held. Only the entries our shortcuts care about are
// listed: ';'+shift → ':' (address-bar focus) and '/'+shift → '?'. Kept deliberately
// minimal — extend it only when a new shifted-punctuation shortcut is added.
var usShiftPunct = map[rune]rune{
	';': ':',
	'/': '?',
}

// isASCIIShortcut reports whether s is already a usable command-shortcut string: a
// single ASCII printable rune, OR a multi-char named/combo key (all of which are pure
// ASCII, e.g. "enter", "esc", "up", "tab", "backspace", "ctrl+1", "ctrl+q"). A string
// containing any non-ASCII byte (a layout-translated char like "т") is rejected so the
// BaseCode fallback runs. Empty is treated as a shortcut (nothing to recover).
func isASCIIShortcut(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
