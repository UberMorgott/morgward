// Package verify runs the §V verification matrix — checking effective behavior,
// not config text. Lockout-capable rows that fail abort; the rest are reported.
package verify

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/ui"
)

// Status is a verification row outcome.
type Status int

const (
	StatusPass    Status = iota
	StatusWarn           // non-lockout check failed
	StatusFail           // lockout-capable check failed
	StatusSkip           // check not applicable (precondition absent) — reason in Detail
	StatusUnknown        // transport/exec error — the row could NOT be measured (not a WARN)
)

func (s Status) String() string {
	switch s {
	case StatusPass:
		return "PASS"
	case StatusWarn:
		return "WARN"
	case StatusFail:
		return "FAIL"
	case StatusUnknown:
		return "UNKNOWN"
	default:
		return "SKIP"
	}
}

// Check is a single verification row.
type Check struct {
	Name    string
	Cmd     string                // run via Sudo
	Want    func(out string) bool // pass predicate over trimmed stdout
	Lockout bool                  // failure aborts the run
	// NA, when set and it returns a reason, marks the row not-applicable (SKIP) —
	// the predicate isn't evaluated and the row never counts as pass/fail. Used for
	// checks whose precondition is absent (so a missing optional feature reads as a
	// skip-with-reason instead of a misleading warning).
	NA func(out string) (reason string, skip bool)
}

// Row is the resolved outcome of one Check, kept structured so callers (CLI log +
// TUI matrix) can render an aligned table and surface SKIP reasons.
type Row struct {
	Name   string
	Status Status
	Detail string // truncated observed value, or the skip reason for StatusSkip
}

// Result summarizes the matrix outcome.
type Result struct {
	Passed  int
	Failed  int
	Skipped int
	Unknown int // rows that could not be measured (transport/exec error)
	Abort   bool
	Rows    []Row // every row, in matrix order — for an aligned final render
}

func contains(sub string) func(string) bool {
	return func(out string) bool { return strings.Contains(strings.ToLower(out), strings.ToLower(sub)) }
}

func equals(want string) func(string) bool {
	return func(out string) bool { return strings.TrimSpace(out) == want }
}

// firewallChecks returns the two firewall verification rows ("Firewall order" +
// "SSH port open") appropriate to how A1 actually applied, keyed off the detected
// firewall manager:
//   - managed iptables (greenfield, or brownfield iptables/none): the original
//     raw-iptables asserts (-P INPUT DROP + --dport ACCEPT), both Lockout — A1 wrote
//     that ruleset, so a missing rule IS a lockout. Byte-identical to before.
//   - ufw: A1 only added `ufw allow` rules; assert ufw is active (informational) and
//     SSH is allowed in ufw (Lockout — a genuinely SSH-closed ufw is a lockout). Both
//     PASS on a correctly-configured ufw box.
//   - firewalld / nftables: A1 deferred entirely (operator owns the firewall), so we
//     have nothing to assert — both rows SKIP-with-reason and never Abort.
func firewallChecks(facts *detect.Facts, port int) []Check {
	if facts != nil && !facts.ManagesIPTables() {
		switch facts.FirewallMgr {
		case "ufw":
			return []Check{
				{Name: "Firewall (ufw) active", Cmd: "LANG=C ufw status 2>/dev/null | grep -i '^Status:'", Want: contains("active"), Lockout: false},
				{Name: "SSH port allowed (ufw)", Cmd: fmt.Sprintf("LANG=C ufw status 2>/dev/null | grep -E '(^|[^0-9])%d/tcp'", port), Want: contains(fmt.Sprintf("%d/tcp", port)), Lockout: true},
			}
		case "firewalld", "nftables":
			reason := facts.FirewallMgr + " manages the firewall — A1 deferred"
			skip := func(string) (string, bool) { return reason, true }
			return []Check{
				{Name: "Firewall order", Cmd: "true", Want: func(string) bool { return true }, Lockout: false, NA: skip},
				{Name: "SSH port open", Cmd: "true", Want: func(string) bool { return true }, Lockout: false, NA: skip},
			}
		}
	}
	// Managed iptables (greenfield + iptables/none): unchanged, byte-identical.
	return []Check{
		{Name: "Firewall order", Cmd: "iptables -S | grep -- '-P INPUT DROP'", Want: contains("drop"), Lockout: true},
		{Name: "SSH port open", Cmd: fmt.Sprintf("iptables -S | grep -- '--dport %d'", port), Want: contains("accept"), Lockout: true},
	}
}

