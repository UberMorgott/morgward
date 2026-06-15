package tui

import (
	"charm.land/bubbles/v2/textinput"
	"github.com/pkg/sftp"

	"github.com/UberMorgott/morgward/internal/sshx"
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

// fileSession is the NON-COPYABLE state of the Files tab: the (lazily-opened) sftp client
// plus the browse state (cwd, listing, selection, scroll, clipboard, last error). It is
// held by the model behind a POINTER so the value-copied model stays copyable (same gotcha
// as termSession). The SSH client is SHARED with the terminal (dialed once by the
// workspace) and is NOT owned here — close() never closes cli, only the sftp client.
type fileSession struct {
	cli   *sshx.Client // shared transport (owned by the workspace, not by fileSession)
	sftp  *sftp.Client // lazily opened on first byte transfer; nil until ensureSFTP
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
// "/root" when empty). The listing is loaded later by the caller (a navigate/refresh op).
func newFileSession(cli *sshx.Client, cwd string) *fileSession {
	if cwd == "" {
		cwd = "/root"
	}
	ti := textinput.New()
	ti.SetValue(cwd)
	ti.CharLimit = 4096 // PATH_MAX is 4096 on Linux
	return &fileSession{cli: cli, cwd: cwd, addr: ti}
}

// ensureSFTP opens the sftp subsystem on first use and caches it. Safe to call repeatedly.
func (f *fileSession) ensureSFTP() error {
	if f.sftp != nil {
		return nil
	}
	sc, err := f.cli.SFTP()
	if err != nil {
		return err
	}
	f.sftp = sc
	return nil
}

// close releases the sftp client if it was opened. It does NOT close f.cli — the workspace
// owns that transport. Idempotent and nil-safe.
func (f *fileSession) close() {
	if f == nil {
		return
	}
	if f.sftp != nil {
		_ = f.sftp.Close()
		f.sftp = nil
	}
}
