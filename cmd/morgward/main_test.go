package main

import (
	"flag"
	"reflect"
	"strings"
	"testing"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"github.com/UberMorgott/morgward/internal/config"
)

// TestUsageDocumentsUpdate asserts the CLI `update` self-update command is
// documented in the usage/help text so operators can discover it.
func TestUsageDocumentsUpdate(t *testing.T) {
	if !strings.Contains(usage, "update") {
		t.Fatalf("usage string does not document the `update` command:\n%s", usage)
	}
}

// TestNewUpdaterHasChecksumValidator confirms self-update is wired with a SHA-256
// ChecksumValidator (F01): without it go-selfupdate would apply an unverified
// binary. The validator field on Updater is unexported, so we assert the gate the
// other way — building the same Config and checking the Validator is a
// ChecksumValidator pointed at checksums.txt, the goreleaser-default asset name.
func TestNewUpdaterHasChecksumValidator(t *testing.T) {
	if checksumsFile != "checksums.txt" {
		t.Fatalf("checksumsFile = %q, want goreleaser default checksums.txt", checksumsFile)
	}

	up, err := newUpdater()
	if err != nil {
		t.Fatalf("newUpdater() error: %v", err)
	}
	if up == nil {
		t.Fatal("newUpdater() returned nil updater")
	}

	cv, ok := newUpdaterConfig().Validator.(*selfupdate.ChecksumValidator)
	if !ok {
		t.Fatalf("validator type = %T, want *selfupdate.ChecksumValidator", newUpdaterConfig().Validator)
	}
	if cv.UniqueFilename != checksumsFile {
		t.Fatalf("validator UniqueFilename = %q, want %q", cv.UniqueFilename, checksumsFile)
	}
}

// TestBindFlagsHostKeyPin confirms the FA-0010 pin flags are wired into the config
// (parsed into KnownHostsPath / HostFingerprint) and documented in usage.
func TestBindFlagsHostKeyPin(t *testing.T) {
	cfg := &config.Config{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	bindFlags(fs, cfg)
	if err := fs.Parse([]string{"--known-hosts", "kh.txt", "--host-fingerprint", "SHA256:abc"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.KnownHostsPath != "kh.txt" {
		t.Errorf("KnownHostsPath = %q, want kh.txt", cfg.KnownHostsPath)
	}
	if cfg.HostFingerprint != "SHA256:abc" {
		t.Errorf("HostFingerprint = %q, want SHA256:abc", cfg.HostFingerprint)
	}
	if !strings.Contains(usage, "--known-hosts") || !strings.Contains(usage, "--host-fingerprint") {
		t.Error("usage does not document the host-key pin flags")
	}
}

// TestParseArgs proves flags work BEFORE, AFTER or interleaved with the step IDs,
// and that value-taking flags absorb their following token so it is never mistaken
// for a step ID. Which flags take a value comes from the FlagSet bindFlags fills,
// so adding a flag can no longer make its value leak into the step IDs.
func TestParseArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantPos []string
		wantCfg config.Config // only the fields a case sets are asserted
	}{
		{
			name:    "flags after step ids",
			args:    []string{"A4", "A6.5", "--host", "1.2.3.4", "--user", "root", "--assume-yes"},
			wantPos: []string{"A4", "A6.5"},
			wantCfg: config.Config{Host: "1.2.3.4", User: "root", Assume: true},
		},
		{
			name:    "flags before step ids",
			args:    []string{"--host", "1.2.3.4", "--user", "root", "--assume-yes", "A4", "A6.5"},
			wantPos: []string{"A4", "A6.5"},
			wantCfg: config.Config{Host: "1.2.3.4", User: "root", Assume: true},
		},
		{
			name:    "interleaved",
			args:    []string{"A4", "--host", "1.2.3.4", "A6.5", "--assume-yes"},
			wantPos: []string{"A4", "A6.5"},
			wantCfg: config.Config{Host: "1.2.3.4", Assume: true},
		},
		{
			name:    "equals form keeps value attached",
			args:    []string{"--host=1.2.3.4", "A4"},
			wantPos: []string{"A4"},
			wantCfg: config.Config{Host: "1.2.3.4"},
		},
		{
			name:    "value that looks like an id is not a step id",
			args:    []string{"--admin-user", "A4", "A5"},
			wantPos: []string{"A5"},
			wantCfg: config.Config{AdminUser: "A4"},
		},
		{
			name:    "no positionals",
			args:    []string{"--host", "1.2.3.4", "--assume-yes"},
			wantPos: nil,
			wantCfg: config.Config{Host: "1.2.3.4", Assume: true},
		},
		{
			name:    "known-hosts value absorbs its token after a step id",
			args:    []string{"A1", "--known-hosts", "kh.txt", "A5"},
			wantPos: []string{"A1", "A5"},
			wantCfg: config.Config{KnownHostsPath: "kh.txt"},
		},
		{
			name:    "host-fingerprint value absorbs its token",
			args:    []string{"--host-fingerprint", "SHA256:abc", "A1"},
			wantPos: []string{"A1"},
			wantCfg: config.Config{HostFingerprint: "SHA256:abc"},
		},
		{
			name:    "bool flag between two step ids consumes no token",
			args:    []string{"A4", "--assume-yes", "A5"},
			wantPos: []string{"A4", "A5"},
			wantCfg: config.Config{Assume: true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.Config{}
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			bindFlags(fs, cfg)
			gotPos, err := parseArgs(fs, c.args)
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}
			if !reflect.DeepEqual(gotPos, c.wantPos) {
				t.Errorf("positional = %v, want %v", gotPos, c.wantPos)
			}
			// Unset fields fall back to the bindFlags defaults.
			want := c.wantCfg
			if want.User == "" {
				want.User = "root"
			}
			if want.AdminUser == "" {
				want.AdminUser = "vpsadmin"
			}
			if cfg.Host != want.Host || cfg.User != want.User ||
				cfg.AdminUser != want.AdminUser || cfg.Assume != want.Assume ||
				cfg.KnownHostsPath != want.KnownHostsPath ||
				cfg.HostFingerprint != want.HostFingerprint {
				t.Errorf("cfg = %+v, want %+v", cfg, want)
			}
		})
	}
}
