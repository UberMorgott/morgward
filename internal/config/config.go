// Package config holds the runtime configuration assembled from CLI flags or
// interactive prompts.
package config

import (
	"errors"
	"fmt"
	"regexp"
)

// Sentinel validation errors. Callers that localize their UI (the TUI) match on
// these with errors.Is to pick a translated message instead of surfacing the raw
// English text; the CLI prints err.Error() as-is.
var (
	ErrHostRequired = errors.New("host is required")
	ErrUserRequired = errors.New("user is required")
	ErrAuthRequired = errors.New("either password or key is required")
	// ErrBadAdminUser rejects an admin username that is not a strict Linux name.
	// AdminUser is spliced (unquoted in places) into root-run shell scripts and a
	// /home/<user> path, so anything outside this charset is a shell-injection
	// vector (F05/F14) — refuse it before it reaches the box.
	ErrBadAdminUser = errors.New("admin-user must match ^[a-z_][a-z0-9_-]{0,31}$ (lowercase Linux username)")
)

// adminUserRe is the portable Linux username pattern (NAME_REGEX-style): a
// lowercase letter or underscore, then up to 31 of [a-z0-9_-]. Excludes spaces,
// quotes, $, backticks, and every other shell metacharacter.
var adminUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// Config is the fully-resolved input for a hardening run.
type Config struct {
	Host      string // VPS address the controller connects to
	Port      int    // SSH port (22 at bootstrap)
	User      string // bootstrap user (usually root)
	Password  string // bootstrap password (cleared after key auth works)
	KeyPath   string // path to an existing private key; empty => generate one
	AdminUser string // non-root sudo user to create/verify (default: vpsadmin)
	LogFile   string // file path for the full run log; empty => no file written
	Assume    bool   // non-interactive: proceed on brownfield / prompts using defaults
	Lang      string // UI language for engine-streamed messages: "ru" | "en" (empty => "ru"); set by the TUI from its active language, left empty by the CLI
}

// Validate checks that the minimum required fields are present.
func (c *Config) Validate() error {
	if c.Host == "" {
		return ErrHostRequired
	}
	if c.User == "" {
		return ErrUserRequired
	}
	if c.KeyPath == "" && c.Password == "" {
		return ErrAuthRequired
	}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.AdminUser == "" {
		c.AdminUser = "vpsadmin"
	}
	// Validate the RESOLVED admin user (after the default), so the built-in
	// vpsadmin passes and any operator-supplied value is charset-checked before it
	// is spliced into remote root-run scripts.
	if !adminUserRe.MatchString(c.AdminUser) {
		return fmt.Errorf("%w, got %q", ErrBadAdminUser, c.AdminUser)
	}
	return nil
}
