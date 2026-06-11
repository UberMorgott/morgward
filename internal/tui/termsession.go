package tui

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/x/vt"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// termSession is the NON-COPYABLE core of the Terminal screen (phase 2a). It owns
// every piece of mutable, non-copyable state — the vt emulator, the input pipe, the
// resize channel, the cancel func, and the background goroutines — so the bubbletea
// model can hold it behind a POINTER and stay fully value-copyable (the model is
// copied by value every Update; see CLAUDE.md "model copied by value" gotcha). NEVER
// inline a termSession as a value field.
//
// Data flow:
//   - OUTPUT (always-drain): sshx.Client.Shell copies remote PTY bytes straight into
//     emu via ShellIO.Out=emu in its OWN goroutine, continuously, regardless of which
//     screen is focused. This is load-bearing — if the drain stalls, SSH flow-control
//     back-pressures and the remote process freezes. Rendering reads emu.Render()
//     on-demand and is fully decoupled from the drain.
//   - INPUT: the model encodes key/paste events to terminal bytes (see termkeys.go)
//     and calls write(), which feeds the io.Pipe whose read end is ShellIO.In →
//     remote stdin.
//   - RESIZE: resize() updates the emulator AND non-blocking-sends a WinSize so the
//     remote pty matches.
//
// Teardown (close): cancels ctx (Shell returns) AND closes the pipe write end (Shell's
// input-copy goroutine reaches EOF per the phase-1 ShellIO.In contract), so no
// goroutine leaks across open/close cycles — critical for the long-lived TUI.
type termSession struct {
	emu *vt.SafeEmulator

	// pw is the write end of the input pipe; the read end (pr) is handed to Shell as
	// ShellIO.In. The model writes key bytes into pw; closing pw EOFs Shell's input
	// goroutine.
	pw *io.PipeWriter

	resizeCh chan sshx.WinSize
	cancel   context.CancelFunc

	// mu guards the post-startup mutable state: done/exitErr (written once by the
	// Shell goroutine, read by the model) and the closed flag (so close() is
	// idempotent). The emulator has its OWN locking (SafeEmulator); the pipe and
	// channel are concurrency-safe on their own.
	mu      sync.Mutex
	closed  bool
	done    bool
	exitErr error

	// title/bell are updated by the emulator callbacks (OSC title, BEL). Read by the
	// model under mu for the window-title and a future bell indicator. Callbacks fire
	// on the drain goroutine, so they take mu.
	title string
	bell  bool

	// alt tracks whether the remote app has switched to the alternate screen (vim/top/
	// less). Updated by the AltScreen callback on the drain goroutine, read by the model
	// under mu. Alt-screen apps own the whole screen, so local scrollback scrolling is
	// disabled while alt is true (the scrollback is frozen/irrelevant).
	alt bool
}

// termScrollback is the number of scrollback lines the emulator retains. A modest
// buffer so paging through `top`/long output works without unbounded growth.
const termScrollback = 5000

// shellFunc is the slice of *sshx.Client the session drives: exactly Client.Shell's
// signature. Extracting it as a seam lets newTermSessionWith run the session wiring
// (always-drain into the emulator, input pipe, resize, teardown) against a fake in
// unit tests — no live SSH server — while production passes client.Shell.
type shellFunc func(ctx context.Context, sio sshx.ShellIO, resize <-chan sshx.WinSize) error

// newTermSession dials nothing — it takes an already-connected *sshx.Client and
// starts an interactive PTY shell on it, wiring the emulator as the always-drain
// output sink and an io.Pipe as the input source. cols/rows size the initial pty +
// emulator. The Shell call runs in its own goroutine; its return error is captured
// into the session so the model can show "session ended".
//
// NOTE the client is owned by the session from here on: close() does NOT close the
// client (the caller that dialed it owns its lifecycle), but the Shell goroutine
// holds it for the session's duration.
func newTermSession(client *sshx.Client, cols, rows int) *termSession {
	return newTermSessionWith(client.Shell, cols, rows)
}

