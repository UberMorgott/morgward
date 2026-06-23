package tui

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// termOut is the STABLE output sink wired to ShellIO.Out for the session's whole life
// (the Shell goroutine captures this writer once and holds it forever — we cannot swap
// the emulator by reassigning a field it already closed over). Every remote PTY byte
// flows through Write, which under mu (a) mirrors the bytes into the replay ring and
// (b) writes them to the CURRENT emulator. On reflow we build a fresh emulator, replay
// the ring into it, then swap `emu` under this same mu — so the drain never writes to a
// half-built emulator and no bytes are lost or reordered across the swap.
type termOut struct {
	mu   sync.Mutex
	emu  *vt.SafeEmulator // the live emulator; swapped atomically on reflow
	ring *termRing        // raw-stream mirror for replay-on-resize
}

// Write mirrors p into the ring and forwards it to the current emulator, both under
// mu. Holding mu across the emulator Write is what serializes the drain against a
// reflow swap: while reflowNow holds mu to replay+swap, a concurrent drain Write
// blocks here and resumes against the NEW emulator, so the stream stays ordered and
// gap-free. (SafeEmulator.Write is itself locked; mu here guards the ring + the
// emu-pointer, not the emulator's internal state.)
func (o *termOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ring.append(p)
	return o.emu.Write(p)
}

