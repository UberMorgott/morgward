package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/steps"
	"github.com/UberMorgott/morgward/internal/ui"
)

// revertScript maps a step ID to the single-logical-line shell snippet that
// undoes that step's on-box changes. Each snippet removes ONLY the artifacts the
// step actually writes (verified against the step source / tweaks probe Cmds) and
// reloads/restarts the affected service. Snippets are best-effort: every dangerous
// sub-action is guarded with `2>/dev/null` / `|| true` so a single missing artifact
// never makes the whole revert fail.
//
// NOT revertable (deliberately absent): A8 (full upgrade + reboot — nothing to
// undo, and a reboot is not an "undo"), A7 (cleanup/purge — irreversible package
// removal), A2.5 (cloud-init disable — re-enabling cloud-init on a live box is
// lockout-adjacent and out of scope). IsRevertable gates these out for the TUI.
//
// LOCKOUT NOTE: A2's revert REMOVES the SSH hardening drop-ins and re-runs
// `ssh-keygen -A` if host keys are missing, then reloads sshd. This OPENS access
// (restores the image-default policy) — it never tightens it — so it cannot lock
// the operator out. It reuses the exact body of the a2_ssh.go ssh-revert payload
// (armSSHRevert), run immediately instead of via a systemd-run timer.
var revertScript = map[string]string{
	"A1":   `iptables -F INPUT 2>/dev/null; iptables -P INPUT ACCEPT 2>/dev/null; ip6tables -F INPUT 2>/dev/null; ip6tables -P INPUT ACCEPT 2>/dev/null; rm -f /etc/iptables/rules.v4 /etc/iptables/rules.v6 /usr/local/sbin/fw-rollback.sh /root/iptables-backup.v4 /root/iptables-backup.v6; systemctl stop fw-rollback.timer 2>/dev/null || true`,
	"A2":   `rm -f /etc/ssh/sshd_config.d/00-hardening.conf /etc/ssh/sshd_config.d/98-access.conf /etc/ssh/sshd_config.d/99-hardening.conf; [ ! -f /etc/ssh/ssh_host_ed25519_key ] && ssh-keygen -A; systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || true`,
	"A3":   `rm -f /etc/fail2ban/jail.local; systemctl restart fail2ban 2>/dev/null || systemctl stop fail2ban 2>/dev/null || true`,
	"A4":   `rm -f /etc/sysctl.d/99-net-tune.conf /etc/sysctl.d/99-bbr.conf /etc/modules-load.d/bbr.conf /etc/udev/rules.d/60-io-scheduler.rules; sysctl --system >/dev/null 2>&1 || true`,
	"A5":   `rm -f /etc/sysctl.d/99-zz-kernel-harden.conf; sysctl --system >/dev/null 2>&1 || true`,
	"A6":   `rm -f /etc/systemd/journald.conf.d/99-vps-cap.conf /etc/needrestart/conf.d/50-autorestart.conf /etc/systemd/system.conf.d/limits.conf; systemctl restart systemd-journald 2>/dev/null || true`,
	"A6.5": `rm -f /etc/systemd/resolved.conf.d/99-morgward-dns.conf; systemctl restart systemd-resolved 2>/dev/null || true`,
	"A6.7": `rm -f /etc/systemd/zram-generator.conf /etc/sysctl.d/99-zram.conf; swapoff /dev/zram0 2>/dev/null || true; sed -i '/^#.*\sswap\s/s/^#//' /etc/fstab 2>/dev/null || true; swapon -a 2>/dev/null || true; systemctl disable --now earlyoom 2>/dev/null || true`,
	"A9":   `rm -f /etc/apt/apt.conf.d/20auto-upgrades /etc/apt/apt.conf.d/52-unattended-upgrades-local`,
	"A10":  `rm -f /etc/audit/rules.d/99-vps.rules /usr/local/sbin/ssh-login-notify.sh; sed -i '/ssh-login-notify/d' /etc/pam.d/sshd 2>/dev/null || true; systemctl restart auditd 2>/dev/null || true`,
}

// IsRevertable reports whether the given step ID has a per-tweak revert snippet.
// The TUI uses it to decide whether to show the [Откатить] button for an applied
// probe. Matching is exact on the canonical step ID (e.g. "A6.5"); the caller is
// responsible for passing a canonical ID (the probe carries it as Probe.Step).
func IsRevertable(stepID string) bool {
	_, ok := revertScript[stepID]
	return ok
}

