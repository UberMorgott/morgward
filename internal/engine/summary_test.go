package engine

import (
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/stats"
	"github.com/UberMorgott/morgward/internal/steps"
)

// statsLines must render BOTH network latency rows (datacenter gateway and
// internet) when either snapshot side has a value, with the measured numbers.
func TestStatsLinesRendersBothPings(t *testing.T) {
	s := Summary{
		Before: &stats.Snapshot{GatewayPingMs: 0.3, InternetPingMs: 26.0},
		After:  &stats.Snapshot{GatewayPingMs: 0.2, InternetPingMs: 24.0},
	}
	joined := strings.Join(s.statsLines(), "\n")
	for _, want := range []string{"задержка ДЦ, ms", "интернет, ms", "0.3", "0.2", "26.0", "24.0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("statsLines output missing %q\n%s", want, joined)
		}
	}
}

func TestSummaryAppliedCount(t *testing.T) {
	s := Summary{Results: []StepResult{
		{ID: "A1", Status: steps.StatusOK},
		{ID: "A2", Status: steps.StatusSkip},
		{ID: "A4", Status: steps.StatusOK},
	}}
	if got := s.Applied(); got != 2 {
		t.Fatalf("applied=%d want 2", got)
	}
	if got := s.Total(); got != 3 {
		t.Fatalf("total=%d want 3", got)
	}
}

func TestApplyResultMarkers(t *testing.T) {
	s := Summary{Results: []StepResult{
		{ID: "A7", Status: steps.StatusOK, Detail: "bloatware purged (5 pkgs), failed units=2 PURGED_COUNT=5"},
		{ID: "A8", Status: steps.StatusOK, Detail: "rebooted (new boot_id abc, system running), firewall reloaded UPGRADED_COUNT=37"},
	}}
	applyResultMarkers(&s)
	if s.PurgedPkgs != 5 {
		t.Fatalf("purged=%d want 5", s.PurgedPkgs)
	}
	if s.UpgradedPkgs != 37 {
		t.Fatalf("upgraded=%d want 37", s.UpgradedPkgs)
	}
	if s.Reboots != 1 {
		t.Fatalf("reboots=%d want 1", s.Reboots)
	}
}

func TestApplyResultMarkersNoRebootOnFail(t *testing.T) {
	s := Summary{Results: []StepResult{
		{ID: "A8", Status: steps.StatusFail, Detail: "full-upgrade failed"},
	}}
	applyResultMarkers(&s)
	if s.Reboots != 0 {
		t.Fatalf("reboots=%d want 0", s.Reboots)
	}
	if s.UpgradedPkgs != 0 {
		t.Fatalf("upgraded=%d want 0", s.UpgradedPkgs)
	}
}

// A KEPT A4 bench (OK=true) lights up the bench line and overrides the snapshot
// throughput delta with the measured PRE→POST medians.
func TestApplyBenchKept(t *testing.T) {
	sum := Summary{Before: &stats.Snapshot{}, After: &stats.Snapshot{}}
	b := &steps.BenchResult{PreMBs: 10, PostMBs: 18, Ratio: 1.8, OK: true}
	applySnapshotBench(&sum, b)
	applyBench(&sum, b)
	if !sum.BenchOK {
		t.Fatalf("BenchOK=false, want true for a kept bench")
	}
	if sum.BenchPostMBs != 18 || sum.BenchRatio != 1.8 {
		t.Fatalf("bench fields not copied: %+v", sum)
	}
	if sum.After.SpeedMBs != 18 {
		t.Fatalf("snapshot After.SpeedMBs=%g want 18", sum.After.SpeedMBs)
	}
}

// A REVERTED A4 bench (OK=false, Reverted=true) must leave BenchOK false so the
// "internet improved" line is omitted, and must NOT override the snapshot speed
// (the regressed measurement is not a real "after" the box is left in).
func TestApplyBenchReverted(t *testing.T) {
	sum := Summary{Before: &stats.Snapshot{SpeedMBs: 0}, After: &stats.Snapshot{SpeedMBs: 0}}
	b := &steps.BenchResult{PreMBs: 15, PostMBs: 2, Ratio: 0.13, OK: false, Reverted: true}
	applySnapshotBench(&sum, b)
	applyBench(&sum, b)
	if sum.BenchOK {
		t.Fatalf("BenchOK=true, want false for a reverted bench")
	}
	if sum.After.SpeedMBs != 0 {
		t.Fatalf("reverted bench must not override snapshot speed; got %g", sum.After.SpeedMBs)
	}
}
