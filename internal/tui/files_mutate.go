package tui

import (
	"strings"

	"github.com/atotto/clipboard"

	"github.com/UberMorgott/morgward/internal/ui"
)

// --- pure command builders ----------------------------------------------------
//
// Every remote path/name flows through shQuote (T6a) before it is spliced into a command
// string, so a name with spaces, quotes, $, ;, backticks, etc. cannot break out of its
// quoting. Targets are assembled with joinPath (forward-slash, Linux remote) then quoted as
// a single unit — never raw string concatenation into the command.

// mkdirCmd builds `mkdir -p -- '<dir>/<name>'` (parents created; no error if it exists).
func mkdirCmd(dir, name string) string {
	return "mkdir -p -- " + shQuote(joinPath(dir, name))
}

// touchCmd builds `touch -- '<dir>/<name>'` (creates an empty file / updates mtime).
func touchCmd(dir, name string) string {
	return "touch -- " + shQuote(joinPath(dir, name))
}

// renameCmd builds `mv -- '<dir>/<old>' '<dir>/<new>'` (rename within the same dir).
func renameCmd(dir, oldName, newName string) string {
	return "mv -- " + shQuote(joinPath(dir, oldName)) + " " + shQuote(joinPath(dir, newName))
}

// rmCmd builds `rm -rf -- '<path>'` (recursive force delete — path is the full target).
func rmCmd(path string) string {
	return "rm -rf -- " + shQuote(path)
}

// cpCmd builds `cp -a -- '<src>' '<dstDir>/'` (archive copy INTO the destination dir; the
// trailing slash makes cp treat dst as a directory).
func cpCmd(src, dstDir string) string {
	return "cp -a -- " + shQuote(src) + " " + shQuote(dstDir+"/")
}

// mvCmd builds `mv -- '<src>' '<dstDir>/'` (move INTO the destination dir).
func mvCmd(src, dstDir string) string {
	return "mv -- " + shQuote(src) + " " + shQuote(dstDir+"/")
}

// chmodCmd builds `chmod -- '<mode>' '<path>'`. The mode is operator-typed, so it is quoted
// too (never trusted into the command bare).
func chmodCmd(path, mode string) string {
	return "chmod -- " + shQuote(mode) + " " + shQuote(path)
}

// chownCmd builds `chown -- '<spec>' '<path>'`. The spec (user[:group]) is operator-typed
// and quoted.
func chownCmd(path, spec string) string {
	return "chown -- " + shQuote(spec) + " " + shQuote(path)
}

// statCmd builds `stat -c '%A %U:%G %s %y %F' -- '<path>'`. The format is a fixed literal we
// control (no user data), single-quoted so the shell passes it to stat verbatim.
func statCmd(path string) string {
	return "stat -c '%A %U:%G %s %y %F' -- " + shQuote(path)
}

// --- selection helpers --------------------------------------------------------

// selectedName returns the name of the entry currently under sel, rejecting the ".." parent
// marker (ok=false) so a MUTATING op never targets the parent directory by accident. Returns
// ok=false when sel is out of range or no listing is loaded.
func (f *fileSession) selectedName() (string, bool) {
	vis := f.visibleEntries()
	if f.sel < 0 || f.sel >= len(vis) {
		return "", false
	}
	name := vis[f.sel].name
	if name == ".." {
		return "", false
	}
	return name, true
}

// selectedEntry returns the full entry under sel (incl ".."), ok=false when out of range.
func (f *fileSession) selectedEntry() (fileEntry, bool) {
	vis := f.visibleEntries()
	if f.sel < 0 || f.sel >= len(vis) {
		return fileEntry{}, false
	}
	return vis[f.sel], true
}

// hasEntry reports whether the CURRENT (full) listing already contains an entry named name
// — used to gate an overwrite-on-paste behind a confirm.
func (f *fileSession) hasEntry(name string) bool {
	for _, e := range f.entry {
		if e.name == name {
			return true
		}
	}
	return false
}

