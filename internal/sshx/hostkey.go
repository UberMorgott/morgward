package sshx

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ErrHostKeyMismatch is returned (wrapped via %w) when an operator-supplied
// host-key pin (a --known-hosts file or a --host-fingerprint) is configured and the
// server's FIRST presented host key does NOT satisfy it. Unlike the unpinned TOFU
// path — which trusts the first key blindly — a pinned dial refuses an unexpected
// first key as a possible MITM, the same way ErrHostKeyChanged refuses a CHANGED
// key on a later in-run handshake.
var ErrHostKeyMismatch = errors.New("ssh: server host key does not match the supplied pin (possible MITM)")

// HostKeyPin is an OPT-IN, operator-supplied expectation for the server's host key,
// verified on the FIRST handshake INSTEAD of blind trust-on-first-use. It is nil for
// the default TOFU path (behavior then is byte-identical to before this feature).
//
// Exactly one of the two sources may be set (Parse rejects both); KnownHostsPath
// points at an OpenSSH known_hosts file, Fingerprint is a single key's SHA-256
// digest in the standard "SHA256:<base64>" form (as printed by `ssh-keygen -lf`).
type HostKeyPin struct {
	// knownHostsCB is the verifier built from a known_hosts file, or nil when the
	// pin is fingerprint-based.
	knownHostsCB ssh.HostKeyCallback
	// fingerprint is the raw 32-byte SHA-256 of the marshaled key, or nil when the
	// pin is known_hosts-based.
	fingerprint []byte
	// desc is a short human description of the pin source for error messages.
	desc string
}

// ParseHostKeyPin builds a HostKeyPin from the operator's flags. Both empty => nil
// pin, nil error (the default TOFU path). Supplying BOTH is rejected — they are two
// ways to express the same expectation and combining them is almost always a
// mistake. A bad path or a malformed fingerprint is rejected here, up front, so a
// typo fails before any network I/O instead of silently degrading to TOFU.
func ParseHostKeyPin(knownHostsPath, fingerprint string) (*HostKeyPin, error) {
	knownHostsPath = strings.TrimSpace(knownHostsPath)
	fingerprint = strings.TrimSpace(fingerprint)
	switch {
	case knownHostsPath == "" && fingerprint == "":
		return nil, nil
	case knownHostsPath != "" && fingerprint != "":
		return nil, errors.New("provide only one of --known-hosts or --host-fingerprint, not both")
	case knownHostsPath != "":
		if _, err := os.Stat(knownHostsPath); err != nil {
			return nil, fmt.Errorf("known-hosts file: %w", err)
		}
		cb, err := knownhosts.New(knownHostsPath)
		if err != nil {
			return nil, fmt.Errorf("known-hosts file: %w", err)
		}
		return &HostKeyPin{knownHostsCB: cb, desc: "known_hosts " + knownHostsPath}, nil
	default:
		fp, err := parseSHA256Fingerprint(fingerprint)
		if err != nil {
			return nil, err
		}
		return &HostKeyPin{fingerprint: fp, desc: "fingerprint " + fingerprint}, nil
	}
}

// parseSHA256Fingerprint decodes a "SHA256:<base64>" host-key fingerprint (the form
// ssh-keygen -lf prints) into the raw 32-byte digest. The "SHA256:" prefix is
// optional and case-insensitive; the base64 is the standard unpadded encoding
// OpenSSH uses. Any other shape (wrong length, bad base64, an MD5 colon-hex digest)
// is rejected so the operator gets a clear error rather than a silent never-match.
func parseSHA256Fingerprint(s string) ([]byte, error) {
	body := s
	if i := strings.IndexByte(body, ':'); i >= 0 && strings.EqualFold(body[:i], "SHA256") {
		body = body[i+1:]
	}
	body = strings.TrimSpace(body)
	// OpenSSH prints the SHA-256 fingerprint as unpadded standard base64.
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(body, "="))
	if err != nil {
		return nil, fmt.Errorf("host-fingerprint: not a valid SHA256:<base64> fingerprint: %w", err)
	}
	if len(raw) != sha256.Size {
		return nil, fmt.Errorf("host-fingerprint: decoded length %d, want %d (a SHA-256 fingerprint)", len(raw), sha256.Size)
	}
	return raw, nil
}

// verify checks a presented host key against the pin. Returns nil on a match,
// ErrHostKeyMismatch (wrapped, with the underlying reason) otherwise. hostport is
// the dial target, threaded through so the known_hosts matcher can match host/IP
// entries; the fingerprint path ignores it.
func (p *HostKeyPin) verify(hostport string, remote net.Addr, key ssh.PublicKey) error {
	if p.knownHostsCB != nil {
		if err := p.knownHostsCB(hostport, remote, key); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrHostKeyMismatch, p.desc, err)
		}
		return nil
	}
	got := sha256.Sum256(key.Marshal())
	// Constant-time compare — defensive; both operands are already public.
	if subtle.ConstantTimeCompare(got[:], p.fingerprint) != 1 {
		return fmt.Errorf("%w: %s: presented key SHA256:%s",
			ErrHostKeyMismatch, p.desc, base64.RawStdEncoding.EncodeToString(got[:]))
	}
	return nil
}
