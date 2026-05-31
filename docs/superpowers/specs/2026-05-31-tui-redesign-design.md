# TUI Redesign — Design Specification

- **Date:** 2026-05-31
- **Status:** approved-pending-user-review
- **Author:** Morgward project
- **Stage:** brainstorming-stage design spec (NOT an implementation plan, NOT code)

## Scope

This document specifies a redesign of the `morgward` interactive TUI (`internal/tui/tui.go`,
Bubble Tea v2). It covers the screen flow, per-screen layout, self-update wiring, a
Security-vs-Tweaks taxonomy, an A2 (SSH) engine split, the apply flows, a richer wiki, and
the cross-cutting constraints. It deliberately ends with a decomposition into implementation
phases (a single spec was chosen; the actual plans are written per-phase at the writing-plans
stage) and an explicit out-of-scope section.

It does **not** include code, exact diffs, or a step-by-step implementation plan. Engine symbol
names are cited as they exist today so the later plans bind to real APIs, not guesses.

---

## Goal / Principle (load-bearing)

- **No `soft`/`strict` mode anywhere.** The mode concept is removed from the user-facing UI and
  is no longer a mandatory choice. (See "Out of scope / deliberate cuts" for what this drops.)
- **By default the app NEVER touches root login, the SSH password, or access policy.** Hardening
  and tweaks leave login exactly as the hosting image left it. Locking access down is **opt-in
  only**, in a separate Security menu, with explicit confirmation plus key display.
- The three current actions `run` / `detect` / `verify` (RU: запуск / разведка / анализ) are
  **removed from the user-facing UI**. They remain engine tokens / under-the-hood only.
  Concretely: remove `kOptRun` / `kOptDetect` / `kOptVerify` from the **form** i18n
  (`internal/tui/i18n.go`, currently lines 55–57 / 217–219 / 344–346), keep `stepTitles` and the
  engine command tokens.

---

## Screen flow (state diagram)

Phases extend the existing Bubble Tea v2 `phase` enum (`internal/tui/tui.go:59`), they do not
replace the framework:

```
Existing: phaseForm  phaseRun  phaseSummary  phaseWiki  phaseKey  phaseMatrix

  ① Landing          = phaseForm     (repurposed)
  ② Dashboard        = phaseDashboard (NEW)
  ③ Security         = phaseSecurity  (NEW)
  ④ Catalog          = phaseCatalog   (NEW)
  ⑤ Detail           = phaseWiki      (extended FixDoc)
     Apply           = phaseRun       (unchanged streaming)
     Summary         = phaseSummary / phaseMatrix
```

```
              (pre-connect, no footer)
        ┌──────────────── ① Landing (phaseForm) ───────────────┐
        │   · Host + Пароль + [Подключиться]                    │
        │   · ▸ Дополнительно (Порт/Пользователь/SSH-ключ)      │
        │   · update strip (+ Обновить ⬇ when available)        │
        │   · "Что настраивает программа ▸"  ─────────────┐     │
        └───────────────┬───────────────────────────────┬┘     │
                        │ Подключиться (Audit)           │      │
                        v                                │      │
        ┌──── ② Dashboard (phaseDashboard) ────┐         │      │
        │  live audit · server card · 3 buttons│         │      │
        │  [footer pinned]                     │         │      │
        └──┬─────────────┬──────────────┬──────┘         │      │
           │ Применить   │ Безопасность │ Каталог        │      │
           │ твики       │   ▸          │ твиков         │      │
           v             v              v                v      │
       Apply        ③ Security      ④ Catalog (phaseCatalog) <──┘
     (phaseRun)    (phaseSecurity)   pre-connect = docs only
        │              │   │         post-connect = + status column
        │              │   │              │ select row
        v              │   │              v
     Summary           │   │         ⑤ Detail (phaseWiki, extended)
  (phaseSummary/        │   │              │ esc → back to caller
   phaseMatrix)         │   │
                        │   └── danger → phaseKey (shows generated key) → Apply
                        └────── safe buttons → Apply (phaseRun) → Summary
```

- Update click on Landing is a terminal action: it quits the alt-screen and hands an update
  intent back to `main()` (see Self-update).
- `phaseWiki` (Detail) is reachable from Dashboard's audit list, from Catalog rows, and from
  Summary's clickable fix list; it returns (esc) to whichever phase invoked it (the existing
  `m.wikiReturn` mechanism, `internal/tui/tui.go:369`).

