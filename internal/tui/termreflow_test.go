package tui

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTermRingRecordsTail proves the bounded ring buffer keeps the most-recent
// `cap` bytes of everything written: after writing more than its capacity, Bytes()
// returns exactly the tail (oldest bytes dropped on overflow).
func TestTermRingRecordsTail(t *testing.T) {
	r := newTermRing(8)

	r.append([]byte("abc"))
	if got := string(r.bytes()); got != "abc" {
		t.Fatalf("under-cap ring = %q, want %q", got, "abc")
	}

	// Total 12 bytes into an 8-byte ring → the last 8 survive.
	r.append([]byte("defghijklmno"[:9])) // "defghijkl" (now 12 total)
	want := "efghijkl"
	if got := string(r.bytes()); got != want {
		t.Fatalf("over-cap ring = %q, want tail %q", got, want)
	}
}

// TestTermRingSingleAppendOverflow proves a single append larger than the ring keeps
// only the trailing `cap` bytes of that append (the common big-paste case).
func TestTermRingSingleAppendOverflow(t *testing.T) {
	r := newTermRing(4)
	r.append([]byte("0123456789"))
	if got := string(r.bytes()); got != "6789" {
		t.Fatalf("single oversized append ring = %q, want %q", got, "6789")
	}
}

// TestReflowRestoresContentOnGrowBack is the core reflow guarantee. A line longer
// than the initial narrow width is fed through the echo seam; we shrink (so x/vt's
// truncating Resize would cut it), then grow back wider than the original and call
// reflowNow synchronously. Because reflow rebuilds the emulator and REPLAYS the raw
// stream, the full line content must reappear — not be permanently truncated.
func TestReflowRestoresContentOnGrowBack(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 20, 6)
	defer s.close()
	<-fs.started

	// A 40-char line: wider than the initial 20 cols (wraps), and we want it intact
	// after shrinking to 10 then growing to 60.
	line := strings.Repeat("X", 40)
	s.write([]byte(line))
	waitFor(t, time.Second, func() bool {
		return strings.Count(stripBlanks(s.view()), "X") >= 40
	}, "echoed long line to render at initial width")

	// Shrink hard: x/vt truncates, losing the wrapped tail.
	s.reflowNow(10, 6)
	// Grow back wider than the original line: a faithful replay restores all 40 X's.
	s.reflowNow(60, 6)

	if n := strings.Count(stripBlanks(s.view()), "X"); n != 40 {
		t.Fatalf("after shrink+grow reflow, view has %d X's, want 40 (content truncated, not rewrapped)", n)
	}
}

// TestReflowEmptyRingIsSafe proves reflowNow before any output (empty ring) is a
// no-op-safe rebuild: it must not panic and must leave a usable emulator at the new
// size.
func TestReflowEmptyRingIsSafe(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 80, 24)
	defer s.close()
	<-fs.started

	s.reflowNow(40, 10)
	if w, h := s.emu.Width(), s.emu.Height(); w != 40 || h != 10 {
		t.Fatalf("emulator after empty-ring reflow = %dx%d, want 40x10", w, h)
	}
}

// TestReflowAfterCloseIsSafe proves reflowNow after close() is a harmless no-op (no
// panic, no resurrection of a torn-down session) — the debounce goroutine that also
// calls reflowNow must be reaped, and any late direct call must be inert.
func TestReflowAfterCloseIsSafe(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 80, 24)
	<-fs.started
	s.write([]byte("hello"))
	s.close()

	// Must not panic; closed session ignores the reflow.
	s.reflowNow(40, 10)
}

// TestResizeReplaysAfterDebounce proves the integration path: a resize() that shrinks
// then a resize() that grows back triggers a DEBOUNCED replay which restores the
// content without any direct reflowNow call. Timing-tolerant via waitFor.
func TestResizeReplaysAfterDebounce(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 20, 6)
	defer s.close()
	<-fs.started

	line := strings.Repeat("Y", 40)
	s.write([]byte(line))
	waitFor(t, time.Second, func() bool {
		return strings.Count(stripBlanks(s.view()), "Y") >= 40
	}, "echoed long line at initial width")

	s.resize(10, 6) // truncating shrink
	s.resize(60, 6) // grow back — debounced replay should restore the line

	waitFor(t, 2*time.Second, func() bool {
		return strings.Count(stripBlanks(s.view()), "Y") == 40
	}, "debounced reflow to restore the full line after grow-back")
}

