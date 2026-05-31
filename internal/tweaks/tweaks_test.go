package tweaks

import (
	"testing"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
)

func TestPredicates(t *testing.T) {
	if !eq("1")(" 1 ") {
		t.Fatal("eq should trim and match")
	}
	if eq("1")("0") {
		t.Fatal("eq should reject mismatch")
	}
	if !has("bbr")("tcp bbr cubic") {
		t.Fatal("has should find substring")
	}
	if has("bbr")("cubic") {
		t.Fatal("has should reject absent substring")
	}
}

func ids(ps []Probe) map[string]Probe {
	m := make(map[string]Probe, len(ps))
	for _, p := range ps {
		m[p.ID] = p
	}
	return m
}

func TestRegistryModeFiltering(t *testing.T) {
	facts := &detect.Facts{Is2604: false, Is2404: true, HasIPv6: false}

	soft := ids(Registry(facts, &config.Config{Mode: config.ModeSoft}))
	strict := ids(Registry(facts, &config.Config{Mode: config.ModeStrict}))

	// strict-only probes must be absent in soft, present in strict.
	for _, id := range []string{"a10.blacklist", "a10.devshm"} {
		if _, ok := soft[id]; ok {
			t.Errorf("soft registry must not contain strict-only probe %s", id)
		}
		if _, ok := strict[id]; !ok {
			t.Errorf("strict registry must contain %s", id)
		}
	}

	// password-auth probe present in both, but its Want differs by mode.
	sp, ok := soft["a2.passauth"]
	if !ok {
		t.Fatal("soft must contain a2.passauth")
	}
	stp := strict["a2.passauth"]
	if !sp.Want("yes") {
		t.Error("soft a2.passauth should accept yes")
	}
	if stp.Want("yes") {
		t.Error("strict a2.passauth should reject yes")
	}
	if !stp.Want("no") {
		t.Error("strict a2.passauth should accept no")
	}
}

func TestRegistryVersionFiltering(t *testing.T) {
	cfg := &config.Config{Mode: config.ModeSoft}

	noble := ids(Registry(&detect.Facts{Is2404: true}, cfg))
	resolute := ids(Registry(&detect.Facts{Is2604: true}, cfg))

	if _, ok := noble["a2.kex_mlkem"]; ok {
		t.Error("24.04 registry must not contain 26.04-only a2.kex_mlkem")
	}
	if _, ok := resolute["a2.kex_mlkem"]; !ok {
		t.Error("26.04 registry must contain a2.kex_mlkem")
	}
}

func TestRegistryIPv6Filtering(t *testing.T) {
	cfg := &config.Config{Mode: config.ModeSoft}
	no6 := ids(Registry(&detect.Facts{Is2404: true, HasIPv6: false}, cfg))
	if _, ok := no6["a1.rules_v6"]; ok {
		t.Error("registry must omit a1.rules_v6 when HasIPv6 is false")
	}
	with6 := ids(Registry(&detect.Facts{Is2404: true, HasIPv6: true}, cfg))
	if _, ok := with6["a1.rules_v6"]; !ok {
		t.Error("registry must include a1.rules_v6 when HasIPv6 is true")
	}
}
