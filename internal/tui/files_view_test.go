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

func TestWSTabAtClick(t *testing.T) {
	m := newModel()
	m.w, m.h = 100, 40
	m.phase = phaseTerminal
	m.wsTab = wsFiles
	m.files = newFileSession(nil, "/", langRU)

	// The tab strip lives on switcherRow. A click on the Terminal pill resolves to
	// wsTerminal; a click on the Files pill resolves to wsFiles.
	termRange, filesRange := m.wsTabZones()
	if tab, ok := m.wsTabAtClick(termRange[0], switcherRow); !ok || tab != wsTerminal {
		t.Fatalf("Terminal-pill click → %v,%v want wsTerminal,true", tab, ok)
	}
	if tab, ok := m.wsTabAtClick(filesRange[0], switcherRow); !ok || tab != wsFiles {
		t.Fatalf("Files-pill click → %v,%v want wsFiles,true", tab, ok)
	}
	// A click off the tab-strip row is not a tab hit.
	if _, ok := m.wsTabAtClick(termRange[0], switcherRow+1); ok {
		t.Fatal("click off the tab-strip row must return ok=false")
	}
}
