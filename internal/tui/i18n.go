package tui

// Lang is the UI language. The whole render path is keyed on the model's current
// Lang so no user-facing literal is hardcoded in a single language.
type Lang int

const (
	langRU Lang = iota
	langEN
)

// defaultLang is Russian — the operator's native language.
const defaultLang = langRU

// next toggles ru<->en (used by the 'l' / ctrl+l hotkey and click handler).
func (l Lang) next() Lang {
	if l == langRU {
		return langEN
	}
	return langRU
}

// langCode maps the model's active Lang to the engine's "ru"/"en" token so the
// engine-streamed messages (e.g. the auth-failure hint) follow the UI language.
func (m model) langCode() string {
	if m.lang == langEN {
		return "en"
	}
	return "ru"
}

// stringKey enumerates every user-facing string in the form/run UI.
type stringKey int

const (
	// form field labels
	kLabelHost stringKey = iota
	kLabelPort
	kLabelUser
	kLabelPassword
	kLabelKey

	// placeholders
	kPhHost
	kPhPort
	kPhUser
	kPhPass
	kPhKey

	// toggle labels + options
	kLabelMode
	kOptSoft
	kOptStrict
	kLabelAction
	kOptRun
	kOptDetect
	kOptVerify

	// start button
	kStart
	kCancel
	kBackToMain

	// toggle help (contextual)
	kHelpModeStrict
	kHelpModeSoft
	kHelpActDetect
	kHelpActVerify
	kHelpActRun
	kHelpActionOnly // "action=%s — focus a pill (tab) for details"
	kHelpModeAction // "mode=%s · action=%s — focus a pill (tab) for details"

	// option names interpolated into the above (lowercase, stable per language)
	kModeSoftName
	kModeStrictName

	// bottom control hints
	kFormHint
	kRunHintRunning
	kRunHintIdle

	// run-phase progress + summary pieces
	kStepN     // "Step" word in "Step N/M"
	kDoneWord  // "done" in the summary line
	kVerifyTag // "verify" in the summary line

	// loud "SSH password auth will be OFF" warning, shown pre-run (first log lines)
	// and post-run (finished tail) in STRICT mode. The key lives only in memory and
	// is shown on the key screen — these strings reference no on-disk path.
	kPwOffWarn  // strict header: "⚠ ВНИМАНИЕ: вход по паролю по SSH будет ОТКЛЮЧЁН."
	kPwOffLogin // strict body:   "пароль root отключён — подключайся сгенерированным ключом ..."

	// soft-mode info: password login STAYS ON; a key is also generated so either
	// works.
	kPwOnInfo // soft body: "вход по SSH: пароль ИЛИ сгенерированный ключ ..."

	// finished tail (rendered below the viewport from m.lang each frame)
	kFinishedOK
	kFinishedErr // "finished with error: " prefix

	// internet benchmark line (Feature G): kBenchLine carries three values — the
	// PRE MB/s, POST MB/s and the ratio (e.g. "internet: 12.3 → 18.7 MB/s (1.52x)").
	kBenchLine
	// skip-reasons block (Feature F): a header, then one "ID — reason" line each.
	kSkipsHeader
	kSkipLine // "%s — %s": step ID, reason

	// monitor footer
	kMonReconnecting
	kMonTitle

	// box / banner titles
	kFormTitleSuffix // " v" stays numeric; this is just the spacing convention reused

	// validation errors shown on the form's error line (built via fmt.Sprintf with
	// a single %q). kErr* mirror config's sentinel errors so the localized UI never
	// surfaces raw English err.Error() text.
	kErrInvalidHost    // "invalid host %q — …"
	kErrKeyNotFound    // "key file not found: %q — …"
	kErrHostRequired   // config.ErrHostRequired
	kErrUserRequired   // config.ErrUserRequired
	kErrAuthRequired   // config.ErrAuthRequired
	kErrBadMode        // config.ErrBadMode (carries a %q for the bad value)
	kErrValidationFail // generic fallback for an unmapped Validate() error

	// window-title chrome (terminal title bar), rebuilt per-frame from m.lang.
	kTitleHardened // success suffix after "Name · host"
	kTitleFailed   // failure suffix after "Name · host"
	kTitleWarding  // in-progress infix: "Name · <warding> host"

	// --- summary screen (phaseSummary) -----------------------------------
	// header line: "applied X/Y · N skipped · N failed · reboots N · verify P/T"
	kSumApplied // "applied %d/%d"
	kSumSkipped // "%d skipped"
	kSumFailed  // "%d failed"
	kSumReboots // "reboots %d"
	kSumVerify  // "verify %d/%d"

	// section headers
	kSecPkgKernel // ПАКЕТЫ И ЯДРО
	kSecDiskMem   // ДИСК И ПАМЯТЬ
	kSecNetwork   // СЕТЬ
	kSecSecurity  // БЕЗОПАСНОСТЬ
	kSecFixes     // ПРИМЕНЁННЫЕ ФИКСЫ (клик — описание)

	// row labels
	kRowUpgraded // обновлено пакетов
	kRowKernel   // ядро
	kRowPurged   // удалено пакетов
	kRowDiskUsed // диск занято
	kRowZram     // zram
	kRowSpeed    // скорость, MB/s (до зеркала)
	kRowPingGW   // задержка ДЦ, ms
	kRowPingNet  // интернет, ms
	kRowPorts    // открытых портов
	kRowRootLogin
	kRowKeyOnly  // ssh только по ключу
	kRowFirewall // файрвол
	kRowFail2ban // fail2ban
	kZramAdded   // "добавлен" / "added" value for the zram row
	kYesWord     // да / yes
	kNoWord      // нет / no

	// nav hints
	kSummaryHint // enter/esc — меню · клик по фиксу — описание · ↑↓ — прокрутка · l — язык
	kWikiHint    // esc — назад · ↑↓ — прокрутка · l — язык

	// --- wiki page (phaseWiki) -------------------------------------------
	kWikiWhat  // ЧТО ДЕЛАЕТ
	kWikiWhy   // ЗАЧЕМ
	kWikiRisk  // БЕЗ ЭТОГО
	kWikiNoDoc // "нет описания" / "no description"

	// --- SSH key screen (phaseKey) ---------------------------------------
	// The generated private key lives ONLY in memory; this screen is the one
	// place it is shown so the operator can copy it before it is lost.
	kKeyTitle      // box title: "SSH key access"
	kKeyWarnSoft   // soft mode: password login is KEPT — the key is an optional extra
	kKeyWarnStrict // strict mode: root password locked, key-only — copy it now or lose access
	kKeyConnHint   // label before the ssh command (the command is built in code)
	kKeyCopyBtn    // clickable button label: "Copy key"
	kKeyCopied     // status after a successful clipboard copy: "✓ copied"
	kKeyCopyFail   // status after a failed copy: "copy failed — select manually"
	kKeyHint       // bottom control hint for the key screen

	// --- main-menu "Save log to file" toggle -----------------------------
	kSaveLogLabel // form toggle label: "Save log to file"
	kSaveLogOn    // on state word (reuses yes/no semantics)
	kSaveLogOff   // off state word
)

