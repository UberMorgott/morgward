package steps

import (
	"fmt"
	"strings"

	"github.com/UberMorgott/morgward/internal/config"
)

// A2SSH implements §A2: SSH crypto hardening via drop-ins, version-conditional
// KexAlgorithms / PerSourcePenalties, minimal host-key handling, ssh-revert
// fail-safe, second-session key-only verify, and the strict-mode root lock +
// executor handoff to the admin user.
type A2SSH struct{}

func (A2SSH) ID() string    { return "A2" }
func (A2SSH) Title() string { return "SSH crypto hardening (drop-ins, AllowGroups, crypto)" }

const conf00 = `PasswordAuthentication no
KbdInteractiveAuthentication no
`

func (a A2SSH) Run(ctx *Context) (Status, string, error) {
	admin := ctx.Cfg.AdminUser
	strict := ctx.Cfg.Mode == config.ModeStrict

	conf99 := build99(ctx, strict)

	// 1. Write drop-ins + cloud-init disable + neutralize stock 50-cloud-init.
	write := putFile("/etc/ssh/sshd_config.d/00-hardening.conf", conf00, "0644") +
		putFile("/etc/ssh/sshd_config.d/99-hardening.conf", conf99, "0644") +
		putFile("/etc/cloud/cloud.cfg.d/99-disable-passwords.cfg", "ssh_pwauth: false\n", "0644") +
		`if [ -f /etc/ssh/sshd_config.d/50-cloud-init.conf ]; then
  sed -ri 's/^\s*PasswordAuthentication\s+yes/PasswordAuthentication no/' /etc/ssh/sshd_config.d/50-cloud-init.conf
fi
mkdir -p /etc/cloud/cloud.cfg.d
`
	if r := ctx.Cli.Sudo(write); r.RC != 0 {
		return StatusFail, "writing sshd drop-ins failed: " + firstLine(r.Stderr), fmt.Errorf("sshd config write failed")
	}

	// 2. Syntax gate BEFORE any destructive key step.
	if r := ctx.Cli.Sudo("sshd -t"); r.RC != 0 {
		ctx.Cli.Sudo("rm -f /etc/ssh/sshd_config.d/00-hardening.conf /etc/ssh/sshd_config.d/99-hardening.conf")
		return StatusFail, "sshd -t rejected config (removed drop-ins): " + firstLine(r.Stderr), fmt.Errorf("sshd -t failed")
	}

	// 3. Minimal host-key path (drop surplus ecdsa, trim moduli) — skip-if clean.
	hostKey := `skip=0
[ ! -f /etc/ssh/ssh_host_ecdsa_key ] && [ -f /etc/ssh/ssh_host_ed25519_key ] && skip=1
if [ "$skip" = "0" ]; then
  rm -f /etc/ssh/ssh_host_ecdsa_key /etc/ssh/ssh_host_ecdsa_key.pub
  awk '$5 >= 3071' /etc/ssh/moduli > /etc/ssh/moduli.safe && [ "$(wc -l < /etc/ssh/moduli.safe)" -ge 20 ] && mv /etc/ssh/moduli.safe /etc/ssh/moduli || rm -f /etc/ssh/moduli.safe
fi
`
	ctx.Cli.Sudo(hostKey)

	// 4. Arm ssh-revert fail-safe, then ONE restart (applies config + host keys).
	ctx.Cli.Sudo(`systemctl stop ssh-revert.timer 2>/dev/null || true
systemctl reset-failed 'ssh-revert.*' 2>/dev/null || true
systemd-run --on-active=300 --unit=ssh-revert sh -c 'rm -f /etc/ssh/sshd_config.d/00-hardening.conf /etc/ssh/sshd_config.d/99-hardening.conf; [ ! -f /etc/ssh/ssh_host_ed25519_key ] && ssh-keygen -A; systemctl reload ssh'`)

	if r := ctx.Cli.Sudo("systemctl restart ssh"); r.RC != 0 {
		return StatusFail, "ssh restart failed: " + firstLine(r.Stderr), fmt.Errorf("ssh restart failed")
	}

	// 5. Second-session key-only verify as the admin user (AllowGroups active).
	ctx.Log.Detail("verifying key-only login as %s in a fresh session…", admin)
	if err := freshLogin(ctx, admin); err != nil {
		return StatusFail, "admin key login verify failed: " + err.Error() + " (ssh-revert will restore in <300s)", fmt.Errorf("ssh hardening locked out admin")
	}

	// Verify succeeded — disarm ssh-revert.
	ctx.Cli.Sudo(`systemctl stop ssh-revert.timer 2>/dev/null || true
systemctl reset-failed 'ssh-revert.*' 2>/dev/null || true`)

	// 6. Strict: lock root password ONLY after admin login proven.
	if strict {
		ctx.Cli.Sudo("passwd -l root")
	}

	// 7. Executor handoff: switch the controller to admin + key + sudo. In strict
	// mode root SSH is now closed; in soft mode this just normalizes the path.
	if ctx.Cli.User == "root" {
		if err := ctx.Cli.SwitchUser(admin); err != nil {
			return StatusFail, "executor handoff to admin failed: " + err.Error(), fmt.Errorf("handoff failed")
		}
		ctx.Log.Detail("executor switched to %s@%s (key + sudo)", admin, ctx.Cfg.Host)
	}

	pol := ctx.Cli.Sudo(`sshd -T 2>/dev/null | grep -Ei 'permitrootlogin|passwordauthentication' | tr '\n' ' '`).Out()
	return StatusOK, "SSH hardened, admin key-only verified; " + pol, nil
}

