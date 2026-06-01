// Package ui provides colored terminal output mirrored to a timestamped log file.
// Per-step status is rendered as [OK] / [SKIP] / [FAIL] exactly as the runbook
// progress contract requires. Output can be redirected to a sink (e.g. a TUI)
// instead of stdout via SetSink.
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ANSI color codes. Windows 11 terminals support VT sequences out of the box.
const (
	cReset  = "\033[0m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cRed    = "\033[31m"
	cCyan   = "\033[36m"
	cGray   = "\033[90m"
	cBold   = "\033[1m"
)

// Logger writes to stdout (or a sink) and to a log file (plain).
type Logger struct {
	file  *os.File
	color bool
	sink  func(string) // when set, terminal lines go here instead of stdout
}

// New returns a Logger. If path is empty, no file is created and output goes
// only to stdout (or the sink). If path is non-empty, the run log is written to
// that file; on failure to open it, a warning is printed and the Logger
// continues without a file (it never panics or fails the program).
func New(path string) *Logger {
	l := &Logger{color: true}
	if path == "" {
		return l
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot create log directory %q: %v (continuing without a log file)\n", dir, err)
			return l
		}
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot create log file %q: %v (continuing without a log file)\n", path, err)
		return l
	}
	l.file = f
	l.raw(fmt.Sprintf("# morgward run log — %s\n", time.Now().Format(time.RFC3339)))
	return l
}

// SetSink redirects terminal output to fn (one call per line, colored). When a
// sink is set, nothing is printed to stdout (the sink owns the screen). The log
// file still receives plain text.
func (l *Logger) SetSink(fn func(string)) { l.sink = fn }

// Path returns the log file path.
func (l *Logger) Path() string {
	if l.file == nil {
		return ""
	}
	return l.file.Name()
}

// Close flushes and closes the log file.
func (l *Logger) Close() {
	if l.file != nil {
		_ = l.file.Close()
	}
}

func (l *Logger) raw(s string) {
	if l.file != nil {
		_, _ = l.file.WriteString(s)
	}
}

// emit sends a finished terminal line (with trailing newline) to the sink or stdout.
func (l *Logger) emit(colored string) {
	if l.sink != nil {
		l.sink(colored)
		return
	}
	fmt.Print(colored)
}

// stamp emits a timestamped line to both sinks; term gets color, file gets plain.
func (l *Logger) stamp(color, plainPrefix, msg string) {
	ts := time.Now().Format("15:04:05")
	if l.color {
		l.emit(fmt.Sprintf("%s%s%s %s%s%s\n", cGray, ts, cReset, color, msg, cReset))
	} else {
		l.emit(fmt.Sprintf("%s %s\n", ts, msg))
	}
	l.raw(fmt.Sprintf("%s %s%s\n", ts, plainPrefix, stripANSI(msg)))
}

// Banner prints a bold section header.
func (l *Logger) Banner(s string) {
	line := strings.Repeat("─", 4) + " " + s + " " + strings.Repeat("─", max(0, 60-len(s)))
	if l.color {
		l.emit(fmt.Sprintf("\n%s%s%s%s\n", cBold, cCyan, line, cReset))
	} else {
		l.emit("\n" + line + "\n")
	}
	l.raw("\n## " + s + "\n")
}

// Info prints an informational line.
func (l *Logger) Info(format string, a ...any) {
	l.stamp(cReset, "INFO ", fmt.Sprintf(format, a...))
}

// Detail prints a dim secondary line (command output, notes).
func (l *Logger) Detail(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	ts := time.Now().Format("15:04:05")
	if l.color {
		l.emit(fmt.Sprintf("%s%s   %s%s\n", cGray, ts, msg, cReset))
	} else {
		l.emit(fmt.Sprintf("%s    %s\n", ts, msg))
	}
	l.raw(fmt.Sprintf("%s   %s\n", ts, msg))
}

// isBenignNoise reports whether a stderr line is known-harmless OS chatter that
// should not be rendered as an alarming error. On Ubuntu 26.04 the rust-coreutils
// package wires an LD_PRELOAD for libstdbuf.so; when apt/dpkg invoke helpers that
// lack it, ld.so emits dozens of lines of the shape
//
//	ERROR: ld.so: object '.../libstdbuf.so' from LD_PRELOAD cannot be preloaded
//	(cannot open shared object file): ignored.
//
// The OS itself ends each with ": ignored." and carries on — these are not
// failures. Matched conservatively (must end in ": ignored." AND mention
// LD_PRELOAD) so real errors keep the red treatment.
func isBenignNoise(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasSuffix(t, ": ignored.") && strings.Contains(t, "LD_PRELOAD")
}

// Stream is the raw passthrough for live server output: a single line emitted by
// a remote command (stream is "out" or "err"). It goes verbatim to the log file
// AND to the streaming sink the TUI consumes, with a subtle dim prefix so it reads
// as server output, distinct from the decorated STEP/OK/FAIL lines — but never
// swallowed. This is the sink the client's OnOutput is wired to in the engine.
func (l *Logger) Stream(stream, line string) {
	if l.color {
		// Dim the whole server-output line (real stderr that is not benign OS noise
		// is alarming red) so it reads as remote chatter, distinct from the decorated
		// STEP/OK/FAIL lines. No "│" gutter glyph: inside the TUI box the ANSI colour
		// is stripped, leaving only a bare bar that tears on every wrapped line — a
		// plain two-space indent cannot misalign.
		color := cGray
		if stream == "err" && !isBenignNoise(line) {
			color = cRed
		}
		l.emit(fmt.Sprintf("%s  %s%s\n", color, line, cReset))
	} else {
		l.emit(fmt.Sprintf("  %s\n", line))
	}
	l.raw(fmt.Sprintf("  | %s\n", line))
}

// Step prints a step start marker.
func (l *Logger) Step(id, title string) {
	l.stamp(cCyan, "STEP ", fmt.Sprintf("▶ %s — %s", id, title))
}

// OK marks a step succeeded.
func (l *Logger) OK(format string, a ...any) {
	l.stamp(cGreen, "OK   ", "[OK] "+fmt.Sprintf(format, a...))
}

// Skip marks a step skipped (idempotency / not-applicable).
func (l *Logger) Skip(format string, a ...any) {
	l.stamp(cYellow, "SKIP ", "[SKIP] "+fmt.Sprintf(format, a...))
}

// Fail marks a step failed.
func (l *Logger) Fail(format string, a ...any) {
	l.stamp(cRed, "FAIL ", "[FAIL] "+fmt.Sprintf(format, a...))
}

// Warn prints a non-fatal warning.
func (l *Logger) Warn(format string, a ...any) {
	l.stamp(cYellow, "WARN ", "⚠ "+fmt.Sprintf(format, a...))
}

// Secret surfaces a one-time secret (e.g. console password). It is written to
// the terminal/sink but deliberately NOT to the log file (anti-leak rule).
func (l *Logger) Secret(label, value string) {
	if l.color {
		l.emit(fmt.Sprintf("%s%s>>> %s: %s%s\n", cBold, cYellow, label, value, cReset))
	} else {
		l.emit(fmt.Sprintf(">>> %s: %s\n", label, value))
	}
	l.raw(fmt.Sprintf("%s %s: <redacted — shown once on terminal only>\n",
		time.Now().Format("15:04:05"), label))
}

func stripANSI(s string) string {
	for _, c := range []string{cReset, cGreen, cYellow, cRed, cCyan, cGray, cBold} {
		s = strings.ReplaceAll(s, c, "")
	}
	return s
}
