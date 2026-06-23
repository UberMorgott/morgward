# morgward

A portable, single-binary tool that hardens and tunes an Ubuntu **24.04 / 26.04**
VPS — fresh or already running services — by applying a curated hardening + tuning
sequence over an embedded SSH client. No external `ssh`, `sshpass`, Docker, or web UI required: one binary,
point it at a server.

You run morgward **on your own computer** (Windows / macOS / Linux); it connects out
to the VPS over SSH. The login/password you give it are the **VPS's** SSH credentials.

---

## Install & run

Pick whichever fits. The one-liners need no manual download and show a **progress
bar** while fetching (handy on a slow connection), verify the download's SHA-256, then
launch the interactive UI.

### Windows — one line (download + run)

PowerShell:

```powershell
irm https://raw.githubusercontent.com/UberMorgott/morgward/main/scripts/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\Programs\morgward\morgward.exe` and opens the TUI. To
install **without** auto-launching: `$env:MORGWARD_NO_LAUNCH='1'` before the command.

### macOS / Linux — one line (download + run)

```sh
curl -fsSL https://raw.githubusercontent.com/UberMorgott/morgward/main/scripts/install.sh | sh
```

Installs to `~/.local/bin/morgward` and launches it. `MORGWARD_NO_LAUNCH=1` to skip
launching.

### Download the binary manually

Grab the build for your OS/arch from the
[releases page](https://github.com/UberMorgott/morgward/releases) (e.g.
`morgward-windows-amd64.exe`, `morgward-linux-amd64`, `morgward-darwin-arm64`), then
run it. On macOS/Linux `chmod +x morgward-* && ./morgward-*`.

### Build from source

```sh
go build -o morgward ./cmd/morgward    # Go 1.26.4
```

---

## How to run it

### 1) Interactive UI (recommended)

Launch with **no arguments** to open the terminal UI and type Host / Port / User /
Password (or a key path) into the form — mouse and keyboard both work:

```sh
morgward
```

### 2) CLI — one line

Pass everything as flags. **The `--user` / `--password` are the VPS's SSH login** (on a
fresh server that's usually `root` + the password your VPS provider emailed you):

```sh
morgward --host 203.0.113.10 --user root --password 'your-VPS-password'
#                 │                 │              │
#                 │                 │              └─ SSH password of the VPS
#                 │                 └─ SSH login (fresh box: usually "root")
#                 └─ VPS IP address (or hostname)
```

| You write here | What it is | Example |
|----------------|------------|---------|
| `--host` | VPS IP or hostname | `203.0.113.10` |
| `--user` | SSH login on the VPS | `root` |
| `--password` | that user's SSH password | `'s3cr3t...'` |

Prefer not to put the password in the command line / shell history? Set it via
environment instead — morgward reads `VPS_HOST` and `VPS_PASSWORD` when the flags are
omitted:

```sh
# PowerShell
$env:VPS_PASSWORD = 'your-VPS-password'
morgward --host 203.0.113.10 --user root

# bash / zsh
VPS_PASSWORD='your-VPS-password' morgward --host 203.0.113.10 --user root
```

On the password path morgward generates a fresh ed25519 SSH key, pushes it to the
server, and switches to key auth. The key is shown **once** (copyable in the TUI,
printed to stdout in the CLI) and **stored nowhere** — save it yourself, then reuse it
with `--key ./that_key` for later `step` / `verify` / `revert` runs.

### 3) Just SSH in

```sh
morgward shell 203.0.113.10 --user root --password 'your-VPS-password'
```

---

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
  footer, full mouse support, and the §V verification matrix.
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
| `shell [host]` | open an interactive PTY shell on the VPS (just SSH) |
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
| `--known-hosts` | — | verify the server host key against this known_hosts file on first connect |
| `--host-fingerprint` | — | verify the first host key against this `SHA256:<base64>` fingerprint |

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

### Verify a download's provenance

Releases are built in CI and carry a signed **build-provenance attestation**. With the
[GitHub CLI](https://cli.github.com) you can confirm a binary was produced by this
repo's release workflow from a specific commit:

```sh
gh attestation verify morgward-windows-amd64.exe --repo UberMorgott/morgward
```

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

`make release` (or `build.ps1`) produces five **stripped** (`-trimpath -ldflags
'-s -w'`) binaries plus `checksums.txt` in `dist/`:

```
morgward-linux-amd64    morgward-linux-arm64
morgward-darwin-amd64   morgward-darwin-arm64
morgward-windows-amd64.exe
checksums.txt
```

`checksums.txt` is standard `sha256sum --text` output (one
`<sha256-hex><two spaces><bare filename>` line per binary).

### Releasing

Releases are automated. Bump `internal/version/version.go`, commit, then push a
`vX.Y.Z` tag:

```sh
git tag -a v0.8.3 -m 'v0.8.3'
git push origin v0.8.3
```

`.github/workflows/release.yml` cross-compiles the five stripped targets via
`make release`, attaches a signed build-provenance attestation, and publishes the
GitHub Release with `checksums.txt`. `.github/workflows/ci.yml` runs on every push
and PR: `go build` → `go vet` → `go test` → `go test -race` → `govulncheck`.

For internals — execution flow, package map, step catalog, and the safety model —
see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).