// build99 assembles 99-hardening.conf with version-conditional tokens.
func build99(ctx *Context, strict bool) string {
	var b strings.Builder
	rootPolicy := "prohibit-password"
	if strict {
		rootPolicy = "no"
	}
	fmt.Fprintf(&b, "PermitRootLogin %s\n", rootPolicy)
	b.WriteString(`MaxAuthTries 3
LoginGraceTime 30
ClientAliveInterval 300
ClientAliveCountMax 2
AllowGroups sshusers
`)
	// KexAlgorithms: mlkem first on 26.04 (OpenSSH 10.x); dropped on 24.04 (9.6p1).
	kex := "sntrup761x25519-sha512@openssh.com,curve25519-sha256,curve25519-sha256@libssh.org,diffie-hellman-group16-sha512,diffie-hellman-group18-sha512,diffie-hellman-group-exchange-sha256"
	if ctx.Facts.Is2604 || (!ctx.Facts.Is2404 && !ctx.Facts.Is2604) {
		kex = "mlkem768x25519-sha256," + kex
	}
	fmt.Fprintf(&b, "KexAlgorithms %s\n", kex)
	b.WriteString(`Ciphers chacha20-poly1305@openssh.com,aes256-gcm@openssh.com,aes128-gcm@openssh.com,aes256-ctr,aes192-ctr,aes128-ctr
MACs hmac-sha2-512-etm@openssh.com,hmac-sha2-256-etm@openssh.com,umac-128-etm@openssh.com
HostKeyAlgorithms sk-ssh-ed25519-cert-v01@openssh.com,ssh-ed25519-cert-v01@openssh.com,rsa-sha2-512-cert-v01@openssh.com,rsa-sha2-256-cert-v01@openssh.com,sk-ssh-ed25519@openssh.com,ssh-ed25519,rsa-sha2-512,rsa-sha2-256
PubkeyAcceptedAlgorithms sk-ssh-ed25519-cert-v01@openssh.com,ssh-ed25519-cert-v01@openssh.com,rsa-sha2-512-cert-v01@openssh.com,rsa-sha2-256-cert-v01@openssh.com,sk-ssh-ed25519@openssh.com,ssh-ed25519,rsa-sha2-512,rsa-sha2-256
CASignatureAlgorithms sk-ssh-ed25519@openssh.com,ssh-ed25519,rsa-sha2-512,rsa-sha2-256
RequiredRSASize 3072
LogLevel VERBOSE
DisableForwarding yes
AllowAgentForwarding no
X11Forwarding no
MaxStartups 10:30:60
HostKey /etc/ssh/ssh_host_ed25519_key
HostKey /etc/ssh/ssh_host_rsa_key
`)
	// PerSourcePenalties: OpenSSH >= 9.8 only (26.04). 9.6p1 (24.04) rejects them.
	if ctx.Facts.Is2604 || (!ctx.Facts.Is2404 && !ctx.Facts.Is2604) {
		b.WriteString("PerSourcePenalties authfail:30 noauth:15 grace-exceeded:120\n")
		if ctx.Facts.ClientIP != "" {
			fmt.Fprintf(&b, "PerSourcePenaltyExemptList %s\n", ctx.Facts.ClientIP)
		}
	}
	return b.String()
}
