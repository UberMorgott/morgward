//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleTitleW = kernel32.NewProc("SetConsoleTitleW")
)

// setConsoleTitle sets the console window title via the Win32 API, so the
// taskbar/title bar shows the program name instead of the launch command path
// (legacy conhost does not reliably honor the OSC title escape bubbletea emits).
func setConsoleTitle(title string) {
	p, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	_, _, _ = procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(p))) // #nosec G103 -- required unsafe.Pointer for the Win32 SetConsoleTitleW syscall
}
