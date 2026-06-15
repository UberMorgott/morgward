# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`morgward` — a portable single-binary Go executor for the **VPS-PREP-RUNBOOK**
(spec: https://github.com/UberMorgott/vps-prep-runbook, file `VPS-PREP-RUNBOOK.md`).
It connects to an Ubuntu 24.04/26.04 VPS (fresh OR already running services) over an
**embedded** SSH client (`golang.org/x/crypto/ssh` — no external `ssh`/`sshpass`/
Docker/web UI) and applies the runbook's hardening + tuning sequence, coexisting with
detected services on a brownfield box (see Brownfield coexistence below).

The spec is cached locally as `RUNBOOK.md` (gitignored, ~98 KB). Read it before
changing any step's behavior — step files are direct translations of runbook blocks
and the apply order is load-bearing.

## Commands

```sh
go build -o morgward ./cmd/morgward   # or: make build
go vet ./...                                  # make vet
gofmt -w .                                    # make fmt
go test ./...                                 # only internal/monitor has tests
go test ./internal/monitor -run TestName -v   # single test
make release                                  # cross-compile 5 targets into dist/
./build.ps1                                    # same, PowerShell (Windows dev host)
```

Targets built/verified: linux+darwin × amd64+arm64, windows/amd64. Go `1.26.2`.

## Architecture

> **Full architecture map + package/step index: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)**
> — read it first to navigate the code (execution flow, entry points, package map,
> step catalog A1–A10, safety model, known-minor).

**Single entrypoint for CLI *and* TUI:** `engine.Execute(ctx, cfg, cmd, ids, Hooks)`
(`internal/engine/engine.go`) dispatches `run | detect | verify | step | audit | revert`.
The CLI passes `context.Background()` + a zero `Hooks{}`; the TUI passes a **cancelable**
ctx + `Sink`/`OnConnect`/`OnProgress` to stream the run. `prepare()` is the shared front
half (dial → key bootstrap → detect → gates → `steps.Context`); steps live one-per-block in
`internal/steps/` and apply in `orderedSteps()` order. A non-nil step error is
**lockout-capable** and aborts the run. See `docs/ARCHITECTURE.md` for the rest. Touch one
path → check the other.

## Critical gotchas

