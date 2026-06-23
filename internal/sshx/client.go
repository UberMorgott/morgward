// Package sshx is a thin SSH client wrapper implementing the runbook's stateless
// one-shot executor model: one TCP connection, a fresh session per command,
// base64 script delivery (stdin-safe), in-run TOFU host-key pinning for fresh
// VPSes.
//
// Concurrency: a Client is NOT concurrency-safe for command execution (Run/Sudo)
// — that contract is unchanged. The ONLY internal goroutine is the per-connection
// keepalive sender (see startKeepalive); its access to the underlying *ssh.Client
// and stop channel is serialized with the rest of the connection lifecycle
// (connect/Close/SwitchUser/UseKey/WaitForReboot) via c.mu. Callers must still
// drive Run/Sudo from a single goroutine.
package sshx

import (
	"bufio"
	"bytes"
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

// ErrHostKeyChanged is returned (wrapped via %w) when a reconnect within the same
// logical run negotiates a host key that differs from the one pinned on the FIRST
// successful dial. The first connect of a fresh box is trust-on-first-use (we have
// no prior fingerprint), but every subsequent connect by the same Client — reboot
// redials, root→admin SwitchUser, UseKey, freshLogin second sessions — must see the
// same host key, else it is treated as a possible MITM and refused.
var ErrHostKeyChanged = errors.New("ssh: host key changed mid-run (possible MITM)")

// ErrRebootAuthFailed is returned by WaitForReboot when the box comes back up and
// answers on the SSH port but rejects our credentials (e.g. a prior A2-danger
// disabled password auth on a password-only box). The box is NOT bricked — it is
// reachable but our auth no longer works — so this is surfaced distinctly from the
// generic "never reconnected / may be bricked" timeout.
var ErrRebootAuthFailed = errors.New("ssh: box rebooted and is reachable but our credentials were rejected (auth no longer works)")

// keepaliveInterval is the cadence of the connection liveness probe, and
// keepaliveMaxFails the number of CONSECUTIVE failed probes tolerated before the
// connection is force-closed. ~30s matches the common OpenSSH ClientAliveInterval
// default; 3 misses (~90s) absorbs a brief network blip while still bounding how
// long a hung command / silently-dead TCP (NAT idle-drop) can park sess.Wait.
const (
	keepaliveInterval = 30 * time.Second
	keepaliveMaxFails = 3
	// keepaliveProbeTimeout bounds a single keepalive global-request round-trip
	// (FA-0021). cli.SendRequest(...,wantReply=true,...) blocks until the server
	// replies or the transport errors; on a half-open TCP (silent NAT drop, dead
	// peer) that can park one probe for the full OS TCP timeout, defeating the
	// fast-fail the miss-counter is meant to give. A probe that does not complete
	// within this window counts as a miss so the existing keepaliveMaxFails logic
	// force-closes promptly. Comfortably under keepaliveInterval so probes never
	// overlap.
	keepaliveProbeTimeout = 10 * time.Second
)

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

	// mu guards the connection lifecycle shared with the keepalive goroutine: cli
	// (the live *ssh.Client) and kaStop (the current keepalive's stop signal).
	// command execution (Run/Sudo) is still single-goroutine by contract; mu only
	// exists so Close can race the internal keepalive sender safely.
	mu     sync.Mutex
	cli    *ssh.Client
	kaStop chan struct{} // closed to stop the current connection's keepalive goroutine

	// pinnedHostKey is the marshaled server host key recorded on the FIRST
	// successful handshake (in-run TOFU). nil until pinned. Once set, every later
	// handshake by this Client must present the same key (see hostKeyCallback).
	pinnedHostKey []byte

	// pin, when non-nil, is an operator-supplied host-key expectation (a
	// known_hosts file and/or a sha256 fingerprint). It replaces blind TOFU on the
	// FIRST handshake: the presented key must satisfy the pin or the connect is
	// refused. nil => today's TOFU (trust-on-first-use), byte-identical.
	pin *HostKeyPin

	// agentConns collects ssh-agent unix sockets opened during a handshake (FA-0020).
	// agentAuthMethod's signers must keep their socket open for the DURATION of the
	// handshake, so we cannot close it inside the auth callback; instead each conn is
	// recorded here and closed by closeAgentConns once connect's handshake returns.
	// Guarded by mu (the auth callback may run on a handshake goroutine).
	agentConns []net.Conn

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

// Dial opens a connection. Provide either keyPEM or password (key wins). Host-key
// handling is the default in-run TOFU (no operator pin); see DialWithPin to verify
// the first host key against a --known-hosts file / --host-fingerprint.
func Dial(host string, port int, user, password string, keyPEM []byte) (*Client, error) {
	return DialWithPin(host, port, user, password, keyPEM, nil)
}

// DialWithPin is Dial with an OPT-IN host-key pin. When pin is non-nil the FIRST
// handshake must satisfy it (else ErrHostKeyMismatch) instead of blindly trusting
// the server's key; when pin is nil this is byte-identical to Dial (default TOFU).
// The in-run pin-on-first + ErrHostKeyChanged-on-later-change guard is unchanged on
// both paths.
func DialWithPin(host string, port int, user, password string, keyPEM []byte, pin *HostKeyPin) (*Client, error) {
	c := &Client{Host: host, Port: port, User: user, password: password, pin: pin}
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
	if am := c.agentAuthMethod(); am != nil {
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
//
// FA-0020: the dialed unix socket must stay open while its signers sign during the
// handshake, so it cannot be closed inside this callback. Instead each opened conn
// is recorded on the Client and closed by closeAgentConns once connect's handshake
// returns — so a long-lived TUI that redials per reconnect (with an agent socket
// present) no longer leaks one fd per dial.
func (c *Client) agentAuthMethod() ssh.AuthMethod {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.agentConns = append(c.agentConns, conn)
		c.mu.Unlock()
		return agent.NewClient(conn).Signers()
	})
}

// closeAgentConns closes (and clears) every ssh-agent socket opened by
// agentAuthMethod during the just-finished handshake. Safe to call when none were
// opened (the common no-agent path) and idempotent. Called after the handshake
// completes — never before signing, which would break agent auth (FA-0020).
func (c *Client) closeAgentConns() {
	c.mu.Lock()
	conns := c.agentConns
	c.agentConns = nil
	c.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

const dialTimeout = 15 * time.Second

// pinnedHostKeyAlgos is the host-key algorithm preference used for EVERY dial.
// ed25519 is preferred FIRST (x/crypto's own default order would pick rsa/ecdsa
// ahead of it). This matters for the in-run host-key pin: §A2's hardening REMOVES
// the box's ecdsa host key (keeping ed25519 + rsa). If the first dial pinned the
// ecdsa key (which x/crypto's default order would prefer over ed25519), the post-
// A8-reboot reconnect — the server no longer offering ecdsa — would negotiate a
// different key and trip ErrHostKeyChanged on a perfectly legitimate box. Pinning
// the ed25519 key (which A2 preserves) makes the pin survive that documented
// removal with no cross-package coordination. rsa variants follow (also preserved
// by A2); ecdsa stays only as a last-resort fallback for an off-target box that
// somehow lacks both ed25519 and rsa, so we can still connect there.
var pinnedHostKeyAlgos = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoRSASHA256,
	ssh.KeyAlgoRSASHA512,
	ssh.KeyAlgoRSA,
	ssh.KeyAlgoECDSA256,
	ssh.KeyAlgoECDSA384,
	ssh.KeyAlgoECDSA521,
}

func (c *Client) connect() error {
	// Defensive: stop any keepalive still attached to a previous connection so a
	// caller that re-connects without Close()/detachConn() cannot orphan the old
	// goroutine. Idempotent — a no-op when the keepalive was already stopped.
	c.stopKeepalive()
	// FA-0020: any ssh-agent socket opened by agentAuthMethod is needed only while
	// the handshake below signs with it; close every one once connect returns
	// (success OR failure), so repeated redials cannot accrete agent-socket fds.
	defer c.closeAgentConns()
	cfg := &ssh.ClientConfig{
		User:              c.User,
		Auth:              c.authMethods(),
		HostKeyCallback:   c.hostKeyCallback(), // in-run TOFU: pin on first connect, verify after
		HostKeyAlgorithms: pinnedHostKeyAlgos,
		Timeout:           dialTimeout,
	}
	addr := net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))

	// Dial the TCP layer ourselves (instead of ssh.Dial) so we can enable TCP-level
	// keepalive on the underlying socket — a second, OS-driven liveness signal
	// beneath the SSH keepalive that helps the kernel tear down a silently-dead
	// connection (e.g. a NAT that dropped the flow) rather than blocking forever.
	conn, err := net.DialTimeout("tcp", addr, cfg.Timeout)
	if err != nil {
		return fmt.Errorf("ssh dial %s@%s: %w", c.User, addr, err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(keepaliveInterval)
	}
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		if isNoMutualAuth(err) {
			// Wrap so callers can errors.Is(err, ErrNoMutualAuth); keep the raw text.
			return fmt.Errorf("ssh handshake %s@%s: %w: %v", c.User, addr, ErrNoMutualAuth, err)
		}
		// Surface a pinned-key mismatch verbatim so errors.Is(err, ErrHostKeyChanged)
		// works for the caller (NewClientConn wraps the callback error).
		if errors.Is(err, ErrHostKeyChanged) {
			return fmt.Errorf("ssh handshake %s@%s: %w", c.User, addr, ErrHostKeyChanged)
		}
		// A failed operator host-key pin on the first dial: surface it preserving the
		// wrapped reason so errors.Is(err, ErrHostKeyMismatch) works for the caller.
		if errors.Is(err, ErrHostKeyMismatch) {
			return fmt.Errorf("ssh handshake %s@%s: %w", c.User, addr, err)
		}
		return fmt.Errorf("ssh handshake %s@%s: %w", c.User, addr, err)
	}
	cli := ssh.NewClient(sc, chans, reqs)

	c.mu.Lock()
	c.cli = cli
	c.mu.Unlock()
	c.startKeepalive(cli)
	return nil
}

