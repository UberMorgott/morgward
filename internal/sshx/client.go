// Package sshx is a thin SSH client wrapper implementing the runbook's stateless
// one-shot executor model: one TCP connection, a fresh session per command,
// base64 script delivery (stdin-safe), AutoAdd host-key policy for fresh VPSes.
package sshx

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// SecretMarkerPrefix tags a stdout line that carries a secret (e.g. the generated
// console password) the engine must capture but NEVER stream. teeLines still writes
// such a line to the capture buffer (so the caller can parse the value out of
// Result.Stdout) but suppresses the live emit, so the raw secret never reaches the
// output sink → run log / TUI scrollback (F02). The producing remote script prints
// "CONSOLE_PW_MARKER:<value>"; the consuming step parses it back via this same prefix.
const SecretMarkerPrefix = "CONSOLE_PW_MARKER:"

// ErrNoMutualAuth is returned (wrapped via %w) when the SSH handshake reaches the
// server but no offered auth method is accepted — i.e. the server and client share
// no usable authentication method. This is the classic fresh-VPS symptom: the box
// offered only publickey while the operator supplied only a password (or vice
// versa). Callers can errors.Is() this to print an actionable, non-cryptic hint.
var ErrNoMutualAuth = errors.New("ssh: server accepted none of the offered auth methods")

// Result is the outcome of a single remote command.
type Result struct {
	Stdout string
	Stderr string
	RC     int   // exit code; -1 on transport/dial error
	Err    error // transport error (not a non-zero exit)
}

// OK reports a zero exit code with no transport error.
func (r Result) OK() bool { return r.RC == 0 && r.Err == nil }

// Out returns trimmed stdout.
func (r Result) Out() string { return strings.TrimSpace(r.Stdout) }

// Client wraps an *ssh.Client plus the connection parameters needed to
// reconnect (after a reboot) or switch identity (root -> admin+sudo).
type Client struct {
	Host string
	Port int
	User string

	signer   ssh.Signer // key auth (preferred once bootstrapped)
	password string     // password auth (bootstrap only)
	cli      *ssh.Client

	// OnOutput, when set, receives every server output line as it is produced
	// (stream is "out" or "err"). Optional: nil = silent capture, preserving the
	// behavior of callers that do not opt in (e.g. short-lived verify dials). This
	// is the single integration point for live streaming — set it once at the
	// client level and every Run/Sudo streams with no per-step boilerplate.
	OnOutput func(stream, line string)
}

// SetOutputSink installs the per-line output callback (see OnOutput). Pass nil
// to disable streaming and fall back to silent capture.
func (c *Client) SetOutputSink(fn func(stream, line string)) { c.OnOutput = fn }

// Dial opens a connection. Provide either keyPEM or password (key wins).
func Dial(host string, port int, user, password string, keyPEM []byte) (*Client, error) {
	c := &Client{Host: host, Port: port, User: user, password: password}
	if len(keyPEM) > 0 {
		signer, err := ssh.ParsePrivateKey(keyPEM)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		c.signer = signer
	}
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) authMethods() []ssh.AuthMethod {
	var m []ssh.AuthMethod
	// 1. Explicit key wins (the operator's deliberate choice / bootstrapped key).
	if c.signer != nil {
		m = append(m, ssh.PublicKeys(c.signer))
	}
	// 2. ssh-agent (if running): the common "just works" path when the provider
	//    installed the operator's public key. Signers are resolved lazily at
	//    handshake time so a dead socket costs nothing here.
	if am := agentAuthMethod(); am != nil {
		m = append(m, am)
	}
	// 3. Password + keyboard-interactive fallback (some sshd configs require the
	//    latter for password auth). Lowest priority so an installed key wins.
	if c.password != "" {
		m = append(m, ssh.Password(c.password))
		m = append(m, ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			ans := make([]string, len(qs))
			for i := range qs {
				ans[i] = c.password
			}
			return ans, nil
		}))
	}
	return m
}

// agentAuthMethod returns a publickey auth method backed by the running ssh-agent
// (via $SSH_AUTH_SOCK), or nil if no agent is reachable. Signers are fetched at
// handshake time, so this is cheap and tolerant of a stale/absent socket.
func agentAuthMethod() ssh.AuthMethod {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, err
		}
		// The conn intentionally outlives this call: the returned signers use it to
		// sign during the handshake. It is reclaimed when the process exits; a
		// bootstrap CLI is short-lived, so this is acceptable.
		return agent.NewClient(conn).Signers()
	})
}

