package engine

import (
	"fmt"

	"github.com/UberMorgott/morgward/internal/stats"
	"github.com/UberMorgott/morgward/internal/steps"
)

// Row selection, suppression and value formatting are shared with the TUI summary
// screen and live in internal/stats (SummaryGroups). Only the labels and the flat
// text layout are local: this file is RU-hardcoded BY DESIGN — the engine must not
// import the TUI localization layer — so it resolves each shared RowKey/Section to
// a fixed Russian string of its own.
var (
	statsSectionLabel = [...]string{
		stats.SecPkgKernel: "ПАКЕТЫ И ЯДРО:",
		stats.SecDiskMem:   "ДИСК И ПАМЯТЬ:",
		stats.SecNetwork:   "СЕТЬ:",
		stats.SecSecurity:  "БЕЗОПАСНОСТЬ:",
	}
	statsRowLabel = [...]string{
		stats.RowUpgraded:  "обновлено пакетов",
		stats.RowKernel:    "ядро",
		stats.RowPurged:    "удалено пакетов",
		stats.RowDiskUsed:  "диск занято",
		stats.RowZram:      "zram",
		stats.RowSpeed:     "скорость, MB/s (до зеркала)",
		stats.RowPingGW:    "задержка ДЦ, ms",
		stats.RowPingNet:   "интернет, ms",
		stats.RowPorts:     "открытых портов",
		stats.RowRootLogin: "root-вход",
		stats.RowKeyOnly:   "ssh только по ключу",
		stats.RowFirewall:  "файрвол",
		stats.RowFail2ban:  "fail2ban",
	}
	// statsWords are the RU value tokens SummaryGroups needs (yes/no posture, the
	// zram "added" marker).
	statsWords = stats.Words{Yes: "да", No: "нет", ZramAdded: "добавлен"}
)

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

// probeSegment renders the real on-box tweak tally as a trailing summary segment,
// or "" when the audit produced nothing (never print a meaningless "0/0"). A
// non-zero red count is called out in words — this line carries no styling.
func (s Summary) probeSegment() string {
	if s.ProbesTotal == 0 {
		return ""
	}
	seg := fmt.Sprintf(" · твиков подтверждено %d/%d", s.ProbesPassed, s.ProbesTotal)
	if n := s.ProbesTotal - s.ProbesPassed; n > 0 {
		seg += fmt.Sprintf(" (НЕ подтверждено %d)", n)
	}
	return seg
}

// statsLines returns the human text block for the run's before/after metrics and
// the applied-fix list. Returns nil when both snapshots are absent. Every row whose
// data is unknown (empty/zero/nil on the relevant side) is skipped by
// stats.SummaryGroups so the output never shows a dangling "→" with a blank side.
func (s Summary) statsLines() []string {
	groups := stats.SummaryGroups(s.Before, s.After, s.UpgradedPkgs, s.PurgedPkgs, statsWords)
	if groups == nil {
		return nil
	}

	// Header: applied/total · skip · fail · reboots.
	out := []string{fmt.Sprintf("СТАТИСТИКА — шагов применено %d/%d · пропущено %d · ошибок %d · перезагрузок %d%s",
		s.Applied(), s.Total(), s.Skip, s.Fail, s.Reboots, s.probeSegment())}

	for _, g := range groups {
		out = append(out, statsSectionLabel[g.Section])
		for _, r := range g.Rows {
			out = append(out, formatDelta(statsRowLabel[r.Key], r.Before, r.After))
		}
	}

	// ПРИМЕНЁННЫЕ ФИКСЫ.
	if len(s.Results) > 0 {
		out = append(out, "ПРИМЕНЁННЫЕ ФИКСЫ:")
		for _, r := range s.Results {
			// A benign skip (target absent / already satisfied) is "не требуется", not
			// a bare "(SKIP)" that reads as "not applied". Show the reason inline; the
			// raw English detail is fine here (this file is RU-hardcoded and the engine
			// must not import the tui localization layer).
			if r.Status == steps.StatusSkip && r.Detail != "" {
				out = append(out, fmt.Sprintf("  [%s] %s — не требуется: %s", r.ID, r.Title, r.Detail))
				continue
			}
			out = append(out, fmt.Sprintf("  [%s] %s (%s)", r.ID, r.Title, statusWord(r.Status)))
		}
	}
	return out
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
