package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/selfupdate"
)

// TestUsageDocumentsUpdate asserts the CLI `update` self-update command is
// documented in the usage/help text so operators can discover it.
func TestUsageDocumentsUpdate(t *testing.T) {
	if !strings.Contains(usage, "update") {
		t.Fatalf("usage string does not document the `update` command:\n%s", usage)
	}
}

// TestCleanupOldBinarySweepsUpdateLeftover covers the Windows ".old" sweep: after
// a self-update the replaced binary is parked at selfupdate.OldPath (it stays
// locked while the updating process runs, so only a later launch can delete it),
// and every launch must clear it. Asserted against selfupdate.OldPath so the sweep
// and the writer cannot drift apart, and on the literal ".<base>.old" name so a
// leftover from a pre-migration build is still swept.
func TestCleanupOldBinarySweepsUpdateLeftover(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable: %v", err)
	}
	old := selfupdate.OldPath(exe)
	if want := filepath.Join(filepath.Dir(exe), "."+filepath.Base(exe)+".old"); old != want {
		t.Fatalf("OldPath(%q) = %q, want %q", exe, old, want)
	}
	if _, err := os.Stat(old); err == nil {
		t.Skipf("%s already exists — refusing to delete a real leftover", old)
	}
	if err := os.WriteFile(old, []byte("previous version"), 0o600); err != nil {
		t.Skipf("cannot stage a leftover next to the test binary: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(old) })

	cleanupOldBinary()

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("cleanupOldBinary left %s behind (stat err = %v)", old, err)
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
