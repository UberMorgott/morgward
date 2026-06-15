//go:build windows

package tui

// osOpenArgs opens path with the Windows shell default handler. `cmd /c start "" <path>`:
// `start` treats a quoted first token as the window TITLE, so the empty "" placeholder is
// required or a path in quotes would be swallowed as the title. path is a discrete arg.
func osOpenArgs(path string) (string, []string) {
	return "cmd", []string{"/c", "start", "", path}
}
