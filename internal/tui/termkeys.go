package tui

import (
	tea "charm.land/bubbletea/v2"
)

// encodeKey converts a bubbletea v2 key press into the byte sequence a real
// terminal would send to a PTY's stdin, so the remote shell (and full-screen apps
// like vim/top) sees exactly what an `ssh` session would deliver.
//
// Grounded in the bubbletea v2 / ultraviolet key model (go doc charm.land/bubbletea/v2):
//   - Key.Text holds the printable character(s) for an ordinary keypress (already the
//     final, shift-applied rune); we emit it verbatim for the no-modifier / shift-only
//     case so Unicode and AltGr composition work without a hand-rolled rune table.
//   - Key.Code is a rune: a literal control rune for Enter/Tab/Esc/Backspace/Space
//     (CR/HT/ESC/DEL/SP, per uv's KeyEnter=CR etc.), an ASCII letter/digit for
//     printable keys, or a KeyExtended+N sentinel for navigation/function keys.
//   - Key.Mod carries ModCtrl/ModAlt (+ModShift, handled via Text/ShiftedCode).
//
// Returns nil for keys that produce no input (pure modifier presses, unknown
// extended keys) so the caller writes nothing.
func encodeKey(k tea.Key) []byte {
	ctrl := k.Mod&tea.ModCtrl != 0
	alt := k.Mod&tea.ModAlt != 0

	// Ctrl+<letter/char> FIRST: the classic C0 control-byte mapping (Ctrl+A=0x01 …
	// Ctrl+Z=0x1a, plus the @[\]^_ block and Ctrl+Space=NUL). Checked ahead of the
	// named-key lookup so chords like Ctrl+Space (→ NUL, not a literal space) and
	// Ctrl+[ (→ ESC) win over their plain forms. Keys with no ctrl encoding (arrows,
	// function keys) fall through to specialKeyBytes below, yielding their plain CSI
	// sequence — a sane default (modifyOtherKeys-style ctrl+arrow encoding is not
	// implemented for 2a).
	if ctrl {
		if b, ok := ctrlByte(k.Code); ok {
			return withAlt([]byte{b}, alt)
		}
	}

	// Named special keys: canonical byte/escape sequences that do NOT vary with Text
	// (Text is empty for most of them anyway).
	if b, ok := specialKeyBytes(k.Code); ok {
		return withAlt(b, alt)
	}

	// Printable text (the common path): emit the actual characters. Text already has
	// shift/AltGr applied, so "A", "é", etc. come through correctly. Alt prefixes ESC.
	if k.Text != "" {
		return withAlt([]byte(k.Text), alt)
	}

	// Fallback: a bare printable Code with no Text (some terminals don't populate Text
	// for plain ASCII). Emit the rune.
	if k.Code > 0 && k.Code < tea.KeyExtended {
		return withAlt([]byte(string(k.Code)), alt)
	}

	return nil
}

// withAlt prefixes ESC (0x1b) when Alt/Meta is held — the conventional "Alt sends
// Escape" encoding xterm uses. b is returned unchanged when alt is false.
func withAlt(b []byte, alt bool) []byte {
	if !alt || len(b) == 0 {
		return b
	}
	out := make([]byte, 0, len(b)+1)
	out = append(out, 0x1b)
	out = append(out, b...)
	return out
}

// specialKeyBytes returns the input bytes for a named/navigation/function key, or
// ok=false when code is not one of them. The CSI/SS3 sequences match xterm's default
// (application-cursor-keys OFF) encoding; full-screen apps that enable DECCKM still
// interpret the CSI form, and `top`/`vim`/`nano` work with it.
func specialKeyBytes(code rune) ([]byte, bool) {
	switch code {
	case tea.KeyEnter:
		return []byte{'\r'}, true
	case tea.KeyTab:
		return []byte{'\t'}, true
	case tea.KeyEscape:
		return []byte{0x1b}, true
	case tea.KeyBackspace:
		return []byte{0x7f}, true
	case tea.KeySpace:
		return []byte{' '}, true

	case tea.KeyUp:
		return []byte("\x1b[A"), true
	case tea.KeyDown:
		return []byte("\x1b[B"), true
	case tea.KeyRight:
		return []byte("\x1b[C"), true
	case tea.KeyLeft:
		return []byte("\x1b[D"), true
	case tea.KeyHome:
		return []byte("\x1b[H"), true
	case tea.KeyEnd:
		return []byte("\x1b[F"), true
	case tea.KeyInsert:
		return []byte("\x1b[2~"), true
	case tea.KeyDelete:
		return []byte("\x1b[3~"), true
	case tea.KeyPgUp:
		return []byte("\x1b[5~"), true
	case tea.KeyPgDown:
		return []byte("\x1b[6~"), true

	// Function keys: F1–F4 use SS3 (\x1bO{P..S}); F5+ use the CSI ~ form, matching
	// xterm.
	case tea.KeyF1:
		return []byte("\x1bOP"), true
	case tea.KeyF2:
		return []byte("\x1bOQ"), true
	case tea.KeyF3:
		return []byte("\x1bOR"), true
	case tea.KeyF4:
		return []byte("\x1bOS"), true
	case tea.KeyF5:
		return []byte("\x1b[15~"), true
	case tea.KeyF6:
		return []byte("\x1b[17~"), true
	case tea.KeyF7:
		return []byte("\x1b[18~"), true
	case tea.KeyF8:
		return []byte("\x1b[19~"), true
	case tea.KeyF9:
		return []byte("\x1b[20~"), true
	case tea.KeyF10:
		return []byte("\x1b[21~"), true
	case tea.KeyF11:
		return []byte("\x1b[23~"), true
	case tea.KeyF12:
		return []byte("\x1b[24~"), true
	}
	return nil, false
}

// ctrlByte maps Ctrl+<key> to its C0 control byte, or ok=false when there is no
// control encoding for code. Covers Ctrl+A..Z (0x01..0x1a), Ctrl+@ / Ctrl+Space
// (NUL), and the Ctrl+[ \ ] ^ _ block (0x1b..0x1f) — the bytes a real terminal
// produces for these chords.
func ctrlByte(code rune) (byte, bool) {
	switch {
	case code >= 'a' && code <= 'z':
		return byte(code-'a') + 1, true // ctrl+a → 0x01
	case code >= 'A' && code <= 'Z':
		return byte(code-'A') + 1, true
	case code == ' ' || code == '@':
		return 0x00, true // ctrl+space / ctrl+@ → NUL
	case code == '[':
		return 0x1b, true
	case code == '\\':
		return 0x1c, true
	case code == ']':
		return 0x1d, true
	case code == '^':
		return 0x1e, true
	case code == '_':
		return 0x1f, true
	}
	return 0, false
}
