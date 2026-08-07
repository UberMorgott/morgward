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

	// info: password login STAYS ON; a key is also generated so either works.
	kPwOnInfo // body: "вход по SSH: пароль ИЛИ сгенерированный ключ ..."

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
	kSumProbes  // "tweaks confirmed %d/%d" (real on-box probe tally)

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
	kWikiHint // esc — назад · ↑↓ — прокрутка · l — язык

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

	// --- wiki PROBE-detail action buttons (only when m.wikiProbeID != "") --------
	kWikiApplyButton   // clickable: apply THIS tweak's step ("Применить" / "Apply")
	kWikiRevertButton  // clickable: revert THIS tweak's step ("Откатить" / "Revert")
	kWikiUpdateButton  // clickable: full upgrade + reboot ("Обновить и перезагрузить" / "Update & reboot")
	kWikiUpdateWarn    // warning line shown when dashFacts.PendingUpgrades > 0
	kWikiUpdateConfirm // A8 reboot confirm prompt shown after the first update-button activation

	// --- live tweak status words (shared by the wiki status line) --------
	kStatusApplied     // "✓ применено" / "✓ applied"
	kStatusCanApply    // "• можно" / "• available"
	kStatusUnavailable // "⊘ недоступно" / "⊘ unavailable"

	// --- SSH key screen (phaseKey) ---------------------------------------
	// The generated private key lives ONLY in memory; this screen is the one
	// place it is shown so the operator can copy it before it is lost.
	kKeyTitle    // box title: "SSH key access"
	kKeyWarnSoft // password login is KEPT — the key is an optional extra
	kKeyConnHint // label before the ssh command (the command is built in code)
	kKeyCopyBtn  // clickable button label: "Copy key"
	kKeyCopied   // status after a successful clipboard copy: "✓ copied"
	kKeyCopyFail // status after a failed copy: "copy failed — select manually"
	kKeyHint     // bottom control hint for the key screen

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
	kDashApplyButton  // "Применить все твики" / "Apply all tweaks"
	kDashSecButton    // "Безопасность ▸" / "Security ▸"
	kDashOS           // "ОС" / "OS"
	kDashKernel       // "Ядро" / "Kernel"
	kDashVirt         // "Виртуализация" / "Virt"
	kDashPorts        // "Порты" / "Ports"
	kDashIPv6         // "IPv6"
	kDashHint         // dashboard control hint
	kDashApplyConfirm // A8 reboot warning shown before applying tweaks

	// --- FEATURE A: detected-services right column in the server card ----
	kDashServicesTitle // services right-column header: "Сервисы" / "Services"
	kDashServicesMore  // "… +%d ещё" / "… +%d more" — overflow line when capped

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

	// --- CHANGE 1: apply-confirm centered modal -------------------------------
	kApplyModalTitle   // modal title: "Применить твики?"
	kApplyModalBody    // modal body: applies all tweaks
	kApplyModalReboot  // extra reboot warning when the bucket includes A8
	kApplyModalConfirm // confirm pill label: "[Enter] применить"
	kApplyModalCancel  // cancel pill label: "[Esc] отмена"

	// --- CHANGE 2: pre-run generated-key modal --------------------------------
	kKeyPreRunWarn    // pre-run guidance: save the key — without it you can't get in
	kKeyPreRunButtons // pre-run buttons: "[Enter] начать применение   [Esc] отмена"
	kKeyPreRunHint    // pre-run bottom hint

	// --- CHANGE 4: two-column summary (SSH access + home button) --------------
	kSecColTitle   // right-column header: "SSH-ДОСТУП" / "SSH ACCESS"
	kSumRAM        // stats-strip / disk-mem row label: "ОЗУ" / "RAM"
	kSumKeyAdded   // ssh-access row: "ключ добавлен" / "key added"
	kSumKeyShow    // clickable ssh-access row: "ключ ‹показать›" / "key ‹show›"
	kSumHomeButton // clickable home pill: "[ На главную ]" / "[ Home ]"
	kSumColFixes   // left-column header: "ФИКСЫ" / "FIXES"
	kSummaryHint2  // updated summary hint mentioning the home button + key row

	// neutral "not needed" reason prefix on a benign StatusSkip fix row
	kFixNotNeeded // "не требуется" / "not needed"

	// --- terminal screen (phaseTerminal, 2a) ----------------------------------
	kDashTermButton  // dashboard action button: "Терминал ▸" / "Terminal ▸"
	kDashFilesButton // dashboard action button: "Файлы ▸" / "Files ▸" (opens the workspace on the Files tab)
	kTermTitle       // terminal box title suffix: "Терминал" / "Terminal"
	kTermHint        // terminal control hint (Ctrl+Q to exit)
	kTermBackHint    // reconnect + back hint shown on the error/ended notice
	kTermDialFail    // dial/setup failure prefix
	kTermEnded       // "Сессия завершена" / "Session ended"

	// --- file manager tab (phaseTerminal, wsFiles, 2b) ------------------------
	kNavHome       // global tab bar leading cell: "Главная" / "Home" (→ phaseDashboard)
	kFmTabTerminal // tab strip: "Терминал" / "Terminal"
	kFmTabFiles    // tab strip: "Файлы" / "Files"
	kFmColName     // listing column header: "Имя" / "Name"
	kFmColSize     // listing column header: "Размер" / "Size"
	kFmColPerms    // listing column header: "Права" / "Perms"
	kFmColMTime    // listing column header: "Изменён" / "Modified"
	kFmEmpty       // empty-listing placeholder: "(пусто)" / "(empty)"
	kFmHint        // FM control hint
	kFmActNew      // action pill: "Создать ▾" / "New ▾"
	kFmActOpen     // action pill: "Открыть" / "Open"
	kFmActDownload // action pill: "Скачать" / "Download"
	kFmActUpload   // action pill: "Загрузить" / "Upload"
	kFmActRename   // action pill: "Переименовать" / "Rename"
	kFmActDelete   // action pill: "Удалить" / "Delete"

	// FM mutating-op prompts / confirms / notices (2b ops).
	kFmPromptNewDir     // prompt: new folder name
	kFmPromptNewFile    // prompt: new file name
	kFmPromptRename     // prompt: new name
	kFmPromptChmod      // prompt: chmod mode
	kFmPromptChown      // prompt: chown spec (user[:group])
	kFmConfirmDelete    // confirm prefix: "Delete"
	kFmConfirmOverwrite // confirm prefix: "Overwrite"
	kFmConfirmYesNo     // confirm key hint: "y — yes · any other — no"
	kFmCopied           // notice prefix: "Copied"
	kFmCut              // notice prefix: "Cut"
	kFmOpsHint          // ops shortcut hint line

	// FM byte-transfer (download/upload) prompts + notices.
	kFmPromptDownload // prompt: local destination path
	kFmPromptUpload   // prompt: local source path
	kFmDownloaded     // notice prefix: "downloaded"
	kFmUploaded       // notice prefix: "uploaded"
	kFmUploadNoFile   // error: local file not found
	kFmTransferring   // notice: "transferring …"

	// FM local-open-and-sync (2c) notices.
	kFmOpened       // notice prefix: "Opened locally:"
	kFmOpenedBinary // notice prefix: "Opened binary file (edit at your own risk):"
	kFmOpenError    // error: "Open failed"
	kFmSynced       // notice prefix: "Synced back:"
	kFmConflict     // error: remote changed since open — press P to overwrite

	// FM context menu (right-click / 'm').
	kFmMenuTitle    // menu box title
	kFmMenuHint     // menu key hint: "↑/↓ · enter · esc"
	kFmMenuNewDir   // menu item: New folder
	kFmMenuNewFile  // menu item: New file
	kFmMenuOpen     // menu item: Open (disabled until 2c)
	kFmMenuRename   // menu item: Rename
	kFmMenuDelete   // menu item: Delete
	kFmMenuCopy     // menu item: Copy
	kFmMenuCut      // menu item: Cut
	kFmMenuPaste    // menu item: Paste
	kFmMenuChmod    // menu item: chmod
	kFmMenuChown    // menu item: chown
	kFmMenuProps    // menu item: Properties
	kFmMenuCopyPath // menu item: Copy path
	kFmMenuDownload // menu item: Download
	kFmMenuUpload   // menu item: Upload

	// FM op error/notice fallbacks (produced inside fileSession methods).
	kFmErrOpFailed   // generic op failure (no stderr)
	kFmErrStatFailed // stat failure (no stderr)
	kFmErrNoConn     // no live SSH connection
	kFmErrClipboard  // host clipboard write failed (prefix)
	kFmCopiedPath    // copy-path success notice (prefix)
)

