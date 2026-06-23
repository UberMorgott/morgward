package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// newTestHostKey returns a fresh ed25519 ssh.PublicKey and its SHA256:<base64>
// fingerprint string (the ssh-keygen -lf form), for exercising the pin paths
// without a live handshake.
func newTestHostKey(t *testing.T) (ssh.PublicKey, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	sum := sha256.Sum256(sshPub.Marshal())
	return sshPub, "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func TestParseHostKeyPin_None(t *testing.T) {
	pin, err := ParseHostKeyPin("", "")
	if err != nil {
		t.Fatalf("both empty should be nil pin, got err %v", err)
	}
	if pin != nil {
		t.Fatalf("both empty should yield nil pin, got %+v", pin)
	}
}

func TestParseHostKeyPin_Conflict(t *testing.T) {
	if _, err := ParseHostKeyPin("some/path", "SHA256:abc"); err == nil {
		t.Fatal("supplying both sources must error")
	}
}

func TestParseHostKeyPin_BadKnownHostsPath(t *testing.T) {
	if _, err := ParseHostKeyPin(filepath.Join(t.TempDir(), "nope"), ""); err == nil {
		t.Fatal("missing known_hosts path must error")
	}
}

func TestParseHostKeyPin_BadFingerprint(t *testing.T) {
	cases := []string{
		"not-base64-!!!",
		"SHA256:" + base64.RawStdEncoding.EncodeToString([]byte("too-short")),
		"MD5:aa:bb:cc",
	}
	for _, c := range cases {
		if _, err := ParseHostKeyPin("", c); err == nil {
			t.Errorf("fingerprint %q should be rejected", c)
		}
	}
}

// TestHostKeyPin_FingerprintMatch proves the fingerprint pin ACCEPTS the matching
// key and REJECTS any other key with ErrHostKeyMismatch.
func TestHostKeyPin_FingerprintMatch(t *testing.T) {
	key, fp := newTestHostKey(t)
	otherKey, _ := newTestHostKey(t)

	pin, err := ParseHostKeyPin("", fp)
	if err != nil {
		t.Fatalf("parse fingerprint pin: %v", err)
	}
	if pin == nil {
		t.Fatal("expected a non-nil pin")
	}

	if err := pin.verify("host:22", nil, key); err != nil {
		t.Fatalf("matching key must be accepted, got %v", err)
	}
	err = pin.verify("host:22", nil, otherKey)
	if err == nil {
		t.Fatal("non-matching key must be rejected")
	}
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("want ErrHostKeyMismatch, got %v", err)
	}
}

// TestHostKeyPin_FingerprintPrefixOptional confirms the "SHA256:" prefix is
// optional and case-insensitive.
func TestHostKeyPin_FingerprintPrefixOptional(t *testing.T) {
	key, fp := newTestHostKey(t)
	bare := fp[len("SHA256:"):] // strip the prefix
	for _, form := range []string{bare, "sha256:" + bare, "SHA256:" + bare} {
		pin, err := ParseHostKeyPin("", form)
		if err != nil {
			t.Fatalf("parse %q: %v", form, err)
		}
		if err := pin.verify("host:22", nil, key); err != nil {
			t.Errorf("form %q: matching key rejected: %v", form, err)
		}
	}
}

// TestHostKeyPin_KnownHostsMatch writes a real known_hosts entry and proves the
// known_hosts pin ACCEPTS the listed key for the listed host and REJECTS a
// different key.
func TestHostKeyPin_KnownHostsMatch(t *testing.T) {
	key, _ := newTestHostKey(t)
	otherKey, _ := newTestHostKey(t)

	const host = "10.20.30.40"
	const port = 22
	addr := net.JoinHostPort(host, "22")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, key)

	khPath := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	pin, err := ParseHostKeyPin(khPath, "")
	if err != nil {
		t.Fatalf("parse known_hosts pin: %v", err)
	}

	tcp := &net.TCPAddr{IP: net.ParseIP(host), Port: port}
	if err := pin.verify(addr, tcp, key); err != nil {
		t.Fatalf("listed key must be accepted, got %v", err)
	}
	err = pin.verify(addr, tcp, otherKey)
	if err == nil {
		t.Fatal("unlisted key must be rejected")
	}
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("want ErrHostKeyMismatch, got %v", err)
	}
}

// TestHostKeyCallback_PinnedFirstHandshake proves the Client-level callback uses
// the pin on the FIRST handshake: a non-matching first key is refused (no blind
// TOFU), a matching first key is accepted AND pinned so a later identical key
// passes while a later changed key trips ErrHostKeyChanged.
func TestHostKeyCallback_PinnedFirstHandshake(t *testing.T) {
	key, fp := newTestHostKey(t)
	otherKey, _ := newTestHostKey(t)
	pin, err := ParseHostKeyPin("", fp)
	if err != nil {
		t.Fatalf("parse pin: %v", err)
	}

	// A fresh client whose FIRST handshake presents the WRONG key: refused.
	c1 := &Client{pin: pin}
	if err := c1.hostKeyCallback()("host:22", nil, otherKey); !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("pinned first handshake with wrong key: want ErrHostKeyMismatch, got %v", err)
	}

	// A fresh client whose FIRST handshake presents the RIGHT key: accepted + pinned.
	c2 := &Client{pin: pin}
	cb := c2.hostKeyCallback()
	if err := cb("host:22", nil, key); err != nil {
		t.Fatalf("pinned first handshake with right key: want accept, got %v", err)
	}
	if err := cb("host:22", nil, key); err != nil {
		t.Fatalf("same key on reconnect must be accepted, got %v", err)
	}
	if err := cb("host:22", nil, otherKey); !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("changed key after pin: want ErrHostKeyChanged, got %v", err)
	}
}

// TestHostKeyCallback_UnpinnedStillTOFU guards the byte-identical default: with no
// pin, the first key is trusted blindly (TOFU) and only a later CHANGE is refused.
func TestHostKeyCallback_UnpinnedStillTOFU(t *testing.T) {
	key, _ := newTestHostKey(t)
	otherKey, _ := newTestHostKey(t)
	c := &Client{} // no pin
	cb := c.hostKeyCallback()
	if err := cb("host:22", nil, key); err != nil {
		t.Fatalf("TOFU first handshake must accept any key, got %v", err)
	}
	if err := cb("host:22", nil, otherKey); !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("changed key: want ErrHostKeyChanged, got %v", err)
	}
}
