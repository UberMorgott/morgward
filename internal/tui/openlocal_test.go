package tui

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOSOpenArgs_CurrentOS(t *testing.T) {
	name, args := osOpenArgs(`C:\tmp\a b.txt`)
	if name == "" {
		t.Fatal("osOpenArgs returned empty command name")
	}
	// The target path must appear as its OWN arg element (never concatenated into a
	// shell string) so spaces / metacharacters in the path can't be reinterpreted.
	found := false
	for _, a := range args {
		if a == `C:\tmp\a b.txt` {
			found = true
		}
	}
	if !found {
		t.Fatalf("path must be a discrete arg; got name=%q args=%v", name, args)
	}
	switch runtime.GOOS {
	case "windows":
		// cmd /c start "" <path>  — the empty "" is the title arg (start treats a quoted
		// first token as the window title, so a real title placeholder is required).
		if name != "cmd" || args[0] != "/c" || args[1] != "start" || args[2] != "" {
			t.Fatalf("windows opener wrong: name=%q args=%v", name, args)
		}
	case "darwin":
		if name != "open" {
			t.Fatalf("darwin opener wrong: name=%q", name)
		}
	default:
		if name != "xdg-open" {
			t.Fatalf("unix opener wrong: name=%q", name)
		}
	}
}

func TestIsBinary(t *testing.T) {
	if isBinary([]byte("hello\nworld\n")) {
		t.Fatal("plain text misdetected as binary")
	}
	if !isBinary([]byte{'a', 'b', 0x00, 'c'}) {
		t.Fatal("NUL byte must mark binary")
	}
	if isBinary(nil) {
		t.Fatal("empty content is not binary")
	}
}

func TestOpenLocalDest(t *testing.T) {
	d1 := openLocalDest("/etc/nginx/nginx.conf")
	d2 := openLocalDest("/opt/app/nginx.conf") // same base, different dir → must differ
	if filepath.Dir(d1) != openTempDir() {
		t.Fatalf("dest not under temp dir: %q", d1)
	}
	if !strings.HasSuffix(d1, "nginx.conf") {
		t.Fatalf("dest must keep the basename: %q", d1)
	}
	if d1 == d2 {
		t.Fatal("same basename in different remote dirs must map to distinct local temps")
	}
	if d1 != openLocalDest("/etc/nginx/nginx.conf") {
		t.Fatal("openLocalDest must be deterministic for the same remote path")
	}
}
