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
//
// LEGACY full-run step (kept for CLI `run --mode` back-compat). The TUI no longer
// uses it; instead it runs A2Safe (crypto only, image-default access) and,
// opt-in, A2Danger (the lockdown). Do NOT change A2SSH behavior — the split
// steps below carry the new default-no-lockout behavior.
type A2SSH struct{}

func (A2SSH) ID() string    { return "A2" }
func (A2SSH) Title() string {
	return "SSH crypto hardening (drop-ins, crypto; AllowGroups+root-lock in strict)"
}

// conf00 builds 00-hardening.conf. In strict mode SSH is key-only
// (PasswordAuthentication no); in soft mode password login STAYS ENABLED — the
// explicit `yes` here OVERRIDES the image's cloud-init 50-cloud-init.conf, which
// ships PasswordAuthentication no. Soft preserves the image-default access policy
// (no AllowGroups, no PermitRootLogin override — see build99); only strict applies
// the access lockdown.
func conf00(strict bool) string {
	pwAuth := "yes"
	if strict {
		pwAuth = "no"
	}
	return fmt.Sprintf("PasswordAuthentication %s\nKbdInteractiveAuthentication %s\n", pwAuth, pwAuth)
}

func (a A2SSH) Run(ctx *Context) (Status, string, error) {
	admin := ctx.Cfg.AdminUser
	strict := ctx.Cfg.Mode == config.ModeStrict

	conf99 := build99(ctx, strict)

	// 0. Brownfield coexistence: add existing key users to sshusers BEFORE the
	// AllowGroups sshusers drop-in takes effect, so we never lock out a user that
	// already has a working key. Additive only (never lockout-capable). Only needed
	// in strict mode, which is the only mode that writes AllowGroups sshusers; soft
	// leaves access image-default, so adding group members there is pointless.
	if strict && !ctx.Facts.Greenfield {
		if script, added := preserveKeyUsers(ctx.Facts.SSHKeyUsers); len(added) > 0 {
			ctx.Cli.Sudo(script)
			ctx.Log.Detail("added existing key users to sshusers: %v", added)
		}
	}

	// 1. Write drop-ins. mkdir the cloud-init dir FIRST so any putFile targeting it
	// (and its chmod) succeeds even on a box without cloud-init pre-provisioned.
	// Strict-only: drop the cloud-init ssh_pwauth:false override + neutralize the
	// stock 50-cloud-init PasswordAuthentication. Soft leaves cloud-init alone so
	// password login remains available.
	write := "mkdir -p /etc/cloud/cloud.cfg.d\n" +
		putFile("/etc/ssh/sshd_config.d/00-hardening.conf", conf00(strict), "0644") +
		putFile("/etc/ssh/sshd_config.d/99-hardening.conf", conf99, "0644")
	if strict {
		write += putFile("/etc/cloud/cloud.cfg.d/99-disable-passwords.cfg", "ssh_pwauth: false\n", "0644") +
			cloudInitPwAuthOff()
	} else {
		write += cloudInitPwAuthOn()
	}
	if r := ctx.Cli.Sudo(write); r.RC != 0 {
		return StatusFail, "writing sshd drop-ins failed: " + firstLine(r.Stderr), fmt.Errorf("sshd config write failed")
	}

	// 2. Syntax gate BEFORE any destructive key step.
	if status, detail, err := syntaxGate(ctx); err != nil {
		return status, detail, err
	}

	// 3. Minimal host-key path (drop surplus ecdsa, trim moduli) — skip-if clean.
	ctx.Cli.Sudo(hostKeyScript)

	// 4. Arm ssh-revert fail-safe, then ONE restart (applies config + host keys).
	armSSHRevert(ctx)
	if r := ctx.Cli.Sudo("systemctl restart ssh"); r.RC != 0 {
		return StatusFail, "ssh restart failed: " + firstLine(r.Stderr), fmt.Errorf("ssh restart failed")
	}

	// 5. Second-session key verify as the admin user (AllowGroups active).
	ctx.Log.Detail("verifying key login as %s in a fresh session…", admin)
	if err := freshLogin(ctx, admin); err != nil {
		return StatusFail, "admin key login verify failed: " + err.Error() + " (ssh-revert will restore in <300s)", fmt.Errorf("ssh hardening locked out admin")
	}

	// Verify succeeded — disarm ssh-revert.
	disarmSSHRevert(ctx)

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

	return StatusOK, "SSH hardened, admin key verified; " + effectivePolicy(ctx), nil
}

