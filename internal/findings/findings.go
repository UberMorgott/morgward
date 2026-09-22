// Package findings reports read-only security observations morgward does not fix.
// Unlike tweaks (applied/apply/revert), a finding is only surfaced to the operator.
// A check that could not run is reported in Report.Errors, never as "no findings".
package findings

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/ui"
)

// Severity levels. Warn is a security observation; Info is operational.
const (
	SevWarn = "warn"
	SevInfo = "info"
)

// Finding is one observation.
type Finding struct {
	ID       string // stable check id, e.g. "acct.uid0"
	Severity string // SevWarn | SevInfo
	Subject  string // what it is about (user, socket, file)
	Detail   string // human explanation
}

// CheckError says a check could not be completed, so its absence of items is unknown.
type CheckError struct {
	Check string
	Err   string
}

// Report is the outcome of all checks.
type Report struct {
	Items  []Finding
	Errors []CheckError
}

// Check names; also the tokens of the remote line protocol.
const (
	checkDBBind   = "db_bind"
	checkUID0     = "uid0"
	checkEmptyPW  = "empty_password"
	checkNopasswd = "nopasswd"
	checkDocker   = "docker"
	checkReboot   = "reboot"
)

// scriptChecks is the order of the checks run by the privileged script.
var scriptChecks = []string{checkUID0, checkEmptyPW, checkNopasswd, checkDocker, checkReboot}

// Collect runs every check: the DB bind check from the detected listeners, the rest
// in one privileged round-trip.
func Collect(cli *sshx.Client, facts *detect.Facts, adminUser string) Report {
	var r Report
	if facts == nil || !facts.ListenersOK {
		r.Errors = append(r.Errors, CheckError{Check: checkDBBind, Err: "listening sockets could not be read"})
	} else {
		r.Items = append(r.Items, DBWildcard(facts.Listeners)...)
	}
	res := cli.Sudo(Script)
	items, errs := Parse(res, adminUser)
	r.Items = append(r.Items, items...)
	r.Errors = append(r.Errors, errs...)
	return r
}

// dbPorts maps "proto/port" to the service conventionally listening there.
var dbPorts = map[string]string{
	"tcp/3306":  "MySQL",
	"tcp/5432":  "PostgreSQL",
	"tcp/6379":  "Redis",
	"tcp/27017": "MongoDB",
	"tcp/9200":  "Elasticsearch",
	"tcp/11211": "Memcached",
	"udp/11211": "Memcached",
}

// DBWildcard flags database ports bound to an unspecified address. It reads raw
// `ss -tulpnH` lines (Facts.Listeners) and parses the LOCAL address column only.
func DBWildcard(listeners []string) []Finding {
	addrs := map[string][]string{}
	var order []string
	for _, line := range listeners {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		host, port, ok := splitLocal(fields[4])
		if !ok || !isWildcard(host) {
			continue
		}
		key := fields[0] + "/" + port
		if _, known := dbPorts[key]; !known {
			continue
		}
		if _, seen := addrs[key]; !seen {
			order = append(order, key)
		}
		addrs[key] = append(addrs[key], ui.StripControlAndANSI(fields[4]))
	}
	out := make([]Finding, 0, len(order))
	for _, key := range order {
		out = append(out, Finding{
			ID:       "db.wildcard_bind",
			Severity: SevWarn,
			Subject:  key + " " + dbPorts[key],
			Detail:   "listens on all interfaces (" + strings.Join(addrs[key], ", ") + ")",
		})
	}
	return out
}

// splitLocal splits an ss local address "host:port" at the last colon.
func splitLocal(local string) (host, port string, ok bool) {
	i := strings.LastIndex(local, ":")
	if i <= 0 || i == len(local)-1 {
		return "", "", false
	}
	return local[:i], local[i+1:], true
}

