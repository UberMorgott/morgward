// Package termsize is the terminal-size trust boundary: it turns a (possibly
// bogus) reported window size into the size the layout may actually use.
//
// Bubble Tea reports the terminal size ONCE correctly and then unreliably on
// Windows:
//
//   - At startup it queries the console itself (bubbletea tty.go checkResize →
//     x/term GetSize → GetConsoleScreenBufferInfo, reading the srWindow VIEWPORT
//     rect). That value is right on every console host.
//   - Afterwards Windows has no SIGWINCH — verbatim from
//     charm.land/bubbletea/v2@v2.0.8 signals_windows.go: "listenForResize is not
//     available on windows because windows does not implement syscall.SIGWINCH",
//     the whole function being a no-op. Every later WindowSizeMsg is therefore
//     synthesized by ultraviolet from a console WINDOW_BUFFER_SIZE_EVENT,
//     forwarding the record's dwSize — which the Win32 API defines as the size of
//     the console SCREEN BUFFER, not of the visible window (ultraviolet
//     terminal_reader_windows.go, the WINDOW_BUFFER_SIZE_EVENT case).
//
// On the legacy conhost host those two differ: Windows PowerShell 5.1's console is
// a ~120x30 window over a 120x3000 scrollback buffer, so a buffer-size event
// reports Height=3000. A layout that honors it grows its content area to
// h-<chrome> and pushes the bottom-pinned chrome (hint row, monitor footer) ~2970
// rows below the visible viewport while the top of the frame still looks correct.
// Windows Terminal keeps its buffer the same size as its viewport, so the two
// never diverge there and the bug cannot reproduce.
//
// So: never trust a WindowSizeMsg's numbers when the console can be asked directly.
// Keep this the ONE funnel every layout dimension flows through — the model's
// width/height must be set only from the WindowSizeMsg case in Update.
//
// This is the FILTER, not the delivery mechanism: with no SIGWINCH the TUI still
// needs its own resize poll (tui.resizeTick) to learn about a resize at all.
package termsize

import (
	"os"

	"golang.org/x/term"
)

// Viewport reports the console's real VIEWPORT size. ok=false when stdout is not a
// console (tests, redirected output), leaving the message as the only source. A var
// so tests can substitute a fake without a console.
//
// COUPLING: os.Stdout is hard-coded because that is what the program renders to —
// tui.Run builds its tea.Program with no WithOutput, and bubbletea's default output
// IS stdout, so the console read here and the surface being drawn are the same
// device. Adopting tea.WithOutput(w) would break that: this would keep measuring
// stdout and silently override a correct WindowSizeMsg with an unrelated console's
// size. Re-point Viewport at that writer's fd if the program ever takes one.
var Viewport = func() (w, h int, ok bool) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	return w, h, err == nil && w > 0 && h > 0
}

// TrueSize returns the dimensions the layout should use for a WindowSizeMsg
// reporting (msgW, msgH): the console's own viewport rect when it can be read,
// else the message's numbers unchanged.
//
// The preference is deliberately UNCONDITIONAL, not gated on GOOS: on a POSIX tty
// term.GetSize (TIOCGWINSZ) returns exactly what the SIGWINCH-driven message
// carries, so overriding it is a no-op there and one code path stays testable on
// every platform. It only stops being a no-op if stdout stops being the rendered
// surface — see Viewport's COUPLING note.
func TrueSize(msgW, msgH int) (w, h int) {
	if vw, vh, ok := Viewport(); ok {
		return vw, vh
	}
	return msgW, msgH
}