// ---------------------------------------------------------------------------
// A2Safe / A2Danger — the default-no-lockout split (TUI security menu).
//
// A2Safe applies ONLY the crypto/cipher/KEX/host-key/forwarding hardening and
// verifies key login in a fresh session. It deliberately leaves the access
// policy at the image default: NO AllowGroups, NO PermitRootLogin override (stays
// prohibit-password from the image), NO PasswordAuthentication line (image
// default untouched), and it NEVER locks the root password. This is the default
// path and CANNOT lock anyone out.
//
// A2Danger is the opt-in lockdown: AllowGroups sshusers + PermitRootLogin no +
// PasswordAuthentication no + passwd -l root. It REUSES A2Safe's ssh-revert
// fail-safe timer and the second-session key-only verify BEFORE locking root, so
// a failed key login self-heals. A returned non-nil error aborts the run
// (lockout-capable).
// ---------------------------------------------------------------------------

// A2Safe writes SSH crypto hardening only — image-default access policy preserved.
type A2Safe struct{}

func (A2Safe) ID() string    { return "A2-safe" }
func (A2Safe) Title() string { return "SSH crypto only + install admin key" }

func (A2Safe) Run(ctx *Context) (Status, string, error) {
	admin := ctx.Cfg.AdminUser

	// 0. Idempotently (re)install the admin authorized_keys line so the fresh-session
	// verify can succeed even if PRE's append was skipped (the user must already
	// exist — created by PRE; this only tops up the key, never creates the user).
	if ctx.AuthLine != "" {
		ctx.Cli.Sudo(installAdminKey(admin, ctx.AuthLine))
	}

	// 1. Write the crypto drop-ins. 00-hardening is NOT written (no
	// PasswordAuthentication line — image default kept). 99-hardening carries
	// crypto only (no AllowGroups, no PermitRootLogin override).
	if r := ctx.Cli.Sudo(buildSafeWrite(ctx)); r.RC != 0 {
		return StatusFail, "writing sshd drop-ins failed: " + firstLine(r.Stderr), fmt.Errorf("sshd config write failed")
	}

	// 2. Syntax gate BEFORE any restart.
	if status, detail, err := syntaxGate(ctx); err != nil {
		return status, detail, err
	}

	// 3. Minimal host-key path.
	ctx.Cli.Sudo(hostKeyScript)

	// 4. Arm ssh-revert, then ONE restart.
	armSSHRevert(ctx)
	if r := ctx.Cli.Sudo("systemctl restart ssh"); r.RC != 0 {
		return StatusFail, "ssh restart failed: " + firstLine(r.Stderr), fmt.Errorf("ssh restart failed")
	}

	// 5. Fresh-session key verify (admin login still open — no AllowGroups gate).
	ctx.Log.Detail("verifying key login as %s in a fresh session…", admin)
	if err := freshLogin(ctx, admin); err != nil {
		return StatusFail, "admin key login verify failed: " + err.Error() + " (ssh-revert will restore in <300s)", fmt.Errorf("ssh hardening locked out admin")
	}
	disarmSSHRevert(ctx)

	return StatusOK, "SSH crypto hardened (access policy unchanged); " + effectivePolicy(ctx), nil
}

// A2Danger applies the opt-in access lockdown (AllowGroups + key-only + root lock).
type A2Danger struct{}

