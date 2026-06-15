package tui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/ui"
)

// fmXferDoneMsg is posted (from the transfer goroutine the tea.Cmd runs in) when an async
// Download/Upload finishes. upload distinguishes the direction (an upload refreshes the
// listing so the new remote file appears); label is the human notice text.
type fmXferDoneMsg struct {
	err    error
	upload bool
	label  string

	// 2c open-sync re-stamp thread: an upload-back (uploadBackCmd) sets syncLocal to the LOCAL
	// temp path of the opened file and newMtime to the remote mtime re-stat'd AFTER a successful
	// upload. The success handler then re-stamps opened[syncLocal].remoteMtime = newMtime so the
	// next save doesn't false-conflict against the now-stale baseline. The Download/Upload
	// callers (downloadCmd/uploadCmd) leave these zero — they are ignored unless syncLocal != "".
	syncLocal string
	newMtime  int64
}

// downloadsDir is the stable host directory downloads land in (created on demand).
func downloadsDir() string {
	return filepath.Join(os.TempDir(), "morgward-downloads")
}

// downloadLocalDest resolves a remote basename to its local download path: the downloads
// dir + the BASE of the name (filepath.Base prevents a crafted remote name with separators
// from escaping the downloads dir). Uses path/filepath — this is the LOCAL (host) side, so
// host separators are correct (the remote path stays forward-slash via joinPath).
func downloadLocalDest(remoteName string) string {
	return filepath.Join(downloadsDir(), filepath.Base(remoteName))
}

// downloadCmd builds the async transfer Cmd for a download. Bubble Tea runs each Cmd in its
// own goroutine; to stay race-free the goroutine captures ONLY plain locals (cli/remotePath/
// localPath/label) and touches NOTHING on the fileSession — it opens its OWN sftp client (a
// new channel multiplexed on the same *ssh.Client) and closes it when done. f.cli is set once
// in newFileSession and never mutated, so reading it here (in the main loop, before launch)
// is safe. If teardown closes the underlying transport mid-transfer, io.Copy returns a
// transport error that flows back in the msg — no panic, no shared-state race. The result is
// an fmXferDoneMsg handled in Update.
func (m model) downloadCmd(remotePath, localPath, label string) tea.Cmd {
	cli := m.files.cli // plain local — never deref f inside the goroutine
	return func() tea.Msg {
		if err := os.MkdirAll(downloadsDir(), 0o755); err != nil {
			return fmXferDoneMsg{err: err, label: label}
		}
		sc, err := cli.SFTP() // the goroutine's OWN sftp client
		if err != nil {
			return fmXferDoneMsg{err: err, label: label}
		}
		defer func() { _ = sc.Close() }()
		err = sshx.DownloadFile(sc, remotePath, localPath)
		return fmXferDoneMsg{err: err, upload: false, label: label}
	}
}

// uploadCmd builds the async transfer Cmd for an upload (local → remote). Same race-free
// shape as downloadCmd (captures only locals, owns its sftp client); upload=true so Update
// reloads the listing afterward.
func (m model) uploadCmd(localPath, remotePath, label string) tea.Cmd {
	cli := m.files.cli // plain local — never deref f inside the goroutine
	return func() tea.Msg {
		sc, err := cli.SFTP()
		if err != nil {
			return fmXferDoneMsg{err: err, upload: true, label: label}
		}
		defer func() { _ = sc.Close() }()
		err = sshx.UploadFile(sc, localPath, remotePath)
		return fmXferDoneMsg{err: err, upload: true, label: label}
	}
}

// filesStartDownloadTo begins a download of the remote entry `name` (in cwd) to the
// operator-chosen local path, marking the session transferring and returning the async Cmd.
// A no-op (no Cmd) when a transfer is already in flight (sftp client is not
// concurrency-safe).
func (m model) filesStartDownloadTo(name, localPath string) tea.Cmd {
	f := m.files
	if f.transferring {
		return nil
	}
	remote := joinPath(f.cwd, name)
	// name is the REMOTE basename (untrusted) — strip ANSI/control bytes before it lands in
	// the label, which reaches the status-line render sink via fmXferDoneMsg (project policy).
	safeName := ui.StripControlAndANSI(name)
	f.transferring = true
	f.xferLabel = safeName
	f.err = ""
	label := t(m.lang, kFmDownloaded) + " " + safeName + " → " + localPath
	return m.downloadCmd(remote, localPath, label)
}

// filesStartUpload begins an upload of the local file at localPath into cwd. It refuses
// (no Cmd, error surfaced) when a transfer is in flight or the local file is missing /
// is a directory. The remote target is cwd + the local BASE name.
func (m model) filesStartUpload(localPath string) (tea.Model, tea.Cmd) {
	f := m.files
	if f.transferring {
		return m, nil
	}
	info, err := os.Stat(localPath)
	if err != nil {
		f.err = fmt.Sprintf("%s: %s", t(m.lang, kFmUploadNoFile), filepath.Base(localPath))
		return m, nil
	}
	if info.IsDir() {
		f.err = t(m.lang, kFmUploadNoFile) + ": " + filepath.Base(localPath)
		return m, nil
	}
	base := filepath.Base(localPath)
	remote := joinPath(f.cwd, base)
	// base is host-derived (operator-typed path), but sanitize for consistency/defense before
	// it reaches the status-line sink via the label.
	safeBase := ui.StripControlAndANSI(base)
	f.transferring = true
	f.xferLabel = safeBase
	f.err = ""
	label := t(m.lang, kFmUploaded) + " " + safeBase
	return m, m.uploadCmd(localPath, remote, label)
}
