package engine

import (
	"testing"

	"github.com/UberMorgott/morgward/internal/steps"
)

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
