package steps

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/UberMorgott/morgward/internal/detect"
)

// a8UpgradeScript is A8's upgrade script: the no-update prelude (which quiesces
// boot-time apt-daily/unattended-upgrades before taking the dpkg lock), then an
// explicit index refresh and the full-upgrade.
func a8UpgradeScript() string {
	return aptPreludeNoUpdate() + aptGet + " update\n" +
		aptGet + " full-upgrade -y" + aptConfOpts
}

// A8Upgrade implements §A8: full upgrade then reboot, sequenced right after A1.
// Pre-gates the reboot (no armed timer, firewall persisted + SSH port in the
// persisted ruleset), then anchors reconnection on a boot_id change.
type A8Upgrade struct{}

func (A8Upgrade) ID() string    { return "A8" }
func (A8Upgrade) Title() string { return "Full upgrade + reboot (boot_id verified)" }

func (a A8Upgrade) Run(ctx *Context) (Status, string, error) {
	port := ctx.Cfg.Port

	// PRE-GATE: always disarm any armed fw-rollback timer (it would fire mid-reboot
	// and flush the firewall regardless of who manages it). The persisted-ruleset
	// assertion that follows is FIREWALL-MANAGER-AWARE: it only applies where
	// morgward owns the iptables ruleset (greenfield, or brownfield iptables/none —
	// A1 wrote /etc/iptables/rules.v4). On a ufw/firewalld/nftables brownfield box
	// that file legitimately does not exist (A1 added `ufw allow` rules or deferred
	// entirely), so asserting it would falsely abort the whole run.
	ctx.Cli.Sudo(disarmTimer("fw-rollback"))

	if ctx.Facts.ManagesIPTables() {
		gate := fmt.Sprintf(`V4=$(wc -l < /etc/iptables/rules.v4 2>/dev/null || echo 0)
V6=$(wc -l < /etc/iptables/rules.v6 2>/dev/null || echo 0)
DP=$(grep -Ec -- '--dport %d( |$)' /etc/iptables/rules.v4 2>/dev/null || echo 0)
printf 'GATE v4=%%s v6=%%s dport=%%s\n' "$V4" "$V6" "$DP"`, port)
		g := ctx.Cli.Sudo(gate)
		if !strings.Contains(g.Stdout, "dport=") || strings.Contains(g.Stdout, "dport=0") || strings.Contains(g.Stdout, "v4=0") {
			return StatusFail, "pre-reboot gate failed: " + firstLine(g.Stdout), fmt.Errorf("firewall not safely persisted before reboot")
		}
		ctx.Log.Detail("pre-reboot gate: %s", firstLine(g.Stdout))
	} else if ctx.Facts.FirewallMgr == "ufw" {
		// ufw-managed: it persists its own rules across reboot. A reasonable
		// substitute gate is "ufw reports active" — if it is, SSH survives the reboot
		// (A1 added the `ufw allow` for it). Don't overbuild beyond that.
		if ctx.Cli.Sudo("LANG=C ufw status 2>/dev/null | grep -qi '^Status: active' && echo ok").Out() != "ok" {
			return StatusFail, "pre-reboot gate failed: ufw not active (SSH may not survive reboot)", fmt.Errorf("ufw not active before reboot")
		}
		ctx.Log.Detail("pre-reboot gate: ufw active (firewall persisted by ufw)")
	} else {
		// firewalld / nftables — the operator owns the firewall and its persistence;
		// morgward changed nothing, so there is no morgward state to gate on.
		ctx.Log.Detail("pre-reboot gate skipped: %s manages the firewall (operator-owned persistence)", ctx.Facts.FirewallMgr)
	}

	// Full upgrade.
	//
	// NO `stdbuf` and no dpkg wrapper any more. stdbuf worked via
	// LD_PRELOAD=libstdbuf.so, which apt leaked into every child it spawned (dpkg →
	// maintainer postinst → update-initramfs / dracut); on rust-coreutils (uutils)
	// boxes those children cannot resolve libstdbuf.so and ld.so spams "object
	// '…/libstdbuf.so' from LD_PRELOAD cannot be preloaded … ignored" for the whole
	// upgrade. The Dir::Bin::dpkg wrapper that used to scrub those env vars existed
	// ONLY to contain that leak, so dropping stdbuf drops the wrapper with it.
	// aptConfOpts carries `Dpkg::Use-Pty=0` for the output shape instead.

	ctx.Log.Detail("apt-get full-upgrade (this can take several minutes)…")
	// aptPreludeNoUpdate: A8 always refreshes the index explicitly right below, so
	// the prelude's once-per-boot update would only double the (silent, up to 300s
	// lock-wait) round-trip.
	up := ctx.Cli.Sudo(a8UpgradeScript())
	if up.RC != 0 {
		return StatusFail, "full-upgrade failed: " + firstLine(up.Stderr), fmt.Errorf("upgrade failed")
	}

	// Count what the REAL run changed, from apt's own summary line. A pre-run
	// `apt-get -s full-upgrade` used to do this, but it ran BEFORE the explicit
	// `apt-get update` above, so on a stale index it reported 0 while the actual
	// upgrade installed ~80 packages. Best-effort: a parse miss yields an empty
	// marker, which the engine treats as 0.
	upCount := aptChangedCount(up.Stdout)
	// Capture boot_id, reboot, poll for a NEW boot_id.
	pre := ctx.Cli.BootID()
	if pre == "" {
		return StatusFail, "could not read boot_id before reboot", fmt.Errorf("boot_id unreadable")
	}

	// BROWNFIELD service snapshot — capture what is RUNNING-but-not-auto-started
	// right before the reboot, so we can restore anything our own reboot took down.
	// Greenfield stays byte-identical: nothing below runs. Both probes are
	// best-effort — a parse/probe miss logs a Detail and leaves the slice empty;
	// they NEVER fail A8 or block the reboot.
	var snapContainers, snapUnits []string
	if !ctx.Facts.Greenfield {
		if ctx.Facts.DockerSeen {
			ps := ctx.Cli.Sudo(dockerSnapshotScript())
			if ps.OK() {
				snapContainers = fields(ps.Stdout)
			} else {
				ctx.Log.Detail("docker snapshot skipped: %s", firstLine(ps.Stderr))
			}
		}
		u := ctx.Cli.Sudo(systemdSnapshotScript())
		if u.OK() {
			snapUnits = fields(u.Stdout)
		} else {
			ctx.Log.Detail("systemd snapshot skipped: %s", firstLine(u.Stderr))
		}
	}

	ctx.Log.Detail("rebooting (pre boot_id %s)…", short(pre))
	// `reboot` drops the connection; ignore the resulting transport error.
	ctx.Cli.Sudo("(sleep 1; systemctl reboot) >/dev/null 2>&1 &")

	// From here on the box is OURS to bring back. An Esc in the TUI cancels ctx.Ctx,
	// which would make every connect() inside WaitForReboot fail instantly: the loop
	// would give up, restoreServices would never run, and a brownfield box we just
	// rebooted would be left with its `restart: no` containers and non-enabled units
	// DOWN. So the reconnect+restore phase runs under a non-cancellable copy of the
	// context (values kept, cancellation dropped). WaitForReboot's own 10-minute
	// deadline still bounds it, and the UI cancel still aborts every SUBSEQUENT step.
	newID, err := ctx.Cli.WaitForReboot(rebootCtx(ctx.Ctx), pre, 10*time.Minute, func(msg string) {
		ctx.Log.Detail("%s", msg)
	})
	if err != nil {
		return StatusFail, err.Error(), err
	}

	// Post-reboot health + firewall re-check. The iptables assertion (INPUT DROP +
	// SSH dport reloaded) is only meaningful where morgward owns the ruleset; on a
	// ufw/firewalld/nftables box the operator's manager restores its own rules, so
	// asserting raw iptables would falsely abort. Match the pre-gate's branching.
	health := ctx.Cli.Run("systemctl is-system-running").Out()
	if ctx.Facts.ManagesIPTables() {
		fwOK := ctx.Cli.Sudo(fmt.Sprintf(`iptables -S | grep -Eq -- '--dport %d( |$)' && iptables -S | grep -q -- "-P INPUT DROP" && echo ok`, port)).Out()
		if fwOK != "ok" {
			return StatusFail, "firewall not loaded after reboot (boot default-ACCEPT?)", fmt.Errorf("post-reboot firewall missing")
		}
	} else if ctx.Facts.FirewallMgr == "ufw" {
		if ctx.Cli.Sudo("LANG=C ufw status 2>/dev/null | grep -qi '^Status: active' && echo ok").Out() != "ok" {
			return StatusFail, "ufw not active after reboot (firewall not loaded?)", fmt.Errorf("post-reboot ufw inactive")
		}
	}
	// firewalld/nftables: operator-owned, nothing for morgward to assert.

	// BROWNFIELD service restore — bring back exactly the containers/units our
	// reboot bounced (running-but-not-auto-started). We `docker start` the SAME
	// container (never `docker compose`/recreate) and `systemctl start` the unit.
	// A service that fails to come back is surfaced (WARN-style Detail) but NEVER
	// turns A8 into StatusFail: a down operator service must not abort the run or
	// trip ssh-revert. Greenfield path skips this entirely (detail stays unchanged).
	tail := ""
	if !ctx.Facts.Greenfield && (len(snapContainers) > 0 || len(snapUnits) > 0) {
		restored, failed := a.restoreServices(ctx, snapContainers, snapUnits)
		tail = fmt.Sprintf("; restored %d svc/ctr, %d failed", restored, len(failed))
	}

	return StatusOK, fmt.Sprintf("rebooted (new boot_id %s, system %s), firewall reloaded UPGRADED_COUNT=%s%s",
		short(newID), health, upCount, tail), nil
}

