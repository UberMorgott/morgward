package config

import (
	"errors"
	"testing"
)

// baseValid returns a Config that passes every check except whatever the caller
// mutates, so a test can isolate a single field's validation.
func baseValid() *Config {
	return &Config{
		Host:      "1.2.3.4",
		User:      "root",
		Password:  "pw",
		Mode:      ModeSoft,
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