// tr is the translation table: every key carries both ru and en.
var tr = map[Lang]map[stringKey]string{
	langRU: {
		kLabelHost:     "Хост",
		kLabelPort:     "Порт",
		kLabelUser:     "Пользователь",
		kLabelPassword: "Пароль",
		kLabelKey:      "SSH-ключ",

		kPhHost: "1.2.3.4",
		kPhPort: "22",
		kPhUser: "root",
		kPhPass: "пароль SSH",
		kPhKey:  "путь к приватному ключу (пусто — использовать пароль)",

		kLabelMode:   "Режим",
		kOptSoft:     "мягкий",
		kOptStrict:   "строгий",
		kLabelAction: "Действие",
		kOptRun:      "запуск",
		kOptDetect:   "разведка",
		kOptVerify:   "проверка",

		kStart:      "Старт",
		kCancel:     "Отмена",
		kBackToMain: " ↩  Назад в меню ",

		kHelpModeStrict: "строгий: заблокировать пароль root и отключить вход root по SSH",
		kHelpModeSoft:   "мягкий: оставить резервный пароль на консоли (root не блокируется) — безопаснее по умолчанию",
		kHelpActDetect:  "разведка: только чтение — инвентаризация, ничего не меняет",
		kHelpActVerify:  "проверка: запустить только матрицу верификации §V",
		kHelpActRun:     "запуск: полное усиление Фазы A + верификация §V",
		kHelpActionOnly: "действие=%s — выберите пункт (tab) для подробностей",
		kHelpModeAction: "режим=%s · действие=%s — выберите пункт (tab) для подробностей",

		kModeSoftName:   "мягкий",
		kModeStrictName: "строгий",

		kFormHint:       "tab/↑↓ переход · ←/→ переключить · enter: следующее поле, запуск (на Старте) · l: язык · esc выход",
		kRunHintRunning: "l: язык · ctrl+c выход",
		kRunHintIdle:    "enter/esc назад · ↑/↓ прокрутка · l: язык · ctrl+c выход",

		kStepN:     "Шаг",
		kDoneWord:  "готово",
		kVerifyTag: "проверка",

		kPwOffWarn:  "⚠ ВНИМАНИЕ: вход по паролю по SSH будет ОТКЛЮЧЁН (ключ обязателен).",
		kPwOffLogin: "пароль root отключён — подключайся сгенерированным ключом (скопируй его на экране ключа)",
		kPwOnInfo:   "вход по SSH: пароль ИЛИ сгенерированный ключ (скопируй его на экране ключа)",

		kFinishedOK:  "запуск завершён",
		kFinishedErr: "завершено с ошибкой: ",

		kBenchLine:   "интернет: %.1f → %.1f МБ/с (%.2fx)",
		kSkipsHeader: "пропущено:",
		kSkipLine:    "%s — %s",

		kMonReconnecting: "монитор: переподключение…",
		kMonTitle:        " монитор ",

		kFormTitleSuffix: "",

		kErrInvalidHost:    "неверный хост %q — введите IP или имя хоста",
		kErrKeyNotFound:    "файл ключа не найден: %q — оставьте поле ключа пустым, чтобы использовать пароль",
		kErrHostRequired:   "укажите хост",
		kErrUserRequired:   "укажите пользователя",
		kErrAuthRequired:   "требуется пароль или ключ",
		kErrBadMode:        "режим должен быть soft или strict, получено %q",
		kErrValidationFail: "ошибка конфигурации: %s",

		kTitleHardened: "защищён",
		kTitleFailed:   "сбой",
		kTitleWarding:  "защита",

		kSumApplied: "применено %d/%d",
		kSumSkipped: "пропущено %d",
		kSumFailed:  "ошибок %d",
		kSumReboots: "перезагрузок %d",
		kSumVerify:  "проверка %d/%d",

		kSecPkgKernel: "ПАКЕТЫ И ЯДРО",
		kSecDiskMem:   "ДИСК И ПАМЯТЬ",
		kSecNetwork:   "СЕТЬ",
		kSecSecurity:  "БЕЗОПАСНОСТЬ",
		kSecFixes:     "ПРИМЕНЁННЫЕ ФИКСЫ (клик — описание)",

		kRowUpgraded:  "обновлено пакетов",
		kRowKernel:    "ядро",
		kRowPurged:    "удалено пакетов",
		kRowDiskUsed:  "диск занято",
		kRowZram:      "zram",
		kRowSpeed:     "скорость, MB/s (до зеркала)",
		kRowPingGW:    "задержка ДЦ, ms",
		kRowPingNet:   "интернет, ms",
		kRowPorts:     "открытых портов",
		kRowRootLogin: "root-вход",
		kRowKeyOnly:   "ssh только по ключу",
		kRowFirewall:  "файрвол",
		kRowFail2ban:  "fail2ban",
		kZramAdded:    "добавлен",
		kYesWord:      "да",
		kNoWord:       "нет",

		kSummaryHint: "enter/esc — меню · клик по фиксу — описание · ↑↓ — прокрутка · l — язык",
		kWikiHint:    "esc — назад · ↑↓ — прокрутка · l — язык",

		kWikiWhat:  "ЧТО ДЕЛАЕТ",
		kWikiWhy:   "ЗАЧЕМ",
		kWikiRisk:  "БЕЗ ЭТОГО",
		kWikiNoDoc: "нет описания для этого шага",

		kKeyTitle:      "Доступ по SSH-ключу",
		kKeyWarnSoft:   "Режим новичка: вход по логину и паролю (root и от хостинга) СОХРАНЁН — доступ ты не потеряешь. Этот ключ — дополнительный способ входа, можешь сохранить его (необязательно).",
		kKeyWarnStrict: "Режим профессионала: пароль root заблокирован, вход на сервер ТОЛЬКО по этому ключу. Скопируй его сейчас — иначе потеряешь доступ к серверу.",
		kKeyConnHint:   "Подключение:",
		kKeyCopyBtn:    "Скопировать ключ",
		kKeyCopied:     "✓ скопировано",
		kKeyCopyFail:   "не удалось скопировать — выдели вручную",
		kKeyHint:       "esc — назад · c — копировать · l — язык",

		kSaveLogLabel: "Сохранять лог в файл",
		kSaveLogOn:    "да",
		kSaveLogOff:   "нет",
	},
	langEN: {
		kLabelHost:     "Host",
		kLabelPort:     "Port",
		kLabelUser:     "User",
		kLabelPassword: "Password",
		kLabelKey:      "SSH key",

		kPhHost: "1.2.3.4",
		kPhPort: "22",
		kPhUser: "root",
		kPhPass: "ssh password",
		kPhKey:  "private key path (leave empty to use password)",

		kLabelMode:   "Mode",
		kOptSoft:     "soft",
		kOptStrict:   "strict",
		kLabelAction: "Action",
		kOptRun:      "run",
		kOptDetect:   "detect",
		kOptVerify:   "verify",

		kStart:      "Start",
		kCancel:     "Cancel",
		kBackToMain: " ↩  Back to main ",

		kHelpModeStrict: "strict: lock the root password & disable root SSH login",
		kHelpModeSoft:   "soft: keep a console password fallback (root not locked) — safer default",
		kHelpActDetect:  "detect: read-only discovery & inventory — changes nothing",
		kHelpActVerify:  "verify: run only the §V verification matrix",
		kHelpActRun:     "run: full Phase A hardening + §V verification",
		kHelpActionOnly: "action=%s — focus a pill (tab) for details",
		kHelpModeAction: "mode=%s · action=%s — focus a pill (tab) for details",

		kModeSoftName:   "soft",
		kModeStrictName: "strict",

		kFormHint:       "tab/↑↓ move · ←/→ toggle · enter: next field, run (on Start) · l: lang · esc quit",
		kRunHintRunning: "l: lang · ctrl+c quit",
		kRunHintIdle:    "enter/esc back · ↑/↓ scroll · l: lang · ctrl+c quit",

		kStepN:     "Step",
		kDoneWord:  "done",
		kVerifyTag: "verify",

		kPwOffWarn:  "⚠ WARNING: SSH password login will be DISABLED (key required).",
		kPwOffLogin: "root password disabled — connect with the generated key (copy it on the key screen)",
		kPwOnInfo:   "SSH login: password OR the generated key (copy it on the key screen)",

		kFinishedOK:  "run finished",
		kFinishedErr: "finished with error: ",

		kBenchLine:   "internet: %.1f → %.1f MB/s (%.2fx)",
		kSkipsHeader: "skipped:",
		kSkipLine:    "%s — %s",

		kMonReconnecting: "monitor: reconnecting…",
		kMonTitle:        " monitor ",

		kFormTitleSuffix: "",

		kErrInvalidHost:    "invalid host %q — enter an IP or hostname",
		kErrKeyNotFound:    "key file not found: %q — leave SSH key empty to use the password",
		kErrHostRequired:   "host is required",
		kErrUserRequired:   "user is required",
		kErrAuthRequired:   "either password or key is required",
		kErrBadMode:        "mode must be soft or strict, got %q",
		kErrValidationFail: "config error: %s",

		kTitleHardened: "hardened",
		kTitleFailed:   "failed",
		kTitleWarding:  "warding",

		kSumApplied: "applied %d/%d",
		kSumSkipped: "%d skipped",
		kSumFailed:  "%d failed",
		kSumReboots: "reboots %d",
		kSumVerify:  "verify %d/%d",

		kSecPkgKernel: "PACKAGES & KERNEL",
		kSecDiskMem:   "DISK & MEMORY",
		kSecNetwork:   "NETWORK",
		kSecSecurity:  "SECURITY",
		kSecFixes:     "APPLIED FIXES (click for details)",

		kRowUpgraded:  "upgraded pkgs",
		kRowKernel:    "kernel",
		kRowPurged:    "purged pkgs",
		kRowDiskUsed:  "disk used",
		kRowZram:      "zram",
		kRowSpeed:     "speed, MB/s (to mirror)",
		kRowPingGW:    "datacenter latency, ms",
		kRowPingNet:   "internet, ms",
		kRowPorts:     "open ports",
		kRowRootLogin: "root login",
		kRowKeyOnly:   "ssh key-only",
		kRowFirewall:  "firewall",
		kRowFail2ban:  "fail2ban",
		kZramAdded:    "added",
		kYesWord:      "yes",
		kNoWord:       "no",

		kSummaryHint: "enter/esc — menu · click a fix for details · ↑↓ — scroll · l — lang",
		kWikiHint:    "esc — back · ↑↓ — scroll · l — lang",

		kWikiWhat:  "WHAT IT DOES",
		kWikiWhy:   "WHY",
		kWikiRisk:  "WITHOUT IT",
		kWikiNoDoc: "no description for this step",

		kKeyTitle:      "SSH key access",
		kKeyWarnSoft:   "Novice mode: password login (root and your hosting login) is KEPT — you won't lose access. This key is an extra way in; save it if you like (optional).",
		kKeyWarnStrict: "Professional mode: the root password is locked — server access is KEY-ONLY. Copy this key now, or you will lose access to the server.",
		kKeyConnHint:   "Connect:",
		kKeyCopyBtn:    "Copy key",
		kKeyCopied:     "✓ copied",
		kKeyCopyFail:   "copy failed — select manually",
		kKeyHint:       "esc — back · c — copy · l — lang",

		kSaveLogLabel: "Save log to file",
		kSaveLogOn:    "yes",
		kSaveLogOff:   "no",
	},
}

