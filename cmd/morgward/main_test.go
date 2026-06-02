package main

import (
	"reflect"
	"strings"
	"testing"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

// TestUsageDocumentsUpdate asserts the CLI `update` self-update command is
// documented in the usage/help text so operators can discover it.
func TestUsageDocumentsUpdate(t *testing.T) {
	if !strings.Contains(usage, "update") {
		t.Fatalf("usage string does not document the `update` command:\n%s", usage)
	}
}

// TestNewUpdaterHasChecksumValidator confirms self-update is wired with a SHA-256
// ChecksumValidator (F01): without it go-selfupdate would apply an unverified
// binary. The validator field on Updater is unexported, so we assert the gate the
// other way — building the same Config and checking the Validator is a
// ChecksumValidator pointed at checksums.txt, the goreleaser-default asset name.
func TestNewUpdaterHasChecksumValidator(t *testing.T) {
	if checksumsFile != "checksums.txt" {
		t.Fatalf("checksumsFile = %q, want goreleaser default checksums.txt", checksumsFile)
	}

	up, err := newUpdater()
	if err != nil {
		t.Fatalf("newUpdater() error: %v", err)
	}
	if up == nil {
		t.Fatal("newUpdater() returned nil updater")
	}

	cv, ok := newUpdaterConfig().Validator.(*selfupdate.ChecksumValidator)
	if !ok {
		t.Fatalf("validator type = %T, want *selfupdate.ChecksumValidator", newUpdaterConfig().Validator)
	}
	if cv.UniqueFilename != checksumsFile {
		t.Fatalf("validator UniqueFilename = %q, want %q", cv.UniqueFilename, checksumsFile)
	}
}

// TestPartitionArgs proves flags work BEFORE or AFTER the step IDs, and that
// value-taking flags correctly absorb their following token so it is not mistaken
// for a step ID.
func TestPartitionArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantFlag []string
		wantPos  []string
	}{
		{
			name:     "flags after step ids",
			args:     []string{"A4", "A6.5", "--host", "1.2.3.4", "--user", "root", "--assume-yes"},
			wantFlag: []string{"--host", "1.2.3.4", "--user", "root", "--assume-yes"},
			wantPos:  []string{"A4", "A6.5"},
		},
		{
			name:     "flags before step ids",
			args:     []string{"--host", "1.2.3.4", "--user", "root", "--assume-yes", "A4", "A6.5"},
			wantFlag: []string{"--host", "1.2.3.4", "--user", "root", "--assume-yes"},
			wantPos:  []string{"A4", "A6.5"},
		},
		{
			name:     "interleaved",
			args:     []string{"A4", "--host", "1.2.3.4", "A6.5", "--assume-yes"},
			wantFlag: []string{"--host", "1.2.3.4", "--assume-yes"},
			wantPos:  []string{"A4", "A6.5"},
		},
		{
			name:     "equals form keeps value attached",
			args:     []string{"--host=1.2.3.4", "A4"},
			wantFlag: []string{"--host=1.2.3.4"},
			wantPos:  []string{"A4"},
		},
		{
			name:     "value that looks like an id is not a step id",
			args:     []string{"--admin-user", "A4", "A5"},
			wantFlag: []string{"--admin-user", "A4"},
			wantPos:  []string{"A5"},
		},
		{
			name:     "no positionals",
			args:     []string{"--host", "1.2.3.4", "--assume-yes"},
			wantFlag: []string{"--host", "1.2.3.4", "--assume-yes"},
			wantPos:  nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotFlag, gotPos := partitionArgs(c.args)
			if !reflect.DeepEqual(gotFlag, c.wantFlag) {
				t.Errorf("flagArgs = %v, want %v", gotFlag, c.wantFlag)
			}
			if !reflect.DeepEqual(gotPos, c.wantPos) {
				t.Errorf("positional = %v, want %v", gotPos, c.wantPos)
			}
		})
	}
}
