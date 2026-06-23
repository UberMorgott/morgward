// Package engine wires config -> SSH bootstrap -> detection -> ordered steps ->
// verification. It owns the password->key bootstrap, the brownfield gate, and
// the load-bearing apply order from the runbook.
package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/monitor"
	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/state"
	"github.com/UberMorgott/morgward/internal/stats"
	"github.com/UberMorgott/morgward/internal/steps"
	"github.com/UberMorgott/morgward/internal/tweaks"
	"github.com/UberMorgott/morgward/internal/ui"
	"github.com/UberMorgott/morgward/internal/verify"
)

// orderedSteps is the load-bearing apply sequence from PHASE A.
func orderedSteps() []steps.Step {
	return []steps.Step{
		steps.Precond{},
		steps.A1Firewall{},
		steps.A8Upgrade{},
		steps.A2SSH{},
		steps.A25CloudInit{},
		steps.A3Fail2ban{},
		steps.A4Network{},
		steps.A5Kernel{},
		steps.A6Maint{},
		steps.A65DNS{},
		steps.A67Memory{},
		steps.A7Cleanup{},
		steps.A9Unattended{},
		steps.A10Detection{},
	}
}

// extraSteps are step types that are NOT part of the default full-run sequence
// (orderedSteps) but ARE resolvable by `step <IDs>` / RunSteps — the TUI security
// menu drives them explicitly. A2Safe (crypto only, image-default access) and
// A2Danger (opt-in access lockdown) are the split of A2SSH; A2SSH stays the
// crypto-only full-run step (image-default access preserved).
func extraSteps() []steps.Step {
	return []steps.Step{
		steps.A2Safe{},
		steps.A2Danger{},
	}
}

// resolvableSteps is the full lookup set for selective runs: the default ordered
// sequence first (so canonical order is preserved for IDs that live there), then
// the extra opt-in steps appended in declaration order.
func resolvableSteps() []steps.Step {
	return append(orderedSteps(), extraSteps()...)
}

// session is the shared connected state produced by prepare().
type session struct {
	log    *ui.Logger
	cli    *sshx.Client
	ctx    *steps.Context
	before *stats.Snapshot // best-effort pre-run snapshot (may be nil/partial)
}

// Hooks bundles the optional callbacks the TUI uses to observe a run. Any field
// may be nil (the CLI passes a zero Hooks ⇒ unchanged behavior).
type Hooks struct {
	Sink       func(string)           // streams each log line to the caller
	OnConnect  func(monitor.ConnInfo) // fires once after key auth is active
	OnProgress func(Progress)         // fires per step and once at the end
	OnKey      func(pem string)       // fires once with the generated ed25519 PEM (password path only; never via the logger)

	// PreparedKey, when non-nil, is an ed25519 keypair the CALLER already generated
	// (the TUI pre-generates it so it can show the key to the operator BEFORE the run
	// starts). On the password path prepare() uses this keypair's AuthorizedLine +
	// PrivatePEM INSTEAD of calling sshx.GenerateKeyPair, and skips OnKey (the caller
	// already holds the PEM). nil ⇒ the original behavior (engine generates the key) —
	// the CLI leaves it nil so it is entirely unaffected.
	PreparedKey *sshx.KeyPair
}

// Progress is a single step lifecycle event (or, with Done set, the run's final
// summary). Total==0 means "no step list" (detect/verify) ⇒ TUI hides the bar.
type Progress struct {
	ID, Title    string
	Index, Total int
	Status       string // "running" or a steps.Status string (OK/SKIP/FAIL)
	Done         bool
	Summary      Summary
}

// StepResult is the outcome of a single step, accumulated in apply order so the
// CLI/TUI can render a per-step table in the summary, not just aggregate counts.
type StepResult struct {
	ID, Title string
	Status    steps.Status
	Detail    string
}

