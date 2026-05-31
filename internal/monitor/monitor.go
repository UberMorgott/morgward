// Package monitor samples live CPU/RAM/DISK metrics from the target VPS over its
// OWN sshx connection, independent of the engine's client (sshx.Client is not
// concurrency-safe). It auto-reconnects across reboots and the root->admin
// handoff, emitting Sample values on a channel for the TUI footer.
package monitor

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// sampleInterval is the fixed cadence between successful metric samples.
const sampleInterval = 1 * time.Second

// metricsCmd fetches every metric in one session to minimize session churn.
const metricsCmd = "cat /proc/stat /proc/meminfo; df -P /; cat /proc/cpuinfo"

// ConnInfo carries the connection parameters the sampler needs to dial its own
// SSH session (the key the engine generated/loaded plus candidate users).
type ConnInfo struct {
	Host      string
	Port      int
	User      string
	AdminUser string
	KeyPEM    []byte
	// Password is used to dial only when KeyPEM is empty — the read-only/password
	// audit path. It is never logged.
	Password string
	// KeyGenerated is true only when KeyPEM is a freshly generated ephemeral key
	// (password path). With a user-supplied --key it is false, so consumers can
	// avoid showing the operator their own private key.
	KeyGenerated bool
}

// Sample is one instantaneous snapshot. Percent fields are 0..100; -1 means
// unknown/parse-failed. Connected is false while reconnecting. The absolute
// fields are in KB (0 when unavailable); CPUMHz is 0 when unknown.
type Sample struct {
	CPU, RAM, Disk float64
	Connected      bool
	Err            string

	RAMUsedKB   float64
	RAMTotalKB  float64
	DiskUsedKB  float64
	DiskTotalKB float64

	CPUMHz float64
}

// Stat holds the parsed numeric fields of a /proc/stat `cpu ` line.
type Stat struct {
	Idle  float64 // idle + iowait
	Total float64 // sum of all fields
	OK    bool    // false if the line was absent/unparseable
}

// Sampler owns one persistent SSH connection and the sample loop.
type Sampler struct {
	info   ConnInfo
	cancel context.CancelFunc
}

// New builds a Sampler from connection info; it does not dial yet.
func New(info ConnInfo) *Sampler { return &Sampler{info: info} }

// Run dials, then loops every sampleInterval emitting a Sample on out, with
// auto-reconnect (backoff 1s->5s, cycling candidate users) on transport break.
// It returns when ctx is canceled.
func (s *Sampler) Run(ctx context.Context, out chan<- Sample) {
	ctx, s.cancel = context.WithCancel(ctx)
	defer s.cancel()
	// Run is the sole sender, so closing out on exit is safe; it unblocks the
	// TUI's pending listenStats() reader instead of leaking it after ctx cancel.
	defer close(out)

	users := s.candidates()
	ui := 0 // current candidate-user index
	backoff := time.Second
	const backoffCap = 5 * time.Second

	var cli *sshx.Client
	defer func() {
		if cli != nil {
			cli.Close()
		}
	}()

	var prev Stat // previous /proc/stat cpu snapshot (for the CPU delta)

	emit := func(sm Sample) {
		select {
		case out <- sm:
		case <-ctx.Done():
		}
	}

	for {
		if ctx.Err() != nil {
			return
		}

		// (Re)connect if we have no live client.
		if cli == nil {
			c, err := sshx.Dial(s.info.Host, s.info.Port, users[ui], s.dialPassword(), s.info.KeyPEM)
			if err != nil {
				emit(Sample{Connected: false, Err: err.Error()})
				// Next attempt: rotate user, grow backoff, sleep (ctx-aware).
				ui = (ui + 1) % len(users)
				if !sleepCtx(ctx, backoff) {
					return
				}
				if backoff < backoffCap {
					backoff *= 2
					if backoff > backoffCap {
						backoff = backoffCap
					}
				}
				continue
			}
			cli = c
			backoff = time.Second // reset backoff on a good connection
			prev = Stat{}         // force first CPU tick to report -1
		}

		// One round-trip for all metrics.
		r := cli.Run(metricsCmd)
		if r.Err != nil || r.RC == -1 {
			// Transport break — drop the connection and reconnect.
			cli.Close()
			cli = nil
			errStr := "transport error"
			if r.Err != nil {
				errStr = r.Err.Error()
			}
			emit(Sample{Connected: false, Err: errStr})
			continue
		}

		cur := parseCPUStat(r.Stdout)
		sm := Sample{Connected: true}
		sm.CPU = parseCPU(prev, cur)
		sm.RAM = parseMem(r.Stdout)
		sm.Disk = parseDiskPct(r.Stdout)
		sm.CPUMHz = parseCPUMHz(r.Stdout)
		// Best-effort absolutes: a parse failure of one leaves its fields at zero
		// and must NOT fail the whole sample.
		if used, total, ok := parseMemKB(r.Stdout); ok {
			sm.RAMUsedKB, sm.RAMTotalKB = used, total
		}
		if used, total, ok := parseDiskKB(r.Stdout); ok {
			sm.DiskUsedKB, sm.DiskTotalKB = used, total
		}
		if cur.OK {
			prev = cur
		}
		emit(sm)

		if !sleepCtx(ctx, sampleInterval) {
			return
		}
	}
}

