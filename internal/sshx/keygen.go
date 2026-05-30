package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
)

// KeyPair is a generated ed25519 SSH key pair.
type KeyPair struct {
	PrivatePEM     []byte // OpenSSH-format private key
	AuthorizedLine string // single-line authorized_keys entry (no trailing newline)
	PrivatePath    string // where the private key was written
}

// GenerateKeyPair creates an ed25519 key pair and writes the private key to
// privPath with 0600 perms (the public line is returned for push to the box).
func GenerateKeyPair(privPath, comment string) (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ed25519 keygen: %w", err)
	}

	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(block)

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("ssh public key: %w", err)
	}
	authLine := string(ssh.MarshalAuthorizedKey(sshPub))
	// MarshalAuthorizedKey appends a newline; append the comment, trim later.
	if comment != "" {
		authLine = trimNL(authLine) + " " + comment
	} else {
		authLine = trimNL(authLine)
	}

	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write private key %s: %w", privPath, err)
	}

	return &KeyPair{PrivatePEM: privPEM, AuthorizedLine: authLine, PrivatePath: privPath}, nil
}

// PublicLineFromPEM derives the authorized_keys line from a private key PEM
// (used when the operator supplies their own key instead of generating one).
func PublicLineFromPEM(pemBytes []byte, comment string) (string, error) {
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	line := trimNL(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if comment != "" {
		line += " " + comment
	}
	return line, nil
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
