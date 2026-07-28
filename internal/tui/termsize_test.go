package tui

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/termsize"
)

// TestMain pins the size trust boundary to "stdout is not a console" for the whole
// package, so every size-driven test uses the WindowSizeMsg's own numbers regardless of
// how the test binary was launched (a `go test` run whose stdout IS a console would
// otherwise have termsize.Viewport override the sizes the tests set). The funnel tests
// below substitute their own fake around this default.
func TestMain(m *testing.M) {
	termsize.Viewport = func() (int, int, bool) { return 0, 0, false }
	os.Exit(m.Run())
}

// TestWindowSizeIgnoresScreenBufferHeight is the Windows-conhost bug in one test: an
// unsolicited WindowSizeMsg carries the console SCREEN BUFFER size (Height=3000, the
// scrollback), not the viewport. The console's real viewport rect must win, so the
// 3000-row buffer height never reaches m.h — otherwise the layout computes against
// h=3000 and pins the monitor footer ~2970 rows below the visible window.
func TestWindowSizeIgnoresScreenBufferHeight(t *testing.T) {
	orig := termsize.Viewport
	t.Cleanup(func() { termsize.Viewport = orig })
	termsize.Viewport = func() (int, int, bool) { return 120, 30, true }

	m := newModel()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 3000})
	mm := next.(model)

	if mm.h == 3000 {
		t.Fatal("screen-buffer height 3000 reached m.h — the layout would pin the footer off-screen")
	}
	if mm.w != 120 || mm.h != 30 {
		t.Fatalf("m.w,m.h = %d,%d, want the console viewport 120,30", mm.w, mm.h)
	}
}

// TestWindowSizeFallsBackToMsg proves the filter is not a hard override: when stdout is
// not a console (redirected output, tests, a non-Windows tty where the message is
// trustworthy), the message's numbers are used unchanged.
func TestWindowSizeFallsBackToMsg(t *testing.T) {
	orig := termsize.Viewport
	t.Cleanup(func() { termsize.Viewport = orig })
	termsize.Viewport = func() (int, int, bool) { return 0, 0, false }

	m := newModel()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	mm := next.(model)
	if mm.w != 100 || mm.h != 40 {
		t.Fatalf("m.w,m.h = %d,%d, want the message's 100,40 when there is no console", mm.w, mm.h)
	}
}