// tr is the translation table: every key carries both languages as {ru, en},
// indexed by Lang.
var tr = map[stringKey][2]string{
	kLabelHost:     {"Хост", "Host"},
	kLabelPort:     {"Порт", "Port"},
	kLabelUser:     {"Пользователь", "User"},
	kLabelPassword: {"Пароль", "Password"},
	kLabelKey:      {"SSH-ключ", "SSH key"},

	kPhHost: {"1.2.3.4", "1.2.3.4"},
	kPhPort: {"22", "22"},
	kPhUser: {"root", "root"},
	kPhPass: {"пароль SSH", "ssh password"},
	kPhKey:  {"путь к приватному ключу (пусто — использовать пароль)", "private key path (leave empty to use password)"},

	kStart:      {"Подключиться", "Connect"},
	kCancel:     {"Отмена", "Cancel"},
	kBackToMain: {" ↩  Назад в меню ", " ↩  Back to main "},

	kFormHint:       {"tab/↑↓ переход · ←/→ переключить · enter: следующее поле, подключение (на «Подключиться») · l: язык · esc выход", "tab/↑↓ move · ←/→ toggle · enter: next field, connect (on Connect) · l: lang · esc quit"},
	kRunHintRunning: {"esc назад · l: язык · ctrl+c выход", "esc back · l: lang · ctrl+c quit"},
	kRunHintIdle:    {"enter/esc назад · ↑/↓ прокрутка · l: язык · ctrl+c выход", "enter/esc back · ↑/↓ scroll · l: lang · ctrl+c quit"},

	kStepN:     {"Шаг", "Step"},
	kDoneWord:  {"готово", "done"},
	kVerifyTag: {"проверка", "verify"},

	kPwOnInfo: {"вход по SSH: пароль ИЛИ сгенерированный ключ (скопируй его на экране ключа)", "SSH login: password OR the generated key (copy it on the key screen)"},

	kFinishedOK:  {"запуск завершён", "run finished"},
	kFinishedErr: {"завершено с ошибкой: ", "finished with error: "},

	kBenchLine:   {"интернет: %.1f → %.1f МБ/с (%.2fx)", "internet: %.1f → %.1f MB/s (%.2fx)"},
	kSkipsHeader: {"пропущено:", "skipped:"},
	kSkipLine:    {"%s — %s", "%s — %s"},

	kMonReconnecting: {"монитор: переподключение…", "monitor: reconnecting…"},
	kMonTitle:        {" монитор ", " monitor "},

	kFormTitleSuffix: {"", ""},

	kErrInvalidHost:    {"неверный хост %q — введите IP или имя хоста", "invalid host %q — enter an IP or hostname"},
	kErrKeyNotFound:    {"файл ключа не найден: %q — оставьте поле ключа пустым, чтобы использовать пароль", "key file not found: %q — leave SSH key empty to use the password"},
	kErrHostRequired:   {"укажите хост", "host is required"},
	kErrUserRequired:   {"укажите пользователя", "user is required"},
	kErrAuthRequired:   {"требуется пароль или ключ", "either password or key is required"},
	kErrValidationFail: {"ошибка конфигурации: %s", "config error: %s"},

	kTitleHardened: {"защищён", "hardened"},
	kTitleFailed:   {"сбой", "failed"},
	kTitleWarding:  {"защита", "warding"},

	kSumApplied: {"шагов применено %d/%d", "steps applied %d/%d"},
	kSumSkipped: {"пропущено %d", "%d skipped"},
	kSumFailed:  {"ошибок %d", "%d failed"},
	kSumReboots: {"перезагрузок %d", "reboots %d"},
	kSumVerify:  {"проверка %d/%d", "verify %d/%d"},
	kSumProbes:  {"твиков подтверждено %d/%d", "tweaks confirmed %d/%d"},

	kSecPkgKernel: {"ПАКЕТЫ И ЯДРО", "PACKAGES & KERNEL"},
	kSecDiskMem:   {"ДИСК И ПАМЯТЬ", "DISK & MEMORY"},
	kSecNetwork:   {"СЕТЬ", "NETWORK"},
	kSecSecurity:  {"БЕЗОПАСНОСТЬ", "SECURITY"},
	kSecFixes:     {"ПРИМЕНЁННЫЕ ФИКСЫ (клик — описание)", "APPLIED FIXES (click for details)"},

	kRowUpgraded:  {"обновлено пакетов", "upgraded pkgs"},
	kRowKernel:    {"ядро", "kernel"},
	kRowPurged:    {"удалено пакетов", "purged pkgs"},
	kRowDiskUsed:  {"диск занято", "disk used"},
	kRowZram:      {"zram", "zram"},
	kRowSpeed:     {"скорость, MB/s (до зеркала)", "speed, MB/s (to mirror)"},
	kRowPingGW:    {"задержка ДЦ, ms", "datacenter latency, ms"},
	kRowPingNet:   {"интернет, ms", "internet, ms"},
	kRowPorts:     {"открытых портов", "open ports"},
	kRowRootLogin: {"root-вход", "root login"},
	kRowKeyOnly:   {"ssh только по ключу", "ssh key-only"},
	kRowFirewall:  {"файрвол", "firewall"},
	kRowFail2ban:  {"fail2ban", "fail2ban"},
	kZramAdded:    {"добавлен", "added"},
	kYesWord:      {"да", "yes"},
	kNoWord:       {"нет", "no"},

	kWikiHint: {"esc — назад · ↑↓ — прокрутка · l — язык", "esc — back · ↑↓ — scroll · l — lang"},

	kWikiWhat:      {"ЧТО ДЕЛАЕТ", "WHAT IT DOES"},
	kWikiWhy:       {"ЗАЧЕМ", "WHY"},
	kWikiRisk:      {"БЕЗ ЭТОГО", "WITHOUT IT"},
	kWikiOnBox:     {"ЧТО МЕНЯЕТСЯ НА СЕРВЕРЕ", "WHAT CHANGES ON THE SERVER"},
	kWikiRevert:    {"КАК ОТКАТИТЬ", "HOW TO REVERT"},
	kWikiStatus:    {"Статус:", "Status:"},
	kWikiNoDoc:     {"нет описания для этого шага", "no description for this step"},
	kWikiBack:      {"← Назад", "← Back"},
	kWikiProbeWhat: {"ЧТО ПРОВЕРЯЕТ", "WHAT THIS CHECKS"},

	kWikiApplyButton:   {"Применить", "Apply"},
	kWikiRevertButton:  {"Откатить", "Revert"},
	kWikiUpdateButton:  {"Обновить и перезагрузить", "Update & reboot"},
	kWikiUpdateWarn:    {"Система не обновлена — рекомендуем обновить перед применением твиков", "System is not up to date — update before applying tweaks"},
	kWikiUpdateConfirm: {"Полное обновление и перезагрузка (A8) — несколько минут. Enter — обновить и перезагрузить, Esc — отмена.", "Full upgrade and reboot (A8) — several minutes. Enter to update & reboot, Esc to cancel."},

	kStatusApplied:     {"✓ применено", "✓ applied"},
	kStatusCanApply:    {"• можно", "• available"},
	kStatusUnavailable: {"⊘ недоступно", "⊘ unavailable"},

	kKeyTitle:    {"Доступ по SSH-ключу", "SSH key access"},
	kKeyWarnSoft: {"Вход по логину и паролю (root и от хостинга) СОХРАНЁН — доступ ты не потеряешь. Этот ключ — дополнительный способ входа, можешь сохранить его (необязательно).", "Password login (root and your hosting login) is KEPT — you won't lose access. This key is an extra way in; save it if you like (optional)."},
	kKeyConnHint: {"Подключение:", "Connect:"},
	kKeyCopyBtn:  {"Скопировать ключ", "Copy key"},
	kKeyCopied:   {"✓ скопировано", "✓ copied"},
	kKeyCopyFail: {"не удалось скопировать — выдели вручную", "copy failed — select manually"},
	kKeyHint:     {"esc — назад · c — копировать · l — язык", "esc — back · c — copy · l — lang"},

	kSaveLogLabel: {"Сохранять лог в файл", "Save log to file"},
	kSaveLogOn:    {"да", "yes"},
	kSaveLogOff:   {"нет", "no"},

	kTweakApplied:    {"применён", "applied"},
	kTweakNotApplied: {"не применён", "not applied"},
	kTweakSummary:    {"%d применено / %d нет", "%d applied / %d missing"},
	kMatrixHint:      {"↑/↓ прокрутка · esc назад", "↑/↓ scroll · esc back"},

	kDisclosureLabel: {"▸ Дополнительно (порт · пользователь · SSH-ключ)", "▸ Advanced (port · user · SSH key)"},
	kDisclosureOpen:  {"▼", "▼"},
	kVersionTagline:  {"VPS guardian · защита свежего Ubuntu VPS", "VPS guardian · fresh Ubuntu VPS protection"},

	kUpdateChecking:    {"Обновления: проверка… ⠋", "Updates: checking… ⠋"},
	kUpdateCurrent:     {"Обновления: ✓ установлена последняя версия", "Updates: ✓ latest version installed"},
	kUpdateAvailable:   {"Обновления: v%s доступна", "Updates: v%s available"},
	kUpdateError:       {"не удалось проверить (офлайн)", "failed to check (offline)"},
	kUpdateButtonLabel: {"Обновить ⬇", "Update ⬇"},

	kDashTitle:        {"Сервер", "Server"},
	kDashAuditLabel:   {"Анализ твиков", "Analyzing tweaks"},
	kDashAuditStatus:  {"применено %d из %d", "applied %d of %d"},
	kDashCanApply:     {"можно применить %d", "can apply %d"},
	kDashApplyButton:  {"Применить все твики", "Apply all tweaks"},
	kDashSecButton:    {"Безопасность ▸", "Security ▸"},
	kDashOS:           {"ОС", "OS"},
	kDashKernel:       {"Ядро", "Kernel"},
	kDashVirt:         {"Виртуализация", "Virt"},
	kDashPorts:        {"Порты", "Ports"},
	kDashIPv6:         {"IPv6", "IPv6"},
	kDashHint:         {"↑/↓ прокрутка · enter описание твика · esc назад", "↑/↓ scroll · enter tweak detail · esc back"},
	kDashApplyConfirm: {"Включает полное обновление и перезагрузку (A8) — несколько минут. Enter — применить, esc — отмена.", "Includes a full upgrade and reboot (A8) — several minutes. Enter to apply, esc to cancel."},

	kDashServicesTitle: {"Сервисы", "Services"},
	kDashServicesMore:  {"… +%d ещё", "… +%d more"},

	kSecMenuTitle:      {" Безопасность и доступ ", " Security and access "},
	kSecRootLogin:      {"Вход root", "Root login"},
	kSecKeyOnly:        {"Только по ключу", "Key-only"},
	kSecAdmin:          {"Админ", "Admin user"},
	kSecSafeHeader:     {"Безопасно (вход не меняется):", "Safe (access unchanged):"},
	kSecCreateAdmin:    {"Создать админа", "Create admin"},
	kSecCryptoKey:      {"Усилить SSH-крипто + ключ", "Strengthen SSH crypto + key"},
	kSecDangerHeader:   {"⚠ Опасная зона (можно потерять доступ):", "⚠ Danger zone (you may lose access):"},
	kSecKeyOnlyBtn:     {"Вход только по ключу · заблокировать пароль root", "Key-only login · lock the root password"},
	kSecDangerConfirm:  {"Вы потеряете доступ к root SSH, если ключ не сохранён. Покажем ключ перед применением. Enter — показать ключ и применить, esc — отмена.", "You will lose root SSH access if the key is not saved. We will show the key before applying. Enter to show the key and apply, esc to cancel."},
	kSecHint:           {"1/2 — безопасные действия · 3 — опасное · esc назад · l — язык", "1/2 — safe actions · 3 — danger · esc back · l — lang"},
	kSecRootByPassword: {"разрешён по паролю", "allowed by password"},
	kSecAdminAbsent:    {"отсутствует", "absent"},

	kApplyModalTitle:   {"Применить твики?", "Apply tweaks?"},
	kApplyModalBody:    {"Будут применены все доступные твики (сеть, ядро, память, обслуживание).", "This applies all available tweaks (network, kernel, memory, maintenance)."},
	kApplyModalReboot:  {"⚠ Включает полное обновление и ПЕРЕЗАГРУЗКУ (A8): VPN и сервисы прервутся примерно на 1–2 минуты.", "⚠ Includes a full upgrade and a REBOOT (A8): VPN and services bounce for ~1–2 minutes."},
	kApplyModalConfirm: {"[Enter] применить", "[Enter] apply"},
	kApplyModalCancel:  {"[Esc] отмена", "[Esc] cancel"},

	kKeyPreRunWarn:    {"Сохрани этот ключ — без него ты не зайдёшь, если позже отключишь вход по паролю. Ключ существует только в памяти и нигде не сохраняется автоматически.", "Save this key — without it you can't get in if you later disable password login. The key lives only in memory and is never saved automatically."},
	kKeyPreRunButtons: {"[Enter] начать применение   [Esc] отмена", "[Enter] start applying   [Esc] cancel"},
	kKeyPreRunHint:    {"enter — начать · c — копировать ключ · esc — отмена · l — язык", "enter — start · c — copy key · esc — cancel · l — lang"},

	kSecColTitle:   {"SSH-ДОСТУП", "SSH ACCESS"},
	kSumRAM:        {"ОЗУ", "RAM"},
	kSumKeyAdded:   {"ключ добавлен", "key added"},
	kSumKeyShow:    {"ключ ‹показать›", "key ‹show›"},
	kSumHomeButton: {"[ На главную ]", "[ Home ]"},
	kSumColFixes:   {"ФИКСЫ", "FIXES"},
	kSummaryHint2:  {"enter/esc — на главную · клик по фиксу — описание · ↑↓ — прокрутка · l — язык", "enter/esc — home · click a fix for details · ↑↓ — scroll · l — lang"},

	kFixNotNeeded: {"не требуется", "not needed"},

	kDashTermButton:  {"Терминал ▸", "Terminal ▸"},
	kDashFilesButton: {"Файлы ▸", "Files ▸"},
	kTermTitle:       {"Терминал", "Terminal"},
	kTermHint:        {"SSH-терминал · колесо / shift+pgup — прокрутка · ctrl+1/2/3 вкладки · ctrl+q — выход · l — язык", "SSH terminal · wheel / shift+pgup to scroll · ctrl+1/2/3 tabs · ctrl+q to exit · l language"},
	kTermBackHint:    {"r — переподключиться · esc / ctrl+q — назад", "r to reconnect · esc / ctrl+q to go back"},
	kTermDialFail:    {"не удалось подключиться", "could not connect"},
	kTermEnded:       {"Сессия завершена", "Session ended"},

	kNavHome:       {"Главная", "Home"},
	kFmTabTerminal: {"Терминал", "Terminal"},
	kFmTabFiles:    {"Файлы", "Files"},
	kFmColName:     {"Имя", "Name"},
	kFmColSize:     {"Размер", "Size"},
	kFmColPerms:    {"Права", "Perms"},
	kFmColMTime:    {"Изменён", "Modified"},
	kFmEmpty:       {"(пусто)", "(empty)"},
	kFmHint:        {"файлы · ↑/↓ выбор · enter — открыть · ctrl+1/2/3 вкладки · ctrl+q выход", "files · ↑/↓ select · enter open · ctrl+1/2/3 tabs · ctrl+q exit"},
	kFmActNew:      {"Создать ▾", "New ▾"},
	kFmActOpen:     {"Открыть", "Open"},
	kFmActDownload: {"Скачать", "Download"},
	kFmActUpload:   {"Загрузить", "Upload"},
	kFmActRename:   {"Переименовать", "Rename"},
	kFmActDelete:   {"Удалить", "Delete"},

	kFmPromptNewDir:     {"Имя новой папки:", "New folder name:"},
	kFmPromptNewFile:    {"Имя нового файла:", "New file name:"},
	kFmPromptRename:     {"Новое имя:", "New name:"},
	kFmPromptChmod:      {"Права (chmod), напр. 644:", "Mode (chmod), e.g. 644:"},
	kFmPromptChown:      {"Владелец (chown), напр. root:root:", "Owner (chown), e.g. root:root:"},
	kFmConfirmDelete:    {"Удалить", "Delete"},
	kFmConfirmOverwrite: {"Перезаписать", "Overwrite"},
	kFmConfirmYesNo:     {"y — да · любая другая — нет · esc — отмена", "y — yes · any other — no · esc — cancel"},
	kFmCopied:           {"Скопировано:", "Copied:"},
	kFmCut:              {"Вырезано:", "Cut:"},
	kFmOpsHint:          {"n папка · N файл · r имя · d удал · c копир · x вырез · v встав · g права · o влад · p инфо · y путь · w скач · u загр", "n dir · N file · r name · d del · c copy · x cut · v paste · g perms · o own · p info · y path · w get · u put"},

	kFmPromptDownload: {"Скачать в (локальный путь):", "Download to (local path):"},
	kFmPromptUpload:   {"Загрузить файл (локальный путь):", "Upload file (local path):"},
	kFmDownloaded:     {"скачано:", "downloaded:"},
	kFmUploaded:       {"загружено:", "uploaded:"},
	kFmUploadNoFile:   {"локальный файл не найден", "local file not found"},
	kFmTransferring:   {"передача", "transferring"},

	kFmOpened:       {"Открыто локально:", "Opened locally:"},
	kFmOpenedBinary: {"Открыт бинарный файл (правка на свой риск):", "Opened binary file (edit at your own risk):"},
	kFmOpenError:    {"Ошибка открытия", "Open failed"},
	kFmSynced:       {"Синхронизировано:", "Synced back:"},
	kFmConflict:     {"Удалённый файл изменился — нажмите P для перезаписи", "Remote changed — press P to overwrite"},

	kFmMenuTitle:    {"Действия", "Actions"},
	kFmMenuHint:     {"↑/↓ — выбор · enter — выполнить · esc — закрыть", "↑/↓ select · enter run · esc close"},
	kFmMenuNewDir:   {"Создать папку", "New folder"},
	kFmMenuNewFile:  {"Создать файл", "New file"},
	kFmMenuOpen:     {"Открыть", "Open"},
	kFmMenuRename:   {"Переименовать", "Rename"},
	kFmMenuDelete:   {"Удалить", "Delete"},
	kFmMenuCopy:     {"Копировать", "Copy"},
	kFmMenuCut:      {"Вырезать", "Cut"},
	kFmMenuPaste:    {"Вставить", "Paste"},
	kFmMenuChmod:    {"Права (chmod)", "Permissions (chmod)"},
	kFmMenuChown:    {"Владелец (chown)", "Owner (chown)"},
	kFmMenuProps:    {"Свойства", "Properties"},
	kFmMenuCopyPath: {"Копировать путь", "Copy path"},
	kFmMenuDownload: {"Скачать", "Download"},
	kFmMenuUpload:   {"Загрузить", "Upload"},

	kFmErrOpFailed:   {"операция не выполнена", "operation failed"},
	kFmErrStatFailed: {"не удалось получить свойства", "stat failed"},
	kFmErrNoConn:     {"нет соединения", "no connection"},
	kFmErrClipboard:  {"буфер обмена:", "clipboard:"},
	kFmCopiedPath:    {"путь скопирован:", "copied path:"},
}

