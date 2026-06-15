package sshx

import (
	"io"
	"os"

	"github.com/pkg/sftp"
)

// copyStream is the transport-agnostic core of the file transfers: it io.Copy's src into
// dst and returns the first error. Factored out so the byte-copy logic is unit-testable
// with in-memory buffers (Download/Upload, which need a live sftp client, are live-tested).
func copyStream(dst io.Writer, src io.Reader) error {
	_, err := io.Copy(dst, src)
	return err
}

// DownloadFile copies the remote file at remotePath to localPath over the given sftp client
// (which the caller guarantees is live). localPath is truncated/created. Both ends are
// closed; the first error encountered is returned.
func DownloadFile(sc *sftp.Client, remotePath, localPath string) error {
	rf, err := sc.Open(remotePath)
	if err != nil {
		return err
	}
	defer func() { _ = rf.Close() }()

	lf, err := os.Create(localPath) // #nosec G304 -- localPath is operator-chosen (download dest)
	if err != nil {
		return err
	}
	if err := copyStream(lf, rf); err != nil {
		_ = lf.Close()
		return err
	}
	return lf.Close()
}

// UploadFile copies the local file at localPath to the remote file at remotePath over the
// given sftp client. The remote file is truncated/created. Both ends are closed; the first
// error encountered is returned.
func UploadFile(sc *sftp.Client, localPath, remotePath string) error {
	lf, err := os.Open(localPath) // #nosec G304 -- localPath is operator-chosen (upload source)
	if err != nil {
		return err
	}
	defer func() { _ = lf.Close() }()

	rf, err := sc.Create(remotePath)
	if err != nil {
		return err
	}
	if err := copyStream(rf, lf); err != nil {
		_ = rf.Close()
		return err
	}
	return rf.Close()
}
