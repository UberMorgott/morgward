# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`morgward` — a portable single-binary Go executor for the **VPS-PREP-RUNBOOK**
(spec: https://github.com/UberMorgott/vps-prep-runbook, file `VPS-PREP-RUNBOOK.md`).
It connects to a fresh Ubuntu 24.04/26.04 VPS over an **embedded** SSH client
(`golang.org/x/crypto/ssh` — no external `ssh`/`sshpass`/Docker/web UI) and applies
the runbook's hardening + tuning sequence.

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

**Single entrypoint for CLI *and* TUI:** `engine.Execute(cfg, cmd, ids, Hooks)`
(`internal/engine/engine.go`) dispatches `run | detect | verify | step`. The CLI
passes a zero `Hooks{}`; the TUI passes `Sink`/`OnConnect`/`OnProgress` callbacks
to stream the run into its log pane and footer. Touch one path → check the other.

**`prepare()` is the shared front half** of every command: dial → key bootstrap →
detect → gate → build `steps.Context`. Flow:
1. Dial via key (`--key`) else password.
2. **Key bootstrap** (password path): generate ed25519 → push to `/root/.ssh/authorized_keys` → `cli.UseKey()` → clear `cfg.Password`. Key saved as `id_ed25519_<host>`.
3. `detect.Run()` — §0.5/§2 discovery, writes `/root/vps-inventory.md`.
4. **Gates:** `AlreadyHardened` (≥2 hardening markers) and brownfield (non-greenfield) both *refuse* a full `run` unless `--assume-yes`. Read-only commands (`detect`/`verify`/`step`) pass `allowBrownfield=true`.

**Steps:** one file per runbook block in `internal/steps/` (`a1_firewall.go` …
`a10_detection.go`). Each implements `Step{ ID(); Title(); Run(*Context) (Status, detail, error) }`.
A returned non-nil `error` means a **lockout-capable** failure and aborts the whole
run. `orderedSteps()` in engine.go defines the canonical apply order — selective
`step <IDs>` runs still execute in that order, never the arg order.

**Idempotency:** `internal/state` writes `morgward-<host>.state.json`; a full `run`
skips checkpoint-completed steps. `step`/`verify` ignore the checkpoint.

**User handoff:** executor connects as `root`, then `cli.SwitchUser(admin)` after A2
so later steps run as `vpsadmin` + sudo. On a hardened box root SSH is blocked by
`AllowGroups sshusers` — connect as the admin user with its key for `detect`/`verify`/`step`.

Packages: `config` (resolved run config + Validate), `ui` (colored terminal + file
logger, `SetSink` redirects to TUI), `sshx` (client + `Run`/`Sudo`/`SwitchUser`/`UseKey`/`WaitForReboot`, keygen), `detect`, `verify` (§V matrix), `monitor` (live TUI footer metrics), `version`.

## Critical gotchas

- **§A1 stdin caveat — NEVER use heredocs in remote scripts.** The script itself is
  piped to `bash` over stdin, so a heredoc would contend for that stdin. Deliver all
  file content via nested base64: use `putFile` / `appendLineIfMissing` / `anchorSysctl`
  in `steps/step.go`, never `cat <<EOF`.

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

- **`sshx.Client` is not concurrency-safe.** `monitor` dials its **own** SSH session
  for metrics rather than sharing the engine's client.

- **Modes:** `soft` (default) keeps a console-password fallback (`PermitRootLogin
  prohibit-password`, root not locked); `strict` locks root password, sets
  `PermitRootLogin no`, adds §A12 OS-hardening. SSH is key-only in both.

- **Secrets:** read from env `VPS_PASSWORD` / `VPS_HOST` when flags omitted; password
  is cleared after key auth works and never written to the log file.

- **Version drift:** detect 24.04 vs 26.04 and branch — `mlkem768x25519-sha256` KEX +
  PerSourcePenalties only on 26.04; forwarding/CVE knobs on 24.04.

## Known minor

- Greenfield classifier can report `greenfield=true` on an already-hardened box (only
  checks listeners/docker/ip_forward, not hardening markers). Cosmetic; the separate
  `AlreadyHardened` gate handles the real refusal.
