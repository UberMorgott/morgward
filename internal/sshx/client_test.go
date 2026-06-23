package sshx

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakePublicKey is a minimal ssh.PublicKey stub whose Marshal output is fully
// controlled by the test, so the host-key pin logic can be exercised without a
// real handshake or live server.
type fakePublicKey struct {
	typ  string
	wire []byte
}

func (k fakePublicKey) Type() string                        { return k.typ }
func (k fakePublicKey) Marshal() []byte                     { return k.wire }
func (k fakePublicKey) Verify([]byte, *ssh.Signature) error { return nil }

// TestHostKeyPinTOFU proves the in-run TOFU pin: the first host key seen by a
// Client is trusted+pinned; an identical key on a later handshake is accepted; a
// DIFFERENT key is rejected with ErrHostKeyChanged (possible MITM mid-run).
func TestHostKeyPinTOFU(t *testing.T) {
	c := &Client{}
	cb := c.hostKeyCallback()
	addr := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 22}

	keyA := fakePublicKey{typ: ssh.KeyAlgoED25519, wire: []byte("HOSTKEY-A")}
	keyB := fakePublicKey{typ: ssh.KeyAlgoED25519, wire: []byte("HOSTKEY-B")}

	// First sight: trust + pin.
	if err := cb("host", addr, keyA); err != nil {
		t.Fatalf("first connect (TOFU) should accept any key, got %v", err)
	}
	// Same key on a reconnect: accepted.
	if err := cb("host", addr, keyA); err != nil {
		t.Fatalf("matching pinned key should be accepted, got %v", err)
	}
	// Changed key on a reconnect: rejected as MITM.
	err := cb("host", addr, keyB)
	if err == nil {
		t.Fatal("changed host key must be rejected, got nil")
	}
	if !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("want ErrHostKeyChanged, got %v", err)
	}
}

// TestIsNoMutualAuth confirms the auth-rejection phrases WaitForReboot relies on
// to surface ErrRebootAuthFailed are still recognized (and unrelated transport
// errors are not misclassified as auth failures).
func TestIsNoMutualAuth(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey]"), true},
		{errors.New("ssh: no supported methods remain"), true},
		{errors.New("dial tcp 10.0.0.1:22: connect: connection refused"), false},
		{errors.New("i/o timeout"), false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := isNoMutualAuth(tc.err); got != tc.want {
			t.Errorf("isNoMutualAuth(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// TestRunProbe covers the bounded keepalive probe (FA-0021): a timely result is
// returned verbatim; a probe that outlives the timeout is reported as a miss
// (errKeepaliveTimeout) rather than blocking; closing stop short-circuits to a miss.
func TestRunProbe(t *testing.T) {
	t.Run("timely success", func(t *testing.T) {
		err := runProbe(func() error { return nil }, make(chan struct{}), 500*time.Millisecond)
		if err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
	t.Run("timely error propagated", func(t *testing.T) {
		sentinel := errors.New("transport down")
		err := runProbe(func() error { return sentinel }, make(chan struct{}), 500*time.Millisecond)
		if !errors.Is(err, sentinel) {
			t.Fatalf("want sentinel, got %v", err)
		}
	})
	t.Run("stuck probe times out as a miss", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release) // unblock the abandoned goroutine so it can exit cleanly
		start := time.Now()
		err := runProbe(func() error { <-release; return nil }, make(chan struct{}), 30*time.Millisecond)
		if !errors.Is(err, errKeepaliveTimeout) {
			t.Fatalf("want errKeepaliveTimeout, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("probe did not fast-fail: took %s", elapsed)
		}
	})
	t.Run("stop signals a miss", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		stop := make(chan struct{})
		close(stop)
		err := runProbe(func() error { <-release; return nil }, stop, 5*time.Second)
		if !errors.Is(err, errKeepaliveTimeout) {
			t.Fatalf("want errKeepaliveTimeout on stop, got %v", err)
		}
	})
}

// slowReader yields its payload in chunks with a small delay between them, so the
// test exercises the streaming path (emit must fire per line as data arrives, not
// only after EOF) rather than a single buffered read.
type slowReader struct {
	chunks []string
	i      int
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.i >= len(r.chunks) {
		return 0, io.EOF
	}
	time.Sleep(2 * time.Millisecond)
	n := copy(p, r.chunks[r.i])
	r.i++
	return n, nil
}

func TestTeeLines(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   []string // expected per-line emits, in order
	}{
		{
			name:   "trailing newline",
			chunks: []string{"first\n", "second\n", "third\n"},
			want:   []string{"first", "second", "third"},
		},
		{
			name:   "no trailing newline on last line",
			chunks: []string{"alpha\n", "beta\n", "gamma"},
			want:   []string{"alpha", "beta", "gamma"},
		},
		{
			name:   "lines split across reads",
			chunks: []string{"he", "llo\nwor", "ld\n"},
			want:   []string{"hello", "world"},
		},
		{
			name:   "empty input",
			chunks: nil,
			want:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var capture strings.Builder
			var got []string
			teeLines(&slowReader{chunks: tc.chunks}, &capture, func(line string) {
				got = append(got, line)
			})

			// emit fired once per line, in order, completely.
			if len(got) != len(tc.want) {
				t.Fatalf("emit count = %d, want %d (got=%q)", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("emit[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}

			// capture accumulates every line, each newline-terminated, in order.
			wantCap := ""
			for _, l := range tc.want {
				wantCap += l + "\n"
			}
			if capture.String() != wantCap {
				t.Errorf("capture = %q, want %q", capture.String(), wantCap)
			}
		})
	}
}

// TestTeeLinesNilEmit verifies the no-sink path: capture still fills, no panic.
func TestTeeLinesNilEmit(t *testing.T) {
	var capture strings.Builder
	teeLines(strings.NewReader("one\ntwo\n"), &capture, nil)
	if capture.String() != "one\ntwo\n" {
		t.Errorf("capture = %q, want %q", capture.String(), "one\ntwo\n")
	}
}

// TestTeeLinesSecretMarkerNeverEmitted proves F02: a SecretMarkerPrefix line is
// captured into the buffer (so the engine can parse the value back out) but is
// NEVER passed to emit, so the raw secret cannot reach the streamed sink → log
// file / TUI scrollback. Ordinary lines around it still stream normally.
func TestTeeLinesSecretMarkerNeverEmitted(t *testing.T) {
	const secret = "Sup3rS3cretPW=="
	input := "before\n" + SecretMarkerPrefix + secret + "\nafter\n"

	var capture strings.Builder
	var emitted []string
	teeLines(strings.NewReader(input), &capture, func(line string) {
		emitted = append(emitted, line)
	})

	// The secret value must never appear in anything the sink saw.
	for _, l := range emitted {
		if strings.Contains(l, secret) || strings.Contains(l, SecretMarkerPrefix) {
			t.Fatalf("secret leaked to sink: emitted line %q", l)
		}
	}
	// Non-secret lines still stream, in order.
	wantEmit := []string{"before", "after"}
	if len(emitted) != len(wantEmit) {
		t.Fatalf("emitted = %q, want %q", emitted, wantEmit)
	}
	for i := range wantEmit {
		if emitted[i] != wantEmit[i] {
			t.Errorf("emitted[%d] = %q, want %q", i, emitted[i], wantEmit[i])
		}
	}
	// But the marker line IS captured, so the value remains extractable downstream.
	if !strings.Contains(capture.String(), SecretMarkerPrefix+secret) {
		t.Fatalf("capture missing marker line; got %q", capture.String())
	}
}
