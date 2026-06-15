package tui

import (
	"os"
	"time"

	"charm.land/bubbles/v2/textinput"
	"github.com/fsnotify/fsnotify"

	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/ui"
)

// wsTab selects which tab of the terminal workspace is shown. The terminal session and
// the file session both run in the background regardless; wsTab only changes what is
// RENDERED. Plain int — value-copy safe on the model.
type wsTab int

const (
	wsTerminal wsTab = iota // the interactive shell (default)
	wsFiles                 // the remote file manager
)

// fileClip is the FM copy/cut clipboard: one absolute remote path, cut=true means a Paste
// MOVES it (mv), cut=false COPIES it (cp).
type fileClip struct {
	path string
	cut  bool
}

// fileSession is the NON-COPYABLE state of the Files tab: the browse state (cwd, listing,
// selection, scroll, clipboard, last error, address/prompt inputs). It is held by the model
// behind a POINTER so the value-copied model stays copyable (same gotcha as termSession).
// The SSH client is SHARED with the terminal (dialed once by the workspace) and is NOT owned
// here — close() never closes cli. The session does NOT cache an sftp client: each async
// transfer (files_xfer.go) opens + closes its OWN sftp client inside its goroutine, so there
// is no shared sftp state to race on or to use-after-close across a teardown.
type fileSession struct {
	cli *sshx.Client // shared transport (owned by the workspace, not by fileSession)
	// lang mirrors the model's active language so the session's own op error/notice
	// fallbacks (produced in fileSession methods that have no model) are localized. Seeded
	// in newFileSession and refreshed by model.toggleLang.
	lang  Lang
	cwd   string
	entry []fileEntry // FULL current directory listing (unfiltered)
	// showHidden controls whether dotfiles appear; when false they are filtered out of
	// visibleEntries (".." is always kept). sel/scroll and the view/hit-test all index the
	// VISIBLE slice (visibleEntries), so toggling this re-renders without reload and keeps a
	// single consistent index space.
	showHidden bool
	sel        int // selected row index into visibleEntries()
	scroll     int // listing scroll offset (into the visible slice)
	clip       fileClip
	err        string // last operation error, surfaced inline in the Files view

	// addr is the editable address bar (seeded to cwd). addrFocus gates whether keystrokes
	// edit the path (true) or drive the listing (false); ':' or '/' focuses it, Enter
	// navigates to the typed path, Esc cancels. A textinput.Model is value-copyable, so it
	// safely rides on the pointer-held session.
	addr      textinput.Model
	addrFocus bool

	// Prompt sub-state for the mutating ops that need a value (new name, rename, chmod mode,
	// chown spec) or a yes/no (delete, overwrite-on-paste). While promptKind != fpNone every
	// keystroke routes to the prompt handler (listing keys suppressed) — same gate as
	// addrFocus. prompt is the inline input; promptMsg is the localized label; promptArg
	// carries the target name (e.g. the entry being renamed/deleted) into the confirm.
	promptKind fmPromptKind
	prompt     textinput.Model
	promptMsg  string
	promptArg  string

	// transferring is true while an ASYNC sftp Download/Upload is in flight. A SECOND transfer
	// is gated out (one sftp transfer at a time) until the in-flight one posts its
	// fmXferDoneMsg (which clears this). Navigation (reload→cli.Run) and synchronous mutations
	// are deliberately NOT blocked during a transfer and may safely OVERLAP it: each opens its
	// OWN ssh channel (Run = a fresh session, the transfer = its own sftp channel) on the
	// concurrency-safe underlying *ssh.Client, so they don't share state and can't race.
	// xferLabel is the entry name shown in the "transferring …" notice.
	transferring bool
	xferLabel    string

	// Context-menu sub-state. menuOpen gates the key router (checked BEFORE prompt/addr/
	// listing, same pattern). menuItems is built for the selected entry on open; menuSel is
	// the highlighted item (always on an ENABLED item); menuRow/menuCol record the anchor
	// where it was triggered (kept for a future cursor-anchored popup — v1 renders centered).
	menuOpen         bool
	menuItems        []fmMenuItem
	menuSel          int
	menuRow, menuCol int

	// --- 2c local-open-and-sync (main-loop-only state; see files_open.go) ---
	// opened maps a LOCAL temp path → the open record. A file opened-for-edit is downloaded
	// to openLocalDest, launched in the host editor, and watched; on a debounced save it is
	// uploaded back to of.remotePath. Keyed by local path because that is what the fsnotify
	// event carries.
	opened map[string]*openedFile
	// watcher is the single lazily-created fsnotify watcher for this session; watchCh is the
	// channel its pump goroutine posts local-path events on (drained by listenWatch). Both nil
	// until the first Open. Held here (pointer session) so the value-copied model stays copyable.
	watcher  *fsnotify.Watcher
	watchCh  chan string
	watchSeq int // monotonic debounce sequence; a flush acts only if it is still the latest

	// Double-click tracking for the listing (main-loop-only; set/read in update.go's
	// MouseClickMsg Files branch). A LEFT click on the SAME row within the double-click
	// window activates the entry (dir → navigate, file → open), same as Enter.
	lastClickRow int
	lastClickAt  time.Time
}