// hostKeyCallback implements in-run TOFU (with an optional operator pin). The first
// successful handshake records (pins) the server's host key; every later handshake
// by this same Client must present a byte-identical key or it is rejected with
// ErrHostKeyChanged.
//
// First connect: when c.pin is nil, accept any key (a fresh VPS has no known
// fingerprint — the documented TOFU tradeoff, byte-identical to before this
// feature). When c.pin is set (opt-in --known-hosts / --host-fingerprint), the
// presented key must satisfy the pin instead of being trusted blindly, else the
// connect is refused with ErrHostKeyMismatch. Either way the accepted first key is
// recorded so the in-run ErrHostKeyChanged guard protects every later redial.
//
// The callback runs synchronously inside the handshake, before connect returns and
// before the keepalive goroutine starts, so the pin field needs no extra locking
// against command execution.
func (c *Client) hostKeyCallback() ssh.HostKeyCallback {
	return func(hostport string, remote net.Addr, key ssh.PublicKey) error {
		marshaled := key.Marshal()
		if c.pinnedHostKey == nil {
			// First sight. With an operator pin, verify against it instead of blind
			// trust; without one, fall back to TOFU. Pin the (accepted) key either way.
			if c.pin != nil {
				if err := c.pin.verify(hostport, remote, key); err != nil {
					return err
				}
			}
			c.pinnedHostKey = marshaled
			return nil
		}
		if !bytes.Equal(c.pinnedHostKey, marshaled) {
			return ErrHostKeyChanged
		}
		return nil
	}
}

