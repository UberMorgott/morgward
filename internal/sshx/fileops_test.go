package sshx

import (
	"bytes"
	"strings"
	"testing"
)

// copyStream is the transport-agnostic core of Download/Upload; test it with in-memory
// buffers so the byte-copy + error-propagation logic is covered without an SSH server.
func TestCopyStream(t *testing.T) {
	src := strings.NewReader("hello sftp payload")
	var dst bytes.Buffer
	if err := copyStream(&dst, src); err != nil {
		t.Fatalf("copyStream err = %v", err)
	}
	if dst.String() != "hello sftp payload" {
		t.Fatalf("copied = %q", dst.String())
	}
}

// errReader fails on the first Read so we can prove copyStream surfaces a read error.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errFakeRead }

var errFakeRead = bytes.ErrTooLarge // any sentinel error

func TestCopyStreamPropagatesError(t *testing.T) {
	var dst bytes.Buffer
	if err := copyStream(&dst, errReader{}); err == nil {
		t.Fatal("copyStream must surface a read error")
	}
}
