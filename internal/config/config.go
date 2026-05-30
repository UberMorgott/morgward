// Package config holds the runtime configuration assembled from CLI flags or
// interactive prompts.
package config

import (
	"errors"
	"fmt"
)

// Sentinel validation errors. Callers that localize their UI (the TUI) match on
// these with errors.Is to pick a translated message instead of surfacing the raw
// English text; the CLI prints err.Error() as-is.
var (
	ErrHostRequired = errors.New("host is required")
	ErrUserRequired = errors.New("user is required")
	ErrAuthRequired = errors.New("either password or key is required")
	ErrBadMode      = errors.New("mode must be soft or strict")
)

// Mode is the hardening profile selected by the operator.
type Mode string

const (
	// ModeSoft keeps a console password fallback (PermitRootLogin prohibit-password,
	// root password NOT locked). Default — safer for beginners.
	ModeSoft Mode = "soft"
	// ModeStrict locks the root password and sets PermitRootLogin no.
	ModeStrict Mode = "strict"
)

// Config is the fully-resolved input for a hardening run.
type Config struct {
	Host      string // VPS address the controller connects to
	Port      int    // SSH port (22 at bootstrap)
	User      string // bootstrap user (usually root)
	Password  string // bootstrap password (cleared after key auth works)
	KeyPath   string // path to an existing private key; empty => generate one
	Mode      Mode   // soft | strict
	AdminUser string // non-root sudo user to create/verify (default: vpsadmin)
	LogDir    string // directory for the run log + checkpoint
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
	if c.Mode != ModeSoft && c.Mode != ModeStrict {
		return fmt.Errorf("%w, got %q", ErrBadMode, c.Mode)
	}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.AdminUser == "" {
		c.AdminUser = "vpsadmin"
	}
	return nil
}