---

## ① Landing (`phaseForm`, repurposed)

### Field set

- **Novice default:** framed **Хост** + **Пароль** only, plus **[Подключиться]**. The user
  defaults to `root`, port `22`, key empty (the existing `newModel()` defaults).
- **"▸ Дополнительно"** — a collapsible disclosure that reveals framed **Порт / Пользователь /
  SSH-ключ**. Backed by a plain `bool advancedOpen` in the model.
- **"Сохранять лог в файл: [нет] да"** toggle stays — on Landing, in the lower-right cluster as a
  session preference (single bool, already in the model).
- **"Что настраивает программа ▸"** link → Catalog (reachable pre-connect = docs only, no status
  column).
- **Update strip** at the top of the card (see Self-update for the four states).

### Framed inputs

- Each input is a `lipgloss.RoundedBorder` box, ~3 screen rows tall (border top + value +
  border bottom).
- Unfocused = dim border (240); focused = accent border (57/213) + bold label (213).
- The whole 3-row box is a click/focus target.
- Password keeps `EchoPassword`.
- **Consequence (call out as the main layout-math task):** every Y-offset in `formRows` /
  `formBodyTopRow` / mouse hit-tests must be recomputed because inputs now span **3 rows, not 1**.

### Mockup — Landing (up-to-date)

```
╭──────────────────────────────────────────────────────────────────────────╮
│                                                       RU | EN              │
│   ┌─ Morgward v0.1.0 ───────────────────────────────────────────────┐     │
│   │  VPS guardian · защита свежего Ubuntu VPS                        │     │
│   │  Обновления: ✓ установлена последняя версия                     │     │
│   └─────────────────────────────────────────────────────────────────┘     │
│   Хост                                                                     │
│   ┌──────────────────────────────────────────────────────────────────┐    │
│   │ 1.2.3.4                                                            │    │
│   └──────────────────────────────────────────────────────────────────┘    │
│   Пароль                                                                   │
│   ┌──────────────────────────────────────────────────────────────────┐    │
│   │ ••••••••••                                                         │    │
│   └──────────────────────────────────────────────────────────────────┘    │
│   ▸ Дополнительно (порт · пользователь · SSH-ключ)                         │
│      ╭─────────────────╮     Сохранять лог в файл:  [ нет ] да             │
│      │   Подключиться  │     Что настраивает программа ▸                   │
│      ╰─────────────────╯                                                   │
╰──────────────────────────────────────────────────────────────────────────╯
```

### Mockup — Landing (update-available strip variant)

```
│   │  Обновления: v0.2.1 доступна   ╭──────────────╮                  │     │
│   │                                │  Обновить ⬇  │                  │     │
│   │                                ╰──────────────╯                  │     │
```

Landing is **pre-connect → no footer** (correct, by design).

---

## ② Dashboard (`phaseDashboard`, NEW)

Reached on connect. Shows the server card, a **live audit** of tweaks (applied vs. can-apply),
and three primary actions. Footer pinned.

### Mockup — Dashboard

```
╭──────────────────────────────────────────────────────────────────────────╮
│   ┌─ Сервер: 1.2.3.4 ───────────────────────────────────────────────┐     │
│   │  ОС  Ubuntu 24.04.1   Ядро 6.8.0-45   Память 1.9G  Диск 8/24G   │     │
│   └─────────────────────────────────────────────────────────────────┘     │
│   Анализ твиков ⠹  применено 18 из 47 · можно применить 29                 │
│   ┌────────────────────────────────────────────────────────────────┐      │
│   │ ✓ BBR контроль перегрузки         • zram-своп активен          │      │
│   │ ✓ fq очередь по умолчанию         • earlyoom активен           │      │
│   └────────────────────────────────────────────────────────────────┘      │
│   ╭──────────────────╮  ╭────────────────╮  ╭──────────────────╮          │
│   │ Применить твики  │  │ Безопасность ▸ │  │ Каталог твиков   │          │
│   ╰──────────────────╯  ╰────────────────╯  ╰──────────────────╯          │
├────────────────────────────────────────────────────────────────────────┤ │
│  монитор  CPU 3%  RAM 21%  ↑0.1 ↓0.2 MB/s  ping 0.4ms                      │
╰──────────────────────────────────────────────────────────────────────────╯
```

