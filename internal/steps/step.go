// Package steps implements each runbook block (A1..A10) as a discrete Step.
// Steps are stateless: they read facts/config from the Context, run remote
// command blocks via the SSH executor, and report OK / SKIP / FAIL.
package steps

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/ui"
)

// Status is the tri-state result of a step.
type Status int

const (
	StatusOK Status = iota
	StatusSkip
	StatusFail
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusSkip:
		return "SKIP"
	default:
		return "FAIL"
	}
}

// BenchResult carries the §A4 internet throughput benchmark (PRE vs POST tuning)
// out of the step so the engine can surface it in the final run summary. OK is
// true only when both samples were valid (a comparable PRE→POST pair); when false
// the renderers omit the bench line entirely.
type BenchResult struct {
	PreMBs   float64 // pre-tuning median throughput, MB/s
	PostMBs  float64 // post-tuning median throughput, MB/s
	Ratio    float64 // PostMBs / PreMBs
	OK       bool    // true ⇒ valid pair measured AND tuning kept
	Reverted bool    // true ⇒ tuning regressed throughput and was rolled back
}

// Context carries everything a step needs. The SSH client is shared and may be
// reconnected (A8 reboot) or have its identity switched (A2 strict handoff).
type Context struct {
	// Ctx carries run-scoped cancellation. The engine checks it only BETWEEN steps
	// (never mid-step), so a step's own lockout-capable sequence — SSH lockdown,
	// firewall, sysctl — always runs to completion once started; cancellation halts
	// the run at the next safe step boundary, not in the middle of a drop-in. Steps
	// that perform long polled waits MAY honor it at their own safe points, but most
	// must NOT. Never nil at runtime (prepare sets it; defaults to context.Background).
	Ctx      context.Context
	Cli      *sshx.Client
	Log      *ui.Logger
	Cfg      *config.Config
	Facts    *detect.Facts
	AuthLine string // public key authorized_keys line to install for the admin user
	KeyPEM   []byte // private key PEM (for [LOCAL] second-session verify)

	// Bench is populated by §A4 with the internet throughput benchmark so the
	// engine can lift it into the run Summary (nil until A4 runs; see BenchResult).
	Bench *BenchResult
}

// Step is one runbook block.
type Step interface {
	ID() string
	Title() string
	// Run returns a status, a short human detail line, and a hard error only for
	// lockout-capable failures that must abort the whole run.
	Run(ctx *Context) (Status, string, error)
}

// aptGet is `apt-get` carrying the dpkg lock timeout EVERY lock-acquiring call
// must have (v0.7.3): unattended-upgrades holding the dpkg lock on a fresh-boot
// box then waits up to 5 min instead of aborting the step. Build every apt-get
// invocation from this const (or from aptInstall) so the flag cannot be forgotten.
const aptGet = "apt-get -o DPkg::Lock::Timeout=300"

// needrestartConf is the needrestart drop-in every apt-invoking step lands before it
// touches apt. `restart = 'a'` auto-restarts affected services instead of drawing the
// ncurses "Which services should be restarted?" checklist, and `kernelhints = -1`
// suppresses the "Pending kernel upgrade" message box — which NEEDRESTART_MODE=a does
// NOT cover. Our remote scripts are piped to bash over stdin with no tty (§A1), so a
// dialog that tries to read the terminal takes the whole step down with it.
const needrestartConf = "$nrconf{restart} = 'a';\n$nrconf{kernelhints} = -1;\n"

// AptUpdateMarker is the per-boot marker file that records "this box's apt index
// has already been refreshed by morgward in this boot". It lives under /run, which
// is a tmpfs, so it disappears on reboot and the next run refreshes again.
const AptUpdateMarker = "/run/morgward-apt-updated"

// aptUpdateOnce refreshes the apt index ONCE per boot, before any install. A fresh
// cloud image ships an EMPTY apt index, so an install issued before the first
// `apt-get update` dies with "Package iptables-persistent is not available" /
// "Unable to locate package fail2ban" — which is exactly how a selective `step A1`
// / `step A3` run (one that never went through PRE) used to fail. Every failure
// mode is swallowed (`|| true`): the marker is only created on a SUCCESSFUL
// refresh, so a transient mirror outage just means the next step tries again, and
// the compound never trips a caller's `set -e`.
const aptUpdateOnce = "if [ ! -e " + AptUpdateMarker + " ]; then " +
	aptGet + " update >/dev/null 2>&1 && : > " + AptUpdateMarker + " || true; fi\n"

