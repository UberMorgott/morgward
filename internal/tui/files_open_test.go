package tui

import (
	"errors"
	"testing"
)

// onOpenDownloaded registers a freshly downloaded file; a second open of the same remote
// path reuses the record (no duplicate). Pure registry behavior.
func TestRegisterOpened(t *testing.T) {
	f := &fileSession{}
	of := f.registerOpened("/etc/hosts", openLocalDest("/etc/hosts"), 1000)
	if f.opened[of.localPath] != of {
		t.Fatal("registerOpened must index by local path")
	}
	again := f.registerOpened("/etc/hosts", of.localPath, 2000)
	if again != of {
		t.Fatal("re-open of same local path must reuse the record")
	}
	if again.remoteMtime != 2000 {
		t.Fatal("re-open must refresh remoteMtime (fresh download baseline)")
	}
}

// markDirtyAndSchedule bumps the per-file + session debounce seq; isLatestFlush only the
// newest tick wins (debounce).
func TestDebounceSeq(t *testing.T) {
	f := &fileSession{}
	of := f.registerOpened("/a", "/tmp/h-a", 0)
	s1 := f.scheduleFlush(of)
	s2 := f.scheduleFlush(of) // a second save before the first flush fires
	if s1 == s2 {
		t.Fatal("each schedule must advance the seq")
	}
	if f.isLatestFlush(of, s1) {
		t.Fatal("a stale (superseded) flush must not act")
	}
	if !f.isLatestFlush(of, s2) {
		t.Fatal("the latest flush must act")
	}
}

// stubOpenLocal swaps the OS-opener for the duration of a test so handlers don't spawn a
// real editor/cmd process. It restores the original on cleanup and records the last path.
func stubOpenLocal(t *testing.T, err error) *string {
	t.Helper()
	prev := openLocalFileFn
	var last string
	openLocalFileFn = func(p string) error {
		last = p
		return err
	}
	t.Cleanup(func() { openLocalFileFn = prev })
	return &last
}

// An open-download that ERRORED clears transferring and surfaces f.err; nothing is registered.
func TestOpenDoneError(t *testing.T) {
	_ = stubOpenLocal(t, nil) // not reached on the error path, but guard against accidental spawn
	m := filesKeyModel(nil)
	m.files.transferring = true
	n, _ := m.Update(fmOpenDoneMsg{remotePath: "/etc/x", localPath: "/tmp/h-x", err: errors.New("boom")})
	mm := n.(model)
	if mm.files.transferring {
		t.Fatal("open-done error must clear transferring")
	}
	if mm.files.err == "" {
		t.Fatal("open-done error must surface f.err")
	}
	if mm.files.opened["/tmp/h-x"] != nil {
		t.Fatal("a failed open must not register the file")
	}
}

// An open-done SUCCESS with NUL bytes in data0 warns binary but still registers the file.
func TestOpenDoneBinaryWarns(t *testing.T) {
	opened := stubOpenLocal(t, nil)
	m := filesKeyModel(nil)
	m.files.transferring = true
	n, _ := m.Update(fmOpenDoneMsg{remotePath: "/bin/x", localPath: "/tmp/h-x", mtime: 5, data0: []byte{0x00}})
	mm := n.(model)
	if mm.files.transferring {
		t.Fatal("open-done must clear transferring")
	}
	if mm.files.opened["/tmp/h-x"] == nil {
		t.Fatal("binary file must still be registered (opened anyway)")
	}
	if mm.files.err == "" {
		t.Fatal("binary file must surface a warning notice")
	}
	if *opened != "/tmp/h-x" {
		t.Fatalf("the editor must be launched for the local temp; got %q", *opened)
	}
}

// A watch event for a known opened file schedules a flush (seq bumps) and returns a non-nil Cmd.
func TestWatchEventSchedulesFlush(t *testing.T) {
	m := filesKeyModel(nil)
	of := m.files.registerOpened("/etc/a", "/tmp/h-a", 0)
	seqBefore := of.seq
	n, cmd := m.Update(fmWatchEventMsg{localPath: "/tmp/h-a"})
	mm := n.(model)
	if cmd == nil {
		t.Fatal("a watch event for a known file must return a non-nil Cmd (listener + flush tick)")
	}
	if mm.files.opened["/tmp/h-a"].seq == seqBefore {
		t.Fatal("a watch event must advance the debounce seq")
	}
	if !mm.files.opened["/tmp/h-a"].dirty {
		t.Fatal("a watch event must mark the file dirty")
	}
}

// A superseded (stale-seq) flush does NOT start an upload; the latest one does.
func TestFlushDebounce(t *testing.T) {
	m := filesKeyModel(nil)
	of := m.files.registerOpened("/etc/a", "/tmp/h-a", 0)
	stale := m.files.scheduleFlush(of)  // first save
	latest := m.files.scheduleFlush(of) // second save supersedes the first

	// Stale flush: superseded → no upload, transferring stays false.
	n, _ := m.Update(fmWatchFlushMsg{localPath: "/tmp/h-a", seq: stale})
	mm := n.(model)
	if mm.files.transferring {
		t.Fatal("a stale flush must NOT start an upload")
	}

	// Latest flush: acts → upload starts (transferring set, non-nil Cmd).
	n, cmd := mm.Update(fmWatchFlushMsg{localPath: "/tmp/h-a", seq: latest})
	mm = n.(model)
	if !mm.files.transferring {
		t.Fatal("the latest flush must start an upload (set transferring)")
	}
	if cmd == nil {
		t.Fatal("the latest flush must return the upload Cmd")
	}
}
