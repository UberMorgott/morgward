package steps

import (
	"fmt"
	"strconv"
	"strings"
)

// A4Network implements §A4: socket buffer / backlog tuning, BBR + fq, virtio I/O
// scheduler, and a before/after throughput benchmark vs the captured baseline.
// Context-dependent knobs (64 MiB buffers, conntrack, tcp_tw_reuse, MTU) are
// SKIPPED in universal Phase A per the decision rule.
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

// benchScript emits machine-parseable samples: SAMPLE <speed> <size> <time> <url>.
const benchScript = `for url in https://proof.ovh.net/files/10Mb.dat http://speedtest.tele2.net/10MB.zip; do
  curl -o /dev/null -s -w "SAMPLE %{speed_download} %{size_download} %{time_total} $url\n" --max-time 30 "$url" 2>/dev/null || echo "SAMPLE 0 0 0 $url"
done`

// minValidBytes is the floor for a sample to count: the test files are ~10 MB,
// so anything under 1 MB is a truncated/reset transfer (a throughput fluke).
const minValidBytes = 1_000_000

// bestSpeed parses benchScript output, returns the highest valid speed (bytes/s)
// and a human line per sample (valid or skipped).
func bestSpeed(out string) (best float64, lines []string) {
	for l := range strings.SplitSeq(out, "\n") {
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
		if speed > best {
			best = speed
		}
	}
	return best, lines
}

func (a A4Network) Run(ctx *Context) (Status, string, error) {
	// PRE-tuning baseline.
	cc := ctx.Cli.Run("sysctl -n net.ipv4.tcp_congestion_control").Out()
	ctx.Log.Detail("baseline congestion_control=%s; running pre-tuning download…", cc)
	preBest, preLines := bestSpeed(ctx.Cli.Run(benchScript).Stdout)
	for _, l := range preLines {
		ctx.Log.Detail("PRE %s", l)
	}

	// Apply tuning.
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

	// Verify BBR effective.
	avail := ctx.Cli.Run("sysctl -n net.ipv4.tcp_available_congestion_control").Out()
	now := ctx.Cli.Run("sysctl -n net.ipv4.tcp_congestion_control").Out()
	if !strings.Contains(avail, "bbr") || now != "bbr" {
		return StatusFail, fmt.Sprintf("BBR not effective (available=%q current=%q)", avail, now), nil
	}

	// POST-tuning benchmark.
	ctx.Log.Detail("running post-tuning download…")
	postBest, postLines := bestSpeed(ctx.Cli.Run(benchScript).Stdout)
	for _, l := range postLines {
		ctx.Log.Detail("POST %s", l)
	}

	bench := "benchmark logged"
	if preBest > 0 && postBest > 0 {
		preMBs, postMBs := preBest/1e6, postBest/1e6
		ratio := postBest / preBest
		bench = fmt.Sprintf("best %.1f→%.1f MB/s (%.2fx)", preMBs, postMBs, ratio)
		ctx.Log.Detail("throughput: %s", bench)
		// Carry the comparable pair out of the step so the engine can lift it into
		// the final run Summary (CLI + TUI render it from there).
		ctx.Bench = &BenchResult{PreMBs: preMBs, PostMBs: postMBs, Ratio: ratio, OK: true}
	} else {
		ctx.Log.Detail("throughput: no valid sample pair (network noise) — knobs applied regardless")
	}

	return StatusOK, "BBR + fq active, 16 MiB buffers, virtio I/O=none; " + bench, nil
}
