package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewNoFileWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	l := New("") // empty path => no file
	l.Info("hello")
	l.Close()
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("expected no files, got %d", len(ents))
	}
}

func TestIsBenignNoise(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{
			name: "ubuntu 26.04 rust-coreutils LD_PRELOAD chatter",
			line: "ERROR: ld.so: object '/usr/libexec/rust-coreutils/libstdbuf.so' from LD_PRELOAD cannot be preloaded (cannot open shared object file): ignored.",
			want: true,
		},
		{
			name: "real error is not benign",
			line: "ERROR: something failed",
			want: false,
		},
		{
			name: "ignored. but no LD_PRELOAD is not benign",
			line: "warning: signal ignored.",
			want: false,
		},
		{
			name: "LD_PRELOAD but not the ignored shape is not benign",
			line: "LD_PRELOAD=/bad/path cannot open shared object file",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBenignNoise(tc.line); got != tc.want {
				t.Fatalf("isBenignNoise(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestNewWritesFileWhenPathGiven(t *testing.T) {
	p := filepath.Join(t.TempDir(), "run.log")
	l := New(p)
	l.Info("hello")
	l.Close()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("log file not written: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("log file empty")
	}
}