// RunRevert undoes the named step IDs on an already-bootstrapped box. It mirrors
// RunSteps' shape (prepare with allowBrownfield=true, then stream a per-id
// Progress + collect a StepResult) so the run renders in the TUI exactly like a
// step apply. It is best-effort PER ID: a single id's Sudo failure marks THAT
// result FAIL but never aborts the batch — revert only OPENS access (restores
// image defaults), so it is not lockout-capable and there is no reason to abort.
//
// Non-revertable ids are skipped (logged) and counted as SKIP. Unknown ids are
// rejected up front (same contract as RunSteps) so a typo never silently no-ops.
func RunRevert(cfg *config.Config, log *ui.Logger, ids []string, h Hooks) error {
	start := time.Now()
	s, cleanup, err := prepare(cfg, log, true, false, h)
	defer cleanup()
	if err != nil {
		return err
	}

	// Validate the requested ids against the full resolvable set (same as RunSteps)
	// so an unknown id is a hard error, not a silent skip.
	_, unknown := selectSteps(ids)
	if len(unknown) > 0 {
		return fmt.Errorf("unknown step id(s): %v (valid: %v)", unknown, allStepIDs())
	}
	if len(ids) == 0 {
		return fmt.Errorf("no steps selected; valid ids: %v", allStepIDs())
	}

	s.log.Info("reverting selected steps: %v", ids)

	var c counts
	total := len(ids)
	emit := func(id, title string, i int, status string) {
		if h.OnProgress == nil {
			return
		}
		h.OnProgress(Progress{
			ID: id, Title: title,
			Index: i + 1, Total: total, Status: status,
		})
	}

	for i, raw := range ids {
		id := canonicalStepID(raw)
		title := "revert " + id
		if !IsRevertable(id) {
			s.log.Skip("revert not supported for %s", id)
			c.skip++
			c.skips = append(c.skips, SkipReason{ID: id, Reason: "revert not supported"})
			c.results = append(c.results, StepResult{
				ID: id, Title: title, Status: steps.StatusSkip,
				Detail: "revert not supported",
			})
			emit(id, title, i, steps.StatusSkip.String())
			continue
		}

		emit(id, title, i, "running")
		s.log.Step(id, title)
		r := s.cli.Sudo(revertScript[id])
		if r.RC == 0 {
			c.ok++
			s.log.OK("%s reverted", id)
			c.results = append(c.results, StepResult{
				ID: id, Title: title, Status: steps.StatusOK, Detail: "reverted",
			})
			emit(id, title, i, steps.StatusOK.String())
			continue
		}
		// FAIL for THIS id only — keep going (best-effort; revert never aborts).
		c.fail++
		detail := "revert failed: " + firstStderrLine(r.Stderr)
		s.log.Fail("%s — %s", id, detail)
		c.results = append(c.results, StepResult{
			ID: id, Title: title, Status: steps.StatusFail, Detail: detail,
		})
		emit(id, title, i, steps.StatusFail.String())
	}

	s.log.Info("revert: %d OK, %d SKIP, %d FAIL", c.ok, c.skip, c.fail)
	sum := Summary{
		OK: c.ok, Skip: c.skip, Fail: c.fail,
		Elapsed: time.Since(start),
		Results: c.results,
		Skips:   c.skips,
	}
	logBenchAndSkips(s.log, sum)
	emitDone(h, sum)
	return nil
}

// canonicalStepID maps a (possibly mixed-case) requested id to the canonical step
// ID as declared by the resolvable steps (e.g. "a6.5" -> "A6.5"). Falls back to the
// uppercased input when no step declares it, so an unknown id still reads sanely in
// logs (it was already rejected by selectSteps before we get here).
func canonicalStepID(raw string) string {
	up := strings.ToUpper(strings.TrimSpace(raw))
	for _, st := range resolvableSteps() {
		if strings.ToUpper(st.ID()) == up {
			return st.ID()
		}
	}
	return up
}

// firstStderrLine returns the first non-empty line of a command's stderr, trimmed,
// for a compact one-line failure detail. Empty stderr yields "" so the caller's
// "revert failed: " prefix degrades gracefully.
func firstStderrLine(stderr string) string {
	for ln := range strings.SplitSeq(stderr, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			return s
		}
	}
	return ""
}
