package sshx

import (
	"io"
	"strings"
	"testing"
	"time"
)

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
