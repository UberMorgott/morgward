package tui

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// TestEncodeKey covers the event→bytes mapping the terminal screen feeds to the
// remote shell: printable text, the named control keys (Enter/Tab/Esc/Backspace/
// Space), Ctrl+<letter> C0 bytes, arrow/nav CSI sequences, function keys, Alt
// prefixing, and the no-input (pure-modifier) case.
func TestEncodeKey(t *testing.T) {
	cases := []struct {
		name string
		key  tea.Key
		want []byte
	}{
		{"printable a", tea.Key{Code: 'a', Text: "a"}, []byte("a")},
		{"printable A (shift)", tea.Key{Code: 'a', ShiftedCode: 'A', Text: "A", Mod: tea.ModShift}, []byte("A")},
		{"unicode", tea.Key{Code: 'é', Text: "é"}, []byte("é")},
		{"enter", tea.Key{Code: tea.KeyEnter}, []byte{'\r'}},
		{"tab", tea.Key{Code: tea.KeyTab}, []byte{'\t'}},
		{"escape", tea.Key{Code: tea.KeyEscape}, []byte{0x1b}},
		{"backspace", tea.Key{Code: tea.KeyBackspace}, []byte{0x7f}},
		{"space", tea.Key{Code: tea.KeySpace}, []byte{' '}},
		{"ctrl+c", tea.Key{Code: 'c', Mod: tea.ModCtrl}, []byte{0x03}},
		{"ctrl+a", tea.Key{Code: 'a', Mod: tea.ModCtrl}, []byte{0x01}},
		{"ctrl+space NUL", tea.Key{Code: ' ', Mod: tea.ModCtrl}, []byte{0x00}},
		{"up", tea.Key{Code: tea.KeyUp}, []byte("\x1b[A")},
		{"down", tea.Key{Code: tea.KeyDown}, []byte("\x1b[B")},
		{"right", tea.Key{Code: tea.KeyRight}, []byte("\x1b[C")},
		{"left", tea.Key{Code: tea.KeyLeft}, []byte("\x1b[D")},
		{"home", tea.Key{Code: tea.KeyHome}, []byte("\x1b[H")},
		{"delete", tea.Key{Code: tea.KeyDelete}, []byte("\x1b[3~")},
		{"f1", tea.Key{Code: tea.KeyF1}, []byte("\x1bOP")},
		{"f5", tea.Key{Code: tea.KeyF5}, []byte("\x1b[15~")},
		{"alt+a", tea.Key{Code: 'a', Text: "a", Mod: tea.ModAlt}, []byte{0x1b, 'a'}},
		{"alt+enter", tea.Key{Code: tea.KeyEnter, Mod: tea.ModAlt}, []byte{0x1b, '\r'}},
		{"pure modifier (no input)", tea.Key{Code: tea.KeyLeftCtrl}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := encodeKey(c.key)
			if !bytes.Equal(got, c.want) {
				t.Fatalf("encodeKey(%+v) = %v, want %v", c.key, got, c.want)
			}
		})
	}
}

// fakeShell is a shellFunc that records the ShellIO it was handed, echoes every
// input byte into the Out sink (the emulator), records resize forwards, and blocks
// until either ctx is canceled OR In reaches EOF — exactly the live Shell contract.
// It lets the session tests exercise drain + resize + teardown with no SSH server.
type fakeShell struct {
	mu       sync.Mutex
	resizes  []sshx.WinSize
	started  chan struct{} // closed once the goroutine is running
	returned chan struct{} // closed when the fake returns (Shell goroutine reaped)
}

func newFakeShell() *fakeShell {
	return &fakeShell{started: make(chan struct{}), returned: make(chan struct{})}
}

func (f *fakeShell) run(ctx context.Context, sio sshx.ShellIO, resize <-chan sshx.WinSize) error {
	close(f.started)
	defer close(f.returned)

	// Echo input → output (emulator) in the background, mirroring a remote pty with
	// ECHO on. Stops when In hits EOF (the session closed the pipe write end).
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		_, _ = io.Copy(sio.Out, sio.In)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-echoDone:
			// In reached EOF (pipe closed by close()) → normal end.
			return nil
		case ws, ok := <-resize:
			if !ok {
				return nil
			}
			f.mu.Lock()
			f.resizes = append(f.resizes, ws)
			f.mu.Unlock()
		}
	}
}

