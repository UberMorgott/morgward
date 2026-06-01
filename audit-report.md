# morgward — L3 Deep Audit Report

- **Project:** `morgward` — single-binary Go executor for the VPS-PREP-RUNBOOK (SSH-based VPS hardening)
- **Level:** L3 Deep + Verified (CRITICAL/HIGH adversarially reproduced)
- **Commit:** `f1b6992` · **Date:** 2026-06-01 · **Go:** 1.26.2 windows/amd64
- **Method:** Wave 1 native scanners (inline) → Wave 2/3 8-dimension review fan-out (workflow `wf_19da26d3-de8`, 11 agents) → adversarial verification of CRITICAL/HIGH

## Health scores

| Check | Result |
|---|---|
| `go build ./...` | ✅ pass |
| `go vet ./...` | ✅ pass |
| `go test ./...` | ✅ pass (all packages) |
| staticcheck | ✅ clean |
| gitleaks (75 commits) | ✅ no leaks |
| govulncheck | ⚠️ 2 reachable stdlib vulns (fixed in go1.26.3) |
| gosec | ⚠️ 12 issues (1 HIGH = false-positive, rest MEDIUM/LOW) |
| `go test -race` | ⛔ not run — no CGO/gcc on host (see F18) |

## Findings by severity

**Counts:** 🔴 1 CRITICAL · 🟠 2 HIGH · 🟡 8 MEDIUM · ⚪ 10 LOW = 21 (+6 wave-1 scanner items)

### 🔴 CRITICAL

- **F01 — Self-update applies downloaded binary with NO signature/checksum verification** (`cmd/morgward/main.go:177,188`, CWE-494, ✅verified)
  - `selfupdate.NewUpdater(selfupdate.Config{})` leaves `Validator` nil → go-selfupdate v1.5.2 skips its integrity check (`if up.validator != nil`) and applies the asset unverified, overwriting the running exe.
  - **Blast radius:** one-click RCE on the operator host (via compromised/typosquatted GitHub release) — and that host SSHes **as root** into every managed VPS. Independent of the Go-version bumps.
  - **Fix:** pass a `Validator` in **both** call sites (`performUpdate` + tui `checkUpdateCmd`): `Config{Validator: &selfupdate.ChecksumValidator{UniqueFilename:"checksums.txt"}}`; publish signed `checksums.txt` per release (goreleaser). Stronger: `ECDSAValidator` with an embedded public key.

### 🟠 HIGH

- **F02 — Generated console password leaks verbatim into log file + TUI scrollback** (`internal/steps/precond.go:61-68`, CWE-532, ✅verified)
  - Soft-mode A2 prints `CONSOLE_PW_MARKER:<pw>` on stdout; `teeLines` forwards every line to the engine sink → `ui.Stream` → log file + TUI pane **before** `extractMarker` reads it. `Logger.Secret()` redaction is bypassed. On-screen leak is unconditional on a normal soft run; file leak bounded by opt-in `--log`.
  - **Fix:** don't transit the secret over streamed stdout — drop any `CONSOLE_PW_MARKER:` line in `teeLines`/`Logger.Stream` before raw/emit, or read it via a dedicated session with `OnOutput=nil`.

- **F03 — Aborted full run cannot be cancelled; engine keeps mutating the VPS** (`engine.go:172` + `update.go:343-350`, CWE-662, ✅verified)
  - `engine.Execute` takes no `context.Context` (0 cancel paths in `internal/engine`). TUI runs it detached; esc/b calls `goBack()` which only swaps in fresh channels. For `command=="run"` the detached goroutine keeps applying lockout-capable hardening (firewall, SSH lockdown, sysctl) with no way to stop. Operator believes they cancelled.
  - **Fix:** thread `context.Context` through `Execute/prepare/Run/steps.Context`; TUI holds the cancel func; cancel between steps at safe boundaries. Minimum: confirm-before-leave on an in-flight run.

### 🟡 MEDIUM