- **Brownfield coexistence — steps adapt, don't refuse.** On a non-fresh box
  (`!ctx.Facts.Greenfield`, run gated behind `--assume-yes`) the high-risk steps
  PRESERVE running services instead of applying universal defaults blind. Driven by
  `detect.Facts` (no new flag). Don't regress these: **A1** opens every detected
  `ListenPortsTCP`/`ListenPortsUDP` and **leaves the FORWARD policy/chains UNTOUCHED**
  on a brownfield box (docker re-asserts `-P FORWARD DROP` + its rules on boot; forcing
  ACCEPT would be overridden or loosen a router's isolation) — never flush existing
  chains/nat (docker DOCKER/DOCKER-USER + nat must survive). `Facts.Forwarding`
  (= `IPForward||DockerSeen||WireguardSeen||NatRules`) drives **A5** only: emit
  `rp_filter=2` (loose) when forwarding (strict `1` severs asymmetric WG/OpenVPN
  routes). **A6.7** keeps pre-existing disk swap (gate the `swapoff`/fstab-comment
  block behind `Greenfield` only); **A2** adds non-root `Facts.SSHKeyUsers` to
  `sshusers` (grant-only `usermod -aG`, `|| true`, root excluded) before `AllowGroups
  sshusers` so it can't lock out existing key users. verify/tweaks accept rp_filter=2
  when `Forwarding` and never assert a FORWARD policy on brownfield. Greenfield path
  stays byte-identical. Full map: `docs/BROWNFIELD.md`.

- **Brownfield round 2 — firewall-manager-aware + role-agnostic.** A1 branches on
  `Facts.FirewallMgr` (`ufw`/`firewalld`/`nftables`/`iptables`/`none`) so it NEVER
  imposes a conflicting second firewall layer: **`ufw`** active → `ufw allow` SSH +
  every detected port (allow-only — never `deny`/`enable`/`disable`/default-policy
  change); **`firewalld`/`nftables`** → DEFER, change nothing, return **`StatusSkip`**
  (NOT an error — a full `run` continues; firewall is the operator's under their
  manager); **`iptables`/`none`** → the proven round-1 `coexistRuleset` (INPUT DROP +
  detected ports, FORWARD untouched). Keep classification CONSERVATIVE — if unsure
  whether a box is native-nftables, fall through to `iptables` (a false "nftables" would
  wrongly make us defer on a docker box). Detection stays **role-agnostic**: NO service
  whitelist — observe & preserve every listening port by NUMBER (works for k8s, NAS,
  routers, game servers, anything). `Facts.ListenServices` (`[]ListenService{Proto,Port,
  Process}`) is surfacing only (inventory port→process table); the ruleset still keys off
  `ListenPortsTCP/UDP`. iptables coexist opens ports LISTENING at apply time only — later
  ephemeral ports (k8s NodePort, torrents) need a re-run of `step A1` or an explicit rule.

- **Brownfield round 3 — `ManagesIPTables()` propagation.** The round-2 A1 branch now
  propagates to every step/path that ASSUMES a morgward-owned iptables ruleset, via the
  single predicate `detect.Facts.ManagesIPTables()` (`internal/detect/detect.go`):
  `true` on Greenfield OR `FirewallMgr` ∈ `{iptables, none, ""}` (CONSERVATIVE — unknown
  ⇒ managed); `false` only on explicit `ufw`/`firewalld`/`nftables`. Gate iptables-specific
  assertions behind it, don't hard-code them. **A8** pre/post-reboot firewall gate:
  `rules.v4`/`-P INPUT DROP` checks only when `ManagesIPTables`; on `ufw` substitute
  `ufw status` *active* as the SSH-survives-reboot gate; on `firewalld`/`nftables` skip
  the gate (operator owns persistence) — no more full-run ABORT on a ufw brownfield box.
  **`verify.Run` now takes `*detect.Facts`** (engine passes `s.ctx.Facts`; nil ⇒ treated
  as managed, byte-identical): `firewallChecks` rows are manager-aware — managed →
  original iptables `Firewall order`/`SSH port open` (Lockout); `ufw` → `Firewall (ufw)
  active` + `SSH port allowed (ufw)`; `firewalld`/`nftables` → two NA skip-with-reason
  rows, **never Abort**. **revert A1** is facts-aware: on ufw/firewalld/nftables a
  deliberate no-op `Skip` ("reverts only open access"); managed → the original flush.
  **A10** inbound-drop `LOG` rule + `netfilter-persistent save` gated on `ManagesIPTables`
  (else `Detail`: "inbound-drop LOG rule skipped: <mgr> manages the firewall (no second
  layer)"). **tweaks** audit probes `a1.{input_drop,ssh_accept,rules_v4,rules_v6}` +
  `a10.log_rule` gate behind the same `managesIPT` so a manager-owned box shows no false
  "not applied". Greenfield/iptables output stays byte-identical (probes still lead the
  registry in the same order). Verified-live: greenfield + ufw-brownfield full runs on
  Ubuntu 26.04 passed.

- **apt-get lock contention (v0.7.3).** All 13 lock-acquiring `apt-get` invocations
  across `internal/steps` (PRE, A1, A3, A6.5, A6.7, A7×3, A8×2, A9, A10) carry
  `-o DPkg::Lock::Timeout=300`, so unattended-upgrades holding the dpkg lock on a
  fresh-boot box waits up to 5 min instead of aborting the step. Add the flag to any NEW
  lock-acquiring apt-get call.

- **§A1 stdin caveat — NEVER use heredocs in remote scripts.** The script itself is
  piped to `bash` over stdin, so a heredoc would contend for that stdin. Deliver all
  file content via nested base64: use `putFile` / `appendLineIfMissing` / `anchorSysctl`
  in `steps/step.go`, never `cat <<EOF`. To run a multi-line script body without landing
  a file, use `pipeToBash(script)` (`steps/step.go`: `echo <b64> | base64 -d | bash`) —
  A6.5 DNS calibration (`a65_dns.go`) uses it instead of a predictable `/tmp` path
  (symlink/TOCTOU target for a local unprivileged user, since the script runs as root).

- **TUI is Bubble Tea v2** (`charm.land/bubbletea/v2 v2.0.6` + `charm.land/bubbles/v2
  v2.1.0` + `charm.land/lipgloss/v2 v2.0.3`). The module path is **`charm.land`**, NOT
  `github.com/charmbracelet/*/v2` — the Go proxy REJECTS the github path for the v2 line.
  v2 API actually used (`internal/tui/tui.go`): `View()` returns a `tea.View` (not a
  string) and sets `AltScreen` / `MouseMode` / `WindowTitle` on the returned view EVERY
  frame; key events are `tea.KeyPressMsg` (NOT v1 `tea.KeyMsg`); mouse is `tea.MouseClickMsg`
  (`msg.Mouse().X/.Y`, `tea.MouseLeft`) and `tea.MouseWheelMsg` (`tea.MouseWheelUp/Down`);
  viewport is constructed `viewport.New(viewport.WithWidth(...), viewport.WithHeight(...))`
  with `Width()` / `SetHeight()` / `ScrollUp` / `ScrollDown`; text width via `textinput.SetWidth`.
  **`tea.SetWindowTitle` was REMOVED in v2** — the model stashes a title-kind enum (`titleK`)
  and rebuilds the localized title per frame in `View()` (`windowTitle()`).
  STILL TRUE: the model is **copied by value** every `Update` — never put a `strings.Builder`
  (or other non-copyable) in the model struct; use a plain `string` (`m.content`).
  **In-session re-run hygiene (v0.7.4):** the re-run paths (`dashStale` re-audit,
  `launchApplyTweaks`, `startSteps`, `startRevert`) funnel into `launchEngine` WITHOUT
  going through `goBack`, so listeners stay parked on reused channels. `launchEngine`
  bumps `m.runGen` per run and every streaming listener (`listen`/`listenConn`/
  `listenStats`/`listenProg`) captures its generation; `Update` DROPS any `genMsg` whose
  `gen != m.runGen` and does not re-issue it (no interleaved progress across runs). The
  `connMsg` handler calls `stopSampler()` (nil-safe + idempotent) before re-creating the
  stats channel so monitor SSH connections don't leak. **After a MUTATING run** (step /
  revert / run) the model sets `dashStale`; on returning to the Dashboard it re-runs an
  `audit` and `captureAudit` repopulates the dash fields with post-apply state (stale
  connect-time checkmarks fixed). **Dashboard tweaks reflect ONLY the "Применить твики"
  bucket.** That button applies `tweakBucketIDs()` and EXCLUDES the security steps
  A2/A2.5 (opt-in via the Security menu), yet A2 has non-informational probes
  (`a2.conf00/conf99/ecdsa_absent`). So the Dashboard grid+counters force any
  `isSecurityStep(r.Probe.Step)` (`wiki.go`) probe to satisfied — `dashRowSatisfied`
  (`dashboard.go`) is the single source for the ✓ glyph (`dashAuditCellText` +
  `dashAuditCell`) AND `captureAudit`'s `dashAuditApplied` tally, so glyph + count can't
  drift and `можно применить` reaches 0 after applying every tweak. The probes stay
  VISIBLE in the grid (NOT marked Informational). The Security screen keeps A2's TRUE
  state — it reads `m.dashAuditRaw` (untouched real `Applied`), never the forced display
  set; do NOT route the force-satisfied rule into `dashAuditRaw`.