// t looks up key k for the given language, falling back to English then to "".
func t(lang Lang, k stringKey) string {
	if m, ok := tr[lang]; ok {
		if s, ok := m[k]; ok {
			return s
		}
	}
	if s, ok := tr[langEN][k]; ok {
		return s
	}
	return ""
}

// langOptionName maps an internal command/mode token (always the EN canonical
// value used by the engine) to its localized display name for the toggle help.
func langModeName(lang Lang, m string) string {
	switch m {
	case "strict":
		return t(lang, kModeStrictName)
	default:
		return t(lang, kModeSoftName)
	}
}

// langActionName maps the canonical command token to its localized display name.
func langActionName(lang Lang, cmd string) string {
	switch cmd {
	case "detect":
		return t(lang, kOptDetect)
	case "verify":
		return t(lang, kOptVerify)
	default:
		return t(lang, kOptRun)
	}
}

// stepTitles maps each engine step ID (the curID streamed in progress events) to a
// SHORT localized name for the top progress line. The full engine Title() (e.g.
// "Firewall + fail-safe (iptables-nft, v4+v6)") runs off the right edge, so the
// progress line shows this compact form instead. The canonical meaning of each ID
// is grounded in the step's Title() in internal/steps/*.go (and the engine's KEY/
// DETECT pseudo-steps in internal/engine/engine.go); names are kept terse so the
// step name always fits beside the counter+bar+percent. IDs not listed fall back to
// the engine-provided Title via localStepTitle.
var stepTitles = map[Lang]map[string]string{
	langRU: {
		"PRE":    "Подготовка",       // §1 Preconditions
		"KEY":    "SSH-ключ",         // generate ed25519 + switch to key auth
		"DETECT": "Разведка",         // §0.5/§2 pre-flight discovery
		"A1":     "Файрвол",          // Firewall + fail-safe (iptables-nft)
		"A2":     "SSH",              // SSH crypto hardening
		"A2.5":   "Cloud-init",       // cloud-init neutralization
		"A3":     "fail2ban",         // fail2ban
		"A4":     "Сеть/BBR",         // network tuning (BBR, buffers)
		"A5":     "Ядро",             // kernel hardening (sysctl)
		"A6":     "Обслуживание",     // maintenance (journald, ntp, …)
		"A6.5":   "DNS",              // DNS hardening (DoT/DNSSEC)
		"A6.7":   "Память",           // memory mgmt (ZRAM + earlyoom)
		"A7":     "Очистка",          // cleanup (purge bloatware)
		"A8":     "Обновление+ребут", // full upgrade + reboot
		"A9":     "Автообновления",   // unattended security updates
		"A10":    "Аудит",            // detection (auditd, login-notify)
	},
	langEN: {
		"PRE":    "Preconditions",
		"KEY":    "SSH key",
		"DETECT": "Discovery",
		"A1":     "Firewall",
		"A2":     "SSH",
		"A2.5":   "Cloud-init",
		"A3":     "fail2ban",
		"A4":     "Network/BBR",
		"A5":     "Kernel",
		"A6":     "Maintenance",
		"A6.5":   "DNS",
		"A6.7":   "Memory",
		"A7":     "Cleanup",
		"A8":     "Upgrade+reboot",
		"A9":     "Auto-updates",
		"A10":    "Audit",
	},
}

// localStepTitle returns the SHORT localized name for step id in lang, falling back
// to fallback (the engine-provided Title) when the id is unknown.
func localStepTitle(lang Lang, id, fallback string) string {
	if m, ok := stepTitles[lang]; ok {
		if s, ok := m[id]; ok {
			return s
		}
	}
	if s, ok := stepTitles[langEN][id]; ok {
		return s
	}
	return fallback
}
