# Architecture

- Start here for code navigation. [Contributor contracts](../CLAUDE.md) cover remote scripts,
  SSH, transport, secrets, reverts and updates; [BROWNFIELD](BROWNFIELD.md) owns coexistence.
- `morgward` runs locally and manages a VPS through embedded `golang.org/x/crypto/ssh`.
  Installation, commands and flags: [README](../README.md).

## Execution

- `engine.Execute(ctx, cfg, cmd, ids, Hooks)` dispatches `run`, `detect`, `verify`,
  `step`, `audit`, `revert`. CLI and TUI share `internal/engine/engine.go`.
- `prepare` loads credentials → dials → bootstraps a key unless `readOnly` → detects
  facts → writes inventory unless `readOnly` → checks egress and confirmation gates
  → builds `steps.Context`. Missing/loopback egress aborts. Before/after stats are best-effort.
- Password bootstrap writes the generated public key to `/root/.ssh/authorized_keys`
  via sudo, then calls `UseKey`. An existing `--key` skips generation.
- Only `Audit` calls `prepare(..., true, true, ...)`: original credentials, no key
  installation or inventory write. `DetectOnly` and `VerifyOnly` use `readOnly=false`:
  both can bootstrap a key and write `/root/vps-inventory.md`; neither applies steps.
  Verification runs the matrix plus `tweaks.Run`.
