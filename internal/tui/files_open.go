package tui

import (
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/ui"
)

// fmOpenDoneMsg is posted when the async open-download finishes. On success Update launches
// the editor + registers the watch; on error it surfaces inline.
type fmOpenDoneMsg struct {
	remotePath string
	localPath  string
	mtime      int64
	data0      []byte // first ~8 KiB for the binary heuristic
	err        error
}

// fmWatchEventMsg carries one fsnotify write event (local temp path) from the pump goroutine.
type fmWatchEventMsg struct{ localPath string }

// fmWatchFlushMsg fires after the debounce window; seq lets Update drop a superseded flush.
type fmWatchFlushMsg struct {
	localPath string
	seq       int
}

func (f *fileSession) registerOpened(remotePath, localPath string, mtime int64) *openedFile {
	if f.opened == nil {
		f.opened = map[string]*openedFile{}
	}
	if of, ok := f.opened[localPath]; ok {
		of.remoteMtime = mtime // fresh download → new conflict baseline
		of.remotePath = remotePath
		return of
	}
	of := &openedFile{remotePath: remotePath, localPath: localPath, remoteMtime: mtime}
	f.opened[localPath] = of
	return of
}

// scheduleFlush advances the debounce seq (session + per-file) and returns the new seq; the
// caller emits a tea.Tick carrying it. Only the flush whose seq still matches of.seq acts.
func (f *fileSession) scheduleFlush(of *openedFile) int {
	f.watchSeq++
	of.seq = f.watchSeq
	of.dirty = true
	return of.seq
}

func (f *fileSession) isLatestFlush(of *openedFile, seq int) bool {
	return of != nil && of.seq == seq
}

// filesOpenLocal starts opening the remote entry `name` (in cwd) in the host editor. Callers
// (filesActivateSelected / the "O" op / fmActOpen / the menu) gate on a regular file (not a
// dir, not ".."); this is a no-op while a transfer is in flight (sftp client busy). Marks the
// session transferring and returns the async open-download Cmd (mirrors filesStartDownloadTo).
func (m model) filesOpenLocal(name string) (tea.Model, tea.Cmd) {
	f := m.files
	if f.transferring {
		return m, nil
	}
	remote := joinPath(f.cwd, name)
	safe := ui.StripControlAndANSI(name)
	f.transferring = true
	f.xferLabel = safe
	f.err = ""
	return m, m.openDownloadCmd(remote, openLocalDest(remote), safe)
}

// openDownloadCmd downloads remote→local AND stats the remote mtime + samples the head bytes,
// race-free (captures only locals, owns its sftp client; never derefs f). Posts fmOpenDoneMsg.
func (m model) openDownloadCmd(remotePath, localPath, label string) tea.Cmd {
	cli := m.files.cli
	return func() tea.Msg {
		if err := os.MkdirAll(openTempDir(), 0o700); err != nil {
			return fmOpenDoneMsg{remotePath: remotePath, localPath: localPath, err: err}
		}
		sc, err := cli.SFTP()
		if err != nil {
			return fmOpenDoneMsg{remotePath: remotePath, localPath: localPath, err: err}
		}
		defer func() { _ = sc.Close() }()
		if err := sshx.DownloadFile(sc, remotePath, localPath); err != nil {
			return fmOpenDoneMsg{remotePath: remotePath, localPath: localPath, err: err}
		}
		var mtime int64
		if st, e := sc.Stat(remotePath); e == nil {
			mtime = st.ModTime().Unix()
		}
		head := make([]byte, 8192)
		if lf, e := os.Open(localPath); e == nil { // #nosec G304 -- localPath is our temp dest
			n, _ := lf.Read(head)
			head = head[:n]
			_ = lf.Close()
		} else {
			head = nil
		}
		return fmOpenDoneMsg{remotePath: remotePath, localPath: localPath, mtime: mtime, data0: head}
	}
}
