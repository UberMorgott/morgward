package steps

import (
	"fmt"
	"strings"
)

// A3Fail2ban implements §A3: fail2ban with the systemd backend (default) and an
// explicit admin/server IP whitelist (ignoreself does NOT cover a remote admin).
type A3Fail2ban struct{}

func (A3Fail2ban) ID() string    { return "A3" }
func (A3Fail2ban) Title() string { return "fail2ban (systemd backend, admin whitelist)" }

func (a A3Fail2ban) Run(ctx *Context) (Status, string, error) {
	// Build ignoreip from resolved IPs only — never write literal placeholders.
	ignore := []string{"127.0.0.1/8", "::1"}
	if ctx.Facts.ServerIPv4 != "" {
		ignore = append(ignore, ctx.Facts.ServerIPv4+"/32")
	}
	if ctx.Facts.ServerIPv6 != "" {
		ignore = append(ignore, ctx.Facts.ServerIPv6+"/128")
	}
	if ctx.Facts.ClientIP != "" && !strings.Contains(ctx.Facts.ClientIP, ":") {
		ignore = append(ignore, ctx.Facts.ClientIP+"/32")
	} else if ctx.Facts.ClientIP != "" {
		ignore = append(ignore, ctx.Facts.ClientIP+"/128")
	}

	jail := fmt.Sprintf(`[DEFAULT]
ignoreself = true
ignoreip   = %s

[sshd]
enabled  = true
maxretry = 3
findtime = 600
bantime  = 3600
`, strings.Join(ignore, " "))

	// fail2ban lives in `universe`; a minimal/container image can ship with that
	// component disabled, and then `apt-get install` dies with "Unable to locate
	// package" (the symptom seen on 26.04). Probe the candidate first and, when it is
	// missing, enable universe + refresh once before giving up. Still missing ⇒ SKIP
	// with the reason, not a run-aborting FAIL: fail2ban is defense-in-depth, and A1
	// already closed the box down.
	if !fail2banAvailable(ctx) {
		ctx.Log.Detail("fail2ban has no install candidate — enabling `universe` and refreshing…")
		ctx.Cli.Sudo(aptPrelude() + "add-apt-repository -y universe >/dev/null 2>&1 || true\n" +
			aptGet + " update >/dev/null 2>&1 || true\n")
		if !fail2banAvailable(ctx) {
			return StatusSkip, "fail2ban has no install candidate in this release's enabled repos (universe unavailable) — skipped", nil
		}
	}

	port := ctx.Cfg.Port
	script := aptPrelude() +
		aptInstall("fail2ban python3-systemd") +
		putFile("/etc/fail2ban/jail.local", jail, "0644")
	if port != 22 {
		// Tell fail2ban which port sshd listens on.
		script += putFile("/etc/fail2ban/jail.d/sshd-port.local",
			fmt.Sprintf("[sshd]\nport = %d\n", port), "0644")
	}
	script += "systemctl enable fail2ban >/dev/null 2>&1\nsystemctl restart fail2ban\n"

	if r := ctx.Cli.Sudo(script); r.RC != 0 {
		return StatusFail, "fail2ban setup failed: " + firstLine(r.Stderr), nil
	}
	st := ctx.Cli.Sudo("fail2ban-client status sshd >/dev/null 2>&1 && echo active").Out()
	if st != "active" {
		return StatusFail, "fail2ban sshd jail not active", nil
	}
	return StatusOK, fmt.Sprintf("fail2ban active, sshd jail loaded, %d whitelisted IP(s)", len(ignore)), nil
}

// fail2banAvailable reports whether apt has an installable candidate for fail2ban on
// this box (already-installed counts too). `apt-cache policy` prints "Candidate:
// (none)" when the package is known but unavailable, and nothing at all when it is not
// in any enabled index — both read as unavailable here.
func fail2banAvailable(ctx *Context) bool {
	cand := ctx.Cli.Run("apt-cache policy fail2ban 2>/dev/null | awk '/Candidate:/{print $2}'").Out()
	return cand != "" && cand != "(none)"
}