- **F04** — `step A2-danger` applies `AllowGroups sshusers` without ensuring admin exists / is in sshusers; only freshLogin + 300s ssh-revert prevent lockout (`a2_ssh.go:171-203`, CWE-862). Fix: assert membership + non-empty authorized_keys, else `StatusFail` "run PRE first".
- **F05** — `AdminUser` interpolated **unquoted** into root-run shell (id/adduser/usermod/chown/sudoers); never charset-validated (`precond.go:35-47`, CWE-78). Operator-controlled → not CRITICAL. Fix: validate `^[a-z_][a-z0-9_-]{0,31}$` in `config.Validate`.
- **F06** — A6.7 revert uncomments **every** commented swap line in fstab + `swapon -a`, re-enabling swap the operator deliberately disabled (`revert.go:38`, CWE-665). Fix: tag morgward-commented lines; revert only those.
- **F07** — A2 revert leaves **root password locked** and cloud-init password-auth forced off (no `passwd -u root`, no rm 99-disable-passwords.cfg) yet TUI shows "reverted" OK (`revert.go:32`, CWE-665). Fix: extend revert or emit explicit partial-revert warning.
- **F08** — No downgrade protection: `performUpdate` applies any differing 'latest' incl. older (GreaterThan check only in TUI strip) (`main.go:188`, CWE-345). Amplifies F01. Fix: re-assert `rel.GreaterThan(version)`.
- **F09** — Release download rides `http.DefaultClient` → GO-2026-4918 (http2 infinite loop) reachable on update path (`main.go:188`, CWE-345). Fix: bump go1.26.3 + add validator.
- **F10** — Security-menu scroll keys are a no-op (`dashScroll` written, never read); bottom DANGER/SAFE buttons clip unreachable on a short terminal (`security.go:76`). Keyboard 1/2/3 still work. Fix: thread clamped `off` into render + hit-test.
- **F11** — Detached engine goroutine leaks (goroutine + SSH session) via **blocking** `Sink`/`OnProgress` sends after `goBack` replaces the channels (`update.go:748,755`, CWE-404). Fix: make sends abort-aware like the guarded `OnConnect`; resolved with F03.

### ⚪ LOW (10)

- **F12** A2Danger disarms ssh-revert + locks root before handoff; SwitchUser failure leaves box admin-only while run reports FAIL (`a2_ssh.go:203`).
- **F13** `WaitForReboot` fixed 8s pre-poll sleep; boot_id inequality is sole stale-session guard (`client.go:356`).
- **F14** `putAuthorizedKey` username-derived path into single-quoted shell without validation (`precond.go:80`) — covered by F05.
- **F15** gosec G204/G702 `exec.Command` relaunch is **not** an injection vector (argv slice, local args) — documented false-positive; fix the real gap via F01.
- **F16** State-persistence contract is **stale**: no `morgward-<host>.state.json` ever written (`Load` ignores path, `Save` is a no-op) — docs claim cross-run idempotency that doesn't exist (`state.go:21`).
- **F17** Key-screen 'Copy key' hit-test Y not clamped to scroll region → stray copy on a small window (`keyview.go:122`).
- **F18** `go test -race` not run (no CGO/gcc on host); static review found no shared mutable model state, but unverified.
- **F19** Inventory write to `/root/vps-inventory.md` is fire-and-forget — fails silently (`engine.go:299`, CWE-252).
- **F20** Unknown/misdetected OS version silently selects 26.04-only SSH directives; fails closed via `sshd -t` but aborts an otherwise-valid 24.04 run (`a2_ssh.go:357`).
- **F21** §V matrix discards RC/Err → transport failure on a non-lockout row reads as WARN not "could not check" (false-RED only; lockout rows fail closed) (`verify.go:131`).

### Wave-1 scanner items (govulncheck / gosec)