- Full `Run` uses `orderedSteps()` then the verification matrix. `RunSteps` selects
  from `resolvableSteps()` in canonical order, regardless of argument order.
  [Confirmation gates](BROWNFIELD.md#gates) apply to a full run, not selective steps.
- Each selected step executes; step-local checks may skip work. There is no checkpoint
  or resume list. `detect.Run` derives state from the server on each invocation.
- `runStepList` checks cancellation before each step. A non-nil step error aborts;
  `StatusFail` alone does not. A8 uses `rebootCtx` without cancellation for reconnect
  and service restoration after sending the reboot.
- CLI passes `context.Background()` and `Hooks{OnKey: printKeyBlock}`. TUI supplies
  cancelable context, `Sink`, `OnConnect`, `OnProgress` and optionally `PreparedKey`.
- CLI `parseArgs` re-parses around positionals; `bindFlags` defines flag arity.
  `VPS_HOST` / `VPS_PASSWORD` fill omitted flags. Bare invocation starts TUI;
  `shell` has its own PTY path. TUI returns `Result{DoUpdate, TargetVer}` so main can
  update after leaving the alternate screen.

## Package map

| Path | Responsibility / entry points |
|------|-------------------------------|
| `cmd/morgward/` | CLI, PTY shell, update/relaunch; `main`, `parseArgs`, `bindFlags`, `applyUpdate` |
| `internal/engine/` | Dispatch, preparation, ordered execution, `Hooks`, `Progress`, `Summary`; `revert.go` owns undo |
| `internal/config/` | `Config.Validate`, `adminUserRe`, credential and host-pin validation |
| `internal/detect/` | `Run`, `Facts`, inventory, listener/firewall classification; [coexistence facts](BROWNFIELD.md#facts) |
| `internal/sshx/` | Embedded SSH, key generation, PTY/SFTP, host-key pins and transport lifecycle |
| `internal/steps/` | `Step`, `Context`, `Status`, apply scripts and shared shell helpers |
| `internal/verify/` | Effective-state matrix; `Run`, `Result`, `StatusUnknown`, `firewallChecks` |
| `internal/tweaks/` | `Registry`, `Probe`, `Run`, `Result`, `Tally`; privileged batch audit |
| `internal/findings/` | `Collect`, `Report`, `Finding`; read-only observations with no apply/revert |
| `internal/tui/` | Bubble Tea UI, dashboard, terminal and file workspace |
| `internal/monitor/`, `internal/stats/` | Live sampler; best-effort before/after `Capture` |
| `internal/termsize/`, `internal/ui/` | `TrueSize`; logger and remote-output sanitizers |
| `internal/selfupdate/` | Release discovery, checksum verification and executable replacement |
| `internal/wiki/`, `internal/version/` | Localized fix docs; `Name`, `Version`, `Tagline` |

## Step catalog

Canonical `orderedSteps()` order; files under `internal/steps/`. [Coexistence](BROWNFIELD.md), [SSH variants](../CLAUDE.md#ssh-access-and-compatibility), [undo](../CLAUDE.md#revert-contract).

| ID | File | Responsibility |
|----|------|----------------|
| PRE | `precond.go` | Apt index, admin user, key and `sshusers` |
| A1 | `a1_firewall.go` | Host firewall and persistence |
| A8 | `a8_upgrade.go` | Full upgrade and reboot |
| A2 | `a2_ssh.go` | SSH crypto/session hardening and executor handoff |
| A2.5 | `a25_cloudinit.go` | Cloud-init neutralization |
| A3 | `a3_fail2ban.go` | fail2ban with systemd backend and admin whitelist |
| A4 | `a4_network.go` | BBR, buffers, ICMP limits, I/O scheduler, conntrack and benchmark |
| A5 | `a5_kernel.go` | Kernel sysctls, THP, coredump policy, panic and io_uring controls |
| A6 | `a6_maint.go` | Journald, needrestart, NOFILE, time sync and fstrim |
| A6.5 | `a65_dns.go` | Resolved upstream calibration, DoT and DNSSEC |
| A6.7 | `a67_mem.go` | ZRAM and earlyoom |
| A7 | `a7_cleanup.go` | Guarded package purge and autoremove |
| A9 | `a9_unattended.go` | Unattended security updates |
| A10 | `a10_detection.go` | auditd, login notification and inbound-drop logging |

- `extraSteps()` adds explicit `A2-safe` / `A2-danger`; `revertScript` supports A1, A2, A3, A4, A5, A6, A6.5, A6.7, A9, A10.

## Audit results

- `a2.socket_activation`, `a5.apparmor_userns`, `a5.lockdown`, `a6.tmp_tmpfs` are informational probes with no apply action.

- `tweaks.Result.Err` is non-empty when the batch cannot run or a probe emits no
  `id<TAB>...` line. `Applied=false` then means unknown: display `?` / `probe error`,
  not an unapplied failure. `Tally` excludes both errors and `Informational` rows.
- Matrix transport errors become `StatusUnknown` for non-lockout rows; lockout checks
  still fail closed. Firewall-specific checks: [BROWNFIELD](BROWNFIELD.md#firewall-consumers).

## Findings

- Findings are observations morgward never fixes; they are not tweaks and have no
  `Probe`, apply, revert, `Tally` or `tweakBucketIDs` entry. Only `Audit` runs
  `findings.Collect`; the result is `Summary.Findings` (`Report{Items, Errors}`).
- A check that cannot run lands in `Report.Errors`; an empty `Items` alone is never
  "clean". `db_bind` reads `Facts.Listeners` and errors when `Facts.ListenersOK` is false.
- `db_bind` flags DB ports (tcp 3306/5432/6379/27017/9200/11211, udp 11211) whose
  local address is unspecified (`0.0.0.0`, `*`, `[::]`, `::`); wording is "listens
  on all interfaces", not proof of internet reachability. Interface-scoped binds are skipped.
- The other checks share one `cli.Sudo(findings.Script)` round-trip. Protocol:
  `F<TAB>check<TAB>value` per finding and `R<TAB>check<TAB>rc` per check; a missing
  `R` or `rc != 0` is a check error. Values pass through `ui.StripControlAndANSI`.
- Checks: non-root UID 0 (`/etc/passwd`); empty `/etc/shadow` password field, names
  only; `NOPASSWD` in non-comment lines of `/etc/sudoers` and `/etc/sudoers.d/*`
  (sudo-ignored names skipped), excluding only PRE's exact
  `<admin> ALL=(ALL) NOPASSWD:ALL` in `90-<admin>`; docker group members, supplementary
  and primary GID; `/run/reboot-required` as `info`.
- CLI prints the `FINDINGS` log block (`logFindings`); the TUI renders the same report
  below the Dashboard audit grid (`dashFindingsLines`), not clickable.

## TUI contracts

- Dependencies: `charm.land/bubbletea/v2 v2.0.9`, `charm.land/bubbles/v2 v2.2.1`,
  `charm.land/lipgloss/v2 v2.0.6`. Use the `charm.land` module paths.
- `tui.go` owns the value-copy `model`, `Run` and `View`; `update.go` owns events and
  lifecycle. Do not store non-copyable values such as `strings.Builder` in the model;
  use `m.content` (string). State shared with goroutines needs explicit ownership.
- `View()` returns `tea.View` and sets `AltScreen`, `MouseMode`, `WindowTitle` and
  `Cursor` every frame. Build the localized title through `windowTitle()` / `titleK`.
  `terminalFrame()` returns body and native `*tea.Cursor` from one `cursorSnapshot`.
- Events use `tea.KeyPressMsg`, `tea.MouseClickMsg`, `tea.MouseWheelMsg` and
  `msg.Mouse()` coordinates. Use `viewport.New(viewport.WithWidth(...), viewport.WithHeight(...))`,
  viewport `Width`/`SetHeight`/`ScrollUp`/`ScrollDown`, and `textinput.SetWidth`.
- `frames_golden_test.go` pins framed screens in `testdata/frames.golden` at 100x40
  and 60x20. Rendering refactors preserve these frames. Set `UPDATE_GOLDEN=1` only
  for intentional layout changes and inspect the diff; never regenerate to hide a failure.
- `chromeViewH(fixed, pinned)` in `render.go` owns row arithmetic. Each screen's named
  height wrapper must serve both rendering and hit-tests. `vpHeight` uses
  `chromeViewH(2, 0)` plus finished-state adjustments; `phaseRun` is covered separately
  by `TestVPHeightMatchesLegacyChrome` / `TestRunViewRowsMatchChromeBudget`.
- Route scrolling through `scrollBy(d)`. `snap.scrollback` must retain
  `len == scrollbackLen` even when only visible entries contain rendered text;
  `cursorBodyRow` depends on that full length.
- Window dimensions pass through `termsize.TrueSize` in `WindowSizeMsg`; Windows
  needs the console viewport, not screen-buffer height. `resizeTick` delivers updates.
- `launchEngine` increments `runGen`; streaming listeners tag `genMsg`. `Update`
  drops stale generations without reissuing their listeners. `connMsg` calls
  `stopSampler` before replacing the stats channel. Mutating runs set `dashStale`;
  returning to Dashboard re-audits and `captureAudit` refreshes state.
- Dashboard applies `tweakBucketIDs()`; A2/A2.5 belong to Security. Display glyphs
  (`dashRowSatisfied`) and `captureAudit` counts treat security rows as satisfied.
  Keep `dashAuditRaw` unchanged for Security's real state; informational rows are
  excluded from the dashboard display, not from the raw results.
- Screens use named files (`form.go`, `dashboard.go`, `run.go`, etc.); `styles.go` owns styling, `i18n.go` localization (RU default, EN), `monitor_footer.go` the footer.
- Terminal/Files share a workspace SSH client independent of the engine. `terminal.go`
  / `termsession.go` own PTY rendering; `files_*` / `filesession.go` own file UI.
  Listing/mutations use quoted shell commands; content transfer uses SFTP.
- Local Open downloads under `openTempDir()` (`os.TempDir()/morgward-open`).
  `openLocalDest` prefixes a sanitized basename with CRC32 of the remote path.
  Keep `safeBaseName` and discrete `osOpenArgs`; Windows opens via `cmd /c start`.
- One lazy `fsnotify.Watcher` per `*fileSession` forwards paths through `watchCh`;
  its pump never dereferences session state. Main-loop handlers own debounce (600 ms)
  and bookkeeping; transfer commands own SFTP clients and check remote mtime.
  Conflict uses `sftpConflictErr` / `kFmConflict`; `P` explicitly forces upload.
  Success threads `syncLocal` / `newMtime` back to refresh the baseline. Closing the
  session closes the watcher and removes temps. Tests stub `openLocalFileFn`.
