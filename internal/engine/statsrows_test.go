package engine

import (
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/stats"
	"github.com/UberMorgott/morgward/internal/steps"
)

// --- statsLines row-selection golden -----------------------------------------
//
// Row selection + suppression are shared with the TUI summary (stats.SummaryGroups);
// only the RU labels and the flat "  label  before → after" layout are local. The
// compiler cannot see a reordered, dropped or mislabelled row, so this table pins the
// EXACT rendered block for every suppression rule the selector exists for: unknown
// values, one-sided snapshots, equal values, the single-valued package/zram rows and
// the network group that disappears whole. Every stats.RowKey appears at least once,
// so a wrong entry in statsRowLabel fails here rather than shipping.

// statsLinesWant is the expected block per case, keyed by case name.
var statsLinesWant = map[string]string{
	"both-nil": ``,
	"empty-both": `СТАТИСТИКА — применено 0/0 · пропущено 0 · ошибок 0 · перезагрузок 0
ПАКЕТЫ И ЯДРО:
ДИСК И ПАМЯТЬ:
БЕЗОПАСНОСТЬ:
  ssh только по ключу  нет
  файрвол  нет
  fail2ban  нет`,
	"before-only": `СТАТИСТИКА — применено 0/0 · пропущено 0 · ошибок 0 · перезагрузок 0
ПАКЕТЫ И ЯДРО:
  ядро  6.8.0-31
ДИСК И ПАМЯТЬ:
  диск занято  2.9G/19.1G
СЕТЬ:
  скорость, MB/s (до зеркала)  11.8
  задержка ДЦ, ms  1.2
  интернет, ms  12.5
БЕЗОПАСНОСТЬ:
  открытых портов  3
  root-вход  yes
  ssh только по ключу  нет
  файрвол  нет
  fail2ban  нет`,
	"after-only": `СТАТИСТИКА — применено 0/0 · пропущено 0 · ошибок 0 · перезагрузок 0
ПАКЕТЫ И ЯДРО:
  ядро  6.8.0-45
ДИСК И ПАМЯТЬ:
  диск занято  3.3G/19.1G
  zram  добавлен
СЕТЬ:
  скорость, MB/s (до зеркала)  24.5
  задержка ДЦ, ms  1.1
  интернет, ms  11.0
БЕЗОПАСНОСТЬ:
  открытых портов  1
  root-вход  prohibit-password
  ssh только по ключу  нет → да
  файрвол  нет → да
  fail2ban  нет → да`,
	"all-changed": `СТАТИСТИКА — применено 1/4 · пропущено 2 · ошибок 1 · перезагрузок 1
ПАКЕТЫ И ЯДРО:
  обновлено пакетов  42
  ядро  6.8.0-31 → 6.8.0-45
  удалено пакетов  7
ДИСК И ПАМЯТЬ:
  диск занято  2.9G/19.1G → 3.3G/19.1G
  zram  добавлен
СЕТЬ:
  скорость, MB/s (до зеркала)  11.8 → 24.5
  задержка ДЦ, ms  1.2 → 1.1
  интернет, ms  12.5 → 11.0
БЕЗОПАСНОСТЬ:
  открытых портов  3 → 1
  root-вход  yes → prohibit-password
  ssh только по ключу  нет → да
  файрвол  нет → да
  fail2ban  нет → да
ПРИМЕНЁННЫЕ ФИКСЫ:
  [A1] Firewall (OK)
  [A5] Sysctl — не требуется: already applied
  [A9] Audit (FAIL)
  [A7] Purge (SKIP)`,
	"unchanged": `СТАТИСТИКА — применено 0/0 · пропущено 0 · ошибок 0 · перезагрузок 0
ПАКЕТЫ И ЯДРО:
  ядро  6.8.0-31
ДИСК И ПАМЯТЬ:
  диск занято  2.9G/19.1G
СЕТЬ:
  скорость, MB/s (до зеркала)  11.8
  задержка ДЦ, ms  1.2
  интернет, ms  12.5
БЕЗОПАСНОСТЬ:
  открытых портов  3
  root-вход  yes
  ssh только по ключу  нет
  файрвол  нет
  fail2ban  нет`,
	"counts-and-zram": `СТАТИСТИКА — применено 0/0 · пропущено 0 · ошибок 0 · перезагрузок 0
ПАКЕТЫ И ЯДРО:
  обновлено пакетов  5
  удалено пакетов  3
ДИСК И ПАМЯТЬ:
  zram  добавлен
БЕЗОПАСНОСТЬ:
  ssh только по ключу  нет
  файрвол  нет
  fail2ban  нет`,
	"zram-already-on": `СТАТИСТИКА — применено 0/0 · пропущено 0 · ошибок 0 · перезагрузок 0
ПАКЕТЫ И ЯДРО:
ДИСК И ПАМЯТЬ:
БЕЗОПАСНОСТЬ:
  ssh только по ключу  нет
  файрвол  нет
  fail2ban  нет`,
	"net-partial-gw-only": `СТАТИСТИКА — применено 0/0 · пропущено 0 · ошибок 0 · перезагрузок 0
ПАКЕТЫ И ЯДРО:
ДИСК И ПАМЯТЬ:
СЕТЬ:
  задержка ДЦ, ms  2.5
БЕЗОПАСНОСТЬ:
  ssh только по ключу  нет
  файрвол  нет
  fail2ban  нет`,
	"net-partial-speed-only": `СТАТИСТИКА — применено 0/0 · пропущено 0 · ошибок 0 · перезагрузок 0
ПАКЕТЫ И ЯДРО:
ДИСК И ПАМЯТЬ:
СЕТЬ:
  скорость, MB/s (до зеркала)  8.0
БЕЗОПАСНОСТЬ:
  ssh только по ключу  нет
  файрвол  нет
  fail2ban  нет`,
	"ports-before-only": `СТАТИСТИКА — применено 0/0 · пропущено 0 · ошибок 0 · перезагрузок 0
ПАКЕТЫ И ЯДРО:
ДИСК И ПАМЯТЬ:
БЕЗОПАСНОСТЬ:
  открытых портов  2
  ssh только по ключу  нет
  файрвол  нет
  fail2ban  нет`,
	"disk-total-missing": `СТАТИСТИКА — применено 0/0 · пропущено 0 · ошибок 0 · перезагрузок 0
ПАКЕТЫ И ЯДРО:
ДИСК И ПАМЯТЬ:
  диск занято  586M/1.0G
БЕЗОПАСНОСТЬ:
  ssh только по ключу  нет
  файрвол  нет
  fail2ban  нет`,
}

