package steps

import (
	"fmt"
	"strings"
	"testing"
)

// sample builds one SAMPLE line in the exact format benchScript emits.
func sample(speed, size float64, url string) string {
	return fmt.Sprintf("SAMPLE %g %g 1.0 %s", speed, size, url)
}

// M03: icmp_ratemask must never set bit 0 (echo reply) — that throttles ping
// replies to icmp_ratelimit (~50/s) and reads as phantom packet loss on every
// uptime monitor. The error types (bits 3,4,11,12,13,14,16) stay throttled.
func TestICMPRatemaskLeavesEchoReplyUnthrottled(t *testing.T) {
	var mask uint
	for line := range strings.SplitSeq(netTuneConf, "\n") {
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "net.ipv4.icmp_ratemask = %d", &mask); err == nil {
			break
		}
	}
	if mask == 0 {
		t.Fatalf("no icmp_ratemask line in netTuneConf:\n%s", netTuneConf)
	}
	if mask&1 != 0 {
		t.Errorf("icmp_ratemask = %d sets bit 0 (echo reply) — ping would be rate-limited", mask)
	}
	if mask != 88088 {
		t.Errorf("icmp_ratemask = %d, want 88088", mask)
	}
}

// M04: the conntrack sizing script must carry all three brownfield guards — leave a
// k8s box alone, never lower an existing value, and persist both the sysctl and the
// modprobe hashsize — and must contain no heredoc (§A1 stdin caveat).
func TestConntrackScriptGuards(t *testing.T) {
	for _, want := range []string{
		"pgrep -x kubelet",                        // kube-proxy owns nf_conntrack
		`[ "$CUR" -gt "$CTMAX" ] && CTMAX="$CUR"`, // never lower an existing value
		conntrackSysctl,
		conntrackModprobe,
		"hashsize=%s",
	} {
		if !strings.Contains(conntrackScript, want) {
			t.Errorf("conntrack script missing %q:\n%s", want, conntrackScript)
		}
	}
	if strings.Contains(conntrackScript, "<<") {
		t.Errorf("no heredoc may appear in a remote script:\n%s", conntrackScript)
	}
	// RAM tiers: 2G -> 65536, 4G -> 131072, anything larger -> 262144.
	for _, want := range []string{"CTMAX=262144", "CTMAX=131072", "CTMAX=65536"} {
		if !strings.Contains(conntrackScript, want) {
			t.Errorf("conntrack script missing RAM tier %q", want)
		}
	}
}

func TestMedianSpeedOddCount(t *testing.T) {
	out := strings.Join([]string{
		sample(10e6, 10e6, "u1"),
		sample(30e6, 10e6, "u1"),
		sample(20e6, 10e6, "u2"),
	}, "\n")
	med, lines := medianSpeed(out)
	if med != 20e6 {
		t.Fatalf("median=%g want 20e6", med)
	}
	if len(lines) != 3 {
		t.Fatalf("want 3 sample lines, got %d: %v", len(lines), lines)
	}
}

func TestMedianSpeedEvenCount(t *testing.T) {
	out := strings.Join([]string{
		sample(10e6, 10e6, "u1"),
		sample(20e6, 10e6, "u1"),
		sample(30e6, 10e6, "u2"),
		sample(40e6, 10e6, "u2"),
	}, "\n")
	med, _ := medianSpeed(out)
	if med != 25e6 { // (20+30)/2
		t.Fatalf("median=%g want 25e6", med)
	}
}

// A lucky spike and a stalled (sub-1MB, discarded) transfer must not move the
// median off the representative samples.
func TestMedianSpeedRobustToOutliers(t *testing.T) {
	out := strings.Join([]string{
		sample(10e6, 10e6, "u1"),
		sample(11e6, 10e6, "u1"),
		sample(12e6, 10e6, "u2"),
		sample(999e6, 10e6, "u2"),    // lucky spike — kept but median-tamed
		sample(500e6, 500_000, "u2"), // stalled (<1MB) — discarded
	}, "\n")
	med, lines := medianSpeed(out)
	// 4 valid samples sorted: 10,11,12,999 → median = (11+12)/2 = 11.5e6.
	// The 999 spike and the discarded sub-1MB sample do not pull it.
	if med != 11.5e6 {
		t.Fatalf("median=%g want 11.5e6", med)
	}
	// one (skipped: ...) line for the sub-1MB transfer
	skipped := 0
	for _, l := range lines {
		if strings.Contains(l, "skipped") {
			skipped++
		}
	}
	if skipped != 1 {
		t.Fatalf("want 1 skipped line, got %d", skipped)
	}
}

func TestMedianSpeedNoValidSamples(t *testing.T) {
	out := strings.Join([]string{
		sample(0, 0, "u1"),
		sample(5e6, 500_000, "u2"),
	}, "\n")
	med, _ := medianSpeed(out)
	if med != 0 {
		t.Fatalf("median=%g want 0 (no valid sample)", med)
	}
}

// benchScript must keep the SAMPLE line byte-identical to the documented format so
// medianSpeed's parser keeps working, and must contain the repeat loop.
func TestBenchScriptFormat(t *testing.T) {
	if !strings.Contains(benchScript, `SAMPLE %{speed_download} %{size_download} %{time_total} $URL`) {
		t.Fatalf("SAMPLE format drifted:\n%s", benchScript)
	}
	if !strings.Contains(benchScript, fmt.Sprintf("seq 1 %d", benchRepeats)) {
		t.Fatalf("repeat loop missing:\n%s", benchScript)
	}
}

// The benchmark must target the box's OWN apt mirror (geo-near, never blocked),
// derived from apt config + codename — never a hardcoded foreign node.
func TestBenchScriptUsesAptMirror(t *testing.T) {
	for _, want := range []string{
		"/etc/apt/sources.list",
		"/dists/",
		"Contents-amd64.gz",
		"UBUNTU_CODENAME",
	} {
		if !strings.Contains(benchScript, want) {
			t.Fatalf("benchScript missing %q:\n%s", want, benchScript)
		}
	}
	for _, banned := range []string{"proof.ovh.net", "tele2", "ovh"} {
		if strings.Contains(benchScript, banned) {
			t.Fatalf("benchScript still references foreign node %q:\n%s", banned, benchScript)
		}
	}
}

func TestMbitFromMBs(t *testing.T) {
	if got := mbitFromMBs(16); got != 128 {
		t.Fatalf("mbitFromMBs(16)=%g want 128", got)
	}
}