- "Применить твики" → Apply over the Tweaks-bucket IDs.
- "Безопасность ▸" → Security (③).
- "Каталог твиков" → Catalog (④, post-connect with the status column).
- Audit rows are selectable → Detail (⑤).

---

## ③ Security (`phaseSecurity`, NEW)

The **only** screen that can change account / access / SSH-login state. Footer pinned. Split
into a SAFE cluster (never locks anyone out) and a clearly-marked DANGER zone.

### Mockup — Security

```
╭──────────────────────────────────────────────────────────────────────────╮
│   ┌─ Безопасность и доступ ──────────────────────────────────────────┐    │
│   │   Вход root по SSH ......... разрешён по паролю                  │    │
│   │   Вход только по ключу ..... нет                                 │    │
│   │   Админ-пользователь ....... отсутствует                        │    │
│   └──────────────────────────────────────────────────────────────────┘    │
│   Безопасно (вход не меняется):                                            │
│   ╭──────────────────╮   ╭────────────────────────────────────╮          │
│   │  Создать админа  │   │  Усилить SSH-крипто + добавить ключ │          │
│   ╰──────────────────╯   ╰────────────────────────────────────╯          │
│   ⚠ Опасная зона (можно потерять доступ):                                 │
│   ╭──────────────────────────────────────────────────────────────────╮   │
│   │  Вход только по ключу · заблокировать пароль root  (покажем ключ) │   │
│   ╰──────────────────────────────────────────────────────────────────╯   │
├────────────────────────────────────────────────────────────────────────┤ │
│  монитор  CPU 3%  RAM 21%  ...                                            │
╰──────────────────────────────────────────────────────────────────────────╯
```

The status card lines reflect the live audit (root SSH policy, key-only state, admin presence).

---

## ④ Catalog (`phaseCatalog`, NEW)

Reachable **pre- AND post-connect** (the same `phaseCatalog`). Pre-connect = docs only;
post-connect = adds a **✓ применено / • можно** status column sourced from the live audit.
Footer is shown only post-connect.

### Mockup — Catalog

```
╭──────────────────────────────────────────────────────────────────────────╮
│   ┌─ Каталог твиков — что настраивает Morgward ─────────────────────┐     │
│   └─────────────────────────────────────────────────────────────────┘     │
│    Сеть и пропускная способность                                          │
│      › Сетевая оптимизация (BBR, буферы)            A4    [✓ применено]   │
│      › Планировщик ввода-вывода                     A4    [• можно]       │
│    Память                                                                  │
│      › Сжатый своп ZRAM + earlyoom                  A6.7  [• можно]       │
│    Ядро и обслуживание                                                     │
│      › Усиление ядра (sysctl)                       A5    [• можно]       │
│      › Защита DNS (DoT/DNSSEC)                       A6.5  [• можно]       │
│   ⓘ Безопасность (SSH, аккаунты) — на отдельном экране.                    │
├────────────────────────────────────────────────────────────────────────┤ │
│  монитор … (только пост-коннект; пре-коннект футера нет)                   │
╰──────────────────────────────────────────────────────────────────────────╯
```

Rows select into Detail (⑤). Catalog explicitly notes that Security (SSH, accounts) lives on a
separate screen.

---

## ⑤ Detail (`phaseWiki`, extended `FixDoc`)

A single fix's rich page. See Rich-wiki design for the structure. Footer pinned (post-connect).

### Mockup — Detail

```
╭──────────────────────────────────────────────────────────────────────────╮
│   ┌─ A4 · Сетевая оптимизация (BBR) ────────────────────────────────┐     │
│   ЧТО ДЕЛАЕТ   Включает BBR + fq, увеличивает буферы сокетов (sysctl).     │
│   ЗАЧЕМ        Держит канал заполненным без раздувания очередей.           │
│   БЕЗ ЭТОГО    Скорость упирается в консервативный CUBIC.                  │
│   ЧТО МЕНЯЕТСЯ  /etc/sysctl.d/99-bbr.conf, 99-net-tune.conf; модуль bbr.   │
│   КАК ОТКАТИТЬ  Удалить файлы + sysctl --system (автоотката нет).          │
│   Статус: ✓ применено                                                      │
├────────────────────────────────────────────────────────────────────────┤ │
│  монитор … (пост-коннект)                                                  │
╰──────────────────────────────────────────────────────────────────────────╯
```

