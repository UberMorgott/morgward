package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// asModel is the CHECKED tea.Model→model assertion every test uses after Update.
// Update always returns our own model, so a failed assertion means the model type
// itself drifted — fail the test with the type we actually got instead of panicking
// on an unchecked assertion.
func asModel(t testing.TB, v tea.Model) model {
	t.Helper()
	m, ok := v.(model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.model", v)
	}
	return m
}
