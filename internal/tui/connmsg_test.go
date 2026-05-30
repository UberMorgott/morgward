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
	if m.keyShown {
		t.Fatalf("supplied --key set keyShown=true; want false")
	}
}

// A freshly generated ephemeral key (KeyGenerated=true) MUST route to the key
// screen once so the operator can copy it before it is lost.
func TestConnMsg_GeneratedKey_RoutesToKeyScreen(t *testing.T) {
	m, stop := route(t, monitor.ConnInfo{KeyPEM: []byte("PRIVATE"), KeyGenerated: true})
	defer stop()
	if m.phase != phaseKey {
		t.Fatalf("generated key did not route to phaseKey; phase=%v", m.phase)
	}
	if !m.keyShown {
		t.Fatalf("generated key left keyShown=false; want true")
	}
	if m.keyReturn != phaseRun {
		t.Fatalf("keyReturn=%v want %v (the prior phase)", m.keyReturn, phaseRun)
	}
}
