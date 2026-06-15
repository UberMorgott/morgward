package sshx

import "testing"

// SFTP on a client with no live connection must fail cleanly (typed error, nil client),
// never panic.
func TestSFTPOnClosedClientErrors(t *testing.T) {
	c := &Client{} // no live cli
	sc, err := c.SFTP()
	if err == nil {
		t.Fatal("SFTP() on a client with no live connection must return an error")
	}
	if sc != nil {
		t.Fatalf("SFTP() error path must return a nil *sftp.Client, got %v", sc)
	}
}
