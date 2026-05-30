package sshx

import (
	"os"
	"strings"
	"testing"
)

func TestGenerateKeyPairWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	kp, err := GenerateKeyPair("morgward@test")
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if !strings.Contains(string(kp.PrivatePEM), "PRIVATE KEY") {
		t.Fatal("no PEM in memory")
	}
	if kp.AuthorizedLine == "" {
		t.Fatal("no authorized line")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("keygen wrote %d files, want 0: %v", len(ents), ents)
	}
}
