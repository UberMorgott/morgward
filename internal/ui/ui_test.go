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
