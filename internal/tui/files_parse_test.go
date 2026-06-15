package tui

import (
	"strings"
	"testing"
)

// Remote filenames are untrusted: an embedded ANSI escape / control byte must be STRIPPED
// from the parsed name (and symlink target) at the source, so it can never reach a render
// sink (the listing, or a notice that splices the name).
func TestParseListingSanitizesName(t *testing.T) {
	out := "-rw-r--r-- 1 r r 5 2026-06-09 09:20 \x1b[31mevil\x1b[0m\n"
	got := parseListing(out)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if strings.ContainsRune(got[0].name, 0x1b) {
		t.Fatalf("name still contains an ESC byte: %q", got[0].name)
	}
	if got[0].name != "evil" {
		t.Fatalf("name = %q want %q (escapes stripped)", got[0].name, "evil")
	}
	// A symlink target with an escape is sanitized too.
	out2 := "lrwxrwxrwx 1 r r 7 2026-06-09 09:20 link -> \x1b[31m/etc/x\n"
	g2 := parseListing(out2)
	if len(g2) != 1 || strings.ContainsRune(g2[0].target, 0x1b) {
		t.Fatalf("target not sanitized: %+v", g2)
	}
}

func TestParseListing(t *testing.T) {
	out := "total 12\n" +
		"drwxr-xr-x 3 root root 4096 2026-06-09 09:20 .\n" +
		"drwxr-xr-x 5 root root 4096 2026-06-01 10:00 ..\n" +
		"drwxr-xr-x 2 root root 4096 2026-06-09 09:20 snippets\n" +
		"-rw-r--r-- 1 root root  512 2026-06-09 09:20 backup.conf\n" +
		"lrwxrwxrwx 1 root root    7 2026-06-02 11:00 link -> /etc/hi\n"
	got := parseListing(out)
	if len(got) != 5 {
		t.Fatalf("want 5 entries, got %d", len(got))
	}
	dir := got[2]
	if dir.name != "snippets" || !dir.isDir || dir.mode != "rwxr-xr-x" {
		t.Errorf("snippets parsed wrong: %+v", dir)
	}
	f := got[3]
	if f.name != "backup.conf" || f.isDir || f.size != 512 || f.mtime != "2026-06-09 09:20" {
		t.Errorf("backup.conf parsed wrong: %+v", f)
	}
	ln := got[4]
	if ln.name != "link" || !ln.isLink || ln.target != "/etc/hi" {
		t.Errorf("symlink parsed wrong: %+v", ln)
	}
}

func TestParseListingSkipsTotalAndBlank(t *testing.T) {
	if got := parseListing("total 0\n\n"); len(got) != 0 {
		t.Fatalf("want 0 entries, got %d", len(got))
	}
}

func TestParseListingNameWithSpaces(t *testing.T) {
	out := "-rw-r--r-- 1 root root 5 2026-06-09 09:20 my file.txt\n"
	got := parseListing(out)
	if len(got) != 1 || got[0].name != "my file.txt" {
		t.Fatalf("space-name parsed wrong: %+v", got)
	}
}