// rebootCtx strips cancellation from the run context for the post-reboot
// reconnect+restore phase (see A8Upgrade.Run). Values survive, so anything the
// engine threads through the context still works; only the Done channel is cut.
// nil-tolerant for tests.
func rebootCtx(parent context.Context) context.Context {
	if parent == nil {
		return context.Background()
	}
	return context.WithoutCancel(parent)
}

// restoreServices restarts the snapshot containers/units that did not come back
// on their own after the reboot, and reports how many were restored vs. failed.
// Best-effort throughout; the caller never escalates a failure to StatusFail.
func (A8Upgrade) restoreServices(ctx *Context, containers, units []string) (restored int, failed []string) {
	// Docker: collect snapshot ids that are not running now, start them in one
	// call, then re-inspect to see which actually came up.
	var down []string
	for _, id := range containers {
		if ctx.Cli.Sudo(fmt.Sprintf("docker inspect -f '{{.State.Running}}' %s 2>/dev/null", detect.ShQuote(id))).Out() != "true" {
			down = append(down, id)
		}
	}
	if len(down) > 0 {
		ctx.Cli.Sudo(dockerStartScript(down))
		for _, id := range down {
			if ctx.Cli.Sudo(fmt.Sprintf("docker inspect -f '{{.State.Running}}' %s 2>/dev/null", detect.ShQuote(id))).Out() == "true" {
				restored++
				continue
			}
			name := ctx.Cli.Sudo(fmt.Sprintf("docker inspect -f '{{.Name}}' %s 2>/dev/null", detect.ShQuote(id))).Out()
			failed = append(failed, "container "+strings.TrimPrefix(name, "/"))
		}
	}

	// systemd: start each snapshot unit that is no longer active, then re-check.
	for _, unit := range units {
		if ctx.Cli.Sudo(fmt.Sprintf("systemctl is-active %s 2>/dev/null", detect.ShQuote(unit))).Out() == "active" {
			continue
		}
		ctx.Cli.Sudo(fmt.Sprintf("systemctl start %s 2>/dev/null || true", detect.ShQuote(unit)))
		if ctx.Cli.Sudo(fmt.Sprintf("systemctl is-active %s 2>/dev/null", detect.ShQuote(unit))).Out() == "active" {
			restored++
		} else {
			failed = append(failed, "unit "+unit)
		}
	}

	if restored > 0 {
		ctx.Log.Detail("post-reboot: restored %d bounced service(s)/container(s)", restored)
	}
	if len(failed) > 0 {
		ctx.Log.Detail("WARN post-reboot: did NOT restart: %s", strings.Join(failed, ", "))
	}
	return restored, failed
}