func (c *Client) connect() error {
	cfg := &ssh.ClientConfig{
		User:            c.User,
		Auth:            c.authMethods(),
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // AutoAdd: fresh VPS, fingerprint unknown
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))
	cli, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		if isNoMutualAuth(err) {
			// Wrap so callers can errors.Is(err, ErrNoMutualAuth); keep the raw text.
			return fmt.Errorf("ssh dial %s@%s: %w: %v", c.User, addr, ErrNoMutualAuth, err)
		}
		return fmt.Errorf("ssh dial %s@%s: %w", c.User, addr, err)
	}
	c.cli = cli
	return nil
}

// isNoMutualAuth reports whether err is the handshake failure that means the
// server rejected every auth method we offered (no shared method). The x/crypto
// ssh package surfaces this only as a text error, so we match on its stable
// phrasing ("unable to authenticate" / "no supported methods remain").
func isNoMutualAuth(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "unable to authenticate") ||
		strings.Contains(s, "no supported methods remain")
}

// DialWithRetry is the resilient connector for the INITIAL dial to a possibly
// just-provisioned box. It repeatedly attempts to connect until success or until
// timeout elapses, sleeping retryBackoff between attempts. A fresh VPS commonly
// transitions auth state (sshd in initramfs → cloud-init installs keys/password)
// inside this window, so BOTH transport/timeout failures AND no-mutual-auth
// failures are retried while the window is open. onTick (if non-nil) is called
// before each wait with a human-readable status, mirroring WaitForReboot.
//
// On the final failure after the window expires it returns the last error; when
// that last error was a no-mutual-auth rejection it is wrapped with
// ErrNoMutualAuth so the caller can print an actionable hint.
func DialWithRetry(host string, port int, user, password string, keyPEM []byte, timeout time.Duration, onTick func(string)) (*Client, error) {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	const retryBackoff = 5 * time.Second

	deadline := time.Now().Add(timeout)
	var lastErr error
	attempt := 0
	for {
		attempt++
		cli, err := Dial(host, port, user, password, keyPEM)
		if err == nil {
			return cli, nil
		}
		lastErr = err
		if !time.Now().Add(retryBackoff).Before(deadline) {
			break // next attempt would land past the window — stop now.
		}
		if onTick != nil {
			if isNoMutualAuth(err) {
				onTick(fmt.Sprintf("server up but auth not ready (attempt %d) — box may still be provisioning…", attempt))
			} else {
				onTick(fmt.Sprintf("waiting for SSH… (attempt %d, box may still be provisioning)", attempt))
			}
		}
		time.Sleep(retryBackoff)
	}
	return nil, lastErr
}

// Close shuts the connection.
func (c *Client) Close() {
	if c.cli != nil {
		_ = c.cli.Close()
		c.cli = nil
	}
}

// maxLine is the per-line scanner buffer ceiling (1 MiB). apt/dpkg lines stay far
// below this; the cap merely avoids bufio.ErrTooLong on a pathological long line.
const maxLine = 1 << 20

// Run executes a raw command as the connected user and captures rc/stdout/stderr.
// Each call opens a fresh session (stateless executor). Output is streamed line by
// line via OnOutput (if set) as the command produces it, while still being captured
// into Result.Stdout/Stderr — non-streaming callers see identical results.
func (c *Client) Run(cmd string) Result {
	sess, err := c.cli.NewSession()
	if err != nil {
		return Result{RC: -1, Err: fmt.Errorf("new session: %w", err)}
	}
	defer sess.Close()

	stdout, err := sess.StdoutPipe()
	if err != nil {
		return Result{RC: -1, Err: fmt.Errorf("stdout pipe: %w", err)}
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return Result{RC: -1, Err: fmt.Errorf("stderr pipe: %w", err)}
	}
	if err := sess.Start(cmd); err != nil {
		return Result{RC: -1, Err: fmt.Errorf("start: %w", err)}
	}

	// One scanner goroutine per pipe: each tees lines into the capture buffer AND
	// (if a sink is set) to OnOutput immediately. Drain both before sess.Wait so
	// no output is lost to an early connection close.
	var out, errb strings.Builder
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		teeLines(stdout, &out, c.lineEmitter("out"))
	}()
	go func() {
		defer wg.Done()
		teeLines(stderr, &errb, c.lineEmitter("err"))
	}()
	wg.Wait()

	runErr := sess.Wait()
	r := Result{Stdout: out.String(), Stderr: errb.String()}
	switch e := runErr.(type) {
	case nil:
		r.RC = 0
	case *ssh.ExitError:
		r.RC = e.ExitStatus()
	default:
		r.RC = -1
		r.Err = runErr
	}
	return r
}

