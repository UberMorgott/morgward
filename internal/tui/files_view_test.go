package tui

import (
	"strings"
	"testing"
)

func TestFilesBodyRendersEntries(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseTerminal
	m.wsTab = wsFiles
	m.files = newFileSession(nil, "/etc", langRU)
	m.files.entry = []fileEntry{{name: "..", isDir: true}, {name: "hosts", size: 200}}
	m.files.sel = 1
	body := m.filesBody()
	joined := strings.Join(body, "\n")
	if !strings.Contains(joined, "hosts") || !strings.Contains(joined, "..") {
		t.Fatalf("listing missing entries:\n%s", joined)
	}
}

func TestFilesViewShowsCwd(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseTerminal
	m.wsTab = wsFiles
	m.files = newFileSession(nil, "/etc/nginx", langRU)
	if !strings.Contains(m.filesView(), "/etc/nginx") {
		t.Fatal("address bar must show cwd")
	}
}

func TestFilesRowAtClick(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseTerminal
	m.wsTab = wsFiles
	m.files = newFileSession(nil, "/", langRU)
	m.files.entry = make([]fileEntry, 5)

	// A click on the FIRST listing row (filesListTopRow) maps to entry index 0.
	idx, ok := m.filesRowAtClick(4, filesListTopRow)
	if !ok || idx != 0 {
		t.Fatalf("first listing row click → idx=%d ok=%v, want 0,true", idx, ok)
	}
	// The third listing row → index 2.
	idx, ok = m.filesRowAtClick(4, filesListTopRow+2)
	if !ok || idx != 2 {
		t.Fatalf("third listing row click → idx=%d ok=%v, want 2,true", idx, ok)
	}
	// A click ABOVE the listing region (the fixed header chrome) is not a row.
	if _, ok := m.filesRowAtClick(4, filesListTopRow-1); ok {
		t.Fatal("click above the listing region must return ok=false")
	}
	// A click past the last entry (within the padded region) is not a row.
	if _, ok := m.filesRowAtClick(4, filesListTopRow+5); ok {
		t.Fatal("click past the last entry must return ok=false")
	}
}

func TestNavTabAtClick(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseTerminal
	m.wsTab = wsFiles
	m.files = newFileSession(nil, "/", langRU)

	// The 3-cell global bar lives on switcherRow. A click on each pill resolves to its
	// nav target (Главная · Терминал · Файлы), pure-function geometry matching the render.
	homeRange, termRange, filesRange := m.navTabZones()
	if tgt, ok := m.navTabAtClick(homeRange[0], switcherRow); !ok || tgt != navHome {
		t.Fatalf("Home-pill click → %v,%v want navHome,true", tgt, ok)
	}
	if tgt, ok := m.navTabAtClick(termRange[0], switcherRow); !ok || tgt != navTerminal {
		t.Fatalf("Terminal-pill click → %v,%v want navTerminal,true", tgt, ok)
	}
	if tgt, ok := m.navTabAtClick(filesRange[0], switcherRow); !ok || tgt != navFiles {
		t.Fatalf("Files-pill click → %v,%v want navFiles,true", tgt, ok)
	}
	// The three zones are disjoint and left→right ordered.
	if !(homeRange[1] <= termRange[0] && termRange[1] <= filesRange[0]) {
		t.Fatalf("nav pill zones overlap/out of order: home=%v term=%v files=%v", homeRange, termRange, filesRange)
	}
	// A click off the bar row is not a tab hit.
	if _, ok := m.navTabAtClick(termRange[0], switcherRow+1); ok {
		t.Fatal("click off the bar row must return ok=false")
	}
	// The active cell follows the current frame: on the Files tab, Файлы is active.
	if got := m.navActiveTab(); got != navFiles {
		t.Fatalf("navActiveTab on wsFiles → %v, want navFiles", got)
	}
	m.phase = phaseDashboard
	if got := m.navActiveTab(); got != navHome {
		t.Fatalf("navActiveTab on phaseDashboard → %v, want navHome", got)
	}
}
