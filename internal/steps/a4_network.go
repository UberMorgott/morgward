package steps

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A4Network implements §A4: socket buffer / backlog tuning, BBR + fq, virtio I/O
// scheduler, and a before/after throughput benchmark against the box's OWN apt
// mirror (geo-near, never blocked). Throughput is INFORMATIONAL: the tuning is
// safe standard server tuning and is applied unconditionally; only a failure of
// BBR to activate is a hard fail. Context-dependent knobs (64 MiB buffers,
// conntrack, tcp_tw_reuse, MTU) are SKIPPED in universal Phase A per the rule.
type A4Network struct{}

func (A4Network) ID() string    { return "A4" }
func (A4Network) Title() string { return "Network tuning (BBR, buffers, I/O sched) + benchmark" }

const netTuneConf = `net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216
net.core.netdev_max_backlog = 16384
net.core.somaxconn = 8192
net.ipv4.tcp_max_syn_backlog = 8192
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_mtu_probing = 1
vm.dirty_ratio = 10
vm.dirty_background_ratio = 5
`

const bbrConf = `net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
`

// benchRepeats is how many times the endpoint is downloaded per benchmark pass.
// A single curl is too noisy (a lucky spike or a stalled transfer can dominate),
// so we take several samples and use the median.
const benchRepeats = 4

// benchScript benchmarks the box's OWN apt mirror — the channel the box actually
// uses, so it is geo-near and never blocked, and auto-adapts to wherever the host
// lives (RU mirror on an RU box, archive.ubuntu.com geo-CDN otherwise). No
// hardcoded foreign nodes.
//
// It derives BASE from the real apt config (the first http(s) .../ubuntu URL in
// the deb822 sources or legacy sources.list, falling back to the first http(s)
// URL there) and CODENAME from /etc/os-release, then downloads
// $BASE/dists/$CODENAME/Contents-amd64.gz (~57 MB) benchRepeats times.
//
// It emits machine-parseable samples: SAMPLE <speed> <size> <time> <url>, the
// same format medianSpeed parses. --max-time bounds the worst case to
// benchRepeats × 30 s. The script is piped to bash over stdin, so it uses no
// heredoc (§A1 stdin caveat).
var benchScript = fmt.Sprintf(`BASE=$(grep -rhoE 'https?://[^ ]+/ubuntu' /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list 2>/dev/null | head -1)
if [ -z "$BASE" ]; then
  BASE=$(grep -rhoE 'https?://[^ ]+' /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list 2>/dev/null | head -1)
fi
CODENAME=$(. /etc/os-release; echo ${UBUNTU_CODENAME:-$VERSION_CODENAME})
URL="$BASE/dists/$CODENAME/Contents-amd64.gz"
for i in $(seq 1 %d); do
  curl -o /dev/null -s -w "SAMPLE %%{speed_download} %%{size_download} %%{time_total} $URL\n" --max-time 30 "$URL" 2>/dev/null || echo "SAMPLE 0 0 0 $URL"
done`, benchRepeats)

// minValidBytes is the floor for a sample to count: the Contents-amd64.gz file is
// tens of MB, so anything under 1 MB is a truncated/reset transfer (a fluke).
const minValidBytes = 1_000_000

// medianSpeed parses benchScript output, collects EVERY valid speed (bytes/s) of a
// sample that transferred at least minValidBytes, and returns their median plus a
// human line per sample (valid or skipped). The median is robust to both a lucky
// spike and a single stalled transfer, which a plain max/avg is not. Returns 0 when
// no sample was valid (network noise — the caller treats that as "not measured").
func medianSpeed(out string) (median float64, lines []string) {
	var speeds []float64
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(l))
		if len(f) < 5 || f[0] != "SAMPLE" {
			continue
		}
		speed, _ := strconv.ParseFloat(f[1], 64)
		size, _ := strconv.ParseFloat(f[2], 64)
		url := f[4]
		if size < minValidBytes {
			lines = append(lines, fmt.Sprintf("(skipped: only %.0f KB transferred) %s", size/1000, url))
			continue
		}
		lines = append(lines, fmt.Sprintf("%.1f MB/s (%.1f MB in %ss) %s", speed/1e6, size/1e6, f[3], url))
		speeds = append(speeds, speed)
	}
	if len(speeds) == 0 {
		return 0, lines
	}
	sort.Float64s(speeds)
	n := len(speeds)
	if n%2 == 1 {
		median = speeds[n/2]
	} else {
		median = (speeds[n/2-1] + speeds[n/2]) / 2
	}
	return median, lines
}

