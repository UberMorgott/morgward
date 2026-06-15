package tui

import (
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
	cli    *sshx.Client // shared transport (owned by the workspace, not by fileSession)
	sftp   *sftp.Client // lazily opened on first byte transfer; nil until ensureSFTP
	cwd    string
	entry  []fileEntry // current directory listing
	sel    int         // selected row index into entry
	scroll int         // listing scroll offset
	clip   fileClip
	err    string // last operation error, surfaced inline in the Files view
}

// newFileSession builds a Files session over the shared client, rooted at cwd (defaults to
// "/root" when empty). The listing is loaded later by the caller (a navigate/refresh op).
func newFileSession(cli *sshx.Client, cwd string) *fileSession {
	if cwd == "" {
		cwd = "/root"
	}
	return &fileSession{cli: cli, cwd: cwd}
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
