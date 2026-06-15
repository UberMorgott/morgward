package tui

import "testing"

func TestShQuote(t *testing.T) {
	if got := shQuote("a b'c"); got != `'a b'\''c'` {
		t.Fatalf("shQuote = %s", got)
	}
	if got := shQuote("plain"); got != `'plain'` {
		t.Fatalf("shQuote = %s", got)
	}
}

func TestListCmd(t *testing.T) {
	if got := listCmd("/var/log"); got != `LC_ALL=C ls -la --time-style=long-iso -- '/var/log'` {
		t.Fatalf("listCmd = %s", got)
	}
}

func TestJoinPath(t *testing.T) {
	for _, c := range []struct{ dir, name, want string }{
		{"/", "etc", "/etc"},
		{"/etc", "nginx", "/etc/nginx"},
		{"/etc/", "nginx", "/etc/nginx"},
	} {
		if got := joinPath(c.dir, c.name); got != c.want {
			t.Errorf("joinPath(%q,%q)=%q want %q", c.dir, c.name, got, c.want)
		}
	}
}

func TestParentPath(t *testing.T) {
	for _, c := range []struct{ dir, want string }{
		{"/etc/nginx", "/etc"},
		{"/etc", "/"},
		{"/", "/"},
	} {
		if got := parentPath(c.dir); got != c.want {
			t.Errorf("parentPath(%q)=%q want %q", c.dir, c.want, got)
		}
	}
}

func TestApplyListingClampsSel(t *testing.T) {
	f := newFileSession(nil, "/", langRU)
	f.sel = 99
	f.applyListing("total 0\n-rw-r--r-- 1 r r 5 2026-06-09 09:20 a\n")
	if len(f.entry) != 1 || f.sel != 0 {
		t.Fatalf("applyListing bad: entry=%d sel=%d", len(f.entry), f.sel)
	}
}

// Hidden-file filtering keeps the visible index space consistent: dotfiles are hidden by
// default (".." always shown); sel and the visible slice agree.
func TestVisibleEntriesHidesDotfiles(t *testing.T) {
	f := newFileSession(nil, "/", langRU)
	f.applyListing("total 0\n" +
		"drwxr-xr-x 2 r r 4096 2026-06-09 09:20 ..\n" +
		"-rw-r--r-- 1 r r    5 2026-06-09 09:20 .hidden\n" +
		"-rw-r--r-- 1 r r    5 2026-06-09 09:20 visible\n")
	vis := f.visibleEntries()
	if len(vis) != 2 || vis[0].name != ".." || vis[1].name != "visible" {
		t.Fatalf("default hides dotfiles but keeps ..: got %+v", vis)
	}
	f.showHidden = true
	if got := len(f.visibleEntries()); got != 3 {
		t.Fatalf("showHidden must reveal dotfiles: visible=%d want 3", got)
	}
}

// navigateTo does the pure path-math + sel/scroll reset (the reload is a separate step),
// so the cwd mutation on entering a directory is unit-testable without SSH.
func TestNavigateToDir(t *testing.T) {
	f := newFileSession(nil, "/etc", langRU)
	f.sel = 4
	f.scroll = 3
	f.navigateTo("nginx")
	if f.cwd != "/etc/nginx" || f.sel != 0 || f.scroll != 0 {
		t.Fatalf("navigateTo(dir): cwd=%q sel=%d scroll=%d", f.cwd, f.sel, f.scroll)
	}
	f.navigateTo("..")
	if f.cwd != "/etc" {
		t.Fatalf("navigateTo(..): cwd=%q want /etc", f.cwd)
	}
}

// navigateAndReload restores the previous cwd when the reload fails, so a bad typed path
// (or any unreachable dir) never leaves cwd pointing at a non-existent directory with a
// stale listing. A nil-cli session makes reload fail deterministically without SSH.
func TestNavigateAndReloadRevertsOnFailure(t *testing.T) {
	f := newFileSession(nil, "/etc", langRU) // nil cli → reload() always fails
	f.sel, f.scroll = 4, 3
	f.navigateAndReload("/no/such/dir")
	if f.cwd != "/etc" {
		t.Fatalf("failed nav must restore cwd: cwd=%q want /etc", f.cwd)
	}
	if f.err == "" {
		t.Fatal("the reload error must stay surfaced in f.err after revert")
	}
}

// moveSel clamps within the visible slice (dotfiles hidden), proving sel indexes the
// visible space the view/hit-test use.
func TestMoveSelClampsToVisible(t *testing.T) {
	f := newFileSession(nil, "/", langRU)
	f.applyListing("total 0\n" +
		"drwxr-xr-x 2 r r 4096 2026-06-09 09:20 ..\n" +
		"-rw-r--r-- 1 r r    5 2026-06-09 09:20 .dot\n" +
		"-rw-r--r-- 1 r r    5 2026-06-09 09:20 a\n" +
		"-rw-r--r-- 1 r r    5 2026-06-09 09:20 b\n")
	// visible = [.., a, b] (3 rows); sel starts at 0.
	f.moveSel(-1)
	if f.sel != 0 {
		t.Fatalf("moveSel up at top: sel=%d want 0", f.sel)
	}
	f.moveSel(10)
	if f.sel != 2 {
		t.Fatalf("moveSel down past end: sel=%d want 2 (last visible)", f.sel)
	}
}
