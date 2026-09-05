package steps

import (
	"context"
	"testing"
)

// Once A8 has ISSUED the reboot, the reconnect+restore phase must survive a
// cancelled run context (Esc in the TUI → goBack cancels ctx.Ctx). Without this,
// every connect() inside WaitForReboot fails instantly, the poll loop gives up and
// restoreServices never runs — leaving a brownfield box we rebooted with its
// `restart: no` containers and non-enabled units down. Values must still pass
// through, and WaitForReboot's own deadline stays the bound.
func TestRebootCtxSurvivesCancellation(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "v"))
	rc := rebootCtx(parent)
	cancel()

	if err := rc.Err(); err != nil {
		t.Errorf("reboot ctx must ignore the cancelled parent, got %v", err)
	}
	select {
	case <-rc.Done():
		t.Error("reboot ctx must not be done after the parent is cancelled")
	default:
	}
	if rc.Value(key{}) != "v" {
		t.Error("reboot ctx must keep the parent's values")
	}
	//nolint:staticcheck // SA1012: a nil parent is exactly the input this guard exists for.
	if rebootCtx(nil) == nil {
		t.Error("rebootCtx(nil) must return a usable context")
	}
}