// --- operation runners --------------------------------------------------------
//
// Each runner builds its command, runs it over the shared transport, and on a non-zero exit
// records the stderr in f.err (NEVER panics, NEVER aborts the run); on success it reloads to
// refresh the listing. reload itself surfaces its own errors.

// runMutation runs a single mutating command, surfacing a non-zero exit's stderr in f.err
// and reloading on success. Centralizes the run/error/reload pattern so every op is uniform.
func (f *fileSession) runMutation(cmd string) {
	if f == nil || f.cli == nil {
		f.setErr("no connection")
		return
	}
	r := f.cli.Run(cmd)
	if r.Err != nil {
		f.err = r.Err.Error()
		return
	}
	if r.RC != 0 {
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(r.Stdout)
		}
		if msg == "" {
			msg = "operation failed"
		}
		f.err = msg
		return
	}
	f.err = ""
	_ = f.reload()
}

// setErr is a nil-safe error setter (f may be nil on a dial-failed workspace).
func (f *fileSession) setErr(s string) {
	if f != nil {
		f.err = s
	}
}

// opNewDir creates a subdirectory `name` in cwd.
func (f *fileSession) opNewDir(name string) { f.runMutation(mkdirCmd(f.cwd, name)) }

// opNewFile creates an empty file `name` in cwd.
func (f *fileSession) opNewFile(name string) { f.runMutation(touchCmd(f.cwd, name)) }

// opRename renames oldName → newName within cwd.
func (f *fileSession) opRename(oldName, newName string) {
	f.runMutation(renameCmd(f.cwd, oldName, newName))
}

// opDelete removes cwd/name (recursive force). Gated upstream behind a yes/no confirm.
func (f *fileSession) opDelete(name string) { f.runMutation(rmCmd(joinPath(f.cwd, name))) }

// opChmod / opChown apply the operator-typed mode/spec to cwd/name.
func (f *fileSession) opChmod(name, mode string) {
	f.runMutation(chmodCmd(joinPath(f.cwd, name), mode))
}
func (f *fileSession) opChown(name, spec string) {
	f.runMutation(chownCmd(joinPath(f.cwd, name), spec))
}

// opPaste copies (or moves, when the clip was a cut) the clipboard path INTO cwd, reloads,
// and clears the clip after a successful cut. No-op when the clipboard is empty.
func (f *fileSession) opPaste() {
	if f.clip.path == "" {
		return
	}
	cmd := cpCmd(f.clip.path, f.cwd)
	if f.clip.cut {
		cmd = mvCmd(f.clip.path, f.cwd)
	}
	f.runMutation(cmd)
	if f.err == "" && f.clip.cut {
		f.clip = fileClip{} // a moved item's source is gone — clear the clipboard
	}
}

// clipBaseName is the last path element of the clipboard entry — used to detect an
// overwrite when pasting into cwd (a same-named entry already present).
func (f *fileSession) clipBaseName() string {
	p := strings.TrimRight(f.clip.path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// opProperties runs stat on cwd/name and surfaces the result as a read-only NOTICE (reusing
// f.err's inline surface — it is info, not an error). Single line, trimmed.
func (f *fileSession) opProperties(name string) {
	if f == nil || f.cli == nil {
		f.setErr("no connection")
		return
	}
	r := f.cli.Run(statCmd(joinPath(f.cwd, name)))
	if r.Err != nil {
		f.err = r.Err.Error()
		return
	}
	if r.RC != 0 {
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = "stat failed"
		}
		f.err = msg
		return
	}
	// stat stdout is untrusted remote output — strip ANSI/control bytes before it reaches
	// the status-line render sink (project invariant).
	f.err = name + ": " + ui.StripControlAndANSI(strings.TrimSpace(r.Stdout))
}

// opCopyPath writes the absolute path of cwd/name to the SYSTEM clipboard (host-side) and
// reports success/failure in the inline notice.
func (f *fileSession) opCopyPath(name string) {
	p := joinPath(f.cwd, name)
	if err := clipboard.WriteAll(p); err != nil {
		f.err = "clipboard: " + err.Error()
		return
	}
	f.err = "copied path: " + p
}
