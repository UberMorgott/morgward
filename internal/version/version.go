// Package version holds the program name + version, shared by the TUI header and
// any other surface that needs it (the TUI must not import package main).
package version

const (
	// Name is the program's display name.
	Name = "Morgward"
	// Tagline is a short description shown next to the name.
	Tagline = "VPS guardian"
)

// Version is the Morgward release version (semver). A var (not const) so release
// builds / tests can stamp it via -ldflags "-X .../version.Version=X.Y.Z".
var Version = "0.8.0"