---

## Monitor footer — hard requirement

- The monitor footer (bottom framed box with CPU/RAM/net/ping) must be **PINNED and VISIBLE on
  EVERY post-connect screen** (Dashboard, Security, Catalog, Detail, Summary, Run) and must
  **NEVER disappear or lose its frame** while the session is connected.
- This also fixes a reported bug: **the footer went missing when connecting as an existing user
  with a PASSWORD** (the audit/verify path, no key generation).
- **Root cause to fix:** the monitor must connect/render regardless of whether auth was by key
  or password (it currently fails the password path). Today `monitor` dials its own SSH session
  (`sshx.Client` is not concurrency-safe), and the connect handoff is wired via
  `notifyConnect` / `Hooks.OnConnect` (`internal/engine/engine.go:303`) which today fires only
  on the key-bootstrap branch with `KeyGenerated: true`; the password path's `OnConnect` must
  carry working credentials so the monitor can dial.
- Landing (pre-connect) has **no footer** — correct.

---

## Self-update design

- **Decided library:** `github.com/creativeprojects/go-selfupdate` v1.5.2 (MIT, wraps
  `minio/selfupdate`).
- **API used:**
  - `NewUpdater(Config{Validator: &ChecksumValidator{UniqueFilename: "checksums.txt"}})`
  - `DetectLatest(ctx, ParseSlug("UberMorgott/morgward")) -> (*Release, found, err)`
  - `UpdateSelf(ctx, currentVersion, slug)`
  - No relaunch is provided by the library.
- **Check on launch** via a one-shot `tea.Cmd` in `Init()` returning
  `updateCheckMsg{found, ver, err}`.
- **Model stores ONLY plain copyable fields** (value-copy invariant):
  - `updateState int` (`updChecking | updCurrent | updAvailable | updErr`)
  - `updateVer string`
  - `wantUpdate bool`
  - **NO** `*Release` / `*Updater` in the model.
- **Strip states** (top of Landing):
  - checking → "Обновления: проверка… ⠋"
  - up-to-date → "Обновления: ✓ установлена последняя версия"
  - available → "Обновления: vX.Y доступна" + a focusable/clickable "Обновить ⬇" button
  - error → "не удалось проверить (офлайн)" (non-blocking)
- `found == false` (TODAY: zero releases) is treated as **up-to-date** → the button **never
  renders today** (mechanism wired but dormant; no fake error).
- **Update flow:**
  1. click → `m.wantUpdate = true`; return `m, tea.Quit` (the alt-screen MUST tear down before
     `UpdateSelf`).
  2. `tui.Run()` signature changes to return an update intent (e.g.
     `Result{ DoUpdate bool; TargetVer string }`) — today `tui.Run()` returns only `error`
     (`internal/tui/tui.go:2765`), and is the only thing `main()` checks
     (`cmd/morgward/main.go:94`).
  3. `main()` then: `NewUpdater` → `DetectLatest` → `UpdateSelf(version.Version, slug)` → exec
     `os.Executable()` with `os.Args` → `os.Exit(0)`.
  4. **Windows:** `minio` renames the running exe to `.old` (hidden), writes `.new`, and swaps;
     the `.old` cannot be deleted in the same process → **best-effort
     `os.Remove("<exe>.old")` in `Init()` on the NEXT launch**, ignore the error.
  5. Print exactly one stdout line "Обновление до vX.Y… перезапуск." between quit and relaunch.

---

## Security vs Tweaks taxonomy (FINAL — user-decided)

The **Security / Accounts** menu contains **ONLY** account + access/SSH-login items. Everything
else is a **Tweak** (Dashboard "Применить твики", not the Security menu).