// startKeepalive launches the per-connection liveness goroutine for cli. It sends
// an OpenSSH keepalive global request every keepaliveInterval; after
// keepaliveMaxFails consecutive failures (or any single error indicating the
// transport is already gone) it Close()s the client, which unblocks a parked
// sess.Wait/Run with a transport error. The goroutine stops when its stop channel
// is closed (by stopKeepalive, called from connect's successor / Close).
func (c *Client) startKeepalive(cli *ssh.Client) {
	stop := make(chan struct{})
	c.mu.Lock()
	c.kaStop = stop
	c.mu.Unlock()

	go func() {
		t := time.NewTicker(keepaliveInterval)
		defer t.Stop()
		fails := 0
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				// wantReply=true so the server actually round-trips; an error means
				// the transport is broken (or the remote is wedged). A half-open TCP
				// can park SendRequest for the full OS TCP timeout, so bound the single
				// call (FA-0021): a probe that neither replies nor errors within
				// keepaliveProbeTimeout is treated as a miss, letting the miss-counter
				// force-close promptly instead of wedging here.
				if err := sendKeepalive(cli, stop); err != nil {
					fails++
					if fails >= keepaliveMaxFails {
						// Force the connection down so a blocked Run/sess.Wait unblocks.
						// During a reboot this is EXPECTED and desirable — WaitForReboot
						// has already Close()d and will redial regardless.
						_ = cli.Close()
						return
					}
					continue
				}
				fails = 0
			}
		}
	}()
}

