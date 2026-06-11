package tui

import (
	"bytes"
	"crypto/ed25519"
	"net"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/crypto/ssh"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// termModel builds a phaseTerminal model with a live (fake-backed) session attached,
// sized for layout/forwarding tests. It bypasses openTerminal's real Dial by wiring a
// fake-shell session directly — the model wiring (key forwarding, exit, render) is the
// unit under test, not the SSH dial.
func termModel(t *testing.T, w, h int) (model, *fakeShell) {
	t.Helper()
	m := newModel()
	m.w, m.h = w, h
	m.host = "1.2.3.4"
	m.phase = phaseTerminal
	m.termReturn = phaseDashboard
	m.termGen = 1
	m.termFollow = true // mirror openTerminal's scroll init (start pinned to the bottom)
	fs := newFakeShell()
	cols, rows := m.termContentSize()
	m.term = newTermSessionWith(fs.run, cols, rows)
	<-fs.started
	return m, fs
}

// TestTerminalKeyForwarding proves a non-exit keypress is encoded and written to the
// session (the fake echoes it back into the emulator, so the view reflects it).
func TestTerminalKeyForwarding(t *testing.T) {
	m, _ := termModel(t, 100, 40)
	defer m.term.close()

	next, _ := m.terminalKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	mm := next.(model)
	if mm.term == nil {
		t.Fatal("terminal session dropped after a normal keypress")
	}
	waitFor(t, time.Second, func() bool {
		return bytes.Contains([]byte(mm.term.view()), []byte("x"))
	}, "echoed keypress to render")
}

// TestTerminalExitKey proves Ctrl+Q closes the session and returns to the previous
// screen, dropping the session pointer (so the emulator is GC'd and the goroutine is
// reaped).
func TestTerminalExitKey(t *testing.T) {
	m, fs := termModel(t, 100, 40)

	next, _ := m.terminalKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	mm := next.(model)
	if mm.phase != phaseDashboard {
		t.Fatalf("after ctrl+q phase = %v, want phaseDashboard (termReturn)", mm.phase)
	}
	if mm.term != nil {
		t.Fatal("session pointer not dropped after exit")
	}
	select {
	case <-fs.returned:
	case <-time.After(time.Second):
		t.Fatal("Shell goroutine not reaped after exit-key close — leak")
	}
}

// TestTerminalCtrlCPassesThrough proves Ctrl+C is NOT an exit (it must reach the
// remote shell as SIGINT 0x03), so the session stays open and the byte is forwarded.
func TestTerminalCtrlCPassesThrough(t *testing.T) {
	m, _ := termModel(t, 100, 40)
	defer m.term.close()

	next, _ := m.terminalKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	mm := next.(model)
	if mm.phase != phaseTerminal {
		t.Fatalf("ctrl+c changed phase to %v — it must pass through to the shell, not exit", mm.phase)
	}
	if mm.term == nil {
		t.Fatal("ctrl+c closed the session — it must pass through")
	}
}

// TestTerminalDialErrorView proves a dial failure renders the error notice (no panic,
// no session) and Esc returns to the previous screen.
func TestTerminalDialErrorView(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseTerminal
	m.termReturn = phaseDashboard
	m.termErr = "could not connect: dial tcp: refused"
	m.term = nil

	// The view must contain the error text and not panic.
	out := m.terminalView()
	if !strings.Contains(out, "could not connect") {
		t.Fatalf("terminalView missing dial error:\n%s", out)
	}

	// Esc returns to termReturn.
	next, _ := m.terminalKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	mm := next.(model)
	if mm.phase != phaseDashboard {
		t.Fatalf("esc on error notice → phase %v, want phaseDashboard", mm.phase)
	}
}

// TestTerminalResizeForwards proves a WindowSizeMsg while the terminal is open resizes
// the live session's emulator to the new content area.
func TestTerminalResizeForwards(t *testing.T) {
	m, _ := termModel(t, 100, 40)
	defer m.term.close()

	next, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 50})
	mm := next.(model)
	wantCols, wantRows := mm.termContentSize()
	if w := mm.term.emu.Width(); w != wantCols {
		t.Errorf("emulator width = %d after resize, want %d", w, wantCols)
	}
	if h := mm.term.emu.Height(); h != wantRows {
		t.Errorf("emulator height = %d after resize, want %d", h, wantRows)
	}
}

