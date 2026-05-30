package wiki

// docs holds one FixDoc per step ID, per language. Every ID that
// engine.orderedSteps() applies must have an entry in BOTH languages
// (see wiki_test.go: TestEveryStepHasDoc). Text is novice-friendly and
// short — each field renders in a narrow terminal pane.
var docs = map[Lang]map[string]FixDoc{
	RU: {
		"PRE": {
			Title:       "Подготовка и проверки",
			What:        "Обновляет список пакетов apt, создаёт администраторского пользователя с sudo и SSH-ключом, проверяет версию ОС и режим защиты.",
			Why:         "Гарантирует, что есть рабочий вход без root и что установка пакетов не упадёт ещё до начала настройки.",
			RiskWithout: "Первая же установка пакета может провалиться, а блокировка SSH без запасного пользователя приведёт к полной потере доступа к серверу.",
		},
		"A1": {
			Title:       "Межсетевой экран",
			What:        "Закрывает все входящие порты кроме SSH через iptables (IPv4 и IPv6) и сохраняет правила до перезагрузки. Перед изменением ставит таймер-страховку, который откатит правила, если связь пропадёт.",
			Why:         "Сервер перестаёт быть виден из интернета по любым службам, кроме нужных, а страховка не даёт случайно заблокировать самого себя.",
			RiskWithout: "Любая служба, случайно слушающая порт, открыта всему интернету для атак и сканирования.",
		},
		"A8": {
			Title:       "Полное обновление и перезагрузка",
			What:        "Устанавливает все доступные обновления системы и перезагружает сервер, подтверждая успешную загрузку по смене boot_id.",
			Why:         "Закрывает известные уязвимости в ядре и пакетах и переводит сервер на свежие версии до тонкой настройки.",
			RiskWithout: "Система остаётся с устаревшим ядром и пакетами, содержащими публично известные дыры в безопасности.",
		},
		"A2": {
			Title:       "Усиление SSH",
			What:        "Отключает вход по паролю, разрешает только современные стойкие алгоритмы шифрования и пускает по SSH только участников группы sshusers.",
			Why:         "Делает невозможным подбор пароля по SSH и оставляет лишь надёжную криптографию и вход по ключу.",
			RiskWithout: "SSH остаётся открыт для перебора паролей и принимает слабые, устаревшие шифры.",
		},
		"A2.5": {
			Title:       "Нейтрализация cloud-init",
			What:        "Отключает службу cloud-init после завершения первичной настройки сервера.",
			Why:         "Не даёт провайдерскому cloud-init при каждой перезагрузке откатывать ваши настройки SSH и сети.",
			RiskWithout: "Любая будущая перезагрузка может незаметно вернуть часть настроек SSH и сети к небезопасным значениям по умолчанию.",
		},
		"A3": {
			Title:       "fail2ban",
			What:        "Ставит fail2ban, который следит за журналом и временно блокирует IP-адреса после нескольких неудачных попыток входа. Ваш админский IP вносится в белый список.",
			Why:         "Автоматически отсекает ботов, перебирающих доступ, и снижает шум в журналах.",
			RiskWithout: "Атакующие могут бесконечно долбить SSH неудачными попытками, не получая никаких ограничений.",
		},
		"A4": {
			Title:       "Сетевая оптимизация",
			What:        "Включает алгоритм управления перегрузкой BBR, увеличивает буферы сокетов и очереди сети через sysctl.",
			Why:         "Повышает пропускную способность и стабильность сетевых соединений, особенно на дальних маршрутах.",
			RiskWithout: "Сетевая производительность остаётся на консервативных значениях по умолчанию, не используя возможности канала.",
		},
		"A5": {
			Title:       "Усиление ядра",
			What:        "Через sysctl ограничивает утечки информации о ядре, защищает символьные ссылки, блокирует подмену маршрутов и перенаправлений, отключает дампы памяти.",
			Why:         "Затрудняет локальную эскалацию привилегий и сетевой обман, оставляя меньше точек для атаки.",
			RiskWithout: "Ядро открывает атакующему адреса и подсказки для эксплойтов и принимает потенциально вредные сетевые пакеты.",
		},
		"A6": {
			Title:       "Обслуживание системы",
			What:        "Ограничивает размер журналов systemd, настраивает неинтерактивный перезапуск служб, поднимает лимит открытых файлов и проверяет синхронизацию времени.",
			Why:         "Не даёт логам забить диск, обеспечивает корректные перезапуски после обновлений и точное системное время.",
			RiskWithout: "Журналы могут переполнить диск, обновления зависнуть на запросе, а службы упереться в низкий лимит файлов.",
		},
		"A6.5": {
			Title:       "Защита DNS",
			What:        "Настраивает systemd-resolved на проверку DNSSEC и шифрование запросов через DNS-over-TLS в щадящем режиме.",
			Why:         "Затрудняет подмену и прослушивание DNS-запросов, не ломая совместимость с обычными сетями.",
			RiskWithout: "DNS-запросы идут открытым текстом без проверки подлинности и легко подменяются или прослушиваются.",
		},
		"A6.7": {
			Title:       "Управление памятью",
			What:        "Включает сжатый своп в ОЗУ (ZRAM, zstd) и ставит earlyoom, который аккуратно завершает процессы до жёсткой нехватки памяти.",
			Why:         "Фактически расширяет доступную память и предотвращает зависание сервера при её исчерпании.",
			RiskWithout: "На малой памяти сервер может полностью зависнуть или жёстко убить важный процесс при нехватке ОЗУ.",
		},
		"A7": {
			Title:       "Очистка системы",
			What:        "Удаляет ненужные на сервере пакеты (firmware-утилиты, отчётчики сбоев и прочий балласт), предварительно записав список в лог.",
			Why:         "Уменьшает поверхность атаки и расход ресурсов, убирая лишние службы.",
			RiskWithout: "Лишние пакеты держат ненужные службы и таймеры, расширяя поверхность атаки и потребление ресурсов.",
		},
		"A9": {
			Title:       "Автообновления безопасности",
			What:        "Включает unattended-upgrades для автоматической установки обновлений безопасности отдельным drop-in-файлом, без авто-перезагрузки.",
			Why:         "Критические патчи ставятся сами, без ручного вмешательства, при этом перезагрузки остаются под вашим контролем.",
			RiskWithout: "Исправления безопасности приходится ставить вручную, и сервер надолго остаётся уязвимым между обновлениями.",
		},
		"A10": {
			Title:       "Обнаружение и мониторинг",
			What:        "Ставит auditd для журнала изменений важных файлов, уведомления об успешных входах по SSH и логирование отброшенных пакетов.",
			Why:         "Создаёт криминалистический след и оповещает о вторжении, тогда как fail2ban видит лишь неудачные попытки.",
			RiskWithout: "Успешный взлом, подмена конфигов и эскалация привилегий проходят незамеченными и без следов.",
		},
	},
	EN: {
		"PRE": {
			Title:       "Preconditions & checks",
			What:        "Refreshes the apt package index, creates a sudo admin user with an SSH key, and verifies the OS version and hardening mode.",
			Why:         "Guarantees a working non-root login exists and that package installs won't fail before setup even begins.",
			RiskWithout: "The very first package install can fail, and locking down SSH with no fallback user means total loss of server access.",
		},
		"A1": {
			Title:       "Firewall",
			What:        "Closes all inbound ports except SSH using iptables (IPv4 and IPv6) and persists the rules across reboots. A safety timer auto-reverts the rules if the connection drops.",
			Why:         "The server stops being reachable on any service but the ones you need, and the timer prevents accidentally locking yourself out.",
			RiskWithout: "Any service that happens to listen on a port is exposed to the whole internet for attack and scanning.",
		},
		"A8": {
			Title:       "Full upgrade & reboot",
			What:        "Installs all available system updates and reboots the server, confirming a clean boot by checking the boot_id changed.",
			Why:         "Closes known kernel and package vulnerabilities and gets the server onto fresh versions before fine-tuning.",
			RiskWithout: "The system stays on an outdated kernel and packages carrying publicly known security holes.",
		},
		"A2": {
			Title:       "SSH hardening",
			What:        "Disables password login, allows only modern strong crypto algorithms, and restricts SSH access to members of the sshusers group.",
			Why:         "Makes SSH password guessing impossible and leaves only robust key-based auth with strong ciphers.",
			RiskWithout: "SSH stays open to password brute-forcing and accepts weak, outdated encryption.",
		},
		"A2.5": {
			Title:       "Cloud-init neutralization",
			What:        "Disables the cloud-init service once the server's initial provisioning is complete.",
			Why:         "Stops the provider's cloud-init from reverting your SSH and network hardening on every reboot.",
			RiskWithout: "Any future reboot can silently roll parts of your SSH and network config back to insecure defaults.",
		},
		"A3": {
			Title:       "fail2ban",
			What:        "Installs fail2ban, which watches the journal and temporarily bans IPs after repeated failed logins. Your admin IP is whitelisted.",
			Why:         "Automatically cuts off bots brute-forcing access and reduces log noise.",
			RiskWithout: "Attackers can hammer SSH with endless failed attempts with no throttling at all.",
		},
		"A4": {
			Title:       "Network tuning",
			What:        "Enables the BBR congestion-control algorithm and enlarges socket buffers and network queues via sysctl.",
			Why:         "Improves throughput and stability of network connections, especially over long-distance links.",
			RiskWithout: "Network performance stays at conservative defaults, leaving the link's capacity unused.",
		},
		"A5": {
			Title:       "Kernel hardening",
			What:        "Uses sysctl to restrict kernel info leaks, protect symlinks, block route/redirect spoofing, and disable memory core dumps.",
			Why:         "Makes local privilege escalation and network spoofing harder, leaving fewer points to attack.",
			RiskWithout: "The kernel hands attackers addresses and exploit hints and accepts potentially malicious network packets.",
		},
		"A6": {
			Title:       "System maintenance",
			What:        "Caps systemd journal size, sets services to restart non-interactively, raises the open-file limit, and checks time sync.",
			Why:         "Keeps logs from filling the disk, ensures clean restarts after updates, and keeps accurate system time.",
			RiskWithout: "Logs can fill the disk, updates can hang on a prompt, and services can hit a too-low file limit.",
		},
		"A6.5": {
			Title:       "DNS hardening",
			What:        "Configures systemd-resolved to validate DNSSEC and encrypt lookups via DNS-over-TLS in an opportunistic mode.",
			Why:         "Makes DNS tampering and eavesdropping harder without breaking compatibility on ordinary networks.",
			RiskWithout: "DNS queries travel in cleartext with no authenticity check and are easily spoofed or sniffed.",
		},
		"A6.7": {
			Title:       "Memory management",
			What:        "Enables compressed in-RAM swap (ZRAM, zstd) and installs earlyoom, which gently kills processes before a hard out-of-memory.",
			Why:         "Effectively expands usable memory and prevents the server from freezing when memory runs out.",
			RiskWithout: "On a low-memory box the server can freeze entirely or hard-kill an important process under memory pressure.",
		},
		"A7": {
			Title:       "System cleanup",
			What:        "Removes packages unneeded on a server (firmware tools, crash reporters, and other bloat), logging the list first.",
			Why:         "Shrinks the attack surface and resource use by stripping out unnecessary services.",
			RiskWithout: "Extra packages keep needless services and timers running, widening the attack surface and resource use.",
		},
		"A9": {
			Title:       "Unattended security updates",
			What:        "Enables unattended-upgrades to auto-install security updates via a separate drop-in file, with auto-reboot left off.",
			Why:         "Critical patches install themselves without manual work, while reboots stay under your control.",
			RiskWithout: "Security fixes must be applied by hand, leaving the server vulnerable for long stretches between updates.",
		},
		"A10": {
			Title:       "Detection & monitoring",
			What:        "Installs auditd to log changes to sensitive files, notifications for successful SSH logins, and logging of dropped packets.",
			Why:         "Builds a forensic trail and alerts you to intrusion, whereas fail2ban only sees failed attempts.",
			RiskWithout: "A successful breach, config tampering, and privilege escalation all go unnoticed and leave no trail.",
		},
	},
}
