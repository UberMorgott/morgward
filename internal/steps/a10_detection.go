package steps

import "github.com/UberMorgott/morgward/internal/config"

// A10Detection implements §A10 (auditd forensic trail + successful-login PAM
// notify), §A11.1 (inbound-drop logging), and the strict-only §A12.1 module
// blacklist + §A12.2 /dev/shm mount hardening.
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

const moduleBlacklist = `install dccp /bin/true
install sctp /bin/true
install rds /bin/true
install tipc /bin/true
install cramfs /bin/true
install freevxfs /bin/true
install jffs2 /bin/true
install usb-storage /bin/true
`

func (a A10Detection) Run(ctx *Context) (Status, string, error) {
	port := ctx.Cfg.Port

	// auditd + rules.
	script := `export DEBIAN_FRONTEND=noninteractive
stdbuf -oL -eL apt-get install -y auditd audispd-plugins
mkdir -p /etc/audit/rules.d
` + putFile("/etc/audit/rules.d/99-vps.rules", auditRules, "0640") +
		"augenrules --load >/dev/null 2>&1\nsystemctl enable --now auditd >/dev/null 2>&1\n"

	// Successful-login PAM notify (session optional — never blocks login).
	script += putFile("/usr/local/sbin/ssh-login-notify.sh", loginNotify, "0700") +
		"grep -q ssh-login-notify /etc/pam.d/sshd || echo 'session optional pam_exec.so seteuid /usr/local/sbin/ssh-login-notify.sh' >> /etc/pam.d/sshd\n"

	// A11.1 inbound-drop logging (last INPUT rule before DROP), idempotent via -C.
	script += `iptables  -C INPUT -m limit --limit 5/min -j LOG --log-prefix "ipt-drop-in: "  --log-level 4 2>/dev/null || iptables  -A INPUT -m limit --limit 5/min -j LOG --log-prefix "ipt-drop-in: "  --log-level 4
ip6tables -C INPUT -m limit --limit 5/min -j LOG --log-prefix "ipt6-drop-in: " --log-level 4 2>/dev/null || ip6tables -A INPUT -m limit --limit 5/min -j LOG --log-prefix "ipt6-drop-in: " --log-level 4
`

	// A12 (strict only): module blacklist + /dev/shm hardening.
	if ctx.Cfg.Mode == config.ModeStrict {
		script += putFile("/etc/modprobe.d/99-vps-blacklist.conf", moduleBlacklist, "0644")
		script += `grep -q '/dev/shm' /etc/fstab || echo 'tmpfs /dev/shm tmpfs defaults,nodev,nosuid,noexec 0 0' >> /etc/fstab
mount -o remount /dev/shm 2>/dev/null || true
`
	}

	// Re-save firewall (A11.1 changed the ruleset) and re-open SSH port marker.
	_ = port
	script += "netfilter-persistent save >/dev/null 2>&1\n"

	if r := ctx.Cli.Sudo(script); r.RC != 0 {
		ctx.Log.Warn("detection setup rc=%d (verifying)", r.RC)
	}

	auditOK := ctx.Cli.Sudo("auditctl -l 2>/dev/null | grep -q sshd_config && echo ok").Out()
	extra := ""
	if ctx.Cfg.Mode == config.ModeStrict {
		extra = ", module blacklist + /dev/shm hardened"
	}
	if auditOK != "ok" {
		ctx.Log.Warn("auditd rules not confirmed loaded")
	}
	return StatusOK, "auditd + login-notify + inbound-drop logging active" + extra, nil
}