// aptPrelude is the shell prelude EVERY remote script that invokes apt-get/dpkg must
// start with — the ONE place the non-interactive contract is defined. Env covers the
// invocation (debconf, needrestart, ucf); the drop-in covers anything apt spawns
// later and a selective `step` run that never went through PRE; the once-per-boot
// index refresh guarantees an install never runs against an empty index. Idempotent;
// all callers run it through Cli.Sudo (root). Pair with aptConfOpts on any subcommand
// that unpacks packages (install / upgrade / full-upgrade / purge / autoremove).
//
// NEEDRESTART_SUSPEND=1 sits alongside NEEDRESTART_MODE=a on purpose: MODE=a still
// runs needrestart (auto-restarting services), SUSPEND=1 makes it a no-op — belt and
// braces against the ncurses checklist on a box whose needrestart ignores the mode.
func aptPrelude() string {
	return aptPreludeNoUpdate() + aptUpdateOnce
}

// aptPreludeNoUpdate is aptPrelude WITHOUT the once-per-boot index refresh, for the
// one caller that runs its own refresh right after: PRE, where a failed `apt-get
// update` must ABORT rather than be swallowed. Using the full prelude there refreshed
// the index TWICE on every first run (aptUpdateOnce, then PRE's hard gate) — a wasted
// round-trip against every mirror. Nobody else should use this: a script that installs
// without a preceding refresh dies on a fresh image's empty index.
func aptPreludeNoUpdate() string {
	return "export DEBIAN_FRONTEND=noninteractive\n" +
		"export NEEDRESTART_MODE=a\n" +
		"export NEEDRESTART_SUSPEND=1\n" +
		"export UCF_FORCE_CONFFOLD=1\n" +
		"mkdir -p /etc/needrestart/conf.d\n" +
		putFile("/etc/needrestart/conf.d/99-morgward.conf", needrestartConf, "0644") +
		aptQuiesce
}

// AptQuiesceWaitSec caps how long aptQuiesce waits for a lock holder (normally
// unattended-upgrade finishing its current package after SIGTERM) before handing
// over to apt's own DPkg::Lock::Timeout.
const AptQuiesceWaitSec = 600

// aptLockFiles are the four apt/dpkg lock files apt-get and unattended-upgrade take.
const aptLockFiles = "/var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/lib/apt/lists/lock /var/cache/apt/archives/lock"

// aptAutomationRegex matches, by full argv, the two apt-automation processes that
// survive `systemctl stop apt-daily-upgrade.service` (KillMode=process):
//   - the unattended-upgrade MAIN process ("/usr/bin/python3 /usr/bin/unattended-upgrade
//     [opts]"), but NOT the persistent "/usr/bin/python3 /usr/share/unattended-upgrades/
//     unattended-upgrade-shutdown --wait-for-signal" helper (unattended-upgrades.service),
//     whose comm is the same truncated "unattended-upgr";
//   - the "/bin/sh /usr/lib/apt/apt.systemd.daily lock_is_held ..." child (no trap in the
//     script, so SIGTERM just ends it) — otherwise it spawns unattended-upgrade AFTER
//     our first signal pass.
//
// Anchored on argv[0], so it never matches a pgrep/bash command line that merely
// carries the pattern text.
const aptAutomationRegex = `^([^ ]*python3[^ ]* /usr/bin/unattended-upgrade|[^ ]*sh /usr/lib/apt/apt\.systemd\.daily)( |$)`

// aptToolComms are the exact process names (pgrep -x) of apt front-ends and dpkg.
const aptToolComms = "apt|apt-get|aptitude|dpkg"

