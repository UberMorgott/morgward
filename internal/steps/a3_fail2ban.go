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
	// python3-systemd is mandatory too: without it the systemd backend cannot read the
	// journal and the sshd jail never loads.
	if m := missingCandidates(ctx, a3Packages); m != "" {
		ctx.Log.Detail("no install candidate for %s — refreshing the index / enabling `universe`…", m)
		ctx.Cli.Sudo(aptPrelude() + aptEnsureCandidates(a3Packages))
		if m := missingCandidates(ctx, a3Packages); m != "" {
			return StatusSkip, m + " has no install candidate in this release's enabled repos (universe unavailable) — skipped", nil
		}
	}

	port := ctx.Cfg.Port
	script := aptPrelude() +
		aptInstall(a3Packages) +
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

// a3Packages are A3's mandatory packages: fail2ban and the journal binding its
// systemd backend needs.
const a3Packages = "fail2ban python3-systemd"

// missingCandidates returns the space-separated pkgs apt has no installable candidate
// for on this box (already-installed counts as available), "" when none is missing.
func missingCandidates(ctx *Context, pkgs string) string {
	return ctx.Cli.Sudo(aptCandidateProbeScript(pkgs)).Out()
}

func aptCandidateProbeScript(pkgs string) string {
	return aptCandidateProbe(pkgs) + "mw_nc_probe\necho $mw_nc\n"
}
