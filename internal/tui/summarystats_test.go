package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/stats"
	"github.com/UberMorgott/morgward/internal/steps"
	"github.com/UberMorgott/morgward/internal/ui"
)

// --- summary stat-row golden --------------------------------------------------
//
// Row selection + suppression are shared with the engine's statsLines
// (stats.SummaryGroups); only the localized labels and the aligned column layout are
// local. The compiler cannot see a reordered, dropped or mislabelled row, so this
// table pins the EXACT rendered strip + stat block for every suppression rule the
// selector exists for: unknown values, one-sided snapshots, equal values, the
// single-valued package/zram rows and the network group that disappears whole. Every
// stats.RowKey appears at least once in BOTH languages, so a wrong entry in sumRowKey
// / sumSectionKey fails here rather than shipping. ANSI is stripped so the table is
// stable across colour profiles.

// summaryStatWant is the expected "strip: …" line plus stat block, keyed by
// "<case>|lang=<n>". Captured at width 100 (the narrow-width column math belongs to
// sumRow and is pinned by TestFramesGolden).
var summaryStatWant = map[string]string{
	"both-nil|lang=0": `strip: `,
	"empty-both|lang=0": `strip: 
ПАКЕТЫ И ЯДРО

ДИСК И ПАМЯТЬ

БЕЗОПАСНОСТЬ
  ssh только по ключу  нет
  файрвол              нет
  fail2ban             нет`,
	"before-only|lang=0": `strip: ОЗУ 1.9G  ·  задержка ДЦ, ms 1.2  ·  интернет, ms 12.5
ПАКЕТЫ И ЯДРО
  ядро  6.8.0-31

ДИСК И ПАМЯТЬ
  диск занято  2.9G/19.1G

СЕТЬ
  скорость, MB/s (до зеркала)  11.8
  задержка ДЦ, ms              1.2
  интернет, ms                 12.5

БЕЗОПАСНОСТЬ
  открытых портов      3
  root-вход            yes
  ssh только по ключу  нет
  файрвол              нет
  fail2ban             нет`,
	"after-only|lang=0": `strip: ОЗУ 1.9G  ·  задержка ДЦ, ms 1.1  ·  интернет, ms 11.0  ·  файрвол да
ПАКЕТЫ И ЯДРО
  ядро  6.8.0-45

ДИСК И ПАМЯТЬ
  диск занято  3.3G/19.1G
  zram         добавлен

СЕТЬ
  скорость, MB/s (до зеркала)  24.5
  задержка ДЦ, ms              1.1
  интернет, ms                 11.0

БЕЗОПАСНОСТЬ
  открытых портов      1
  root-вход            prohibit-password
  ssh только по ключу  нет → да
  файрвол              нет → да
  fail2ban             нет → да`,
	"all-changed|lang=0": `strip: ОЗУ 1.9G  ·  задержка ДЦ, ms 1.2 → 1.1  ·  интернет, ms 12.5 → 11.0  ·  файрвол да
ПАКЕТЫ И ЯДРО
  обновлено пакетов  42
  ядро               6.8.0-31 → 6.8.0-45
  удалено пакетов    7

ДИСК И ПАМЯТЬ
  диск занято  2.9G/19.1G → 3.3G/19.1G
  zram         добавлен

СЕТЬ
  скорость, MB/s (до зеркала)  11.8 → 24.5
  задержка ДЦ, ms              1.2 → 1.1
  интернет, ms                 12.5 → 11.0

БЕЗОПАСНОСТЬ
  открытых портов      3 → 1
  root-вход            yes → prohibit-password
  ssh только по ключу  нет → да
  файрвол              нет → да
  fail2ban             нет → да`,
	"unchanged|lang=0": `strip: ОЗУ 1.9G  ·  задержка ДЦ, ms 1.2  ·  интернет, ms 12.5
ПАКЕТЫ И ЯДРО
  ядро  6.8.0-31

ДИСК И ПАМЯТЬ
  диск занято  2.9G/19.1G

СЕТЬ
  скорость, MB/s (до зеркала)  11.8
  задержка ДЦ, ms              1.2
  интернет, ms                 12.5

БЕЗОПАСНОСТЬ
  открытых портов      3
  root-вход            yes
  ssh только по ключу  нет
  файрвол              нет
  fail2ban             нет`,
	"counts-and-zram|lang=0": `strip: 
ПАКЕТЫ И ЯДРО
  обновлено пакетов  5
  удалено пакетов    3

ДИСК И ПАМЯТЬ
  zram  добавлен

БЕЗОПАСНОСТЬ
  ssh только по ключу  нет
  файрвол              нет
  fail2ban             нет`,
	"zram-already-on|lang=0": `strip: 
ПАКЕТЫ И ЯДРО

ДИСК И ПАМЯТЬ

БЕЗОПАСНОСТЬ
  ssh только по ключу  нет
  файрвол              нет
  fail2ban             нет`,
	"net-partial-gw-only|lang=0": `strip: задержка ДЦ, ms 2.5
ПАКЕТЫ И ЯДРО

ДИСК И ПАМЯТЬ

СЕТЬ
  задержка ДЦ, ms  2.5

БЕЗОПАСНОСТЬ
  ssh только по ключу  нет
  файрвол              нет
  fail2ban             нет`,
	"net-partial-speed-only|lang=0": `strip: 
ПАКЕТЫ И ЯДРО

ДИСК И ПАМЯТЬ

СЕТЬ
  скорость, MB/s (до зеркала)  8.0

БЕЗОПАСНОСТЬ
  ssh только по ключу  нет
  файрвол              нет
  fail2ban             нет`,
	"ports-before-only|lang=0": `strip: 
ПАКЕТЫ И ЯДРО

ДИСК И ПАМЯТЬ

БЕЗОПАСНОСТЬ
  открытых портов      2
  ssh только по ключу  нет
  файрвол              нет
  fail2ban             нет`,
	"disk-total-missing|lang=0": `strip: 
ПАКЕТЫ И ЯДРО

ДИСК И ПАМЯТЬ
  диск занято  586M/1.0G

БЕЗОПАСНОСТЬ
  ssh только по ключу  нет
  файрвол              нет
  fail2ban             нет`,
	"both-nil|lang=1": `strip: `,
	"empty-both|lang=1": `strip: 
PACKAGES & KERNEL

DISK & MEMORY

SECURITY
  ssh key-only  no
  firewall      no
  fail2ban      no`,
	"before-only|lang=1": `strip: RAM 1.9G  ·  datacenter latency, ms 1.2  ·  internet, ms 12.5
PACKAGES & KERNEL
  kernel  6.8.0-31

DISK & MEMORY
  disk used  2.9G/19.1G

NETWORK
  speed, MB/s (to mirror)  11.8
  datacenter latency, ms   1.2
  internet, ms             12.5

SECURITY
  open ports    3
  root login    yes
  ssh key-only  no
  firewall      no
  fail2ban      no`,
	"after-only|lang=1": `strip: RAM 1.9G  ·  datacenter latency, ms 1.1  ·  internet, ms 11.0  ·  firewall yes
PACKAGES & KERNEL
  kernel  6.8.0-45

DISK & MEMORY
  disk used  3.3G/19.1G
  zram       added

NETWORK
  speed, MB/s (to mirror)  24.5
  datacenter latency, ms   1.1
  internet, ms             11.0

SECURITY
  open ports    1
  root login    prohibit-password
  ssh key-only  no → yes
  firewall      no → yes
  fail2ban      no → yes`,
	"all-changed|lang=1": `strip: RAM 1.9G  ·  datacenter latency, ms 1.2 → 1.1  ·  internet, ms 12.5 → 11.0  ·  firewall yes
PACKAGES & KERNEL
  upgraded pkgs  42
  kernel         6.8.0-31 → 6.8.0-45
  purged pkgs    7

DISK & MEMORY
  disk used  2.9G/19.1G → 3.3G/19.1G
  zram       added

NETWORK
  speed, MB/s (to mirror)  11.8 → 24.5
  datacenter latency, ms   1.2 → 1.1
  internet, ms             12.5 → 11.0

SECURITY
  open ports    3 → 1
  root login    yes → prohibit-password
  ssh key-only  no → yes
  firewall      no → yes
  fail2ban      no → yes`,
	"unchanged|lang=1": `strip: RAM 1.9G  ·  datacenter latency, ms 1.2  ·  internet, ms 12.5
PACKAGES & KERNEL
  kernel  6.8.0-31

DISK & MEMORY
  disk used  2.9G/19.1G

NETWORK
  speed, MB/s (to mirror)  11.8
  datacenter latency, ms   1.2
  internet, ms             12.5

SECURITY
  open ports    3
  root login    yes
  ssh key-only  no
  firewall      no
  fail2ban      no`,
	"counts-and-zram|lang=1": `strip: 
PACKAGES & KERNEL
  upgraded pkgs  5
  purged pkgs    3

DISK & MEMORY
  zram  added

SECURITY
  ssh key-only  no
  firewall      no
  fail2ban      no`,
	"zram-already-on|lang=1": `strip: 
PACKAGES & KERNEL

DISK & MEMORY

SECURITY
  ssh key-only  no
  firewall      no
  fail2ban      no`,
	"net-partial-gw-only|lang=1": `strip: datacenter latency, ms 2.5
PACKAGES & KERNEL

DISK & MEMORY

NETWORK
  datacenter latency, ms  2.5

SECURITY
  ssh key-only  no
  firewall      no
  fail2ban      no`,
	"net-partial-speed-only|lang=1": `strip: 
PACKAGES & KERNEL

DISK & MEMORY

NETWORK
  speed, MB/s (to mirror)  8.0

SECURITY
  ssh key-only  no
  firewall      no
  fail2ban      no`,
	"ports-before-only|lang=1": `strip: 
PACKAGES & KERNEL

DISK & MEMORY

SECURITY
  open ports    2
  ssh key-only  no
  firewall      no
  fail2ban      no`,
	"disk-total-missing|lang=1": `strip: 
PACKAGES & KERNEL

DISK & MEMORY
  disk used  586M/1.0G

SECURITY
  ssh key-only  no
  firewall      no
  fail2ban      no`,
}

