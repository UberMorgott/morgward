package steps

import (
	"fmt"
	"strings"
)

// A10Detection implements §A10 (auditd forensic trail + successful-login PAM
// notify) and §A11.1 (inbound-drop logging).
type A10Detection struct{}

func (A10Detection) ID() string    { return "A10" }
func (A10Detection) Title() string { return "Detection (auditd, login-notify, drop-log, OS hardening)" }

const auditRules = `-w /etc/ssh/sshd_config -p wa -k sshd_config
-w /etc/ssh/sshd_config.d/ -p wa -k sshd_config
-w /etc/sudoers -p wa -k sudoers
-w /etc/sudoers.d/ -p wa -k sudoers
-w /etc/passwd -p wa -k identity
-w /etc/shadow -p wa -k identity
-w /etc/group -p wa -k identity
-w /root/.ssh/ -p wa -k ssh_keys
-w /etc/cron.d/ -p wa -k cron
-w /etc/crontab -p wa -k cron
-a always,exit -F arch=b64 -F euid=0 -S execve -k root_exec
`

const loginNotify = `#!/bin/sh
[ "$PAM_TYPE" = "open_session" ] || exit 0
MSG="SSH login: user=$PAM_USER from=$PAM_RHOST on $(hostname) at $(date -Is)"
logger -t ssh-login-notify "$MSG"
if [ -n "$NOTIFY_WEBHOOK" ]; then
  curl -fsS --max-time 5 -d "$MSG" "$NOTIFY_WEBHOOK" >/dev/null 2>&1 || true
fi
exit 0
`

// a10State is the post-apply probe script: one token per piece A10 claims to have
// installed. Tokens are distinct words so a substring check cannot alias.
const a10State = `command -v auditctl >/dev/null 2>&1 && echo AUDITD_BIN
auditctl -l 2>/dev/null | grep -q sshd_config && echo AUDIT_RULES
systemctl is-active auditd >/dev/null 2>&1 && echo AUDITD_ACTIVE
test -x /usr/local/sbin/ssh-login-notify.sh && echo NOTIFY_SCRIPT
grep -q ssh-login-notify /etc/pam.d/sshd 2>/dev/null && echo NOTIFY_PAM
`

// detectionVerdict scores the a10State probe output. It NEVER reports success for
// a piece that is not actually there: a missing required piece yields StatusFail
// naming exactly what is missing. The failure is NOT lockout-capable (a box without
// auditd is not locked out), so the caller returns a nil error and the run
// continues — the step just stops lying about having applied the tweak.
func detectionVerdict(state string, aptRC int, logRule bool) (Status, string) {
	var missing []string
	for _, c := range []struct{ tok, what string }{
		{"AUDITD_BIN", "auditd not installed"},
		{"AUDITD_ACTIVE", "auditd service not active"},
		{"AUDIT_RULES", "audit rules (99-vps.rules) not loaded"},
		{"NOTIFY_SCRIPT", "ssh-login-notify.sh missing"},
		{"NOTIFY_PAM", "pam.d/sshd notify line missing"},
	} {
		if !strings.Contains(state, c.tok) {
			missing = append(missing, c.what)
		}
	}
	if len(missing) > 0 {
		d := "detection setup incomplete: " + strings.Join(missing, "; ")
		if aptRC != 0 {
			d += fmt.Sprintf(" (apt/setup exited rc=%d — check package availability and the dpkg lock)", aptRC)
		}
		return StatusFail, d
	}
	d := "auditd + login-notify active"
	if logRule {
		d += " + inbound-drop logging"
	}
	return StatusOK, d
}

func (a A10Detection) Run(ctx *Context) (Status, string, error) {
	// auditd is REQUIRED. audispd-plugins is a TRANSITIONAL package (absent on newer
	// releases) — installing it on the same line would make its absence abort the
	// whole install, so it gets its own best-effort line.
	script := "export DEBIAN_FRONTEND=noninteractive\n" +
		aptInstall("auditd") +
		aptInstallOptional("audispd-plugins") +
		"mkdir -p /etc/audit/rules.d\n" +
		putFile("/etc/audit/rules.d/99-vps.rules", auditRules, "0640") +
		"augenrules --load >/dev/null 2>&1\nsystemctl enable --now auditd >/dev/null 2>&1\n"

	// Successful-login PAM notify (session optional — never blocks login).
	script += putFile("/usr/local/sbin/ssh-login-notify.sh", loginNotify, "0700") +
		"grep -q ssh-login-notify /etc/pam.d/sshd || echo 'session optional pam_exec.so seteuid /usr/local/sbin/ssh-login-notify.sh' >> /etc/pam.d/sshd\n"

	// A11.1 inbound-drop logging (last INPUT rule before DROP), idempotent via -C.
	// FIREWALL-MANAGER-AWARE: this appends a raw iptables INPUT rule and persists it
	// via netfilter-persistent — both only valid where morgward owns the iptables
	// ruleset (greenfield, or brownfield iptables/none). On a ufw/firewalld/nftables
	// box this would impose a conflicting second firewall layer over the operator's
	// manager, so the block is skipped there entirely. auditd + login-notify above
	// are firewall-agnostic and always apply.
	if ctx.Facts.ManagesIPTables() {
		script += `iptables  -C INPUT -m limit --limit 5/min -j LOG --log-prefix "ipt-drop-in: "  --log-level 4 2>/dev/null || iptables  -A INPUT -m limit --limit 5/min -j LOG --log-prefix "ipt-drop-in: "  --log-level 4
ip6tables -C INPUT -m limit --limit 5/min -j LOG --log-prefix "ipt6-drop-in: " --log-level 4 2>/dev/null || ip6tables -A INPUT -m limit --limit 5/min -j LOG --log-prefix "ipt6-drop-in: " --log-level 4
`
		// Re-save firewall (A11.1 changed the ruleset).
		script += "netfilter-persistent save >/dev/null 2>&1\n"
	} else {
		ctx.Log.Detail("inbound-drop LOG rule skipped: %s manages the firewall (no second layer)", ctx.Facts.FirewallMgr)
	}

	r := ctx.Cli.Sudo(script)
	if r.RC != 0 {
		ctx.Log.Warn("detection setup rc=%d (verifying what actually landed)", r.RC)
	}
	status, detail := detectionVerdict(ctx.Cli.Sudo(a10State).Stdout, r.RC, ctx.Facts.ManagesIPTables())
	return status, detail, nil
}