func (f *fakeShell) resizeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.resizes)
}

// lastResize returns the most recently forwarded window size (ok=false if none yet).
func (f *fakeShell) lastResize() (sshx.WinSize, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.resizes) == 0 {
		return sshx.WinSize{}, false
	}
	return f.resizes[len(f.resizes)-1], true
}

// waitFor polls cond until true or the deadline, failing the test otherwise. Mirrors
// the polling idiom in sshx/shell_test.go.
func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

// TestTermSessionWriteDrains proves the always-drain path: bytes written to the
// session (→ pipe → ShellIO.In) come back through ShellIO.Out=emulator (the fake
// echoes), so the rendered screen reflects the input. Verifies the emulator is wired
// as the continuous output sink.
func TestTermSessionWriteDrains(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 80, 24)
	defer s.close()
	<-fs.started

	s.write([]byte("hello"))
	// The echoed "hello" must land on the emulator's first row.
	waitFor(t, time.Second, func() bool {
		return bytes.Contains([]byte(s.view()), []byte("hello"))
	}, "emulator to render echoed input")
}

// TestTermSessionResize proves resize() both resizes the emulator AND forwards the
// new geometry to the (fake) shell's resize channel.
func TestTermSessionResize(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 80, 24)
	defer s.close()
	<-fs.started

	s.resize(120, 40)
	if w := s.emu.Width(); w != 120 {
		t.Errorf("emulator width = %d, want 120", w)
	}
	if h := s.emu.Height(); h != 40 {
		t.Errorf("emulator height = %d, want 40", h)
	}
	waitFor(t, time.Second, func() bool { return fs.resizeCount() >= 1 }, "resize forwarded to shell")

	// Coalesce-to-latest: a burst of resizes must leave the remote pty at the NEWEST
	// size, never a stale one (the prior bug kept the oldest queued size and dropped the
	// newest, so a drag-resize stuck the pty at an old width).
	s.resize(100, 30)
	s.resize(140, 50)
	s.resize(132, 44) // the final, authoritative size
	if w, h := s.emu.Width(), s.emu.Height(); w != 132 || h != 44 {
		t.Errorf("emulator = %dx%d, want 132x44", w, h)
	}
	waitFor(t, time.Second, func() bool {
		last, ok := fs.lastResize()
		return ok && last.Cols == 132 && last.Rows == 44
	}, "newest size forwarded to shell (coalesced to latest)")
}

// TestTermSessionTeardown proves close() reaps the Shell goroutine (no leak): closing
// the pipe write end EOFs ShellIO.In, the fake returns, and the session reports
// finished. Also asserts close() is idempotent.
func TestTermSessionTeardown(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 80, 24)
	<-fs.started

	if done, _ := s.finished(); done {
		t.Fatal("session reported finished before close")
	}

	s.close()

	// The fake's goroutine must return (reaped) after close().
	select {
	case <-fs.returned:
	case <-time.After(time.Second):
		t.Fatal("Shell goroutine not reaped after close() — leak")
	}
	// And the session records the finished state.
	waitFor(t, time.Second, func() bool { d, _ := s.finished(); return d }, "session to mark finished")

	// Idempotent: a second close must not panic (no double pipe-close / double cancel).
	s.close()

	// Writing after close is a harmless no-op (the pipe is closed).
	s.write([]byte("late"))
}

// TestTermSessionTitleCallback proves the OSC title callback updates the session
// title, surfaced via windowTitle(). The fake echoes the OSC sequence into the
// emulator, which parses it and fires Callbacks.Title.
func TestTermSessionTitleCallback(t *testing.T) {
	fs := newFakeShell()
	s := newTermSessionWith(fs.run, 80, 24)
	defer s.close()
	<-fs.started

	// OSC 2 ; <title> BEL sets the window title.
	s.write([]byte("\x1b]2;mybox\x07"))
	waitFor(t, time.Second, func() bool { return s.windowTitle() == "mybox" }, "title callback to fire")
}
