package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/sshx"
)

// runShell opens an interactive PTY shell on the VPS — morgward as a real
// terminal. It dials EXACTLY as the engine does (key wins, else password) but
// performs none of the hardening machinery: no key bootstrap, no detection, no
// gates. The local terminal is put into raw mode (when stdin is a TTY) so the
// remote shell owns echo, line editing, Ctrl-C, and full-screen apps; piped
// (non-TTY) stdin runs the same plumbing without raw mode so scripts work
// (`echo "cmd" | morgward shell ...`).
func runShell(cfg *config.Config) error {
	// Auth must be present — host/user are defaulted/prompted by the caller. The
	// shell needs only host/user/auth, so validate that subset directly rather
	// than the full hardening config.
	if cfg.Host == "" {
		return fmt.Errorf("%w", config.ErrHostRequired)
	}
	if cfg.User == "" {
		return fmt.Errorf("%w", config.ErrUserRequired)
	}
	if cfg.KeyPath == "" && cfg.Password == "" {
		return fmt.Errorf("%w", config.ErrAuthRequired)
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}

	// Dial identically to engine.prepare's dial: load --key if given (key wins),
	// else fall back to password. No DialWithRetry — an interactive shell targets
	// a box that is already up, so a fast single dial with a clear error is better
	// than a 90s provisioning retry loop.
	var keyPEM []byte
	if cfg.KeyPath != "" {
		var err error
		keyPEM, err = sshx.LoadKeyFile(cfg.KeyPath)
		if err != nil {
			return fmt.Errorf("load key: %w", err)
		}
	}
	cli, err := sshx.Dial(cfg.Host, cfg.Port, cfg.User, cfg.Password, keyPEM)
	if err != nil {
		if errors.Is(err, sshx.ErrNoMutualAuth) {
			return fmt.Errorf("could not authenticate to %s@%s — the server accepted none of the offered methods (check user/key/password): %w", cfg.User, cfg.Host, err)
		}
		return fmt.Errorf("connect %s@%s:%d: %w", cfg.User, cfg.Host, cfg.Port, err)
	}
	defer cli.Close()

	ctx := context.Background()
	fd := int(os.Stdin.Fd())

	sio := sshx.ShellIO{
		In:   os.Stdin,
		Out:  os.Stdout,
		Term: os.Getenv("TERM"),
	}
	var resize <-chan sshx.WinSize

	if term.IsTerminal(fd) {
		// Interactive terminal: raw mode so the remote shell owns echo / line
		// editing / Ctrl-C, and a live resize feed. Restore is armed IMMEDIATELY
		// so every later early-return leaves the terminal sane.
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("enter raw mode: %w", err)
		}
		restored := false
		restore := func() {
			if !restored {
				restored = true
				_ = term.Restore(fd, oldState)
				fmt.Fprintln(os.Stderr) // clean trailing newline after the raw session
			}
		}
		defer restore()

		if cols, rows, gerr := term.GetSize(fd); gerr == nil {
			sio.Cols, sio.Rows = cols, rows
		}
		ch, stop := watchResize(fd)
		defer stop()
		resize = ch

		if err := cli.Shell(ctx, sio, resize); err != nil {
			restore() // surface the error on a sane terminal
			return fmt.Errorf("shell session: %w", err)
		}
		return nil
	}

	// Non-TTY (piped / scripted): no raw mode, no resize. Default geometry; the
	// pty plumbing still runs so `echo "cmd" | morgward shell ...` works.
	if err := cli.Shell(ctx, sio, nil); err != nil {
		return fmt.Errorf("shell session: %w", err)
	}
	return nil
}

// resolveShellHost picks the shell target host with the documented precedence:
// a positional arg (first non-flag after `shell`) wins, else --host, else
// $VPS_HOST, else an interactive prompt — mirroring the run-path resolution.
func resolveShellHost(cfg *config.Config, positional []string) error {
	if len(positional) > 0 && positional[0] != "" {
		cfg.Host = positional[0]
		return nil
	}
	if cfg.Host != "" {
		return nil
	}
	if h := os.Getenv("VPS_HOST"); h != "" {
		cfg.Host = h
		return nil
	}
	r := bufio.NewReader(os.Stdin)
	cfg.Host = prompt(r, "VPS host (IP/hostname)", "")
	return nil
}
