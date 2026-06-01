package tui

import (
	"testing"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/tweaks"
)

// TestEveryProbeHasDesc guards completeness of the per-probe descriptions: every
// probe ID that tweaks.Registry can emit — across BOTH modes and with the
// version/IPv6 variants surfaced (Is2604 + HasIPv6, which gate a2.kex_mlkem and
// a1.rules_v6) — must have a non-empty probeDesc in BOTH RU and EN. Without this,
// clicking a gated probe on the Dashboard would fall back to the shared step doc.
func TestEveryProbeHasDesc(t *testing.T) {
	facts := &detect.Facts{ID: "ubuntu", VersionID: "26.04", Is2604: true, HasIPv6: true}

	ids := map[string]struct{}{}
	for _, mode := range []config.Mode{config.ModeSoft, config.ModeStrict} {
		cfg := &config.Config{Mode: mode, Port: 22}
		for _, p := range tweaks.Registry(facts, cfg) {
			ids[p.ID] = struct{}{}
		}
	}

	// Sanity: the union must include the gated probes, else the test isn't proving
	// what it claims.
	for _, gated := range []string{"a1.rules_v6", "a2.kex_mlkem"} {
		if _, ok := ids[gated]; !ok {
			t.Fatalf("registry union missing gated probe %q — test inputs are wrong", gated)
		}
	}

	for id := range ids {
		if d, ok := probeDesc(langRU, id); !ok || d == "" {
			t.Errorf("probe %q has no RU description", id)
		}
		if d, ok := probeDesc(langEN, id); !ok || d == "" {
			t.Errorf("probe %q has no EN description", id)
		}
	}
}
