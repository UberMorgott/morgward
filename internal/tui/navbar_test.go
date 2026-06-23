package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// liveWorkspaceModel returns a model that LOOKS like a live terminal workspace (a
// non-nil termClient + term + files, no termErr) WITHOUT a real dial — enough to exercise
// the navTo/openTerminal reuse + keep-alive paths. The pointers are stubs; the reuse paths
// under test never dereference the transport (navHome doesn't touch it; navTerminal only
// flips wsTab; navFiles finds m.files already set so ensureFiles is a no-op).
func liveWorkspaceModel(w, h int) model {
	m := newModel()
	m.w, m.h = w, h
	m.phase = phaseTerminal
	m.wsTab = wsTerminal
	m.termClient = &sshx.Client{}
	m.term = &termSession{}
	m.files = newFileSession(nil, "/root", langRU)
	m.termReturn = phaseDashboard
	return m
}

// TestNavToHomeKeepsSessionAlive asserts switching to Главная sets phaseDashboard WITHOUT
// tearing down the terminal: term/termClient/files stay non-nil and termGen is NOT bumped
// (only closeTerminal invalidates the running render tick).
func TestNavToHomeKeepsSessionAlive(t *testing.T) {
	m := liveWorkspaceModel(100, 40)
	gen0 := m.termGen
	client0 := m.termClient
	term0 := m.term
	files0 := m.files

	next, _ := m.navTo(navHome)
	mm := next.(model)

	if mm.phase != phaseDashboard {
		t.Fatalf("navTo(navHome) phase=%v, want phaseDashboard", mm.phase)
	}
	if mm.termClient != client0 || mm.termClient == nil {
		t.Fatal("navTo(navHome) must KEEP termClient alive (no closeTerminal)")
	}
	if mm.term != term0 || mm.term == nil {
		t.Fatal("navTo(navHome) must KEEP the term session alive")
	}
	if mm.files != files0 || mm.files == nil {
		t.Fatal("navTo(navHome) must KEEP the files session alive")
	}
	if mm.termGen != gen0 {
		t.Fatalf("navTo(navHome) bumped termGen %d→%d (keep-alive must not bump it)", gen0, mm.termGen)
	}
}

// TestNavToTerminalReuseNoRedial asserts re-entering Терминал while a session is alive
// REUSES it: the termClient pointer is identical (no second Dial) and termGen is unchanged.
func TestNavToTerminalReuseNoRedial(t *testing.T) {
	// Start parked on the Dashboard but with a live session retained (the keep-alive state).
	m := liveWorkspaceModel(100, 40)
	m.phase = phaseDashboard
	gen0 := m.termGen
	client0 := m.termClient

	next, _ := m.navTo(navTerminal)
	mm := next.(model)

	if mm.phase != phaseTerminal {
		t.Fatalf("navTo(navTerminal) phase=%v, want phaseTerminal", mm.phase)
	}
	if mm.wsTab != wsTerminal {
		t.Fatalf("navTo(navTerminal) wsTab=%v, want wsTerminal", mm.wsTab)
	}
	if mm.termClient != client0 {
		t.Fatal("navTo(navTerminal) redialed (termClient pointer changed) — must reuse the live session")
	}
	if mm.termGen != gen0 {
		t.Fatalf("navTo(navTerminal) bumped termGen %d→%d — a reuse must not redial", gen0, mm.termGen)
	}
	if mm.termReturn != phaseDashboard {
		t.Fatalf("navTo(navTerminal) termReturn=%v, want phaseDashboard", mm.termReturn)
	}
}

// TestNavToFilesReuseNoRedial asserts re-entering Файлы while alive reuses the session
// (no redial) and lands on the Files tab.
func TestNavToFilesReuseNoRedial(t *testing.T) {
	m := liveWorkspaceModel(100, 40)
	m.phase = phaseDashboard
	gen0 := m.termGen
	client0 := m.termClient
	files0 := m.files

	next, _ := m.navTo(navFiles)
	mm := next.(model)

	if mm.phase != phaseTerminal || mm.wsTab != wsFiles {
		t.Fatalf("navTo(navFiles) phase=%v wsTab=%v, want phaseTerminal/wsFiles", mm.phase, mm.wsTab)
	}
	if mm.termClient != client0 {
		t.Fatal("navTo(navFiles) redialed — must reuse the live session")
	}
	if mm.files != files0 {
		t.Fatal("navTo(navFiles) recreated the files session — ensureFiles must be a no-op when one exists")
	}
	if mm.termGen != gen0 {
		t.Fatalf("navTo(navFiles) bumped termGen %d→%d — a reuse must not redial", gen0, mm.termGen)
	}
}