// aptQuiesce parks Ubuntu's apt automation for the duration of ONE script so the
// script's own apt-get never races it for the dpkg lock. Root cause it fixes: on a
// fresh boot apt-daily-upgrade.timer (Persistent=true, missed run) fires immediately
// and unattended-upgrade holds /var/lib/dpkg/lock-frontend for longer than
// DPkg::Lock::Timeout=300 on a slow box, so A8's full-upgrade died with "Unable to
// acquire the dpkg frontend lock" (and apt-daily.timer's 12h RandomizedDelaySec can
// re-fire mid-run at any time).
//
//  1. Stop apt-daily{,-upgrade}.timer ONLY if active, and restart exactly those on
//     script EXIT (bash EXIT trap, list expanded when set so a second prelude in the
//     same script cannot blank it; the script's exit status is preserved). Runtime-
//     only: nothing enabled/disabled/masked, so there is nothing for revert to undo,
//     and operator-disabled timers stay off.
//  2. `systemctl stop --no-block` the two services (--no-block: apt-daily-upgrade
//     .service has TimeoutStopSec=900). Its KillMode=process means systemd SIGTERMs
//     only the outer apt.systemd.daily shell; its lock_is_held child and the
//     unattended-upgrade it spawns are SIGTERMed directly (aptAutomationRegex), each
//     process (PID + /proc starttime, so a reused PID counts as new) once, re-checked on every wait iteration so a u-u spawned after the first
//     pass is still caught. u-u then stops after its current commit (MinimalSteps).
//  3. Bounded wait (AptQuiesceWaitSec) for remaining lock holders: fuser on the lock
//     files, pgrep -x on apt tool comms, and the unattended-upgrade main process.
//     NOT `pgrep -x unattended-upgr`: that truncated comm also matches the always-on
//     unattended-upgrade-shutdown --wait-for-signal helper, which would make every
//     script wait the full cap. One status line per 30s instead of apt's per-second
//     "Waiting for cache lock" redraws.
//  4. If the lock is free and `dpkg --audit` reports an interrupted run, finish it
//     with `dpkg --configure -a` (conffile-safe) so the caller's apt does not abort.
//
// Every command is guarded so a caller's `set -e` can never trip on it. Timeout is
// not fatal: the caller's apt-get still waits DPkg::Lock::Timeout on its own.
var aptQuiesce = "MW_APT_TIMERS=\"\"\n" +
	"for mw_t in apt-daily.timer apt-daily-upgrade.timer; do\n" +
	"  if systemctl is-active --quiet \"$mw_t\" 2>/dev/null; then MW_APT_TIMERS=\"$MW_APT_TIMERS $mw_t\"; fi\n" +
	"done\n" +
	"if [ -n \"$MW_APT_TIMERS\" ]; then\n" +
	"  systemctl stop $MW_APT_TIMERS >/dev/null 2>&1 || true\n" +
	"  trap \"systemctl start$MW_APT_TIMERS >/dev/null 2>&1 || true\" EXIT\n" +
	"fi\n" +
	"systemctl stop --no-block apt-daily.service apt-daily-upgrade.service >/dev/null 2>&1 || true\n" +
	"MW_UU_RE='" + aptAutomationRegex + "'\n" +
	"mw_sig=\"\"\n" +
	": \"${MW_PROC:=/proc}\"\n" +
	// Dedupe key = PID + starttime (stat field 22 = 20th field after the LAST ") ",
	// since comm may contain spaces/parens), so a reused PID is signalled again.
	"mw_apt_term() { for mw_p in $(pgrep -f \"$MW_UU_RE\" 2>/dev/null); do mw_k=\"$mw_p:$(sed 's/.*) //' \"$MW_PROC/$mw_p/stat\" 2>/dev/null | cut -d' ' -f20)\"; case \" $mw_sig \" in *\" $mw_k \"*) ;; *) kill -TERM \"$mw_p\" 2>/dev/null || true; mw_sig=\"$mw_sig $mw_k\" ;; esac; done; }\n" +
	"mw_apt_term\n" +
	"mw_apt_holders() { { fuser " + aptLockFiles + " 2>/dev/null; pgrep -x '" + aptToolComms + "' 2>/dev/null; pgrep -f \"$MW_UU_RE\" 2>/dev/null; } | tr -s ' \\t' '\\n' | grep -E '^[0-9]+$' | sort -un | tr '\\n' ' '; }\n" +
	"mw_w=0\n" +
	"mw_h=$(mw_apt_holders)\n" +
	"while [ -n \"$mw_h\" ] && [ \"$mw_w\" -lt " + fmt.Sprint(AptQuiesceWaitSec) + " ]; do\n" +
	"  if [ $((mw_w % 30)) -eq 0 ]; then echo \"morgward: waiting for apt automation to release the dpkg lock (${mw_w}s): $(ps -o pid=,comm= -p \"$(echo $mw_h | tr ' ' ',')\" 2>/dev/null | tr -s ' \\n' ' ')\"; fi\n" +
	"  sleep 5\n" +
	"  mw_w=$((mw_w + 5))\n" +
	"  mw_apt_term\n" +
	"  mw_h=$(mw_apt_holders)\n" +
	"done\n" +
	"if [ -z \"$mw_h\" ] && [ -n \"$(dpkg --audit 2>/dev/null)\" ]; then\n" +
	"  echo 'morgward: finishing an interrupted dpkg run (dpkg --configure -a)'\n" +
	"  dpkg --force-confdef --force-confold --configure -a >/dev/null 2>&1 || true\n" +
	"fi\n"