// mbitFromMBs converts a MB/s (decimal megabytes) figure to Mbit/s, so the report
// answers the "16 MB/s vs gigabit?" confusion (16 MB/s = 128 Mbit/s).
func mbitFromMBs(mbs float64) float64 { return mbs * 8 }

func (a A4Network) Run(ctx *Context) (Status, string, error) {
	// Current congestion control, for logging only (no revert path anymore).
	cc := ctx.Cli.Run("sysctl -n net.ipv4.tcp_congestion_control").Out()
	if cc == "" {
		cc = "cubic"
	}
	ctx.Log.Detail("baseline congestion_control=%s; running pre-tuning download…", cc)
	preMedian, preLines := medianSpeed(ctx.Cli.Run(benchScript).Stdout)
	for _, l := range preLines {
		ctx.Log.Detail("PRE %s", l)
	}

	// Apply tuning unconditionally — BBR+fq, socket buffers, virtio I/O=none are
	// safe standard server tuning. Delivered via base64 file/exec fragments (never
	// a heredoc; the script is piped to bash over stdin — §A1 stdin caveat).
	apply := putFile("/etc/sysctl.d/99-net-tune.conf", netTuneConf, "0644") +
		putFile("/etc/sysctl.d/99-bbr.conf", bbrConf, "0644") +
		"echo tcp_bbr > /etc/modules-load.d/bbr.conf\n" +
		"modprobe tcp_bbr 2>/dev/null || true\n" +
		"sysctl --system >/dev/null 2>&1\n"

	// virtio I/O scheduler = none.
	if disk := ctx.Cli.Run(`lsblk -dno NAME | grep -m1 '^vd' || true`).Out(); disk != "" {
		apply += putFile("/etc/udev/rules.d/60-io-scheduler.rules",
			"ACTION==\"add|change\", KERNEL==\"vd[a-z]*\", ATTR{queue/scheduler}=\"none\"\n", "0644") +
			"udevadm control --reload-rules && udevadm trigger\n" +
			fmt.Sprintf("echo none > /sys/block/%s/queue/scheduler 2>/dev/null || true\n", disk)
	}

	if r := ctx.Cli.Sudo(apply); r.RC != 0 {
		return StatusFail, "network tuning apply failed: " + firstLine(r.Stderr), nil
	}

	// Verify BBR effective — the ONLY hard-fail condition.
	avail := ctx.Cli.Run("sysctl -n net.ipv4.tcp_available_congestion_control").Out()
	now := ctx.Cli.Run("sysctl -n net.ipv4.tcp_congestion_control").Out()
	if !strings.Contains(avail, "bbr") || now != "bbr" {
		return StatusFail, fmt.Sprintf("BBR not effective (available=%q current=%q)", avail, now), nil
	}

	// POST-tuning benchmark — informational only.
	ctx.Log.Detail("running post-tuning download…")
	postMedian, postLines := medianSpeed(ctx.Cli.Run(benchScript).Stdout)
	for _, l := range postLines {
		ctx.Log.Detail("POST %s", l)
	}

	const base = "BBR+fq active, 16 MiB buffers, virtio I/O=none"

	// Throughput is INFORMATIONAL — it never gates keep/revert (BBR vs CUBIC is
	// statistically identical on a clean link, and providers hard-cap bandwidth,
	// so any delta is noise). If we have no comparable pair, keep and say so.
	if preMedian <= 0 || postMedian <= 0 {
		ctx.Log.Detail("throughput: not measured (mirror unreachable)")
		return StatusOK, base + "; throughput not measured", nil
	}

	preMBs, postMBs := preMedian/1e6, postMedian/1e6
	ratio := postMedian / preMedian
	// Carry the comparable pair out for the run summary. OK=true so renderers show
	// the speed; this is never a revert trigger now, so Reverted stays false.
	ctx.Bench = &BenchResult{PreMBs: preMBs, PostMBs: postMBs, Ratio: ratio, OK: true}

	bench := fmt.Sprintf("median %.1f→%.1f MB/s (%.0f→%.0f Mbit/s)",
		preMBs, postMBs, mbitFromMBs(preMBs), mbitFromMBs(postMBs))
	ctx.Log.Detail("throughput: %s", bench)
	return StatusOK, base + "; " + bench, nil
}