// Summary is the aggregate run outcome carried by the final Done progress event.
type Summary struct {
	OK, Skip, Fail             int
	VerifyPassed, VerifyFailed int
	Elapsed                    time.Duration

	// Results carries every step's outcome in apply order (ID/Title/Status/Detail).
	Results []StepResult

	// Before/After are best-effort system snapshots captured right after detection
	// and right after the last step; either may be nil (capture failed) and the
	// renderers hide unknown fields. Snapshots are cosmetic — never run-fatal.
	Before, After *stats.Snapshot

	// UpgradedPkgs/PurgedPkgs/Reboots are parsed from A8/A7 step markers (and A8's
	// OK status) for the summary's change tally.
	UpgradedPkgs, PurgedPkgs, Reboots int

	// Skips carries the per-skip reasons (the detail string each SKIPPED step
	// returned), so the CLI/TUI can show WHY a step was skipped, not just a count.
	Skips []SkipReason

	// Tweaks carries the per-tweak audit (the "анализ" action): every individual
	// change morgward applies, probed live, with applied/not verdict. Filled only
	// in the verify/audit path; nil for run/detect. The TUI renders it as phaseMatrix
	// (verify) or the Dashboard live audit (audit); the CLI ignores it (its verify
	// output stays the §V matrix only).
	Tweaks []tweaks.Result

	// Facts is the read-only §0.5/§2 discovery snapshot captured during prepare().
	// Filled by Audit so the Dashboard can render the server card (OS/kernel/IPv6/
	// ports); nil for run/verify/detect (those carry no Facts in the Summary). It is
	// never mutated after detection, so passing the pointer through the value-copied
	// TUI model is safe.
	Facts *detect.Facts

	// Bench* mirror steps.BenchResult: the §A4 internet throughput benchmark
	// (PRE→POST). BenchOK gates rendering — false ⇒ omit the bench line (detect/
	// verify, or A4 skipped / produced no comparable sample pair).
	BenchPreMBs, BenchPostMBs, BenchRatio float64
	BenchOK                               bool
}

// Applied is the count of steps that finished StatusOK (actually applied work).
func (s Summary) Applied() int {
	n := 0
	for _, r := range s.Results {
		if r.Status == steps.StatusOK {
			n++
		}
	}
	return n
}

// Total is the number of steps attempted (rows in the per-step table).
func (s Summary) Total() int { return len(s.Results) }

// SkipReason pairs a skipped step's ID with the human reason it returned.
type SkipReason struct {
	ID, Reason string
}

// counts is the per-step tally returned by runStepList so Run can build a Summary.
type counts struct {
	ok, skip, fail int
	skips          []SkipReason // per-skip ID + reason, in apply order
	results        []StepResult // every step's outcome, in apply order
}

// ErrCanceled is returned when a run is halted via the context between steps. It
// is NOT a lockout-capable failure — it means the operator aborted at a safe step
// boundary; whatever steps already completed stay applied (the box is in a valid
// intermediate hardened state, never mid-lockdown).
var ErrCanceled = errors.New("run canceled at step boundary")

