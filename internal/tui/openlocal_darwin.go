//go:build darwin

package tui

// osOpenArgs opens path with the macOS default handler.
func osOpenArgs(path string) (string, []string) {
	return "open", []string{path}
}
