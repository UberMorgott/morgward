package engine

import "testing"

// TestSelectStepsResolvesA2Split asserts the split IDs resolve via the selective
// path WITHOUT being part of the default full-run sequence.
func TestSelectStepsResolvesA2Split(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"A2-safe"}, []string{"A2-safe"}},
		{[]string{"A2-danger"}, []string{"A2-danger"}},
		{[]string{"PRE"}, []string{"PRE"}},
		{[]string{"PRE", "A2-safe"}, []string{"PRE", "A2-safe"}},
		// Case-insensitive + still resolves.
		{[]string{"a2-DANGER"}, []string{"A2-danger"}},
	}
	for _, tc := range cases {
		sel, unknown := selectSteps(tc.in)
		if len(unknown) != 0 {
			t.Errorf("selectSteps(%v): unexpected unknown ids %v", tc.in, unknown)
		}
		var got []string
		for _, s := range sel {
			got = append(got, s.ID())
		}
		if len(got) != len(tc.want) {
			t.Fatalf("selectSteps(%v) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("selectSteps(%v) = %v, want %v", tc.in, got, tc.want)
			}
		}
	}
}

// TestA2SplitNotInDefaultRun guards the locked decision: the split steps must NOT
// appear in the default full-run sequence (legacy A2SSH stays the full-run step).
func TestA2SplitNotInDefaultRun(t *testing.T) {
	for _, st := range orderedSteps() {
		if st.ID() == "A2-safe" || st.ID() == "A2-danger" {
			t.Fatalf("split step %q must not be in orderedSteps()", st.ID())
		}
	}
	// And the legacy step is still present.
	found := false
	for _, st := range orderedSteps() {
		if st.ID() == "A2" {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy A2 step missing from orderedSteps()")
	}
}

// TestSelectStepsUnknown still reports genuinely unknown ids.
func TestSelectStepsUnknown(t *testing.T) {
	_, unknown := selectSteps([]string{"NOPE"})
	if len(unknown) != 1 || unknown[0] != "NOPE" {
		t.Fatalf("unknown=%v want [NOPE]", unknown)
	}
}