// pick resolves one entry of a translation table: the pair is indexed by Lang
// (langRU, langEN), so both languages are structurally mandatory — a key added
// to one is a compile error unless the other is filled too. Falls back to
// English, then to def, when the requested slot is empty. All five tables below
// share this lookup shape.
func pick[K comparable](tbl map[K][2]string, lang Lang, k K, def string) string {
	p := tbl[k]
	if p[lang] != "" {
		return p[lang]
	}
	if p[langEN] != "" {
		return p[langEN]
	}
	return def
}

// t looks up key k for the given language, falling back to English then to "".
func t(lang Lang, k stringKey) string { return pick(tr, lang, k, "") }

// stepTitles maps each engine step ID (the curID streamed in progress events) to a
// SHORT localized name for the top progress line. The full engine Title() (e.g.
// "Firewall + fail-safe (iptables-nft, v4+v6)") runs off the right edge, so the
// progress line shows this compact form instead. The canonical meaning of each ID
// is grounded in the step's Title() in internal/steps/*.go (and the engine's KEY/
// DETECT pseudo-steps in internal/engine/engine.go); names are kept terse so the
// step name always fits beside the counter+bar+percent. IDs not listed fall back to
// the engine-provided Title via localStepTitle.
var stepTitles = map[string][2]string{
	"PRE":    {"Подготовка", "Preconditions"},        // §1 Preconditions
	"KEY":    {"SSH-ключ", "SSH key"},                // generate ed25519 + switch to key auth
	"DETECT": {"Разведка", "Discovery"},              // §0.5/§2 pre-flight discovery
	"A1":     {"Файрвол", "Firewall"},                // Firewall + fail-safe (iptables-nft)
	"A2":     {"SSH", "SSH"},                         // SSH crypto hardening
	"A2.5":   {"Cloud-init", "Cloud-init"},           // cloud-init neutralization
	"A3":     {"fail2ban", "fail2ban"},               // fail2ban
	"A4":     {"Сеть/BBR", "Network/BBR"},            // network tuning (BBR, buffers)
	"A5":     {"Ядро", "Kernel"},                     // kernel hardening (sysctl)
	"A6":     {"Обслуживание", "Maintenance"},        // maintenance (journald, ntp, …)
	"A6.5":   {"DNS", "DNS"},                         // DNS hardening (DoT/DNSSEC)
	"A6.7":   {"Память", "Memory"},                   // memory mgmt (ZRAM + earlyoom)
	"A7":     {"Очистка", "Cleanup"},                 // cleanup (purge bloatware)
	"A8":     {"Обновление+ребут", "Upgrade+reboot"}, // full upgrade + reboot
	"A9":     {"Автообновления", "Auto-updates"},     // unattended security updates
	"A10":    {"Аудит", "Audit"},                     // detection (auditd, login-notify)
}