// Execute is the single entrypoint used by both the CLI and the TUI: it opens
// the log (optionally streaming lines to h.Sink), then dispatches to the command.
// All hook fields may be nil (only the TUI sets them). ctx carries run-scoped
// cancellation: the CLI passes context.Background(); the TUI passes a cancelable
// context it cancels on abort so an in-flight run stops at the next step boundary.
func Execute(ctx context.Context, cfg *config.Config, cmd string, ids []string, h Hooks) error {
	if ctx == nil {
		ctx = context.Background()
	}
	log := ui.New(cfg.LogFile)
	defer log.Close()
	if h.Sink != nil {
		log.SetSink(h.Sink)
	}
	switch cmd {
	case "", "run":
		return Run(ctx, cfg, log, h)
	case "detect":
		return DetectOnly(ctx, cfg, log, h)
	case "verify":
		return VerifyOnly(ctx, cfg, log, h)
	case "audit":
		return Audit(ctx, cfg, log, h)
	case "step":
		return RunSteps(ctx, cfg, log, ids, h)
	case "revert":
		return RunRevert(ctx, cfg, log, ids, h)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// prepare connects, bootstraps the key, detects the box, and (unless
// allowBrownfield) gates a non-greenfield/hardened box. The returned cleanup
// closes the connection (the caller owns the log).
//
// readOnly is the audit contract: when true, prepare DOES NOT mutate the box —
// it skips the key bootstrap entirely (no ed25519 generation, no push to
// authorized_keys, no UseKey, no password clear) and keeps the operator's
// original credentials (password OR --key) live for the connection and the
// monitor. notifyConnect still fires (KeyGenerated=false) so the monitor footer
// works on the password path. Used by Audit; Run/RunSteps/Verify pass false.
func prepare(ctx context.Context, cfg *config.Config, log *ui.Logger, allowBrownfield, readOnly bool, h Hooks) (*session, func(), error) {
	cleanup := func() {}

	log.Banner(fmt.Sprintf("morgward — %s@%s:%d", cfg.User, cfg.Host, cfg.Port))
	if p := log.Path(); p != "" {
		log.Info("log file: %s", p)
	}

	// 1. Bootstrap connection (key wins; else password).
	var keyPEM []byte
	var err error
	if cfg.KeyPath != "" {
		keyPEM, err = sshx.LoadKeyFile(cfg.KeyPath)
		if err != nil {
			return nil, cleanup, fmt.Errorf("load key: %w", err)
		}
	}
	// Opt-in host-key pin (FA-0010): verify the FIRST handshake against an operator
	// known_hosts file / fingerprint instead of blind TOFU. nil => unchanged TOFU.
	// Config.Validate already vetted the flag shape; ParseHostKeyPin builds the
	// verifier (and re-checks, defensively).
	pin, err := sshx.ParseHostKeyPin(cfg.KnownHostsPath, cfg.HostFingerprint)
	if err != nil {
		return nil, cleanup, fmt.Errorf("host-key pin: %w", err)
	}
	// Resilient initial dial: a freshly-reset box often is not auth-ready for the
	// first ~minute (sshd in initramfs / cloud-init still installing keys or the
	// root password). Retry for up to 90s with 5s backoff, streaming progress.
	cli, err := sshx.DialWithRetry(cfg.Host, cfg.Port, cfg.User, cfg.Password, keyPEM, pin,
		90*time.Second, func(msg string) { log.Info("%s", msg) })
	if err != nil {
		if errors.Is(err, sshx.ErrNoMutualAuth) {
			emitAuthHint(log, cfg, err)
		} else {
			log.Fail("initial SSH connection failed: %v", err)
		}
		return nil, cleanup, err
	}
	cleanup = func() { cli.Close() }
	// Wire live output streaming once, at the client level: from here every
	// ctx.Cli.Run/Sudo in every step tees its server output through the logger
	// (log file + TUI sink) with zero per-step boilerplate.
	cli.SetOutputSink(func(stream, line string) { log.Stream(stream, line) })
	log.OK("connected to %s@%s", cfg.User, cfg.Host)

	// 2. Key bootstrap: generate ed25519, push to the bootstrap user, switch to key.
	// SKIPPED in read-only (audit) mode — Audit must not mutate the box, so it never
	// generates/pushes a key nor clears the password; it keeps the operator's original
	// credentials live. notifyConnect still fires below so the monitor footer works.
	var authLine string
	switch {
	case readOnly:
		// Read-only: change nothing. Derive the public line only if a --key was given
		// (cosmetic — steps that would consume it never run on this path). On the
		// password path keyPEM stays nil and the monitor dials with cfg.Password.
		if keyPEM != nil {
			if authLine, err = sshx.PublicLineFromPEM(keyPEM, "morgward@"+cfg.Host); err != nil {
				return nil, cleanup, fmt.Errorf("derive public key: %w", err)
			}
		}
		notifyConnect(h.OnConnect, cfg, keyPEM, false)
	case keyPEM == nil:
		// The caller may have pre-generated the keypair (TUI shows it before the run);
		// in that case reuse it and skip OnKey (the caller already holds the PEM). When
		// nil (CLI path), generate here exactly as before and fire OnKey.
		if h.PreparedKey != nil {
			log.Step("KEY", "Use caller-prepared ed25519 key and switch to key auth")
			authLine = h.PreparedKey.AuthorizedLine
			keyPEM = h.PreparedKey.PrivatePEM
		} else {
			log.Step("KEY", "Generate ed25519 key and switch to key auth")
			kp, gerr := sshx.GenerateKeyPair("morgward@" + cfg.Host)
			if gerr != nil {
				return nil, cleanup, fmt.Errorf("keygen: %w", gerr)
			}
			authLine = kp.AuthorizedLine
			keyPEM = kp.PrivatePEM
			if h.OnKey != nil {
				h.OnKey(string(kp.PrivatePEM))
			}
		}

		push := "mkdir -p /root/.ssh && chmod 700 /root/.ssh\n" +
			pushAuthLine("/root/.ssh/authorized_keys", authLine) +
			"chmod 600 /root/.ssh/authorized_keys\n"
		if r := cli.Sudo(push); r.RC != 0 {
			return nil, cleanup, fmt.Errorf("push key to root: %s", r.Stderr)
		}
		if err := cli.UseKey(keyPEM); err != nil {
			return nil, cleanup, fmt.Errorf("switch to key auth: %w", err)
		}
		cfg.Password = "" // bootstrap secret no longer needed
		log.OK("ephemeral SSH key generated (held in memory — copy it from the key screen or CLI output)")
		notifyConnect(h.OnConnect, cfg, keyPEM, true)
	default:
		authLine, err = sshx.PublicLineFromPEM(keyPEM, "morgward@"+cfg.Host)
		if err != nil {
			return nil, cleanup, fmt.Errorf("derive public key: %w", err)
		}
		notifyConnect(h.OnConnect, cfg, keyPEM, false)
	}

	// 3. Detection + §0.5 inventory.
	log.Step("DETECT", "§0.5/§2 pre-flight discovery")
	facts := detect.Run(cli)
	log.OK("OS=%s %s (%s), iface=%s, ipv4=%s, virt=%s, greenfield=%v",
		facts.ID, facts.VersionID, facts.Codename, facts.EgressIface, facts.ServerIPv4, facts.Virt, facts.Greenfield)
	// The inventory file is the only write detect performs; skip it in read-only
	// (audit) mode so Audit truly mutates nothing on the box. Non-fatal, but surface
	// a warning instead of failing silently (F19) so the operator isn't told the
	// /root/vps-inventory.md record exists when the write actually failed.
	if !readOnly {
		if r := cli.Sudo(writeInventory(facts.Inventory)); r.RC != 0 {
			log.Warn("could not write /root/vps-inventory.md (rc=%d): %s", r.RC, firstStderrLine(r.Stderr))
		}
	}

	if !facts.IsUbuntu {
		log.Warn("ID=%s is not ubuntu — runbook is Ubuntu-specific; proceeding with version-drift protocol", facts.ID)
	}
	if facts.EgressIface == "" || facts.EgressIface == "lo" {
		return nil, cleanup, fmt.Errorf("egress interface detection failed (got %q) — aborting per §2", facts.EgressIface)
	}

	// Best-effort "before" snapshot for the run summary. A capture error is
	// cosmetic — log it and keep whatever partial snapshot we got; NEVER abort.
	before, serr := stats.Capture(cli)
	if serr != nil {
		log.Warn("before snapshot incomplete: %v", serr)
	}

	// 4a. Already-hardened gate — refuse to re-run Phase A blind on a box this
	// runbook already processed (read-only commands pass through).
	if facts.AlreadyHardened {
		log.Banner("ALREADY HARDENED")
		log.Warn("box carries hardening markers: %v", facts.HardenMarkers)
		if !allowBrownfield && !cfg.Assume {
			log.Info("nothing to do — use `verify` to check state or `step <IDs>` to re-tweak a block (or --assume-yes to force a full re-run)")
			return nil, cleanup, fmt.Errorf("box already hardened: refusing to re-run Phase A (use verify/step)")
		}
		if !allowBrownfield {
			log.Warn("--assume-yes set: forcing a full re-run on an already-hardened box")
		}
	} else if !facts.Greenfield {
		// 4b. Brownfield gate (skipped for read-only commands).
		log.Banner("BROWNFIELD DETECTED")
		log.Warn("box is not empty: forwarding=%v docker=%v listeners=%d firewall=%s", facts.IPForward, facts.DockerSeen, len(facts.Listeners), facts.FirewallMgr)
		for _, l := range facts.Listeners {
			log.Detail("listener: %s", l)
		}
		if !allowBrownfield && !cfg.Assume {
			log.Info("re-run with --assume-yes to apply in COEXISTENCE mode (detected service ports/forwarding/swap are preserved); see /root/vps-inventory.md")
			return nil, cleanup, fmt.Errorf("brownfield box: refusing to run Phase A without confirmation (re-run with --assume-yes for coexistence mode)")
		}
		if !allowBrownfield {
			log.Warn("--assume-yes set: proceeding in coexistence mode (existing services preserved) — see /root/vps-inventory.md")
		}
	}

	chk := state.Load("")
	chk.Host = cfg.Host
	chk.Greenfield = facts.Greenfield
	// Read-only audit does not persist a checkpoint (it applies no steps), so the
	// state file is left untouched.
	if !readOnly {
		chk.Save()
	}

	sctx := &steps.Context{
		Ctx: ctx,
		Cli: cli, Log: log, Cfg: cfg, State: chk, Facts: facts,
		AuthLine: authLine, KeyPEM: keyPEM,
	}
	return &session{log: log, cli: cli, ctx: sctx, before: before}, cleanup, nil
}

// notifyConnect fires the onConnect callback (if set) with the monitor's
// connection info, right after key auth becomes active.
func notifyConnect(onConnect func(monitor.ConnInfo), cfg *config.Config, keyPEM []byte, generated bool) {
	if onConnect == nil {
		return
	}
	onConnect(monitor.ConnInfo{
		Host:      cfg.Host,
		Port:      cfg.Port,
		User:      cfg.User,
		AdminUser: cfg.AdminUser,
		KeyPEM:    keyPEM,
		// Password is consumed by the monitor only when KeyPEM is empty (the
		// read-only/password audit path); cleared after key bootstrap on the
		// other paths, so it is "" there.
		Password:     cfg.Password,
		KeyGenerated: generated,
	})
}

// Run executes a full hardening pass (all Phase A steps + §V verification).
func Run(ctx context.Context, cfg *config.Config, log *ui.Logger, h Hooks) error {
	start := time.Now()
	s, cleanup, err := prepare(ctx, cfg, log, false, false, h)
	defer cleanup()
	if err != nil {
		return err
	}
	cnt, err := runStepList(ctx, s, orderedSteps(), true, h)
	if err != nil {
		return err
	}

	// Best-effort "after" snapshot — cosmetic, never run-fatal.
	after, serr := stats.Capture(s.cli)
	if serr != nil {
		s.log.Warn("after snapshot incomplete: %v", serr)
	}

	res := verify.Run(s.cli, s.log, cfg.Port, s.ctx.Facts)
	s.log.Banner("SUMMARY")
	s.log.Info("verify: %d passed, %d failed%s", res.Passed, res.Failed, unmeasuredSuffix(res.Unknown))
	sum := Summary{
		OK: cnt.ok, Skip: cnt.skip, Fail: cnt.fail,
		VerifyPassed: res.Passed, VerifyFailed: res.Failed,
		Elapsed: time.Since(start),
		Results: cnt.results,
		Before:  s.before, After: after,
		Skips: cnt.skips,
	}
	applyResultMarkers(&sum)
	applySnapshotBench(&sum, s.ctx.Bench)
	applyBench(&sum, s.ctx.Bench)
	logBenchAndSkips(s.log, sum)
	emitDone(h, sum)
	if res.Abort {
		s.log.Fail("a lockout-capable verification failed — review before trusting the box")
		return fmt.Errorf("verification matrix reported a lockout-capable failure")
	}
	if p := s.log.Path(); p != "" {
		s.log.OK("hardening run complete — log: %s", p)
	} else {
		s.log.OK("hardening run complete")
	}
	return nil
}

// RunSteps executes only the named step IDs (e.g. "A4", "A5"), respecting their
// dependencies' on-box state but NOT the load-bearing full order — use for
// targeted re-tweaks on an already-bootstrapped box (pass --key to reuse a key).
func RunSteps(ctx context.Context, cfg *config.Config, log *ui.Logger, ids []string, h Hooks) error {
	start := time.Now()
	s, cleanup, err := prepare(ctx, cfg, log, true, false, h)
	defer cleanup()
	if err != nil {
		return err
	}
	selected, unknown := selectSteps(ids)
	if len(unknown) > 0 {
		return fmt.Errorf("unknown step id(s): %v (valid: %v)", unknown, allStepIDs())
	}
	if len(selected) == 0 {
		return fmt.Errorf("no steps selected; valid ids: %v", allStepIDs())
	}
	s.log.Info("running selected steps: %v", ids)
	cnt, err := runStepList(ctx, s, selected, false, h)
	if err != nil {
		return err
	}
	sum := Summary{OK: cnt.ok, Skip: cnt.skip, Fail: cnt.fail, Elapsed: time.Since(start), Skips: cnt.skips, Results: cnt.results}
	applyResultMarkers(&sum)
	applyBench(&sum, s.ctx.Bench)
	logBenchAndSkips(s.log, sum)
	emitDone(h, sum)
	return nil
}

// unmeasuredSuffix renders the F21 "could not check" rows for the verify summary
// line: empty when none, " (N unmeasured)" otherwise. Unknown rows are separate
// from passed/failed (they never inflate either count and don't affect res.Abort),
// so appending this is backward-compatible.
func unmeasuredSuffix(unknown int) string {
	if unknown <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%d unmeasured)", unknown)
}

// VerifyOnly runs the §V verification matrix without mutating the box.
func VerifyOnly(ctx context.Context, cfg *config.Config, log *ui.Logger, h Hooks) error {
	start := time.Now()
	s, cleanup, err := prepare(ctx, cfg, log, true, false, h)
	defer cleanup()
	if err != nil {
		return err
	}
	res := verify.Run(s.cli, s.log, cfg.Port, s.ctx.Facts)
	s.log.Banner("SUMMARY")
	s.log.Info("verify: %d passed, %d failed%s", res.Passed, res.Failed, unmeasuredSuffix(res.Unknown))
	tw := tweaks.Run(s.cli, s.log, s.ctx.Facts, cfg)
	emitDone(h, Summary{
		VerifyPassed: res.Passed, VerifyFailed: res.Failed,
		Elapsed: time.Since(start),
		Tweaks:  tw,
	})
	if res.Abort {
		return fmt.Errorf("verification matrix reported a lockout-capable failure")
	}
	return nil
}

// DetectOnly connects and runs read-only discovery, writing the inventory but
// changing nothing.
func DetectOnly(ctx context.Context, cfg *config.Config, log *ui.Logger, h Hooks) error {
	start := time.Now()
	_, cleanup, err := prepare(ctx, cfg, log, true, false, h)
	defer cleanup()
	if err != nil {
		return err
	}
	emitDone(h, Summary{Elapsed: time.Since(start)})
	return nil
}

// Audit is the read-only Dashboard entrypoint: it dials with the operator's
// original credentials (password OR --key), runs §0.5/§2 detection and the
// per-tweak audit, and changes NOTHING on the box — no key bootstrap, no admin
// creation, no step application (prepare is called with readOnly=true). The
// tweak results stream to the TUI via OnProgress (one per Result; cosmetic
// streaming of a single tweaks.Run batch, not real concurrency), and the final
// Done carries Summary{Facts, Tweaks} so the Dashboard can render the server
// card + live audit grid.
func Audit(ctx context.Context, cfg *config.Config, log *ui.Logger, h Hooks) error {
	start := time.Now()
	s, cleanup, err := prepare(ctx, cfg, log, true, true, h)
	defer cleanup()
	if err != nil {
		return err
	}
	tw := tweaks.Run(s.cli, s.log, s.ctx.Facts, cfg)
	// Stream one Progress per tweak Result so the Dashboard audit list fills in
	// incrementally (this is cosmetic — tweaks.Run already completed in a single
	// privileged round-trip; we are replaying the parsed batch, not probing in
	// parallel).
	for i, res := range tw {
		if h.OnProgress != nil {
			h.OnProgress(Progress{
				ID: res.Probe.ID, Title: res.Probe.Name,
				Index: i + 1, Total: len(tw), Status: "running",
			})
		}
	}
	emitDone(h, Summary{Elapsed: time.Since(start), Tweaks: tw, Facts: s.ctx.Facts})
	return nil
}

// applyResultMarkers derives the change tally from step result details: the
// UPGRADED_COUNT marker A8 emits, the PURGED_COUNT marker A7 emits, and a reboot
// count (1 when A8 finished OK — A8 reboots). Markers are embedded in the detail
// string via the same key=value convention extractMarker reads on the box.
func applyResultMarkers(sum *Summary) {
	for _, r := range sum.Results {
		switch r.ID {
		case "A8":
			if n, ok := markerInt(r.Detail, "UPGRADED_COUNT="); ok {
				sum.UpgradedPkgs = n
			}
			if r.Status == steps.StatusOK {
				sum.Reboots++
			}
		case "A7":
			if n, ok := markerInt(r.Detail, "PURGED_COUNT="); ok {
				sum.PurgedPkgs = n
			}
		}
	}
}

// markerInt scans detail for "<marker><int>" (whitespace-delimited) and returns
// the parsed int. ok is false when the marker is absent or unparseable.
func markerInt(detail, marker string) (int, bool) {
	_, rest, found := strings.Cut(detail, marker)
	if !found {
		return 0, false
	}
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return n, true
}

// applySnapshotBench folds the §A4 internet benchmark into the before/after
// snapshots' SpeedMBs (which the snapshot script cannot measure itself) so the
// summary's throughput delta lines up with the rest of the snapshot.
func applySnapshotBench(sum *Summary, b *steps.BenchResult) {
	if b == nil || !b.OK {
		return
	}
	if sum.Before != nil {
		sum.Before.SpeedMBs = b.PreMBs
	}
	if sum.After != nil {
		sum.After.SpeedMBs = b.PostMBs
	}
}

// applyBench copies a step BenchResult (if present and valid) into the Summary's
// flat Bench* fields. A nil or not-OK bench leaves BenchOK false so renderers skip
// the line.
func applyBench(sum *Summary, b *steps.BenchResult) {
	if b == nil || !b.OK {
		return
	}
	sum.BenchPreMBs = b.PreMBs
	sum.BenchPostMBs = b.PostMBs
	sum.BenchRatio = b.Ratio
	sum.BenchOK = true
}

// logBenchAndSkips writes the skip reasons and the internet benchmark to the
// CLI/log under the SUMMARY banner (the TUI renders the same data from the Summary
// it receives via emitDone). Both blocks are omitted when there is nothing to show.
func logBenchAndSkips(log *ui.Logger, sum Summary) {
	for _, sk := range sum.Skips {
		log.Detail("skipped %s — %s", sk.ID, sk.Reason)
	}
	if sum.BenchOK {
		log.Info("internet: %.1f → %.1f MB/s (%.2fx)", sum.BenchPreMBs, sum.BenchPostMBs, sum.BenchRatio)
	}
	// Before/after stats block (CLI text path). statsLines returns nil when both
	// snapshots are absent (detect/verify/step), so this is a no-op there. The TUI
	// renders the same data from its own summary screen; these lines land only in
	// the scrolling log pane, which is acceptable (separate surfaces).
	for _, line := range sum.statsLines() {
		log.Info("%s", line)
	}
}

// emitDone fires the one final Done progress event (if a progress hook is set).
func emitDone(h Hooks, sum Summary) {
	if h.OnProgress == nil {
		return
	}
	h.OnProgress(Progress{Done: true, Summary: sum})
}

// runStepList runs steps in order; honorCheckpoint skips already-completed steps.
// It emits a per-step Progress (running, then final status) when h.OnProgress is
// set, and returns the OK/SKIP/FAIL tally so the caller can build a Summary.
func runStepList(ctx context.Context, s *session, list []steps.Step, honorCheckpoint bool, h Hooks) (counts, error) {
	chk := s.ctx.State
	var c counts
	total := len(list)
	emit := func(st steps.Step, i int, status string) {
		if h.OnProgress == nil {
			return
		}
		h.OnProgress(Progress{
			ID: st.ID(), Title: st.Title(),
			Index: i + 1, Total: total, Status: status,
		})
	}
	for i, st := range list {
		// Cancellation is checked ONLY here, at the boundary BEFORE a step starts —
		// never mid-step. This guarantees the load-bearing SSH-lockdown / firewall /
		// sysctl sequence inside any step always runs to completion once begun; an
		// abort halts the run at the next safe boundary, leaving a valid intermediate
		// state rather than a half-applied lockdown.
		if err := ctx.Err(); err != nil {
			s.log.Warn("run canceled before %s — stopping at step boundary (%d/%d done)", st.ID(), i, total)
			return c, ErrCanceled
		}
		if honorCheckpoint && chk.Done(st.ID()) {
			s.log.Skip("%s — already completed (checkpoint)", st.ID())
			c.skip++
			c.skips = append(c.skips, SkipReason{ID: st.ID(), Reason: "already completed (checkpoint)"})
			c.results = append(c.results, StepResult{
				ID: st.ID(), Title: st.Title(), Status: steps.StatusSkip,
				Detail: "already completed (checkpoint)",
			})
			// Checkpoint-skipped steps still emit with their final status.
			emit(st, i, steps.StatusSkip.String())
			continue
		}
		emit(st, i, "running")
		s.log.Step(st.ID(), st.Title())
		start := time.Now()
		status, detail, herr := st.Run(s.ctx)
		dur := time.Since(start).Round(time.Second)
		c.results = append(c.results, StepResult{
			ID: st.ID(), Title: st.Title(), Status: status, Detail: detail,
		})
		switch status {
		case steps.StatusOK:
			c.ok++
			s.log.OK("%s (%s) — %s", st.ID(), dur, detail)
		case steps.StatusSkip:
			c.skip++
			c.skips = append(c.skips, SkipReason{ID: st.ID(), Reason: detail})
			s.log.Skip("%s — %s", st.ID(), detail)
		case steps.StatusFail:
			c.fail++
			s.log.Fail("%s — %s", st.ID(), detail)
		}
		chk.Mark(st.ID(), status.String())
		emit(st, i, status.String())
		if herr != nil {
			s.log.Fail("ABORT: lockout-capable failure in %s: %v", st.ID(), herr)
			return c, herr
		}
	}
	s.log.Info("steps: %d OK, %d SKIP, %d FAIL", c.ok, c.skip, c.fail)
	return c, nil
}

// selectSteps returns the ordered steps matching ids (case-insensitive), plus
// any unrecognized ids. Selection preserves the canonical apply order.
func selectSteps(ids []string) (selected []steps.Step, unknown []string) {
	want := map[string]bool{}
	for _, id := range ids {
		want[strings.ToUpper(id)] = true
	}
	seen := map[string]bool{}
	for _, st := range resolvableSteps() {
		if want[strings.ToUpper(st.ID())] && !seen[strings.ToUpper(st.ID())] {
			selected = append(selected, st)
			seen[strings.ToUpper(st.ID())] = true
		}
	}
	for id := range want {
		if !seen[id] {
			unknown = append(unknown, id)
		}
	}
	return selected, unknown
}

func allStepIDs() []string {
	var ids []string
	for _, st := range resolvableSteps() {
		ids = append(ids, st.ID())
	}
	return ids
}

func pushAuthLine(file, line string) string {
	// Mirror of steps.appendLineIfMissing for the engine's pre-step bootstrap.
	return fmt.Sprintf(
		"__L=%q; grep -qxF \"$__L\" '%s' 2>/dev/null || printf '%%s\\n' \"$__L\" >> '%s'\n",
		line, file, file)
}

func writeInventory(inv string) string {
	// base64 delivery — a heredoc would contend for the script's stdin (§A1 caveat).
	b64 := base64.StdEncoding.EncodeToString([]byte(inv))
	return fmt.Sprintf("echo '%s' | base64 -d > /root/vps-inventory.md\n", b64)
}

// engineLang normalizes cfg.Lang into "ru" | "en", defaulting to "ru" (the CLI
// leaves Lang empty; the TUI sets it from its active language). It is the single
// language source for engine-streamed user-facing text, so the CLI and TUI share
// one accessor and the CLI never crashes for lack of a TUI Lang.
func engineLang(cfg *config.Config) string {
	if cfg != nil && cfg.Lang == "en" {
		return "en"
	}
	return "ru"
}

// emitAuthHint prints the localized, actionable hint shown when the server
// accepted none of the offered auth methods. Localized ru/en via engineLang so
// both the CLI (ru default) and the TUI (its active language) get a correct
// message; the admin user is substituted from the real config.
func emitAuthHint(log *ui.Logger, cfg *config.Config, err error) {
	admin := cfg.AdminUser
	if admin == "" {
		admin = "vpsadmin"
	}
	if engineLang(cfg) == "en" {
		log.Fail("could not authenticate to %s: the server accepted none of the offered auth methods (no password and no working SSH key).", cfg.Host)
		log.Detail("If this box is ALREADY hardened, root SSH is blocked by `AllowGroups sshusers` —")
		log.Detail("  reconnect as the admin user %q with the key shown by morgward (pass it via --key, or save the PEM from the key screen / CLI output).", admin)
		log.Detail("Otherwise check host/port/user/key, or enable password login / set a root password in your provider panel.")
		log.Detail("raw error: %v", err)
		return
	}
	log.Fail("не удалось аутентифицироваться на %s: сервер не принял ни один из предложенных методов (нет пароля и нет рабочего SSH-ключа).", cfg.Host)
	log.Detail("Если машина УЖЕ защищена, вход root по SSH заблокирован `AllowGroups sshusers` —")
	log.Detail("  переподключитесь админ-пользователем %q с ключом, который показал morgward (передайте его через --key или сохраните PEM с экрана ключа / из вывода CLI).", admin)
	log.Detail("Иначе проверьте хост/порт/пользователя/ключ либо включите вход по паролю / задайте пароль root в панели провайдера.")
	log.Detail("исходная ошибка: %v", err)
}
