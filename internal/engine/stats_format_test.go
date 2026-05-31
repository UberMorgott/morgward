package engine

import (
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/stats"
)

func TestFormatDeltaArrow(t *testing.T) {
	if got := formatDelta("ядро", "6.8.0-31", "6.8.0-45"); !strings.Contains(got, "→") || !strings.Contains(got, "6.8.0-45") {
		t.Fatalf("got %q", got)
	}
}

func TestFormatDeltaSame(t *testing.T) {
	if got := formatDelta("x", "5", "5"); strings.Contains(got, "→") {
		t.Fatalf("should not show arrow when equal: %q", got)
	}
}

func TestFormatDeltaOneSideEmpty(t *testing.T) {
	if got := formatDelta("x", "", "9"); strings.Contains(got, "→") {
		t.Fatalf("should not show arrow when one side empty: %q", got)
	}
	if got := formatDelta("x", "", "9"); !strings.Contains(got, "9") {
		t.Fatalf("want lone value 9: %q", got)
	}
}

func TestStatsLinesNilSnapshots(t *testing.T) {
	if lines := (Summary{}).statsLines(); lines != nil {
		t.Fatalf("want nil, got %v", lines)
	}
}

func TestStatsLinesKernel(t *testing.T) {
	s := Summary{
		Before:       &stats.Snapshot{KernelVer: "6.8.0-31", PkgInstalled: 400},
		After:        &stats.Snapshot{KernelVer: "6.8.0-45", PkgInstalled: 528},
		UpgradedPkgs: 128,
		Results:      []StepResult{{ID: "A8", Title: "Full upgrade"}},
	}
	joined := strings.Join(s.statsLines(), "\n")
	if !strings.Contains(joined, "6.8.0-45") || !strings.Contains(joined, "128") || !strings.Contains(joined, "A8") {
		t.Fatalf("missing data:\n%s", joined)
	}
}

func TestStatsLinesDiskAndNet(t *testing.T) {
	s := Summary{
		Before: &stats.Snapshot{DiskUsedKB: 2 * 1024 * 1024, DiskTotalKB: 20 * 1024 * 1024, ZramActive: false, SpeedMBs: 10, GatewayPingMs: 0.3, InternetPingMs: 26.0},
		After:  &stats.Snapshot{DiskUsedKB: 3 * 1024 * 1024, DiskTotalKB: 20 * 1024 * 1024, ZramActive: true, SpeedMBs: 40, GatewayPingMs: 0.2, InternetPingMs: 24.0},
	}
	joined := strings.Join(s.statsLines(), "\n")
	if !strings.Contains(joined, "→") {
		t.Fatalf("expected a delta arrow somewhere:\n%s", joined)
	}
	if !strings.Contains(joined, "MB/s") {
		t.Fatalf("expected speed row:\n%s", joined)
	}
	if !strings.Contains(joined, "ms") {
		t.Fatalf("expected ping row:\n%s", joined)
	}
}

func TestStatsLinesNetSkipsZero(t *testing.T) {
	s := Summary{
		Before: &stats.Snapshot{SpeedMBs: 0, GatewayPingMs: 0, InternetPingMs: 0},
		After:  &stats.Snapshot{SpeedMBs: 0, GatewayPingMs: 0, InternetPingMs: 0},
	}
	joined := strings.Join(s.statsLines(), "\n")
	if strings.Contains(joined, "MB/s") {
		t.Fatalf("speed row must be skipped when zero:\n%s", joined)
	}
	if strings.Contains(joined, " ms") {
		t.Fatalf("ping row must be skipped when zero:\n%s", joined)
	}
}

func TestStatsLinesPosture(t *testing.T) {
	s := Summary{
		Before: &stats.Snapshot{RootLogin: "yes", KeyOnly: false, FirewallActive: false, Fail2banActive: false, OpenPorts: []string{"22", "80"}},
		After:  &stats.Snapshot{RootLogin: "no", KeyOnly: true, FirewallActive: true, Fail2banActive: true, OpenPorts: []string{"2222"}},
	}
	joined := strings.Join(s.statsLines(), "\n")
	if !strings.Contains(joined, "no") || !strings.Contains(joined, "yes") {
		t.Fatalf("expected root-login before/after:\n%s", joined)
	}
}