| Item | ID | Bucket | Touches login/access? | Confirm + key display? |
|------|----|--------|----------------------|------------------------|
| Create admin user (`vpsadmin`) + `sshusers` group + install key | PRE | **Security** (SAFE) | No (adds an account, removes nothing) | No |
| SSH crypto only + install admin key | A2-safe | **Security** (SAFE) | No | No |
| Key-only + block root password | A2-danger (+A2.5) | **Security** (DANGER) | **Yes** — can lose access | **Yes** (shows generated key) |
| cloud-init neutralization | A2.5 | **Security** (tied to A2 danger) | Protects the SSH/password policy from reboot reset | with A2-danger |
| Firewall | A1 | Tweaks | No | No |
| fail2ban | A3 | Tweaks | No | No |
| Network / BBR | A4 | Tweaks | No | No |
| Kernel sysctl harden | A5 | Tweaks | No | No |
| Maintenance | A6 | Tweaks | No | No |
| DNS (DoT/DNSSEC) | A6.5 | Tweaks | No | No |
| zram / earlyoom | A6.7 | Tweaks | No | No |
| Cleanup | A7 | Tweaks | No | No |
| Full upgrade + reboot | A8 | Tweaks | No | **Apply-tweaks confirm must warn** (see below) |
| unattended-upgrades | A9 | Tweaks | No | No |
| auditd / login-notify / drop-log | A10 | Tweaks | No | No |

- **A8 warning (mandatory):** the Apply-tweaks confirm must say "включает полное обновление и
  перезагрузку — несколько минут".
- **A10 scope cut:** with no strict mode, the former **strict-only** OS-hardening extras in A10
  (kernel module blacklist, `/dev/shm` mount options) are **DROPPED** from the product. This is
  a deliberate cut; it can return later as an opt-in danger-zone item. (See Out of scope.)

---

## A2 split detail (engine)

`A2SSH` (`internal/steps/a2_ssh.go`) is split in the engine into two halves. Today it is one step
keyed on `strict := ctx.Cfg.Mode == config.ModeStrict`; the split replaces the mode branch with
two explicit, separately-runnable halves.

### A2-safe (default, never locks anyone out)

- Crypto only: `KexAlgorithms` / `Ciphers` / `MACs` / `HostKeyAlgorithms`, `MaxAuthTries` /
  `LoginGraceTime`, remove the weak ECDSA host key, moduli filter.
- **PLUS** install the admin key.
- **Does NOT** write `PermitRootLogin`, **does NOT** write `AllowGroups`, **does NOT** touch
  `PasswordAuthentication`, **does NOT** lock the root password.
- (Today these are exactly the tokens emitted unconditionally in `build99` minus
  `PermitRootLogin`/`AllowGroups`, plus `conf00(false)` which writes
  `PasswordAuthentication yes` — A2-safe must instead write **neither** value, leaving the image
  default untouched.)

### A2-danger (opt-in only, "опасная зона", explicit confirm + shows generated key)

- `AllowGroups sshusers` + `PermitRootLogin no` + `PasswordAuthentication no` (key-only) +
  `passwd -l root`.
- Keeps the existing safety machinery: the `sshd -t` syntax gate, the `ssh-revert`
  `systemd-run --on-active=300` fail-safe timer, and the second-session key-only verify as the
  admin user (`freshLogin`) before disarming the timer / locking root.
- Shows the generated key via `phaseKey`.

### A2.5 cloud-init neutralization

- Lives with Security because it exists solely to protect the SSH/password policy from being
  reset on reboot — tie it to A2 (danger).

---

## Apply flows

Reuse the existing engine subset runner `engine.RunSteps(cfg, log, ids, h)` (it exists,
`internal/engine/engine.go:367`). It runs the named IDs in canonical order via `selectSteps`,
with `allowBrownfield=true`.

- **"Применить твики"** (Dashboard) = `RunSteps` over the **Tweaks-bucket IDs**.
- **"Создать админа"** (Security, safe) = `RunSteps(["PRE"])`.
- **"Усилить SSH-крипто + добавить ключ"** (Security, safe) = `RunSteps(["PRE","A2-safe"])` (the
  safe half of A2).
- **"Опасная зона: вход только по ключу + блок root"** (Security) = the **danger half of A2**
  (+A2.5), explicit confirm, shows key (`phaseKey`).
- **NEW `engine.Audit(cfg, h)`** (lightweight): dial → key-bootstrap (or password) →
  `detect.Run` → `tweaks.Run` → stream `Results` to the Dashboard live audit.
  - Today `tweaks.Run` (`internal/tweaks/tweaks.go:225`) runs only inside the full `run` / `verify`
    path (`VerifyOnly`, `engine.go:405`). `engine.Audit` is a **REQUIRED new engine entrypoint**.
  - The "parallel / multi-threaded" audit is **COSMETIC ONLY** — `tweaks.Run` already does ONE
    sudo round-trip (`sshx.Client` is not concurrency-safe); stream the **parsing** of that
    single batch incrementally. **Do NOT claim real multi-session probing.**

