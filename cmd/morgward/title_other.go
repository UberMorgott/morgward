//go:build !windows

package main

// setConsoleTitle is a no-op off Windows (POSIX terminals get the title from the
// TUI's OSC escape via tea.SetWindowTitle).
func setConsoleTitle(string) {}