// TestDashboardNoTerminalFilesButtons asserts the Dashboard action row exposes ONLY the
// Apply + Security buttons (Terminal/Files moved to the nav bar): the names list is length
// 2 and a click anywhere on the buttons row never resolves to a removed Terminal/Files
// action (those dashButton values no longer exist; here we assert the hit-test only ever
// yields Apply/Security/None).
func TestDashboardNoTerminalFilesButtons(t *testing.T) {
	m := dashModel(100, 40)
	names := m.dashButtonNames()
	if len(names) != 2 {
		t.Fatalf("dashButtonNames has %d buttons, want 2 (Apply, Security)", len(names))
	}
	innerW := innerWidth(m.boxWidth())
	btnRow := m.dashButtonsRowY(innerW)
	ranges := pillRanges(names, dashButtonStartCol)
	got := make([]dashButton, len(ranges))
	for i, r := range ranges {
		got[i] = m.dashButtonAtClick(r[0]+1, btnRow)
	}
	if got[0] != dashBtnApply || got[1] != dashBtnSecurity {
		t.Fatalf("dashboard buttons resolved to %v, want [Apply Security]", got)
	}
	// A click PAST the two pills (where Terminal/Files used to be) must be a miss.
	pastX := ranges[len(ranges)-1][1] + 5
	if hit := m.dashButtonAtClick(pastX, btnRow); hit != dashBtnNone {
		t.Fatalf("click past the 2 buttons → %v, want dashBtnNone (no Terminal/Files button)", hit)
	}
}

// TestDashboardNavBarClickRoutesHome asserts a click on the Главная cell of the bar while
// on the Dashboard is a harmless self-switch (stays on phaseDashboard) and a click on the
// Терминал cell routes through navTo (dials — here the empty form fails the dial, so it
// lands on phaseTerminal with termErr set, NOT a crash).
func TestDashboardNavBarClickRoutesHome(t *testing.T) {
	m := dashModel(100, 40)
	homeRange, _, _ := m.navTabZones()
	next, _ := m.dashboardClick(homeRange[0]+1, switcherRow)
	mm := next.(model)
	if mm.phase != phaseDashboard {
		t.Fatalf("Home-cell click on Dashboard → phase %v, want phaseDashboard", mm.phase)
	}
}

// TestNavToTerminalFinishedSessionNoReuse asserts a FINISHED (dead) session is NOT
// reused: when the remote shell exited (term.finished()==true) but termErr is still ""
// and termClient is still non-nil, re-entering Терминал must NOT take the reuse branch —
// it must tear the dead session down and attempt a fresh dial. With an empty form (no
// host) the fresh dial fails fast, leaving termErr set and termClient nil, and crucially
// the DEAD term pointer must not be retained.
func TestNavToTerminalFinishedSessionNoReuse(t *testing.T) {
	m, _ := termModel(t, 100, 40) // real session (cancel/pipe set → closeTerminal is safe)
	// Mark the session FINISHED, mimicking the remote shell having exited via `exit`.
	m.term.mu.Lock()
	m.term.done = true
	m.term.mu.Unlock()
	m.termClient = &sshx.Client{} // non-nil, no termErr — the trap the old guard fell into
	deadTerm := m.term
	m.phase = phaseDashboard

	next, _ := m.navTo(navTerminal)
	mm := next.(model)

	// The dead term pointer must NOT survive — the reuse branch would have kept it.
	if mm.term == deadTerm {
		t.Fatal("navTo(navTerminal) reused a FINISHED session — it must tear it down and re-dial")
	}
	// The fresh-dial path ran (empty host → dial fails fast): termErr set, no leaked client.
	if mm.termErr == "" {
		t.Fatalf("expected a fresh dial to fail (empty host) setting termErr; got termErr=%q term=%v", mm.termErr, mm.term)
	}
	if mm.termClient != nil {
		t.Fatal("a failed fresh dial must leave termClient nil (the dead one was closed, the new one never connected)")
	}
}

// TestWorkspaceCtrlKeysSwitch asserts the renumbered ctrl+1/2/3 route correctly from the
// workspace: ctrl+1 → Главная (keep alive, phaseDashboard), ctrl+2 → Terminal tab.
func TestWorkspaceCtrlKeysSwitch(t *testing.T) {
	m := liveWorkspaceModel(100, 40)
	m.wsTab = wsFiles
	// ctrl+1 → Главная, keeping the session.
	next, _ := m.workspaceKey(tea.KeyPressMsg{Code: '1', Mod: tea.ModCtrl, Text: ""})
	mm := next.(model)
	if mm.phase != phaseDashboard {
		t.Fatalf("ctrl+1 phase=%v, want phaseDashboard", mm.phase)
	}
	if mm.termClient == nil {
		t.Fatal("ctrl+1 (Главная) must keep the session alive")
	}
}
