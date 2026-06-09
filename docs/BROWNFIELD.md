# Brownfield coexistence

> How morgward hardens a **non-fresh** VPS (already running docker, WireGuard/OpenVPN,
> nginx/sites, bots, custom listeners) **without breaking those services**. Greenfield
> behavior is byte-identical to before; coexistence only changes the brownfield path.
>
> Read [`ARCHITECTURE.md`](ARCHITECTURE.md) first for the execution flow, and
> [`../CLAUDE.md`](../CLAUDE.md#critical-gotchas) for the load-bearing per-session
> gotchas (heredoc/stdin, Bubble Tea v2, the brownfield-coexistence bullet).

> Status: **implemented and verified end-to-end** on the live brownfield box
> `147.45.79.231` — full `run --assume-yes` (12 OK / 2 SKIP / 0 FAIL, §V verify 16/16),
> survived the A8 upgrade + reboot, external reachability + on-box state re-checked from
> a separate host (see §2 evidence and the per-step table). The greenfield path is
> byte-identical to before.

## 1. Principle

- **Coexistence is automatic, driven by `detect.Facts`** — no new mandatory flags.
- The existing brownfield confirmation gate stays: a blind full `run` on a non-fresh /
  already-hardened box still refuses unless `--assume-yes` (so production is never
  hammered without consent). Once the operator confirms, the steps apply in a
  **service-preserving** way — they read `Facts` and adapt; the engine needs no extra
  switch.
- Read-only commands (`detect`/`verify`/`step`/`audit`) already pass the gate
  (`allowBrownfield=true`) and never mutate, so they were always brownfield-safe.
- Every high-risk step *detects* what exists and *adapts* to preserve it, instead of
  refusing (A1) or applying a blast-radius tweak blind (A5 rp_filter, A6.7 swap,
  A2 AllowGroups).

## 1a. Role-agnostic by design

morgward keeps **NO service whitelist**. It does not know or care what your box "is" —
it preserves the box by **OBSERVATION**, not by recognizing named software:

- every **listening port by number** (`ss -tulpnH` → `ListenPortsTCP/UDP`) — opened as-is;
- **forwarding/routing** (`ip_forward`, docker, WireGuard/OpenVPN, NAT) — left to its
  manager;
- **disk swap** — preserved;
- **existing key users** — added to `sshusers` so they keep access.

Because the signal is the port number (and the manager), it works for **ANY role**. The
list below is **EXAMPLES ONLY — not a whitelist, not an allow-list, not a detection
table**; an unlisted role is treated exactly the same (its ports are observed and kept):

- Kubernetes nodes (API/kubelet/NodePort)
- Docker / Docker Compose stacks
- PaaS: Coolify, Dokku, CapRover
- Automation: n8n
- Streaming/queues: Kafka
- NAS / file servers: Samba, NFS
- Routers / VPN relays / tunnels
- Remote-desktop / remote-demo boxes
- Game servers
- Torrent boxes
- Databases (Postgres, MySQL, Redis, …)

**Proof it is role-agnostic:** on the live box, 6 distinct *arbitrary* services were
preserved purely by their port numbers — `docker-proxy` tcp `8080`, `ncat` tcp
`8443/9099/9092/5678`, WireGuard udp `51820` — none of them recognized by name, all kept
open because they were *listening*.

## 2. What coexistence detects — `detect.Facts`

Discovery runs in `detect.Run` ([`../internal/detect/detect.go`](../internal/detect/detect.go))
and is parsed from the **same** `ss -tulpnH` output already collected for `Listeners`,
plus a few best-effort probes. **All probes are best-effort:** a miss leaves the field
zero/empty and never fails detection.

| Facts field | Type | Meaning / source |
|-------------|------|------------------|
| `Listeners` (existing) | `[]string` | raw `ss -tulpnH` lines for public (non-loopback, non-sshd) sockets |
| `IPForward` (existing) | `bool` | `sysctl -n net.ipv4.ip_forward == 1` |
| `DockerSeen` (existing) | `bool` | `command -v docker` present |
| `Greenfield` (existing) | `bool` | `!IPForward && !DockerSeen && len(Listeners)==0` — **formula unchanged** |
| `ListenPortsTCP` | `[]int` | public TCP listening ports, deduped/sorted, parsed from the `ss` lines (incl. 22; A1 dedups against `cfg.Port`) |
| `ListenPortsUDP` | `[]int` | public UDP listening ports, deduped/sorted |
| `WireguardSeen` | `bool` | `wg show interfaces` non-empty, OR `wg-quick@*`/`openvpn*` active, OR `*.conf` in `/etc/wireguard` |
| `NatRules` | `bool` | `iptables -t nat -S` contains a real `MASQUERADE`, `-j SNAT`, or `-j DNAT` (beyond empty default chains) |
| `Forwarding` | `bool` | convenience = `IPForward \|\| DockerSeen \|\| WireguardSeen \|\| NatRules` — the routing/VPN/docker signal every step keys off |
| `DiskSwap` | `[]string` | active swap devices with `TYPE != zram` (e.g. `/swapfile`), from `swapon --show=NAME,TYPE --noheadings` |
| `SSHKeyUsers` | `[]string` | login users (root + every `/home/*`) with a NON-empty `~/.ssh/authorized_keys` — used by A2 so it doesn't lock out existing key users |
| `FirewallMgr` | `string` | the active firewall manager: `ufw` \| `firewalld` \| `nftables` \| `iptables` \| `none` (`classifyFirewallMgr`, priority order, best-effort). nftables verdict is CONSERVATIVE — needs the unit active, a non-empty `nft` ruleset, AND an effectively-empty `iptables -S` (`iptablesEffectivelyEmpty`); else it falls through to `iptables`. A1 branches on it so morgward never imposes a conflicting second firewall layer — see §7 |
| `ListenServices` | `[]ListenService` | `ListenService{ Proto string; Port int; Process string }` parsed from the same `ss -tulpnH` output (Process via `processFromSS`, or `""`/`(unknown)`), deduped by `proto:port` — the rich role-agnostic "what's found" map for surfacing the box's de-facto role. `ListenPortsTCP/UDP` stay as the ruleset inputs |

These fields are populated even on a greenfield box (where they come out empty), so the
steps branch purely on `Greenfield`/`Forwarding` without re-probing.

Port parsing helper `portFromLocal(local string) (int, bool)`: takes field[4]
(`Local Address:Port`), returns the port after the LAST `:`. Handles `0.0.0.0:8080`,
`[::]:8080`, `192.168.0.1:53`, `[::1]:x` (loopback already filtered out upstream).
Unit-tested in `detect_test.go`.

`buildInventory` (writes `/root/vps-inventory.md`) dumps listeners, docker, wireguard,
forwarding, nat rules, firewall, then appends a `=== coexistence (parsed) ===` block
(`coexistSummary`): `mode` (greenfield/brownfield), `tcp/udp ports kept open`,
`forwarding` (with the `ip_forward`/`docker`/`wireguard`/`nat` breakdown), `disk swap
preserved`, and `ssh key users (added to sshusers)` — so the on-box record states
exactly what the coexistence steps will keep open/preserve. It also renders
`firewall manager: <FirewallMgr>` (with a short note on what A1 does for it,
`fwMgrLabel`) and a `detected services (proto port -> process):` table from
`ListenServices`, so the operator sees the box's de-facto role at a glance.

### Live test-box evidence (`147.45.79.231`) — verified

Brownfield reference applied against. Pre-existing services on the box:

- docker: a `web` container (nginx), `docker-proxy` on `:8080` (v4 + v6), NAT
  (`MASQUERADE` + `DNAT`), `net.ipv4.ip_forward=1`
- WireGuard `wg0`: `udp/51820` (v4 + v6)
- `ncat` listeners on `:8443` and `:9099`
- a `/swapfile` disk swap

Detect result: `ListenPortsTCP ⊇ {8080, 8443, 9099}`, `ListenPortsUDP ⊇ {51820}`,
`WireguardSeen=true`, `NatRules=true`, `Forwarding=true`, `DiskSwap=[/swapfile]`,
`Greenfield=false`.

Step report (per-step `coexist:` lines):

- A1: `coexist: INPUT DROP, SSH :22 + 4 service port(s) kept open, FORWARD preserved, v6 mirrored, persisted` — the count (`countServicePorts`) is the DISTINCT non-SSH ports actually opened: `8080/8443/9099` tcp + `51820` udp = 4
- A5: `sysctl hardening applied (core_pattern=|/bin/false, rp_filter=2), THP=madvise` — `rp_filter=2` because forwarding is active
- A6.7: `ZRAM zstd swap active, swappiness=180, earlyoom=active (disk swap preserved: brownfield)`

**Full `run --assume-yes` result:** 12 OK, 2 SKIP, 0 FAIL; §V verify 16/16 passed;
survived the A8 full-upgrade + reboot. Post-apply state, re-checked from a separate host
**after the reboot** (the proof nothing broke):

- EXTERNAL reachability: tcp `22`, `8080`, `8443`, `9099` ALL still OPEN; `http://:8080`
  returns `HTTP/1.1 200 OK` (the nginx container).
- `iptables -S INPUT`: `-P INPUT DROP` with explicit ACCEPT for dport `22/8080/8443/9099`
  (tcp, ctstate NEW) + `51820` (udp). **`iptables -S FORWARD`: `-P FORWARD DROP` with
  `-A FORWARD -j DOCKER-USER` + `-A FORWARD -j DOCKER-FORWARD` intact** — morgward did
  NOT touch FORWARD; docker manages it and containers still work (external `:8080 → 200`
  post-reboot confirms it). The persisted `rules.v4` keeps docker's FORWARD chains.
- docker `web` container Up, `localhost:8080 → 200`; `wg-quick@wg0`, `fakebot`,
  `fakesite`, `docker` units active; all listeners bound.
- `rp_filter=2`. `swapon --show` lists BOTH `/swapfile` (disk, preserved, prio -1) AND
  `/dev/zram0` (prio 100) — the recommended layered layout.
- A2 user preservation PROVEN: `getent group sshusers` →
  `sshusers:x:1002:vpsadmin,deploy`. The pre-existing `deploy` user (had
  `~/.ssh/authorized_keys`) was auto-added, so `AllowGroups sshusers` does NOT lock it
  out; root is intentionally excluded (not in group, `prohibit-password`).

## 3. Per-step decision table — greenfield vs brownfield

"Brownfield" = `!Facts.Greenfield` (and, for `run`, the operator passed `--assume-yes`).
Steps not listed below behave identically in both modes.

| Step | Greenfield (unchanged) | Brownfield (coexistence) | Driven by |
|------|------------------------|--------------------------|-----------|
| **A1** firewall ([`a1_firewall.go`](../internal/steps/a1_firewall.go)) | `greenfieldRuleset`: INPUT DROP, FORWARD DROP, SSH `:cfg.Port` only; v6 mirror; persist | `coexistRuleset`: INPUT DROP but **also ACCEPT each `ListenPortsTCP` (tcp, ctstate NEW) and `ListenPortsUDP` (udp)**, deduped against `cfg.Port`. **FORWARD policy and chains are left EXACTLY as found — morgward never touches FORWARD on a brownfield box** (docker re-asserts its own `-P FORWARD DROP` + rules on boot; forcing ACCEPT would be overridden or would loosen a router's chosen isolation). The ruleset only APPENDs to INPUT — existing chains/nat **never flushed** (DOCKER/DOCKER-USER + nat untouched); v6 mirrors the same tcp+udp service ports. Same snapshot + 300s `fw-rollback` fail-safe + second-session verify | `ListenPortsTCP/UDP` |
| **A5** kernel ([`a5_kernel.go`](../internal/steps/a5_kernel.go)) | `kernelHardenConf(false)`: `net.ipv4.conf.{all,default}.rp_filter = 1` (strict) | `kernelHardenConf(true)`: `rp_filter = 2` (loose) when `Forwarding` — strict reverse-path silently drops asymmetric-routed return packets and breaks WireGuard/OpenVPN/multi-egress. Rest of the sysctl conf is router-safe, identical on both paths; live rpf-revert fail-safe kept | `Forwarding` |
| **A6.7** memory ([`a67_mem.go`](../internal/steps/a67_mem.go)) | install zram + earlyoom, then `swapoff -a` + comment disk-swap fstab lines (tagged `# morgward-disabled-swap`) | install zram + earlyoom + swappiness, but **keep pre-existing disk swap** (`disableDiskSwap` block gated behind `Greenfield` only). zram default priority 100 > disk swap, so zram is still used first; the zram-high + disk-low layered layout is recommended | `Greenfield` |
| **A2** SSH ([`a2_ssh.go`](../internal/steps/a2_ssh.go)) | `AllowGroups sshusers` admits only sshusers members | `preserveKeyUsers`: BEFORE the `AllowGroups` drop-in takes effect, add every existing key user (non-root) in `SSHKeyUsers` to `sshusers` (`usermod -aG sshusers <u> \|\| true`) so they keep SSH access; **root is intentionally excluded** (stays blocked: not in group, `prohibit-password`). Additive/grant-only — never lockout-capable; crypto + PermitRootLogin logic unchanged. Runs in both A2SSH (legacy) and A2Danger; A2Safe untouched (it never sets AllowGroups) | `SSHKeyUsers` |
| **verify / tweaks** ([`../internal/verify/`](../internal/verify/), [`../internal/tweaks/`](../internal/tweaks/)) | assert FORWARD DROP, `rp_filter == 1` | coexist-aware: accept `rp_filter == 2` when `Forwarding`, and do **not** assert a FORWARD policy on a brownfield box (it is docker/operator-managed), so a correctly-coexisting box does not report false failures. All other matrix rows intact | `Forwarding` |

Notes:
- A1's skip-if guard (INPUT DROP + `cfg.Port` open + persisted) still fires correctly on
  a coexist ruleset — a re-run idempotently re-opens the same service ports.
- A1 always keeps the snapshot + 300s `fw-rollback` timer that iptables-saves the
  EXISTING ruleset and restores it on timeout; this already protects docker/wg rules
  during the apply window.
- A2's F04 precondition guard (`assertAdminLoginable`) and F12 handoff-before-lock
  ordering are unchanged by coexistence.

## 4. How to run on a non-fresh box

Coexistence needs **no new flag** — it is automatic once the brownfield gate is passed.
The existing `--assume-yes` is the consent switch.

```sh
# 1. Inspect first (read-only — always safe, never mutates):
morgward detect --host 147.45.79.231 --user root --password XXX
#   → writes /root/vps-inventory.md (listeners, docker, wg, nat, forwarding, ports kept)

# 2. Apply in coexistence mode (full run on the brownfield box):
morgward run --host 147.45.79.231 --user root --password XXX --assume-yes
#   → BROWNFIELD DETECTED banner, then steps apply service-preserving (ports kept,
#     FORWARD policy left untouched, disk swap kept, existing key users added to sshusers)
```

Gate behavior ([`../internal/engine/engine.go`](../internal/engine/engine.go) `prepare`):

- Without `--assume-yes`, a brownfield `run` prints **BROWNFIELD DETECTED** + the
  listeners and **refuses**: `re-run with --assume-yes to apply in COEXISTENCE mode
  (detected service ports/forwarding/swap are preserved); see /root/vps-inventory.md`
  (error: `brownfield box: refusing to run Phase A without confirmation`).
- With `--assume-yes`, it logs `proceeding in coexistence mode (existing services
  preserved) — see /root/vps-inventory.md` and the steps coexist automatically.
- `step <IDs>` already allows brownfield (`allowBrownfield=true`) — use it to apply a
  single block on a production box without the full run.
- An **already-hardened** box (≥2 hardening markers) hits the separate **ALREADY
  HARDENED** gate first; use `verify`/`step`, or `--assume-yes` to force a full re-run.

A `run` is crypto-only and keeps a console-password fallback; it never locks you
out. The opt-in access lockdown (key-only, root blocked) lives in the `A2-danger`
step / TUI security menu. Coexistence is orthogonal to that.

## 5. Residual manual notes

Coexistence preserves running services, but a few steps still apply universal defaults
the operator may want to review on a customized box. None are lockout-capable.

- **A6.5 DNS** ([`a65_dns.go`](../internal/steps/a65_dns.go)) — if `systemd-resolved`
  is **not** active (you run a custom resolver: unbound, dnsmasq, Pi-hole, a docker
  DNS), the step **skips** (`StatusSkip "systemd-resolved not active"`) and leaves your
  resolver alone. If resolved IS active, A6.5 re-pins `DNS=` to the two fastest measured
  public upstreams via a `99-morgward-dns.conf` drop-in (DoT opportunistic, DNSSEC
  allow-downgrade) and self-reverts if resolution breaks. To keep a hand-tuned
  `resolved.conf`, run `step` without A6.5, or remove
  `/etc/systemd/resolved.conf.d/99-morgward-dns.conf` afterward.
- **A7 cleanup** ([`a7_cleanup.go`](../internal/steps/a7_cleanup.go)) — **IRREVERSIBLE
  purge.** It removes: `apport apport-symptoms whoopsie sysstat packagekit` (+ `fwupd`
  on a guest/`Virt != none`), and `multipath-tools` (+`-boot`) **unless** root is on
  multipath (gated). It guards `cloud-init netplan.io software-properties-common` from
  autoremove, then `apt-get autoremove --purge`. Every removed package is logged to
  `/root/vps-purged-packages.log`. On a box where you rely on any of those packages,
  exclude A7 (run `step` for the blocks you want instead of full `run`).
- **A1 FORWARD / custom INPUT rules** — on a brownfield box morgward **never changes the
  FORWARD policy or chains**: VPN/container forwarding is left exactly as the
  operator/docker configured it (on the docker box it stays `-P FORWARD DROP`, managed
  by docker). A1 also does **not** import arbitrary custom INPUT rules you added by hand
  — it only opens the detected listeners. If you maintain bespoke filter/FORWARD rules
  beyond the detected listeners, check `iptables -S` / `ip6tables -S` after the run.

## 6. Verification it worked

After a coexistence run, confirm services survived:

```sh
morgward verify --host 147.45.79.231 --user vpsadmin --key ./id_ed25519_...
#   (after A2 the executor switches to the admin user; root SSH is closed only by the opt-in A2-danger lockdown)
```

On the box: detected listeners still bound (`ss -tulpnH`), docker/wg traffic still
forwards (`iptables -S FORWARD` is **unchanged from before the run** — morgward leaves
FORWARD to docker/the operator; containers/VPN keep working), `sysctl
net.ipv4.conf.all.rp_filter` shows `2` when routing, `swapon --show` lists both zram
**and** the preserved disk swap, and existing key users are in `sshusers`
(`getent group sshusers` / `id -nG <user>`).

## 7. Firewall managers

People manage host ports in different ways. A1 **detects the active manager**
(`Facts.FirewallMgr`) and adapts so morgward **never imposes a conflicting second
firewall layer** — it works *through* the manager in charge, or defers to it. The branch
is taken on a brownfield box only; greenfield always takes the iptables build.

| `FirewallMgr` | Detected when | A1 brownfield behavior | Result |
|---------------|---------------|------------------------|--------|
| `ufw` | `ufw status` first line == `Status: active` | `ufwAllowScript`: `ufw allow <sshPort>/tcp` + `ufw allow <port>/tcp` / `<port>/udp` for every detected port (idempotent, additive — never deny/enable/disable, default policy untouched); `freshLogin` verify | `StatusOK` `ufw-managed: SSH :N + M service port(s) allowed via ufw (firewall left under ufw control)`; skip-if all already allowed → `StatusSkip` `ufw-managed: SSH :N + all detected service ports already allowed in ufw` |
| `firewalld` | `systemctl is-active firewalld` == active | **defers — changes nothing** (no untested `firewall-cmd` mutations) | `StatusSkip` `firewalld manages the firewall — morgward left it untouched; ensure SSH :N and your service ports stay allowed there (see docs/BROWNFIELD.md §firewall-managers)` |
| `nftables` | `nftables` unit active AND a non-empty `nft` ruleset AND `iptables -S` effectively empty (`iptablesEffectivelyEmpty`) — CONSERVATIVE; if unsure it falls through to `iptables` | **defers — changes nothing** (no untested `nft` mutations) | `StatusSkip` (same message shape as firewalld, with `nftables`) |
| `iptables` | `iptables -S` shows a non-default rule OR iptables-persistent present | the proven round-1 `coexistRuleset`: INPUT DROP + open SSH + all detected ports, FORWARD preserved, v6 mirror, 300s `fw-rollback`, persist | `StatusOK` `coexist: INPUT DROP, SSH :N + M service port(s) kept open, FORWARD preserved, …` |
| `none` | none of the above | same as `iptables` (round-1 coexist build) | `StatusOK` (coexist) |

Why: `firewalld`/`nftables`-native boxes own their ruleset with their own tooling;
clobbering it with raw `iptables` policy would create two competing layers and could
lock the box out. `StatusSkip` (not an error) lets a full `run` continue to the other
hardening steps while the firewall stays the operator's responsibility under their
chosen manager. The nftables classifier deliberately prefers a false negative (treat as
`iptables`) over a false positive — wrongly deferring on a docker/iptables box would
leave it unhardened. **Lockout-safety holds on every path:** the ufw path is allow-only
(never deny/enable), the firewalld/nftables path changes nothing, the iptables path is
the proven round-1 build — no path sets a default-deny without first opening SSH + the
detected ports.

### ufw path — live-verified (`147.45.79.231`)

Setup: installed ufw, `ufw allow 22/tcp`, `ufw --force enable` (SSH-only — service ports
`9092`/`5678` were CLOSED by ufw, as a contrast). Then `morgward step A1` as `vpsadmin`:

- A1 return: `ufw-managed: SSH :22 + 6 service port(s) allowed via ufw (firewall left under ufw control)`.
- After: `ufw status` lists ALLOW for `22/5678/8080/8443/9092/9099` tcp + `51820` udp, on
  **both v4 and v6**; the previously-closed `9092`/`5678` went CLOSED → OPEN. External:
  all 6 ports OPEN.
- morgward imposed **no policy of its own** — the `-P INPUT DROP` present is **ufw's own**
  (`ufw-user-input` chain); morgward touched no raw iptables and no
  netfilter-persistent. ufw stays in control.
- Surfacing live-confirmed in `/root/vps-inventory.md` `=== coexistence (parsed) ===`
  (exactly once): `firewall manager: ufw (A1 allows SSH + detected ports via ufw; no
  policy change)`; `tcp ports kept open [22 5678 8080 8443 9092 9099]`; `udp [51820]`;
  `forwarding: true`; `disk swap preserved [/swapfile /dev/zram0]`; `ssh key users
  [root deploy vpsadmin]`; and the `detected services (proto port -> process)` table:
  `tcp 22->sshd, 5678->ncat, 8080->docker-proxy, 8443->ncat, 9092->ncat, 9099->ncat,
  udp 51820->(unknown)`.

## 8. Dynamic / ephemeral ports — honest boundary

The **iptables** coexist build opens the ports that are **LISTENING at apply time**. A
service that opens a port **later** is NOT auto-opened while INPUT policy is DROP:

- Kubernetes NodePort churn (`30000–32767`)
- torrent clients picking a fresh random port
- passive-FTP data-port ranges
- anything that binds on demand after the run

Remedies (iptables path only):

- re-run `morgward step A1` after the service is up — it re-snapshots the now-listening
  ports and re-opens them, then re-persists; OR
- add an explicit rule yourself and persist it, e.g.
  `iptables -A INPUT -p tcp --dport 30000:32767 -m conntrack --ctstate NEW -j ACCEPT`
  then `netfilter-persistent save`.

Under **ufw** or **firewalld/nftables** this is moot — the operator's own manager governs
which ports are allowed, so add the range there (`ufw allow 30000:32767/tcp`, etc.).

## 9. Proposed / future — brownfield VPN MTU/MSS coexistence (NOT YET IMPLEMENTED)

> Status: **proposal only — no code exists for this yet.** Documented here so the gap is
> on record and the design is fixed before anyone wires it. Don't cite it as shipped
> behavior; a `run` today does **none** of the below.

**Problem.** On a brownfield box running a **routed** VPN (WireGuard / AmneziaWG /
OpenVPN that forwards a client subnet → `MASQUERADE` → uplink), forwarded **client** TCP
flows hit a **PMTUD blackhole** whenever ICMP `frag-needed` (type 3 code 4) is filtered
somewhere on the path — common on RU ISP links and behind obfuscated transports. Symptom
is the classic intermittent "VPN works then doesn't": small packets (DNS lookups, page
open, the handshake) pass, large packets (TLS records, downloads, video) are silently
dropped, so pages hang or half-load. Live diagnosis on an AmneziaWG box bears this out —
`IcmpOutDestUnreachs ≈ 105k` vs `IcmpInDestUnreachs ≈ 1.2k` (PMTUD **return** path
broken), `IpFragCreates ≈ 740k`, tunnel iface `awg0` (MTU 1420) `TX dropped ≈ 22k`, and
**zero** MSS-clamp present in the host nft/iptables or in any container.

**Why morgward does NOT cover it today (the gap, code-verified):**
- A4 ([`a4_network.go`](../internal/steps/a4_network.go)) sets only
  `net.ipv4.tcp_mtu_probing = 1` in `netTuneConf`. That is PLPMTUD for
  **host-terminated** TCP sockets; it does **nothing** for **forwarded** (router-role)
  client flows, and the outer WireGuard layer is **UDP** (`tcp_mtu_probing` is TCP-only).
  A4's own header comment states MTU tuning is **SKIPPED** in universal Phase A.
- **No TCP MSS clamping exists anywhere in `internal/`** (`grep -i mss|clamp|TCPMSS` →
  none). Nothing on the forward path adjusts MSS.
- A1 deliberately leaves the **FORWARD policy + docker/nat chains UNTOUCHED** on
  brownfield (§1, §5) — so by design nothing morgward does today fixes the forwarded path.

**Proposed step (working name "A4.5" — exact ID is the author's call):**
brownfield-only, detection-driven, role-agnostic (no service whitelist),
greenfield path **byte-identical / unchanged**. Gated on `!Greenfield` and a detected
routed-VPN signal (`Facts.Forwarding` + a VPN tunnel iface — `detect.Facts` has
`WireguardSeen`/`Forwarding` today but **no tunnel-iface field yet**; this step would add
detection of `awg0`/`wg0`/`tun0`).
- **(a) zero-downtime — default, hot-applied, no reconnect:** TCP **MSS clamp** on the
  forward path for tunnelled traffic —
  `iptables -t mangle -A FORWARD -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu`
  (or `--set-mss <tunnel_mtu-40>`). On a **docker** box insert into the **`DOCKER-USER`**
  chain — the docker-sanctioned operator chain that survives `docker` reloads and that
  morgward must **never flush** (consistent with the existing "never flush docker chains"
  rule, §1/§5). Idempotent / additive, like the A1 allow-only paths.
- **(b) opt-in — needs an iface restart, so NOT zero-downtime:** lower the detected VPN
  tunnel iface MTU (`awg0`/`wg0`/`tun0`) to a safe **1280**, and surface a recommendation
  that **client** configs set the same MTU (WireGuard does **not** push MTU to clients).
  An MTU change bounces the iface → gate behind an explicit opt-in and warn; never fold it
  into the default zero-downtime path.

Default behavior is the **(a) MSS clamp only**; **(b)** is opt-in. Both are
**lockout-safe** (forward-path / tunnel-iface only — neither touches INPUT or sshd), so
neither can abort a `run` or trip ssh-revert.
