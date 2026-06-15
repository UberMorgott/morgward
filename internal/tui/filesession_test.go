package tui

import "testing"

func TestNewFileSession(t *testing.T) {
	f := newFileSession(nil, "/etc", langRU)
	if f.cwd != "/etc" || f.sel != 0 || len(f.entry) != 0 {
		t.Fatalf("bad init: %+v", f)
	}
	f.close() // sftp nil → must not panic
}

func TestNewFileSessionDefaultCwd(t *testing.T) {
	if f := newFileSession(nil, "", langRU); f.cwd != "/root" {
		t.Fatalf("empty cwd should default to /root, got %q", f.cwd)
	}
}

func TestWSTabDefault(t *testing.T) {
	if (model{}).wsTab != wsTerminal {
		t.Fatal("zero-value wsTab must be wsTerminal")
	}
}

func TestFileSessionCloseNilSafe(t *testing.T) {
	var f *fileSession
	f.close() // must not panic
}
