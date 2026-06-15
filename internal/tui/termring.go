package tui

// termRing is a bounded, append-only byte ring that retains the MOST-RECENT `cap`
// bytes of everything written through it. It backs reflow-by-replay (terminal phase
// 2a): the raw PTY byte stream is mirrored here so a resize can rebuild a fresh vt
// emulator at the new geometry and REPLAY the stream through it — giving true rewrap
// where vt.SafeEmulator.Resize would merely truncate.
//
// Bounded so a long-lived session can't grow it without limit; on overflow the OLDEST
// bytes are dropped. NOTE: a drop can split an escape sequence at the cut point, so
// the very oldest reflowed line may render with a cosmetic glitch (a stray byte or a
// dropped SGR attribute). This is acceptable — only the eldest survivor line is at
// risk, the rest of the replay is byte-faithful, and the bound (termRingCap) is large
// enough that real screens + a healthy scrollback fit comfortably.
//
// NOT concurrency-safe on its own — termOut owns one and guards every access with its
// mutex (the same mutex that serializes ring-append vs emulator-swap), so the ring
// never needs its own lock.
type termRing struct {
	buf  []byte // backing store, len == capacity once filled
	size int    // bytes currently valid (≤ cap(buf))
	cap  int    // configured capacity
}

// termRingCap bounds the replay ring at ~1 MB. Comfortably holds a full screen plus a
// deep scrollback of raw bytes; on a box that streams more than this between resizes,
// only the oldest bytes (already scrolled far off) are dropped.
const termRingCap = 1 << 20 // 1 MiB

// newTermRing returns a ring bounded at the given capacity (clamped to ≥1).
func newTermRing(capacity int) *termRing {
	if capacity < 1 {
		capacity = 1
	}
	return &termRing{cap: capacity}
}

// append records p into the ring, dropping the oldest bytes if it would overflow. The
// ring is stored contiguously (oldest→newest) so bytes() is a cheap copy with no
// wrap-stitching; appends amortize fine for the streaming case (full writes are
// screen-sized, far under cap, so the common path just appends and occasionally
// re-slices the head off).
func (r *termRing) append(p []byte) {
	if len(p) == 0 {
		return
	}
	if len(p) >= r.cap {
		// This write alone overflows the ring → keep only its trailing cap bytes.
		r.buf = append(r.buf[:0], p[len(p)-r.cap:]...)
		r.size = r.cap
		return
	}
	if r.size+len(p) > r.cap {
		// Drop the oldest bytes to make room, keeping the buffer contiguous.
		drop := r.size + len(p) - r.cap
		r.buf = append(r.buf[:0], r.buf[drop:r.size]...)
		r.size = r.size - drop
	}
	r.buf = append(r.buf[:r.size], p...)
	r.size += len(p)
}

// bytes returns a COPY of the current ring contents (oldest→newest). A copy so the
// caller can replay it into a fresh emulator without racing a concurrent append (the
// caller already holds termOut.mu, but a copy keeps the replay independent of further
// mutation and avoids handing out the live backing slice).
func (r *termRing) bytes() []byte {
	out := make([]byte, r.size)
	copy(out, r.buf[:r.size])
	return out
}
