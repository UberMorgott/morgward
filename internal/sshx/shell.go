package sshx

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/crypto/ssh"
)

// WinSize is one terminal dimension update (columns x rows). The CLI driver
// feeds these from SIGWINCH (Unix) / a size poll (Windows); the future TUI pane
// feeds them from its own layout — Shell forwards each to the remote pty.
type WinSize struct{ Cols, Rows int }

// ShellIO wires a PTY session to caller-owned streams so the core never touches
// os.Stdin/os.Stdout directly: the CLI passes the real terminal, the TUI passes
// its own pipes. Term/Cols/Rows are the initial pty geometry (defaulted when
// empty/zero by defaultShellIO).
type ShellIO struct {
	In         io.Reader // bytes typed by the user → remote stdin; caller must close In (or let it reach EOF) after Shell returns to reap the input-copy goroutine — matters for the long-lived TUI driver calling Shell repeatedly, not the one-shot CLI
	Out        io.Writer // remote pty output → screen (pty merges stderr in)
	Term       string    // $TERM; default "xterm-256color" when empty
	Cols, Rows int       // initial size; default 80x24 when zero
}

// Default pty geometry / terminal when the caller leaves ShellIO fields zero.
const (
	defaultTerm = "xterm-256color"
	defaultCols = 80
	defaultRows = 24
)

// defaultShellIO returns sio with empty Term / zero Cols/Rows filled in with the
// sane defaults. Pure (no I/O), so it is unit-testable on its own.
func defaultShellIO(sio ShellIO) ShellIO {
	if sio.Term == "" {
		sio.Term = defaultTerm
	}
	if sio.Cols <= 0 {
		sio.Cols = defaultCols
	}
	if sio.Rows <= 0 {
		sio.Rows = defaultRows
	}
	return sio
}

// ptySession is the slice of *ssh.Session that Shell drives. Extracting it as an
// interface lets the wiring (exit classification, resize forwarding) be unit-
// tested with a fake — no live server. *ssh.Session satisfies it natively; the
// Stdout sink is set via SetStdout rather than the exported field so the fake can
// observe it too.
type ptySession interface {
	RequestPty(term string, h, w int, modes ssh.TerminalModes) error
	StdinPipe() (io.WriteCloser, error)
	SetStdout(io.Writer)
	Shell() error
	Wait() error
	WindowChange(h, w int) error
	Close() error
}

// sshPtySession adapts *ssh.Session to ptySession. The only impedance is Stdout:
// *ssh.Session exposes it as a settable field, not a method, so SetStdout assigns
// it.
type sshPtySession struct{ *ssh.Session }

func (s sshPtySession) SetStdout(w io.Writer) { s.Stdout = w }

// ptyModes are the terminal modes requested for the remote pty. ECHO is on so the
// remote (not us) echoes typed input — the CLI puts the LOCAL terminal in raw
// mode so there is no double echo. The i/ospeed values are conventional and only
// inform programs that query the baud rate.
var ptyModes = ssh.TerminalModes{
	ssh.ECHO:          1,
	ssh.TTY_OP_ISPEED: 14400,
	ssh.TTY_OP_OSPEED: 14400,
}

// Shell opens an interactive login shell on a PTY and blocks until the remote
// shell exits, the connection drops, or ctx is canceled. resize may be nil; when
// non-nil, each value is forwarded to the remote pty (Session.WindowChange) until
// ctx is done or the channel closes.
//
// A clean remote exit — including a non-zero status (the user typed `exit 1`) —
// is success: a remote shell ending is the normal way out, so Shell returns nil.
// Only transport/setup failures (dial gone, pty request rejected, …) return a
// non-nil error. On ctx cancel the session is closed to unblock Wait and ctx.Err
// is returned.
func (c *Client) Shell(ctx context.Context, sio ShellIO, resize <-chan WinSize) error {
	// Snapshot the live client under the lock — same race-safe pattern as Run:
	// connect/Close (and the keepalive goroutine's Close) mutate c.cli under c.mu.
	c.mu.Lock()
	cli := c.cli
	c.mu.Unlock()
	if cli == nil {
		return fmt.Errorf("shell: not connected")
	}
	raw, err := cli.NewSession()
	if err != nil {
		return fmt.Errorf("shell: new session: %w", err)
	}
	return runShellSession(ctx, sshPtySession{raw}, sio, resize)
}

// runShellSession is the UI-agnostic, server-agnostic wiring of an interactive
// pty shell over any ptySession. Split out from Shell so a fake session can drive
// the exit-classification + resize-forwarding paths without a live SSH server.
func runShellSession(ctx context.Context, sess ptySession, sio ShellIO, resize <-chan WinSize) error {
	defer sess.Close()
	sio = defaultShellIO(sio)

	if err := sess.RequestPty(sio.Term, sio.Rows, sio.Cols, ptyModes); err != nil {
		return fmt.Errorf("shell: request pty: %w", err)
	}

	// Remote pty merges stderr into stdout, so a single Out sink suffices.
	sess.SetStdout(sio.Out)
	stdin, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("shell: stdin pipe: %w", err)
	}
	// Copy local input → remote stdin in the background. It unblocks when sio.In
	// hits EOF (piped input drained) or the session closes; we never wait on it —
	// the remote shell exit / ctx cancel drives termination.
	go func() {
		_, _ = io.Copy(stdin, sio.In)
		_ = stdin.Close()
	}()

	if err := sess.Shell(); err != nil {
		return fmt.Errorf("shell: start: %w", err)
	}

	// Forward terminal resizes to the remote pty until the shell ends. done stops
	// this goroutine on the normal exit path (so it can't leak past Wait).
	done := make(chan struct{})
	defer close(done)
	if resize != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-done:
					return
				case ws, ok := <-resize:
					if !ok {
						return
					}
					_ = sess.WindowChange(ws.Rows, ws.Cols)
				}
			}
		}()
	}

	// Wait for the remote shell in the background so ctx cancel can race it: on
	// cancel we close the session (unblocking Wait) and return ctx.Err.
	waitErr := make(chan error, 1)
	go func() { waitErr <- sess.Wait() }()

	select {
	case <-ctx.Done():
		_ = sess.Close() // unblock Wait
		<-waitErr        // reap the goroutine
		return ctx.Err()
	case err := <-waitErr:
		return classifyShellExit(err)
	}
}

// classifyShellExit maps a Session.Wait result to Shell's contract: a clean exit
// OR a non-zero remote exit (*ssh.ExitError — the user ran `exit 1`) is success
// (nil); only a transport/setup error is surfaced. A free function so the
// classification is unit-testable in isolation.
func classifyShellExit(err error) error {
	switch err.(type) {
	case nil:
		return nil
	case *ssh.ExitError:
		// Remote shell exited with a non-zero status: still a normal end of an
		// interactive session, not a transport failure.
		return nil
	default:
		return err
	}
}
