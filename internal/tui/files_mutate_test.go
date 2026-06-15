package tui

import "testing"

func TestMkdirCmd(t *testing.T) {
	if got := mkdirCmd("/etc/nginx", "new dir"); got != `mkdir -p -- '/etc/nginx/new dir'` {
		t.Fatalf("mkdirCmd = %s", got)
	}
}

func TestTouchCmd(t *testing.T) {
	if got := touchCmd("/etc", "a.conf"); got != `touch -- '/etc/a.conf'` {
		t.Fatalf("touchCmd = %s", got)
	}
}

func TestRenameCmd(t *testing.T) {
	if got := renameCmd("/d", "a", "b c"); got != `mv -- '/d/a' '/d/b c'` {
		t.Fatalf("renameCmd = %s", got)
	}
}

func TestRmCmd(t *testing.T) {
	if got := rmCmd("/tmp/x y"); got != `rm -rf -- '/tmp/x y'` {
		t.Fatalf("rmCmd = %s", got)
	}
}

func TestCpCmd(t *testing.T) {
	if got := cpCmd("/a/file", "/b dir"); got != `cp -a -- '/a/file' '/b dir/'` {
		t.Fatalf("cpCmd = %s", got)
	}
}

func TestMvCmd(t *testing.T) {
	if got := mvCmd("/a/file", "/b dir"); got != `mv -- '/a/file' '/b dir/'` {
		t.Fatalf("mvCmd = %s", got)
	}
}

func TestChmodCmd(t *testing.T) {
	if got := chmodCmd("/f x", "644"); got != `chmod -- '644' '/f x'` {
		t.Fatalf("chmodCmd = %s", got)
	}
}

func TestChownCmd(t *testing.T) {
	if got := chownCmd("/f x", "root:root"); got != `chown -- 'root:root' '/f x'` {
		t.Fatalf("chownCmd = %s", got)
	}
}

func TestStatCmd(t *testing.T) {
	if got := statCmd("/f x"); got != `stat -c '%A %U:%G %s %y %F' -- '/f x'` {
		t.Fatalf("statCmd = %s", got)
	}
}

// A name with an embedded single quote must keep the POSIX escape intact end-to-end
// through the path build (joinPath) and the command builder.
func TestMutateCmdEscapesEmbeddedQuote(t *testing.T) {
	if got := mkdirCmd("/d", "a'b"); got != `mkdir -p -- '/d/a'\''b'` {
		t.Fatalf("mkdirCmd with quote = %s", got)
	}
	if got := rmCmd(joinPath("/d", "a'b")); got != `rm -rf -- '/d/a'\''b'` {
		t.Fatalf("rmCmd with quote = %s", got)
	}
}

// Opening a prompt seeds it and suppresses listing keys; Esc cancels back to fpNone.
func TestPromptOpenAndCancel(t *testing.T) {
	f := newFileSession(nil, "/etc")
	f.openPrompt(fpRename, "seed name", "Rename to:")
	if f.promptKind != fpRename {
		t.Fatalf("openPrompt did not set kind: %d", f.promptKind)
	}
	if f.prompt.Value() != "seed name" {
		t.Fatalf("prompt not seeded: %q", f.prompt.Value())
	}
	if f.promptMsg != "Rename to:" {
		t.Fatalf("promptMsg = %q", f.promptMsg)
	}
	if !f.prompting() {
		t.Fatal("prompting() must be true while a prompt is open")
	}
	f.cancelPrompt()
	if f.promptKind != fpNone || f.prompting() {
		t.Fatalf("cancelPrompt did not reset: kind=%d", f.promptKind)
	}
}

// selectedEntry returns the visible entry under sel (ok=false when out of range or ".."
// for ops that must not act on the parent marker — selectedName guards ".." for mutating).
func TestSelectedName(t *testing.T) {
	f := newFileSession(nil, "/")
	f.applyListing("total 0\n" +
		"drwxr-xr-x 2 r r 4096 2026-06-09 09:20 ..\n" +
		"-rw-r--r-- 1 r r    5 2026-06-09 09:20 a\n")
	f.sel = 0 // ".."
	if _, ok := f.selectedName(); ok {
		t.Fatal("selectedName must reject the '..' parent marker for mutations")
	}
	f.sel = 1 // "a"
	if name, ok := f.selectedName(); !ok || name != "a" {
		t.Fatalf("selectedName(a) = %q,%v want a,true", name, ok)
	}
}