// current returns the live emulator pointer under mu — every session accessor grabs it
// through here so a read can't observe a torn swap.
func (o *termOut) current() *vt.SafeEmulator {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.emu
}

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
	// emu always points at the CURRENT live emulator. Existing tests/render paths read
	// it directly (emu.Width()/Height()/Scrollback()/Render()); reflowNow updates it
	// atomically under out.mu when it swaps in a rebuilt emulator. Read it via emuNow()
	// (which grabs out.mu) anywhere a concurrent swap is possible; the field stays for
	// the byte-identical accessor surface tests rely on.
	emu *vt.SafeEmulator

	// out is the stable ShellIO.Out sink: it owns the replay ring and the current-
	// emulator pointer, mirroring every drained byte into the ring and the live
	// emulator. Reflow swaps the emulator inside out under out.mu; emu (above) is kept
	// in lockstep. See termOut.
	out *termOut

	// reflowMu guards the debounce timer + the geometry it will replay at, plus the
	// alt-screen "resize happened while alt" bookkeeping (altReflowWait/W/H), the
	// alt-off deferral timer, and the `replaying` re-entrancy flag. Distinct from out.mu
	// (which guards the ring+emulator swap) and from mu (session lifecycle), so arming a
	// debounce never contends with the drain.
	reflowMu      sync.Mutex
	reflowTimer   *time.Timer
	reflowW       int // latest requested geometry the debounce will replay at
	reflowH       int
	altReflowWait bool // a resize landed while on alt-screen → replay once on leaving alt
	altReflowW    int
	altReflowH    int
	// altTimer fires the OWED alt-off replay on its OWN goroutine. The AltScreen(false)
	// callback runs synchronously inside emu.Write while the drain holds out.mu, so it
	// must NEVER call reflowNow inline (reflowNow re-locks out.mu → self-deadlock on the
	// drain goroutine). Instead it arms this time.AfterFunc(0,…) timer, which runs off the
	// drain and is free to take out.mu. Stopped by close() so it can't fire post-teardown.
	altTimer *time.Timer
	// replaying is true ONLY while reflowNow is replaying the ring into a fresh emulator.
	// The replay re-emits alt-off/alt-on, firing the fresh emulator's AltScreen callback;
	// without this guard a replayed alt-off would arm ANOTHER reflow → unbounded recursion.
	// The callback skips all reflow-arming bookkeeping while this is set. Guarded by reflowMu.
	replaying bool
	// reflowRunMu serializes reflowNow end-to-end so only ONE rebuild+replay runs at a
	// time. Two reflowNow calls can race (the debounce reflowTimer on one goroutine, the
	// alt-off altTimer on another); without this, G2 could observe `replaying` already
	// cleared by G1 and let a replayed alt-off arm a spurious extra reflow. It is the
	// OUTERMOST lock in reflowNow (taken before reflowMu and out.mu); nothing else takes
	// it, so there is no lock-ordering inversion. NOT taken by the AltScreen callback —
	// that must stay non-blocking on the drain goroutine.
	reflowRunMu sync.Mutex

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

	// cursorVisible tracks DECTCEM (?25): true = the remote wants a visible cursor.
	// Updated by the CursorVisibility callback (drain goroutine, s.mu only — like
	// title/bell, NEVER touches out.mu/reflowMu so it can't deadlock the drain), read by
	// the model under mu to gate the cursor overlay. Initialized true: ?25 is on by
	// default, and an app that wants no cursor (vim insert-elsewhere, password prompts)
	// sends ?25l to turn it off.
	cursorVisible bool
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
	emu := newTermEmulator(cols, rows)

	pr, pw := io.Pipe()
	resizeCh := make(chan sshx.WinSize, 1)
	ctx, cancel := context.WithCancel(context.Background())

	s := &termSession{
		emu:           emu,
		out:           &termOut{emu: emu, ring: newTermRing(termRingCap)},
		pw:            pw,
		resizeCh:      resizeCh,
		cancel:        cancel,
		cursorVisible: true, // DECTCEM ?25 defaults ON
	}

	// Wire title/bell/alt callbacks BEFORE the drain goroutine starts writing, so there
	// is no race on first output. Every emulator (initial AND each reflow rebuild) gets
	// the same callbacks via setCallbacks, so title/bell/alt keep firing after a swap.
	s.setCallbacks(emu)

	// Shell runs in its own goroutine. ShellIO.Out=s.out is the STABLE sink the Shell
	// goroutine captures once: it mirrors every drained byte into the replay ring and
	// the current emulator (always-drain), independent of the focused screen, and lets
	// reflow swap the emulator underneath without the goroutine ever seeing it. We
	// capture the return error so the model can report a finished/failed session.
	go func() {
		err := shell(ctx, sshx.ShellIO{
			In:   pr,
			Out:  s.out,
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

// reflowDebounce is how long resize() waits after the LAST size change before
// rebuilding+replaying the emulator. A rapid drag-resize fires many intermediate
// sizes; debouncing collapses them to one replay at the final geometry, so we don't
// re-parse the whole ring on every pixel of the drag. The remote pty still gets each
// newest size promptly (resize() forwards it immediately, coalesced-to-latest); only
// the local rewrap is deferred to settle.
const reflowDebounce = 120 * time.Millisecond

// newTermEmulator builds a fresh emulator at cols×rows with the session's scrollback
// budget. Centralized so the initial build and every reflow rebuild are identical.
func newTermEmulator(cols, rows int) *vt.SafeEmulator {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	emu := vt.NewSafeEmulator(cols, rows)
	emu.SetScrollbackSize(termScrollback)
	return emu
}

// setCallbacks wires the title/bell/alt callbacks onto emu. Called for the initial
// emulator and again for each reflow rebuild (a fresh emulator has no callbacks, so a
// post-swap OSC title / BEL / alt-screen toggle would be lost otherwise). The callbacks
// fire synchronously inside the emulator's Write under its own lock; they only take
// s.mu / s.reflowMu (never call back into the emulator), so there is no deadlock.
//
// The AltScreen callback additionally drives the alt-screen reflow deferral: while alt
// is ON we SKIP replay (the remote app redraws itself on the SIGWINCH we already
// forward), but if a resize landed during alt we owe ONE replay when the app leaves alt
// so the normal-screen buffer underneath is rewrapped to the new width.
func (s *termSession) setCallbacks(emu *vt.SafeEmulator) {
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
		CursorVisibility: func(visible bool) {
			// DECTCEM ?25 toggle. Fires SYNCHRONOUSLY on the drain goroutine under out.mu
			// (same deadlock class as AltScreen): keep it TRIVIAL — take ONLY s.mu, never
			// out.mu/reflowMu, never call reflowNow. Just record the bit.
			s.mu.Lock()
			s.cursorVisible = visible
			s.mu.Unlock()
		},
		AltScreen: func(on bool) {
			s.mu.Lock()
			s.alt = on
			s.mu.Unlock()
			if on {
				return
			}
			// Left alt-screen. This callback fires SYNCHRONOUSLY inside emu.Write — on the
			// drain goroutine it runs while out.mu is held, and during a reflow replay it
			// runs while reflowNow holds out.mu. So we must NOT call reflowNow inline (it
			// re-locks out.mu → self-deadlock). Two guards:
			//   1. While `replaying`, this alt-off was re-emitted BY the replay itself —
			//      ignore it entirely, else a replayed alt-off would arm another replay
			//      (unbounded recursion).
			//   2. Otherwise, if a resize was owed, DEFER the replay to a fresh goroutine
			//      via time.AfterFunc(0,…) so it acquires out.mu free of the drain.
			s.reflowMu.Lock()
			if s.replaying {
				s.reflowMu.Unlock()
				return
			}
			owe := s.altReflowWait
			w, h := s.altReflowW, s.altReflowH
			s.altReflowWait = false
			if owe {
				if s.altTimer != nil {
					s.altTimer.Stop()
				}
				s.altTimer = time.AfterFunc(0, func() {
					s.mu.Lock()
					done := s.closed
					s.mu.Unlock()
					if done {
						return
					}
					s.reflowNow(w, h)
				})
			}
			s.reflowMu.Unlock()
		},
	})
}

// emuNow returns the current live emulator under out.mu, so a render/accessor can't
// observe a half-completed reflow swap. Cheap (a pointer read under a short lock).
func (s *termSession) emuNow() *vt.SafeEmulator {
	if s.out == nil {
		return s.emu
	}
	return s.out.current()
}

// reflowNow synchronously rebuilds the emulator at cols×rows and REPLAYS the raw stream
// ring through it, then swaps it in atomically. This is the heart of reflow-by-replay:
// vt.SafeEmulator.Resize merely truncates on shrink (the wrapped tail is lost and never
// restored on grow-back), whereas replaying the byte stream into a fresh emulator of the
// new width re-runs the terminal's own wrapping logic → faithful rewrap, alt-screen
// state and all (the replay is byte-identical, so it reconstructs whatever the stream
// produced).
//
// Concurrency: reflowRunMu serializes the WHOLE function so only one rebuild+replay runs
// at a time (the debounce timer and the alt-off timer can both call this on different
// goroutines). Within that, out.mu is taken for the rebuild+replay+swap: holding it
// across the replay parks the drain goroutine (also under out.mu in termOut.Write) until
// the swap completes — so the drain can never write to the half-built emulator, and no
// drained byte is lost or reordered relative to the replayed stream. After the swap,
// s.emu and out.emu both point at the new emulator. Lock order is reflowRunMu → reflowMu
// (replaying flag) → out.mu; nothing else takes reflowRunMu, so no inversion.
//
// No-op-safe: an empty ring just yields a blank emulator at the new size; a call after
// close() (out==nil-guarded path / closed flag) is inert.
func (s *termSession) reflowNow(cols, rows int) {
	// Outermost lock: hold it for the entire body so overlapping reflowNow calls run
	// strictly one-after-another. This keeps the `replaying` set/clear single-owned per
	// critical section (a second call can't start until this one has cleared it).
	s.reflowRunMu.Lock()
	defer s.reflowRunMu.Unlock()

	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed || s.out == nil {
		return
	}

	fresh := newTermEmulator(cols, rows)
	s.setCallbacks(fresh)

	// Mark `replaying` so the alt-off/alt-on the replay re-emits into `fresh` (firing its
	// AltScreen callback synchronously inside fresh.Write below) does NOT arm another
	// reflow — that would recurse without bound. Set/cleared under reflowMu, but NOT held
	// across fresh.Write: the callback itself takes reflowMu, so holding it would deadlock.
	s.reflowMu.Lock()
	s.replaying = true
	s.reflowMu.Unlock()

	s.out.mu.Lock()
	stream := s.out.ring.bytes()
	if len(stream) > 0 {
		// Replay the recorded stream into the fresh emulator. Errors from Write are not
		// possible for the in-memory emulator; ignore for parity with the drain path.
		_, _ = fresh.Write(stream)
	}
	s.out.emu = fresh
	s.emu = fresh
	s.out.mu.Unlock()

	s.reflowMu.Lock()
	s.replaying = false
	s.reflowMu.Unlock()
}

// scheduleReflow arms (or re-arms) the debounce: it stashes the latest geometry and
// (re)sets a one-shot timer that calls reflowNow after reflowDebounce of quiet. A burst
// of resizes collapses to a single replay at the final size. While on the alt-screen we
// DEFER instead: record that a replay is owed and let the AltScreen-off callback fire it
// once, since the alt app redraws itself on SIGWINCH and replaying mid-alt would just
// cost flicker. The timer is stopped by close() (under reflowMu) so it can't fire after
// teardown.
func (s *termSession) scheduleReflow(cols, rows int) {
	// On alt-screen: skip the live replay, but remember to do one when alt turns off.
	s.mu.Lock()
	alt := s.alt
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	if alt {
		s.reflowMu.Lock()
		s.altReflowWait = true
		s.altReflowW, s.altReflowH = cols, rows
		// Cancel any pending normal-screen timer; the alt path owns the next replay.
		if s.reflowTimer != nil {
			s.reflowTimer.Stop()
		}
		s.reflowMu.Unlock()
		return
	}

	s.reflowMu.Lock()
	defer s.reflowMu.Unlock()
	s.reflowW, s.reflowH = cols, rows
	if s.reflowTimer != nil {
		s.reflowTimer.Stop()
	}
	s.reflowTimer = time.AfterFunc(reflowDebounce, func() {
		// Re-check closed and re-read the latest geometry under the lock at fire time.
		s.mu.Lock()
		done := s.closed
		s.mu.Unlock()
		if done {
			return
		}
		s.reflowMu.Lock()
		w, h := s.reflowW, s.reflowH
		s.reflowMu.Unlock()
		s.reflowNow(w, h)
	})
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
	// Immediate truncating resize for LIVENESS: the emulator snaps to the new geometry
	// instantly (vt truncates on shrink) so the frame is the right shape right away,
	// then the debounced replay below rewraps it faithfully once the drag settles. We
	// resize via emuNow() under out.mu so this can't race a concurrent reflow swap.
	s.emuNow().Resize(cols, rows)
	// Schedule the debounced rebuild+replay that turns the truncated resize into a true
	// rewrap (restoring content lost to a shrink when the window grows back).
	s.scheduleReflow(cols, rows)
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
	return s.emuNow().Render()
}

// altScreen reports whether the remote app is currently on the alternate screen
// (vim/top/less). Read under mu (the callback writes it on the drain goroutine).
func (s *termSession) altScreen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alt
}