// errKeepaliveTimeout is the synthetic miss returned by sendKeepalive when a probe
// neither replied nor errored within keepaliveProbeTimeout (FA-0021). It is treated
// exactly like any other probe failure by the caller's miss-counter.
var errKeepaliveTimeout = errors.New("ssh: keepalive probe timed out")

// sendKeepalive runs a single keepalive global request with a bounded deadline so a
// stuck round-trip (half-open TCP) cannot park the keepalive loop. The blocking
// SendRequest runs in its own goroutine; we return on whichever fires first: the
// request result, the probe timeout, or the keepalive stop signal. When the timer
// or stop wins, the in-flight SendRequest goroutine is abandoned — it unblocks on
// its own once the transport is torn down (the miss-counter force-closes cli after
// keepaliveMaxFails), and it only writes to a buffered channel, so it cannot leak
// or block. Returns nil only on a real, timely reply.
func sendKeepalive(cli *ssh.Client, stop <-chan struct{}) error {
	return runProbe(func() error {
		_, _, err := cli.SendRequest("keepalive@openssh.com", true, nil)
		return err
	}, stop, keepaliveProbeTimeout)
}

// runProbe runs a single blocking probe (send) in its own goroutine and returns on
// whichever fires first: the probe result, the timeout, or stop. On timeout/stop it
// returns errKeepaliveTimeout (a miss) and abandons the in-flight goroutine, which
// unblocks once the transport is torn down; done is buffered so that goroutine never
// blocks. Split out from sendKeepalive so the timeout/stop behavior is unit-testable
// without a live *ssh.Client. (FA-0021)
func runProbe(send func() error, stop <-chan struct{}, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- send() }()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		return errKeepaliveTimeout
	case <-stop:
		// Connection is being torn down; report a miss so the loop exits its select
		// and re-reads stop (it will return on the next iteration). No force-close
		// here — stopKeepalive owns lifecycle.
		return errKeepaliveTimeout
	}
}

