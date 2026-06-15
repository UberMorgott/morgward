//go:build !windows && !darwin

package tui

// osOpenArgs opens path with the freedesktop default handler (xdg-open).
func osOpenArgs(path string) (string, []string) {
	return "xdg-open", []string{path}
}