// localStepTitle returns the SHORT localized name for step id in lang, falling back
// to fallback (the engine-provided Title) when the id is unknown.
func localStepTitle(lang Lang, id, fallback string) string {
	return pick(stepTitles, lang, id, fallback)
}

// skipReasons maps the static (non-dynamic) English skip detail a step returns with
// StatusSkip to a localized "why it wasn't needed" reason. Every StatusSkip in
// internal/steps is benign (target absent / already satisfied), so this reads as a
// neutral "не требуется" state, not a failure. Dynamic details (ufw-managed port
// lists, "manages the firewall") carry their own data and are left as-is by
// localSkipReason's fallback. The EN slot is deliberately empty: English uses
// the raw detail verbatim (it is already English).
var skipReasons = map[string][2]string{
	"cloud-init not installed":                            {"cloud-init отсутствует", ""},
	"cloud-init already disabled":                         {"cloud-init уже отключён", ""},
	"systemd-resolved not active (different resolver)":    {"systemd-resolved не активен (другой резолвер)", ""},
	"firewall already closed with SSH open and persisted": {"файрвол уже закрыт, SSH открыт", ""},
}

// localSkipReason returns the localized "not needed" reason for a step's skip detail
// in lang, falling back to the raw detail unchanged for dynamic reasons (and for EN,
// where the detail is already English).
func localSkipReason(lang Lang, detail string) string {
	return pick(skipReasons, lang, detail, detail)
}