// dockerSnapshotScript lists the IDs of currently-running containers (full,
// non-truncated). Output is parsed with fields() into a []string.
func dockerSnapshotScript() string {
	return "docker ps -q --no-trunc 2>/dev/null || true"
}

// systemdSnapshotScript lists system .service units that are RUNNING but NOT
// enabled (no auto-start) — exactly the units a reboot would silently drop.
// Heredoc-free (the script is piped to bash over stdin; §A1 stdin caveat): the
// per-unit is-enabled filter keeps only units whose state is neither "enabled"
// nor "enabled-runtime". Emits kept unit names one per line.
func systemdSnapshotScript() string {
	return `for u in $(systemctl list-units --type=service --state=running --no-legend --plain --no-pager 2>/dev/null | awk '{print $1}'); do
  st=$(systemctl is-enabled "$u" 2>/dev/null)
  if [ "$st" != "enabled" ] && [ "$st" != "enabled-runtime" ]; then
    printf '%s\n' "$u"
  fi
done`
}

// dockerStartScript starts the given container ids in a single `docker start`
// (the SAME containers — never `docker compose`/recreate). Empty input yields
// a no-op so the caller need not special-case it.
func dockerStartScript(ids []string) string {
	if len(ids) == 0 {
		return "true"
	}
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = detect.ShQuote(id)
	}
	return "docker start " + strings.Join(quoted, " ") + " >/dev/null 2>&1 || true"
}

// fields splits whitespace/newline-separated command output into a slice with
// empties dropped (strings.Fields semantics, named for call-site clarity).
func fields(s string) []string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return nil
	}
	return f
}

// aptSummaryRe matches apt's always-printed run summary,
// "N upgraded, M newly installed, K to remove and J not upgraded."
var aptSummaryRe = regexp.MustCompile(`(\d+) upgraded, (\d+) newly installed`)

// aptChangedCount returns upgraded+newly-installed from the LAST apt summary line
// in out (the full-upgrade's own; an earlier `apt-get update` prints none), or ""
// when there is no such line.
func aptChangedCount(out string) string {
	m := aptSummaryRe.FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		return ""
	}
	last := m[len(m)-1]
	up, _ := strconv.Atoi(last[1])
	nw, _ := strconv.Atoi(last[2])
	return strconv.Itoa(up + nw)
}

// short truncates a boot_id to its first 8 chars for log lines.
func short(s string) string { return s[:min(8, len(s))] }