// TestTeardownReapsReflowTimer extends the teardown contract: after close() the
// debounce timer must be stopped so no reflow fires post-close. We schedule a resize
// (arming the debounce), close immediately, and assert the session is finished and a
// subsequent reflowNow is inert — proving no goroutine/timer outlives close().
func TestTeardownReapsReflowTimer(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 20, 6)
	<-fs.started

	s.write([]byte(strings.Repeat("Z", 40)))
	s.resize(10, 6) // arm the debounce timer
	s.close()       // must stop the timer before it fires

	select {
	case <-fs.returned:
	case <-time.After(time.Second):
		t.Fatal("Shell goroutine not reaped after close() with a pending reflow — leak")
	}
	waitFor(t, time.Second, func() bool { d, _ := s.finished(); return d }, "session to mark finished")

	// A late reflow is a safe no-op on a closed session.
	s.reflowNow(60, 6)
}

// TestAltOffReflowDoesNotDeadlockDrain is the regression guard for the self-deadlock:
// the AltScreen(false) callback fires SYNCHRONOUSLY inside emu.Write while the drain
// goroutine holds out.mu, so an owed reflow run inline would re-lock out.mu on the same
// goroutine and wedge the drain forever (→ SSH back-pressure → remote freeze). We drive
// the exact path through the live drain: enter alt-screen, resize() (arming altReflowWait),
// then write alt-off THROUGH the pipe→fakeShell echo→termOut.Write so the callback fires
// on the drain goroutine under out.mu. A subsequent write must still be drained (proving
// the drain goroutine is not wedged). Guarded by a timeout — a hang = the deadlock.
func TestAltOffReflowDoesNotDeadlockDrain(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 40, 12)
	defer s.close()
	<-fs.started

	// Enter alt-screen via the live drain; wait until the callback has set alt.
	s.write([]byte("\x1b[?1049h"))
	waitFor(t, time.Second, func() bool { return s.altScreen() }, "emulator to enter alt-screen")

	// Resize while on alt: this arms altReflowWait so leaving alt owes a replay.
	s.resize(20, 12)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Alt-off through the drain → AltScreen(false) fires under out.mu on the drain
		// goroutine. If the owed reflow runs inline it re-locks out.mu → deadlock here.
		s.write([]byte("\x1b[?1049l"))
		// A follow-up write must still be echoed+drained, proving the drain lives.
		s.write([]byte("ALIVE"))
		waitFor(t, time.Second, func() bool {
			return strings.Contains(stripBlanks(s.view()), "ALIVE")
		}, "drain to keep echoing after alt-off")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drain deadlocked on alt-off reflow (out.mu re-locked on the drain goroutine)")
	}
}

// TestConcurrentReflowNowSerializes proves overlapping reflowNow calls (the debounce
// timer and the alt-off timer can fire on different goroutines) complete without hang or
// panic and leave a CONSISTENT emulator, even with alt-on/alt-off in the replayed ring
// (which re-fires the AltScreen callback mid-replay). reflowRunMu serializes the calls
// end-to-end, so the `replaying` guard stays single-owned and the final geometry is
// exactly the last call's size.
func TestConcurrentReflowNowSerializes(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 40, 12)
	defer s.close()
	<-fs.started

	// Seed the ring (through the drain) with an alt-on/alt-off cycle plus content, so a
	// replay re-emits the alt toggles and exercises the replaying guard.
	s.write([]byte("before\r\n\x1b[?1049h" + strings.Repeat("A", 30) + "\x1b[?1049l" + "after"))
	waitFor(t, time.Second, func() bool {
		return strings.Contains(stripBlanks(s.view()), "after")
	}, "drain to record the seed stream")

	// Fire many reflowNow concurrently; all must complete (serialized by reflowRunMu).
	const goroutines = 16
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				// Alternate sizes; the LAST call to win the final swap is non-deterministic,
				// but every call leaves a valid emulator and none may hang/panic.
				s.reflowNow(20+n%20, 10)
			}(i)
		}
		wg.Wait()
		// A final, deterministic reflow fixes the end geometry for the assertion.
		s.reflowNow(50, 14)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent reflowNow hung — serialization broken")
	}

	if w, h := s.emu.Width(), s.emu.Height(); w != 50 || h != 14 {
		t.Fatalf("final emulator = %dx%d, want 50x14 (last reflow geometry)", w, h)
	}
}

// stripBlanks removes spaces and newlines so an X/Y count is independent of the
// emulator's padding/wrapping layout — we only care that the CONTENT survived reflow.
func stripBlanks(s string) string {
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// ensure bytes import stays used if a future assertion needs it.
var _ = bytes.Equal