func TestStatsLinesGolden(t *testing.T) {
	for _, c := range statsLinesCases() {
		t.Run(c.name, func(t *testing.T) {
			want, ok := statsLinesWant[c.name]
			if !ok {
				t.Fatalf("no expectation for case %q", c.name)
			}
			got := strings.Join(c.s.statsLines(), "\n")
			if got != want {
				t.Errorf("statsLines mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// statsLinesCases are the representative before/after inputs: the edge cases the
// suppression rules exist for, plus one full run with a fix list.
func statsLinesCases() []struct {
	name string
	s    Summary
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
		s    Summary
	}{
		{"both-nil", Summary{OK: 3, Skip: 1, Fail: 0, Reboots: 1}},
		{"empty-both", Summary{Before: &stats.Snapshot{}, After: &stats.Snapshot{}}},
		{"before-only", Summary{Before: full()}},
		{"after-only", Summary{After: after()}},
		{"all-changed", Summary{
			OK: 9, Skip: 2, Fail: 1, Reboots: 1, UpgradedPkgs: 42, PurgedPkgs: 7,
			Before: full(), After: after(),
			Results: []StepResult{
				{ID: "A1", Title: "Firewall", Status: steps.StatusOK},
				{ID: "A5", Title: "Sysctl", Status: steps.StatusSkip, Detail: "already applied"},
				{ID: "A9", Title: "Audit", Status: steps.StatusFail},
				{ID: "A7", Title: "Purge", Status: steps.StatusSkip},
			},
		}},
		{"unchanged", Summary{Before: full(), After: full()}},
		{"counts-and-zram", Summary{
			UpgradedPkgs: 5, PurgedPkgs: 3,
			Before: &stats.Snapshot{ZramActive: false},
			After:  &stats.Snapshot{ZramActive: true},
		}},
		{"zram-already-on", Summary{
			Before: &stats.Snapshot{ZramActive: true},
			After:  &stats.Snapshot{ZramActive: true},
		}},
		{"net-partial-gw-only", Summary{
			Before: &stats.Snapshot{},
			After:  &stats.Snapshot{GatewayPingMs: 2.5},
		}},
		{"net-partial-speed-only", Summary{
			Before: &stats.Snapshot{SpeedMBs: 8.0},
			After:  &stats.Snapshot{},
		}},
		{"ports-before-only", Summary{
			Before: &stats.Snapshot{OpenPorts: []string{"22", "80"}},
			After:  &stats.Snapshot{},
		}},
		{"disk-total-missing", Summary{
			Before: &stats.Snapshot{DiskUsedKB: 500_000},
			After:  &stats.Snapshot{DiskUsedKB: 600_000, DiskTotalKB: 1_048_576},
		}},
	}
}