// stopKeepalive signals the current connection's keepalive goroutine to exit, if
// one is running. Caller need not hold c.mu. Idempotent.
func (c *Client) stopKeepalive() {
	c.mu.Lock()
	stop := c.kaStop
	c.kaStop = nil
	c.mu.Unlock()
	if stop != nil {
		close(stop)
	}
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
// pin is the OPT-IN host-key expectation (see DialWithPin / ParseHostKeyPin); pass
// nil for the default trust-on-first-use. A pinned mismatch is NOT retried — an
// unexpected host key never heals by waiting, so it fails fast with
// ErrHostKeyMismatch rather than burning the retry window.
//
// On the final failure after the window expires it returns the last error; when
// that last error was a no-mutual-auth rejection it is wrapped with
// ErrNoMutualAuth so the caller can print an actionable hint.
func DialWithRetry(host string, port int, user, password string, keyPEM []byte, pin *HostKeyPin, timeout time.Duration, onTick func(string)) (*Client, error) {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	const retryBackoff = 5 * time.Second

	deadline := time.Now().Add(timeout)
	var lastErr error
	attempt := 0
	for {
		attempt++
		cli, err := DialWithPin(host, port, user, password, keyPEM, pin)
		if err == nil {
			return cli, nil
		}
		lastErr = err
		// A host-key pin mismatch is a hard stop, not a transient provisioning state:
		// retrying cannot make a wrong/forged key become right, so fail immediately.
		if errors.Is(err, ErrHostKeyMismatch) {
			return nil, err
		}
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

// Close shuts the connection and stops its keepalive goroutine. Safe to call
// concurrently with the internal keepalive sender (the only other goroutine that
// touches the connection); guarded by c.mu.
func (c *Client) Close() {
	c.stopKeepalive()
	c.mu.Lock()
	cli := c.cli
	c.cli = nil
	c.mu.Unlock()
	if cli != nil {
		_ = cli.Close()
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
	// Snapshot the live client under the lock: command execution is single-goroutine
	// by contract, but connect/Close (and the keepalive goroutine's Close) mutate
	// c.cli under c.mu, so read it the same way to stay race-free.
	c.mu.Lock()
	cli := c.cli
	c.mu.Unlock()
	if cli == nil {
		return Result{RC: -1, Err: fmt.Errorf("new session: not connected")}
	}
	sess, err := cli.NewSession()
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

// detachConn stops the current connection's keepalive goroutine and returns the
// live *ssh.Client (clearing c.cli) so a reconnect can replace it without leaking
// the old keepalive. The caller closes the returned client once the new connect
// has succeeded. Returns nil if there was no live connection.
func (c *Client) detachConn() *ssh.Client {
	c.stopKeepalive()
	c.mu.Lock()
	old := c.cli
	c.cli = nil
	c.mu.Unlock()
	return old
}

// SwitchUser reconnects as a different user (root -> admin) using key auth.
// Used at the §A2 executor handoff once root SSH is closed in strict mode. The
// pinned host key (in-run TOFU) carries over, so the re-dial verifies the box is
// the same host.
func (c *Client) SwitchUser(user string) error {
	if c.signer == nil {
		return fmt.Errorf("cannot switch to %s: no key loaded", user)
	}
	old := c.detachConn()
	prev := c.User
	c.User = user
	c.password = "" // key only from here on
	if err := c.connect(); err != nil {
		c.User = prev
		if old != nil {
			_ = old.Close()
		}
		return err
	}
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// UseKey switches the active connection from password auth to key auth. The
// pinned host key carries over so the re-dial verifies the same host.
func (c *Client) UseKey(keyPEM []byte) error {
	signer, err := ssh.ParsePrivateKey(keyPEM)
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}
	c.signer = signer
	old := c.detachConn()
	if err := c.connect(); err != nil {
		if old != nil {
			_ = old.Close()
		}
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
	// rebootAuthFailMax: consecutive auth rejections tolerated before concluding
	// our credentials genuinely no longer work. Early in boot an auth rejection
	// can be TRANSIENT — e.g. a late-mounted /home leaves authorized_keys
	// unreadable while sshd is already answering — and waiting heals it, so a
	// single rejection must not fast-fail. Only a stable streak does; any
	// non-auth error resets the streak.
	const rebootAuthFailMax = 5
	authFails := 0
	for time.Now().Before(deadline) {
		if err := c.connect(); err != nil {
			// Distinguish "box still down" (connection refused / TCP timeout — keep
			// polling) from "box is back UP but rejected our credentials". The latter
			// means the SSH handshake reached the server and it declined every auth
			// method we offered — e.g. a prior A2-danger disabled password auth on a
			// password-only box. A persistent streak of those is NOT a transient
			// reboot state and never heals by waiting, so surface it distinctly
			// instead of looping to a misleading "may be bricked" timeout. (The
			// boot_id gate / 8s courtesy sleep are untouched.)
			if isNoMutualAuth(err) {
				authFails++
				if authFails >= rebootAuthFailMax {
					return "", fmt.Errorf("%w: %v", ErrRebootAuthFailed, err)
				}
			} else {
				authFails = 0
			}
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
	return os.ReadFile(path) // #nosec G304 -- path is operator-supplied (--key flag), not network/attacker input
}