func (A2Danger) ID() string    { return "A2-danger" }
func (A2Danger) Title() string { return "Key-only + block root + AllowGroups sshusers" }

func (A2Danger) Run(ctx *Context) (Status, string, error) {
	admin := ctx.Cfg.AdminUser

	// 0. Ensure the admin key is present before we cut off password / root login.
	if ctx.AuthLine != "" {
		ctx.Cli.Sudo(installAdminKey(admin, ctx.AuthLine))
	}

	// 0b. Precondition guard (F04): the lockdown writes `AllowGroups sshusers` +
	// `PermitRootLogin no`. If the admin user was never created in sshusers (PRE
	// skipped) or its authorized_keys is empty, the restart activates AllowGroups
	// with no eligible member and there is no working key — a hard lockout that
	// only the 300s ssh-revert timer would heal. Refuse up front instead of
	// leaning on that net. Admin name is charset-validated in config.Validate.
	if status, detail, err := assertAdminLoginable(ctx, admin); err != nil {
		return status, detail, err
	}

	// 0c. Brownfield coexistence: add existing key users to sshusers BEFORE the
	// AllowGroups sshusers drop-in is written, so the lockdown does not lock out a
	// user that already has a working key. Additive only (never lockout-capable).
	if !ctx.Facts.Greenfield {
		if script, added := preserveKeyUsers(ctx.Facts.SSHKeyUsers); len(added) > 0 {
			ctx.Cli.Sudo(script)
			ctx.Log.Detail("added existing key users to sshusers: %v", added)
		}
	}

	// 1. Write the DANGER drop-in: AllowGroups + PermitRootLogin no + key-only.
	// Also neutralize cloud-init's password override so it can't re-enable
	// PasswordAuthentication on the next boot.
	if r := ctx.Cli.Sudo(buildDangerWrite(ctx)); r.RC != 0 {
		return StatusFail, "writing sshd lockdown drop-in failed: " + firstLine(r.Stderr), fmt.Errorf("sshd config write failed")
	}

	// 2. Syntax gate BEFORE the restart that could lock root out.
	if status, detail, err := syntaxGate(ctx); err != nil {
		return status, detail, err
	}

	// 3. Arm ssh-revert fail-safe (the lockdown is lockout-capable), then restart.
	armSSHRevert(ctx)
	if r := ctx.Cli.Sudo("systemctl restart ssh"); r.RC != 0 {
		return StatusFail, "ssh restart failed: " + firstLine(r.Stderr), fmt.Errorf("ssh restart failed")
	}

	// 4. Second-session key-only verify as admin (AllowGroups now active) BEFORE we
	// lock root — if the admin can't get back in, abort and let ssh-revert restore.
	ctx.Log.Detail("verifying key login as %s in a fresh session before locking root…", admin)
	if err := freshLogin(ctx, admin); err != nil {
		return StatusFail, "admin key login verify failed: " + err.Error() + " (ssh-revert will restore in <300s)", fmt.Errorf("ssh lockdown locked out admin")
	}

	// 5. Executor handoff FIRST, while ssh-revert is still armed and root not yet
	// locked (F12): if the handoff fails here the box self-heals via the 300s timer
	// and root is still usable, instead of being left admin-only with the timer
	// already disarmed. Only after a successful handoff do we disarm + lock root.
	if ctx.Cli.User == "root" {
		if err := ctx.Cli.SwitchUser(admin); err != nil {
			return StatusFail, "executor handoff to admin failed: " + err.Error() + " (ssh-revert still armed; root not yet locked — box will self-restore in <300s)", fmt.Errorf("handoff failed")
		}
		ctx.Log.Detail("executor switched to %s@%s (key + sudo)", admin, ctx.Cfg.Host)
	}

	// 6. Handoff proven (or already non-root) — disarm the fail-safe and lock the
	// root password. Both run as admin+sudo when handoff happened.
	disarmSSHRevert(ctx)
	ctx.Cli.Sudo("passwd -l root")

	return StatusOK, "SSH locked down (key-only, root blocked); " + effectivePolicy(ctx), nil
}

