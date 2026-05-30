package steps

import (
	"fmt"
	"strings"
	"time"
)

// A8Upgrade implements §A8: full upgrade then reboot, sequenced right after A1.
// Pre-gates the reboot (no armed timer, firewall persisted + SSH port in the
// persisted ruleset), then anchors reconnection on a boot_id change.
type A8Upgrade struct{}

func (A8Upgrade) ID() string    { return "A8" }
func (A8Upgrade) Title() string { return "Full upgrade + reboot (boot_id verified)" }

func (a A8Upgrade) Run(ctx *Context) (Status, string, error) {
	port := ctx.Cfg.Port

	// PRE-GATE: timer disarmed, persisted rules exist and open the SSH port.
	gate := fmt.Sprintf(`systemctl stop fw-rollback.timer 2>/dev/null || true
systemctl reset-failed 'fw-rollback.*' 2>/dev/null || true
V4=$(wc -l < /etc/iptables/rules.v4 2>/dev/null || echo 0)
V6=$(wc -l < /etc/iptables/rules.v6 2>/dev/null || echo 0)
DP=$(grep -c -- "--dport %d" /etc/iptables/rules.v4 2>/dev/null || echo 0)
printf 'GATE v4=%%s v6=%%s dport=%%s\n' "$V4" "$V6" "$DP"`, port)
	g := ctx.Cli.Sudo(gate)
	if !strings.Contains(g.Stdout, "dport=") || strings.Contains(g.Stdout, "dport=0") || strings.Contains(g.Stdout, "v4=0") {
		return StatusFail, "pre-reboot gate failed: " + firstLine(g.Stdout), fmt.Errorf("firewall not safely persisted before reboot")
	}
	ctx.Log.Detail("pre-reboot gate: %s", firstLine(g.Stdout))

	// Full upgrade.
	//
	// stdbuf on apt-get keeps apt's own progress line-buffered so it streams live.
	// But stdbuf works via LD_PRELOAD=libstdbuf.so (+ _STDBUF_*), which apt would
	// otherwise leak into every child it spawns (dpkg → maintainer postinst →
	// update-initramfs / dracut). On rust-coreutils (uutils) boxes those children
	// can't resolve libstdbuf.so in their context, so ld.so spams a harmless but
	// alarming "object '…/libstdbuf.so' from LD_PRELOAD cannot be preloaded …
	// ignored" for the whole upgrade.
	//
	// Fix: keep stdbuf on apt-get, but point apt at a tiny dpkg wrapper via
	// Dir::Bin::dpkg (apt.conf: the location of the dpkg program apt execs — the
	// same args, --status-fd/--configure/--unpack/…, are forwarded). The wrapper
	// scrubs the stdbuf env vars before exec'ing the real dpkg, so dpkg and all
	// maintainer scripts run without LD_PRELOAD → no spam. The wrapper lives in
	// /usr/local/sbin (root-owned, exec-allowed) — never /tmp, which a hardened box
	// may mount noexec. The earlier `apt-get update` keeps plain stdbuf: it spawns
	// no postinst triggers, so it has no children to leak into.
	const dpkgWrapper = "/usr/local/sbin/morgward-dpkg"
	wrapperBody := "#!/bin/sh\n" +
		`exec env -u LD_PRELOAD -u _STDBUF_O -u _STDBUF_E -u _STDBUF_I /usr/bin/dpkg "$@"` + "\n"

	// Count the packages a full-upgrade WOULD install/upgrade, BEFORE running it
	// (simulation; heredoc-free). `apt-get -s full-upgrade` lists each change as an
	// "Inst <pkg> ..." line, so a grep -c is the upgrade count. Best-effort: a parse
	// miss just yields an empty marker, which the engine treats as 0.
	upCount := ctx.Cli.Sudo(
		"DEBIAN_FRONTEND=noninteractive apt-get -s full-upgrade 2>/dev/null | grep -c '^Inst' || echo 0").Out()

	ctx.Log.Detail("apt-get full-upgrade (this can take several minutes)…")
	up := ctx.Cli.Sudo(`export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a
` + putFile(dpkgWrapper, wrapperBody, "0755") + `stdbuf -oL -eL apt-get update
stdbuf -oL -eL apt-get full-upgrade -y -o Dir::Bin::dpkg=` + dpkgWrapper + ` -o Dpkg::Options::="--force-confdef" -o Dpkg::Options::="--force-confold"
__rc=$?
rm -f ` + dpkgWrapper + `
exit $__rc`)
	if up.RC != 0 {
		return StatusFail, "full-upgrade failed: " + firstLine(up.Stderr), fmt.Errorf("upgrade failed")
	}

	// Capture boot_id, reboot, poll for a NEW boot_id.
	pre := ctx.Cli.BootID()
	if pre == "" {
		return StatusFail, "could not read boot_id before reboot", fmt.Errorf("boot_id unreadable")
	}
	ctx.Log.Detail("rebooting (pre boot_id %s)…", short(pre))
	// `reboot` drops the connection; ignore the resulting transport error.
	ctx.Cli.Sudo("(sleep 1; systemctl reboot) >/dev/null 2>&1 &")

	newID, err := ctx.Cli.WaitForReboot(pre, 10*time.Minute, func(msg string) {
		ctx.Log.Detail("%s", msg)
	})
	if err != nil {
		return StatusFail, err.Error(), err
	}
	ctx.State.BootID = newID
	ctx.State.Save()

	// Post-reboot health + firewall re-check.
	health := ctx.Cli.Run("systemctl is-system-running").Out()
	fwOK := ctx.Cli.Sudo(fmt.Sprintf(`iptables -S | grep -q -- "--dport %d" && iptables -S | grep -q -- "-P INPUT DROP" && echo ok`, port)).Out()
	if fwOK != "ok" {
		return StatusFail, "firewall not loaded after reboot (boot default-ACCEPT?)", fmt.Errorf("post-reboot firewall missing")
	}
	return StatusOK, fmt.Sprintf("rebooted (new boot_id %s, system %s), firewall reloaded UPGRADED_COUNT=%s",
		short(newID), health, upCount), nil
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