- **FM local-open-and-sync (2c).** "Open" a remote file (Enter / double-click / menu Open
  row / the Open action / the `O` op) is an async sftp-download to `openTempDir()`
  (`os.TempDir()/morgward-open`, dest = `openLocalDest` = `crc32(remotePath)-basename` so
  same-named files in different remote dirs never collide) → `openLocalFileFn` (build-tagged
  `osOpenArgs` in `openlocal_{windows,darwin,unix}.go`, ARGS **never a shell string** — the
  remote name can't be reinterpreted). A single **lazy** `*fsnotify.Watcher` lives on the
  pointer `*fileSession`; its pump goroutine forwards local paths on `watchCh` and **derefs
  NOTHING** on `*fileSession` (value-copy + sshx-concurrency invariants). All debounce
  (`fmSaveDebounce` 600ms) + conflict-check + upload-back happen on the **MAIN LOOP**
  (`update.go` `fmOpenDoneMsg`/`fmWatchEventMsg`/`fmWatchFlushMsg`); the upload-back goroutine
  owns its OWN sftp client. Conflict = remote mtime moved since download → `errRemoteChanged`
  (`sftpConflictErr`), surfaced as the localized `kFmConflict` notice (special-cased via
  `errors.As` in the `fmXferDoneMsg` error branch, NOT the raw English) → press **`P`**
  (`filesForcePush`) to overwrite. `of.remoteMtime` is **re-stamped** after each successful
  sync (threaded via `fmXferDoneMsg.syncLocal`/`newMtime`, NOT matched by label) so the next
  save doesn't false-conflict. `fileSession.close()` closes the watcher (ends the pump → it
  `close()`s `watchCh` so `listenWatch` retires) and removes the temps. Tests stub the OS
  opener via the `openLocalFileFn` seam so they never spawn a real editor.

