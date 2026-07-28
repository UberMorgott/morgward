package stats

import (
	"fmt"
	"strconv"
)

// --- shared value formatters --------------------------------------------------
//
// The CLI summary block and the TUI summary screen render the SAME metrics from
// the same two snapshots. Only the final presentation differs (RU-hardcoded flat
// text vs localized, styled, column-aligned rows), so the value formatting and the
// row selection live here — a third package both sides already import, which keeps
// the tui→engine direction the only edge between them.

// HumanKB renders a KB value (1024 base): G with 1 decimal when >=1 GiB, else M as
// an integer. e.g. 1468006 -> "1.4G", 524288 -> "512M".
func HumanKB(kb float64) string {
	if kb < 0 {
		kb = 0
	}
	if kb >= 1024*1024 {
		return fmt.Sprintf("%.1fG", kb/(1024*1024))
	}
	return fmt.Sprintf("%.0fM", kb/1024)
}

// Num1 renders a positive metric (MB/s, ms) with one decimal, or "" when the value
// is unknown (<=0) so the caller drops the row.
func Num1(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// MemStr humanizes a snapshot's total RAM, or "" when unknown.
func MemStr(s *Snapshot) string {
	if s.MemKB <= 0 {
		return ""
	}
	return HumanKB(float64(s.MemKB))
}

// diskStr renders "used/total", or "" when the total is unknown.
func diskStr(s *Snapshot) string {
	if s.DiskTotalKB <= 0 {
		return ""
	}
	return HumanKB(float64(s.DiskUsedKB)) + "/" + HumanKB(float64(s.DiskTotalKB))
}

// portsStr renders the open-port count, or "" when none are known.
func portsStr(p []string) string {
	if len(p) == 0 {
		return ""
	}
	return strconv.Itoa(len(p))
}

// --- shared row model ---------------------------------------------------------

// Section identifies one metric group of the summary, in render order.
type Section int

const (
	SecPkgKernel Section = iota
	SecDiskMem
	SecNetwork
	SecSecurity
)

// RowKey identifies one metric row. It is a KEY, never a rendered label: the
// engine resolves it to a hardcoded RU string and the TUI to a localized one.
type RowKey int

const (
	RowUpgraded RowKey = iota
	RowKernel
	RowPurged
	RowDiskUsed
	RowZram
	RowSpeed
	RowPingGW
	RowPingNet
	RowPorts
	RowRootLogin
	RowKeyOnly
	RowFirewall
	RowFail2ban
)

// Row is one metric's before/after pair. A row only exists when at least one side
// is known, so a renderer never has to guard against a blank "before -> after".
// A single-valued row (package counts, zram) carries its value in After.
type Row struct {
	Key           RowKey
	Before, After string
}

// Group is a section and the rows that survived suppression. A group may have NO
// rows and still be rendered (its header is unconditional); SummaryGroups omits
// the network group entirely when it is empty.
type Group struct {
	Section Section
	Rows    []Row
}

// Words are the caller's language-specific value tokens. They are values, not
// labels — i18n stays out of this package.
type Words struct{ Yes, No, ZramAdded string }

func (w Words) boolWord(v bool) string {
	if v {
		return w.Yes
	}
	return w.No
}

// SummaryGroups selects the before/after rows of the run summary and applies the
// suppression rules: a row whose data is unknown on BOTH sides is dropped, and the
// network group disappears when it has no rows. Returns nil when both snapshots are
// absent (the caller then renders no stats block at all). Either snapshot may be
// nil; a nil side reads as "unknown" rather than panicking.
func SummaryGroups(before, after *Snapshot, upgradedPkgs, purgedPkgs int, w Words) []Group {
	if before == nil && after == nil {
		return nil
	}
	b, a := before, after
	if b == nil {
		b = &Snapshot{}
	}
	if a == nil {
		a = &Snapshot{}
	}

	add := func(rows []Row, k RowKey, bv, av string) []Row {
		if bv == "" && av == "" {
			return rows
		}
		return append(rows, Row{Key: k, Before: bv, After: av})
	}

	var pkg []Row
	if upgradedPkgs > 0 {
		pkg = add(pkg, RowUpgraded, "", strconv.Itoa(upgradedPkgs))
	}
	pkg = add(pkg, RowKernel, b.KernelVer, a.KernelVer)
	if purgedPkgs > 0 {
		pkg = add(pkg, RowPurged, "", strconv.Itoa(purgedPkgs))
	}

	var dm []Row
	dm = add(dm, RowDiskUsed, diskStr(b), diskStr(a))
	if !b.ZramActive && a.ZramActive {
		dm = add(dm, RowZram, "", w.ZramAdded)
	}

	var net []Row
	net = add(net, RowSpeed, Num1(b.SpeedMBs), Num1(a.SpeedMBs))
	net = add(net, RowPingGW, Num1(b.GatewayPingMs), Num1(a.GatewayPingMs))
	net = add(net, RowPingNet, Num1(b.InternetPingMs), Num1(a.InternetPingMs))

	var sec []Row
	sec = add(sec, RowPorts, portsStr(b.OpenPorts), portsStr(a.OpenPorts))
	sec = add(sec, RowRootLogin, b.RootLogin, a.RootLogin)
	sec = add(sec, RowKeyOnly, w.boolWord(b.KeyOnly), w.boolWord(a.KeyOnly))
	sec = add(sec, RowFirewall, w.boolWord(b.FirewallActive), w.boolWord(a.FirewallActive))
	sec = add(sec, RowFail2ban, w.boolWord(b.Fail2banActive), w.boolWord(a.Fail2banActive))

	out := []Group{{Section: SecPkgKernel, Rows: pkg}, {Section: SecDiskMem, Rows: dm}}
	if len(net) > 0 {
		out = append(out, Group{Section: SecNetwork, Rows: net})
	}
	return append(out, Group{Section: SecSecurity, Rows: sec})
}
