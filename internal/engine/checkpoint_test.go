package engine

import "testing"

func TestStaleCheckpoint(t *testing.T) {
	cases := []struct {
		completed int
		hardened  bool
		wantStale bool
	}{
		{0, false, false}, // fresh box, empty checkpoint — not stale
		{14, true, false}, // hardened box, full checkpoint — legit, not stale
		{14, false, true}, // reinstalled box, stale checkpoint — STALE
		{2, false, true},  // partial checkpoint, no markers — stale
		{0, true, false},  // hardened box, empty checkpoint — not stale
	}
	for _, c := range cases {
		if got := staleCheckpoint(c.completed, c.hardened); got != c.wantStale {
			t.Errorf("staleCheckpoint(%d,%v)=%v want %v", c.completed, c.hardened, got, c.wantStale)
		}
	}
}