- **Stream sanitization lives in `internal/ui` (v0.7.4).** `ui.SanitizeStreamLine` /
  `ui.StripControlAndANSI` strip ANSI escapes + C0 control chars (CR-redraw collapses to
  last segment, tabs→space, newlines preserved) from untrusted remote output. Both the
  CLI terminal print path AND the log file go through it (`ui.Logger`), and the TUI
  delegates (`tui/render.go` `sanitizeStreamLine` → `ui.SanitizeStreamLine`) — so remote
  ANSI/control injection can't corrupt either sink. One hardened stripper, don't
  re-implement per surface.

- **`sshx.Client` is not concurrency-safe.** `monitor` dials its **own** SSH session
  for metrics rather than sharing the engine's client. **Transport liveness + pin
  (v0.7.4):** each `Dial` does a manual TCP dial with OS keepalive
  (`SetKeepAlivePeriod`) AND a protocol-level `keepalive@openssh.com` global request
  every `keepaliveInterval` (30s); after `keepaliveMaxFails` (3) consecutive misses it
  `Close()`s the client so a blocked `Run`/`sess.Wait` (hung command, dead NAT) unblocks
  with a transport error instead of wedging the run forever. **In-run TOFU host-key pin:**
  the FIRST successful handshake pins the host key (`hostKeyCallback`); every later redial
  by the same `Client` (reboot, `SwitchUser`, `UseKey`, second session) must present a
  byte-identical key or it's refused with `ErrHostKeyChanged` ("possible MITM").
  `HostKeyAlgorithms` is **ed25519-first** (`pinnedHostKeyAlgos`) on purpose — A2 removes
  the box's ECDSA host key, so pinning ed25519 (which A2 preserves) keeps the pin valid
  across the A8 reboot. **`WaitForReboot`** returns `ErrRebootAuthFailed` after
  `rebootAuthFailMax` (5) CONSECUTIVE auth rejections (box reachable but our creds
  rejected — distinct from the generic "never reconnected / may be bricked" timeout).

- **Modes removed — default `run` is crypto-only and never locks access.** There is
  NO `--mode` / `config.Mode` / soft / strict any more. A `run` applies SSH **crypto**
  hardening only and PRESERVES the image-default access policy: A2SSH does NOT write
  `AllowGroups sshusers`, does NOT override `PermitRootLogin`, keeps
  `PasswordAuthentication yes`, and never locks root. Whatever root/password login the
  box already had survives a `run`. `build99(ctx)` emits crypto + session knobs only
  (NO AllowGroups / PermitRootLogin). The access lockdown
  (`AllowGroups sshusers` + `PermitRootLogin no` + `passwd -l root`, SSH key-only) is
  the **opt-in `A2-danger` step / TUI security menu** ONLY — A2Danger keeps its own
  `preserveKeyUsers`→sshusers grant. Don't regress: a `run` must leave a brownfield
  operator able to log in exactly as before. The former strict-only §A12 OS-hardening
  (kernel-module blacklist + `/dev/shm` remount) was REMOVED with strict — gone, not
  relocated.

- **A8 restores running services it reboots (brownfield).** A8's reboot bounces a
  brownfield box; services without auto-start (docker `restart: no`, systemd units
  active-but-not-enabled) would stay down. Gated on `!Greenfield`, A8 snapshots the
  running set (docker `ps -q` if `DockerSeen` + active non-enabled `.service` units)
  **before** the reboot and after reconnect `docker start` / `systemctl start`s the
  ones that didn't return. Uses **`docker start`** (same container, no recreate) —
  NEVER `docker compose up` (recreate surfaces latent operator-config bugs, e.g. a
  `postgres:18` PGDATA-layout guard). Restore failure is surfaced, NEVER `StatusFail`
  (a down operator service must not abort the run or trip ssh-revert). Greenfield path
  is byte-identical.

