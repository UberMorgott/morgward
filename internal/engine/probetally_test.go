package engine

import (
	"testing"

	"github.com/UberMorgott/morgward/internal/steps"
	"github.com/UberMorgott/morgward/internal/tweaks"
)

func probeRes(step string, applied, info bool) tweaks.Result {
	return tweaks.Result{Probe: tweaks.Probe{Step: step, Informational: info}, Applied: applied, Informational: info}
}

// TestProbeTallyCountsOnlyExecutedSteps pins the honest denominator: probes of a
// step this pass never ran (A2 for the tweak bucket) and of a SKIPPED step stay
// out; a FAILED step's probes stay in, and Informational rows never count.
func TestProbeTallyCountsOnlyExecutedSteps(t *testing.T) {
	all := []tweaks.Result{
		probeRes("A1", true, false),
		probeRes("A2", false, false),  // step never selected
		probeRes("A6", false, false),  // step skipped
		probeRes("A9", false, false),  // step failed -> must count (red)
		probeRes("A10", true, true),   // informational
		probeRes("A6.5", true, false), // dotted id, matches verbatim
	}
	done := []StepResult{
		{ID: "A1", Status: steps.StatusOK},
		{ID: "A6", Status: steps.StatusSkip},
		{ID: "A6.5", Status: steps.StatusOK},
		{ID: "A9", Status: steps.StatusFail},
		{ID: "A10", Status: steps.StatusOK},
	}
	passed, total := probeTally(all, done)
	if passed != 2 || total != 3 {
		t.Fatalf("got %d/%d, want 2/3", passed, total)
	}
}

// TestProbeTallyA2SplitNormalized pins that the A2-safe/A2-danger step IDs map onto
// the "A2"-tagged probes.
func TestProbeTallyA2SplitNormalized(t *testing.T) {
	all := []tweaks.Result{probeRes("A2", true, false)}
	for _, id := range []string{"A2", "A2-safe", "A2-danger"} {
		if _, total := probeTally(all, []StepResult{{ID: id, Status: steps.StatusOK}}); total != 1 {
			t.Fatalf("%s: A2 probe not counted", id)
		}
	}
	if _, total := probeTally(all, []StepResult{{ID: "A1", Status: steps.StatusOK}}); total != 0 {
		t.Fatalf("A2 probe counted for an A1-only pass")
	}
}