// Run executes the matrix and prints an aligned result table (one row per check).
// facts may be nil (defensive); a nil facts is treated as managed iptables so the
// matrix degrades to its original byte-identical behavior.
func Run(c *sshx.Client, log *ui.Logger, port int, facts *detect.Facts) Result {
	// Root-login policy is INFORMATIONAL, not a lockout assert. The default path
	// (A2 / A2-safe) hardens SSH crypto only and leaves the image's access policy
	// untouched — root login stays at the image default (prohibit-password) and
	// PasswordAuthentication is left to the image, so neither is something we force.
	// Only the opt-in lockdown (A2-danger) sets PermitRootLogin no +
	// PasswordAuthentication no. We therefore report the observed policy (PASS when
	// it matches the image default, WARN otherwise) but never abort the run on it —
	// a tighter or looser policy is not a lockout.
	rootCheck := "prohibit-password"
	fw := firewallChecks(facts, port)
	checks := []Check{
		{Name: "SSH syntax", Cmd: "sshd -t && echo ok", Want: equals("ok"), Lockout: true},
		{Name: "SSH effective policy", Cmd: "sshd -T | grep -i permitrootlogin", Want: contains(rootCheck), Lockout: false},
		fw[0],
		fw[1],
		{Name: "Rollback disarmed", Cmd: "systemctl list-timers --all | grep -c fw-rollback || true", Want: equals("0"), Lockout: false},
		{Name: "fail2ban", Cmd: "fail2ban-client status sshd >/dev/null 2>&1 && echo ok", Want: equals("ok"), Lockout: false},
		{Name: "BBR", Cmd: "sysctl -n net.ipv4.tcp_congestion_control", Want: equals("bbr"), Lockout: false},
		{Name: "core_pattern", Cmd: "sysctl -n kernel.core_pattern", Want: contains("/bin/false"), Lockout: false},
		{Name: "THP madvise", Cmd: "cat /sys/kernel/mm/transparent_hugepage/enabled", Want: contains("[madvise]"), Lockout: false},
		{Name: "ZRAM", Cmd: "swapon --show | grep -q zram && echo ok", Want: equals("ok"), Lockout: false},
		{Name: "earlyoom", Cmd: "systemctl is-active earlyoom", Want: equals("active"), Lockout: false},
		{Name: "DNS hardening", Cmd: "resolvectl status 2>/dev/null | grep -i dnsovertls | head -1", Want: contains("opportunistic"), Lockout: false},
		{Name: "NOFILE limit", Cmd: "systemctl show -p DefaultLimitNOFILE --value", Want: contains("524288"), Lockout: false},
		{Name: "Auto-updates", Cmd: "unattended-upgrade --dry-run --debug 2>&1 | grep -qi 'allowed origins' && echo ok", Want: equals("ok"), Lockout: false},
		{
			Name:    "auditd",
			Cmd:     "if command -v auditctl >/dev/null 2>&1; then auditctl -l 2>/dev/null | grep -q sshd_config && echo ok; else echo __noauditd__; fi",
			Want:    equals("ok"),
			Lockout: false,
			// §A10 installs auditd and loads the sshd_config watch rule, so after a
			// full run this passes. Where auditd is absent (e.g. §A10 was skipped or
			// never ran) the auditctl binary is missing, so report the row as a
			// skip-with-reason, not a warning.
			NA: func(out string) (string, bool) {
				if strings.TrimSpace(out) == "__noauditd__" {
					return "auditd not installed", true
				}
				return "", false
			},
		},
		{Name: "no failed units", Cmd: "systemctl --failed --no-legend | wc -l", Want: equals("0"), Lockout: false},
	}

	var res Result
	for _, ch := range checks {
		row := classify(ch, c.Sudo(ch.Cmd))
		switch row.Status {
		case StatusUnknown:
			res.Unknown++
		case StatusSkip:
			res.Skipped++
		case StatusPass:
			res.Passed++
		case StatusFail:
			res.Failed++
			res.Abort = true
		default: // StatusWarn
			res.Failed++
		}
		res.Rows = append(res.Rows, row)
	}

	renderMatrix(log, res.Rows)
	return res
}

