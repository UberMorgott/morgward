package sshx

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestDefaultShellIO covers the pure term/size defaulting: empty Term and
// zero/negative Cols/Rows fall back to the sane defaults; explicit values pass
// through untouched.
func TestDefaultShellIO(t *testing.T) {
	got := defaultShellIO(ShellIO{})
	if got.Term != defaultTerm || got.Cols != defaultCols || got.Rows != defaultRows {
		t.Fatalf("zero ShellIO defaulted to {Term:%q Cols:%d Rows:%d}, want {%q %d %d}",
			got.Term, got.Cols, got.Rows, defaultTerm, defaultCols, defaultRows)
	}

	explicit := ShellIO{Term: "vt100", Cols: 132, Rows: 50}
	if got := defaultShellIO(explicit); got != explicit {
		t.Fatalf("explicit ShellIO mutated: got %+v, want %+v", got, explicit)
	}

	// Negative dimensions are treated as unset.
	neg := defaultShellIO(ShellIO{Cols: -1, Rows: -5})
	if neg.Cols != defaultCols || neg.Rows != defaultRows {
		t.Fatalf("negative dims defaulted to {Cols:%d Rows:%d}, want {%d %d}",
			neg.Cols, neg.Rows, defaultCols, defaultRows)
	}
}

// TestClassifyShellExit proves the exit contract: nil and a non-zero remote exit
// (*ssh.ExitError, e.g. user typed `exit 1`) are success; a transport error is
// surfaced verbatim.
func TestClassifyShellExit(t *testing.T) {
	if err := classifyShellExit(nil); err != nil {
		t.Errorf("clean exit should be nil, got %v", err)
	}
	// *ssh.ExitError is unexported-constructed in practice; classifyShellExit only
	// type-switches on it, so a zero-value pointer exercises the same branch.
	if err := classifyShellExit(&ssh.ExitError{}); err != nil {
		t.Errorf("non-zero remote exit should be nil (normal shell end), got %v", err)
	}
	transport := errors.New("ssh: connection lost")
	if err := classifyShellExit(transport); !errors.Is(err, transport) {
		t.Errorf("transport error should be surfaced, got %v", err)
	}
}

// fakePtySession is a scriptable ptySession for driving runShellSession without a
// live server. It records the requested geometry and resize forwards, and lets a
// test control when Wait returns and with what error.
type fakePtySession struct {
	mu sync.Mutex

	reqTerm       string
	reqRows, reqW int
	resizes       []WinSize
	stdoutSet     bool
	shellStarted  bool
	closed        bool

	waitErr  error
	waitGate chan struct{} // Wait blocks until closed
}

func newFakePtySession(waitErr error) *fakePtySession {
	return &fakePtySession{waitErr: waitErr, waitGate: make(chan struct{})}
}

func (f *fakePtySession) RequestPty(term string, h, w int, _ ssh.TerminalModes) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqTerm, f.reqRows, f.reqW = term, h, w
	return nil
}

func (f *fakePtySession) StdinPipe() (io.WriteCloser, error) {
	return nopWriteCloser{io.Discard}, nil
}

func (f *fakePtySession) SetStdout(io.Writer) {
	f.mu.Lock()
	f.stdoutSet = true
	f.mu.Unlock()
}

func (f *fakePtySession) Shell() error {
	f.mu.Lock()
	f.shellStarted = true
	f.mu.Unlock()
	return nil
}

func (f *fakePtySession) Wait() error {
	<-f.waitGate
	return f.waitErr
}

func (f *fakePtySession) WindowChange(h, w int) error {
	f.mu.Lock()
	f.resizes = append(f.resizes, WinSize{Cols: w, Rows: h})
	f.mu.Unlock()
	return nil
}

func (f *fakePtySession) Close() error {
	f.mu.Lock()
	if !f.closed {
		f.closed = true
		// Closing the session unblocks a parked Wait (mirrors *ssh.Session).
		select {
		case <-f.waitGate:
		default:
			close(f.waitGate)
		}
	}
	f.mu.Unlock()
	return nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// TestRunShellSessionExit drives a normal remote-shell end: Wait returns a
// non-zero ExitError, which runShellSession must treat as success (nil).
func TestRunShellSessionExit(t *testing.T) {
	f := newFakePtySession(&ssh.ExitError{})
	// Let Wait return immediately.
	close(f.waitGate)

	err := runShellSession(context.Background(),
		f, ShellIO{In: strings.NewReader(""), Out: io.Discard}, nil)
	if err != nil {
		t.Fatalf("normal shell exit should be nil, got %v", err)
	}
	if f.reqTerm != defaultTerm || f.reqRows != defaultRows || f.reqW != defaultCols {
		t.Errorf("pty requested {term:%q rows:%d cols:%d}, want defaults {%q %d %d}",
			f.reqTerm, f.reqRows, f.reqW, defaultTerm, defaultRows, defaultCols)
	}
	if !f.stdoutSet || !f.shellStarted {
		t.Errorf("wiring incomplete: stdoutSet=%v shellStarted=%v", f.stdoutSet, f.shellStarted)
	}
}

// TestRunShellSessionResize proves a value sent on the resize channel is
// forwarded to the remote pty via WindowChange before the shell exits.
func TestRunShellSessionResize(t *testing.T) {
	f := newFakePtySession(nil)
	resize := make(chan WinSize, 1)
	resize <- WinSize{Cols: 120, Rows: 40}

	go func() {
		// Give the resize goroutine a moment to forward, then end the shell.
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			f.mu.Lock()
			n := len(f.resizes)
			f.mu.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
		close(f.waitGate)
	}()

	if err := runShellSession(context.Background(),
		f, ShellIO{In: strings.NewReader(""), Out: io.Discard}, resize); err != nil {
		t.Fatalf("clean exit should be nil, got %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.resizes) != 1 || f.resizes[0] != (WinSize{Cols: 120, Rows: 40}) {
		t.Fatalf("resize not forwarded: got %v, want one {120x40}", f.resizes)
	}
}

// TestRunShellSessionCtxCancel proves ctx cancel closes the session (unblocking
// Wait) and returns ctx.Err.
func TestRunShellSessionCtxCancel(t *testing.T) {
	f := newFakePtySession(nil) // Wait stays blocked on waitGate until Close.
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := runShellSession(ctx, f, ShellIO{In: blockingReader{}, Out: io.Discard}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx cancel should return context.Canceled, got %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		t.Error("session should be closed on ctx cancel")
	}
}

// blockingReader blocks forever on Read, modeling a live stdin that never hits
// EOF — so the input-copy goroutine cannot drive termination in the cancel test.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) {
	select {} // block forever
}
