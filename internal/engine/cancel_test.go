package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/UberMorgott/morgward/internal/state"
	"github.com/UberMorgott/morgward/internal/steps"
	"github.com/UberMorgott/morgward/internal/ui"
)

// fakeStep is a no-op step that records whether its Run was invoked, so a test can
// assert the engine stopped at a boundary without touching SSH.
type fakeStep struct {
	id  string
	ran *bool
}

func (f fakeStep) ID() string    { return f.id }
func (f fakeStep) Title() string { return "fake " + f.id }
func (f fakeStep) Run(*steps.Context) (steps.Status, string, error) {
	*f.ran = true
	return steps.StatusOK, "ok", nil
}

// TestRunStepListCancelBeforeFirstStep proves F03's safe-boundary semantics: a
// context already cancelled when runStepList starts halts the run at the boundary
// BEFORE the first step's Run, returns ErrCanceled, and never executes the step.
func TestRunStepListCancelBeforeFirstStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled up front

	var ran bool
	s := &session{
		log: ui.New(""),
		ctx: &steps.Context{Ctx: ctx, State: state.Load("")},
	}
	_, err := runStepList(ctx, s, []steps.Step{fakeStep{id: "A1", ran: &ran}}, false, Hooks{})

	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("err = %v, want ErrCanceled", err)
	}
	if ran {
		t.Fatal("step ran despite the context being cancelled before the boundary")
	}
}

// TestRunStepListCancelBetweenSteps proves cancellation observed AFTER the first
// step stops the run at the NEXT boundary: step 1 runs to completion (atomic),
// step 2 never starts, and ErrCanceled is returned.
func TestRunStepListCancelBetweenSteps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var ran1, ran2 bool
	// step1 cancels the run from inside its Run — simulating the operator aborting
	// while a step is mid-flight. The step still finishes (atomic), then the loop's
	// boundary check halts before step2.
	step1 := cancelingStep{id: "A1", ran: &ran1, cancel: cancel}
	step2 := fakeStep{id: "A2", ran: &ran2}

	s := &session{
		log: ui.New(""),
		ctx: &steps.Context{Ctx: ctx, State: state.Load("")},
	}
	c, err := runStepList(ctx, s, []steps.Step{step1, step2}, false, Hooks{})

	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("err = %v, want ErrCanceled", err)
	}
	if !ran1 {
		t.Fatal("step1 should have run to completion before the cancellation took effect")
	}
	if ran2 {
		t.Fatal("step2 ran despite cancellation at the prior boundary")
	}
	if c.ok != 1 {
		t.Fatalf("ok count = %d, want 1 (only step1 applied)", c.ok)
	}
}

// cancelingStep runs to completion but cancels the run context as a side effect,
// modeling an abort that lands mid-step; the loop must still let it finish.
type cancelingStep struct {
	id     string
	ran    *bool
	cancel context.CancelFunc
}

func (f cancelingStep) ID() string    { return f.id }
func (f cancelingStep) Title() string { return "canceling " + f.id }
func (f cancelingStep) Run(*steps.Context) (steps.Status, string, error) {
	*f.ran = true
	f.cancel()
	return steps.StatusOK, "ok", nil
}
