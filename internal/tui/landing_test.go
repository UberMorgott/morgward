package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// formModel builds a form-phase model sized for layout tests.
func formModel(w, h int) model {
	m := newModel()
	m.w, m.h = w, h
	return m
}

// TestLandingFormPhaseExists asserts the form phase still renders without panic
// and that advancedOpen defaults to false on a fresh model.
func TestLandingFormPhaseExists(t *testing.T) {
	m := newModel()
	if m.phase != phaseForm {
		t.Fatalf("newModel phase=%v want phaseForm", m.phase)
	}
	if m.advancedOpen {
		t.Fatalf("advancedOpen should default to false")
	}
	m.w, m.h = 80, 24
	if s := m.formView(); s == "" {
		t.Fatalf("formView returned empty string")
	}
}

// TestDisclosureKeysExist asserts the disclosure label/state keys translate
// non-empty in both languages.
func TestDisclosureKeysExist(t *testing.T) {
	for _, lang := range []Lang{langRU, langEN} {
		if s := t2(lang, kDisclosureLabel); s == "" {
			t.Fatalf("lang %d kDisclosureLabel empty", lang)
		}
		if s := t2(lang, kDisclosureOpen); s == "" {
			t.Fatalf("lang %d kDisclosureOpen empty", lang)
		}
	}
}

// t2 is a thin alias for t so tests read clearly (t shadows *testing.T name).
func t2(lang Lang, k stringKey) string { return t(lang, k) }

// TestFramedInputRender3Rows asserts framedInputRow returns exactly three lines
// (top border, label+value, bottom border) and that no line is wider than the
// box inner width.
func TestFramedInputRender3Rows(t *testing.T) {
	m := formModel(80, 24)
	lines := m.framedInputRow(fHost, m.lang, "Хост", m.inputs[fHost], true)
	if len(lines) != 3 {
		t.Fatalf("framedInputRow returned %d lines, want 3", len(lines))
	}
	innerW := innerWidth(m.boxWidth())
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w > innerW {
			t.Fatalf("line %d width %d > innerW %d: %q", i, w, innerW, ln)
		}
	}
	// Middle line must carry the label text.
	if !strings.Contains(lines[1], "Хост") {
		t.Fatalf("middle line missing label: %q", lines[1])
	}
}

func init() {
	// keep lipgloss import referenced for later tasks even before first use
	_ = lipgloss.Width
	_ = strings.TrimSpace
}