// newTermSessionWith is newTermSession's testable core: identical wiring, but driving
// an arbitrary shellFunc (production passes client.Shell). The fake in the tests can
// echo input to the emulator and block until close() cancels its ctx / EOFs its In,
// exercising the teardown contract without a real server.
func newTermSessionWith(shell shellFunc, cols, rows int) *termSession {
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	emu := vt.NewSafeEmulator(cols, rows)
	emu.SetScrollbackSize(termScrollback)

	pr, pw := io.Pipe()
	resizeCh := make(chan sshx.WinSize, 1)
	ctx, cancel := context.WithCancel(context.Background())

	s := &termSession{
		emu:      emu,
		pw:       pw,
		resizeCh: resizeCh,
		cancel:   cancel,
	}

	// Wire title/bell callbacks BEFORE the drain goroutine starts writing, so there is
	// no race on first output. SetCallbacks is on the embedded *Emulator; SafeEmulator
	// serializes Write, and the callbacks fire synchronously inside Write under that
	// lock, so taking s.mu here is safe (no Write↔callback deadlock: the callback path
	// never calls back into the emulator).
	emu.SetCallbacks(vt.Callbacks{
		Title: func(t string) {
			s.mu.Lock()
			s.title = t
			s.mu.Unlock()
		},
		Bell: func() {
			s.mu.Lock()
			s.bell = true
			s.mu.Unlock()
		},
		AltScreen: func(on bool) {
			s.mu.Lock()
			s.alt = on
			s.mu.Unlock()
		},
	})

	// Shell runs in its own goroutine. ShellIO.Out=emu means Shell's OWN output
	// goroutine drains the remote PTY into the emulator continuously (always-drain),
	// independent of the focused screen. We capture the return error so the model can
	// report a finished/failed session.
	go func() {
		err := shell(ctx, sshx.ShellIO{
			In:   pr,
			Out:  emu,
			Term: "xterm-256color",
			Cols: cols,
			Rows: rows,
		}, resizeCh)
		s.mu.Lock()
		s.done = true
		s.exitErr = err
		s.mu.Unlock()
		// EOF the read end too so a writer parked in write() unblocks rather than
		// hanging on a dead pipe after the shell exited on its own.
		_ = pr.CloseWithError(io.EOF)
	}()

	return s
}

// write feeds raw input bytes to the remote shell's stdin (via the pipe → ShellIO.In).
// A nil/empty slice is a no-op. After close()/session-end the pipe write returns an
// error which is swallowed — input to a dead shell is simply dropped.
func (s *termSession) write(b []byte) {
	if len(b) == 0 {
		return
	}
	_, _ = s.pw.Write(b)
}

// resize updates the emulator geometry AND forwards the new size to the remote pty
// (the resize channel is buffered 1 and Shell drains it). The send coalesces to the
// LATEST geometry — any stale pending size is drained first — so a rapid drag-resize
// can never leave the remote pty stuck at an old width. cols/rows below 1 are clamped.
func (s *termSession) resize(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	s.emu.Resize(cols, rows)
	// Coalesce to the LATEST geometry: drain any stale pending size first, THEN enqueue
	// this one. The old code kept the OLDEST queued size and dropped the newest, so a
	// rapid drag-resize could leave the remote pty stuck at a stale width — the remote
	// shell then renders too wide/narrow for the window and never restores on grow-back.
	// Always deliver the newest size so the remote pty matches the actual window.
	select {
	case <-s.resizeCh:
	default:
	}
	select {
	case s.resizeCh <- sshx.WinSize{Cols: cols, Rows: rows}:
	default:
	}
}

// view returns the ANSI-styled full screen for rendering into the Terminal screen's
// content area. The SafeEmulator serializes this against the drain goroutine's writes.
func (s *termSession) view() string {
	return s.emu.Render()
}

// altScreen reports whether the remote app is currently on the alternate screen
// (vim/top/less). Read under mu (the callback writes it on the drain goroutine).
func (s *termSession) altScreen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alt
}

// screenLines is the live screen split into physical rows (ANSI-styled), trailing
// blank rows trimmed so the join is exactly the visible content. The SafeEmulator
// serializes Render against the drain goroutine.
func (s *termSession) screenLines() []string {
	return strings.Split(strings.TrimRight(s.emu.Render(), "\n"), "\n")
}

// scrollbackLines renders the off-screen scrollback buffer (oldest→newest) as
// ANSI-styled rows. Empty when nothing has scrolled off yet. Each uv.Line renders
// itself; SafeEmulator's Scrollback accessor is serialized against the drain.
func (s *termSession) scrollbackLines() []string {
	sb := s.emu.Scrollback()
	if sb == nil {
		return nil
	}
	lines := sb.Lines()
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		out = append(out, ln.Render())
	}
	return out
}

// dirty reports whether the emulator has untouched-since-last-render damage, so the
// render ticker can skip a repaint when nothing changed. Touched() returns the set of
// changed lines (and clears the damage), so this is a one-shot check per call —
// callers that want the value to drive a render must act on a true result.
func (s *termSession) dirty() bool {
	return len(s.emu.Touched()) > 0
}

// finished reports whether the Shell goroutine has returned (the remote shell exited
// or the transport died) and, if so, its error (nil = clean exit). Read under mu.
func (s *termSession) finished() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done, s.exitErr
}

// windowTitle returns the latest OSC-set terminal title (empty until the remote sets
// one). Read under mu.
func (s *termSession) windowTitle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// close tears the session down: cancel ctx (Shell returns at its select), then close
// the pipe write end so Shell's input-copy goroutine reaches EOF and is reaped (per
// the phase-1 ShellIO.In contract). Idempotent — safe to call more than once. Does NOT
// close the underlying *sshx.Client (the caller owns it). After close() the model must
// drop its pointer so the emulator is GC'd.
func (s *termSession) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	s.cancel()
	// Close the write end so io.Copy(stdin, ShellIO.In) in Shell hits EOF and that
	// goroutine returns — no leak across open/close cycles.
	_ = s.pw.Close()
}
