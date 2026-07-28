package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// TestTerminalFollowSurvivesTabRoundTrip proves follow mode still pins the view to the
// newest output after Терминал → Главная → Терминал.
//
// Follow mode is re-pinned ONLY by the repaint heartbeat (termTickMsg → termPinIfFollowing)
// and by a forwarded keystroke. Switching to Главная parks the model on phaseDashboard,
// where the tick handler's phase gate retires the in-flight tick WITHOUT rescheduling it.
// So re-entering Терминал over a live session must hand the runtime a fresh termTick —
// otherwise the heartbeat is gone for the rest of the session and new output never
// scrolls into view (the user has to reach the bottom by hand with the wheel).
func TestTerminalFollowSurvivesTabRoundTrip(t *testing.T) {
	m, _ := termModel(t, 60, 20)
	defer m.term.close()
	m.termClient = &sshx.Client{} // non-nil transport so openTerminal takes the live-reuse path
	_, rows := m.termContentSize()

	seedLines(t, m, 40, 5)

	// First entry: the heartbeat is running — a tick re-schedules itself.
	if _, cmd := m.Update(termTickMsg{gen: m.termGen}); cmd == nil {
		t.Fatal("on first entry a termTickMsg must re-schedule the repaint tick")
	}

	// Терминал → Главная. The session stays alive, but the in-flight tick retires here.
	next, _ := m.navTo(navHome)
	mm := next.(model)
	if _, cmd := mm.Update(termTickMsg{gen: mm.termGen}); cmd != nil {
		t.Fatal("a tick delivered on the Dashboard must retire (phase gate), not re-schedule")
	}

	// Главная → Терминал over the same live session.
	next, cmd := mm.navTo(navTerminal)
	mm = next.(model)
	if cmd == nil {
		t.Fatal("re-entering Терминал returned no cmd: the repaint tick is dead, so follow mode never re-pins again")
	}

	// New output arrives, then the runtime delivers the tick the re-entry scheduled.
	seedLines(t, mm, 40, 10)
	msg := cmd()
	tick, ok := msg.(termTickMsg)
	if !ok {
		t.Fatalf("re-entry cmd produced %T, want termTickMsg", msg)
	}
	next, _ = mm.Update(tick)
	mm = next.(model)

	if want := max(mm.termBodyLen()-rows, 0); mm.termScroll != want {
		t.Fatalf("after the tab round-trip follow mode left termScroll=%d, want bottom %d", mm.termScroll, want)
	}
	if !mm.termFollow {
		t.Fatal("termFollow must still be armed after a tab round-trip")
	}
}

// TestTerminalReentryFirstFrameIsAtBottom pins the frame the tick never gets to fix:
// while parked on Главная nothing re-pins, so output arriving there leaves termScroll
// behind the body. Re-entry must land at the bottom on the FIRST rendered frame — the
// restarted heartbeat is up to one tick (25ms) away, and a frame drawn at the stale
// offset in the meantime is a visible jump.
func TestTerminalReentryFirstFrameIsAtBottom(t *testing.T) {
	m, _ := termModel(t, 60, 20)
	defer m.term.close()
	m.termClient = &sshx.Client{}
	_, rows := m.termContentSize()

	seedLines(t, m, 40, 5)
	m.termPinIfFollowing()

	// Park on Главная; the heartbeat retires there.
	next, _ := m.navTo(navHome)
	mm := next.(model)

	// Output keeps arriving while parked — nothing re-pins it.
	mm.term.write([]byte("MARKER-ZZ\r\n"))
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(mm.term.view(), "MARKER-ZZ")
	}, "marker line to reach the emulator")

	// Back to Терминал. No tick pumped: this is the first frame.
	next, _ = mm.navTo(navTerminal)
	mm = next.(model)

	if want := max(mm.termBodyLen()-rows, 0); mm.termScroll != want {
		t.Fatalf("first frame after re-entry at termScroll=%d, want bottom %d", mm.termScroll, want)
	}
	if !strings.Contains(mm.terminalView(), "MARKER-ZZ") {
		t.Fatal("the first frame after re-entry does not show output that arrived while parked")
	}
}
