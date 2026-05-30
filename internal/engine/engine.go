// Package engine wires config -> SSH bootstrap -> detection -> ordered steps ->
// verification. It owns the password->key bootstrap, the brownfield gate, and
// the load-bearing apply order from the runbook.
package engine

import (
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/monitor"
	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/state"
	"github.com/UberMorgott/morgward/internal/steps"
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

// session is the shared connected state produced by prepare().
type session struct {
	log *ui.Logger
	cli *sshx.Client
	ctx *steps.Context
}

// Hooks bundles the optional callbacks the TUI uses to observe a run. Any field
// may be nil (the CLI passes a zero Hooks ⇒ unchanged behavior).
type Hooks struct {
	Sink       func(string)           // streams each log line to the caller
	OnConnect  func(monitor.ConnInfo) // fires once after key auth is active
	OnProgress func(Progress)         // fires per step and once at the end
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

// Summary is the aggregate run outcome carried by the final Done progress event.
type Summary struct {
	OK, Skip, Fail             int
	VerifyPassed, VerifyFailed int
	Elapsed                    time.Duration

	// Skips carries the per-skip reasons (the detail string each SKIPPED step
	// returned), so the CLI/TUI can show WHY a step was skipped, not just a count.
	Skips []SkipReason

	// Bench* mirror steps.BenchResult: the §A4 internet throughput benchmark
	// (PRE→POST). BenchOK gates rendering — false ⇒ omit the bench line (detect/
	// verify, or A4 skipped / produced no comparable sample pair).
	BenchPreMBs, BenchPostMBs, BenchRatio float64
	BenchOK                               bool
}

// SkipReason pairs a skipped step's ID with the human reason it returned.
type SkipReason struct {
	ID, Reason string
}

// counts is the per-step tally returned by runStepList so Run can build a Summary.
type counts struct {
	ok, skip, fail int
	skips          []SkipReason // per-skip ID + reason, in apply order
}

// Execute is the single entrypoint used by both the CLI and the TUI: it opens
// the log (optionally streaming lines to h.Sink), then dispatches to the command.
// All hook fields may be nil (only the TUI sets them).
func Execute(cfg *config.Config, cmd string, ids []string, h Hooks) error {
	log, err := ui.New(cfg.LogDir)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer log.Close()
	if h.Sink != nil {
		log.SetSink(h.Sink)
	}
	switch cmd {
	case "", "run":
		return Run(cfg, log, h)
	case "detect":
		return DetectOnly(cfg, log, h)
	case "verify":
		return VerifyOnly(cfg, log, h)
	case "step":
		return RunSteps(cfg, log, ids, h)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// prepare connects, bootstraps the key, detects the box, and (unless
// allowBrownfield) gates a non-greenfield/hardened box. The returned cleanup
// closes the connection (the caller owns the log).
func prepare(cfg *config.Config, log *ui.Logger, allowBrownfield bool, h Hooks) (*session, func(), error) {
	cleanup := func() {}

	log.Banner(fmt.Sprintf("morgward — %s@%s:%d  mode=%s", cfg.User, cfg.Host, cfg.Port, cfg.Mode))
	log.Info("log file: %s", log.Path())

	// 1. Bootstrap connection (key wins; else password).
	var keyPEM []byte
	var err error
	if cfg.KeyPath != "" {
		keyPEM, err = sshx.LoadKeyFile(cfg.KeyPath)
		if err != nil {
			return nil, cleanup, fmt.Errorf("load key: %w", err)
		}
	}
	// Resilient initial dial: a freshly-reset box often is not auth-ready for the
	// first ~minute (sshd in initramfs / cloud-init still installing keys or the
	// root password). Retry for up to 90s with 5s backoff, streaming progress.
	cli, err := sshx.DialWithRetry(cfg.Host, cfg.Port, cfg.User, cfg.Password, keyPEM,
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
	var authLine string
	if keyPEM == nil {
		log.Step("KEY", "Generate ed25519 key and switch to key auth")
		kpPath := filepath.Join(dirOf(cfg.LogDir), fmt.Sprintf("id_ed25519_%s", sanitize(cfg.Host)))
		kp, gerr := sshx.GenerateKeyPair(kpPath, "morgward@"+cfg.Host)
		if gerr != nil {
			return nil, cleanup, fmt.Errorf("keygen: %w", gerr)
		}
		authLine = kp.AuthorizedLine
		keyPEM = kp.PrivatePEM

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
		log.OK("key generated (%s) and key auth active", kp.PrivatePath)
		notifyConnect(h.OnConnect, cfg, keyPEM)
	} else {
		authLine, err = sshx.PublicLineFromPEM(keyPEM, "morgward@"+cfg.Host)
		if err != nil {
			return nil, cleanup, fmt.Errorf("derive public key: %w", err)
		}
		notifyConnect(h.OnConnect, cfg, keyPEM)
	}

	// 3. Detection + §0.5 inventory.
	log.Step("DETECT", "§0.5/§2 pre-flight discovery")
	facts := detect.Run(cli)
	log.OK("OS=%s %s (%s), iface=%s, ipv4=%s, virt=%s, greenfield=%v",
		facts.ID, facts.VersionID, facts.Codename, facts.EgressIface, facts.ServerIPv4, facts.Virt, facts.Greenfield)
	cli.Sudo(writeInventory(facts.Inventory))

	if !facts.IsUbuntu {
		log.Warn("ID=%s is not ubuntu — runbook is Ubuntu-specific; proceeding with version-drift protocol", facts.ID)
	}
	if facts.EgressIface == "" || facts.EgressIface == "lo" {
		return nil, cleanup, fmt.Errorf("egress interface detection failed (got %q) — aborting per §2", facts.EgressIface)
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
		log.Warn("box is not empty: forwarding=%v docker=%v listeners=%d", facts.IPForward, facts.DockerSeen, len(facts.Listeners))
		for _, l := range facts.Listeners {
			log.Detail("listener: %s", l)
		}
		if !allowBrownfield && !cfg.Assume {
			log.Info("re-run with --assume-yes to proceed with the universal baseline anyway (NOT recommended without adaptation)")
			return nil, cleanup, fmt.Errorf("brownfield box: refusing to run universal Phase A blind (see §0.5)")
		}
		if !allowBrownfield {
			log.Warn("--assume-yes set: proceeding on a brownfield box at operator's risk")
		}
	}

	chk := state.Load(filepath.Join(dirOf(cfg.LogDir), "morgward-"+sanitize(cfg.Host)+".state.json"))
	chk.Host = cfg.Host
	chk.Mode = string(cfg.Mode)
	chk.KeyPath = filepath.Join(dirOf(cfg.LogDir), fmt.Sprintf("id_ed25519_%s", sanitize(cfg.Host)))
	chk.Greenfield = facts.Greenfield
	chk.Save()

	ctx := &steps.Context{
		Cli: cli, Log: log, Cfg: cfg, State: chk, Facts: facts,
		AuthLine: authLine, KeyPEM: keyPEM,
	}
	return &session{log: log, cli: cli, ctx: ctx}, cleanup, nil
}

// notifyConnect fires the onConnect callback (if set) with the monitor's
// connection info, right after key auth becomes active.
func notifyConnect(onConnect func(monitor.ConnInfo), cfg *config.Config, keyPEM []byte) {
	if onConnect == nil {
		return
	}
	onConnect(monitor.ConnInfo{
		Host:      cfg.Host,
		Port:      cfg.Port,
		User:      cfg.User,
		AdminUser: cfg.AdminUser,
		KeyPEM:    keyPEM,
	})
}

// Run executes a full hardening pass (all Phase A steps + §V verification).
func Run(cfg *config.Config, log *ui.Logger, h Hooks) error {
	start := time.Now()
	s, cleanup, err := prepare(cfg, log, false, h)
	defer cleanup()
	if err != nil {
		return err
	}
	cnt, err := runStepList(s, orderedSteps(), true, h)
	if err != nil {
		return err
	}
	res := verify.Run(s.cli, s.log, cfg.Port, string(cfg.Mode))
	s.log.Banner("SUMMARY")
	s.log.Info("verify: %d passed, %d failed", res.Passed, res.Failed)
	sum := Summary{
		OK: cnt.ok, Skip: cnt.skip, Fail: cnt.fail,
		VerifyPassed: res.Passed, VerifyFailed: res.Failed,
		Elapsed: time.Since(start),
		Skips:   cnt.skips,
	}
	applyBench(&sum, s.ctx.Bench)
	logBenchAndSkips(s.log, sum)
	emitDone(h, sum)
	if res.Abort {
		s.log.Fail("a lockout-capable verification failed — review before trusting the box")
		return fmt.Errorf("verification matrix reported a lockout-capable failure")
	}
	s.log.OK("hardening run complete — log: %s", s.log.Path())
	return nil
}

// RunSteps executes only the named step IDs (e.g. "A4", "A5"), respecting their
// dependencies' on-box state but NOT the load-bearing full order — use for
// targeted re-tweaks on an already-bootstrapped box (pass --key to reuse a key).
func RunSteps(cfg *config.Config, log *ui.Logger, ids []string, h Hooks) error {
	start := time.Now()
	s, cleanup, err := prepare(cfg, log, true, h)
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
	cnt, err := runStepList(s, selected, false, h)
	if err != nil {
		return err
	}
	sum := Summary{OK: cnt.ok, Skip: cnt.skip, Fail: cnt.fail, Elapsed: time.Since(start), Skips: cnt.skips}
	applyBench(&sum, s.ctx.Bench)
	logBenchAndSkips(s.log, sum)
	emitDone(h, sum)
	return nil
}

// VerifyOnly runs the §V verification matrix without mutating the box.
func VerifyOnly(cfg *config.Config, log *ui.Logger, h Hooks) error {
	start := time.Now()
	s, cleanup, err := prepare(cfg, log, true, h)
	defer cleanup()
	if err != nil {
		return err
	}
	res := verify.Run(s.cli, s.log, cfg.Port, string(cfg.Mode))
	s.log.Banner("SUMMARY")
	s.log.Info("verify: %d passed, %d failed", res.Passed, res.Failed)
	emitDone(h, Summary{
		VerifyPassed: res.Passed, VerifyFailed: res.Failed,
		Elapsed: time.Since(start),
	})
	if res.Abort {
		return fmt.Errorf("verification matrix reported a lockout-capable failure")
	}
	return nil
}

// DetectOnly connects and runs read-only discovery, writing the inventory but
// changing nothing.
func DetectOnly(cfg *config.Config, log *ui.Logger, h Hooks) error {
	start := time.Now()
	_, cleanup, err := prepare(cfg, log, true, h)
	defer cleanup()
	if err != nil {
		return err
	}
	emitDone(h, Summary{Elapsed: time.Since(start)})
	return nil
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
func runStepList(s *session, list []steps.Step, honorCheckpoint bool, h Hooks) (counts, error) {
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
		if honorCheckpoint && chk.Done(st.ID()) {
			s.log.Skip("%s — already completed (checkpoint)", st.ID())
			c.skip++
			c.skips = append(c.skips, SkipReason{ID: st.ID(), Reason: "already completed (checkpoint)"})
			// Checkpoint-skipped steps still emit with their final status.
			emit(st, i, steps.StatusSkip.String())
			continue
		}
		emit(st, i, "running")
		s.log.Step(st.ID(), st.Title())
		start := time.Now()
		status, detail, herr := st.Run(s.ctx)
		dur := time.Since(start).Round(time.Second)
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
	for _, st := range orderedSteps() {
		if want[strings.ToUpper(st.ID())] {
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
	for _, st := range orderedSteps() {
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

func dirOf(d string) string {
	if d == "" {
		return "."
	}
	return d
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

// keyPathFor reproduces the SAME generated key path the bootstrap uses
// (id_ed25519_<sanitized host> under the log dir) so user-facing hints point at
// the real file, not a guessed name.
func keyPathFor(cfg *config.Config) string {
	return filepath.Join(dirOf(cfg.LogDir), fmt.Sprintf("id_ed25519_%s", sanitize(cfg.Host)))
}

// emitAuthHint prints the localized, actionable hint shown when the server
// accepted none of the offered auth methods. Localized ru/en via engineLang so
// both the CLI (ru default) and the TUI (its active language) get a correct
// message; the key path/admin user are substituted from the real config.
func emitAuthHint(log *ui.Logger, cfg *config.Config, err error) {
	admin := cfg.AdminUser
	if admin == "" {
		admin = "vpsadmin"
	}
	keyPath := keyPathFor(cfg)
	if engineLang(cfg) == "en" {
		log.Fail("could not authenticate to %s: the server accepted none of the offered auth methods (no password and no working SSH key).", cfg.Host)
		log.Detail("If this box is ALREADY hardened, root SSH is blocked by `AllowGroups sshusers` —")
		log.Detail("  connect as the admin user with its generated key, e.g.:  ssh -i %s %s@%s", keyPath, admin, cfg.Host)
		log.Detail("Otherwise check host/port/user/key, or enable password login / set a root password in your provider panel.")
		log.Detail("raw error: %v", err)
		return
	}
	log.Fail("не удалось аутентифицироваться на %s: сервер не принял ни один из предложенных методов (нет пароля и нет рабочего SSH-ключа).", cfg.Host)
	log.Detail("Если машина УЖЕ защищена, вход root по SSH заблокирован `AllowGroups sshusers` —")
	log.Detail("  подключайтесь админ-пользователем с его сгенерированным ключом, например:  ssh -i %s %s@%s", keyPath, admin, cfg.Host)
	log.Detail("Иначе проверьте хост/порт/пользователя/ключ либо включите вход по паролю / задайте пароль root в панели провайдера.")
	log.Detail("исходная ошибка: %v", err)
}

func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}
