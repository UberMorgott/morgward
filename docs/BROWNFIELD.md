# Brownfield coexistence

- Coexistence is driven by `detect.Facts`, without a separate mode or role whitelist.
  Ports, routing and existing state determine step behavior; service names are diagnostic.
- Preserve the contracts below when editing detection, apply, verify, audit or revert.
  Shared execution and safety rules: [ARCHITECTURE](ARCHITECTURE.md), [CLAUDE](../CLAUDE.md).

## Gates

- A full `run` requires `--assume-yes` when `AlreadyHardened` or `!Greenfield`.
  `AlreadyHardened` is checked first. Selective `step` bypasses this confirmation.
- Command preparation and its writes: [execution](ARCHITECTURE.md#execution).
  Use `audit` for inspection without key bootstrap or an inventory-file write:

```sh
morgward audit --host 203.0.113.10 --user root --key ./saved_key
morgward run --host 203.0.113.10 --user root --key ./saved_key --assume-yes
```

## Facts

`internal/detect/detect.go` collects coexistence probes best-effort; missing results
leave zero/empty fields. Fields can be populated on greenfield hosts too.

| Fact | Meaning |
|------|---------|
| `Listeners` | Public, non-loopback, non-sshd sockets from `ss -tulpnH` |
| `ListenPortsTCP`, `ListenPortsUDP` | Sorted, deduplicated public ports, including SSH; A1 deduplicates against `cfg.Port` |
| `ListenServices` | `ListenService` records keyed by protocol/port, with process/origin metadata for display; not firewall inputs |
| `IPForward`, `DockerSeen` | IPv4 forwarding enabled; `docker` command present |
| `WireguardSeen` | WG interfaces, active `wg-quick@*`/`openvpn*` units, or `/etc/wireguard/*.conf` |
| `NatRules` | Exact `MASQUERADE`, `SNAT` or `DNAT` jump target in `iptables -t nat -S` |
| `Forwarding` | `IPForward \|\| DockerSeen \|\| WireguardSeen \|\| NatRules` |
| `DiskSwap` | Names from `swapon --show=NAME,TYPE --noheadings` whose reported type is not `zram` |
| `SSHKeyUsers` | `/root` and `/home/*` directories with non-empty `.ssh/authorized_keys` |
| `Greenfield` | Initially `!IPForward && !DockerSeen && len(Listeners)==0`; forced false by `AlreadyHardened` |
| `AlreadyHardened` | At least two `HardenMarkers`; external-manager INPUT DROP is excluded when `!ManagesIPTables()` |
| `FirewallMgr` | Classifier result below |

- `portFromLocal` reads the port after the final colon; loopback filtering happens
  upstream. `buildInventory` / `coexistSummary` include the parsed facts and service map.
- `classifyFirewallMgr` priority: active ufw → active firewalld → native nftables →
  iptables → none. Native nftables requires an active unit, non-empty nft ruleset and
  `iptablesEffectivelyEmpty`. Anything beyond default ACCEPT policies counts as iptables
  use; installed iptables-persistent also selects iptables. Ambiguity must not falsely defer.
- `Facts.ManagesIPTables()` is true on greenfield; otherwise false only for explicit
  `ufw`, `firewalld`, `nftables`. `iptables`, `none`, empty and unknown values are managed.
  Use this predicate for downstream iptables assumptions.

## Firewall managers

A1 in `internal/steps/a1_firewall.go` uses the greenfield iptables build regardless
of manager: SSH allowance with INPUT/FORWARD DROP, mirrored for IPv6. Brownfield branches:

| `FirewallMgr` | A1 action | Result |
|---------------|-----------|--------|
| `ufw` | `ufwAllowScript`: allow SSH and detected TCP/UDP ports; no deny, enable/disable or default-policy changes; no raw iptables/persistence writes | OK after `freshLoginVerify`, or SKIP when already allowed |
| `firewalld`, `nftables` | Leave the manager untouched | `StatusSkip`, no error; full run continues |
| `iptables`, `none`, unknown | `coexistRuleset`: append INPUT allowances for SSH and detected ports, then INPUT DROP; IPv6 mirror | OK after fresh login and persistence checks, or SKIP if already applied |

- Brownfield `coexistRuleset` leaves FORWARD policy/chains and existing Docker/NAT
  chains untouched; it never flushes them. Existing INPUT rules and their order remain.
- The managed path snapshots both families and arms a 300-second `fw-rollback` timer.
  After fresh login it disarms and saves with `netfilter-persistent`; missing SSH
  persistence is an error. The ufw and deferred paths do not use this timer.
- The managed skip guard checks INPUT DROP plus SSH and every detected service port
  in both live IPv4 rules and `rules.v4`; a newly detected port prevents skipping.

## Firewall consumers

- A8 gates reboot via `ManagesIPTables()`: managed requires non-empty `rules.v4` and
  an SSH dport entry before reboot, then live INPUT DROP and an SSH dport entry after.
  ufw requires active status before and after; firewalld/nftables persistence is operator-owned.
- `verify.Run(..., *detect.Facts)` uses `firewallChecks`: managed/nil facts produce
  iptables INPUT-DROP/SSH-open checks; ufw produces active/SSH-port rows;
  firewalld/nftables produce two NA rows with reasons, without aborting.
- `tweaks.Registry` includes `a1.input_drop`, `a1.ssh_accept`, `a1.rules_v4`,
  `a10.log_rule` only when managed; `a1.rules_v6` additionally requires `HasIPv6`.
- A10 adds its inbound-drop LOG rule and saves netfilter state only when managed.
- A1 revert skips ufw/firewalld/nftables. Managed revert flushes INPUT, sets INPUT
  ACCEPT and removes morgward persistence/rollback files; it does not flush FORWARD/NAT.
- Verification/audit must not impose a FORWARD policy on brownfield hosts.

## SSH

- Access defaults, opt-in lockdown and precondition/handoff guards: [SSH contract](../CLAUDE.md#ssh-access-and-compatibility).
- Only brownfield `A2Danger` calls `preserveKeyUsers` before `AllowGroups`: add each
  non-root `SSHKeyUsers` entry using grant-only `usermod -aG sshusers ... || true`.
  A2SSH and A2Safe do not perform that group-preservation loop.
- Under the [sshd version gate](../CLAUDE.md#ssh-access-and-compatibility), idle timeouts
  (`UnusedConnectionTimeout 600`, `ChannelTimeout session:*=1800`) require
  `Greenfield || Facts.SSHTimeoutsSet`. Detection sets `SSHTimeoutsSet` from an existing
  `UnusedConnectionTimeout` in morgward's `99-hardening.conf`; it is an ownership fact,
  not another hardening marker. Re-runs must retain morgward's own timeout settings.
- Under the same gate, `a2.unused_timeout` reports `na` on unowned brownfield state;
  `a2.channel_timeout` is registered only for greenfield or owned timeouts.

## Other step adaptations

- A5 `kernelHardenConf(Forwarding)` writes `net.ipv4.conf.{all,default}.rp_filter=2`
  when routing, otherwise `1`; `a5.rp_filter` in the audit (also run by `verify`) checks
  the matching value. The matrix has no separate rp_filter row. The live
  `rpf-revert` timer uses the same routing-aware fallback, with fresh-login verification.
- A5 applies `kernel.io_uring_disabled=1` only on greenfield when the proc key exists.
  Brownfield skips the whole write/removal block. Audit/verify check for morgward's
  `60-morgward-iouring.conf` before reporting NA, so existing owned settings remain measurable.
- A5 writes `kernel.panic=10`, `kernel.panic_on_oops=1`; when systemd-coredump tooling
  exists, its drop-in sets `Storage=none` / `ProcessSizeMax=0`. [Live revert rules](../CLAUDE.md#revert-contract).
- A4 keeps `tcp_max_syn_backlog=8192` and `tcp_syncookies=1` in `99-net-tune.conf` alongside its ICMP limits.
- A4 sizes conntrack only for `DockerSeen || NatRules`, skipping kubelet hosts.
  RAM thresholds are 2 GiB → 65536, 4 GiB → 131072, above → 262144; keep a higher
  current maximum. Write `hashsize=max/4`; live writes are best-effort. `a4.conntrack`
  uses the same routing gate and returns `na` for kubelet.
- A6 enables `fstrim.timer` where discard is reported. Time sync optionally installs
  chrony on 22.04/24.04; an active chrony skips installation. The chrony drop-in uses
  `/etc/chrony/conf.d/90-morgward.conf`; `timedatectl` fallback applies in every branch.
- A6.7 keeps disk swap on brownfield; only greenfield runs `swapoff -a` and comments
  fstab swap lines. ZRAM priority is 100; operator disk priorities remain unchanged.
  [Marker-aware revert](../CLAUDE.md#revert-contract) preserves operator-disabled swap.
- Before reboot, A8 snapshots running Docker containers when `DockerSeen` and active
  non-enabled systemd services on brownfield. After reconnect/firewall checks, restore
  missing members with `docker start` / `systemctl start`; never recreate containers
  with `docker compose up`. Restoration failures are reported, not escalated to `StatusFail`.

## Operator boundaries

- A1 opens detected ports, not future listeners or dynamic ranges. Re-run `step A1`
  after a listener appears, or add/persist explicit allowances under the active manager.
  Earlier custom INPUT rules and provider firewalls can still block traffic.
- A6.5 skips when `systemd-resolved` is inactive. Otherwise it selects measured candidate
  upstreams and writes `99-morgward-dns.conf` with opportunistic DoT / allow-downgrade
  DNSSEC, self-reverting on resolution failure. Omit A6.5 to retain a custom resolved setup.
- A7 purges `apport apport-symptoms whoopsie sysstat packagekit`, plus `fwupd` on
  detected guests and multipath packages only when its multipath guards permit.
  It marks `cloud-init netplan.io software-properties-common` manual before autoremove;
  purge records go to `/root/vps-purged-packages.log`. Omit A7 if these packages are needed.
- A4 sets `tcp_mtu_probing=1`; it does not configure forwarded-flow TCP MSS clamping
  or tunnel-interface MTU. Coexistence does not repair a routed VPN's MTU/MSS path.
- After applying, use `verify` plus service checks (`ss -tulpnH`, `swapon --show`,
  service status and external requests). Compare forwarding rules with the pre-run state.