---

## Rich-wiki design

Extend `wiki.FixDoc` (`internal/wiki/wiki.go:13`, currently `Title` / `What` / `Why` /
`RiskWithout`) by **adding** `OnBox` and `Revert`. Detail screen structure:

```
ЧТО ДЕЛАЕТ        (What)        ┐
ЗАЧЕМ             (Why)         ├ first three — for novices
БЕЗ ЭТОГО         (RiskWithout) ┘
ЧТО МЕНЯЕТСЯ НА СЕРВЕРЕ  (OnBox)  ┐ last two — for pros
КАК ОТКАТИТЬ      (Revert)        ┘
+ live Статус on the detail screen
```

- **Honest:** there is **NO auto per-step rollback**. `Revert` is **MANUAL instructions only**.
  (A1 has a connection-loss fail-safe timer, not a user rollback button; A2-danger has the
  ~300s `ssh-revert` safety on a failed login verify, not a rollback button.)
- `wiki_test.go` `TestEveryStepHasDoc` (`internal/wiki/wiki_test.go:8`) must be **extended** to
  assert the **two new fields non-empty in BOTH langs**.
- RU primary + **EN parity required** for all entries.

### Sample entry — A4 (Сетевая оптимизация / BBR), verbatim RU

> A4 — Сетевая оптимизация (BBR): ЧТО ДЕЛАЕТ — включает BBR + планировщик fq, увеличивает буферы
> сокетов/очередей через sysctl. ЗАЧЕМ — BBR измеряет реальную пропускную способность и держит
> канал заполненным без раздувания очередей: выше фактическая скорость, стабильнее пинг под
> нагрузкой. БЕЗ ЭТОГО — упор в консервативный CUBIC и мелкие буферы, на дальних маршрутах потеря
> в разы. ЧТО МЕНЯЕТСЯ — /etc/sysctl.d/99-bbr.conf (tcp_congestion_control=bbr,
> default_qdisc=fq) и 99-net-tune.conf (буферы); модуль tcp_bbr; переживает перезагрузку. КАК
> ОТКАТИТЬ — удалить оба файла + sysctl --system; модуль выгрузится после перезагрузки;
> автоотката в программе нет.

### Sample entry — A2 (Усиление SSH / доступ), verbatim RU

> A2 — Усиление SSH (доступ): ЧТО ДЕЛАЕТ — безопасная часть: только современная стойкая
> криптография (KEX/шифры/MAC), удаление слабого ECDSA host-key, установка ключа админа; вход и
> пароль НЕ трогаются. Опасная зона (по отдельной кнопке) — AllowGroups sshusers, PermitRootLogin
> no, отключение пароля (только ключ), блокировка пароля root. ЗАЧЕМ — перебор пароля по SSH —
> самый массовый взлом; ключ 256 бит не подобрать. Перед опасными изменениями программа проверяет
> вход админа по ключу в отдельной сессии и показывает ключ для копирования. БЕЗ ЭТОГО — SSH
> принимает слабые шифры; при опт-ине в «опасную зону» без сохранённого ключа можно потерять
> доступ. ЧТО МЕНЯЕТСЯ — /etc/ssh/sshd_config.d/00-hardening.conf и 99-hardening.conf; на 26.04
> постквантовый KEX mlkem768x25519-sha256; перезапуск ssh. КАК ОТКАТИТЬ — удалить drop-in'ы +
> systemctl restart ssh; при блокировке пароля root сначала разблокировать через консоль
> провайдера (passwd -u root); автоотката нет, но опасные изменения имеют ~300с страховку при
> срыве проверки входа.

EN parity for both entries is required (not drafted here).

---

## Cross-cutting constraints

- **RU primary + EN parity** for all new i18n keys.
- **Model stays fully value-copyable:** every new field is `int` / `bool` / `string` — no
  `*Release`, no `strings.Builder`, no pointers to non-copyable types. (Bubble Tea v2 copies the
  model by value every `Update`.)
- **ALL column / box alignment via `lipgloss.Width`** (NEVER `%-*s`) — Cyrillic width.
- The TUI is **Bubble Tea v2** (`charm.land/...`); extend the existing phase enum, do not rewrite
  the framework.

---

## Honesty / required engine additions (checklist)

These are NOT cosmetic; the redesign cannot ship honestly without them:

