package sshx

import (
	"errors"

	"github.com/pkg/sftp"
)

// ErrNoConn is returned by SFTP when the client has no live SSH connection.
var ErrNoConn = errors.New("sshx: no live SSH connection")

// SFTP opens an sftp subsystem over the live connection. The caller OWNS the returned
// *sftp.Client and must Close() it. It multiplexes a new channel on the same transport
// Run/Shell use; the sftp client is NOT concurrency-safe — the caller serializes use. If
// a redial replaces the underlying connection (reboot/SwitchUser), an existing sftp
// client is invalidated — the file manager is used within a stable session, so reopen on
// demand. Reads c.cli under c.mu (same lock guarding the live connection elsewhere).
func (c *Client) SFTP() (*sftp.Client, error) {
	c.mu.Lock()
	cli := c.cli
	c.mu.Unlock()
	if cli == nil {
		return nil, ErrNoConn
	}
	return sftp.NewClient(cli)
}
