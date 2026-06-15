package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// Ctrl+2 from the terminal tab switches to Files and keeps the terminal session alive.
func TestCtrl2SwitchesToFiles(t *testing.T) {
	m, _ := termModel(t, 100, 40) // existing helper: live phaseTerminal w/ fake session
	defer m.term.close()
	// A non-nil (unconnected) transport so ensureFiles creates the session — the switch is
	// a deliberate no-op when termClient is nil (dial-failed guard), which is not under test
	// here. No live connection is needed: the tab flip + session creation don't touch it.
	m.termClient = &sshx.Client{}
	next, _ := m.Update(tea.KeyPressMsg{Code: '2', Mod: tea.ModCtrl})
	mm := next.(model)
	if mm.wsTab != wsFiles {
		t.Fatal("ctrl+2 must switch to the Files tab")
	}
	if mm.term == nil {
		t.Fatal("terminal session must stay alive across a tab switch")
	}
}

// Ctrl+Q exits the workspace from the Files tab (not just Terminal).
func TestCtrlQExitsFromFilesTab(t *testing.T) {
	m, _ := termModel(t, 100, 40)
	m.wsTab = wsFiles
	next, _ := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	mm := next.(model)
	if mm.phase == phaseTerminal {
		t.Fatal("ctrl+q on the Files tab must leave phaseTerminal (close the workspace)")
	}
	if mm.term != nil || mm.files != nil {
		t.Fatal("ctrl+q must tear down both the terminal and file sessions")
	}
}

// Files state survives a tab round-trip.
func TestFilesStatePersistsAcrossSwitch(t *testing.T) {
	m, _ := termModel(t, 100, 40)
	defer m.term.close()
	m.files = newFileSession(m.termClient, "/var/log", langRU)
	m.wsTab = wsFiles
	n1, _ := m.Update(tea.KeyPressMsg{Code: '1', Mod: tea.ModCtrl})
	m = n1.(model)
	if m.wsTab != wsTerminal {
		t.Fatal("ctrl+1 must switch to Terminal")
	}
	n2, _ := m.Update(tea.KeyPressMsg{Code: '2', Mod: tea.ModCtrl})
	m = n2.(model)
	if m.files == nil || m.files.cwd != "/var/log" {
		t.Fatal("Files cwd must survive a tab round-trip")
	}
}
