package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// wrapTo wraps text to w display cells the same way the viewport does (model.wrapped
// uses lipgloss.NewStyle().Width(w).Render). Pulled out so the test exercises the
// exact wrap the on-screen path uses, without needing a full model.
func wrapTo(s string, w int) string {
	return lipgloss.NewStyle().Width(w).Render(s)
}

func TestSanitizeStreamLine(t *testing.T) {
	const esc = "\x1b"

	cases := []struct {
		name string
		in   string
	}{
		{"plain", "hello world from the server"},
		{"long line", strings.Repeat("abcdefghij ", 40)},
		{"carriage returns (apt progress)", "Reading\rReading 30%\rReading 60%\rReading done"},
		{"tabs", "col1\tcol2\t\tcol3"},
		{"ansi color (SGR)", esc + "[31mred" + esc + "[0m and " + esc + "[1;32mbold green" + esc + "[0m"},
		{"ansi cursor move + erase", "start" + esc + "[2K" + esc + "[1G" + "redrawn" + esc + "[10C tail"},
		{"cjk wide runes", "服务器输出 中文 测试 行 こんにちは 世界"},
		{"mixed garbage", esc + "[33mwarn\t" + "70%\rdone" + esc + "[0m" + "\x07bell"},
	}

	const width = 20

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			san := sanitizeStreamLine(c.in)

			// No carriage returns, no ESC, no other C0 controls survive (newlines are
			// the only allowed control char, as the chunk may hold several lines).
			for _, r := range san {
				if r == '\r' {
					t.Fatalf("sanitized output still contains \\r: %q", san)
				}
				if r == 0x1b {
					t.Fatalf("sanitized output still contains ESC: %q", san)
				}
				if r == '\t' {
					t.Fatalf("sanitized output still contains a tab: %q", san)
				}
				if (r < 0x20 && r != '\n') || r == 0x7f {
					t.Fatalf("sanitized output contains a control char %#x: %q", r, san)
				}
			}

			// After wrapping to width, every physical line must be ≤ width display
			// cells so it can never push past the box border.
			wrapped := wrapTo(san, width)
			for _, ln := range strings.Split(wrapped, "\n") {
				if w := lipgloss.Width(ln); w > width {
					t.Fatalf("wrapped line exceeds width %d (got %d): %q", width, w, ln)
				}
			}
		})
	}
}

// TestSanitizeStreamLineCRKeepsLastSegment locks in that \r redraws collapse to the
// last segment (what the terminal would have shown after the progress settled).
func TestSanitizeStreamLineCRKeepsLastSegment(t *testing.T) {
	got := sanitizeStreamLine("Unpacking 30%\rUnpacking 80%\rUnpacking 100%")
	if got != "Unpacking 100%" {
		t.Fatalf("CR collapse: got %q, want %q", got, "Unpacking 100%")
	}
}

// TestSanitizeStreamLineMultiline confirms newline structure is preserved (each
// physical line sanitized independently).
func TestSanitizeStreamLineMultiline(t *testing.T) {
	in := "line one\r\x1b[31mline two\x1b[0m\tend\n  │ second line"
	got := sanitizeStreamLine(in)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), got)
	}
	if lines[0] != "line two end" {
		t.Fatalf("line 0: got %q, want %q", lines[0], "line two end")
	}
	if lines[1] != "  │ second line" {
		t.Fatalf("line 1: got %q, want %q", lines[1], "  │ second line")
	}
}