func TestSummaryStatLinesGolden(t *testing.T) {
	for _, lang := range []Lang{langRU, langEN} {
		for _, c := range summaryStatCases() {
			key := fmt.Sprintf("%s|lang=%d", c.name, lang)
			t.Run(key, func(t *testing.T) {
				want, ok := summaryStatWant[key]
				if !ok {
					t.Fatalf("no expectation for case %q", key)
				}
				m := newModel()
				m.w, m.h = 100, 40
				m.lang = lang
				m.phase = phaseSummary
				m.finished = true
				m.haveSummary = true
				m.summary = c.s
				innerW := innerWidth(m.boxWidth())
				got := []string{"strip: " + ui.SanitizeStreamLine(m.summaryStatStrip(innerW))}
				for _, l := range m.summaryStatLines(innerW) {
					got = append(got, ui.SanitizeStreamLine(l))
				}
				if j := strings.Join(got, "\n"); j != want {
					t.Errorf("summary stats mismatch\n--- got ---\n%s\n--- want ---\n%s", j, want)
				}
			})
		}
	}
}

// summaryStatCases mirrors the engine-side statsLinesCases: the edge cases the
// suppression rules exist for, plus one full run.
func summaryStatCases() []struct {
	name string
	s    engine.Summary
} {
	full := func() *stats.Snapshot {
		return &stats.Snapshot{
			KernelVer: "6.8.0-31", DiskUsedKB: 3_000_000, DiskTotalKB: 20_000_000,
			MemKB: 2_000_000, ZramActive: false,
			OpenPorts: []string{"22", "80", "443"}, RootLogin: "yes",
			KeyOnly: false, FirewallActive: false, Fail2banActive: false,
			GatewayPingMs: 1.24, InternetPingMs: 12.5, SpeedMBs: 11.75,
		}
	}
	after := func() *stats.Snapshot {
		return &stats.Snapshot{
			KernelVer: "6.8.0-45", DiskUsedKB: 3_500_000, DiskTotalKB: 20_000_000,
			MemKB: 2_000_000, ZramActive: true,
			OpenPorts: []string{"22"}, RootLogin: "prohibit-password",
			KeyOnly: true, FirewallActive: true, Fail2banActive: true,
			GatewayPingMs: 1.11, InternetPingMs: 11.02, SpeedMBs: 24.5,
		}
	}
	return []struct {
		name string
		s    engine.Summary
	}{
		{"both-nil", engine.Summary{OK: 3, Skip: 1, Fail: 0, Reboots: 1}},
		{"empty-both", engine.Summary{Before: &stats.Snapshot{}, After: &stats.Snapshot{}}},
		{"before-only", engine.Summary{Before: full()}},
		{"after-only", engine.Summary{After: after()}},
		{"all-changed", engine.Summary{
			OK: 9, Skip: 2, Fail: 1, Reboots: 1, UpgradedPkgs: 42, PurgedPkgs: 7,
			Before: full(), After: after(),
			Results: []engine.StepResult{
				{ID: "A1", Title: "Firewall", Status: steps.StatusOK},
				{ID: "A5", Title: "Sysctl", Status: steps.StatusSkip, Detail: "already applied"},
				{ID: "A9", Title: "Audit", Status: steps.StatusFail},
				{ID: "A7", Title: "Purge", Status: steps.StatusSkip},
			},
		}},
		{"unchanged", engine.Summary{Before: full(), After: full()}},
		{"counts-and-zram", engine.Summary{
			UpgradedPkgs: 5, PurgedPkgs: 3,
			Before: &stats.Snapshot{ZramActive: false},
			After:  &stats.Snapshot{ZramActive: true},
		}},
		{"zram-already-on", engine.Summary{
			Before: &stats.Snapshot{ZramActive: true},
			After:  &stats.Snapshot{ZramActive: true},
		}},
		{"net-partial-gw-only", engine.Summary{
			Before: &stats.Snapshot{},
			After:  &stats.Snapshot{GatewayPingMs: 2.5},
		}},
		{"net-partial-speed-only", engine.Summary{
			Before: &stats.Snapshot{SpeedMBs: 8.0},
			After:  &stats.Snapshot{},
		}},
		{"ports-before-only", engine.Summary{
			Before: &stats.Snapshot{OpenPorts: []string{"22", "80"}},
			After:  &stats.Snapshot{},
		}},
		{"disk-total-missing", engine.Summary{
			Before: &stats.Snapshot{DiskUsedKB: 500_000},
			After:  &stats.Snapshot{DiskUsedKB: 600_000, DiskTotalKB: 1_048_576},
		}},
	}
}
