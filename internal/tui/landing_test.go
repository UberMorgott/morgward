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

func init() {
	// keep lipgloss import referenced for later tasks even before first use
	_ = lipgloss.Width
	_ = strings.TrimSpace
}