// isWildcard reports whether an ss local host is an unspecified address: "*",
// 0.0.0.0, [::], ::, or an expanded unspecified IPv6. Interface-scoped binds
// ("0.0.0.0%eth0") listen on one interface only and are not wildcard.
func isWildcard(host string) bool {
	if strings.Contains(host, "%") {
		return false
	}
	if host == "*" {
		return true
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// Script is the privileged read-only check script. Line protocol, tab-separated:
//
//	F <check> <value>   one finding (nopasswd: <file> <rule>)
//	R <check> <rc>      the check finished with exit status rc
//
// A check without an R line, or with rc != 0, is a check error. Shadow output is
// user names only, never hash fields.
const Script = `set +e
awk -F: '$1 != "root" && $3 ~ /^[0-9]+$/ && $3 + 0 == 0 {print "F\tuid0\t" $1}' /etc/passwd
printf 'R\tuid0\t%s\n' "$?"
awk -F: 'NF > 1 && $2 == "" {print "F\tempty_password\t" $1}' /etc/shadow
printf 'R\tempty_password\t%s\n' "$?"
(
  rc=0
  for f in /etc/sudoers /etc/sudoers.d/*; do
    [ -f "$f" ] || continue
    case "$f" in /etc/sudoers.d/*.*|*~) continue ;; esac
    awk -v f="$f" '{s = $0; gsub(/\t/, " ", s); sub(/^ +/, "", s)} s != "" && substr(s, 1, 1) != "#" && s ~ /NOPASSWD/ {print "F\tnopasswd\t" f "\t" s}' "$f" || rc=1
  done
  exit "$rc"
)
printf 'R\tnopasswd\t%s\n' "$?"
(
  g=$(getent group docker); rc=$?
  [ "$rc" = 2 ] && exit 0
  [ "$rc" = 0 ] || exit "$rc"
  gid=$(printf '%s\n' "$g" | cut -d: -f3)
  [ -n "$gid" ] || exit 1
  printf '%s\n' "$g" | cut -d: -f4 | tr ',' '\n' | awk 'NF {print "F\tdocker\t" $0}'
  getent passwd | awk -F: -v gid="$gid" '$4 == gid {print "F\tdocker\t" $1}'
)
printf 'R\tdocker\t%s\n' "$?"
if [ -e /run/reboot-required ]; then
  printf 'F\treboot\t%s\n' "$(cat /run/reboot-required.pkgs 2>/dev/null | sort -u | wc -l)"
fi
printf 'R\treboot\t0\n'
`

// Parse decodes the Script output into findings and per-check errors. adminUser
// identifies morgward's own sudoers rule, which is not reported.
func Parse(res sshx.Result, adminUser string) ([]Finding, []CheckError) {
	if res.Err != nil || res.RC == -1 {
		msg := "SSH transport lost"
		if res.Err != nil {
			msg = res.Err.Error()
		}
		return nil, allErrors("findings round-trip failed: " + msg)
	}
	rcs := map[string]string{}
	vals := map[string][]string{}
	for line := range strings.SplitSeq(res.Stdout, "\n") {
		parts := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		switch parts[0] {
		case "R":
			rcs[parts[1]] = strings.TrimSpace(parts[2])
		case "F":
			vals[parts[1]] = append(vals[parts[1]], parts[2])
		}
	}
	if len(rcs) == 0 {
		msg := "findings script produced no results"
		if s := firstLine(res.Stderr); s != "" {
			msg += ": " + s
		}
		return nil, allErrors(msg)
	}

	var items []Finding
	var errs []CheckError
	for _, c := range scriptChecks {
		rc, done := rcs[c]
		switch {
		case !done:
			errs = append(errs, CheckError{Check: c, Err: "check did not complete"})
		case rc != "0":
			errs = append(errs, CheckError{Check: c, Err: "exit status " + ui.StripControlAndANSI(rc)})
		}
		items = append(items, decode(c, vals[c], adminUser)...)
	}
	return items, errs
}

// decode turns one check's raw values into findings.
func decode(check string, vals []string, adminUser string) []Finding {
	var out []Finding
	seen := map[string]bool{}
	add := func(f Finding) {
		key := f.ID + "\x00" + f.Subject + "\x00" + f.Detail
		if !seen[key] {
			seen[key] = true
			out = append(out, f)
		}
	}
	for _, raw := range vals {
		v := strings.TrimSpace(ui.StripControlAndANSI(raw))
		switch check {
		case checkUID0:
			add(Finding{ID: "acct.uid0", Severity: SevWarn, Subject: v, Detail: "non-root account with UID 0"})
		case checkEmptyPW:
			add(Finding{ID: "acct.empty_password", Severity: SevWarn, Subject: v, Detail: "empty password field in /etc/shadow"})
		case checkNopasswd:
			file, rule, _ := strings.Cut(raw, "\t")
			file = strings.TrimSpace(ui.StripControlAndANSI(file))
			rule = strings.TrimSpace(ui.StripControlAndANSI(rule))
			if IsOwnSudoersRule(file, rule, adminUser) {
				continue
			}
			add(Finding{ID: "sudo.nopasswd", Severity: SevWarn, Subject: file, Detail: "NOPASSWD rule: " + rule})
		case checkDocker:
			add(Finding{ID: "group.docker", Severity: SevWarn, Subject: v, Detail: "docker group member (root-equivalent)"})
		case checkReboot:
			detail := "reboot required"
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				detail = fmt.Sprintf("reboot required by %d package(s)", n)
			}
			add(Finding{ID: "os.reboot_required", Severity: SevInfo, Subject: "/run/reboot-required", Detail: detail})
		}
	}
	return out
}

// IsOwnSudoersRule reports whether rule is exactly the admin rule PRE writes to
// /etc/sudoers.d/90-<admin> ("<admin> ALL=(ALL) NOPASSWD:ALL", whitespace-insensitive).
// Any other line, including extra lines in that file, is still reported.
func IsOwnSudoersRule(file, rule, adminUser string) bool {
	if adminUser == "" || file != "/etc/sudoers.d/90-"+adminUser {
		return false
	}
	return slices.Equal(strings.Fields(rule), []string{adminUser, "ALL=(ALL)", "NOPASSWD:ALL"})
}

// allErrors marks every script check as failed with msg.
func allErrors(msg string) []CheckError {
	errs := make([]CheckError, 0, len(scriptChecks))
	for _, c := range scriptChecks {
		errs = append(errs, CheckError{Check: c, Err: msg})
	}
	return errs
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(ui.StripControlAndANSI(s))
}
