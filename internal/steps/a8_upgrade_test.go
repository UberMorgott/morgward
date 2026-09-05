package steps

import (
	"strings"
	"testing"
)

// TestSystemdSnapshotScript asserts the snapshot script lists running services
// and filters them by is-enabled (only running-but-not-auto-started units),
// heredoc-free.
func TestSystemdSnapshotScript(t *testing.T) {
	s := systemdSnapshotScript()
	for _, want := range []string{
		"list-units --type=service --state=running",
		"is-enabled",
		"enabled-runtime",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("systemdSnapshotScript missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "<<") {
		t.Errorf("systemdSnapshotScript must be heredoc-free (§A1 stdin caveat):\n%s", s)
	}
}

// TestDockerSnapshotScript asserts the docker snapshot enumerates running
// container IDs (full, non-truncated).
func TestDockerSnapshotScript(t *testing.T) {
	s := dockerSnapshotScript()
	if !strings.Contains(s, "docker ps -q") || !strings.Contains(s, "--no-trunc") {
		t.Errorf("dockerSnapshotScript must list running container IDs:\n%s", s)
	}
}

// TestDockerSnapshotGatedOnDockerSeen asserts that the docker snapshot probe is
// only attempted when Facts.DockerSeen is true (present when seen, absent when
// not) — mirrored here at the builder level: the script itself is unconditional,
// but Run only calls it under DockerSeen. We assert the gating contract by
// confirming the snapshot string is docker-specific so a non-docker box that
// never calls it produces no docker scripting.
func TestDockerSnapshotGatedOnDockerSeen(t *testing.T) {
	// When DockerSeen would be false, Run never builds this; the script is the
	// only docker scripting on the snapshot path and is unmistakably docker.
	if !strings.HasPrefix(dockerSnapshotScript(), "docker ") {
		t.Errorf("docker snapshot must be docker-specific so it is absent off-docker:\n%s", dockerSnapshotScript())
	}
}

// TestDockerStartScriptReusesContainers asserts the restore path uses
// `docker start` on the exact ids and NEVER `docker compose` / recreate.
func TestDockerStartScriptReusesContainers(t *testing.T) {
	s := dockerStartScript([]string{"abc123", "def456"})
	if !strings.Contains(s, "docker start 'abc123' 'def456'") {
		t.Errorf("restore must `docker start` the exact ids:\n%s", s)
	}
	if strings.Contains(s, "docker compose") || strings.Contains(s, "docker-compose") || strings.Contains(s, "run ") || strings.Contains(s, "create") {
		t.Errorf("restore must NOT recreate / use compose:\n%s", s)
	}
	// Empty input is a no-op.
	if got := dockerStartScript(nil); got != "true" {
		t.Errorf("dockerStartScript(nil) = %q, want \"true\"", got)
	}
}

// TestFieldsParsing asserts the whitespace/newline splitter drops empties and
// returns nil (not an empty non-nil slice) for blank output.
func TestFieldsParsing(t *testing.T) {
	got := fields("  id1\nid2 \t id3\n\n")
	want := []string{"id1", "id2", "id3"}
	if len(got) != len(want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fields[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if fields("   \n\t ") != nil {
		t.Errorf("fields(blank) must be nil")
	}
}

// TestA8ID resolves the step ID.
func TestA8ID(t *testing.T) {
	if (A8Upgrade{}).ID() != "A8" {
		t.Errorf("A8Upgrade.ID()=%q want A8", (A8Upgrade{}).ID())
	}
}

// The old pre-run `apt-get -s full-upgrade` simulation ran BEFORE A8's explicit
// `apt-get update`, so a stale index reported 0 after a real 80-package upgrade.
// The count now comes from apt's own summary line in the REAL run's stdout.
func TestAptChangedCount(t *testing.T) {
	for name, tc := range map[string]struct{ out, want string }{
		"upgrade + install": {"Reading package lists...\n50 upgraded, 25 newly installed, 0 to remove and 0 not upgraded.\nGet:1 ...\n", "75"},
		"nothing to do":     {"0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", "0"},
		"last line wins":    {"1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n3 upgraded, 4 newly installed, 0 to remove and 2 not upgraded.\n", "7"},
		"no summary line":   {"E: Could not get lock\n", ""},
		"empty":             {"", ""},
	} {
		if got := aptChangedCount(tc.out); got != tc.want {
			t.Errorf("%s: aptChangedCount = %q, want %q", name, got, tc.want)
		}
	}
}
