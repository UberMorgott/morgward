package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UberMorgott/morgward/internal/ui"
	"github.com/UberMorgott/morgward/internal/version"
)

// goldenVersion is stamped over version.Version while rendering the golden, so a
// release version bump does not move the file. The golden pins the frame CHROME;
// the version string in the title is not part of that contract, and letting it
// churn per release would train everyone to regenerate the golden on red — the
// exact habit the golden exists to prevent.
const goldenVersion = "0.0.0-golden"

// --- rendered-frame golden ----------------------------------------------------
//
// Every framed screen draws the SAME chrome in the same order (titled top, nav
// line, optional fixed rows, scroll region, pinned rows, hint, bottom border, the
// 3-row monitor box). The chrome-row arithmetic is shared (chromeViewH), so a
// change to one screen's row budget can silently shift a click target computed in
// another function — the compiler cannot see a moved row.
//
// This golden pins the EXACT rendered frame of every such screen at two terminal
// sizes. A pure-rendering refactor must leave the file byte-identical; a real
// layout change is an intentional golden update (UPDATE_GOLDEN=1 go test ./internal/tui).
//
// ANSI/SGR is stripped (ui.SanitizeStreamLine) so the golden is stable across
// terminals/CI colour profiles while every row, column and pad stays pinned.

const framesGoldenPath = "testdata/frames.golden"

// goldenFilesModel builds a wsFiles model with a small deterministic listing.
func goldenFilesModel(w, h int) model {
	m := newModel()
	m.w, m.h = w, h
	m.host = "1.2.3.4"
	m.phase = phaseTerminal
	m.wsTab = wsFiles
	m.files = newFileSession(nil, "/etc/nginx", langRU)
	m.files.entry = []fileEntry{
		{name: "..", isDir: true, mode: "rwxr-xr-x", mtime: "2026-06-09 09:20"},
		{name: "nginx.conf", size: 2048, mode: "rw-r--r--", mtime: "2026-06-09 09:21"},
		{name: "sites-enabled", isDir: true, mode: "rwxr-xr-x", mtime: "2026-06-09 09:22"},
	}
	m.files.sel = 1
	return m
}

// goldenTermModel builds a phaseTerminal model over a fake-backed session seeded with
// a fixed screen. The fake shell echoes exactly the bytes written, in order, so waiting
// for the LAST one ("$") proves the whole seed is drained — the frame is settled, not
// raced. Returns the session closer.
func goldenTermModel(t *testing.T, w, h int) (model, func()) {
	t.Helper()
	m, _ := termModel(t, w, h)
	m.term.write([]byte("uname -sr\r\nLinux 6.14.0-33-generic\r\n$ "))
	waitFor(t, 5*time.Second, func() bool {
		return strings.Contains(stripBlanks(m.term.view()), "$")
	}, "seeded terminal screen to render")
	m.termPinIfFollowing()
	return m, func() { m.term.close() }
}

// renderFrames renders every framed screen at both sizes into one dump.
func renderFrames(t *testing.T) string {
	t.Helper()
	defer func(v string) { version.Version = v }(version.Version)
	version.Version = goldenVersion
	var sb strings.Builder
	for _, sz := range [][2]int{{100, 40}, {60, 20}} {
		w, h := sz[0], sz[1]
		screens := []struct {
			name string
			view func() string
		}{
			{"dashboard", func() string { return dashModel(w, h).dashboardView() }},
			{"dashboard-apply-confirm", func() string {
				m := dashModel(w, h)
				m.dashApplyConfirm = true
				return m.dashboardView()
			}},
			{"security", func() string { return secModel(w, h).securityView() }},
			{"security-danger-confirm", func() string {
				m := secModel(w, h)
				m.secDangerConfirm = true
				return m.securityView()
			}},
			{"summary", func() string { return summaryModel(w, h).summaryView() }},
			{"matrix", func() string { return matrixModel(w, h).matrixView() }},
			{"wiki-applied", func() string {
				return wikiProbeModel(w, h, "a3.installed", "A3", true, 0).wikiView()
			}},
			{"wiki-pending-upgrades", func() string {
				return wikiProbeModel(w, h, "a3.installed", "A3", false, 5).wikiView()
			}},
			{"wiki-update-confirm", func() string {
				m := wikiProbeModel(w, h, "a3.installed", "A3", false, 5)
				m.wikiUpdateConfirm = true
				return m.wikiView()
			}},
			{"key", func() string { return keyModel(w, h, 6).keyView() }},
			{"files", func() string { return goldenFilesModel(w, h).filesView() }},
			{"terminal", func() string {
				tm, closeTerm := goldenTermModel(t, w, h)
				defer closeTerm()
				return tm.terminalView()
			}},
		}
		for _, s := range screens {
			fmt.Fprintf(&sb, "=== %s %dx%d ===\n%s\n", s.name, w, h,
				ui.SanitizeStreamLine(s.view()))
		}
	}
	return sb.String()
}

// TestFramesGolden is the regression net for the shared frame chrome: not one
// rendered row may move. Regenerate deliberately with UPDATE_GOLDEN=1.
func TestFramesGolden(t *testing.T) {
	got := renderFrames(t)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(framesGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(framesGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden updated: %s (%d bytes)", framesGoldenPath, len(got))
		return
	}
	raw, err := os.ReadFile(framesGoldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (regenerate with UPDATE_GOLDEN=1)", err)
	}
	// The repo checks out with autocrlf=true on Windows; normalize so the golden
	// compares by content, not by line ending.
	want := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if got == want {
		return
	}
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		g, w := "", ""
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("rendered frame changed at line %d:\n got: %q\nwant: %q", i+1, g, w)
		}
	}
	t.Fatal("rendered frames differ")
}
