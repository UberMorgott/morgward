package engine

import "testing"

// TestProbeSegment pins the honest CLI tally segment: hidden when the post-run audit
// produced nothing (never "0/0"), and calling out the unconfirmed count when red.
func TestProbeSegment(t *testing.T) {
	if got := (Summary{}).probeSegment(); got != "" {
		t.Fatalf("no probes must render nothing, got %q", got)
	}
	if got := (Summary{ProbesPassed: 9, ProbesTotal: 9}).probeSegment(); got != " · твиков подтверждено 9/9" {
		t.Fatalf("all-green segment: %q", got)
	}
	if got := (Summary{ProbesPassed: 7, ProbesTotal: 9}).probeSegment(); got != " · твиков подтверждено 7/9 (НЕ подтверждено 2)" {
		t.Fatalf("red segment: %q", got)
	}
}
