package tui

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// monSep is the dim vertical separator drawn BETWEEN the CPU/RAM/DISK segments,
// padded one space each side (" │ ", 3 cells). It uses the box border color so the
// monitor row reads as the same chrome family as the surrounding frame, and visually
// divides the segments (so e.g. CPU's "4.2GHz" extra can't be misread as RAM's).
const monSepCells = 3

func monSep() string { return " " + borderStyle.Render("│") + " " }

// renderMonitor renders the full-width live CPU/RAM/DISK footer sized to width.
// !haveSample or disconnected → a dim "reconnecting…" line. Otherwise three labelled
// bar+percent segments, divided by a dim "│" separator, that consume the available
// width: the fixed parts (labels, percents, extras) and the two separators are
// subtracted, and the REMAINING width is split evenly across the three bars so they
// are wide and the row spans edge to edge. It degrades gracefully on a narrow box:
// drop the used/total + freq extras first, then shrink bars (min 3), then fall back
// to a compact percent-only line — never overflowing width.
func (m model) renderMonitor(width int) string {
	if width < 1 {
		width = 1
	}
	// Blank to "reconnecting…" ONLY on a genuine outage: either we never got a
	// first sample, or we've seen statMissThreshold consecutive disconnected
	// samples (≈3s — a reboot, not jitter). Below the threshold we keep rendering
	// the last-good held sample so a single slow/failed sample doesn't blank the
	// footer. (m.sample holds the last good sample; it is only overwritten on a
	// Connected==true sample, so it never carries a zeroed disconnected snapshot.)
	if !m.haveSample || m.statMiss >= statMissThreshold {
		return monDimStyle.Render(clip(t(m.lang, kMonReconnecting), width))
	}

	s := m.sample
	labels := []string{"CPU", "RAM", "DISK"}
	pcts := []float64{s.CPU, s.RAM, s.Disk}
	extras := []string{
		cpuFreq(s.CPUMHz),
		pairKB(s.RAMUsedKB, s.RAMTotalKB),
		pairKB(s.DiskUsedKB, s.DiskTotalKB),
	}

	const minBars = 3
	// Try the richest layout first (with extras), then drop extras if too tight. For
	// each candidate, sum the FIXED (non-bar) cells of all three segments plus the two
	// separators; the bars consume ALL the remaining width so the row spans edge to
	// edge. The remainder of the even split is handed to the leftmost segments (one
	// extra cell each) so the total exactly fills width with no trailing void.
	for _, withExtra := range []bool{true, false} {
		fixed := 2 * monSepCells // the two " │ " separators
		for i := range labels {
			ex := ""
			if withExtra {
				ex = extras[i]
			}
			fixed += monSegFixed(labels[i], pcts[i], ex)
		}
		barBudget := width - fixed
		if barBudget < len(labels)*minBars {
			continue // not even minimum bars fit at this richness — go leaner
		}
		base := barBudget / len(labels)
		rem := barBudget % len(labels) // distribute leftover cells across segments
		segs := make([]string, len(labels))
		for i := range labels {
			ex := ""
			if withExtra {
				ex = extras[i]
			}
			bars := base
			if i < rem {
				bars++ // leftmost segments absorb the remainder so the row fills width
			}
			segs[i] = monitorSeg(labels[i], pcts[i], bars, ex)
		}
		return strings.Join(segs, monSep())
	}

	// Last resort: compact percent-only form, separated by the same dim │.
	var parts []string
	for i, l := range labels {
		parts = append(parts, monLabelStyle.Render(l)+" "+pctStyle(pcts[i]).Render(fmtPct(pcts[i])))
	}
	return clip(strings.Join(parts, monSep()), width)
}

// monSegFixed returns the FIXED (non-bar) display width of a segment built by
// monitorSeg with the same label/pct/extra: label + " " + " " + pct [+ " " + extra].
// Used to budget how much width is left for the bars.
func monSegFixed(label string, pct float64, extra string) int {
	w := lipgloss.Width(label) + 1 + 1 + lipgloss.Width(fmtPct(pct))
	if extra != "" {
		w += 1 + lipgloss.Width(extra)
	}
	return w
}

// monitorSeg renders one labelled bar+percent segment plus an optional extra.
func monitorSeg(label string, pct float64, bars int, extra string) string {
	if bars < 3 {
		bars = 3
	}
	seg := monLabelStyle.Render(label) + " " + renderBar(pct, bars) +
		" " + pctStyle(pct).Render(fmtPct(pct))
	if extra != "" {
		seg += " " + monDimStyle.Render(extra)
	}
	return seg
}

// humanKB renders a KB value (1024 base): G with 1 decimal when ≥1 GiB, else M
// as an integer. e.g. 1468006→"1.4G", 524288→"512M".
func humanKB(kb float64) string {
	if kb < 0 {
		kb = 0
	}
	if kb >= 1024*1024 {
		return fmt.Sprintf("%.1fG", kb/(1024*1024))
	}
	return fmt.Sprintf("%.0fM", kb/1024)
}

// pairKB renders "used/total" sharing the unit suffix when both land on the same
// unit (e.g. "1.4/2.0G"); otherwise each value keeps its own suffix. Empty when
// total is unknown (≤0).
func pairKB(usedKB, totalKB float64) string {
	if totalKB <= 0 {
		return ""
	}
	u := humanKB(usedKB)
	t := humanKB(totalKB)
	if u[len(u)-1] == t[len(t)-1] {
		return u[:len(u)-1] + "/" + t
	}
	return u + "/" + t
}

// cpuFreq renders a CPU frequency from MHz as "2.4GHz", or "" when unknown.
func cpuFreq(mhz float64) string {
	if mhz <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1fGHz", mhz/1000)
}

// renderBar draws a barW-wide bar filled proportional to pct (-1 → empty/dim).
func renderBar(pct float64, barW int) string {
	if barW < 1 {
		barW = 1
	}
	if pct < 0 {
		return monDimStyle.Render(strings.Repeat("░", barW))
	}
	filled := min(max(int(math.Round(pct/100*float64(barW))), 0), barW)
	return pctStyle(pct).Render(strings.Repeat("█", filled)) +
		monDimStyle.Render(strings.Repeat("░", barW-filled))
}

// fmtPct formats a percent as "NN%"; -1 (unknown/parse-failed) → "--%".
func fmtPct(pct float64) string {
	if pct < 0 {
		return "--%"
	}
	return fmt.Sprintf("%2.0f%%", pct)
}

// pctStyle picks the threshold color: green <70, yellow <90, red ≥90; dim if unknown.
func pctStyle(pct float64) lipgloss.Style {
	switch {
	case pct < 0:
		return monDimStyle
	case pct < 70:
		return monGreenStyle
	case pct < 90:
		return monYellowStyle
	default:
		return monRedStyle
	}
}

// clip truncates s (by display width) to at most w cells so the footer never
// overflows the terminal width.
func clip(s string, w int) string {
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}
