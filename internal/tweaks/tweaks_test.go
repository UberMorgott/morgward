package tweaks

import (
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
)

// TestA1SSHAcceptDportAnchored is the regression guard for the dport-prefix
// false-match bug: the a1.ssh_accept probe greps `iptables -S` for the SSH port,
// and an unanchored `--dport 22` substring wrongly matches a :2222 rule. The probe
// runs remotely, so the behavioral proof is the CLI repro documented on the branch;
// this test only pins the `( |$)` anchor into the generated Cmd against silent
// regression.
func TestA1SSHAcceptDportAnchored(t *testing.T) {
	// Empty FirewallMgr ⇒ ManagesIPTables() true ⇒ A1 probes are emitted.
	ps := ids(Registry(&detect.Facts{Is2404: true}, &config.Config{Port: 22}))
	p, ok := ps["a1.ssh_accept"]
	if !ok {
		t.Fatal("registry must contain a1.ssh_accept on a managed-iptables box")
	}
	if !strings.Contains(p.Cmd, "( |$)") {
		t.Errorf("a1.ssh_accept Cmd lost its dport anchor (regression):\n%s", p.Cmd)
	}
}

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

func TestRegistryAccessProbes(t *testing.T) {
	facts := &detect.Facts{Is2604: false, Is2404: true, HasIPv6: false}

	ps := ids(Registry(facts, &config.Config{}))

	// The dropped (formerly strict-only) A10 extras must be absent.
	for _, id := range []string{"a10.blacklist", "a10.devshm"} {
		if _, ok := ps[id]; ok {
			t.Errorf("registry must not contain dropped probe %s", id)
		}
	}

	// The access-policy probes reflect the image default and are Informational.
	pa, ok := ps["a2.passauth"]
	if !ok {
		t.Fatal("registry must contain a2.passauth")
	}
	if !pa.Want("yes") {
		t.Error("a2.passauth should accept the image default (yes)")
	}
	if !pa.Informational {
		t.Error("a2.passauth must be Informational")
	}
}

func TestRegistryVersionFiltering(t *testing.T) {
	cfg := &config.Config{}

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
	cfg := &config.Config{}
	no6 := ids(Registry(&detect.Facts{Is2404: true, HasIPv6: false}, cfg))
	if _, ok := no6["a1.rules_v6"]; ok {
		t.Error("registry must omit a1.rules_v6 when HasIPv6 is false")
	}
	with6 := ids(Registry(&detect.Facts{Is2404: true, HasIPv6: true}, cfg))
	if _, ok := with6["a1.rules_v6"]; !ok {
		t.Error("registry must include a1.rules_v6 when HasIPv6 is true")
	}
}

// TestA2SafeProbesInformational asserts the access-policy probes the default
// (safe) path deliberately leaves at the image default are marked Informational —
// so the audit does NOT render them as "не применён"/red until A2-danger.
func TestA2SafeProbesInformational(t *testing.T) {
	facts := &detect.Facts{Is2404: true}
	ps := ids(Registry(facts, &config.Config{Port: 22}))
	for _, id := range []string{"a2.allowgroups", "a2.permitroot", "a2.passauth"} {
		p, ok := ps[id]
		if !ok {
			t.Errorf("probe %q missing from registry", id)
			continue
		}
		if !p.Informational {
			t.Errorf("probe %q must be Informational on the safe/default path", id)
		}
	}
}

// TestA10ExtrasRemoved guards the locked decision: the former strict-only A10
// extras (module blacklist, /dev/shm) are dropped entirely.
func TestA10ExtrasRemoved(t *testing.T) {
	facts := &detect.Facts{Is2404: true}
	ps := ids(Registry(facts, &config.Config{Port: 22}))
	for _, id := range []string{"a10.blacklist", "a10.devshm"} {
		if _, ok := ps[id]; ok {
			t.Errorf("dropped probe %q must be removed", id)
		}
	}
}

// TestRpFilterCoexist asserts the rp_filter probe accepts the value A5 actually
// applies: strict (=1) when not forwarding, loose (=2) when forwarding/routing is
// active — so a correctly-coexisting box does not report a false failure.
func TestRpFilterCoexist(t *testing.T) {
	cfg := &config.Config{Port: 22}

	noFwd := ids(Registry(&detect.Facts{Is2404: true, Forwarding: false}, cfg))["a5.rp_filter"]
	if !noFwd.Want("1") {
		t.Error("non-forwarding box: rp_filter probe should accept 1")
	}
	if noFwd.Want("2") {
		t.Error("non-forwarding box: rp_filter probe should reject 2")
	}

	fwd := ids(Registry(&detect.Facts{Is2404: true, Forwarding: true}, cfg))["a5.rp_filter"]
	if !fwd.Want("2") {
		t.Error("forwarding box: rp_filter probe should accept 2 (loose)")
	}
	if fwd.Want("1") {
		t.Error("forwarding box: rp_filter probe should reject 1")
	}
}

// TestNonInformationalProbesStayHard sanity-checks ordinary probes are NOT
// flagged informational (only the access-policy ones are relaxed).
func TestNonInformationalProbesStayHard(t *testing.T) {
	facts := &detect.Facts{Is2404: true}
	ps := ids(Registry(facts, &config.Config{Port: 22}))
	if p, ok := ps["a1.input_drop"]; !ok || p.Informational {
		t.Error("a1.input_drop must exist and not be informational")
	}
}
