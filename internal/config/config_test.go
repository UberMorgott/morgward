package config

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// baseValid returns a Config that passes every check except whatever the caller
// mutates, so a test can isolate a single field's validation.
func baseValid() *Config {
	return &Config{
		Host:      "1.2.3.4",
		User:      "root",
		Password:  "pw",
		AdminUser: "vpsadmin",
	}
}

// TestValidateAdminUser covers F05/F14: AdminUser is spliced into root-run shell
// scripts and a /home/<user> path, so it must match a strict Linux-username charset.
// Empty resolves to the vpsadmin default (and passes); shell metacharacters reject.
func TestValidateAdminUser(t *testing.T) {
	cases := []struct {
		name    string
		admin   string
		wantErr bool
	}{
		{"default vpsadmin", "vpsadmin", false},
		{"empty defaults to vpsadmin", "", false},
		{"leading underscore", "_svc", false},
		{"digits and dash", "deploy-01", false},
		{"single lowercase letter", "a", false},
		{"max length 32", "a234567890123456789012345678901b", false},

		{"leading digit", "1admin", true},
		{"uppercase", "Admin", true},
		{"too long 33", "a2345678901234567890123456789012c", true},
		{"space", "vps admin", true},
		{"semicolon", "admin;rm", true},
		{"command sub", "admin$(id)", true},
		{"single quote", "admin'x", true},
		{"backtick", "admin`id`", true},
		{"slash", "admin/x", true},
		{"dollar", "admin$x", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := baseValid()
			cfg.AdminUser = c.admin
			err := cfg.Validate()
			if c.wantErr {
				if !errors.Is(err, ErrBadAdminUser) {
					t.Fatalf("AdminUser %q: err = %v, want ErrBadAdminUser", c.admin, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("AdminUser %q: unexpected err %v", c.admin, err)
			}
		})
	}
}

// goodFingerprint returns a syntactically valid SHA256:<base64> host-key
// fingerprint (32-byte digest, OpenSSH unpadded base64) for the pin-flag tests.
func goodFingerprint() string {
	sum := sha256.Sum256([]byte("any 32-byte-after-hash payload"))
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// TestValidateHostKeyPin covers FA-0010 flag-shape validation: both empty is the
// default TOFU path; both set conflicts; a missing known_hosts path and a malformed
// fingerprint are rejected up front; one valid source of either kind passes.
func TestValidateHostKeyPin(t *testing.T) {
	khFile := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(khFile, []byte("example.com ssh-ed25519 AAAA...\n"), 0o600); err != nil {
		t.Fatalf("seed known_hosts: %v", err)
	}
	fp := goodFingerprint()

	cases := []struct {
		name    string
		kh, fp  string
		wantErr error // nil => must pass
	}{
		{"both empty (TOFU)", "", "", nil},
		{"valid known_hosts", khFile, "", nil},
		{"valid fingerprint", "", fp, nil},
		{"valid fingerprint no prefix", "", fp[len("SHA256:"):], nil},
		{"both set conflicts", khFile, fp, ErrPinConflict},
		{"missing known_hosts", filepath.Join(t.TempDir(), "absent"), "", ErrKnownHostsPath},
		{"fingerprint bad base64", "", "SHA256:not base64 %%%", ErrBadFingerprint},
		{"fingerprint wrong length", "", "SHA256:" + base64.RawStdEncoding.EncodeToString([]byte("short")), ErrBadFingerprint},
		{"fingerprint md5 form", "", "MD5:aa:bb:cc:dd", ErrBadFingerprint},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := baseValid()
			cfg.KnownHostsPath = c.kh
			cfg.HostFingerprint = c.fp
			err := cfg.Validate()
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
		})
	}
}

// TestValidateAdminUserDefaultApplied confirms an empty AdminUser is resolved to
// vpsadmin by Validate (the default still wins, then passes the charset check).
func TestValidateAdminUserDefaultApplied(t *testing.T) {
	cfg := baseValid()
	cfg.AdminUser = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.AdminUser != "vpsadmin" {
		t.Fatalf("AdminUser = %q, want vpsadmin", cfg.AdminUser)
	}
}
