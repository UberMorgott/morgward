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

	// start button
	kStart
	kCancel
	kBackToMain

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
	kWikiWhat      // ЧТО ДЕЛАЕТ
	kWikiWhy       // ЗАЧЕМ
	kWikiRisk      // БЕЗ ЭТОГО
	kWikiOnBox     // ЧТО МЕНЯЕТСЯ НА СЕРВЕРЕ
	kWikiRevert    // КАК ОТКАТИТЬ
	kWikiStatus    // "Статус:" label prefixed to the post-connect status word
	kWikiNoDoc     // "нет описания" / "no description"
	kWikiBack      // clickable back button: "← Назад" / "← Back"
	kWikiProbeWhat // per-probe detail label: "ЧТО ПРОВЕРЯЕТ" / "WHAT THIS CHECKS"

	// --- live tweak status words (shared by the wiki status line) --------
	kStatusApplied     // "✓ применено" / "✓ applied"
	kStatusCanApply    // "• можно" / "• available"
	kStatusUnavailable // "⊘ недоступно" / "⊘ unavailable"

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

	// --- анализ matrix (phaseMatrix) -------------------------------------
	kTweakApplied
	kTweakNotApplied
	kTweakSummary
	kMatrixHint

	// --- landing redesign (P1) -------------------------------------------
	// disclosure toggle revealing the advanced Port/User/SSH-key inputs
	kDisclosureLabel // collapsible toggle label: "▸ Дополнительно (порт · пользователь · SSH-ключ)"
	kDisclosureOpen  // open-state indicator glyph "▼" (RU/EN same)

	// landing version-frame tagline under the "Morgward vX.Y" header
	kVersionTagline // "VPS guardian · защита свежего Ubuntu VPS"

	// update strip states (P1 prep; P2 wires the model state machine + button)
	kUpdateChecking    // "Обновления: проверка… ⠋"
	kUpdateCurrent     // "Обновления: ✓ установлена последняя версия"
	kUpdateAvailable   // "Обновления: vX.Y доступна" (carries a %s for the version)
	kUpdateError       // "не удалось проверить (офлайн)"
	kUpdateButtonLabel // clickable button: "Обновить ⬇"

	// --- dashboard (phaseDashboard, P3) ----------------------------------
	kDashTitle        // server card header prefix: "Сервер" / "Server"
	kDashAuditLabel   // live audit line label: "Анализ твиков" / "Analyzing tweaks"
	kDashAuditStatus  // "применено %d из %d" / "applied %d of %d"
	kDashCanApply     // "можно применить %d" / "can apply %d"
	kDashApplyButton  // "Применить твики" / "Apply tweaks"
	kDashSecButton    // "Безопасность ▸" / "Security ▸"
	kDashOS           // "ОС" / "OS"
	kDashKernel       // "Ядро" / "Kernel"
	kDashVirt         // "Виртуализация" / "Virt"
	kDashMemory       // "Память" / "Memory"
	kDashDisk         // "Диск" / "Disk"
	kDashPorts        // "Порты" / "Ports"
	kDashIPv6         // "IPv6"
	kDashHint         // dashboard control hint
	kDashApplyConfirm // A8 reboot warning shown before applying tweaks

	// --- security menu (phaseSecurity, P4) -------------------------------
	kSecMenuTitle      // box title: "Безопасность и доступ" / "Security and access"
	kSecRootLogin      // access-card label: "Вход root" / "Root login"
	kSecKeyOnly        // access-card label: "Только по ключу" / "Key-only"
	kSecAdmin          // access-card label: "Админ" / "Admin user"
	kSecSafeHeader     // "Безопасно (вход не меняется):" / "Safe (access unchanged):"
	kSecCreateAdmin    // SAFE button: "Создать админа" / "Create admin"
	kSecCryptoKey      // SAFE button: "Усилить SSH-крипто + ключ" / "Strengthen SSH crypto + key"
	kSecDangerHeader   // "⚠ Опасная зона (можно потерять доступ):" / danger header
	kSecKeyOnlyBtn     // DANGER button: "Вход только по ключу · заблокировать пароль root" / …
	kSecDangerConfirm  // explicit blocking warning shown before the danger apply
	kSecHint           // security-menu control hint
	kSecRootByPassword // access-card value: root login allowed by password
	kSecAdminAbsent    // access-card value: no admin user present
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

		kStart:      "Подключиться",
		kCancel:     "Отмена",
		kBackToMain: " ↩  Назад в меню ",

		kFormHint:       "tab/↑↓ переход · ←/→ переключить · enter: следующее поле, подключение (на «Подключиться») · l: язык · esc выход",
		kRunHintRunning: "esc назад · l: язык · ctrl+c выход",
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

		kWikiWhat:      "ЧТО ДЕЛАЕТ",
		kWikiWhy:       "ЗАЧЕМ",
		kWikiRisk:      "БЕЗ ЭТОГО",
		kWikiOnBox:     "ЧТО МЕНЯЕТСЯ НА СЕРВЕРЕ",
		kWikiRevert:    "КАК ОТКАТИТЬ",
		kWikiStatus:    "Статус:",
		kWikiNoDoc:     "нет описания для этого шага",
		kWikiBack:      "← Назад",
		kWikiProbeWhat: "ЧТО ПРОВЕРЯЕТ",

		kStatusApplied:     "✓ применено",
		kStatusCanApply:    "• можно",
		kStatusUnavailable: "⊘ недоступно",

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

		kTweakApplied:    "применён",
		kTweakNotApplied: "не применён",
		kTweakSummary:    "%d применено / %d нет",
		kMatrixHint:      "↑/↓ прокрутка · esc назад",

		kDisclosureLabel: "▸ Дополнительно (порт · пользователь · SSH-ключ)",
		kDisclosureOpen:  "▼",
		kVersionTagline:  "VPS guardian · защита свежего Ubuntu VPS",

		kUpdateChecking:    "Обновления: проверка… ⠋",
		kUpdateCurrent:     "Обновления: ✓ установлена последняя версия",
		kUpdateAvailable:   "Обновления: v%s доступна",
		kUpdateError:       "не удалось проверить (офлайн)",
		kUpdateButtonLabel: "Обновить ⬇",

		kDashTitle:        "Сервер",
		kDashAuditLabel:   "Анализ твиков",
		kDashAuditStatus:  "применено %d из %d",
		kDashCanApply:     "можно применить %d",
		kDashApplyButton:  "Применить твики",
		kDashSecButton:    "Безопасность ▸",
		kDashOS:           "ОС",
		kDashKernel:       "Ядро",
		kDashVirt:         "Виртуализация",
		kDashMemory:       "Память",
		kDashDisk:         "Диск",
		kDashPorts:        "Порты",
		kDashIPv6:         "IPv6",
		kDashHint:         "↑/↓ прокрутка · enter описание твика · esc назад",
		kDashApplyConfirm: "Включает полное обновление и перезагрузку (A8) — несколько минут. Enter — применить, esc — отмена.",

		kSecMenuTitle:      " Безопасность и доступ ",
		kSecRootLogin:      "Вход root",
		kSecKeyOnly:        "Только по ключу",
		kSecAdmin:          "Админ",
		kSecSafeHeader:     "Безопасно (вход не меняется):",
		kSecCreateAdmin:    "Создать админа",
		kSecCryptoKey:      "Усилить SSH-крипто + ключ",
		kSecDangerHeader:   "⚠ Опасная зона (можно потерять доступ):",
		kSecKeyOnlyBtn:     "Вход только по ключу · заблокировать пароль root",
		kSecDangerConfirm:  "Вы потеряете доступ к root SSH, если ключ не сохранён. Покажем ключ перед применением. Enter — показать ключ и применить, esc — отмена.",
		kSecHint:           "1/2 — безопасные действия · 3 — опасное · esc назад · l — язык",
		kSecRootByPassword: "разрешён по паролю",
		kSecAdminAbsent:    "отсутствует",
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

		kStart:      "Connect",
		kCancel:     "Cancel",
		kBackToMain: " ↩  Back to main ",

		kFormHint:       "tab/↑↓ move · ←/→ toggle · enter: next field, connect (on Connect) · l: lang · esc quit",
		kRunHintRunning: "esc back · l: lang · ctrl+c quit",
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

		kWikiWhat:      "WHAT IT DOES",
		kWikiWhy:       "WHY",
		kWikiRisk:      "WITHOUT IT",
		kWikiOnBox:     "WHAT CHANGES ON THE SERVER",
		kWikiRevert:    "HOW TO REVERT",
		kWikiStatus:    "Status:",
		kWikiNoDoc:     "no description for this step",
		kWikiBack:      "← Back",
		kWikiProbeWhat: "WHAT THIS CHECKS",

		kStatusApplied:     "✓ applied",
		kStatusCanApply:    "• available",
		kStatusUnavailable: "⊘ unavailable",

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

		kTweakApplied:    "applied",
		kTweakNotApplied: "not applied",
		kTweakSummary:    "%d applied / %d missing",
		kMatrixHint:      "↑/↓ scroll · esc back",

		kDisclosureLabel: "▸ Advanced (port · user · SSH key)",
		kDisclosureOpen:  "▼",
		kVersionTagline:  "VPS guardian · fresh Ubuntu VPS protection",

		kUpdateChecking:    "Updates: checking… ⠋",
		kUpdateCurrent:     "Updates: ✓ latest version installed",
		kUpdateAvailable:   "Updates: v%s available",
		kUpdateError:       "failed to check (offline)",
		kUpdateButtonLabel: "Update ⬇",

		kDashTitle:        "Server",
		kDashAuditLabel:   "Analyzing tweaks",
		kDashAuditStatus:  "applied %d of %d",
		kDashCanApply:     "can apply %d",
		kDashApplyButton:  "Apply tweaks",
		kDashSecButton:    "Security ▸",
		kDashOS:           "OS",
		kDashKernel:       "Kernel",
		kDashVirt:         "Virt",
		kDashMemory:       "Memory",
		kDashDisk:         "Disk",
		kDashPorts:        "Ports",
		kDashIPv6:         "IPv6",
		kDashHint:         "↑/↓ scroll · enter tweak detail · esc back",
		kDashApplyConfirm: "Includes a full upgrade and reboot (A8) — several minutes. Enter to apply, esc to cancel.",

		kSecMenuTitle:      " Security and access ",
		kSecRootLogin:      "Root login",
		kSecKeyOnly:        "Key-only",
		kSecAdmin:          "Admin user",
		kSecSafeHeader:     "Safe (access unchanged):",
		kSecCreateAdmin:    "Create admin",
		kSecCryptoKey:      "Strengthen SSH crypto + key",
		kSecDangerHeader:   "⚠ Danger zone (you may lose access):",
		kSecKeyOnlyBtn:     "Key-only login · lock the root password",
		kSecDangerConfirm:  "You will lose root SSH access if the key is not saved. We will show the key before applying. Enter to show the key and apply, esc to cancel.",
		kSecHint:           "1/2 — safe actions · 3 — danger · esc back · l — lang",
		kSecRootByPassword: "allowed by password",
		kSecAdminAbsent:    "absent",
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

