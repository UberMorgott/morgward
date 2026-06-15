package tui

import (
	"fmt"
	"strings"
)

// Deferred FM features (intentionally NOT implemented in 2b — absent by design, not bugs):
//   - multi-select (operations act on the single selected entry only)
//   - search / filter (no in-listing find; only the '.' hidden-file toggle)
//   - sort (entries render in the server's `ls` order)
//   - symlink creation (no `ln`; existing symlinks are shown + followed on navigate)
//   - archive / extract (no tar/zip create or unpack)
//   - directory disk-usage (`du`) totals
//   - in-TUI Open (local-edit-and-sync) — this is the separate 2c task; the menu's "Open"
//     item is present-but-disabled until then.
//
// shQuote wraps s in single quotes for safe shell interpolation, replacing each embedded
// single quote with the standard POSIX escape sequence: close-quote, a backslash-escaped
// quote, then re-open (see the ReplaceAll below for the exact bytes). EVERY remote
// path/name MUST go through this before it is spliced into a command string, so a name
// containing spaces, quotes, dollar signs, semicolons, or other metacharacters cannot
// break out of its quoting.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// listCmd builds the machine-friendly directory listing command: C locale (stable field
// formatting), long-iso timestamps (date + " " + time, parseListing's expected shape),
// and `--` so a dir starting with '-' is not mistaken for a flag. dir is shQuote'd.
func listCmd(dir string) string {
	return "LC_ALL=C ls -la --time-style=long-iso -- " + shQuote(dir)
}

// joinPath joins a remote dir and an entry name into a clean absolute path. The remote is
// ALWAYS Linux (forward slashes), regardless of the Windows build host — so this does NOT
// use path/filepath (which would emit '\' on windows). It collapses a trailing slash on
// dir so joinPath("/etc/","nginx") == "/etc/nginx".
func joinPath(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return strings.TrimRight(dir, "/") + "/" + name
}

// parentPath returns dir's parent directory; "/" (and any root-level dir) collapses to
// "/". Forward-slash only (remote Linux), independent of the host OS.
func parentPath(dir string) string {
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return "/"
	}
	i := strings.LastIndex(dir, "/")
	if i <= 0 {
		return "/"
	}
	return dir[:i]
}

// visibleEntries returns the listing rows currently shown: the full entry slice when
// showHidden, else dotfiles filtered out (".." is ALWAYS kept so the user can always go
// up). sel/scroll, the view, and the click hit-test all index THIS slice, so the index
// space is consistent everywhere. Nil-safe.
func (f *fileSession) visibleEntries() []fileEntry {
	if f == nil {
		return nil
	}
	if f.showHidden {
		return f.entry
	}
	vis := make([]fileEntry, 0, len(f.entry))
	for _, e := range f.entry {
		if e.name != ".." && strings.HasPrefix(e.name, ".") {
			continue
		}
		vis = append(vis, e)
	}
	return vis
}

// applyListing parses out into f.entry (the FULL list) and clamps f.sel/f.scroll into the
// VISIBLE range. Pure given out (no SSH) — unit-tested directly; reload wraps it with the
// SSH fetch.
func (f *fileSession) applyListing(out string) {
	f.entry = parseListing(out)
	f.clampSel()
}

// clampSel bounds sel and scroll to the current visible-entry count: sel into
// [0, nVisible-1] (0 when empty) and scroll never negative. Called after any change to the
// listing or the hidden-file filter so the selection never points off the end.
func (f *fileSession) clampSel() {
	n := len(f.visibleEntries())
	if n == 0 {
		f.sel, f.scroll = 0, 0
		return
	}
	if f.sel < 0 {
		f.sel = 0
	}
	if f.sel >= n {
		f.sel = n - 1
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
}

// reload re-lists f.cwd over SSH and replaces the listing. A blocking Run is intentional
// (a single ls is tens of ms; mirrors openTerminal's synchronous Dial). On a non-zero exit
// (or transport error) it records the stderr in f.err and LEAVES the old listing intact;
// on success it clears f.err and applies the new listing. Returns a non-nil error only on
// a transport failure (so the caller can distinguish "box gone" from "no such dir").
func (f *fileSession) reload() error {
	if f == nil {
		return fmt.Errorf("no connection")
	}
	if f.cli == nil {
		// Surface it in f.err too (consistent with the exit/transport branches below) so a
		// caller guarding on f.err — e.g. navigateAndReload's revert — sees the failure. The
		// returned error is internal (not a render sink); f.err is the user-visible one.
		f.err = t(f.lang, kFmErrNoConn)
		return fmt.Errorf("no connection")
	}
	r := f.cli.Run(listCmd(f.cwd))
	if r.Err != nil {
		f.err = r.Err.Error()
		return r.Err
	}
	if r.RC != 0 {
		// Non-zero exit (e.g. permission denied / no such directory): keep the old listing,
		// surface the reason. Not a transport error, so return nil.
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = fmt.Sprintf("ls exited %d", r.RC)
		}
		f.err = msg
		return nil
	}
	f.err = ""
	f.applyListing(r.Stdout)
	return nil
}

// navigateTo changes cwd to the directory entry `name` (joinPath for a normal entry, the
// parent for ".."), resetting sel/scroll to the top. PURE path math — the caller runs
// reload() afterwards. Unit-testable without SSH.
func (f *fileSession) navigateTo(name string) {
	if name == ".." {
		f.cwd = parentPath(f.cwd)
	} else {
		f.cwd = joinPath(f.cwd, name)
	}
	f.sel, f.scroll = 0, 0
}

// navigateAndReload changes cwd to dest, resets sel/scroll, and reloads the listing —
// REVERTING cwd to the previous (last-good) directory if the reload fails. Without the
// revert a bad destination (a typed path that doesn't exist, an unreachable dir) would
// leave cwd pointing at the bad path with a stale listing, and every later reload / ".."
// would then operate off that bad path. The reload error stays surfaced in f.err so the
// user sees WHY the move didn't happen. This is the single nav seam all cwd-changing
// paths funnel through (address-bar typed path, backspace/.., entry activate).
func (f *fileSession) navigateAndReload(dest string) {
	prev := f.cwd
	f.cwd = dest
	f.sel, f.scroll = 0, 0
	_ = f.reload()
	if f.err != "" {
		f.cwd = prev // failed nav: restore the last-good dir (f.err keeps the reason)
	}
}

// moveSel shifts the selection by delta, clamped to the visible-entry range, and keeps the
// selected row inside the scroll window (mirroring the other scroll screens). Pure.
func (f *fileSession) moveSel(delta int) {
	n := len(f.visibleEntries())
	if n == 0 {
		f.sel, f.scroll = 0, 0
		return
	}
	f.sel += delta
	if f.sel < 0 {
		f.sel = 0
	}
	if f.sel >= n {
		f.sel = n - 1
	}
}

// keepSelVisible adjusts f.scroll so the selected row is within the viewport of height
// viewH (scroll up when sel is above the window, down when below). Called from the key
// handler which knows the current viewport height.
func (f *fileSession) keepSelVisible(viewH int) {
	if viewH < 1 {
		viewH = 1
	}
	if f.sel < f.scroll {
		f.scroll = f.sel
	}
	if f.sel >= f.scroll+viewH {
		f.scroll = f.sel - viewH + 1
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
}