// aptConfOpts stops dpkg from ever prompting about a modified conffile (keep the
// installed file, take the package default where there is no local change) and
// keeps dpkg off a pty (`Dpkg::Use-Pty=0`): on a pty dpkg draws a redrawing
// progress bar full of control sequences, which is noise in a piped, non-interactive
// session and defeats line-oriented streaming.
const aptConfOpts = ` -o Dpkg::Options::="--force-confdef" -o Dpkg::Options::="--force-confold" -o Dpkg::Use-Pty=0`

// aptInstall returns the non-interactive install line for pkgs (a space-separated
// package list), terminated by a newline.
//
// NO `stdbuf`: it works via LD_PRELOAD=libstdbuf.so, which apt leaks into every
// child it spawns (dpkg → maintainer postinst → update-initramfs). On Ubuntu 26.04
// (rust-coreutils/uutils) those children cannot resolve libstdbuf.so and ld.so spams
// "object '…/libstdbuf.so' from LD_PRELOAD cannot be preloaded … ignored" for the
// whole install. Dpkg::Use-Pty=0 in aptConfOpts covers the output shape instead.
func aptInstall(pkgs string) string {
	return aptGet + " install -y" + aptConfOpts + " " + pkgs + "\n"
}

// aptInstallOptional is aptInstall for a package whose absence must NOT fail the
// step — a transitional/renamed package that may not exist on every target release
// (e.g. audispd-plugins, dropped after 24.04). Only its exit code is ignored; the
// dpkg lock timeout and non-interactive frontend are unchanged. NEVER use it for a
// package the step's result depends on — that one must fail loudly.
func aptInstallOptional(pkgs string) string {
	return strings.TrimSuffix(aptInstall(pkgs), "\n") + " || true\n"
}

// pkgInstalledCheck is the one-line "is this dpkg package installed" probe,
// echoing "yes" when it is and nothing otherwise. Used by steps that must report
// WHICH package failed to install rather than a generic "not active".
func pkgInstalledCheck(pkg string) string {
	return "dpkg-query -W -f='${Status}' " + pkg + " 2>/dev/null | grep -q 'install ok installed' && echo yes"
}

// armTimer returns the fragment that arms a 300s systemd-run fail-safe: it first
// clears any previously-armed instance of the unit (stop + reset-failed on the
// unit glob, both best-effort) so a re-run cannot inherit a stale/failed timer,
// then schedules cmd. This is the lockout-safety primitive shared by A1
// (fw-rollback), A2 (ssh-revert) and A5 (rpf-revert) — one copy, not three.
func armTimer(unit, cmd string) string {
	return disarmTimer(unit) +
		fmt.Sprintf("systemd-run --on-active=300 --unit=%s %s\n", unit, cmd)
}