// tweakNames maps a tweaks.Probe.ID to its localized display name. Missing IDs
// fall through to the probe's English Name (see localTweakName).
var tweakNames = map[Lang]map[string]string{
	langRU: {
		"a1.input_drop":     "Политика INPUT DROP",
		"a1.ssh_accept":     "Порт SSH разрешён",
		"a1.rules_v4":       "Правила v4 сохранены",
		"a1.rules_v6":       "Правила v6 сохранены",
		"a1.persistent":     "iptables-persistent",
		"a2.conf00":         "00-hardening.conf",
		"a2.conf99":         "99-hardening.conf",
		"a2.allowgroups":    "AllowGroups sshusers",
		"a2.ecdsa_absent":   "ECDSA host-key удалён",
		"a2.ssh_active":     "Служба ssh активна",
		"a2.permitroot":     "PermitRootLogin",
		"a2.passauth":       "Парольный вход",
		"a2.kex_mlkem":      "PQ-обмен ключей (mlkem768)",
		"a25.disabled":      "cloud-init отключён",
		"a3.installed":      "fail2ban установлен",
		"a3.jail_local":     "jail.local",
		"a3.sshd_jail":      "Джейл sshd активен",
		"a4.net_tune":       "99-net-tune.conf",
		"a4.bbr_conf":       "99-bbr.conf",
		"a4.bbr_module":     "Модуль tcp_bbr загружен",
		"a4.bbr_active":     "Контроль перегрузки BBR",
		"a4.qdisc":          "Очередь fq по умолчанию",
		"a4.io_sched":       "Планировщик I/O (udev)",
		"a5.harden_conf":    "99-zz-kernel-harden.conf",
		"a5.core_pattern":   "core_pattern отключён",
		"a5.rp_filter":      "rp_filter строгий",
		"a5.kptr":           "kptr_restrict",
		"a5.thp":            "THP madvise",
		"a6.journald":       "Лимит journald",
		"a6.needrestart":    "needrestart авто",
		"a6.nofile":         "Лимит NOFILE",
		"a6.ntp":            "NTP включён",
		"a65.dns_conf":      "Защита DNS (resolved)",
		"a65.dot":           "DNSOverTLS opportunistic",
		"a67.zram_conf":     "zram-generator.conf",
		"a67.zram_sysctl":   "zram swappiness",
		"a67.zram_active":   "zram-своп активен",
		"a67.earlyoom":      "earlyoom активен",
		"a9.installed":      "unattended-upgrades",
		"a9.auto":           "20auto-upgrades",
		"a9.local":          "52-unattended-upgrades-local",
		"a10.auditd":        "auditd установлен",
		"a10.audit_rules":   "99-vps.rules",
		"a10.auditd_active": "auditd активен",
		"a10.notify":        "ssh-login-notify",
		"a10.pam":           "pam.d/sshd уведомление",
		"a10.log_rule":      "LOG входящих (firewall)",
		"a10.blacklist":     "Чёрный список модулей",
		"a10.devshm":        "/dev/shm защищён",
	},
	langEN: {}, // English falls back to Probe.Name; no overrides needed
}

