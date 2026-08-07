package tui

import (
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/ui"
)

// TestSummaryHeaderProbeSegment pins the honest probe tally: it renders only when the
// post-run audit produced probes, and disappears entirely otherwise (never "0/0").
func TestSummaryHeaderProbeSegment(t *testing.T) {
	m := model{lang: 0}
	m.summary = engine.Summary{ProbesPassed: 7, ProbesTotal: 9}
	if got := ui.StripControlAndANSI(m.summaryHeader()); !strings.Contains(got, "твиков подтверждено 7/9") {
		t.Fatalf("probe segment missing: %q", got)
	}

	m.summary = engine.Summary{}
	if got := ui.StripControlAndANSI(m.summaryHeader()); strings.Contains(got, "твиков подтверждено") {
		t.Fatalf("probe segment must be hidden without probes: %q", got)
	}
}
