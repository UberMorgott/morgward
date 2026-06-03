package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/monitor"
)

// route drives one connMsg through Update and returns the resulting model plus a
// cleanup that stops the background sampler goroutine the handler spawns.
func route(t *testing.T, info monitor.ConnInfo) (model, func()) {
	t.Helper()
	start := model{phase: phaseRun}
	next, _ := start.Update(tea.Msg(connMsg(info)))
	m := next.(model)
	stop := func() {
		if m.stopSample != nil {
			m.stopSample()
		}
	}
	return m, stop
}

// A user-supplied --key (KeyGenerated=false) must NOT route to the key screen —
// the operator must never be shown their own private key.
func TestConnMsg_SuppliedKey_NoKeyScreen(t *testing.T) {
	m, stop := route(t, monitor.ConnInfo{KeyPEM: []byte("PRIVATE"), KeyGenerated: false})
	defer stop()
	if m.phase == phaseKey {
		t.Fatalf("supplied --key routed to phaseKey; phase=%v want %v", m.phase, phaseRun)
	}
}

// CHANGE 2: the generated key is now shown as a PRE-RUN modal (start() routes to
// phaseKey BEFORE launching the engine), so a generated-key connect must NOT
// re-route to the key screen on connect (that would double-show it after the run
// started). It records keyGenerated=true (so the summary can re-show the key) and
// stashes the PEM, but stays on the run view.
func TestConnMsg_GeneratedKey_NoReRouteButRecorded(t *testing.T) {
	m, stop := route(t, monitor.ConnInfo{KeyPEM: []byte("PRIVATE"), KeyGenerated: true})
	defer stop()
	if m.phase == phaseKey {
		t.Fatalf("generated key re-routed to phaseKey on connect; want it shown pre-run only (phase=%v)", m.phase)
	}
	if !m.keyGenerated {
		t.Fatalf("generated-key connect left keyGenerated=false; want true (summary key-show row)")
	}
	if m.keyPEM != "PRIVATE" {
		t.Fatalf("generated-key connect did not stash the PEM; keyPEM=%q", m.keyPEM)
	}
}