// Stop cancels the sampler loop and (via Run's defer) closes its connection.
func (s *Sampler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// dialPassword returns the password to dial with: the configured password only
// when there is no key (read-only/password audit path); "" when a key is present
// so dialing uses key auth.
func (s *Sampler) dialPassword() string {
	if len(s.info.KeyPEM) == 0 {
		return s.info.Password
	}
	return ""
}

// candidates returns the non-empty, de-duplicated user list to cycle on dial.
func (s *Sampler) candidates() []string {
	var u []string
	for _, c := range []string{s.info.User, s.info.AdminUser} {
		if c == "" {
			continue
		}
		if !slices.Contains(u, c) {
			u = append(u, c)
		}
	}
	if len(u) == 0 {
		u = []string{"root"}
	}
	return u
}

// sleepCtx sleeps for d unless ctx is canceled first; returns false on cancel.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// parseCPUStat extracts the `cpu ` aggregate line from a /proc/stat blob (which
// may be concatenated with /proc/meminfo and df output). idle_all = idle+iowait,
// total = sum of every numeric field.
func parseCPUStat(blob string) Stat {
	for line := range strings.SplitSeq(blob, "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:] // drop the "cpu" label
		if len(fields) < 5 {
			return Stat{}
		}
		var total, idle float64
		for i, f := range fields {
			v, err := strconv.ParseFloat(f, 64)
			if err != nil {
				return Stat{}
			}
			total += v
			// Per /proc/stat: idx 3 = idle, idx 4 = iowait.
			if i == 3 || i == 4 {
				idle += v
			}
		}
		return Stat{Idle: idle, Total: total, OK: true}
	}
	return Stat{}
}

// parseCPU computes busy% between two snapshots: Δbusy/Δtotal*100, where
// busy = total - idle_all. Returns -1 when there is no usable delta (first tick
// or a missing/parse-failed line).
func parseCPU(prev, cur Stat) float64 {
	if !prev.OK || !cur.OK {
		return -1
	}
	dTotal := cur.Total - prev.Total
	if dTotal <= 0 {
		return -1
	}
	dIdle := cur.Idle - prev.Idle
	dBusy := dTotal - dIdle
	pct := dBusy / dTotal * 100
	return clampPct(pct)
}

// parseMem computes RAM used% = (1 - MemAvailable/MemTotal)*100 from a
// /proc/meminfo block. Returns -1 if either field is missing/zero.
func parseMem(blob string) float64 {
	used, total, ok := parseMemKB(blob)
	if !ok {
		return -1
	}
	return clampPct(used / total * 100)
}

// parseMemKB returns used and total memory in KB from /proc/meminfo lines.
// used = MemTotal - MemAvailable. ok is false when either is missing or total<=0.
func parseMemKB(blob string) (usedKB, totalKB float64, ok bool) {
	var total, avail float64
	var haveTotal, haveAvail bool
	for line := range strings.SplitSeq(blob, "\n") {
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total, haveTotal = meminfoVal(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			avail, haveAvail = meminfoVal(line)
		}
	}
	if !haveTotal || !haveAvail || total <= 0 {
		return 0, 0, false
	}
	return total - avail, total, true
}

// meminfoVal pulls the numeric value (kB) from a "Key: NNN kB" line.
func meminfoVal(line string) (float64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseDiskPct returns the Use% of the `/` row from `df -P /` output (POSIX
// format: Filesystem 1024-blocks Used Available Capacity Mounted-on). Returns -1
// if the row or column is absent.
func parseDiskPct(blob string) float64 {
	for line := range strings.SplitSeq(blob, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		if fields[len(fields)-1] != "/" {
			continue
		}
		pctStr := strings.TrimSuffix(fields[len(fields)-2], "%")
		v, err := strconv.ParseFloat(pctStr, 64)
		if err != nil {
			return -1
		}
		return clampPct(v)
	}
	return -1
}

// parseDiskKB returns used and total disk space in KB for the `/` row from
// `df -P /` output. POSIX column layout: Filesystem 1024-blocks Used Available
// Capacity Mounted-on. ok is false when the `/` row is absent or unparseable.
func parseDiskKB(blob string) (usedKB, totalKB float64, ok bool) {
	for line := range strings.SplitSeq(blob, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		if fields[len(fields)-1] != "/" {
			continue
		}
		// Columns from the end: Mounted(-1) Capacity(-2) Available(-3) Used(-4)
		// 1024-blocks(-5).
		total, errT := strconv.ParseFloat(fields[len(fields)-5], 64)
		used, errU := strconv.ParseFloat(fields[len(fields)-4], 64)
		if errT != nil || errU != nil || total <= 0 {
			continue
		}
		return used, total, true
	}
	return 0, 0, false
}

// parseCPUMHz reads the first "cpu MHz" line from /proc/cpuinfo, e.g.
// "cpu MHz\t\t: 2400.000". Returns 0 when absent (ARM/virt may omit it).
func parseCPUMHz(blob string) float64 {
	for line := range strings.SplitSeq(blob, "\n") {
		if !strings.HasPrefix(line, "cpu MHz") {
			continue
		}
		_, after, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(after), 64)
		if err != nil {
			continue
		}
		return v
	}
	return 0
}

// clampPct bounds a percent to [0,100].
func clampPct(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
