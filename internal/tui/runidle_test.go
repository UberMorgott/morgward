package tui

import (
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/ui"
)

// TestRunIdleHeartbeat asserts the idle heartbeat appears on the run hint line only after
// runIdleHeartbeat silent ticks and clears on the next streamed output line. Driven by
// fake tickMsg/logMsg through Update — the same 1s tick that runs the spinner, no second
// ticker.
func TestRunIdleHeartbeat(t *testing.T) {
	m, _ := runNavModel()
	tick := func(mo model) model {
		next, _ := mo.Update(tickMsg{})
		return asModel(t, next)
	}
	for range runIdleHeartbeat {
		m = tick(m)
	}
	if hb := m.idleHeartbeat(); hb != "" {
		t.Fatalf("heartbeat at %d ticks = %q, want silence until >%d", runIdleHeartbeat, hb, runIdleHeartbeat)
	}
	mm := tick(m) // 31st silent tick
	if hb := mm.idleHeartbeat(); hb == "" {
		t.Fatalf("no heartbeat after %d silent ticks", runIdleHeartbeat+1)
	}
	if !strings.Contains(ui.StripControlAndANSI(mm.runHints()), "31") {
		t.Fatalf("hint line lacks the idle seconds: %q", mm.runHints())
	}

	// An output line clears it.
	next, _ := mm.Update(logMsg("apt-get: waiting for lock\n"))
	am := asModel(t, next)
	if am.idleTicks != 0 {
		t.Fatalf("logMsg left idleTicks=%d, want 0", am.idleTicks)
	}
	if hb := am.idleHeartbeat(); hb != "" {
		t.Fatalf("heartbeat survived an output line: %q", hb)
	}
}