// tweakNames maps a tweaks.Probe.ID to its localized display name. Missing IDs
// fall through to the probe's English Name (see localTweakName); the EN slot is
// deliberately empty for the same reason — no override needed.
var tweakNames = map[string][2]string{
	"a1.input_drop":     {"Политика INPUT DROP", ""},
	"a1.ssh_accept":     {"Порт SSH разрешён", ""},
	"a1.rules_v4":       {"Правила v4 сохранены", ""},
	"a1.rules_v6":       {"Правила v6 сохранены", ""},
	"a1.persistent":     {"iptables-persistent", ""},
	"a2.conf00":         {"00-hardening.conf", ""},
	"a2.conf99":         {"99-hardening.conf", ""},
	"a2.allowgroups":    {"AllowGroups sshusers", ""},
	"a2.ecdsa_absent":   {"ECDSA host-key удалён", ""},
	"a2.ssh_active":     {"Служба ssh активна", ""},
	"a2.permitroot":     {"PermitRootLogin", ""},
	"a2.passauth":       {"Парольный вход", ""},
	"a2.kex_mlkem":      {"PQ-обмен ключей (mlkem768)", ""},
	"a25.disabled":      {"cloud-init отключён", ""},
	"a3.installed":      {"fail2ban установлен", ""},
	"a3.jail_local":     {"jail.local", ""},
	"a3.sshd_jail":      {"Джейл sshd активен", ""},
	"a4.net_tune":       {"99-net-tune.conf", ""},
	"a4.bbr_conf":       {"99-bbr.conf", ""},
	"a4.bbr_module":     {"Модуль tcp_bbr загружен", ""},
	"a4.bbr_active":     {"Контроль перегрузки BBR", ""},
	"a4.qdisc":          {"Очередь fq по умолчанию", ""},
	"a4.io_sched":       {"Планировщик I/O (udev)", ""},
	"a5.harden_conf":    {"99-zz-kernel-harden.conf", ""},
	"a5.core_pattern":   {"core_pattern отключён", ""},
	"a5.rp_filter":      {"rp_filter строгий", ""},
	"a5.kptr":           {"kptr_restrict", ""},
	"a5.thp":            {"THP madvise", ""},
	"a6.journald":       {"Лимит journald", ""},
	"a6.needrestart":    {"needrestart авто", ""},
	"a6.nofile":         {"Лимит NOFILE", ""},
	"a6.ntp":            {"NTP включён", ""},
	"a65.dns_conf":      {"Защита DNS (resolved)", ""},
	"a65.dot":           {"DNSOverTLS opportunistic", ""},
	"a67.zram_conf":     {"zram-generator.conf", ""},
	"a67.zram_sysctl":   {"zram swappiness", ""},
	"a67.zram_active":   {"zram-своп активен", ""},
	"a67.earlyoom":      {"earlyoom активен", ""},
	"a9.installed":      {"unattended-upgrades", ""},
	"a9.auto":           {"20auto-upgrades", ""},
	"a9.local":          {"52-unattended-upgrades-local", ""},
	"a10.auditd":        {"auditd установлен", ""},
	"a10.audit_rules":   {"99-vps.rules", ""},
	"a10.auditd_active": {"auditd активен", ""},
	"a10.notify":        {"ssh-login-notify", ""},
	"a10.pam":           {"pam.d/sshd уведомление", ""},
	"a10.log_rule":      {"LOG входящих (firewall)", ""},
}

// localTweakName returns the localized name for a probe ID, or fallback (the
// probe's English Name) when no localized entry exists.
func localTweakName(lang Lang, id, fallback string) string {
	return pick(tweakNames, lang, id, fallback)
}