- **W1-VULN-1** GO-2026-4971 net NUL-byte panic on Windows (HIGH, fixed go1.26.3) — reachable via `sshx.Dial` + self-update.
- **W1-VULN-2** GO-2026-4918 x/net http2 infinite loop (MEDIUM, fixed go1.26.3) — reachable via self-update (= F09).
- **W1-SAST** gosec G304×2 (operator-supplied paths — benign), G301 log-dir 0755 (→0750), G103 Windows `unsafe.Pointer` syscall (intentional, `#nosec`). G106 `InsecureIgnoreHostKey` is documented/by-design (fresh VPS, unknown fingerprint).

## What positively held (verified, not bugs)

- SSH lockdown ordering is **correct**: syntax gate (`sshd -t`) before restart; ssh-revert timer armed before every lockout-capable restart; second-session `freshLogin` verify before disarm; root locked only after admin login proven; firewall verified on a second session before persist; reboot pre-gated on persisted SSH port.
- All lockout-capable `sshd_config` paths **fail closed** (bad config → drop-ins removed → run aborts).
- No heredocs in any remote script (base64 delivery intact); key content safely base64-delivered.
- `monitor` dials its **own** SSH session — never touches the engine's `*sshx.Client`.
- TUI model struct has **no non-copyable fields**; both goroutines write only channels; i18n parity OK across maps.

## Top recommendations (priority order)

1. **F01** — wire up the self-update `Validator` (checksum/signature) in both call sites. *(security-critical, one-click RCE)*
2. **F02** — stop streaming the console password; suppress the marker line in the sink. *(secret leak)*
3. **F03/F11** — thread `context.Context` through `Execute`; make TUI abort actually cancel the engine. *(operational safety + goroutine/session leak)*
4. **Bump toolchain to go1.26.3** — closes GO-2026-4971 + GO-2026-4918 (F09). *(one-line, high value)*
5. **F05** — validate `AdminUser` charset in `config.Validate`. *(closes F05+F14 injection)*
6. **F07/F06** — make A2 / A6.7 reverts faithful or warn on partial restore.
7. Run `CGO_ENABLED=1 go test -race ./...` in CI (F18); fix detect/verify robustness (F19/F20/F21) and TUI scroll (F10/F17).

## Limitations of this audit

- `go test -race` could not run on the Windows host (no CGO/gcc) — concurrency conclusions are static-review only.
- Steps were reviewed against code, not executed against a live VPS (no target box).
- MEDIUM/LOW findings were not adversarially re-verified (only CRITICAL/HIGH were); confidence scores reflect single-reviewer assessment ≥40 (L3 threshold).
- Audit-process meta-issues (instruction-source fidelity, missing `versions.lock`, etc.) tracked separately in `audit-meta-issues.md`.

## Fixed in branch `fix/audit-findings`

| Finding | Severity | Commit | Fix |
|---|---|---|---|
| **F01** (+F08) | 🔴 CRITICAL | `8891e17` | Self-update wires a `selfupdate.ChecksumValidator{UniqueFilename:"checksums.txt"}` in both call sites and re-asserts `rel.GreaterThan(version)` (anti-downgrade); unverified/older assets refused. |
| **F09** (+W1-VULN-1/2) | 🟡 MEDIUM | `696c078` | Toolchain bumped to `go1.26.3`, closing GO-2026-4971 (net NUL-byte panic) + GO-2026-4918 (http2 infinite loop) on the update path. |
| **F05** (+F14) | 🟡 MEDIUM | `188b7f2` | `config.Validate` rejects `AdminUser` not matching `^[a-z_][a-z0-9_-]{0,31}$` (`ErrBadAdminUser`); closes the unquoted-shell/`/home/<user>` injection vector. |
| **F02** | 🟠 HIGH | `d437e31` | Console password kept off the streamed sink — the `CONSOLE_PW_MARKER:` line no longer reaches the log file or TUI scrollback. |
| **F03** (+F11) | 🟠 HIGH | `8a07dad` | `context.Context` threaded through `Execute`/`prepare`/`Run`/`RunSteps`; TUI holds the cancel func and an in-flight run now stops at the next step boundary (`ErrCanceled`), with abort-aware hooks (F11). |
