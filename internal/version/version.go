// Package version holds the program name + version, shared by the TUI header and
// any other surface that needs it (the TUI must not import package main).
package version

const (
	// Name is the program's display name.
	Name = "Morgward"
	// Version is the Morgward release version (semver).
	Version = "0.1.0"
	// Tagline is a short description shown next to the name.
	Tagline = "VPS guardian"
)
