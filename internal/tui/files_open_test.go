package tui

import "testing"

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