// lineEmitter returns the per-line emit callback for teeLines: it forwards to
// OnOutput tagged with stream, or nil (no-op) when no sink is set.
func (c *Client) lineEmitter(stream string) func(string) {
	if c.OnOutput == nil {
		return nil
	}
	return func(line string) { c.OnOutput(stream, line) }
}

// teeLines scans r line by line, appending each line (with a trailing newline) to
// capture for the buffered Result AND passing it to emit (if non-nil) the moment
// it is read, so a live sink sees output as it is produced. The scanner buffer is
// enlarged to maxLine to avoid bufio.ErrTooLong on long lines.
func teeLines(r io.Reader, capture *strings.Builder, emit func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		line := sc.Text()
		capture.WriteString(line)
		capture.WriteByte('\n')
		// Secret-marked lines are captured (the caller parses the value back out of
		// Result.Stdout) but never emitted, so the raw secret cannot reach the
		// streamed sink → log file / TUI scrollback (F02).
		if emit != nil && !strings.HasPrefix(strings.TrimSpace(line), SecretMarkerPrefix) {
			emit(line)
		}
	}
}

// Sudo runs a multi-line script with root privileges using base64 delivery,
// which is robust against the stdin-pipe corruption that breaks `cat <<EOF`
// here-docs under a piped controller (§A1 stdin caveat). When the connected
// user is not root, the decoded script is piped into `sudo bash`.
func (c *Client) Sudo(script string) Result {
	b64 := base64.StdEncoding.EncodeToString([]byte(script))
	runner := "bash"
	if c.User != "root" {
		runner = "sudo bash"
	}
	// `set -o pipefail` would mask the inner script's rc; keep the simple pipe.
	full := fmt.Sprintf("echo %s | base64 -d | %s", b64, runner)
	return c.Run(full)
}

// SwitchUser reconnects as a different user (root -> admin) using key auth.
// Used at the §A2 executor handoff once root SSH is closed in strict mode.
func (c *Client) SwitchUser(user string) error {
	if c.signer == nil {
		return fmt.Errorf("cannot switch to %s: no key loaded", user)
	}
	old := c.cli
	prev := c.User
	c.User = user
	c.password = "" // key only from here on
	if err := c.connect(); err != nil {
		c.User = prev
		return err
	}
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// UseKey switches the active connection from password auth to key auth.
func (c *Client) UseKey(keyPEM []byte) error {
	signer, err := ssh.ParsePrivateKey(keyPEM)
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}
	c.signer = signer
	old := c.cli
	if err := c.connect(); err != nil {
		return err
	}
	c.password = ""
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// BootID reads the kernel boot_id (used to confirm a reboot actually happened).
func (c *Client) BootID() string {
	return c.Run("cat /proc/sys/kernel/random/boot_id").Out()
}

// WaitForReboot polls the SSH port until the box answers AND boot_id differs
// from preBootID. Returns the new boot_id, or an error on timeout (box may be
// bricked — caller must escalate, not claim rollback).
func (c *Client) WaitForReboot(preBootID string, timeout time.Duration, onTick func(string)) (string, error) {
	deadline := time.Now().Add(timeout)
	c.Close()
	// Give the box a moment to actually go down before polling.
	time.Sleep(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := c.connect(); err != nil {
			if onTick != nil {
				onTick("waiting for SSH…")
			}
			time.Sleep(5 * time.Second)
			continue
		}
		bid := c.BootID()
		if bid != "" && bid != preBootID {
			return bid, nil
		}
		if onTick != nil {
			onTick("SSH up, boot_id unchanged — still rebooting…")
		}
		c.Close()
		time.Sleep(5 * time.Second)
	}
	return "", fmt.Errorf("SSH never reconnected with a new boot_id within %s — box may be bricked; recover via provider console", timeout)
}

// LoadKeyFile reads a private key file from disk.
func LoadKeyFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
