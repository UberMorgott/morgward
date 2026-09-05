package tui

import (
	"bytes"
	"context"
	"regexp"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// recShell is a shellFunc that RECORDS everything the session sends to the remote pty
// stdin (ShellIO.In) instead of echoing it. That is the sink the emulator's query
// answers (DSR/CPR) must reach, so it is what the drain tests assert on.
type recShell struct {
	mu      sync.Mutex
	got     []byte
	started chan struct{}
}

func newRecShell() *recShell { return &recShell{started: make(chan struct{})} }

func (r *recShell) run(ctx context.Context, sio sshx.ShellIO, _ <-chan sshx.WinSize) error {
	close(r.started)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 128)
		for {
			n, err := sio.In.Read(buf)
			if n > 0 {
				r.mu.Lock()
				r.got = append(r.got, buf[:n]...)
				r.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (r *recShell) sent() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Clone(r.got)
}

// cprRe matches a cursor-position report (CSI <row> ; <col> R).
var cprRe = regexp.MustCompile(`\x1b\[\d+;\d+R`)

// TestEmuDrainAnswersCursorQuery is the regression test for the terminal hang: the
// emulator answers \x1b[6n by writing into an UNBUFFERED pipe, so with no drain the
// vt.Write inside termOut.Write blocks forever under out.mu and the whole terminal
// wedges (whiptail/dialog/vim query the cursor on startup). The drain must (a) let the
// write complete and (b) forward the CPR reply to the remote pty stdin.
func TestEmuDrainAnswersCursorQuery(t *testing.T) {
	rs := newRecShell()
	s := newTermSessionWith(rs.run, 80, 24)
	defer s.close()
	<-rs.started

	// Simulate remote output containing a cursor-position query. Without the drain this
	// call never returns and the test times out.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.out.Write([]byte("hi\x1b[6n"))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("termOut.Write blocked on an un-drained emulator reply (terminal hang)")
	}

	waitFor(t, 2*time.Second, func() bool { return cprRe.Match(rs.sent()) },
		"CPR reply forwarded to the remote pty stdin")
}

// TestStripQueries pins the replay filter: every sequence the emulator would ANSWER is
// removed, everything that paints is kept byte-for-byte.
func TestStripQueries(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"cursor position report", "a\x1b[6nb", "ab"},
		{"bare DSR", "\x1b[n", ""},
		{"primary DA", "x\x1b[cy", "xy"},
		{"secondary DA", "\x1b[>0c", ""},
		{"mode request", "\x1b[?2026$p", ""},
		{"keeps SGR", "\x1b[1;31mred\x1b[0m", "\x1b[1;31mred\x1b[0m"},
		{"keeps cursor moves", "\x1b[2J\x1b[H\x1b[10;5Hhi", "\x1b[2J\x1b[H\x1b[10;5Hhi"},
		{"keeps alt-screen toggle", "\x1b[?1049h\x1b[?1049l", "\x1b[?1049h\x1b[?1049l"},
		{"keeps OSC title", "\x1b]2;box\x07", "\x1b]2;box\x07"},
		{"plain text", "hello\r\n", "hello\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(stripQueries([]byte(c.in))); got != c.want {
				t.Fatalf("stripQueries(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestEmuDrainSurvivesReflow proves the fresh emulator a reflow builds gets its OWN
// drain (otherwise the first query after any window resize would hang the terminal),
// and that the replayed stream answers nothing twice (stripQueries).
func TestEmuDrainSurvivesReflow(t *testing.T) {
	rs := newRecShell()
	s := newTermSessionWith(rs.run, 80, 24)
	defer s.close()
	<-rs.started

	_, _ = s.out.Write([]byte("x\x1b[6n"))
	waitFor(t, 2*time.Second, func() bool { return len(cprRe.FindAll(rs.sent(), -1)) == 1 },
		"first CPR reply")

	// The ring holds the \x1b[6n; replaying it into the fresh emulator must not hang and
	// must not emit a second (stale) answer.
	s.reflowNow(100, 30)
	if got := len(cprRe.FindAll(rs.sent(), -1)); got != 1 {
		t.Fatalf("stale CPR replies after reflow: got %d, want 1", got)
	}

	// A query on the NEW emulator is still answered.
	_, _ = s.out.Write([]byte("\x1b[6n"))
	waitFor(t, 2*time.Second, func() bool { return len(cprRe.FindAll(rs.sent(), -1)) == 2 },
		"post-reflow CPR reply (fresh emulator drained)")
}

// The emulator reply pipe is UNBUFFERED, so retireEmuDrain's wake-up write blocks
// until the drain reads it. A drain that has ALREADY exited never will, and the
// writer parks forever — one leaked goroutine per reflow. d.done makes that case
// detectable, so no write is issued for a dead drain.
func TestRetireEmuDrainSkipsDeadDrain(t *testing.T) {
	emu := newTermEmulator(80, 24)
	d := newEmuDrain()
	close(d.done) // the drain already returned (Read errored / earlier retire reaped it)

	time.Sleep(20 * time.Millisecond) // let unrelated goroutines settle
	before := runtime.NumGoroutine()
	retireEmuDrain(emu, d)
	time.Sleep(50 * time.Millisecond) // a would-be wake-up goroutine would be parked by now

	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("retiring a dead drain leaked %d goroutine(s) on the unbuffered reply pipe", after-before)
	}
	if !d.retired.Load() {
		t.Error("retireEmuDrain must still mark the drain retired")
	}
}
