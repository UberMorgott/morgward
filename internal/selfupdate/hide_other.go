//go:build !windows

package selfupdate

// hideFile is a no-op off Windows: there the replaced binary is unlinked
// successfully, so nothing is ever left to hide.
func hideFile(string) error { return nil }
