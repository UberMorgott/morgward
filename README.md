# morgward

Harden and tune an Ubuntu **22.04 / 24.04 / 26.04** VPS from your own computer.
One binary runs on Windows, macOS or Linux and connects through embedded SSH;
no external SSH client, Docker or web UI is required.

## Install and launch

### Windows

```powershell
irm https://raw.githubusercontent.com/UberMorgott/morgward/main/scripts/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\Programs\morgward\morgward.exe` and opens the TUI.
Set `$env:MORGWARD_NO_LAUNCH='1'` first to install without launching.

### macOS / Linux

```sh
curl -fsSL https://raw.githubusercontent.com/UberMorgott/morgward/main/scripts/install.sh | sh
```

Installs to `~/.local/bin/morgward`. Set `MORGWARD_NO_LAUNCH=1` to skip launching.
Both installers show progress and attempt a checksum check. Missing checksum lists
or entries can skip verification; a checksum mismatch aborts installation.

### Manual download or source build

- Download your OS/architecture from [Releases](https://github.com/UberMorgott/morgward/releases),
  such as `morgward-windows-amd64.exe` or `morgward-darwin-arm64`.
- On macOS/Linux, make the downloaded binary executable with `chmod +x <binary>`.
- To build, use the Go version declared in [go.mod](go.mod):

```sh
go build -o morgward ./cmd/morgward
```

## Connect

- Launch `morgward` without arguments for the interactive form. Enter the VPS host,
  port and SSH credentials; keyboard and mouse are supported. The UI defaults to
  Russian and can switch to English.
- For CLI use, supply the VPS login, not your local computer's credentials:

```sh
morgward --host 203.0.113.10 --user root --key ./saved_key
```

For password authentication, omit `--key` and enter the password when prompted,
pass `--password`, or supply `VPS_PASSWORD` from your environment. `VPS_HOST` fills
an omitted host flag. Save a generated bootstrap key when shown, then use `--key`
for subsequent commands; see the [key-handling contract](CLAUDE.md#transport-and-secrets).

For an interactive SSH shell:

```sh
morgward shell 203.0.113.10 --user root --key ./saved_key
```

The TUI includes a live run log, progress, host monitor, tweak audit, Security menu
and Terminal/Files workspace. Local file editing can sync changes back to the VPS.

## Applying changes

- A full run performs hardening and tuning, including a package upgrade/reboot and
  package cleanup, then verifies the result. [Step catalog and apply order](docs/ARCHITECTURE.md#step-catalog).
- **SSH policy:** [crypto-only defaults, password behavior and opt-in `A2-danger`](CLAUDE.md#ssh-access-and-compatibility).
- **Existing services:** [brownfield gates, firewall managers and per-step preservation rules](docs/BROWNFIELD.md).
- **Recovery:** [timers and transport checks](CLAUDE.md#transport-and-secrets),
  [what revert restores and leaves behind](CLAUDE.md#revert-contract).
- Use `audit` to inspect first. For a customized server, select only the desired steps;
  review the [operator boundaries](docs/BROWNFIELD.md#operator-boundaries) before a full run.
  Keep provider-console access for failures that prevent SSH reconnection.

## CLI reference

### Commands

| Command | Effect |
|---------|--------|
| `run` | Full apply and verification; default when flags are supplied without a command |
| `detect` | Discovery and inventory; preparation can install a bootstrap key and write inventory |
| `verify` | Verification matrix plus tweak audit; same preparation writes as `detect` |
| `audit` | Facts and tweak audit without key installation, inventory writes or step application |
| `step <ID...>` | Apply selected steps in canonical order |
| `revert <ID...>` | Best-effort undo for supported step IDs |
| `shell [host]` | Interactive PTY shell |
| `tui` | Interactive UI; also the default with no arguments |
| `update` | Update to a newer GitHub release |
| `version` | Print the version |
| `help` | Show usage |

- IDs: [canonical step catalog](docs/ARCHITECTURE.md#step-catalog), plus explicit
  `A2-safe` and `A2-danger`. Flags may follow commands and step IDs.
- Example targeted operation:

```sh
morgward step A4 A5 --host 203.0.113.10 --user vpsadmin --key ./saved_key
```

### Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--host` | prompt | VPS IP/hostname; fallback `VPS_HOST` |
| `--port` | `22` | SSH port |
| `--user` | `root` | Initial SSH user |
| `--password` | prompt without key | SSH password; fallback `VPS_PASSWORD` |
| `--key` | unset | Existing private key; takes precedence over password |
| `--admin-user` | `vpsadmin` | Non-root sudo user to create/verify |
| `--log-file` | unset | Optional run log |
| `--assume-yes` | `false` | Confirm a full run on a brownfield/already-hardened box |
| `--known-hosts` | unset | Check the first engine connection against a known_hosts file |
| `--host-fingerprint` | unset | Expected `SHA256:<base64>` host key; mutually exclusive with `--known-hosts` |

### Updates and download verification

- Use `morgward update` or the TUI update action. The TUI checks for releases on launch;
  [update validation and replacement rules](CLAUDE.md#self-update-contract) apply to both paths.
- Release CI attaches build-provenance attestations. To verify a downloaded binary:

```sh
gh attestation verify morgward-windows-amd64.exe --repo UberMorgott/morgward
```

## Development

```sh
go build -o morgward ./cmd/morgward
go vet ./...
go test ./...
make release
```

- Windows release build: `.\build.ps1`.
- Targets: Linux/macOS × amd64/arm64, Windows/amd64. Release binaries use
  `-trimpath -ldflags '-s -w'`. Linux/Windows binaries are UPX-packed before hashing;
  `make release` requires UPX, while `build.ps1` warns and continues if it is unavailable.
- `dist/checksums.txt` lists SHA-256, two spaces and the bare filename for each
  `morgward-<os>-<arch>[.exe]` binary. `build.ps1` also creates desktop packages under
  `dist/desktop/`; Make uses a separate `package-linux` target.
- CI checks formatting and vulnerabilities, builds/vets/tests on Linux, macOS and
  Windows, and runs race tests on Linux.
- To release, update `internal/version/version.go` and push the corresponding
  `vX.Y.Z` tag. Release CI runs vet, tests and vulnerability checks before building,
  attesting and publishing the binaries and checksum list.
- Contributor rules: [CLAUDE.md](CLAUDE.md). Code navigation and TUI contracts:
  [ARCHITECTURE.md](docs/ARCHITECTURE.md).