- [ ] **`engine.Audit(cfg, h)`** — new entrypoint. `tweaks.Run` today runs only inside
      `run` / `verify`; the Dashboard live audit needs its own dial → detect → tweaks path.
- [ ] **A2 split** in the engine into `A2-safe` and `A2-danger` (replacing the `Mode`/`strict`
      branch). A2-safe must write **neither** `PasswordAuthentication` value (leave the image
      default), and must not write `PermitRootLogin` / `AllowGroups` / `passwd -l root`.
- [ ] **Default never locks access:** removing `soft`/`strict` mode means the default apply must
      not change root login, SSH password, or access policy. (Today even `soft` writes
      `PermitRootLogin prohibit-password`, `AllowGroups sshusers`, and `PasswordAuthentication
      yes` — all of that moves into A2-danger.)
- [ ] **Monitor on the password path:** `OnConnect` / monitor dial must work when auth was by
      password (no key generated), not only on the key-bootstrap branch.
- [ ] **`tui.Run()` signature change** to return an update intent (`Result{DoUpdate, TargetVer}`),
      and `main()` performing `UpdateSelf` + relaunch outside the alt-screen.
- [ ] **No fake update state:** `found == false` is up-to-date; never synthesize an error or a
      phantom release.
- [ ] **Audit is single-session:** stream the parse of ONE `tweaks.Run` batch; do NOT advertise
      concurrent multi-session probing.
- [ ] **Revert is manual:** no auto per-step rollback exists; wiki `Revert` text is instructions
      only.
- [ ] **`wiki_test.go` extended** to assert `OnBox` + `Revert` non-empty in RU and EN.

---

## Decomposition into implementation phases

This single spec is large; it will be **DECOMPOSED INTO IMPLEMENTATION PHASES** at the
writing-plans stage (the user chose one spec). Suggested phase order:

- **P1 — Landing + framed inputs + remove actions + save-log.** Repurpose `phaseForm`: framed
  3-row inputs, `▸ Дополнительно` disclosure, remove `kOptRun`/`kOptDetect`/`kOptVerify` from the
  form i18n, save-log toggle, "Что настраивает программа ▸" link. Recompute all Y-offset /
  hit-test math.
- **P2 — Self-update wiring.** Add `go-selfupdate` v1.5.2, `updateCheckMsg`, the three plain
  model fields, the strip states, `tui.Run()` → `Result{DoUpdate,TargetVer}`, `main()` update +
  relaunch, and the `.old` cleanup in `Init()`.
- **P3 — `engine.Audit` + Dashboard live audit.** New engine entrypoint; new `phaseDashboard`
  with server card, incremental audit parse stream, and the three buttons.
- **P4 — Security menu + A2 split (safe/danger) + default-no-lockout.** New `phaseSecurity`; the
  engine A2 split; default apply touches nothing access-related. **P4 subsumes the earlier
  "soft-root: don't touch root" fix.**
- **P5 — Catalog + rich wiki.** New `phaseCatalog` (pre/post-connect); extend `FixDoc` with
  `OnBox` + `Revert`, fill all entries RU+EN, extend `wiki_test.go`; Detail screen renders the
  five sections + live Статус.
- **P6 — Monitor-always-on fix.** Pin the footer on every post-connect phase; fix the
  password-path monitor dial. **P6 subsumes the password-connect monitor bug.**

---

## Out of scope / deliberate cuts

- **`soft` / `strict` mode** is removed entirely from the product UI and is no longer a mandatory
  choice. Not a future feature — replaced by the default-safe + opt-in-danger model.
- **A10 strict-only OS-hardening extras** (kernel module blacklist, `/dev/shm` mount options) are
  **DROPPED** from the product. They may return later as an opt-in danger-zone item; they are not
  in this redesign.
- **No automatic per-step rollback** is in scope. Wiki `Revert` is manual instructions only; the
  only on-box safety nets are A1's connection-loss timer and A2-danger's ~300s `ssh-revert`
  fail-safe on a failed login verify — neither is a user-facing rollback button.
- **No real multi-session / parallel audit probing.** The "parallel" audit framing is cosmetic
  streaming of a single `tweaks.Run` batch only.
- **No CLI behavior change** beyond what the self-update relaunch in `main()` requires; the
  `run` / `detect` / `verify` / `step` engine tokens remain.