// localTweakName returns the localized name for a probe ID, or fallback (the
// probe's English Name) when no localized entry exists.
func localTweakName(lang Lang, id, fallback string) string {
	if m, ok := tweakNames[lang]; ok {
		if s, ok := m[id]; ok {
			return s
		}
	}
	return fallback
}

// probeDescs holds a short, per-PROBE description keyed by tweaks.Probe.ID, in
// both languages. Unlike the step-level wiki.Doc (shared by every probe of a
// step), each entry names the concrete artifact THIS probe verifies — a file,
// sysctl key, systemd service, dpkg package or iptables rule — so the wiki/detail
// screen explains the individual check, not the whole step. Every probe ID that
// tweaks.Registry can emit (including the version/IPv6-gated a1.rules_v6 and
// a2.kex_mlkem) must have an entry in BOTH languages — see TestEveryProbeHasDesc.
var probeDescs = map[Lang]map[string]string{
	langRU: {
		// --- A1 firewall ---
		"a1.input_drop": "Проверяет, что базовая политика цепочки INPUT в iptables — DROP, то есть весь незаявленный входящий трафик отбрасывается по умолчанию.",
		"a1.ssh_accept": "Проверяет, что в цепочке INPUT есть правило ACCEPT для порта SSH — иначе политика DROP отрезала бы вас от сервера.",
		"a1.rules_v4":   "Проверяет наличие файла /etc/iptables/rules.v4 — сохранённых правил IPv4, которые iptables-persistent восстанавливает после перезагрузки.",
		"a1.rules_v6":   "Проверяет наличие файла /etc/iptables/rules.v6 — сохранённых правил IPv6 (только если у сервера есть IPv6).",
		"a1.persistent": "Проверяет, что установлен пакет iptables-persistent — без него правила файрвола не переживут перезагрузку.",

		// --- A2 ssh ---
		"a2.conf00":       "Проверяет наличие drop-in /etc/ssh/sshd_config.d/00-hardening.conf — первой части усиленной конфигурации sshd.",
		"a2.conf99":       "Проверяет наличие drop-in /etc/ssh/sshd_config.d/99-hardening.conf — финальной части усиленной конфигурации sshd (перекрывает дефолты).",
		"a2.allowgroups":  "Проверяет через sshd -T, что задан AllowGroups sshusers — вход по SSH разрешён только членам группы sshusers. Информационно: на безопасном пути может быть не задано.",
		"a2.ecdsa_absent": "Проверяет, что host-key /etc/ssh/ssh_host_ecdsa_key удалён — оставляем только Ed25519, убирая слабый ECDSA-ключ сервера.",
		"a2.ssh_active":   "Проверяет через systemctl is-active, что служба ssh (или sshd) запущена и работает.",
		"a2.permitroot":   "Читает действующее значение PermitRootLogin из sshd -T. Информационно: в режиме soft ожидается prohibit-password, в strict — no.",
		"a2.passauth":     "Читает действующее значение PasswordAuthentication из sshd -T. Информационно: в soft пароль остаётся (yes), в strict выключается (no).",
		"a2.kex_mlkem":    "Проверяет через sshd -T, что в KexAlgorithms включён постквантовый обмен ключами mlkem768x25519-sha256 (только Ubuntu 26.04).",

		// --- A2.5 cloud-init ---
		"a25.disabled": "Проверяет наличие файла-флага /etc/cloud/cloud-init.disabled (или что cloud-init вовсе не установлен) — чтобы он не откатывал настройки при перезагрузке.",

		// --- A3 fail2ban ---
		"a3.installed":  "Проверяет через dpkg, что установлен пакет fail2ban — сам демон бана по неудачным входам.",
		"a3.jail_local": "Проверяет наличие файла /etc/fail2ban/jail.local — локальной конфигурации джейлов (jail sshd, белый список IP).",
		"a3.sshd_jail":  "Проверяет через fail2ban-client status sshd, что джейл sshd реально загружен и активен, а не просто прописан в конфиге.",

		// --- A4 network ---
		"a4.net_tune":   "Проверяет наличие /etc/sysctl.d/99-net-tune.conf — sysctl-настроек буферов сокетов и сетевых очередей.",
		"a4.bbr_conf":   "Проверяет наличие /etc/sysctl.d/99-bbr.conf — файла, включающего BBR и очередь fq при загрузке.",
		"a4.bbr_module": "Проверяет через lsmod, что модуль ядра tcp_bbr загружен — без него алгоритм BBR недоступен.",
		"a4.bbr_active": "Читает sysctl net.ipv4.tcp_congestion_control и проверяет, что действующий контроль перегрузки — bbr.",
		"a4.qdisc":      "Читает sysctl net.core.default_qdisc и проверяет, что дисциплина очереди по умолчанию — fq (нужна для корректной работы BBR).",
		"a4.io_sched":   "Проверяет наличие udev-правила /etc/udev/rules.d/60-io-scheduler.rules на дисках vd* (на других дисках проверка неприменима).",

		// --- A5 kernel ---
		"a5.harden_conf":  "Проверяет наличие /etc/sysctl.d/99-zz-kernel-harden.conf — набора sysctl-параметров усиления ядра.",
		"a5.core_pattern": "Читает sysctl kernel.core_pattern и проверяет, что дампы памяти перенаправлены в /bin/false, то есть отключены.",
		"a5.rp_filter":    "Читает sysctl net.ipv4.conf.all.rp_filter и проверяет строгую (=1) обратную проверку маршрута против подмены адресов.",
		"a5.kptr":         "Читает sysctl kernel.kptr_restrict и проверяет значение 2 — адреса ядра полностью скрыты, чтобы не подсказывать эксплойтам.",
		"a5.thp":          "Читает /sys/kernel/mm/transparent_hugepage/enabled и проверяет режим [madvise] для прозрачных больших страниц.",

		// --- A6 maintenance ---
		"a6.journald":    "Проверяет наличие /etc/systemd/journald.conf.d/99-vps-cap.conf — лимита размера журнала systemd, чтобы логи не забили диск.",
		"a6.needrestart": "Проверяет наличие /etc/needrestart/conf.d/50-autorestart.conf — настройки неинтерактивного перезапуска служб после обновлений.",
		"a6.nofile":      "Проверяет наличие /etc/systemd/system.conf.d/limits.conf — поднятого лимита открытых файлов (NOFILE).",
		"a6.ntp":         "Читает timedatectl и проверяет, что синхронизация времени по NTP включена (NTP=yes).",

		// --- A6.5 DNS ---
		"a65.dns_conf": "Проверяет наличие /etc/systemd/resolved.conf.d/99-morgward-dns.conf — drop-in с защищёнными резолверами для systemd-resolved.",
		"a65.dot":      "Проверяет, что в том же drop-in задано DNSOverTLS=opportunistic — DNS-запросы шифруются по DNS-over-TLS, где это возможно.",

		// --- A6.7 memory ---
		"a67.zram_conf":   "Проверяет наличие /etc/systemd/zram-generator.conf — конфигурации сжатого свопа в ОЗУ (ZRAM, zstd).",
		"a67.zram_sysctl": "Проверяет наличие /etc/sysctl.d/99-zram.conf — sysctl-настройки swappiness под ZRAM.",
		"a67.zram_active": "Проверяет через swapon, что zram-устройство реально подключено как своп, а не только описано в конфиге.",
		"a67.earlyoom":    "Проверяет через systemctl is-active, что служба earlyoom работает — она мягко завершает процессы до жёсткой нехватки памяти.",

		// --- A9 unattended-upgrades ---
		"a9.installed": "Проверяет через dpkg, что установлен пакет unattended-upgrades — механизм автоматической установки обновлений безопасности.",
		"a9.auto":      "Проверяет наличие /etc/apt/apt.conf.d/20auto-upgrades — файла, включающего периодический запуск автообновлений.",
		"a9.local":     "Проверяет наличие /etc/apt/apt.conf.d/52-unattended-upgrades-local — локальной настройки (без авто-перезагрузки, чистка ядер).",

		// --- A10 detection ---
		"a10.auditd":        "Проверяет через dpkg, что установлен пакет auditd — демон аудита изменений важных файлов.",
		"a10.audit_rules":   "Проверяет наличие /etc/audit/rules.d/99-vps.rules — набора правил аудита для отслеживаемых файлов и событий.",
		"a10.auditd_active": "Проверяет через systemctl is-active, что служба auditd запущена и собирает события.",
		"a10.notify":        "Проверяет наличие скрипта /usr/local/sbin/ssh-login-notify.sh — он шлёт уведомление об успешном входе по SSH.",
		"a10.pam":           "Проверяет, что в /etc/pam.d/sshd прописана строка вызова ssh-login-notify — без неё уведомления о входе не сработают.",
		"a10.log_rule":      "Проверяет через iptables, что в цепочке INPUT есть LOG-правило (метка ipt-drop-in), журналирующее отброшенные входящие пакеты.",
	},
	langEN: {
		// --- A1 firewall ---
		"a1.input_drop": "Checks that the iptables INPUT chain's default policy is DROP, so all unsolicited inbound traffic is rejected by default.",
		"a1.ssh_accept": "Checks the INPUT chain has an ACCEPT rule for the SSH port — without it the DROP policy would cut you off from the server.",
		"a1.rules_v4":   "Checks for /etc/iptables/rules.v4 — the saved IPv4 ruleset that iptables-persistent restores after a reboot.",
		"a1.rules_v6":   "Checks for /etc/iptables/rules.v6 — the saved IPv6 ruleset (only when the server has IPv6).",
		"a1.persistent": "Checks that the iptables-persistent package is installed — without it firewall rules do not survive a reboot.",

		// --- A2 ssh ---
		"a2.conf00":       "Checks for the drop-in /etc/ssh/sshd_config.d/00-hardening.conf — the first part of the hardened sshd configuration.",
		"a2.conf99":       "Checks for the drop-in /etc/ssh/sshd_config.d/99-hardening.conf — the final part of the hardened sshd config (overrides defaults).",
		"a2.allowgroups":  "Checks via sshd -T that AllowGroups sshusers is set — SSH login is restricted to members of the sshusers group. Informational: may be unset on the safe path.",
		"a2.ecdsa_absent": "Checks that the host key /etc/ssh/ssh_host_ecdsa_key is removed — keeping only Ed25519 and dropping the weaker ECDSA server key.",
		"a2.ssh_active":   "Checks via systemctl is-active that the ssh (or sshd) service is up and running.",
		"a2.permitroot":   "Reads the effective PermitRootLogin from sshd -T. Informational: soft mode expects prohibit-password, strict expects no.",
		"a2.passauth":     "Reads the effective PasswordAuthentication from sshd -T. Informational: soft keeps it on (yes), strict turns it off (no).",
		"a2.kex_mlkem":    "Checks via sshd -T that the post-quantum key exchange mlkem768x25519-sha256 is enabled in KexAlgorithms (Ubuntu 26.04 only).",

		// --- A2.5 cloud-init ---
		"a25.disabled": "Checks for the flag file /etc/cloud/cloud-init.disabled (or that cloud-init is not installed at all) so it can't revert your config on reboot.",

		// --- A3 fail2ban ---
		"a3.installed":  "Checks via dpkg that the fail2ban package is installed — the daemon that bans IPs after failed logins.",
		"a3.jail_local": "Checks for /etc/fail2ban/jail.local — the local jail configuration (sshd jail, IP whitelist).",
		"a3.sshd_jail":  "Checks via fail2ban-client status sshd that the sshd jail is actually loaded and active, not just present in config.",

		// --- A4 network ---
		"a4.net_tune":   "Checks for /etc/sysctl.d/99-net-tune.conf — the sysctl settings for socket buffers and network queues.",
		"a4.bbr_conf":   "Checks for /etc/sysctl.d/99-bbr.conf — the file that enables BBR and the fq queue at boot.",
		"a4.bbr_module": "Checks via lsmod that the tcp_bbr kernel module is loaded — without it the BBR algorithm is unavailable.",
		"a4.bbr_active": "Reads sysctl net.ipv4.tcp_congestion_control and checks the active congestion control is bbr.",
		"a4.qdisc":      "Reads sysctl net.core.default_qdisc and checks the default queueing discipline is fq (needed for BBR to work correctly).",
		"a4.io_sched":   "Checks for the udev rule /etc/udev/rules.d/60-io-scheduler.rules on vd* disks (not applicable on other disk types).",

		// --- A5 kernel ---
		"a5.harden_conf":  "Checks for /etc/sysctl.d/99-zz-kernel-harden.conf — the bundle of kernel-hardening sysctl parameters.",
		"a5.core_pattern": "Reads sysctl kernel.core_pattern and checks core dumps are piped to /bin/false, i.e. disabled.",
		"a5.rp_filter":    "Reads sysctl net.ipv4.conf.all.rp_filter and checks strict (=1) reverse-path filtering against address spoofing.",
		"a5.kptr":         "Reads sysctl kernel.kptr_restrict and checks the value is 2 — kernel addresses are fully hidden so they can't aid exploits.",
		"a5.thp":          "Reads /sys/kernel/mm/transparent_hugepage/enabled and checks the [madvise] mode for transparent huge pages.",

		// --- A6 maintenance ---
		"a6.journald":    "Checks for /etc/systemd/journald.conf.d/99-vps-cap.conf — the systemd journal size cap that keeps logs from filling the disk.",
		"a6.needrestart": "Checks for /etc/needrestart/conf.d/50-autorestart.conf — the non-interactive service-restart setting used after updates.",
		"a6.nofile":      "Checks for /etc/systemd/system.conf.d/limits.conf — the raised open-file limit (NOFILE).",
		"a6.ntp":         "Reads timedatectl and checks that NTP time synchronization is enabled (NTP=yes).",

		// --- A6.5 DNS ---
		"a65.dns_conf": "Checks for /etc/systemd/resolved.conf.d/99-morgward-dns.conf — the systemd-resolved drop-in with hardened resolvers.",
		"a65.dot":      "Checks the same drop-in sets DNSOverTLS=opportunistic — DNS queries are encrypted over DNS-over-TLS where possible.",

		// --- A6.7 memory ---
		"a67.zram_conf":   "Checks for /etc/systemd/zram-generator.conf — the configuration for compressed in-RAM swap (ZRAM, zstd).",
		"a67.zram_sysctl": "Checks for /etc/sysctl.d/99-zram.conf — the swappiness sysctl tuned for ZRAM.",
		"a67.zram_active": "Checks via swapon that a zram device is actually active as swap, not merely described in config.",
		"a67.earlyoom":    "Checks via systemctl is-active that the earlyoom service is running — it gently kills processes before a hard out-of-memory.",

		// --- A9 unattended-upgrades ---
		"a9.installed": "Checks via dpkg that the unattended-upgrades package is installed — the mechanism that auto-installs security updates.",
		"a9.auto":      "Checks for /etc/apt/apt.conf.d/20auto-upgrades — the file that enables the periodic auto-upgrade jobs.",
		"a9.local":     "Checks for /etc/apt/apt.conf.d/52-unattended-upgrades-local — the local tuning (no auto-reboot, kernel cleanup).",

		// --- A10 detection ---
		"a10.auditd":        "Checks via dpkg that the auditd package is installed — the daemon that audits changes to sensitive files.",
		"a10.audit_rules":   "Checks for /etc/audit/rules.d/99-vps.rules — the audit ruleset for the watched files and events.",
		"a10.auditd_active": "Checks via systemctl is-active that the auditd service is running and collecting events.",
		"a10.notify":        "Checks for the script /usr/local/sbin/ssh-login-notify.sh — it sends a notification on a successful SSH login.",
		"a10.pam":           "Checks that /etc/pam.d/sshd contains the ssh-login-notify line — without it login notifications never fire.",
		"a10.log_rule":      "Checks via iptables that the INPUT chain has a LOG rule (ipt-drop-in tag) that logs dropped inbound packets.",
	},
}

// probeDesc returns the localized per-probe description for a probe ID and ok=true
// when present, falling back to English then to ok=false when missing. Used by
// wikiBodyLines to render the per-PROBE detail instead of the step-level wiki.Doc.
func probeDesc(lang Lang, id string) (string, bool) {
	if m, ok := probeDescs[lang]; ok {
		if s, ok := m[id]; ok && s != "" {
			return s, true
		}
	}
	if s, ok := probeDescs[langEN][id]; ok && s != "" {
		return s, true
	}
	return "", false
}