// cursorShown reports whether the remote wants a visible cursor (DECTCEM ?25 on). Read
// under mu (the CursorVisibility callback writes it on the drain goroutine). The model
// gates the cursor overlay on this so an app that hides the cursor (vim, password
// prompts) suppresses our block.
func (s *termSession) cursorShown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursorVisible
}

// screenLines is the live screen split into physical rows (ANSI-styled), trailing
// blank rows trimmed so the join is exactly the visible content. The SafeEmulator
// serializes Render against the drain goroutine.
func (s *termSession) screenLines() []string {
	return strings.Split(strings.TrimRight(s.emuNow().Render(), "\n"), "\n")
}

// scrollbackLines renders the off-screen scrollback buffer (oldest→newest) as
// ANSI-styled rows. Empty when nothing has scrolled off yet. Each uv.Line renders
// itself; SafeEmulator's Scrollback accessor is serialized against the drain.
func (s *termSession) scrollbackLines() []string {
	sb := s.emuNow().Scrollback()
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

// termSnapshot is a CONSISTENT, single-locked view of everything the terminal view +
// cursor overlay need for one frame: the live screen rows, the scrollback rows (and its
// length, so the overlay's body-row math can't drift from the body it splices), the
// alt-screen flag, cursor visibility, the cursor cell position, and the grapheme +
// display width under the cursor. Taken atomically so the overlay maps the cursor
// against EXACTLY the body it overlays (no TOCTOU between separate locked reads racing
// the drain).
type termSnapshot struct {
	screen        []string
	scrollback    []string
	scrollbackLen int
	alt           bool
	cursorVisible bool
	cursorX       int
	cursorY       int
	cursorCell    string // grapheme under the cursor ("" = blank cell)
	cursorWidth   int    // display width of that grapheme (1 or 2; 0 for a blank cell)
}

// cursorSnapshot returns a consistent one-frame view (see termSnapshot). It holds out.mu
// for the WHOLE read — out.mu serializes the drain (the only writer), so every field
// reflects the same emulator state with no interleaved write. s.mu is taken (nested
// under out.mu) only to read the callback-written alt/cursorVisible bits; the lock order
// out.mu→s.mu is safe because no path takes s.mu then out.mu (reflowNow releases s.mu
// before acquiring out.mu). Does NO heavy work beyond the reads and never calls reflow.
func (s *termSession) cursorSnapshot() termSnapshot {
	s.out.mu.Lock()
	defer s.out.mu.Unlock()
	emu := s.out.emu

	var snap termSnapshot
	snap.screen = strings.Split(strings.TrimRight(emu.Render(), "\n"), "\n")
	if sb := emu.Scrollback(); sb != nil {
		lines := sb.Lines()
		snap.scrollback = make([]string, 0, len(lines))
		for _, ln := range lines {
			snap.scrollback = append(snap.scrollback, ln.Render())
		}
	}
	snap.scrollbackLen = len(snap.scrollback)

	p := emu.CursorPosition()
	snap.cursorX, snap.cursorY = p.X, p.Y
	if c := emu.CellAt(p.X, p.Y); c != nil {
		snap.cursorCell = c.Content
		snap.cursorWidth = c.Width
	}

	s.mu.Lock()
	snap.alt = s.alt
	snap.cursorVisible = s.cursorVisible
	s.mu.Unlock()
	return snap
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

	// Stop both reflow timers so a pending replay can't fire after teardown — the debounce
	// timer (normal-screen resize) AND the alt-off deferral timer. Each timer's callback
	// ALSO re-checks s.closed (set above) before doing work, so even a fire that already
	// started is inert — Stop() just avoids the wasted wakeup. No goroutine outlives
	// close(): both time.AfterFunc goroutines are one-shot and gated on !closed.
	s.reflowMu.Lock()
	if s.reflowTimer != nil {
		s.reflowTimer.Stop()
		s.reflowTimer = nil
	}
	if s.altTimer != nil {
		s.altTimer.Stop()
		s.altTimer = nil
	}
	s.altReflowWait = false
	s.reflowMu.Unlock()

	s.cancel()
	// Close the write end so io.Copy(stdin, ShellIO.In) in Shell hits EOF and that
	// goroutine returns — no leak across open/close cycles.
	_ = s.pw.Close()
}
