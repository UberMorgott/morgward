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

# Agent guide

- [CLAUDE.md](CLAUDE.md) is the shared contributor guide: development commands,
  remote-script/apt contracts, SSH compatibility, transport, secrets, reverts and self-update.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) owns execution flow, package/step navigation and TUI invariants.
- [docs/BROWNFIELD.md](docs/BROWNFIELD.md) owns detection and service coexistence rules.
- [README.md](README.md) owns installation and CLI usage.
- Keep technical contracts in those files; link here instead of duplicating them.
