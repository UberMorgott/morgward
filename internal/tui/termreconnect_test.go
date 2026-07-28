package tui

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// dyingShell is a shellFunc whose remote end dies on command — the reboot case: the
// transport drops, so Shell returns an error without the model closing anything.
type dyingShell struct {
	die     chan struct{}
	started chan struct{}
}

func newDyingShell() *dyingShell {
	return &dyingShell{die: make(chan struct{}), started: make(chan struct{})}
}

func (d *dyingShell) run(ctx context.Context, sio sshx.ShellIO, _ <-chan sshx.WinSize) error {
	close(d.started)
	go func() { _, _ = io.Copy(sio.Out, sio.In) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.die:
		return errors.New("connection lost")
	}
}

// deadSessionModel builds a phaseTerminal model whose session has just died the way a
// server reboot kills it (Shell returned an error; nothing was closed by the model).
func deadSessionModel(t *testing.T, w, h int) model {
	t.Helper()
	m := newModel()
	m.w, m.h = w, h
	m.host = "1.2.3.4"
	m.phase = phaseTerminal
	m.termReturn = phaseDashboard
	m.termGen = 1
	m.termFollow = true
	ds := newDyingShell()
	cols, rows := m.termContentSize()
	m.term = newTermSessionWith(ds.run, cols, rows)
	<-ds.started
	close(ds.die)
	waitFor(t, 2*time.Second, func() bool {
		done, _ := m.term.finished()
		return done
	}, "shell goroutine to record the transport death")
	return m
}

// TestTerminalRepaintsAfterSessionDeath pins what is NOT broken about a mid-session
// reboot, so the "nothing updates" symptom is never misdiagnosed as a repaint bug
// again: with NO phase change and NO tab switch, the frame picks up the dead session
// on its own AND the repaint heartbeat stays armed. The missing piece is reconnection
// (below), not repainting.
// (tt, not t: `t` is the i18n lookup func in this package.)
func TestTerminalRepaintsAfterSessionDeath(tt *testing.T) {
	m := deadSessionModel(tt, 80, 24)
	defer m.term.close()

	// The heartbeat must survive the death: m.term is non-nil and the gen still matches,
	// so the tick handler re-schedules.
	if _, cmd := m.Update(termTickMsg{gen: m.termGen}); cmd == nil {
		tt.Fatal("the repaint tick must stay armed after the session dies")
	}
	// And the frame reflects the dead session with no tab switch.
	if got := m.terminalView(); !strings.Contains(got, t(m.lang, kTermEnded)) {
		tt.Fatal("the frame must show the session-ended banner without a tab switch")
	}
}

// TestTerminalReconnectKeyRedials is the BUG 3 fix: after the box reboots out from under
// the session, the user must be able to reconnect WITHOUT leaving the terminal tab.
// Before the fix the only path back to a live shell was a tab round-trip (Главная →
// Терминал), because openTerminal — the sole dial site — is reachable only through navTo.
func TestTerminalReconnectKeyRedials(t *testing.T) {
	addr, stop := startLoopbackSSH(t)
	defer stop()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}

	m := deadSessionModel(t, 80, 24)
	// Point the form at the loopback server so the redial has somewhere real to go.
	m.inputs[fHost].SetValue(host)
	m.inputs[fPort].SetValue(portStr)
	m.inputs[fUser].SetValue("tester")
	m.inputs[fPass].SetValue("")
	m.inputs[fKey].SetValue("")

	gen0, dead := m.termGen, m.term

	next, _ := m.terminalKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	mm := next.(model)
	defer func() {
		if mm.term != nil {
			mm.term.close()
		}
		if mm.termClient != nil {
			mm.termClient.Close()
		}
	}()

	if mm.termGen == gen0 {
		t.Fatalf("reconnect key did not redial: termGen still %d — the dead session is unrecoverable without a tab round-trip", gen0)
	}
	if mm.term == dead {
		t.Fatal("reconnect key kept the DEAD session — a redial must build a fresh one")
	}
	if mm.termErr != "" {
		t.Fatalf("reconnect dial failed: %s", mm.termErr)
	}
	if mm.termClient == nil {
		t.Fatal("reconnect produced no client")
	}
	if mm.phase != phaseTerminal || mm.wsTab != wsTerminal {
		t.Fatalf("reconnect left phase=%v wsTab=%v, want the terminal tab", mm.phase, mm.wsTab)
	}
}

// TestTerminalReconnectKeyRetriesDialError proves the same key retries a FAILED dial
// (the termErr notice), not just a session that died after connecting.
func TestTerminalReconnectKeyRetriesDialError(t *testing.T) {
	addr, stop := startLoopbackSSH(t)
	defer stop()
	host, portStr, _ := net.SplitHostPort(addr)

	m := newModel()
	m.w, m.h = 80, 24
	m.phase = phaseTerminal
	m.termReturn = phaseDashboard
	m.termGen = 1
	m.termErr = "could not connect: boom" // the dial-failed notice, no session at all
	m.inputs[fHost].SetValue(host)
	m.inputs[fPort].SetValue(portStr)
	m.inputs[fUser].SetValue("tester")

	next, _ := m.terminalKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	mm := next.(model)
	defer func() {
		if mm.term != nil {
			mm.term.close()
		}
		if mm.termClient != nil {
			mm.termClient.Close()
		}
	}()

	if mm.termErr != "" {
		t.Fatalf("reconnect key did not clear the dial error: %q", mm.termErr)
	}
	if mm.termClient == nil {
		t.Fatal("reconnect key did not retry the dial after a dial failure")
	}
}