// classify maps one Check's command Result to a verification Row. It is the pure
// decision core of Run (no SSH, no logging) so the §V status logic is unit-testable.
//
// A transport/exec error (RC==-1 or Err!=nil) leaves stdout empty for reasons
// unrelated to the feature under test. For a NON-lockout row we surface that as
// StatusUnknown ("could not measure") rather than a misleading WARN/false-RED. Lockout
// rows still fail closed: an unmeasured lockout-capable check falls through to the
// predicate (empty stdout → StatusFail → Abort) exactly as before.
func classify(ch Check, r sshx.Result) Row {
	out := r.Out()
	row := Row{Name: ch.Name, Detail: truncate(out, 40)}

	if (r.RC == -1 || r.Err != nil) && !ch.Lockout {
		row.Status = StatusUnknown
		row.Detail = transportDetail(r)
		return row
	}
	if ch.NA != nil {
		if reason, skip := ch.NA(out); skip {
			row.Status = StatusSkip
			row.Detail = reason
			return row
		}
	}
	switch {
	case ch.Want(out):
		row.Status = StatusPass
	case ch.Lockout:
		row.Status = StatusFail
	default:
		row.Status = StatusWarn
	}
	return row
}

// renderMatrix prints the §V matrix as an aligned table to the CLI/log. Column
// widths are computed with lipgloss.Width (display cells — multibyte-safe, unlike
// %-*s which miscounts wide/Cyrillic runes) so the name/status/detail columns line
// up. Each row is routed to the colored logger method matching its status so the
// CLI keeps its green/yellow/red semantics; the TUI re-renders the same Rows inside
// its scrollable box (see internal/tui).
func renderMatrix(log *ui.Logger, rows []Row) {
	log.Banner("§V VERIFICATION MATRIX")
	nameW := 0
	for _, r := range rows {
		if w := lipgloss.Width(r.Name); w > nameW {
			nameW = w
		}
	}
	for _, r := range rows {
		name := padCells(r.Name, nameW)
		switch r.Status {
		case StatusPass:
			log.OK("%s  %s", name, r.Detail)
		case StatusSkip:
			log.Skip("%s  %s", name, r.Detail)
		case StatusFail:
			log.Fail("%s  LOCKOUT-CAPABLE got=%q", name, r.Detail)
		case StatusUnknown:
			log.Warn("%s  UNMEASURED (%s)", name, r.Detail)
		default: // StatusWarn
			log.Warn("%s  got=%q", name, r.Detail)
		}
	}
}

// padCells right-pads s to w display cells using lipgloss.Width (NOT byte len), so
// multibyte names still align to a common column edge.
func padCells(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// transportDetail summarizes why a row could not be measured: the transport error
// when present, else the stderr tail, else a generic exit-code note. Kept short so it
// fits the matrix detail column.
func transportDetail(r sshx.Result) string {
	if r.Err != nil {
		return truncate(r.Err.Error(), 40)
	}
	if s := strings.TrimSpace(r.Stderr); s != "" {
		return truncate(s, 40)
	}
	return fmt.Sprintf("rc=%d", r.RC)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if lipgloss.Width(s) > n {
		return lipgloss.NewStyle().MaxWidth(n).Render(s) + "…"
	}
	return s
}
