// Package config holds the runtime configuration assembled from CLI flags or
// interactive prompts.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/UberMorgott/morgward/internal/sshx"
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
	// ErrPinConflict rejects supplying BOTH --known-hosts and --host-fingerprint:
	// they are two ways to express the same first-handshake expectation and
	// combining them is almost always operator error.
	ErrPinConflict = errors.New("provide only one of --known-hosts or --host-fingerprint, not both")
	// ErrKnownHostsPath rejects a --known-hosts path that does not exist / is not
	// readable, so a typo fails up front instead of silently degrading to TOFU.
	ErrKnownHostsPath = errors.New("known-hosts file is not readable")
	// ErrKnownHostsParse is the DISTINCT failure of a known_hosts file that exists
	// and is readable but does not parse (a malformed line). Kept separate from
	// ErrKnownHostsPath so the operator is not told to fix a path that is fine.
	ErrKnownHostsParse = errors.New("known-hosts file could not be parsed")
	// ErrBadFingerprint rejects a --host-fingerprint that is not a SHA256:<base64>
	// host-key fingerprint (the form `ssh-keygen -lf` prints).
	ErrBadFingerprint = errors.New("host-fingerprint must be a SHA256:<base64> host-key fingerprint")
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

	// KnownHostsPath / HostFingerprint are the OPT-IN host-key pin (FA-0010): when
	// set, the FIRST SSH handshake must match the given known_hosts file / SHA-256
	// fingerprint instead of blind trust-on-first-use. At most one may be set; both
	// empty => default TOFU (unchanged). The actual verification lives in sshx
	// (ParseHostKeyPin); Validate only enforces shape so a typo fails up front.
	KnownHostsPath  string // path to an OpenSSH known_hosts file
	HostFingerprint string // "SHA256:<base64>" host-key fingerprint (ssh-keygen -lf form)
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
	if err := c.validateHostKeyPin(); err != nil {
		return err
	}
	return nil
}

// validateHostKeyPin enforces the shape of the opt-in host-key pin flags (FA-0010):
// at most one source, an existing/readable known_hosts file, and a well-formed
// SHA256:<base64> fingerprint. Both empty is the default TOFU path and passes.
//
// The pin itself is built by sshx.ParseHostKeyPin on the dial path; this calls the
// SAME function so a pin that passes Validate cannot fail later at connect time,
// and only translates its error into the sentinel the caller matches on. (The
// both-set case stays here because the sentinel must be distinguishable and the
// check is two lines.)
//
// ParseHostKeyPin collapses "path unusable" and "file parses badly" into one error,
// so the os.Stat is repeated here to keep those two operator-facing causes on
// distinct sentinels: a readable-but-malformed known_hosts must not be reported as
// a path problem.
func (c *Config) validateHostKeyPin() error {
	kh := strings.TrimSpace(c.KnownHostsPath)
	fp := strings.TrimSpace(c.HostFingerprint)
	if kh != "" && fp != "" {
		return ErrPinConflict
	}
	if kh != "" {
		if _, err := os.Stat(kh); err != nil {
			return fmt.Errorf("%w: %q: %v", ErrKnownHostsPath, kh, err)
		}
	}
	if _, err := sshx.ParseHostKeyPin(kh, fp); err != nil {
		if kh != "" {
			return fmt.Errorf("%w: %q: %v", ErrKnownHostsParse, kh, err)
		}
		return fmt.Errorf("%w, got %q: %v", ErrBadFingerprint, fp, err)
	}
	return nil
}
