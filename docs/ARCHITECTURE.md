# morgward — Architecture Index

> Single source for navigating the code. Read this first; then jump to the file.
> Per-session warnings (heredoc/stdin, Bubble Tea v2, concurrency, modes, secrets,
> version drift) live in [`../CLAUDE.md`](../CLAUDE.md#critical-gotchas) — load-bearing, keep open.

## 1. What it is

`morgward` is a portable **single-binary Go executor** for the VPS-PREP-RUNBOOK
(spec cached locally as [`../RUNBOOK.md`](../RUNBOOK.md), gitignored ~98 KB). It
SSHes into a host over an **embedded** `golang.org/x/crypto/ssh` client (no external
`ssh`/`sshpass`/Docker/web UI) and applies the runbook's Phase A hardening + tuning
sequence, then verifies it (§V matrix). One entrypoint serves both a **CLI** and a
**Bubble Tea v2 TUI**.

**Brownfield coexistence (not fresh-only):** it targets Ubuntu 24.04/26.04, fresh OR
already running services. It detects greenfield vs brownfield and **refuses a blind full
`run`** on a non-empty or already-hardened box unless `--assume-yes` — but once consent
is given the steps apply in a **service-preserving** way (A1 keeps detected service ports
and leaves the FORWARD policy untouched; A5 uses `rp_filter=2` when routing; A6.7 keeps
disk swap; A2 adds existing key users to `sshusers`). Coexistence is automatic, driven by
`detect.Facts` — no new flag. See [`BROWNFIELD.md`](BROWNFIELD.md) for the full
detection set + per-step decision table. Read-only commands (`detect`/`verify`/`step`/
`audit`) pass through. State is **in-memory per run** — no cross-invocation skip (the
on-box configs are the durable checkpoints).

## 2. Execution flow

`engine.Execute(ctx, cfg, cmd, ids, Hooks)` ([`internal/engine/engine.go`](../internal/engine/engine.go))
is the single dispatch for `run | detect | verify | step | audit | revert`.
`prepare()` is the shared front half of every command:

```
dial (DialWithRetry, 90s/5s backoff; key wins, else password)
  └─ key bootstrap  (password path only; SKIPPED when readOnly/audit)
        generate ed25519 → push to /root/.ssh/authorized_keys → UseKey → clear cfg.Password
  └─ detect.Run(cli)  → §0.5/§2 discovery; writes /root/vps-inventory.md (skipped if readOnly)
        egress-iface == "" | "lo"  ⇒ hard abort (§2)
  └─ gates
        AlreadyHardened (≥2 markers) ─┐  refuse full `run` unless --assume-yes
        brownfield (non-greenfield)  ─┘  (read-only cmds set allowBrownfield=true → pass)
  └─ build steps.Context {Ctx, Cli, Log, Cfg, State, Facts, AuthLine, KeyPEM}
```

Then per command:

- **run** → `runStepList(orderedSteps(), honorCheckpoint=true)` → §V `verify.Run`.
  Each step writes drop-ins, runs `sshd -t` (fail-closed gate), arms a **300s ssh-revert
  systemd timer**, restarts sshd, **verifies key login in a second independent session**
  before disarming/locking root. A non-nil step error = lockout-capable ⇒ aborts the run.
- **step `<IDs>`** → `RunSteps`, selective; still executes in canonical `orderedSteps()`
  order, never arg order. `allowBrownfield=true`.
- **verify** → `verify.Run` (§V matrix) + `tweaks.Run` audit. No mutation.
- **detect** → discovery + inventory only.
- **audit** → `prepare(readOnly=true)` (mutates **nothing**) + `tweaks.Run`; streams one
  `Progress` per probe to the TUI Dashboard.
- **revert** → `RunRevert` ([`internal/engine/revert.go`](../internal/engine/revert.go)),
  best-effort per-ID undo (only OPENS access, never lockout-capable).

**Cancellation (F03):** `runStepList` checks `ctx.Err()` **only at the boundary BEFORE
each step**, never mid-step — so any SSH/firewall/sysctl lockdown sequence always runs to
completion once begun; an abort halts at the next safe boundary (`ErrCanceled`), leaving a
valid intermediate state. The CLI passes `context.Background()` + a zero `Hooks{}`; the TUI
passes a **cancelable** ctx plus `Sink` (stream log lines), `OnConnect` (monitor footer),
`OnProgress` (per-step + final `Done` summary), `OnKey` (ephemeral PEM — CLI only).

## 3. Entry points

### CLI — [`cmd/morgward/main.go`](../cmd/morgward/main.go)
Flag/env parsing (`partitionArgs` lets flags follow positional step IDs), secrets from
`VPS_PASSWORD`/`VPS_HOST`, interactive prompts when host absent, `cfg.Validate()`, then
`engine.Execute(context.Background(), …, Hooks{OnKey: printKeyBlock})`. Also owns
`performUpdate` (go-selfupdate: detect → anti-downgrade gate → checksum-verified `UpdateTo`
→ relaunch). Bare invocation (no args) launches the TUI. `title_windows.go` / `title_other.go`
set the console title (Windows-only).

### TUI — [`internal/tui/`](../internal/tui/) (Bubble Tea v2, `charm.land/*`)
`tui.Run()` returns a `Result{DoUpdate, TargetVer}` so `main` can self-update after the
alt-screen tears down. Phases: `phaseForm`, `phaseRun`, `phaseSummary`, `phaseWiki`,
`phaseKey`, `phaseMatrix`, `phaseDashboard`, `phaseSecurity`.

| File | Role |
|------|------|
| [`tui.go`](../internal/tui/tui.go) | model struct, `Run()`/`Result`, `Init`, `View()` dispatch, frame-fields, phase enum |
| [`update.go`](../internal/tui/update.go) | `Update` — key/mouse/tick/engine-msg handling, run lifecycle, self-update strip |
| [`dashboard.go`](../internal/tui/dashboard.go) | `phaseDashboard`: post-connect server card + live tweak audit + apply/security buttons |
| [`form.go`](../internal/tui/form.go) | `phaseForm`: host/port/user/password-or-key/mode entry |
| [`run.go`](../internal/tui/run.go) | `phaseRun`: titled progress box + log viewport + monitor footer |
| [`security.go`](../internal/tui/security.go) | `phaseSecurity`: access-state card + SAFE actions + DANGER key-only lock (drives A2-safe/A2-danger/A2.5) |
| [`wiki.go`](../internal/tui/wiki.go) | `phaseWiki`: one fix's what/why/risk/OnBox/Revert page, clickable apply/revert |
| [`summary.go`](../internal/tui/summary.go) | `phaseSummary`: post-finish stats + clickable fix list |
| [`matrix.go`](../internal/tui/matrix.go) | `phaseMatrix`: per-tweak audit ("анализ") table |
| [`keyview.go`](../internal/tui/keyview.go) | `phaseKey`: generated PEM + clipboard "Copy key" button |
| [`render.go`](../internal/tui/render.go) | `sanitizeStreamLine` — CR/tab/width-safe streamed output |
| [`monitor_footer.go`](../internal/tui/monitor_footer.go) | CPU/RAM/DISK footer row rendering |
| [`i18n.go`](../internal/tui/i18n.go) | `Lang` (RU default / EN) + every localized string |
| [`styles.go`](../internal/tui/styles.go) | lipgloss styles + box chrome |
| [`util.go`](../internal/tui/util.go) | focus helpers, host/port parsing |

## 4. Package map

| Package | Path | Responsibility | Key symbols |
|---------|------|----------------|-------------|
| main | [`cmd/morgward/`](../cmd/morgward/) | CLI flag/env parse, dispatch, self-update | `main`, `performUpdate`, `partitionArgs`, `newUpdater`, `printKeyBlock` |
| engine | [`internal/engine/`](../internal/engine/) | wires config → bootstrap → detect → ordered steps → verify; gates; revert map | `Execute`, `prepare`, `Run`, `RunSteps`, `VerifyOnly`, `DetectOnly`, `Audit`, `RunRevert`, `orderedSteps`, `Hooks`, `Progress`, `Summary`, `IsRevertable`, `ErrCanceled` |
| config | [`internal/config/`](../internal/config/) | resolved run config + validation | `Config`, `Validate`, `Mode` (`ModeSoft`/`ModeStrict`), `adminUserRe` (`^[a-z_][a-z0-9_-]{0,31}$`), `Err*` sentinels |
| sshx | [`internal/sshx/`](../internal/sshx/) | embedded SSH client (one-shot executor, base64 delivery), keygen | `Dial`, `DialWithRetry`, `Client.Run`/`Sudo`/`SwitchUser`/`UseKey`/`BootID`/`WaitForReboot`/`SetOutputSink`, `GenerateKeyPair`, `LoadKeyFile`, `SecretMarkerPrefix`, `ErrNoMutualAuth` |
| steps | [`internal/steps/`](../internal/steps/) | one Step per runbook block; stateless | `Step`, `Context`, `Status`, `BenchResult`, `putFile`/`appendLineIfMissing`/`freshLogin`, `A1Firewall` … `A10Detection` |
| detect | [`internal/detect/`](../internal/detect/) | §0.5/§2 discovery; greenfield/brownfield classify; coexistence facts; firewall-manager + service surfacing | `Run`, `Facts` (`Is2604`, `EgressIface`, `ClientIP`, `Greenfield`, `AlreadyHardened`, `Inventory`; coexistence: `ListenPortsTCP`/`ListenPortsUDP`/`WireguardSeen`/`NatRules`/`Forwarding`/`DiskSwap`/`SSHKeyUsers`; round 2: `FirewallMgr` (`ufw`/`firewalld`/`nftables`/`iptables`/`none`), `ListenServices` `[]ListenService{Proto,Port,Process}`), `portFromLocal` |
| verify | [`internal/verify/`](../internal/verify/) | §V verification matrix (effective behavior, not config text) | `Run`, `Result`, `Status` (`StatusPass`/`Warn`/`Fail`/`Skip`/`StatusUnknown`) |
| tweaks | [`internal/tweaks/`](../internal/tweaks/) | per-tweak audit registry (one privileged round-trip); view-only | `Run`, `Probe`, `Result` |
| monitor | [`internal/monitor/`](../internal/monitor/) | live CPU/RAM/DISK over its **OWN** SSH session; reconnects | `ConnInfo`, `Sample`, sampler loop |
| state | [`internal/state/`](../internal/state/) | in-memory per-run checkpoint (no disk, no cross-run skip) | `Checkpoint`, `Load`, `Done`, `Mark`, `Save` (no-op) |
| stats | [`internal/stats/`](../internal/stats/) | best-effort before/after snapshot (cosmetic) | `Capture`, `Snapshot`, `parse.go` parsers |
| ui | [`internal/ui/`](../internal/ui/) | colored terminal + file logger; `SetSink` redirects to TUI | `Logger`, `New`, `Step`/`OK`/`Skip`/`Fail`/`Stream`, `SetSink` |
| wiki | [`internal/wiki/`](../internal/wiki/) | localized what/why/risk/OnBox/Revert per fix (no TUI import) | `Doc`, `FixDoc`, `Lang` |
| version | [`internal/version/`](../internal/version/) | program name/version/tagline (no `main` import) | `Name`, `Version`, `Tagline` |

## 5. Step catalog

One file per runbook block in [`internal/steps/`](../internal/steps/). Apply order is
defined by `orderedSteps()` in [`engine.go`](../internal/engine/engine.go) and is
**load-bearing** — selective `step <IDs>` runs still follow it. "Lockout-capable" =
`Run` can return a non-nil error that **aborts the whole run**.

"Brownfield" notes describe the coexistence path (`!Facts.Greenfield`); blank = same in
both modes. Full table in [`BROWNFIELD.md`](BROWNFIELD.md#3-per-step-decision-table--greenfield-vs-brownfield).

| ID | File | What it does | Lockout-capable | Brownfield (coexistence) |
|----|------|-------------|:---:|--------------------------|
| PRE | [`precond.go`](../internal/steps/precond.go) | §1 preconditions: apt index, admin user, key, sshusers group | yes | — |
| A1 | [`a1_firewall.go`](../internal/steps/a1_firewall.go) | Firewall + fail-safe (iptables-nft, v4+v6) | yes | branches on `FirewallMgr`: `ufw`→`ufw allow` SSH+detected ports (allow-only); `firewalld`/`nftables`→defer, `StatusSkip` (untouched); `iptables`/`none`→coexist INPUT DROP, opens detected `ListenPortsTCP/UDP`, **FORWARD untouched**, chains/nat never flushed. [BROWNFIELD §7](BROWNFIELD.md#7-firewall-managers) |
| A8 | [`a8_upgrade.go`](../internal/steps/a8_upgrade.go) | Full upgrade + reboot (boot_id verified) | yes | — |
| A2 | [`a2_ssh.go`](../internal/steps/a2_ssh.go) | SSH crypto hardening (drop-ins, AllowGroups, crypto) — **legacy full-run** | yes | adds non-root `SSHKeyUsers` to `sshusers` before AllowGroups (grant-only) |
| A2.5 | [`a25_cloudinit.go`](../internal/steps/a25_cloudinit.go) | Cloud-init neutralization | no | — |
| A3 | [`a3_fail2ban.go`](../internal/steps/a3_fail2ban.go) | fail2ban (systemd backend, admin whitelist) | no | — |
| A4 | [`a4_network.go`](../internal/steps/a4_network.go) | Network tuning (BBR, buffers, I/O sched) + benchmark | no | — |
| A5 | [`a5_kernel.go`](../internal/steps/a5_kernel.go) | Kernel hardening (sysctl, THP=madvise, core_pattern) | no | `rp_filter=2` (loose) when `Forwarding` instead of 1 |
| A6 | [`a6_maint.go`](../internal/steps/a6_maint.go) | Maintenance (journald, needrestart, NOFILE, ntp) | no | — |
| A6.5 | [`a65_dns.go`](../internal/steps/a65_dns.go) | DNS hardening (calibrated upstream + DoT/DNSSEC) | no | skips if `systemd-resolved` inactive (custom resolver kept) |
| A6.7 | [`a67_mem.go`](../internal/steps/a67_mem.go) | Memory mgmt (ZRAM zstd + earlyoom) | no | keeps pre-existing disk swap (no `swapoff`); zram still prio 100 |
| A7 | [`a7_cleanup.go`](../internal/steps/a7_cleanup.go) | Cleanup (purge bloatware, apt-mark guard) | no | IRREVERSIBLE purge runs the same — exclude on a customized box |
| A9 | [`a9_unattended.go`](../internal/steps/a9_unattended.go) | Unattended security updates | no | — |
| A10 | [`a10_detection.go`](../internal/steps/a10_detection.go) | Detection (auditd, login-notify, drop-log, OS hardening) | no | — |

**A2 split** (not in `orderedSteps`, resolvable via `step`/TUI security menu, in
[`a2_ssh.go`](../internal/steps/a2_ssh.go)): `A2-safe` (`A2Safe`) = crypto only, image-default
access, **never locks anyone out** (default path); `A2-danger` (`A2Danger`) = opt-in lockdown
(AllowGroups sshusers + PermitRootLogin no + key-only + `passwd -l root`), lockout-capable.
`A2SSH` stays the full-run step for CLI `run --mode` back-compat.

## 6. Safety model

- **Modes** (`config.Mode`): `soft` (default) keeps a console-password fallback
  (`PermitRootLogin prohibit-password`, root **not** locked); `strict` locks the root
  password, sets `PermitRootLogin no`, adds §A12 OS hardening. SSH is key-only in both.
- **ssh-revert timer**: every SSH step `armSSHRevert` installs a **300s** `systemd-run`
  unit that strips morgward's drop-ins + reloads sshd, then `disarmSSHRevert` only **after**
  a verified second-session key login. A bad config self-heals in <300s.
- **Fail-closed `sshd -t` gate**: `syntaxGate` runs `sshd -t` BEFORE any restart; on
  rejection it removes the drop-ins it wrote and aborts — never restarts a broken config.
- **Second-session verify**: `freshLogin` opens an **independent** SSH session with the key
  and runs `true` before disarming the revert timer / locking root. A2-danger also runs
  `assertAdminLoginable` (F04) up front (admin in `sshusers` + non-empty authorized_keys).
- **Key bootstrap**: password path generates an ephemeral ed25519 in memory (`keygen.go`,
  never written to disk by the package), pushes the public line, switches to key auth,
  clears `cfg.Password`. The PEM is shown once (CLI stdout / TUI key screen) — secrets are
  never streamed to the log (`SecretMarkerPrefix`, ui redaction).
- **Root handoff**: executor connects as root, then `SwitchUser(admin)` so later steps run
  as the admin + sudo. On a hardened box root SSH is blocked by `AllowGroups sshusers` —
  reconnect as the admin user with its key for `detect`/`verify`/`step`.
- **Self-update**: `ChecksumValidator{UniqueFilename: "checksums.txt"}` gates every download
  (F01) — a release without `checksums.txt` **fails closed** (`ErrValidationAssetNotFound`)
  rather than applying an unverified binary; plus an anti-downgrade `GreaterThan` gate (F08).
- **In-memory state**: `state.Load` ignores its path and returns a fresh checkpoint; `Save`
  is a no-op. Every run starts clean — no cross-invocation skip; on-box configs are the
  durable checkpoints.

## 7. Known minor

- **Greenfield classifier** can report `greenfield=true` on an already-hardened box (it only
  checks listeners/docker/ip_forward, not hardening markers). Cosmetic — the separate
  `AlreadyHardened` gate handles the real refusal.

## 8. Pointers

- [`../RUNBOOK.md`](../RUNBOOK.md) — cached upstream spec (gitignored, ~98 KB). Read before
  changing any step's behavior; step files are direct translations of runbook blocks and the
  apply order is load-bearing.
- [`../audit-report.md`](../audit-report.md) — security/audit findings (the `F##` IDs
  referenced throughout the code).
- [`../CLAUDE.md`](../CLAUDE.md) — per-session **critical gotchas** (heredoc/stdin caveat,
  Bubble Tea v2 `charm.land` path, `sshx.Client` not concurrency-safe, soft/strict modes,
  secrets handling, version drift). Keep open while editing.
