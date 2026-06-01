# morgward

Portable single-binary executor for the
[VPS-PREP-RUNBOOK](https://github.com/UberMorgott/vps-prep-runbook). Connects to a
fresh Ubuntu **24.04 / 26.04** VPS over an embedded SSH client (no external `ssh`,
`sshpass`, Docker, or web UI) and applies the full hardening + tuning sequence.

> ⚠️ **For FRESH VPS only.** This release targets a clean, newly-provisioned
> Ubuntu 24.04 / 26.04 box. Run it on a brand-new server before you install
> anything else. On a box that already has services, users, or hardening in
> place a full `run` will refuse (brownfield / already-hardened gates) — support
> for already-provisioned boxes is **not done yet** and is planned for a later
> release.

## Features

- **Embedded SSH** (`golang.org/x/crypto/ssh`) — password bootstrap → generate
  ed25519 key → push to server → switch to key auth. `AutoAdd` host-key policy
  for fresh VPSes. Stateless one-shot executor (fresh session per command).
- **Version-aware** — auto-detects 24.04 vs 26.04 and adapts: PerSourcePenalties
  and the post-quantum `mlkem768x25519-sha256` KEX only on 26.04; the
  forwarding/CVE workaround knobs on 24.04.
- **Fail-safe timers** — `systemd-run` revert timers armed before every
  lockout-capable change (firewall, SSH, rp_filter), verified from an independent
  second session, then disarmed.
- **Reboot handling** — A8 polls SSH until reconnect and confirms `boot_id` changed.
- **Brownfield detection** — a non-empty box stops the run and prints the inventory.
- **Network benchmark** — throughput sampled before and after BBR/buffer tuning.
- **Idempotent** — every step is skip-if-already-applied on the box; safe to re-run.
  No local checkpoint file is written (run state is held in memory for the session).
- **Zero local footprint** — by default the program creates no files next to the exe:
  no checkpoint, no key file, no log. The generated SSH key is shown on a copyable
  key screen (TUI) / printed to stdout (CLI) and stored nowhere — save it yourself.
- **Progress + optional log** — colored `[OK]/[SKIP]/[FAIL]` to terminal; pass
  `--log-file <path>` (or the TUI "save log" toggle) to also write a full run log.
  Secrets (console password / key) are shown once on the terminal, never in the log.
- **Interactive TUI** — run the bare binary (no subcommand) for a terminal form
  (Host/Port/User/Password-or-Key + Action/Mode), then a live streaming view with a
  per-step progress bar, host monitor footer, and the §V verification matrix.
- **Bilingual UI (i18n)** — Russian (default) and English, switchable on the fly via
  the `RU | EN` switcher or the `l` / `Ctrl+L` hotkey.

## Apply order (load-bearing, from the runbook)

`§1 preconditions → A1 firewall → A8 full-upgrade + reboot → A2 SSH crypto →
A2.5 cloud-init → A3 fail2ban → A4 network (BBR + benchmark) → A5 kernel →
A6 maintenance → A6.5 DNS → A6.7 ZRAM/earlyoom → A7 cleanup → A9 unattended →
A10 detection (+ A12 OS-hardening in strict) → §V verification matrix`

## Usage

### TUI (default — just run the binary)

Launching with no arguments opens a Bubble Tea form: **Host**, **Port** (default
`22`), **User** (default `root`), **Password** *or* **SSH key**, plus an **Action**
(run/detect/verify) toggle and a **Mode** (soft/strict) toggle (the Mode row is
hidden for the read-only `detect`). After **Start**, the run streams live into a
scrollable log pane, with a per-step progress bar, a live host **monitor** footer
(CPU/mem/etc.), and — for `run`/`verify` — the §V verification matrix.

```sh
morgward            # opens the TUI
morgward tui        # explicit
```

Keys: `Tab`/`↑↓` move · `←/→` toggle the focused pill · `Enter` next field / Start ·
`↑/↓` scroll the log · `l` (or `Ctrl+L`) switch language · `Esc`/`Ctrl+C` quit.
Mouse clicks work too (focus a field, flip a pill, press Start, scroll the log).

**Language (i18n):** the UI ships in **Russian** (default) and **English**. Toggle
at any time — in both the form and the live run — with the `RU | EN` switcher in the
top-right corner, or the `l` / `Ctrl+L` hotkey. The active language also follows
through to the engine-streamed log messages.

### CLI

```sh
# full hardening + verification (the default command)
morgward --host 1.2.3.4 --user root --password 'XXX' --mode soft

# fully interactive (prompts for host/user/password, password masked)
morgward

# use an existing key instead of generating one
morgward --host 1.2.3.4 --user root --key ~/.ssh/id_ed25519 --mode strict
```

### Commands

| Command | Effect |
|---------|--------|
| `run` (default) | full Phase A + §V verification |
| `detect` | read-only discovery + inventory; **changes nothing** |
| `verify` | run only the §V verification matrix |
| `step <ID…>` | run only the named steps, e.g. `step A4 A5` |
| `help` | show usage |

```sh
morgward detect --host 1.2.3.4 --user root --password XXX     # inspect first
morgward verify --host 1.2.3.4 --key ./my_saved_key          # checks only
morgward step A4 A5 --host 1.2.3.4 --key ./my_saved_key      # re-tweak BBR+kernel
```

Step IDs: `PRE A1 A8 A2 A2.5 A3 A4 A5 A6 A6.5 A6.7 A7 A9 A10`. The ephemeral key
generated during a `run` is held in memory and shown on the key screen / printed
to the CLI — it is **never written to disk**. Save it yourself, then pass it via
`--key` to reuse it for targeted `step`/`verify` invocations.

### Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--host` | — | VPS IP/hostname (prompted if omitted) |
| `--port` | `22` | SSH port |
| `--user` | `root` | bootstrap SSH user |
| `--password` | — | bootstrap password (prompted if omitted and no `--key`) |
| `--key` | — | existing private key path (skips key generation) |
| `--mode` | `soft` | `soft` (console password fallback) or `strict` (root locked) |
| `--admin-user` | `vpsadmin` | non-root sudo user to create/verify |
| `--log-file` | — | write a full run log to this file (default: no file written) |
| `--assume-yes` | `false` | proceed on a brownfield box (NOT recommended) |

**soft vs strict:** `soft` keeps `PermitRootLogin prohibit-password` and an admin
console password (recoverable if the SSH key is lost). `strict` sets
`PermitRootLogin no`, locks the root password, and adds the §A12 OS-hardening
(module blacklist, `/dev/shm` mount options). SSH is key-only in both modes.

## Build

```sh
go build -o morgward ./cmd/morgward
```

Cross-compile (or use `./build.ps1` / `make release`):

```sh
GOOS=linux  GOARCH=amd64 go build -o dist/morgward-linux-amd64   ./cmd/morgward
GOOS=linux  GOARCH=arm64 go build -o dist/morgward-linux-arm64   ./cmd/morgward
GOOS=darwin GOARCH=arm64 go build -o dist/morgward-darwin-arm64  ./cmd/morgward
GOOS=windows GOARCH=amd64 go build -o dist/morgward-windows-amd64.exe ./cmd/morgward
```

## Layout

```
cmd/morgward/   CLI entry point (flags + interactive prompts)
internal/config/    resolved run configuration
internal/ui/        colored terminal + file logger, step status
internal/sshx/      SSH client wrapper, ed25519 keygen, reboot polling
internal/state/     in-memory run checkpoint (idempotency; not persisted)
internal/detect/    §0.5/§2 discovery, greenfield/brownfield classification
internal/steps/     one file per runbook block (a1_firewall.go … a10_detection.go)
internal/verify/    §V verification matrix runner
internal/engine/    orchestrator (bootstrap → detect → steps → verify)
```

## Safety notes

- Run against a **fresh** VPS. Brownfield boxes (existing Docker/VPN/services) are
  detected and halt the run — adapt manually per §0.5 first.
- A cloud-provider firewall/security-group is invisible to the host; host
  `ACCEPT` ≠ reachability.
- If the A8 reboot never reconnects, the box may be bricked — recover via the
  provider console. The tool reports this; it does not (and cannot) roll back a
  lost connection.