// ---------------------------------------------------------------------------
// Shared script builders (pure — no SSH; assertable in tests) and helpers.
// ---------------------------------------------------------------------------

// hostKeyScript drops the surplus ecdsa host key and trims weak moduli, skipping
// the work if the box is already clean. Shared by A2SSH / A2Safe.
const hostKeyScript = `skip=0
[ ! -f /etc/ssh/ssh_host_ecdsa_key ] && [ -f /etc/ssh/ssh_host_ed25519_key ] && skip=1
if [ "$skip" = "0" ]; then
  rm -f /etc/ssh/ssh_host_ecdsa_key /etc/ssh/ssh_host_ecdsa_key.pub
  awk '$5 >= 3071' /etc/ssh/moduli > /etc/ssh/moduli.safe && [ "$(wc -l < /etc/ssh/moduli.safe)" -ge 20 ] && mv /etc/ssh/moduli.safe /etc/ssh/moduli || rm -f /etc/ssh/moduli.safe
fi
`

// cloudInitPwAuthOn rewrites the stock 50-cloud-init drop-in to re-enable
// password auth (soft path of the legacy A2SSH).
func cloudInitPwAuthOn() string {
	return `if [ -f /etc/ssh/sshd_config.d/50-cloud-init.conf ]; then
  sed -ri 's/^\s*PasswordAuthentication\s+no/PasswordAuthentication yes/' /etc/ssh/sshd_config.d/50-cloud-init.conf
fi
`
}

// cloudInitPwAuthOff neutralizes the stock 50-cloud-init drop-in's password auth
// (strict path of the legacy A2SSH and the danger split).
func cloudInitPwAuthOff() string {
	return `if [ -f /etc/ssh/sshd_config.d/50-cloud-init.conf ]; then
  sed -ri 's/^\s*PasswordAuthentication\s+yes/PasswordAuthentication no/' /etc/ssh/sshd_config.d/50-cloud-init.conf
fi
`
}

// installAdminKey appends the admin authorized_keys line idempotently (the user
// must already exist). Mirrors precond.putAuthorizedKey; used by the split steps
// so a stand-alone A2-safe/A2-danger run still has a working key.
func installAdminKey(admin, line string) string {
	return putAuthorizedKey(admin, line)
}

// preserveKeyUsers adds every existing key user (except root) to the sshusers
// group so the AllowGroups sshusers lockdown does not lock out users that already
// have a working key. Purely additive (only GRANTS access — never lockout-capable)
// and best-effort (`|| true`). Returns the script and the list of users added
// (for logging). Users come from detect (charset-validated /home/* basenames),
// and each is shell-quoted defensively. Returns ("", nil) when there is nothing
// to do, so the caller can skip the round-trip.
func preserveKeyUsers(users []string) (string, []string) {
	var b strings.Builder
	var added []string
	for _, u := range users {
		if u == "" || u == "root" {
			continue
		}
		fmt.Fprintf(&b, "usermod -aG sshusers %s 2>/dev/null || true\n", shellQuote(u))
		added = append(added, u)
	}
	return b.String(), added
}

// shellQuote wraps s in single quotes, escaping any embedded single quote, so an
// arbitrary username can be interpolated into a shell command safely.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildSafeWrite is the pure SAFE drop-in writer: crypto-only 99-hardening.conf,
// no 00-hardening.conf (no PasswordAuthentication line), no AllowGroups, no
// PermitRootLogin override. Image-default access policy is preserved.
func buildSafeWrite(ctx *Context) string {
	return "mkdir -p /etc/ssh/sshd_config.d\n" +
		putFile("/etc/ssh/sshd_config.d/99-hardening.conf", safe99(ctx), "0644")
}