// probeDescs holds a short, per-PROBE description keyed by tweaks.Probe.ID, in
// both languages. Unlike the step-level wiki.Doc (shared by every probe of a
// step), each entry names the concrete artifact THIS probe verifies — a file,
// sysctl key, systemd service, dpkg package or iptables rule — so the wiki/detail
// screen explains the individual check, not the whole step. Every probe ID that
// tweaks.Registry can emit (including the version/IPv6-gated a1.rules_v6 and
// a2.kex_mlkem) must have an entry in BOTH languages — see TestEveryProbeHasDesc.
var probeDescs = map[string][2]string{
	// --- A1 firewall ---
	"a1.input_drop": {"Проверяет базовую политику цепочки INPUT в iptables (строка -P INPUT). По умолчанию iptables принимает всё; политика DROP делает наоборот — отбрасывать весь входящий трафик, кроме явно разрешённого. Применение твика ставит -P INPUT DROP, превращая файрвол в модель «запрещено всё, что не разрешено».", "Checks the iptables INPUT chain's default policy (the -P INPUT line). By default iptables accepts everything; a DROP policy flips that to reject all inbound traffic unless explicitly allowed. Applying the tweak sets -P INPUT DROP, turning the firewall into a default-deny posture."},
	"a1.ssh_accept": {"Проверяет, что в цепочке INPUT есть правило ACCEPT именно для вашего SSH-порта. При политике DROP без такого правила-исключения вы потеряли бы доступ к серверу сразу после включения файрвола. Применение твика добавляет ACCEPT на SSH-порт раньше, чем включается DROP, — это страховка от самоблокировки.", "Checks the INPUT chain has an ACCEPT rule for your specific SSH port. Under a DROP policy, without this exception rule you would be locked out the instant the firewall comes up. Applying the tweak inserts the SSH ACCEPT before DROP takes effect — the lockout safeguard."},
	"a1.rules_v4":   {"Проверяет наличие файла /etc/iptables/rules.v4 — снимка действующих правил IPv4. Правила в памяти ядра живут только до перезагрузки; этот файл — то, что iptables-persistent восстанавливает при загрузке. Применение твика сохраняет текущий набор правил в rules.v4, чтобы файрвол поднимался автоматически после ребута.", "Checks for /etc/iptables/rules.v4 — a snapshot of the live IPv4 ruleset. Rules in the kernel only last until reboot; this file is what iptables-persistent restores at boot. Applying the tweak saves the current ruleset to rules.v4 so the firewall comes back automatically after a reboot."},
	"a1.rules_v6":   {"Проверяет наличие файла /etc/iptables/rules.v6 — сохранённого набора правил IPv6 (проверка появляется только если у сервера есть глобальный IPv6). Без него IPv6-стек остался бы без файрвола после перезагрузки. Применение твика сохраняет правила ip6tables, чтобы IPv6 защищался так же, как IPv4.", "Checks for /etc/iptables/rules.v6 — the saved IPv6 ruleset (this probe appears only when the server has a global IPv6). Without it the IPv6 stack would come up unprotected after a reboot. Applying the tweak persists the ip6tables rules so IPv6 is firewalled just like IPv4."},
	"a1.persistent": {"Проверяет через dpkg, что установлен пакет iptables-persistent. Именно его служба загружает rules.v4/rules.v6 на старте системы; без пакета сохранённые файлы правил никто не применит. Применение твика ставит пакет, замыкая цепочку «правила сохранены → правила восстановлены при загрузке».", "Checks via dpkg that the iptables-persistent package is installed. Its service is what loads rules.v4/rules.v6 at boot; without the package the saved rule files are never applied. Applying the tweak installs it, closing the loop from saved rules to rules restored at boot."},

	// --- A2 ssh ---
	"a2.conf00":       {"Проверяет наличие drop-in /etc/ssh/sshd_config.d/00-hardening.conf — первого фрагмента усиленной конфигурации sshd. Префикс 00 гарантирует ранний порядок подключения. В нём задаются базовые параметры криптостойкости; применение твика создаёт этот файл, не трогая основной sshd_config.", "Checks for the drop-in /etc/ssh/sshd_config.d/00-hardening.conf — the first fragment of the hardened sshd config. The 00 prefix makes it load early. It carries the baseline crypto-strength settings; applying the tweak creates this file without touching the main sshd_config."},
	"a2.conf99":       {"Проверяет наличие drop-in /etc/ssh/sshd_config.d/99-hardening.conf — финального фрагмента конфигурации sshd. Префикс 99 ставит его последним, чтобы он перекрывал дефолты дистрибутива и другие drop-in. Применение твика кладёт сюда итоговые жёсткие значения (алгоритмы, тайм-ауты, доступ).", "Checks for the drop-in /etc/ssh/sshd_config.d/99-hardening.conf — the final sshd config fragment. The 99 prefix makes it load last so it overrides distro defaults and other drop-ins. Applying the tweak writes the decisive hardened values here (algorithms, timeouts, access)."},
	"a2.allowgroups":  {"Проверяет через sshd -T действующее значение AllowGroups: вход по SSH разрешён только членам группы sshusers, все остальные отсекаются ещё до проверки пароля/ключа. Информационно: на безопасном пути образ не трогается, поэтому может быть не задано — это не ошибка. Значение становится sshusers только после жёсткой блокировки A2 (или strict-режима).", "Checks the effective AllowGroups via sshd -T: SSH login is restricted to members of the sshusers group, rejecting everyone else before any password/key check. Informational: the safe path leaves the image untouched, so this may be unset — that is not a failure. It only becomes sshusers after the opt-in A2 lockdown (or strict mode)."},
	"a2.ecdsa_absent": {"Проверяет, что host-key /etc/ssh/ssh_host_ecdsa_key удалён с сервера. ECDSA-ключ хоста опирается на кривые NIST, к которым есть вопросы доверия; оставляя только Ed25519, мы сужаем поверхность и стандартизируем отпечаток сервера. Применение твика удаляет ECDSA-ключ и перезапускает sshd.", "Checks that the host key /etc/ssh/ssh_host_ecdsa_key has been removed. The ECDSA host key relies on NIST curves of debated trust; keeping only Ed25519 shrinks the surface and standardizes the server fingerprint. Applying the tweak deletes the ECDSA key and restarts sshd."},
	"a2.ssh_active":   {"Проверяет через systemctl is-active, что служба ssh (или sshd на некоторых сборках) запущена и работает. Это базовый контроль живости: если после изменения конфигурации демон не поднялся, доступ был бы потерян. Проба подтверждает, что sshd активен с применённой конфигурацией.", "Checks via systemctl is-active that the ssh (or sshd on some builds) service is up and running. This is the basic liveness gate: if the daemon failed to come back after a config change, access would be lost. The probe confirms sshd is active with the applied config."},
	"a2.permitroot":   {"Читает действующее значение PermitRootLogin из sshd -T — разрешён ли прямой вход root по SSH. Информационно: в режиме soft ожидается prohibit-password (root только по ключу, не по паролю), в strict — no (root по SSH запрещён полностью). На безопасном пути образ не меняется, поэтому несовпадение не считается ошибкой.", "Reads the effective PermitRootLogin from sshd -T — whether direct root SSH login is allowed. Informational: soft mode expects prohibit-password (root by key only, never password), strict expects no (root SSH fully disabled). The safe path leaves the image value alone, so a mismatch is not a failure."},
	"a2.passauth":     {"Читает действующее значение PasswordAuthentication из sshd -T — пускают ли на сервер по паролю. Информационно: в soft пароль остаётся включён (yes), чтобы не потерять доступ, в strict выключается (no) и остаётся только вход по ключу. На безопасном пути значение из образа не трогается.", "Reads the effective PasswordAuthentication from sshd -T — whether the server accepts password logins. Informational: soft keeps it on (yes) so you don't lose access, strict turns it off (no) leaving key-only login. The safe path does not touch the image value."},
	"a2.kex_mlkem":    {"Проверяет через sshd -T, что в KexAlgorithms включён постквантовый обмен ключами mlkem768x25519-sha256 (проба есть только на Ubuntu 26.04). Гибридный обмен защищает сессию от будущей расшифровки квантовым компьютером по схеме «перехвати сейчас — расшифруй потом». Применение твика добавляет этот алгоритм первым в список KEX.", "Checks via sshd -T that the post-quantum key exchange mlkem768x25519-sha256 is enabled in KexAlgorithms (this probe exists on Ubuntu 26.04 only). The hybrid exchange protects the session against future quantum decryption of a recorded handshake (\"harvest now, decrypt later\"). Applying the tweak puts this algorithm first in the KEX list."},

	// --- A2.5 cloud-init ---
	"a25.disabled": {"Проверяет наличие файла-флага /etc/cloud/cloud-init.disabled (либо что cloud-init вовсе не установлен). cloud-init при каждой загрузке может переписывать сеть, пользователей и SSH по шаблону провайдера, откатывая нашу настройку. Применение твика создаёт файл-флаг, нейтрализуя cloud-init, чтобы изменения переживали перезагрузку.", "Checks for the flag file /etc/cloud/cloud-init.disabled (or that cloud-init isn't installed at all). On every boot cloud-init can rewrite network, users and SSH from the provider template, reverting our hardening. Applying the tweak drops the flag file, neutralizing cloud-init so changes survive a reboot."},

	// --- A3 fail2ban ---
	"a3.installed":  {"Проверяет через dpkg, что установлен пакет fail2ban — демон, который читает журналы и банит IP после серии неудачных входов. Без него перебор пароля по SSH ничем не ограничен. Применение твика ставит пакет, давая основу для защиты от брутфорса.", "Checks via dpkg that the fail2ban package is installed — the daemon that reads logs and bans IPs after a run of failed logins. Without it, SSH password guessing is unthrottled. Applying the tweak installs the package, providing the basis for brute-force protection."},
	"a3.jail_local": {"Проверяет наличие файла /etc/fail2ban/jail.local — локальной конфигурации, переопределяющей дефолтный jail.conf. Здесь включается джейл sshd, задаются порог и время бана, белый список доверенных IP. Применение твика кладёт jail.local с нашими параметрами, не трогая пакетный jail.conf.", "Checks for /etc/fail2ban/jail.local — the local config that overrides the default jail.conf. It enables the sshd jail, sets the ban threshold and duration, and the trusted-IP whitelist. Applying the tweak writes jail.local with our parameters, leaving the packaged jail.conf untouched."},
	"a3.sshd_jail":  {"Проверяет через fail2ban-client status sshd, что джейл sshd реально загружен в работающем демоне, а не просто описан в конфиге. Только активный джейл действительно отслеживает неудачные входы и выставляет баны. Проба подтверждает, что защита SSH от брутфорса включена и работает.", "Checks via fail2ban-client status sshd that the sshd jail is actually loaded in the running daemon, not merely written in config. Only an active jail truly watches failed logins and issues bans. The probe confirms SSH brute-force protection is live."},

	// --- A4 network ---
	"a4.net_tune":   {"Проверяет наличие /etc/sysctl.d/99-net-tune.conf — sysctl-настроек сетевого стека: размеры буферов сокетов, длины очередей, параметры TCP. На дефолтных значениях быстрые каналы недоиспользуются. Применение твика кладёт этот файл, поднимая пропускную способность под VPS-нагрузку.", "Checks for /etc/sysctl.d/99-net-tune.conf — sysctl settings for the network stack: socket buffer sizes, queue lengths, TCP parameters. On defaults, fast links are under-utilized. Applying the tweak drops this file, raising throughput for VPS workloads."},
	"a4.bbr_conf":   {"Проверяет наличие /etc/sysctl.d/99-bbr.conf — файла, который при загрузке задаёт алгоритм контроля перегрузки BBR и дисциплину очереди fq. Это декларативная часть включения BBR через sysctl. Применение твика создаёт файл, чтобы настройки переживали перезагрузку.", "Checks for /etc/sysctl.d/99-bbr.conf — the file that at boot sets the BBR congestion-control algorithm and the fq queueing discipline. This is the declarative half of enabling BBR via sysctl. Applying the tweak creates the file so the settings survive a reboot."},
	"a4.bbr_module": {"Проверяет через lsmod, что модуль ядра tcp_bbr загружен в данный момент. Без модуля в памяти ядро не сможет применить алгоритм BBR, даже если он прописан в sysctl. Применение твика загружает модуль и прописывает его автозагрузку.", "Checks via lsmod that the tcp_bbr kernel module is loaded right now. Without the module in the kernel, BBR can't be applied even if it's set in sysctl. Applying the tweak loads the module and sets it to autoload."},
	"a4.bbr_active": {"Читает sysctl net.ipv4.tcp_congestion_control — реально действующий алгоритм контроля перегрузки. BBR заметно лучше старого cubic держит скорость на каналах с потерями и большой задержкой. Проба подтверждает, что эффективное значение — bbr, а не просто прописано в файле.", "Reads sysctl net.ipv4.tcp_congestion_control — the actually-active congestion-control algorithm. BBR holds throughput far better than the old cubic on lossy, high-latency links. The probe confirms the effective value is bbr, not just present in a file."},
	"a4.qdisc":      {"Читает sysctl net.core.default_qdisc — дисциплину очереди пакетов по умолчанию. Для корректной работы BBR нужна именно fq (fair queue), иначе выигрыш от алгоритма теряется. Проба подтверждает, что действующее значение — fq.", "Reads sysctl net.core.default_qdisc — the default packet queueing discipline. BBR needs fq (fair queue) to work correctly, otherwise its benefit is lost. The probe confirms the effective value is fq."},
	"a4.io_sched":   {"Проверяет наличие udev-правила /etc/udev/rules.d/60-io-scheduler.rules на виртуальных дисках vd* (на других типах дисков проверка неприменима и помечается «na»). Правило выставляет планировщик ввода-вывода, оптимальный для виртуального хранилища. Применение твика кладёт это правило udev.", "Checks for the udev rule /etc/udev/rules.d/60-io-scheduler.rules on virtual vd* disks (not applicable on other disk types, where it reports \"na\"). The rule sets the I/O scheduler best suited to virtualized storage. Applying the tweak installs this udev rule."},

	// --- A5 kernel ---
	"a5.harden_conf":  {"Проверяет наличие /etc/sysctl.d/99-zz-kernel-harden.conf — общего файла с набором sysctl-параметров усиления ядра. Префикс zz ставит его последним, чтобы перекрыть менее строгие значения. Применение твика кладёт файл; остальные пробы A5 проверяют отдельные ключи из этого набора.", "Checks for /etc/sysctl.d/99-zz-kernel-harden.conf — the umbrella file holding the kernel-hardening sysctl bundle. The zz prefix makes it load last to override looser values. Applying the tweak drops the file; the other A5 probes verify individual keys from this bundle."},
	"a5.core_pattern": {"Читает sysctl kernel.core_pattern — куда ядро пишет дамп памяти упавшего процесса. Дампы могут содержать пароли и ключи и раздувать диск, поэтому их перенаправляют в /bin/false, то есть отключают. Проба подтверждает, что core_pattern указывает на /bin/false.", "Reads sysctl kernel.core_pattern — where the kernel writes a crashed process's memory dump. Dumps can contain passwords and keys and can bloat the disk, so they are piped to /bin/false, i.e. disabled. The probe confirms core_pattern points at /bin/false."},
	"a5.rp_filter":    {"Читает sysctl net.ipv4.conf.all.rp_filter — режим обратной проверки маршрута. Строгий режим (=1) отбрасывает пакеты с подделанным адресом источника, который не вернулся бы тем же интерфейсом. Применение твика выставляет rp_filter=1 против спуфинга.", "Reads sysctl net.ipv4.conf.all.rp_filter — the reverse-path filtering mode. Strict (=1) drops packets with a spoofed source address that wouldn't return on the same interface. Applying the tweak sets rp_filter=1 against spoofing."},
	"a5.kptr":         {"Читает sysctl kernel.kptr_restrict — насколько скрыты адреса ядра в /proc и логах. Значение 2 прячет указатели ядра ото всех, включая root, чтобы не подсказывать эксплойтам раскладку памяти. Проба подтверждает kptr_restrict=2.", "Reads sysctl kernel.kptr_restrict — how far kernel addresses are hidden in /proc and logs. A value of 2 hides kernel pointers from everyone, including root, so they can't reveal the memory layout to exploits. The probe confirms kptr_restrict=2."},
	"a5.thp":          {"Читает /sys/kernel/mm/transparent_hugepage/enabled — режим прозрачных больших страниц. Режим [madvise] включает большие страницы только там, где приложение их явно запросило, избегая скачков задержки и лишнего расхода памяти от глобального always. Применение твика выставляет madvise.", "Reads /sys/kernel/mm/transparent_hugepage/enabled — the transparent huge pages mode. [madvise] enables huge pages only where an app explicitly asks, avoiding the latency spikes and wasted memory of the global always mode. Applying the tweak sets madvise."},

	// --- A6 maintenance ---
	"a6.journald":    {"Проверяет наличие /etc/systemd/journald.conf.d/99-vps-cap.conf — drop-in с лимитом размера журнала systemd. Без ограничения журнал способен разрастись и забить диск под ноль. Применение твика задаёт потолок (SystemMaxUse), удерживая логи в безопасных рамках.", "Checks for /etc/systemd/journald.conf.d/99-vps-cap.conf — a drop-in capping the systemd journal size. Without a cap the journal can grow and fill the disk completely. Applying the tweak sets a ceiling (SystemMaxUse), keeping logs within safe bounds."},
	"a6.needrestart": {"Проверяет наличие /etc/needrestart/conf.d/50-autorestart.conf. needrestart после обновлений библиотек спрашивает, какие службы перезапустить; на сервере без оператора этот интерактивный запрос вешает автообновление. Применение твика переводит needrestart в неинтерактивный авто-режим.", "Checks for /etc/needrestart/conf.d/50-autorestart.conf. After library updates needrestart asks which services to restart; on an unattended server that interactive prompt stalls auto-upgrades. Applying the tweak switches needrestart to non-interactive auto mode."},
	"a6.nofile":      {"Проверяет наличие /etc/systemd/system.conf.d/limits.conf — drop-in с поднятым лимитом открытых файлов (NOFILE). Дефолтный лимит мал для нагруженных сетевых служб и приводит к ошибкам «too many open files». Применение твика поднимает потолок дескрипторов для служб systemd.", "Checks for /etc/systemd/system.conf.d/limits.conf — a drop-in raising the open-file limit (NOFILE). The default is too low for busy network services and causes \"too many open files\" errors. Applying the tweak raises the descriptor ceiling for systemd services."},
	"a6.ntp":         {"Читает timedatectl и проверяет, что синхронизация времени по NTP включена (NTP=yes). Уход часов ломает TLS-сертификаты, логи и работу fail2ban. Применение твика включает systemd-timesyncd, удерживая системное время точным.", "Reads timedatectl and checks NTP time synchronization is enabled (NTP=yes). Clock drift breaks TLS certificates, logs and fail2ban's correlation. Applying the tweak enables systemd-timesyncd to keep system time accurate."},

	// --- A6.5 DNS ---
	"a65.dns_conf": {"Проверяет наличие /etc/systemd/resolved.conf.d/99-morgward-dns.conf — drop-in для systemd-resolved с заданными доверенными резолверами (проба активна, только если resolved работает). Это фиксирует, через какие DNS-серверы ходит система. Применение твика кладёт файл с нашими резолверами и базовыми настройками безопасности DNS.", "Checks for /etc/systemd/resolved.conf.d/99-morgward-dns.conf — a systemd-resolved drop-in setting trusted resolvers (active only when resolved is running). It pins which DNS servers the system uses. Applying the tweak drops the file with our resolvers and baseline DNS-security settings."},
	"a65.dot":      {"Проверяет, что в том же drop-in задано DNSOverTLS=opportunistic. В этом режиме DNS-запросы шифруются по DNS-over-TLS там, где сервер это поддерживает, скрывая их от перехвата и подмены провайдером. Применение твика включает оппортунистический DoT (без жёсткого требования, чтобы не сломать резолвинг).", "Checks that the same drop-in sets DNSOverTLS=opportunistic. In this mode DNS queries are encrypted over DNS-over-TLS where the server supports it, hiding them from ISP interception and tampering. Applying the tweak enables opportunistic DoT (not strict, so resolution can't break)."},

	// --- A6.7 memory ---
	"a67.zram_conf":   {"Проверяет наличие /etc/systemd/zram-generator.conf — конфигурации сжатого свопа в ОЗУ (ZRAM на алгоритме zstd). ZRAM создаёт быстрый своп прямо в памяти, отодвигая OOM без обращения к медленному диску. Применение твика кладёт конфиг, задавая размер zram-устройства.", "Checks for /etc/systemd/zram-generator.conf — the config for compressed in-RAM swap (ZRAM using zstd). ZRAM creates fast swap inside memory, pushing back OOM without touching the slow disk. Applying the tweak drops the config, sizing the zram device."},
	"a67.zram_sysctl": {"Проверяет наличие /etc/sysctl.d/99-zram.conf — sysctl-настройки vm.swappiness, подстроенной под ZRAM. С быстрым свопом в памяти ядру выгодно охотнее вытеснять страницы, поэтому swappiness повышают. Применение твика кладёт этот sysctl-файл в пару к zram-generator.conf.", "Checks for /etc/sysctl.d/99-zram.conf — a vm.swappiness sysctl tuned for ZRAM. With fast in-RAM swap the kernel can profitably evict pages more eagerly, so swappiness is raised. Applying the tweak drops this sysctl file alongside zram-generator.conf."},
	"a67.zram_active": {"Проверяет через swapon, что zram-устройство реально подключено как своп прямо сейчас, а не только описано в конфиге. Только активный своп даёт защиту от нехватки памяти. Проба подтверждает, что zram-своп смонтирован и используется ядром.", "Checks via swapon that a zram device is actually attached as swap right now, not merely described in config. Only active swap gives real out-of-memory protection. The probe confirms the zram swap is mounted and used by the kernel."},
	"a67.earlyoom":    {"Проверяет через systemctl is-active, что служба earlyoom запущена. earlyoom мягко завершает самый прожорливый процесс ДО того, как штатный OOM-killer ядра подвесит весь сервер при исчерпании памяти. Применение твика ставит и включает earlyoom как страховку от зависаний по памяти.", "Checks via systemctl is-active that the earlyoom service is running. earlyoom gently kills the most memory-hungry process BEFORE the kernel's own OOM killer hangs the whole server under memory pressure. Applying the tweak installs and enables earlyoom as a guard against memory hangs."},

	// --- A9 unattended-upgrades ---
	"a9.installed": {"Проверяет через dpkg, что установлен пакет unattended-upgrades — механизм автоматической установки обновлений безопасности без участия оператора. Без него критические патчи ставятся только вручную и часто запаздывают. Применение твика ставит пакет, давая основу для автообновлений.", "Checks via dpkg that the unattended-upgrades package is installed — the mechanism that auto-installs security updates without operator involvement. Without it, critical patches are manual-only and often lag. Applying the tweak installs the package, the basis for automatic updates."},
	"a9.auto":      {"Проверяет наличие /etc/apt/apt.conf.d/20auto-upgrades — файла, который включает периодические задания apt (обновление списков и накат обновлений). Без него установленный unattended-upgrades ничего не запускает. Применение твика создаёт файл, активируя расписание автообновлений.", "Checks for /etc/apt/apt.conf.d/20auto-upgrades — the file that enables apt's periodic jobs (list refresh and applying upgrades). Without it, an installed unattended-upgrades runs nothing. Applying the tweak creates the file, activating the auto-upgrade schedule."},
	"a9.local":     {"Проверяет наличие /etc/apt/apt.conf.d/52-unattended-upgrades-local — нашей локальной донастройки автообновлений. Здесь отключается авто-перезагрузка (чтобы сервер не ушёл в ребут внезапно) и включается чистка старых ядер. Применение твика кладёт этот файл поверх дефолтов пакета.", "Checks for /etc/apt/apt.conf.d/52-unattended-upgrades-local — our local tuning of auto-upgrades. It disables auto-reboot (so the server never reboots unexpectedly) and enables old-kernel cleanup. Applying the tweak drops this file over the package defaults."},

	// --- A10 detection ---
	"a10.auditd":        {"Проверяет через dpkg, что установлен пакет auditd — демон аудита ядра Linux, фиксирующий доступ к важным файлам и системные события. Без него не остаётся следов для разбора инцидента. Применение твика ставит пакет, давая основу для журнала аудита.", "Checks via dpkg that the auditd package is installed — the Linux kernel audit daemon that records access to sensitive files and system events. Without it there is no trail to investigate an incident. Applying the tweak installs the package, the basis for the audit log."},
	"a10.audit_rules":   {"Проверяет наличие /etc/audit/rules.d/99-vps.rules — набора правил аудита: какие файлы и системные вызовы отслеживать (sudoers, ключи SSH, изменения учёток). Без правил auditd работает, но ничего не пишет. Применение твика кладёт наш набор правил наблюдения.", "Checks for /etc/audit/rules.d/99-vps.rules — the audit ruleset defining which files and syscalls to watch (sudoers, SSH keys, account changes). Without rules, auditd runs but records nothing. Applying the tweak drops our watch ruleset."},
	"a10.auditd_active": {"Проверяет через systemctl is-active, что служба auditd запущена и реально собирает события по загруженным правилам. Установленного, но остановленного демона недостаточно. Проба подтверждает, что аудит работает и журналирует.", "Checks via systemctl is-active that the auditd service is running and actually collecting events against the loaded rules. An installed but stopped daemon isn't enough. The probe confirms auditing is live and logging."},
	"a10.notify":        {"Проверяет наличие скрипта /usr/local/sbin/ssh-login-notify.sh. Скрипт при успешном входе по SSH шлёт уведомление, чтобы чужой логин не остался незамеченным. Применение твика кладёт исполняемый скрипт в системный путь.", "Checks for the script /usr/local/sbin/ssh-login-notify.sh. On a successful SSH login the script sends a notification so an unexpected login doesn't go unnoticed. Applying the tweak installs the executable script in a system path."},
	"a10.pam":           {"Проверяет, что в /etc/pam.d/sshd прописана строка вызова ssh-login-notify через pam_exec. Именно она запускает скрипт уведомления при каждой SSH-сессии; без этой строки скрипт лежит, но не срабатывает. Применение твика добавляет строку в PAM-стек sshd.", "Checks that /etc/pam.d/sshd contains the ssh-login-notify call via pam_exec. That line is what runs the notify script on each SSH session; without it the script sits unused. Applying the tweak adds the line to sshd's PAM stack."},
	"a10.log_rule":      {"Проверяет через iptables, что в цепочке INPUT есть LOG-правило с меткой ipt-drop-in — оно журналирует пакеты, отброшенные политикой DROP. Это даёт видимость сканирований и попыток подключения к закрытым портам. Применение твика добавляет логирующее правило перед финальным DROP.", "Checks via iptables that the INPUT chain has a LOG rule tagged ipt-drop-in — it logs packets the DROP policy rejected. This gives visibility into port scans and attempts on closed ports. Applying the tweak adds the logging rule just before the final DROP."},
}

// probeDesc returns the localized per-probe description for a probe ID and ok=true
// when present, falling back to English then to ok=false when missing. Used by
// wikiBodyLines to render the per-PROBE detail instead of the step-level wiki.Doc.
func probeDesc(lang Lang, id string) (string, bool) {
	s := pick(probeDescs, lang, id, "")
	return s, s != ""
}
