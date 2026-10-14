<!-- quality-gate -->
## Completion gate (mandatory)

After changing files, run `qgate` from the repository root. Use `qgate -All` when
dependencies, build configuration, generated files or several stacks changed.

Exit code 0 means done. Anything else means NOT done: the output names the exact
failures -- fix them and run it again. Do not report completion while the gate is
failing, and never edit or disable the gate to make it pass. Include the command
you ran and its pass/fail result in your final response.

If `qgate` is unavailable, report that as a blocker, do not skip it. It installs with
`irm https://raw.githubusercontent.com/UberMorgott/quality-gate/main/bootstrap.ps1 | iex`

If the gate itself is wrong -- it crashes, blames code that is provably correct,
misses a whole stack, or cannot be satisfied at all -- do not work around it and do
not disable it. Open an issue against the gate and say so in your final response:

```powershell
qgate where   # install path + commit, paste this into the issue
gh issue create --repo UberMorgott/quality-gate --title "<what broke>" --body "<qgate output, the command you ran, the file it blamed, `qgate where` output>"
```
<!-- /quality-gate -->

# Contributor guide

- Read [ARCHITECTURE.md](docs/ARCHITECTURE.md) for execution flow, packages, steps and TUI contracts.
- Read [BROWNFIELD.md](docs/BROWNFIELD.md) before changing detection or service coexistence.
- Read the local, gitignored `RUNBOOK.md` before changing step behavior; `audit-report.md` records audit findings.
- Preserve the invariants below across CLI and TUI. Keep each contract in its owning document.

## Development

