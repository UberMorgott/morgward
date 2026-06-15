package tui

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"

	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/ui"
)

// fmSaveDebounce is the quiet-period after a local save before the upload-back fires, so a
// burst of editor writes (write-truncate-write, swap-file dance) collapses to one upload.
const fmSaveDebounce = 600 * time.Millisecond

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

// ensureWatcher lazily creates the session's fsnotify watcher + event channel and starts the
// pump goroutine. The pump reads fsnotify events and forwards ONLY the local path of write/
// create events on watchCh; it derefs NOTHING on *fileSession (captures the watcher + channel
// as locals). On watcher Close the Events channel drains+closes → the pump closes watchCh so
// listenWatch retires. Returns false if the watcher could not be created.
func (f *fileSession) ensureWatcher() bool {
	if f.watcher != nil {
		return true
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return false
	}
	ch := make(chan string, 16)
	f.watcher = w
	f.watchCh = ch
	go func() {
		defer close(ch)
		for ev := range w.Events {
			// Editors save via write OR atomic rename-into-place; treat write/create as a save.
			if ev.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				select {
				case ch <- ev.Name:
				default: // channel full → a later event still triggers a flush; drop is safe
				}
			}
		}
	}()
	return true
}

// listenWatch blocks on the next watcher event (re-issued by Update after each). Nil channel
// (no Open yet) or a closed channel (teardown) → nil, retiring the listener.
func (m model) listenWatch() tea.Cmd {
	ch := m.files.watchCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		local, ok := <-ch
		if !ok {
			return nil
		}
		return fmWatchEventMsg{localPath: local}
	}
}

// uploadBackCmd pushes the edited local temp back to its remote path, race-free (owns its sftp
// client, captures locals). force skips the conflict re-check. Posts fmXferDoneMsg (reuses the
// existing transfer-done handler — upload:false so it does NOT reload the listing, the file's
// content changed not the directory). On a successful upload it re-stats the remote mtime and
// returns it via syncLocal/newMtime so the handler re-stamps of.remoteMtime (next save won't
// false-conflict against the now-stale baseline).
func (m model) uploadBackCmd(localPath, remotePath, label string, expectMtime int64, force bool) tea.Cmd {
	cli := m.files.cli
	return func() tea.Msg {
		sc, err := cli.SFTP()
		if err != nil {
			return fmXferDoneMsg{err: err, label: label}
		}
		defer func() { _ = sc.Close() }()
		if !force {
			if st, e := sc.Stat(remotePath); e == nil && st.ModTime().Unix() != expectMtime {
				return fmXferDoneMsg{err: errRemoteChanged, label: label}
			}
		}
		if err := sshx.UploadFile(sc, localPath, remotePath); err != nil {
			return fmXferDoneMsg{err: err, upload: false, label: label}
		}
		var newMtime int64
		if st, e := sc.Stat(remotePath); e == nil {
			newMtime = st.ModTime().Unix()
		}
		return fmXferDoneMsg{upload: false, label: label, syncLocal: localPath, newMtime: newMtime}
	}
}

// errRemoteChanged signals an upload-back conflict (remote mtime moved since download).
var errRemoteChanged = sftpConflictErr("remote file changed since it was opened")

type sftpConflictErr string

func (e sftpConflictErr) Error() string { return string(e) }
