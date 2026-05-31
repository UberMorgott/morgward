package engine

import (
	"fmt"
	"strconv"

	"github.com/UberMorgott/morgward/internal/stats"
	"github.com/UberMorgott/morgward/internal/steps"
)

// humanKB renders a KB value (1024 base): G with 1 decimal when ≥1 GiB, else M as
// an integer. e.g. 1468006→"1.4G", 524288→"512M". Local copy — the TUI's identical
// helper is not importable here (would create an import cycle via the TUI pulling
// the engine).
func humanKB(kb int64) string {
	if kb < 0 {
		kb = 0
	}
	f := float64(kb)
	if kb >= 1024*1024 {
		return fmt.Sprintf("%.1fG", f/(1024*1024))
	}
	return fmt.Sprintf("%.0fM", f/1024)
}

// formatDelta renders "label  before → after" with the arrow only when BOTH sides
// are known (non-empty) and differ. When one side is empty it shows the lone known
// value; when both are equal it shows the single value (no arrow). Returns "" only
// if both sides are empty (caller should not emit the row).
func formatDelta(label, before, after string) string {
	switch {
	case before == "" && after == "":
		return ""
	case before == "":
		return fmt.Sprintf("  %s  %s", label, after)
	case after == "":
		return fmt.Sprintf("  %s  %s", label, before)
	case before == after:
		return fmt.Sprintf("  %s  %s", label, before)
	default:
		return fmt.Sprintf("  %s  %s → %s", label, before, after)
	}
}

// boolWord renders a posture bool as a Russian yes/no token for the summary.
func boolWord(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}

// statsLines returns the human text block for the run's before/after metrics and
// the applied-fix list. Returns nil when both snapshots are absent. Every row whose
// data is unknown (empty/zero/nil on the relevant side) is skipped so the output
// never shows a dangling "→" with a blank side.
func (s Summary) statsLines() []string {
	if s.Before == nil && s.After == nil {
		return nil
	}
	b, a := s.Before, s.After
	if b == nil {
		b = &emptySnap
	}
	if a == nil {
		a = &emptySnap
	}

	var out []string
	add := func(line string) {
		if line != "" {
			out = append(out, line)
		}
	}

	// Header: applied/total · skip · fail · reboots.
	out = append(out, fmt.Sprintf("СТАТИСТИКА — применено %d/%d · пропущено %d · ошибок %d · перезагрузок %d",
		s.Applied(), s.Total(), s.Skip, s.Fail, s.Reboots))

	// ПАКЕТЫ И ЯДРО.
	out = append(out, "ПАКЕТЫ И ЯДРО:")
	if s.UpgradedPkgs > 0 {
		add(fmt.Sprintf("  обновлено пакетов  %d", s.UpgradedPkgs))
	}
	add(formatDelta("ядро", b.KernelVer, a.KernelVer))
	if s.PurgedPkgs > 0 {
		add(fmt.Sprintf("  удалено пакетов  %d", s.PurgedPkgs))
	}

	// ДИСК И ПАМЯТЬ.
	out = append(out, "ДИСК И ПАМЯТЬ:")
	add(formatDelta("диск занято", diskStr(b), diskStr(a)))
	if !b.ZramActive && a.ZramActive {
		add("  zram  добавлен")
	}

	// СЕТЬ.
	net := []string{}
	if b.SpeedMBs > 0 || a.SpeedMBs > 0 {
		net = append(net, formatDelta("скорость, MB/s (до зеркала)", speedStr(b.SpeedMBs), speedStr(a.SpeedMBs)))
	}
	if b.GatewayPingMs > 0 || a.GatewayPingMs > 0 {
		net = append(net, formatDelta("задержка ДЦ, ms", pingStr(b.GatewayPingMs), pingStr(a.GatewayPingMs)))
	}
	if b.InternetPingMs > 0 || a.InternetPingMs > 0 {
		net = append(net, formatDelta("интернет, ms", pingStr(b.InternetPingMs), pingStr(a.InternetPingMs)))
	}
	if len(net) > 0 {
		out = append(out, "СЕТЬ:")
		for _, l := range net {
			add(l)
		}
	}

	// БЕЗОПАСНОСТЬ.
	out = append(out, "БЕЗОПАСНОСТЬ:")
	add(formatDelta("открытых портов", portsStr(b.OpenPorts), portsStr(a.OpenPorts)))
	add(formatDelta("root-вход", b.RootLogin, a.RootLogin))
	add(formatDelta("ssh только по ключу", boolWord(b.KeyOnly), boolWord(a.KeyOnly)))
	add(formatDelta("файрвол", boolWord(b.FirewallActive), boolWord(a.FirewallActive)))
	add(formatDelta("fail2ban", boolWord(b.Fail2banActive), boolWord(a.Fail2banActive)))

	// ПРИМЕНЁННЫЕ ФИКСЫ.
	if len(s.Results) > 0 {
		out = append(out, "ПРИМЕНЁННЫЕ ФИКСЫ:")
		for _, r := range s.Results {
			out = append(out, fmt.Sprintf("  [%s] %s (%s)", r.ID, r.Title, statusWord(r.Status)))
		}
	}
	return out
}

// emptySnap is the all-zero fallback used when one snapshot is nil so the row
// helpers see "unknown" on that side rather than dereferencing a nil pointer.
var emptySnap = stats.Snapshot{}

func diskStr(s *stats.Snapshot) string {
	if s.DiskTotalKB <= 0 {
		return ""
	}
	return humanKB(s.DiskUsedKB) + "/" + humanKB(s.DiskTotalKB)
}

func speedStr(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func pingStr(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func portsStr(p []string) string {
	if len(p) == 0 {
		return ""
	}
	return strconv.Itoa(len(p))
}

func statusWord(st steps.Status) string {
	switch st {
	case steps.StatusOK:
		return "OK"
	case steps.StatusSkip:
		return "SKIP"
	case steps.StatusFail:
		return "FAIL"
	default:
		return st.String()
	}
}
