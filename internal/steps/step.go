// Package steps implements each runbook block (A1..A10) as a discrete Step.
// Steps are stateless: they read facts/config from the Context, run remote
// command blocks via the SSH executor, and report OK / SKIP / FAIL.
package steps

import (
	"context"
	"encoding/base64"
	"fmt"

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

// aptInstall returns the streamed non-interactive install line for pkgs (a
// space-separated package list), terminated by a newline. stdbuf keeps apt's
// progress line-buffered so it streams live.
func aptInstall(pkgs string) string {
	return "stdbuf -oL -eL " + aptGet + " install -y " + pkgs + "\n"
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

// freshLogin opens an INDEPENDENT new SSH session (the runbook's [LOCAL]
// second-session verify) using key auth as user, runs `true`, and closes it.
// Proves reachability without relying on the kept-open executor connection.
func freshLogin(ctx *Context, user string) error {
	c, err := sshx.Dial(ctx.Cfg.Host, ctx.Cfg.Port, user, "", ctx.KeyPEM)
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
