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

// An unconfirmed OS version (both Is2404 and Is2604 false — an off-target release
// or a glitched os-release probe) must degrade to the 24.04 probe set, NOT claim the
// 26.04-only mlkem KEX we never emitted. The engine warns about the fallback; the
// audit must not then show a red "not applied" row for a tweak morgward
// deliberately skipped.
func TestRegistryUnknownVersionMatchesConservativeSet(t *testing.T) {
	cfg := &config.Config{Port: 22}
	unknown := ids(Registry(&detect.Facts{}, cfg))
	if _, ok := unknown["a2.kex_mlkem"]; ok {
		t.Error("unconfirmed version must not probe the 26.04-only mlkem KEX")
	}
	// Everything the 24.04 set probes must also be probed on an unconfirmed box,
	// EXCEPT the knobs A2 itself version-gates and therefore never wrote there — the
	// audit must mirror the apply, not assert more than it applied.
	versionGated := map[string]bool{"a2.unused_timeout": true, "a2.channel_timeout": true}
	for id := range ids(Registry(&detect.Facts{Is2404: true}, cfg)) {
		if versionGated[id] {
			continue
		}
		if _, ok := unknown[id]; !ok {
			t.Errorf("unconfirmed version registry is missing %q from the 24.04 set", id)
		}
	}
	// …and the gated ones must indeed be absent, matching cryptoBlock's own gate.
	for id := range versionGated {
		if _, ok := unknown[id]; ok {
			t.Errorf("unconfirmed version must not probe the sshd-9.2-only %q", id)
		}
	}
}

// Each new probe must be registered under exactly the gate its apply uses, so the
// audit never asserts a knob morgward deliberately did not write.
func TestNewProbeGates(t *testing.T) {
	cfg := &config.Config{Port: 22}

	// M04: conntrack is sized only on a box that actually tracks connections.
	plain := ids(Registry(&detect.Facts{Is2404: true}, cfg))
	if _, ok := plain["a4.conntrack"]; ok {
		t.Error("a plain box must not be probed for conntrack sizing")
	}
	for _, f := range []*detect.Facts{{Is2404: true, DockerSeen: true}, {Is2404: true, NatRules: true}} {
		if _, ok := ids(Registry(f, cfg))["a4.conntrack"]; !ok {
			t.Errorf("docker=%v nat=%v must be probed for conntrack sizing", f.DockerSeen, f.NatRules)
		}
	}

	// M07: ChannelTimeout is greenfield-only; UnusedConnectionTimeout is universal
	// on sshd >= 9.2.
	brown := ids(Registry(&detect.Facts{Is2404: true, Greenfield: false}, cfg))
	if _, ok := brown["a2.channel_timeout"]; ok {
		t.Error("brownfield must not be probed for ChannelTimeout (never applied there)")
	}
	if _, ok := brown["a2.unused_timeout"]; !ok {
		t.Error("UnusedConnectionTimeout is applied on every sshd >= 9.2 box, so it must be probed")
	}
	if _, ok := ids(Registry(&detect.Facts{Is2404: true, Greenfield: true}, cfg))["a2.channel_timeout"]; !ok {
		t.Error("greenfield must be probed for ChannelTimeout")
	}

	// The INFO-only probes must never be scored as failures.
	for _, id := range []string{"a5.apparmor_userns", "a5.lockdown", "a6.tmp_tmpfs", "a2.socket_activation"} {
		p, ok := plain[id]
		if !ok {
			t.Errorf("probe %q missing from the registry", id)
			continue
		}
		if !p.Informational {
			t.Errorf("probe %q reports state morgward does not change — it must be Informational", id)
		}
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

// C2: a probe that could not be EXECUTED is unknown, not "not applied" — Tally
// must count it as neither applied nor applicable, so an unreachable box cannot
// deflate the "applied" score into a wall of false failures.
func TestTallyExcludesErroredProbes(t *testing.T) {
	rs := []Result{
		{Probe: Probe{ID: "ok"}, Applied: true},
		{Probe: Probe{ID: "missing"}, Applied: false},
		{Probe: Probe{ID: "unknown"}, Applied: false, Err: "probe round-trip failed: SSH transport lost"},
		{Probe: Probe{ID: "info"}, Applied: false, Informational: true},
	}
	passed, total := Tally(rs)
	if passed != 1 || total != 2 {
		t.Errorf("Tally = (%d,%d), want (1,2) — errored and informational rows must not count", passed, total)
	}
	if got := firstErr(rs); got == "" {
		t.Error("firstErr must surface the probe error for the log summary")
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