// disarmTimer returns the fragment that cancels an armed fail-safe timer: stop
// the timer and reset any failed unit matching the glob (covering both the
// .timer and the .service). Best-effort — a missing unit is not an error.
func disarmTimer(unit string) string {
	return fmt.Sprintf("systemctl stop %[1]s.timer 2>/dev/null || true\n"+
		"systemctl reset-failed '%[1]s.*' 2>/dev/null || true\n", unit)
}

// putFile returns a shell fragment that writes content to path with mode, using
// nested base64 so the outer base64 script delivery stays stdin-safe and the
// content needs no shell quoting (§A1 stdin caveat).
func putFile(path, content, mode string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	return fmt.Sprintf("echo '%s' | base64 -d > '%s'\nchmod %s '%s'\n", b64, path, mode, path)
}

// pipeToBash returns a shell fragment that runs script by piping its base64-decoded
// body straight into a fresh `bash` — landing NO file on disk. This avoids the
// symlink/TOCTOU window of writing a root-executed script to a predictable path
// (e.g. /tmp/...). The base64 keeps the body stdin-safe inside the outer
// stdin-piped controller script, and `echo ... | bash` provides bash its OWN
// stdin, so it consumes nothing from the controller's stdin (no §A1 contention).
func pipeToBash(script string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(script))
	return fmt.Sprintf("echo '%s' | base64 -d | bash\n", b64)
}

// AppendLineIfMissing returns a fragment that appends line to file only if an
// exact line match is absent (idempotent edit of a shared/non-owned file). The
// line is delivered via base64 so it needs no shell quoting — the §A1 stdin-safe
// form. Exported because the engine's pre-step key bootstrap needs the SAME
// primitive; it previously carried its own %q-quoting copy that had drifted.
func AppendLineIfMissing(file, line string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(line))
	return fmt.Sprintf(
		"__L=$(echo '%s' | base64 -d); grep -qxF \"$__L\" '%s' 2>/dev/null || printf '%%s\\n' \"$__L\" >> '%s'\n",
		b64, file, file)
}

// transportLost reports whether a command Result reflects a LOST SSH TRANSPORT
// (rc = -1, or a transport error) as opposed to a command that ran to completion
// and exited non-zero. The distinction matters for every step that follows a long
// command with a reachability verify: a dropped transport says nothing about what
// the step changed, so it must not be reported as a lockout.
func transportLost(r sshx.Result) bool { return r.RC == -1 || r.Err != nil }

// freshLoginRetryDelay is how long freshLoginVerify waits before its single retry —
// long enough for sshd to finish a restart or for a transient network blip to clear,
// short enough to stay far inside the 300s fail-safe timers.
const freshLoginRetryDelay = 5 * time.Second

// freshLoginVerify is freshLogin with ONE retry after a short pause. Every step
// that gates a lockout-capable change on "can I still log in?" goes through it: a
// single immediate dial turns any transient transport hiccup — an sshd still
// restarting, a NAT blip, a session killed by an operator's stuck dpkg dialog —
// into a false lockout verdict that rolls back a change which was in fact fine.
// A cancelled run skips the wait and reports the first error.
func freshLoginVerify(ctx *Context, user string) error {
	err := freshLogin(ctx, user)
	if err == nil {
		return nil
	}
	ctx.Log.Warn("second-session verify failed (%v) — retrying once in %s", err, freshLoginRetryDelay)
	select {
	case <-ctx.Ctx.Done():
		return err
	case <-time.After(freshLoginRetryDelay):
	}
	return freshLogin(ctx, user)
}

// freshLogin opens an INDEPENDENT new SSH session (the runbook's [LOCAL]
// second-session verify) using key auth as user, runs `true`, and closes it.
// Proves reachability without relying on the kept-open executor connection.
func freshLogin(ctx *Context, user string) error {
	c, err := sshx.Dial(ctx.Ctx, ctx.Cfg.Host, ctx.Cfg.Port, user, "", ctx.KeyPEM)
	if err != nil {
		return err
	}
	defer c.Close()
	if r := c.Run("true"); !r.OK() {
		if r.Err != nil {
			return r.Err
		}
		return fmt.Errorf("remote `true` returned rc=%d", r.RC)
	}
	return nil
}
