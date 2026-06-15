package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestPhysKey_DirectCases unit-tests physKey across every branch.
func TestPhysKey_DirectCases(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		// Fast path: a plain ASCII letter from a US layout returns unchanged.
		{"ascii letter", tea.KeyPressMsg{Text: "n", Code: 'n', BaseCode: 'n'}, "n"},
		// Fast path: a named key is pure ASCII → unchanged.
		{"named enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "enter"},
		{"named esc", tea.KeyPressMsg{Code: tea.KeyEscape}, "esc"},
		{"named up", tea.KeyPressMsg{Code: tea.KeyUp}, "up"},
		// Fast path: a ctrl combo is pure ASCII → unchanged (already works on every layout).
		{"ctrl+q", tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}, "ctrl+q"},

		// Russian layout: physical N produces "т"; recover "n" from BaseCode.
		{"ru letter n", tea.KeyPressMsg{Text: "т", Code: 'т', BaseCode: 'n'}, "n"},
		// Russian layout shift: physical shift+N produces "Т"; recover upper "N".
		{"ru shift N", tea.KeyPressMsg{Text: "Т", Code: 'т', BaseCode: 'n', Mod: tea.ModShift}, "N"},
		// Russian layout: physical '.' position. On RU JCUKEN that physical key produces
		// the Cyrillic "ю" (non-ASCII); BaseCode is '.', so the hidden-toggle shortcut
		// recovers ".".
		{"ru dot", tea.KeyPressMsg{Text: "ю", Code: 'ю', BaseCode: '.'}, "."},
		// A physical '/' key that produces a NON-ASCII char (forces the BaseCode path) →
		// recovers "/" unshifted.
		{"nonascii slash", tea.KeyPressMsg{Text: "ё", Code: 'ё', BaseCode: '/'}, "/"},
		// Shifted ';' producing a non-ASCII char → ":" via the US-shift punct map.
		{"shift semicolon to colon", tea.KeyPressMsg{Text: "Ж", Code: 'Ж', BaseCode: ';', Mod: tea.ModShift}, ":"},
		// Shifted '/' producing a non-ASCII char → "?" via the US-shift punct map.
		{"shift slash to question", tea.KeyPressMsg{Text: "Ё", Code: 'Ё', BaseCode: '/', Mod: tea.ModShift}, "?"},
		// Caps/ShiftedCode signal (no ModShift) still upper-cases a letter.
		{"shiftedcode upper", tea.KeyPressMsg{Text: "Т", Code: 'т', BaseCode: 'n', ShiftedCode: 'N'}, "N"},

		// Fallback: BaseCode == 0 (non-Kitty Linux/macOS) AND non-ASCII Text/Code →
		// unrecoverable, return String() unchanged.
		{"basecode zero fallback", tea.KeyPressMsg{Text: "т", Code: 'т', BaseCode: 0}, "т"},
		// Legacy conhost (BaseCode=0): String() is the non-ASCII Text "ё" (forces the
		// fallback path), but Key.Code is the ASCII rune 'n' → recover the hotkey from Code.
		{"basecode zero code ascii", tea.KeyPressMsg{Text: "ё", Code: 'n', BaseCode: 0}, "n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := physKey(c.msg); got != c.want {
				t.Fatalf("physKey(%+v) = %q want %q", c.msg, got, c.want)
			}
		})
	}
}

// TestPhysKey_RUMatchesEN asserts a Russian-layout press dispatches identically to the
// equivalent US press through the FM new-folder handler: a synthetic Cyrillic 'n' opens
// the new-folder prompt exactly like a plain 'n'.
func TestPhysKey_RUNewFolderMatchesEN(t *testing.T) {
	en := filesKeyModel([]fileEntry{{name: ".."}, {name: "a"}})
	enOut, _ := en.filesKey(tea.KeyPressMsg{Text: "n", Code: 'n', BaseCode: 'n'})
	en = enOut.(model)
	if en.files.promptKind != fpNewDir {
		t.Fatalf("US 'n' must open the new-folder prompt, got kind=%d", en.files.promptKind)
	}

	ru := filesKeyModel([]fileEntry{{name: ".."}, {name: "a"}})
	ruOut, _ := ru.filesKey(tea.KeyPressMsg{Text: "т", Code: 'т', BaseCode: 'n'})
	ru = ruOut.(model)
	if ru.files.promptKind != fpNewDir {
		t.Fatalf("RU physical-N must open the new-folder prompt like US 'n', got kind=%d", ru.files.promptKind)
	}
}

// TestPhysKey_RUToggleHidden asserts the RU physical '.' key toggles hidden files (the
// punctuation-shortcut path), matching a US '.'.
func TestPhysKey_RUToggleHidden(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: ".dot"}, {name: "a"}})
	out, _ := m.filesKey(tea.KeyPressMsg{Text: "ю", Code: 'ю', BaseCode: '.'})
	m = out.(model)
	if !m.files.showHidden {
		t.Fatal("RU physical '.' must toggle showHidden on like US '.'")
	}
}

// TestPhysKey_RUAddressFocus asserts a physical '/' key that produces a non-ASCII char
// (forcing the BaseCode recovery path) focuses the address bar like a US '/'.
func TestPhysKey_RUAddressFocus(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: "a"}})
	out, _ := m.filesKey(tea.KeyPressMsg{Text: "ё", Code: 'ё', BaseCode: '/'})
	m = out.(model)
	if !m.files.addrFocus {
		t.Fatal("physical '/' (non-ASCII output) must focus the address bar like US '/'")
	}
}

// TestPhysKey_TextEntryStaysRaw asserts a TEXT-ENTRY path (the FM address bar) still
// receives the RAW Cyrillic character — normalization must never reach .Update.
func TestPhysKey_AddrTextEntryStaysRaw(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: "a"}})
	// Focus the address bar (US '/').
	out, _ := m.filesKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = out.(model)
	if !m.files.addrFocus {
		t.Fatal("precondition: address bar must be focused")
	}
	m.files.addr.SetValue("")
	// Type a Cyrillic "т" into the focused address bar — it must land verbatim.
	out, _ = m.filesKey(tea.KeyPressMsg{Text: "т", Code: 'т', BaseCode: 'n'})
	m = out.(model)
	if got := m.files.addr.Value(); got != "т" {
		t.Fatalf("address bar must receive raw Cyrillic, got %q", got)
	}
}