// TestTerminalTickStopsAfterClose proves the render tick is dropped once the screen is
// left (gen mismatch / nil session), so there is no busy-loop ticker leak.
func TestTerminalTickStopsAfterClose(t *testing.T) {
	m, _ := termModel(t, 100, 40)
	gen := m.termGen

	// A tick for the current gen while open reschedules (non-nil cmd).
	if _, cmd := m.Update(termTickMsg{gen: gen}); cmd == nil {
		t.Fatal("tick for the open terminal should reschedule")
	}

	// After closing, a tick for the OLD gen must be dropped (nil cmd, no reschedule).
	m = m.closeTerminal()
	if _, cmd := m.Update(termTickMsg{gen: gen}); cmd != nil {
		t.Fatal("stale-gen tick after close must NOT reschedule (ticker leak)")
	}
}

// startLoopbackSSH spins up a minimal in-process SSH server on 127.0.0.1 that accepts
// any client (NoClientAuth) and discards every channel/request. It exists so a test can
// dial a REAL *sshx.Client (with its keepalive goroutine + live transport) and assert
// teardown closes it. Returns the listen address and a stop func.
func startLoopbackSSH(t *testing.T) (addr string, stop func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("gen host key: %v", err)
	}
	signer, err := ssh.NewSignerFromSigner(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					_ = conn.Close()
					return
				}
				// Drain global requests (incl. keepalive@openssh.com) and reject channels —
				// the terminal test never opens a session here; it only needs a live transport
				// + keepalive round-trip so Close() has a real goroutine + conn to tear down.
				go ssh.DiscardRequests(reqs)
				go func() {
					for nc := range chans {
						_ = nc.Reject(ssh.UnknownChannelType, "no channels in loopback test server")
					}
				}()
				<-done
				_ = sc.Close()
			}()
		}
	}()
	return ln.Addr().String(), func() {
		close(done)
		_ = ln.Close()
	}
}

// TestTerminalTeardownClosesClient is the regression guard for the leak fix: every
// terminal open→close cycle must Close() the dialed *sshx.Client (stopping its keepalive
// goroutine + transport), not just the *ssh.Session. It dials a real loopback SSH server,
// attaches the client to the model exactly as openTerminal does (the screen wiring is the
// unit under test, not the Dial), then asserts closeTerminal nils termClient AND the
// goroutine count returns to baseline — proving the keepalive goroutine was reaped.
func TestTerminalTeardownClosesClient(t *testing.T) {
	addr, stop := startLoopbackSSH(t)
	defer stop()

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	port := atoiDefault(portStr, 0)

	cli, err := sshx.Dial(host, port, "tester", "", nil)
	if err != nil {
		t.Fatalf("dial loopback ssh: %v", err)
	}

	// Wire the model as openTerminal does on the success path (client owned by the model,
	// a fake-backed session so we don't need a real channel from the bare server).
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseTerminal
	m.termReturn = phaseDashboard
	m.termGen = 1
	m.termClient = cli
	fs := newFakeShell()
	cols, rows := m.termContentSize()
	m.term = newTermSessionWith(fs.run, cols, rows)
	<-fs.started

	// Sanity: the live transport works before teardown.
	if r := cli.Run("true"); r.Err != nil && strings.Contains(r.Err.Error(), "not connected") {
		t.Fatalf("precondition: client should be connected before close, got %v", r.Err)
	}

	m = m.closeTerminal()

	if m.termClient != nil {
		t.Fatal("closeTerminal did not nil termClient — client leaked")
	}
	// Close() set the client's live *ssh.Client to nil AND stopped its keepalive
	// goroutine; a subsequent Run therefore reports "not connected" — direct, race-free
	// proof the transport was actually torn down (not merely the *ssh.Session).
	if r := cli.Run("true"); r.Err == nil || !strings.Contains(r.Err.Error(), "not connected") {
		t.Fatalf("client transport not closed after closeTerminal: Run err = %v (want \"not connected\")", r.Err)
	}

	// Idempotent: a second close must not panic (double Close / double nil).
	m = m.closeTerminal()
}

// TestGoBackClosesTerminalClient proves the defensive goBack path also closes the dialed
// client (not just the session), so leaving the screen via a home-navigation can't leak
// the transport + keepalive goroutine either.
func TestGoBackClosesTerminalClient(t *testing.T) {
	addr, stop := startLoopbackSSH(t)
	defer stop()

	host, portStr, _ := net.SplitHostPort(addr)
	port := atoiDefault(portStr, 0)
	cli, err := sshx.Dial(host, port, "tester", "", nil)
	if err != nil {
		t.Fatalf("dial loopback ssh: %v", err)
	}

	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseTerminal
	m.termClient = cli
	fs := newFakeShell()
	cols, rows := m.termContentSize()
	m.term = newTermSessionWith(fs.run, cols, rows)
	<-fs.started

	next, _ := m.goBack()
	mm := next.(model)
	if mm.termClient != nil {
		t.Fatal("goBack did not nil termClient — client leaked on home-navigation")
	}
	if mm.term != nil {
		t.Fatal("goBack did not drop the terminal session")
	}
}