- `go.mod`: Go `1.27.1`; module `github.com/UberMorgott/morgward`.
- Build: `go build -o morgward ./cmd/morgward`.
- Check: `go vet ./...`, `go test ./...`; focused test: `go test ./internal/tui -run TestName -v`.
- Format Go changes with `gofmt`; release commands and targets: [README](README.md#development).

## Remote scripts and apt

- `sshx.Client.Sudo` delivers scripts to `bash` over stdin. Never use heredocs.
  Use `putFile` or `AppendLineIfMissing` in `internal/steps/step.go` for content;
  `pipeToBash(script)` runs nested scripts without predictable root-executed temp files.
- Quote remote values with `detect.ShQuote`; `tui.shQuote` delegates to it.
  `steps.AppendLineIfMissing` is shared with engine bootstrap; keep one implementation.
- Build lock-acquiring apt commands from `aptGet` (`apt-get -o DPkg::Lock::Timeout=300`)
  or `aptInstall(pkgs)`, including `clean`, `autoremove` and `purge`. Bare `apt-get -s`
  simulations are exempt. Use `aptInstallOptional` only when absence is non-fatal.
- Apt/dpkg scripts use `aptPrelude()`: `DEBIAN_FRONTEND=noninteractive`,
  `NEEDRESTART_MODE=a`, `NEEDRESTART_SUSPEND=1`, `UCF_FORCE_CONFFOLD=1`, the needrestart
  drop-in, `aptQuiesce`, and a per-boot index refresh. `aptQuiesce` runs before any
  lock-taking apt: stops active `apt-daily{,-upgrade}.timer` (restarted via EXIT trap;
  runtime-only, no revert), `systemctl stop --no-block` both services, SIGTERMs the
  unattended-upgrade main process and the `apt.systemd.daily lock_is_held` child by
  argv (`aptAutomationRegex`; KillMode=process; never the `unattended-upgrade-shutdown`
  helper, same truncated comm) once per PID, re-checked each wait iteration, waits up to
  `AptQuiesceWaitSec` (600s) for lock holders with one status line per 30s, then
  `dpkg --configure -a` if `dpkg --audit` reports an interrupted run. `AptUpdateMarker` is `/run/morgward-apt-updated`;
  stamp only after success, swallow refresh failure so later steps retry.
- PRE and A8 use `aptPreludeNoUpdate()` with their own unconditional refresh. PRE
  aborts on refresh failure and stamps the marker. A8 runs update then full-upgrade.
- Use `aptConfOpts` for package-changing calls: `--force-confdef`, `--force-confold`,
  `Dpkg::Use-Pty=0`. Never use `stdbuf`; its `LD_PRELOAD` propagates into apt children.
- Feature-dependent sysctls need `/proc/sys/...` existence gates before writing drop-ins.
  Keep apply, audit and revert coverage aligned for each knob.

## SSH access and compatibility

- There are no modes or `--mode`. Default `run` uses `A2SSH` for SSH crypto/session
  hardening, without adding `AllowGroups`, overriding `PermitRootLogin` or locking root.
  It explicitly enables `PasswordAuthentication` and `KbdInteractiveAuthentication`
  through `conf00(false)` and enables the cloud-init password override.
- `A2Safe` writes only the crypto/session `99-hardening.conf`, installs the admin key
  and leaves password directives untouched. Keep its writer separate from `A2SSH`;
  only `A2SSH` requests the root-to-admin executor handoff in `applyCryptoHardening`.
- `build99` contains no access-policy directives, but `cryptoBlock` includes
  `DisableForwarding yes`, `AllowAgentForwarding no` and `X11Forwarding no`.
  Crypto-only does not mean all SSH behavior is unchanged or that lockout is impossible.
- `A2Danger` is opt-in: `98-access.conf` sets `AllowGroups sshusers` and
  `PermitRootLogin no`; `conf00(true)` disables password/keyboard-interactive auth;
  cloud-init gets `ssh_pwauth: false`; root's password is locked after verification.
  Brownfield key-user grants and timeout ownership: [BROWNFIELD](docs/BROWNFIELD.md#ssh).
- Before the lockdown drop-in, `assertAdminLoginable` requires the admin to exist,
  belong to `sshusers` and have non-empty `authorized_keys`. Resolve its home with
  `getent passwd`, falling back to `/home/<admin>`; failures say to run PRE first.
- `syntaxGate` runs `sshd -t` before host-key changes/restart and removes
  `RmSSHDropIns` on rejection. `armSSHRevert` schedules drop-in removal, missing
  ed25519-key recovery and SSH reload for 300 seconds; disarm after fresh admin login.
- `A2Danger` must complete `SwitchUser(admin)` while `ssh-revert` is armed and before
  `passwd -l root`. A failed handoff leaves the timer armed without locking root
  itself. Only successful handoff (or an already non-root executor) permits disarm/lock.
- `steps.RmSSHDropIns` lives in `internal/steps/a2_ssh.go`: `00-hardening.conf`,
  `98-access.conf`, `99-hardening.conf`. Add new owned SSH drop-ins to this shared list
  used by syntax rollback, the timer and explicit revert.
- Supported release flags are `Is2204`, `Is2404`, `Is2604`. `detect.Run` probes
  `sshd -V`, falling back to `ssh -V`; `SSHDAtLeast` is false when `SSHDMajor == 0`.
  `cryptoBlock` accepts the release flag OR the probed version:

| Directive | Release shortcut | Minimum probed sshd |
|-----------|------------------|---------------------|
| `RequiredRSASize 3072` | 24.04 / 26.04 | 9.1 |
| `UnusedConnectionTimeout`, `ChannelTimeout` | 24.04 / 26.04 | 9.2; also the [ownership gate](docs/BROWNFIELD.md#ssh) |
| `PerSourcePenalties` | 26.04 | 9.8 |
| `mlkem768x25519-sha256` | 26.04 | 9.9 |

- Unconfirmed release and failed version probe use the conservative OpenSSH 8.9-safe set.

## Transport and secrets

- `sshx.Client` is not safe for concurrent caller operations. `monitor` uses its own
  SSH client; the internal keepalive goroutine has separate lifecycle synchronization.
- `transportLost(r)` means `r.RC == -1 || r.Err != nil`, not a remote command failure
  or proof of lockout. A1 reconnects its executor after transport loss during the
  persistence-package install before checking a fresh login.
- A1, A2/A2-danger and A5 use `freshLoginVerify`: independent key login plus `true`,
  one retry after five seconds; cancellation skips the retry wait.
- `DialWithRetry` uses a 90-second default window and five-second backoff. A pin
  mismatch fails immediately; `isNoMethodAvailable` (`attempted methods [none]`)
  stops after the second failed attempt. Rejected credentials with an attempted
  method, such as `[none publickey]`, retain provisioning retries.
- `hostKeyCallback` pins the first accepted host key per `Client` (TOFU unless an
  operator pin is supplied). Same-client reconnects, `UseKey`, `SwitchUser` and reboot
  redials require identical key bytes or return `ErrHostKeyChanged`.
  `pinnedHostKeyAlgos` prefers ed25519, which A2 preserves when deleting ECDSA keys.
  Independent `freshLogin` and monitor clients use their own TOFU pins, not the engine's pin.
- OS keepalive and `keepalive@openssh.com` run at `keepaliveInterval` (30 seconds).
  Probes have a ten-second timeout; three consecutive misses close the connection
  and unblock commands. `WaitForReboot` reports `ErrRebootAuthFailed` after five
  consecutive authentication failures.
- Reboot completion requires a non-empty `boot_id` different from `preBootID`.
  The initial eight-second sleep is only a delay, never evidence of a reboot.
- Generated ed25519 private keys stay in memory and reach CLI stdout through `OnKey`
  or the TUI key screen, not the logger. `cfg.Password` is cleared after password
  bootstrap switches to key auth. `SecretMarkerPrefix` lines are excluded from streaming.
- `ui.SanitizeStreamLine` / `ui.StripControlAndANSI` sanitize remote log output for
  CLI, file log and TUI: strip ANSI/C0 controls, collapse CR redraws, replace tabs
  with spaces and preserve line breaks. Reuse these sanitizers.

## Revert contract

- `internal/engine/revert.go` applies best-effort per-ID undo; SSH/firewall reverts
  must open access. Unknown IDs fail; unsupported IDs skip. A per-ID failure does
  not abort the batch. This is targeted cleanup, not restoration of an original snapshot.
- A2 removes owned SSH drop-ins and `99-disable-passwords.cfg`, restores
  `50-cloud-init.conf` password auth from `no` to `yes`, attempts `passwd -u root`,
  regenerates missing ed25519 host keys and reloads SSH. Root without a usable password
  may remain locked. Use `revert A2` for the SSH family; split IDs have no revert script.
- A6.7 apply tags only the fstab swap lines it disables with `# morgward-disabled-swap`.
  Revert uncomments and strips only that marker before `swapon -a`; operator-commented
  lines remain disabled. Keep both sed expressions on single logical lines.
- A5 removes its drop-ins, reloads sysctls and explicitly resets live `kernel.panic=0`,
  `kernel.panic_on_oops=0`, `kernel.io_uring_disabled=0` with best-effort writes.
  `sysctl --system` alone cannot reset values whose defaults have no remaining file.
- `revertDetail` must identify intentional leftovers: A5 may need reboot if live resets
  fail; A6 leaves `fstrim.timer` enabled. Firewall-manager exceptions: [BROWNFIELD](docs/BROWNFIELD.md#firewall-consumers).

## Self-update contract

- `internal/selfupdate`: `Latest(ctx, slug)` → `GreaterThan(current)` → `Apply(ctx, exe)`.
  CLI `applyUpdate` and TUI `checkUpdateCmd` share it; `applyUpdate` enforces the
  anti-downgrade gate. `GreaterThan` fails closed on unparsable versions.
- `AssetName(slug)` must match `dist/<repo>-<goos>-<goarch>[.exe]` from the release
  scripts; `TestAssetNameMatchesReleasePipeline` pins that contract.
- `checksums.txt` is the required SHA-256 integrity check, not a signature verifier.
  `Latest` errors if a matching platform asset lacks it; no platform asset means no update.
  Missing checksum entries or mismatched hashes abort `Apply` before disk writes.
- `OldPath(exe)` is `<dir>/.<base>.old`, shared by the swap and `cleanupOldBinary`
  at startup. Preserve the name. A failed replacement rename attempts to restore it.