// buildDangerWrite is the pure DANGER drop-in writer: the access lockdown
// (AllowGroups + PermitRootLogin no + PasswordAuthentication no) plus the
// cloud-init password-off neutralization so a reboot can't re-open password auth.
func buildDangerWrite(ctx *Context) string {
	_ = ctx
	return "mkdir -p /etc/ssh/sshd_config.d /etc/cloud/cloud.cfg.d\n" +
		putFile("/etc/ssh/sshd_config.d/00-hardening.conf", conf00(true), "0644") +
		putFile("/etc/ssh/sshd_config.d/98-access.conf", danger99(), "0644") +
		putFile("/etc/cloud/cloud.cfg.d/99-disable-passwords.cfg", "ssh_pwauth: false\n", "0644") +
		cloudInitPwAuthOff()
}

// danger99 is the access-lockdown drop-in content (no crypto — A2Safe owns that).
func danger99() string {
	return "PermitRootLogin no\nAllowGroups sshusers\n"
}

// assertAdminLoginable is the F04 precondition guard for the A2-danger lockdown.
// It verifies, BEFORE the AllowGroups sshusers + PermitRootLogin no drop-in is
// written, that the admin user (a) is a member of sshusers and (b) has a
// non-empty authorized_keys. Either failing means PRE never ran for this box, so
// the lockdown would cut every login path; we StatusFail with a "run PRE first"
// message rather than relying on the ssh-revert timer. The admin name is already
// charset-validated in config.Validate, so the unquoted interpolation is safe.
func assertAdminLoginable(ctx *Context, admin string) (Status, string, error) {
	groups := ctx.Cli.Sudo(fmt.Sprintf("id -nG %s 2>/dev/null", admin))
	if groups.RC != 0 {
		return StatusFail, fmt.Sprintf("admin user %q not found — run the PRE step first", admin),
			fmt.Errorf("admin precondition: user missing")
	}
	inGroup := false
	for _, g := range strings.Fields(groups.Out()) {
		if g == "sshusers" {
			inGroup = true
			break
		}
	}
	if !inGroup {
		return StatusFail, fmt.Sprintf("admin user %q not in sshusers — run the PRE step first (AllowGroups would lock everyone out)", admin),
			fmt.Errorf("admin precondition: not in sshusers")
	}
	if r := ctx.Cli.Sudo(fmt.Sprintf("test -s /home/%s/.ssh/authorized_keys", admin)); r.RC != 0 {
		return StatusFail, fmt.Sprintf("admin %q has no authorized_keys — run the PRE step first (key-only login would fail)", admin),
			fmt.Errorf("admin precondition: empty authorized_keys")
	}
	return StatusOK, "", nil
}

// syntaxGate runs `sshd -t`; on failure it removes the drop-ins this package
// writes and returns a hard error so the caller aborts before any restart.
func syntaxGate(ctx *Context) (Status, string, error) {
	if r := ctx.Cli.Sudo("sshd -t"); r.RC != 0 {
		ctx.Cli.Sudo("rm -f /etc/ssh/sshd_config.d/00-hardening.conf /etc/ssh/sshd_config.d/98-access.conf /etc/ssh/sshd_config.d/99-hardening.conf")
		return StatusFail, "sshd -t rejected config (removed drop-ins): " + firstLine(r.Stderr), fmt.Errorf("sshd -t failed")
	}
	return StatusOK, "", nil
}

// armSSHRevert installs a 300s self-healing timer that strips the morgward
// drop-ins and reloads sshd if it is not disarmed first.
func armSSHRevert(ctx *Context) {
	ctx.Cli.Sudo(`systemctl stop ssh-revert.timer 2>/dev/null || true
systemctl reset-failed 'ssh-revert.*' 2>/dev/null || true
systemd-run --on-active=300 --unit=ssh-revert sh -c 'rm -f /etc/ssh/sshd_config.d/00-hardening.conf /etc/ssh/sshd_config.d/98-access.conf /etc/ssh/sshd_config.d/99-hardening.conf; [ ! -f /etc/ssh/ssh_host_ed25519_key ] && ssh-keygen -A; systemctl reload ssh'`)
}

