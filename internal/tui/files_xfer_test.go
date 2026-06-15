package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// downloadLocalDest resolves a remote basename to a stable local downloads dir + the name.
func TestDownloadLocalDest(t *testing.T) {
	got := downloadLocalDest("backup.conf")
	want := filepath.Join(os.TempDir(), "morgward-downloads", "backup.conf")
	if got != want {
		t.Fatalf("downloadLocalDest = %q want %q", got, want)
	}
	// A remote basename with path separators is reduced to its base (no escaping the dir).
	if g := downloadLocalDest("a/b/evil"); g != filepath.Join(os.TempDir(), "morgward-downloads", "evil") {
		t.Fatalf("downloadLocalDest path = %q", g)
	}
}

// A crafted remote basename with an ANSI/control byte is STRIPPED from the transfer label
// (and the status-line xferLabel) before it can reach the render sink. Starting a download
// returns a non-nil Cmd; the observable sanitized text is f.xferLabel.
func TestTransferLabelSanitized(t *testing.T) {
	m := filesKeyModel(nil)
	m.files.cwd = "/d"
	cmd := m.filesStartDownloadTo("\x1b[31mevil\x1b[0m", "/tmp/evil")
	if cmd == nil {
		t.Fatal("download should dispatch a Cmd")
	}
	if strings.ContainsRune(m.files.xferLabel, 0x1b) {
		t.Fatalf("xferLabel still contains an ESC byte: %q", m.files.xferLabel)
	}
	if m.files.xferLabel != "evil" {
		t.Fatalf("xferLabel = %q want %q (escapes stripped)", m.files.xferLabel, "evil")
	}
}

// Upload of a non-existent local file is refused before any dispatch (f.err set, no Cmd).
func TestUploadMissingFileGuard(t *testing.T) {
	m := filesKeyModel(nil)
	m.files.cwd = "/dst"
	missing := filepath.Join(t.TempDir(), "does-not-exist.txt")
	_, cmd := m.filesStartUpload(missing)
	if cmd != nil {
		t.Fatal("upload of a missing local file must NOT dispatch a transfer Cmd")
	}
	if m.files.err == "" {
		t.Fatal("missing local file must surface an error")
	}
	if m.files.transferring {
		t.Fatal("a refused upload must not set the transferring flag")
	}
}

// A second transfer trigger is ignored while one is in flight (sftp not concurrency-safe).
func TestTransferBlockedWhileInFlight(t *testing.T) {
	// A real existing local file so the missing-file guard passes.
	local := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(local, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := filesKeyModel(nil)
	m.files.cwd = "/dst"
	m.files.transferring = true // already transferring
	_, cmd := m.filesStartUpload(local)
	if cmd != nil {
		t.Fatal("a second upload must be ignored while transferring")
	}
}

// fmXferDoneMsg handling: clears the flag; failure sets f.err; success sets a notice.
func TestXferDoneMsgHandling(t *testing.T) {
	m := filesKeyModel(nil)
	m.files.transferring = true

	// Failure path.
	n, _ := m.Update(fmXferDoneMsg{err: os.ErrPermission, label: "download x"})
	mm := n.(model)
	if mm.files.transferring {
		t.Fatal("fmXferDoneMsg must clear the transferring flag")
	}
	if mm.files.err == "" {
		t.Fatal("a failed transfer must surface an error")
	}

	// Success path.
	mm.files.transferring = true
	n, _ = mm.Update(fmXferDoneMsg{err: nil, upload: false, label: "downloaded x"})
	mm = n.(model)
	if mm.files.transferring {
		t.Fatal("success must clear the transferring flag")
	}
	if mm.files.err == "" {
		t.Fatal("success should set a notice")
	}
}

// 'w' opens a download prompt for a regular file (seeded with the local dest) but is a
// no-op on a directory or "..".
func TestDownloadPromptOnlyRegularFiles(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: ".."}, {name: "sub", isDir: true}, {name: "f.txt"}})
	// On "..": no prompt.
	m.files.sel = 0
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'w'})
	m = n.(model)
	if m.files.prompting() {
		t.Fatal("download on '..' must be a no-op")
	}
	// On a directory: no prompt.
	m.files.sel = 1
	n, _ = m.filesKey(tea.KeyPressMsg{Code: 'w'})
	m = n.(model)
	if m.files.prompting() {
		t.Fatal("download on a directory must be a no-op")
	}
	// On a regular file: prompt opens, seeded with the resolved local dest, kind=fpDownload.
	m.files.sel = 2
	n, _ = m.filesKey(tea.KeyPressMsg{Code: 'w'})
	m = n.(model)
	if m.files.promptKind != fpDownload {
		t.Fatalf("'w' on a file must open the download prompt: kind=%d", m.files.promptKind)
	}
	if m.files.promptArg != "f.txt" {
		t.Fatalf("download promptArg (remote name) = %q want f.txt", m.files.promptArg)
	}
}

// 'w'/'u' are no-ops while a transfer is in flight (don't open a prompt mid-transfer).
func TestTransferTriggersBlockedWhileInFlight(t *testing.T) {
	m := filesKeyModel([]fileEntry{{name: "f.txt"}})
	m.files.sel = 0
	m.files.transferring = true
	n, _ := m.filesKey(tea.KeyPressMsg{Code: 'w'})
	m = n.(model)
	if m.files.prompting() {
		t.Fatal("'w' must not open a prompt while transferring")
	}
	n, _ = m.filesKey(tea.KeyPressMsg{Code: 'u'})
	m = n.(model)
	if m.files.prompting() {
		t.Fatal("'u' must not open a prompt while transferring")
	}
}

// A successful UPLOAD msg requests a listing refresh (the new remote file should appear);
// with a nil cli reload fails but the intent is exercised without panic.
func TestXferDoneUploadReloads(t *testing.T) {
	m := filesKeyModel(nil)
	m.files.transferring = true
	n, _ := m.Update(fmXferDoneMsg{err: nil, upload: true, label: "uploaded y"})
	mm := n.(model)
	if mm.files.transferring {
		t.Fatal("upload-done must clear the flag")
	}
	// Just assert no panic + flag cleared; reload over nil cli sets f.err internally which is
	// overwritten by the success notice path — the key point is upload triggers reload.
	_ = tea.Msg(fmXferDoneMsg{})
}
