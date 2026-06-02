# morgward

A portable, single-binary tool that hardens and tunes an Ubuntu **24.04 / 26.04**
VPS — fresh or already running services — by applying the
[VPS-PREP-RUNBOOK](https://github.com/UberMorgott/vps-prep-runbook) over an embedded
SSH client. No external `ssh`, `sshpass`, Docker, or web UI required: download one
binary and point it at a server.

## Quick start

1. **Get the binary.** Grab a release for your OS/arch from the
   [releases page](https://github.com/UberMorgott/morgward/releases), or build it
   yourself: `go build -o morgward ./cmd/morgward`.
2. **Run the TUI.** Launch with no arguments to open the interactive terminal UI and
   fill in Host / Port / User / Password (or key):

   ```sh
   morgward
   ```
3. **Or run from the CLI** in one line:

   ```sh
   morgward --host 1.2.3.4 --user root --password 'XXX'
   ```

On the password path morgward generates a fresh ed25519 SSH key, pushes it to the
server, and switches to key auth. The key is shown once (copyable in the TUI, printed
to stdout in the CLI) and **stored nowhere** — save it yourself.

## What it does

morgward applies the runbook's full Phase A hardening + tuning sequence, then runs a
§V verification matrix. The apply order is load-bearing:

```
§1 preconditions → A1 firewall → A8 full-upgrade + reboot → A2 SSH crypto →
A2.5 cloud-init → A3 fail2ban → A4 network (BBR + benchmark) → A5 kernel →
A6 maintenance → A6.5 DNS → A6.7 ZRAM/earlyoom → A7 cleanup → A9 unattended →
A10 detection → §V verification matrix
```

Highlights:

- **Embedded SSH** — password bootstrap → generate ed25519 key → push to server →
  switch to key auth. Stateless one-shot executor (fresh session per command).
- **Version-aware** — auto-detects 24.04 vs 26.04 and adapts (e.g. the post-quantum
  `mlkem768x25519-sha256` KEX and PerSourcePenalties only on 26.04).
- **Fail-safe timers** — a `systemd-run` revert timer is armed before every
  lockout-capable change (firewall, SSH, rp_filter), verified from a second
  independent session, then disarmed.
- **Reboot handling** — A8 polls SSH until reconnect and confirms `boot_id` changed,
  then restores running services that lacked auto-start.
- **Idempotent** — every step is skip-if-already-applied; safe to re-run.
- **Zero local footprint** — by default no files are written next to the binary (no
  checkpoint, no key file, no log). Pass `--log-file <path>` to also write a run log;
  secrets are never written to it.
- **Interactive TUI** — live streaming view, per-step progress bar, host monitor
  footer, and the §V verification matrix.
- **Bilingual UI** — Russian (default) and English, switchable on the fly.
- **Verified self-update** — see [Self-update](#self-update) below.

## Fresh vs brownfield

On a **fresh** box morgward applies the universal baseline. On a **brownfield** box
(one already running docker, WireGuard/OpenVPN, nginx, custom listeners, …) a full
`run` is gated behind `--assume-yes`. With that flag set, it does **not** refuse —
it runs in **coexistence mode**, adapting high-risk steps to preserve what is already
there instead of imposing blind defaults:

- **Firewall (A1) is firewall-manager-aware:** `ufw` active → `ufw allow` SSH and
  every detected port (allow-only); `firewalld` / native `nftables` → defer (change
  nothing); `iptables` / none → INPUT DROP + open detected service ports.
- The **FORWARD policy and docker/nat chains are left untouched** (docker re-asserts
  them on boot).
- **`rp_filter=2`** (loose) is used when the box forwards, so asymmetric WG/OpenVPN
  routes are not severed.
- **Disk swap is preserved**, and **existing SSH key users are kept**.
- After the A8 reboot, services that were running but had no auto-start (docker
  `restart:no`, active-but-not-enabled systemd units) are **restarted**.

A `detect` run inventories everything first and writes `/root/vps-inventory.md`.
Full detail: [`docs/BROWNFIELD.md`](docs/BROWNFIELD.md).

## Reference

### Commands

| Command | Effect |
|---------|--------|
| `run` (default) | full Phase A hardening + §V verification |
| `detect` | read-only discovery + inventory; **changes nothing** |
| `verify` | run only the §V verification matrix |
| `audit` | read-only audit (server facts + tweak audit); **changes nothing** |
| `step <ID…>` | run only the named steps, e.g. `step A4 A5` |
| `revert <ID…>` | revert the named steps, e.g. `revert A2` (undo its drop-ins) |
| `tui` | launch the interactive terminal UI (this is the default with no args) |
| `update` | self-update to the latest GitHub release (checksum-verified) |
| `version` | print the version and exit |
| `help` | show usage |

```sh
morgward                                                      # opens the TUI
morgward detect --host 1.2.3.4 --user root --password XXX     # inspect first
morgward verify --host 1.2.3.4 --key ./my_saved_key           # checks only
morgward step A4 A5 --host 1.2.3.4 --key ./my_saved_key       # re-tweak BBR+kernel
morgward revert A2 --host 1.2.3.4 --key ./my_saved_key        # undo A2's lockdown
```

Step IDs: `PRE A1 A8 A2 A2.5 A3 A4 A5 A6 A6.5 A6.7 A7 A9 A10`. The ephemeral key
generated during a `run` is held in memory and shown once — save it, then pass it via
`--key` to reuse it for targeted `step` / `verify` / `revert` invocations.

### Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--host` | — | VPS IP/hostname (prompted if omitted; also read from `VPS_HOST`) |
| `--port` | `22` | SSH port |
| `--user` | `root` | bootstrap SSH user |
| `--password` | — | bootstrap password (prompted if omitted and no `--key`; also read from `VPS_PASSWORD`) |
| `--key` | — | existing private key path (skips key generation) |
| `--admin-user` | `vpsadmin` | non-root sudo user to create/verify |
| `--log-file` | — | write a full run log to this file (default: no file written) |
| `--assume-yes` | `false` | proceed on a brownfield box (runs in coexistence mode) |

### Hardening is crypto-only by default

A `run` applies SSH **crypto** hardening only and **preserves the box's existing
access policy**: it does not write `AllowGroups`, does not change `PermitRootLogin`,
and keeps password authentication on. The default run **cannot lock you out** —
whatever root/password login you had still works afterward.

The access lockdown — `AllowGroups sshusers`, `PermitRootLogin no`, and locking the
root password (SSH becomes **key-only**) — is **opt-in** via the `A2-danger` step /
TUI security menu.

### Self-update

morgward can update itself to the latest GitHub release in three ways: the CLI
`morgward update` command, the TUI **Обновить** action, and an automatic check on
launch. In every case a downloaded binary is applied only after its SHA-256 is
verified against the release's `checksums.txt`, and only when it is **strictly newer**
(no downgrade). Unverified or older assets are refused, leaving you on your current
build.

## Safety notes

- A default run cannot lock you out (crypto-only, access policy preserved). The
  opt-in `A2-danger` step makes SSH key-only — keep the generated key safe.
- On a **brownfield** box, a full `run` is gated behind `--assume-yes` and then runs
  in coexistence mode (see [Fresh vs brownfield](#fresh-vs-brownfield)) rather than
  imposing blind defaults.
- A cloud-provider firewall / security-group is **invisible to the host**: a host
  `ACCEPT` rule does not by itself mean the port is reachable.
- If the A8 reboot never reconnects, recover via the **provider console**. morgward
  reports this; it cannot roll back a connection it has lost.

## Development

```sh
go build -o morgward ./cmd/morgward   # or: make build
go vet ./...
go test ./...                         # unit tests across most packages
make release                          # cross-compile all targets + checksums.txt
.\build.ps1                           # same, on a Windows dev host
```

`make release` (or `build.ps1` / `build.bat`) produces five binaries plus
`checksums.txt` in `dist/`:

```
morgward-linux-amd64    morgward-linux-arm64
morgward-darwin-amd64   morgward-darwin-arm64
morgward-windows-amd64.exe
checksums.txt
```

`checksums.txt` is standard `sha256sum --text` output (one
`<sha256-hex><two spaces><bare filename>` line per binary). When publishing a GitHub
release, **attach all six files with their names exactly as built** — self-update
matches each downloaded asset against its line in `checksums.txt` and fails closed if
the file is missing or a name doesn't match.

CI (`.github/workflows/ci.yml`) runs on every push and PR: `go build` → `go vet` →
`go test` → `go test -race` → `govulncheck`.

For internals — execution flow, package map, step catalog, and the safety model —
see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).