- **Secrets:** read from env `VPS_PASSWORD` / `VPS_HOST` when flags omitted; password
  is cleared after key auth works and never written to the log file.

- **Version drift:** detect 24.04 vs 26.04 and branch — `mlkem768x25519-sha256` KEX +
  PerSourcePenalties only on 26.04; forwarding/CVE knobs on 24.04. **Conservative
  fallback (F20):** `cryptoBlock` gates the OpenSSH-10-only tokens strictly on a
  *confirmed* `Is2604`; a glitched os-release probe (both `Is2404`/`Is2604` false)
  falls back to the 24.04 set (valid on 26.04 too) rather than emitting mlkem KEX that
  `syntaxGate`'s `sshd -t` would reject — a transient detection miss degrades safely
  instead of aborting an otherwise-valid run. **`RequiredRSASize 3072` (OpenSSH 9.1+)**
  is gated the same way — emitted only on `Is2404 || Is2604` (both ship >= 9.1). The
  both-false fallback now targets the OLDEST realistic sshd (22.04 jammy / 8.9p1, which
  lacks `RequiredRSASize`) so pointing morgward at an off-target <24.04 box no longer
  aborts A2 with `sshd -t`: Bad configuration option: RequiredRSASize. Everything else
  in `cryptoBlock` is <= OpenSSH 8.5 (incl. `sntrup761`, `PubkeyAcceptedAlgorithms`,
  `sk-*`), valid on 8.9. Confirmed-24.04/26.04 output stays byte-identical.

- **A2-danger precondition guard (F04):** before writing the `AllowGroups sshusers` +
  `PermitRootLogin no` lockdown drop-in, `A2Danger.Run` calls `assertAdminLoginable` —
  the admin must be in `sshusers` **and** have a non-empty `authorized_keys`, else it
  returns `StatusFail` "run the PRE step first". This refuses up front when PRE never
  ran (user/group/key live only in `Precond`), instead of leaning on the 300s
  ssh-revert timer to heal a hard lockout. `step A2-danger` on a box where PRE never
  ran now fails fast.

- **Handoff before lock (F12):** `A2Danger.Run` does the `SwitchUser` handoff while the
  ssh-revert fail-safe is still armed and root is unlocked; only after a *proven*
  handoff does it `disarmSSHRevert` + `passwd -l root`. A failed handoff self-heals via
  the 300s timer with root still usable (message says the box self-restores in <300s),
  rather than leaving it admin-only with the timer already disarmed.

- **Reverts are faithful, not partial.** `internal/engine/revert.go`:
  - **A2 (F07):** besides dropping the sshd drop-ins, the revert `rm`s cloud-init
    `99-disable-passwords.cfg`, restores `50-cloud-init.conf` `PasswordAuthentication`
    (no→yes), and `passwd -u root` (best-effort `|| true` — on a no-root-password image
    it stays locked, the image default). Every added action only *opens* access, so the
    lockout-safety invariant holds; no more "reverted OK" while root is still locked.
  - **A6.7 (F06):** the A6.7 apply tags each disk-swap fstab line it comments with a
    trailing `# morgward-disabled-swap` marker; the revert uncomments + strips **only**
    marker-tagged lines before `swapon -a`, so it never re-enables swap the operator
    deliberately disabled before morgward ran. Both seds stay single-logical-line (no
    heredoc).

## Known minor

> Cosmetic known-limitations list lives in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
> (§7). The item below is a load-bearing correctness note, kept here.

- **`WaitForReboot` 8s sleep is courtesy-only (F13).** The fixed 8s pre-poll sleep in
  `sshx.WaitForReboot` just avoids racing the still-running pre-reboot sshd; it is
  **not** the correctness guard. The authoritative reconnect signal is `boot_id`
  *inequality* (`bid != preBootID`) — the loop only returns once a connection reports a
  *different* boot_id, so a too-short sleep can't make it act on the pre-reboot kernel.
  Keep boot_id inequality as the gate; don't trust first-reconnect on the strength of
  the sleep alone.