// disarmSSHRevert cancels the ssh-revert fail-safe after a verified key login.
func disarmSSHRevert(ctx *Context) {
	ctx.Cli.Sudo(`systemctl stop ssh-revert.timer 2>/dev/null || true
systemctl reset-failed 'ssh-revert.*' 2>/dev/null || true`)
}

// effectivePolicy reports the live PermitRootLogin / PasswordAuthentication for
// the step detail line.
func effectivePolicy(ctx *Context) string {
	return ctx.Cli.Sudo(`sshd -T 2>/dev/null | grep -Ei 'permitrootlogin|passwordauthentication' | tr '\n' ' '`).Out()
}

// build99 assembles 99-hardening.conf with version-conditional tokens.
//
// LEGACY (A2SSH): in SOFT mode it emits crypto + session knobs ONLY, leaving the
// image-default access policy intact (NO AllowGroups, NO PermitRootLogin override)
// — so the default `run` can never lock the operator out. STRICT mode additionally
// emits the access lockdown (PermitRootLogin no + AllowGroups sshusers). The split
// steps use safe99 / danger99 to carve the same boundary.
func build99(ctx *Context, strict bool) string {
	var b strings.Builder
	b.WriteString(`MaxAuthTries 3
LoginGraceTime 30
ClientAliveInterval 300
ClientAliveCountMax 2
`)
	if strict {
		b.WriteString("PermitRootLogin no\nAllowGroups sshusers\n")
	}
	b.WriteString(cryptoBlock(ctx))
	return b.String()
}

// safe99 is the crypto-only 99-hardening.conf for the default path: the shared
// cryptoBlock WITHOUT any AllowGroups or PermitRootLogin line (image default
// access policy preserved). It still carries the session knobs the crypto block
// owns (MaxAuthTries/timeouts/forwarding off) — those don't gate access.
func safe99(ctx *Context) string {
	var b strings.Builder
	b.WriteString(`MaxAuthTries 3
LoginGraceTime 30
ClientAliveInterval 300
ClientAliveCountMax 2
`)
	b.WriteString(cryptoBlock(ctx))
	return b.String()
}

// cryptoBlock is the version-conditional cipher/KEX/MAC/host-key/forwarding body
// shared by build99 (legacy) and safe99 (split). It contains NO access-policy
// directives (no AllowGroups, no PermitRootLogin, no PasswordAuthentication).
func cryptoBlock(ctx *Context) string {
	var b strings.Builder
	// KexAlgorithms: mlkem first on 26.04 (OpenSSH 10.x); dropped on 24.04 (9.6p1).
	// F20: gate strictly on a CONFIRMED 26.04. An unknown/misdetected version now
	// falls back to the conservative 24.04 set (no mlkem) rather than assuming
	// newest — the 24.04 KEX list is valid on 26.04 too, so a glitched os-release
	// probe degrades safely instead of emitting OpenSSH-10-only tokens.
	kex := "sntrup761x25519-sha512@openssh.com,curve25519-sha256,curve25519-sha256@libssh.org,diffie-hellman-group16-sha512,diffie-hellman-group18-sha512,diffie-hellman-group-exchange-sha256"
	if ctx.Facts.Is2604 {
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
	// F20: confirmed 26.04 only — an unknown version conservatively omits them
	// (sshd -t on a pre-9.8 box would otherwise reject the directive).
	if ctx.Facts.Is2604 {
		b.WriteString("PerSourcePenalties authfail:30 noauth:15 grace-exceeded:120\n")
		if ctx.Facts.ClientIP != "" {
			fmt.Fprintf(&b, "PerSourcePenaltyExemptList %s\n", ctx.Facts.ClientIP)
		}
	}
	return b.String()
}
