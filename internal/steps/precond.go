package steps

import (
	"fmt"
	"strings"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/sshx"
)

// Precond implements §1 preconditions: apt index refresh, non-root sudo user
// with key auth + NOPASSWD sudoers, sshusers group, and (soft mode) a console
// password generated on-box and surfaced once.
type Precond struct{}

func (Precond) ID() string    { return "PRE" }
func (Precond) Title() string { return "§1 Preconditions (apt index, admin user, key, group)" }

func (Precond) Run(ctx *Context) (Status, string, error) {
	// apt index refresh — HARD gate before the first apt-install (A1).
	ctx.Log.Detail("apt-get update (apt index gate)…")
	upd := ctx.Cli.Sudo("export DEBIAN_FRONTEND=noninteractive; stdbuf -oL -eL apt-get update")
	if upd.RC != 0 {
		return StatusFail, "apt-get update failed: " + firstLine(upd.Stderr), fmt.Errorf("apt index refresh failed")
	}
	if cand := ctx.Cli.Run("apt-cache policy iptables-persistent | awk '/Candidate/{print $2}'").Out(); cand == "" || cand == "(none)" {
		return StatusFail, "iptables-persistent has no install candidate", fmt.Errorf("apt index empty")
	}

	admin := ctx.Cfg.AdminUser
	if ctx.AuthLine == "" {
		return StatusFail, "no public key available to install for admin user", fmt.Errorf("missing auth line")
	}

	// Create the admin user + group + key + NOPASSWD sudoers (idempotent).
	script := fmt.Sprintf(`set -e
id %[1]s >/dev/null 2>&1 || adduser --disabled-password --gecos "" %[1]s
usermod -aG sudo %[1]s
groupadd -f sshusers
usermod -aG sshusers %[1]s
install -d -m 700 -o %[1]s -g %[1]s /home/%[1]s/.ssh
%[2]s
chown %[1]s:%[1]s /home/%[1]s/.ssh/authorized_keys
chmod 600 /home/%[1]s/.ssh/authorized_keys
printf '%%s ALL=(ALL) NOPASSWD:ALL\n' %[1]s > /etc/sudoers.d/90-%[1]s
chmod 440 /etc/sudoers.d/90-%[1]s
visudo -cf /etc/sudoers.d/90-%[1]s >/dev/null
`, admin, putAuthorizedKey(admin, ctx.AuthLine))

	if r := ctx.Cli.Sudo(script); r.RC != 0 {
		return StatusFail, "admin user setup failed: " + firstLine(r.Stderr), fmt.Errorf("admin user setup failed")
	}

	// Verify the admin can sudo non-interactively.
	chk := ctx.Cli.Run(fmt.Sprintf("sudo -u %s sudo -n true 2>&1", admin))
	if chk.RC != 0 {
		ctx.Log.Warn("admin NOPASSWD sudo check inconclusive: %s", firstLine(chk.Stdout+chk.Stderr))
	}

	// Soft mode: set a console password, generated on-box, surfaced exactly once.
	if ctx.Cfg.Mode == config.ModeSoft {
		// The marker line is emitted on stdout but teeLines suppresses it from the
		// streamed sink (sshx.SecretMarkerPrefix), so the password is captured into
		// Result.Stdout for extractMarker yet never hits the log file / TUI (F02).
		pwScript := fmt.Sprintf(`set +o history
ADMIN_PW="$(openssl rand -base64 18)"
printf '%%s:%%s\n' %q "$ADMIN_PW" | chpasswd
printf '%s%%s\n' "$ADMIN_PW"
unset ADMIN_PW`, admin, sshx.SecretMarkerPrefix)
		r := ctx.Cli.Sudo(pwScript)
		if pw := extractMarker(r.Stdout, sshx.SecretMarkerPrefix); pw != "" {
			ctx.Log.Secret(fmt.Sprintf("CONSOLE PASSWORD for %s (store now — shown once)", admin), pw)
		} else {
			ctx.Log.Warn("could not set/extract console password (continuing)")
		}
	}

	ctx.State.AdminUser = admin
	ctx.State.Save()
	return StatusOK, fmt.Sprintf("admin user %q ready, sshusers group set, apt index fresh", admin), nil
}

// putAuthorizedKey writes the key line into the user's authorized_keys via base64.
func putAuthorizedKey(user, line string) string {
	return appendLineIfMissing(fmt.Sprintf("/home/%s/.ssh/authorized_keys", user), line)
}

func extractMarker(out, marker string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), marker) {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), marker))
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