// openedFile tracks one remote file opened in the local editor. remoteMtime is the remote
// mtime (unix seconds) captured at download time; the upload-back re-stats and refuses to
// clobber silently if it changed (conflict). seq is the debounce generation for THIS file.
type openedFile struct {
	remotePath  string
	localPath   string
	remoteMtime int64
	seq         int // last scheduled debounce tick for this file
	dirty       bool
}

// fmMenuItem is one context-menu row. key is the SAME single-char shortcut its keyboard
// dispatch uses (e.g. "r" for Rename), so picking it synthesizes that keypress and routes
// through the existing op handler — no duplicated op logic. A disabled item (e.g. Paste
// with an empty clipboard, or a mutation on "..") is dimmed and unselectable.
type fmMenuItem struct {
	label   string
	key     string
	enabled bool
}

// fmPromptKind selects which mutating op the inline prompt/confirm is currently driving.
type fmPromptKind int

const (
	fpNone          fmPromptKind = iota // no prompt active
	fpNewDir                            // text: new folder name
	fpNewFile                           // text: new file name
	fpRename                            // text: new name (seeded to the old)
	fpChmod                             // text: octal/symbolic mode
	fpChown                             // text: user[:group]
	fpConfirmDelete                     // yes/no: delete promptArg
	fpConfirmPaste                      // yes/no: overwrite an existing name on paste
	fpDownload                          // text: local dest path (download the selected file)
	fpUpload                            // text: local source path (upload into cwd)
)

// prompting reports whether an inline prompt/confirm is currently open (gates the key
// router + the view's prompt line). Nil-safe.
func (f *fileSession) prompting() bool {
	return f != nil && f.promptKind != fpNone
}

// isConfirm reports whether the active prompt is a yes/no confirm (vs a text-entry prompt).
func (f *fileSession) isConfirm() bool {
	return f.promptKind == fpConfirmDelete || f.promptKind == fpConfirmPaste
}

// openPrompt opens a TEXT prompt of the given kind, seeding the input to seed (e.g. the
// current name for a rename), focusing it, and recording the localized label. promptArg is
// set to seed (the rename case wants the OLD name as the dispatch target); ops that need a
// distinct target (chmod/chown act on the SELECTED entry, not on the typed value) set
// promptArg explicitly after this call.
func (f *fileSession) openPrompt(kind fmPromptKind, seed, msg string) {
	f.promptKind = kind
	f.promptMsg = msg
	f.promptArg = seed
	ti := textinput.New()
	ti.SetValue(seed)
	ti.CharLimit = 4096
	ti.Focus()
	ti.CursorEnd()
	f.prompt = ti
}

// openPromptFor opens a text prompt (empty input) whose dispatch target is `arg` — for
// chmod/chown, which act on the selected entry while the typed value is the mode/spec.
func (f *fileSession) openPromptFor(kind fmPromptKind, arg, msg string) {
	f.openPrompt(kind, "", msg)
	f.promptArg = arg
}

// openConfirm opens a yes/no confirm of the given kind (no text input), with the target
// name stashed in promptArg and the localized prompt in promptMsg.
func (f *fileSession) openConfirm(kind fmPromptKind, arg, msg string) {
	f.promptKind = kind
	f.promptMsg = msg
	f.promptArg = arg
}

// cancelPrompt closes any open prompt/confirm and blurs the input.
func (f *fileSession) cancelPrompt() {
	f.promptKind = fpNone
	f.promptMsg = ""
	f.promptArg = ""
	f.prompt.Blur()
}

// newFileSession builds a Files session over the shared client, rooted at cwd (defaults to
// "/root" when empty). lang seeds the session's own notice localization (kept in sync by
// model.toggleLang). The listing is loaded later by the caller (a navigate/refresh op).
func newFileSession(cli *sshx.Client, cwd string, lang Lang) *fileSession {
	if cwd == "" {
		cwd = "/root"
	}
	ti := textinput.New()
	ti.SetValue(cwd)
	ti.CharLimit = 4096 // PATH_MAX is 4096 on Linux
	return &fileSession{cli: cli, cwd: cwd, addr: ti, lang: lang}
}

// close tears down the Files session. It owns no sftp client (transfers own theirs) and does
// NOT close f.cli — the workspace owns that transport. It DOES own the 2c open-sync watcher
// and the opened-file temps: closing the watcher stops its pump goroutine (the watchCh range
// ends), and the temp files downloaded for editing are removed best-effort.
func (f *fileSession) close() {
	if f == nil {
		return
	}
	if f.watcher != nil {
		_ = f.watcher.Close() // stops the pump goroutine (watchCh range ends)
		f.watcher = nil
	}
	for local := range f.opened {
		_ = os.Remove(local) // best-effort temp cleanup
	}
	f.opened = nil
}

// setErr stores an inline error/notice, stripping untrusted remote control/ANSI bytes
// before it reaches the status-line render sink. Remote stderr/error text routinely echoes
// the (attacker-controllable) remote filename — e.g. "rm: cannot remove '<name>'" — so every
// error sink must funnel through here (the project invariant: all untrusted remote output
// passes ui.StripControlAndANSI before any render). f.err is rendered via truncDisplay,
// which does NOT itself strip control bytes, so the sanitization must happen here.
func (f *fileSession) setErr(s string) { f.err = ui.StripControlAndANSI(s) }
