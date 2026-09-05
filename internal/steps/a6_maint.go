package steps

// A6Maint implements §A6: journald cap, needrestart non-interactive,
// DefaultLimitNOFILE, time sync, periodic TRIM. AppArmor is verified, not disabled.
type A6Maint struct{}

func (A6Maint) ID() string { return "A6" }
func (A6Maint) Title() string {
	return "Maintenance (journald, needrestart, NOFILE, ntp, fstrim)"
}

// chronyConf is the morgward chrony drop-in (M06). It lands under conf.d so the
// distro's chrony.conf and any operator sources.d/ pool stay untouched:
//   - makestep 1.0 3 lets chrony STEP the clock (not just slew) for the first three
//     updates, so a VPS that resumed from a snapshot hours out of date is correct in
//     seconds instead of days — slewing 1h takes ~20 days at chrony's default rate.
//   - rtcsync keeps the hardware clock in step where one exists (a no-op in a
//     container/VM without an RTC).
const chronyConf = "makestep 1.0 3\nrtcsync\n"

// fstrimScript enables the weekly fstrim timer (M05) on a box whose storage
// actually supports discard. On a thin-provisioned VPS this is what returns freed
// blocks to the host; without it the image grows monotonically. Skipped when no
// block device reports a non-zero discard granularity (plain spinning/passthrough
// storage), so the audit never shows a timer we deliberately did not enable.
const fstrimScript = `if lsblk --discard -n -o DISC-GRAN 2>/dev/null | grep -qv '^\s*0B\s*$'; then
  systemctl enable --now fstrim.timer >/dev/null 2>&1 && echo "fstrim.timer enabled" || echo "fstrim.timer unavailable"
else
  echo "fstrim skipped: no device reports discard support"
fi
`

// timeSyncScript is the M06 time-sync path. chrony steps a badly-wrong clock and
// survives suspend/resume far better than systemd-timesyncd, which only ever slews.
// 26.04 already ships chrony, so there it is a pure verify; on 22.04/24.04 chrony is
// installed (its postinst disables systemd-timesyncd itself — never both at once).
// The install is OPTIONAL: a release without a chrony candidate keeps timesyncd, and
// A6 falls back to `timedatectl set-ntp true` in every branch, so time sync is never
// left off. Skipped entirely when chrony is already active (operator's own setup).
func timeSyncScript(installChrony bool) string {
	s := "if systemctl is-active chrony >/dev/null 2>&1 || systemctl is-active chronyd >/dev/null 2>&1; then\n" +
		"  echo 'chrony already active'\n" +
		"else\n"
	if installChrony {
		s += aptPrelude() + aptInstallOptional("chrony")
	}
	s += "fi\n" +
		"if [ -d /etc/chrony/conf.d ]; then\n" +
		putFile("/etc/chrony/conf.d/90-morgward.conf", chronyConf, "0644") +
		"  systemctl restart chrony >/dev/null 2>&1 || systemctl restart chronyd >/dev/null 2>&1 || true\n" +
		"fi\n" +
		"timedatectl status | grep -q 'synchronized: yes' || timedatectl set-ntp true 2>/dev/null || true\n"
	return s
}

const journaldConf = `[Journal]
SystemMaxUse=500M
SystemKeepFree=1G
MaxRetentionSec=2week
`

const nofileConf = `[Manager]
DefaultLimitNOFILE=65536:524288
`

func (a A6Maint) Run(ctx *Context) (Status, string, error) {
	script := "mkdir -p /var/log/journal /etc/systemd/journald.conf.d /etc/needrestart/conf.d /etc/systemd/system.conf.d\n" +
		putFile("/etc/systemd/journald.conf.d/99-vps-cap.conf", journaldConf, "0644") +
		putFile("/etc/needrestart/conf.d/50-autorestart.conf", "$nrconf{restart} = 'a';\n", "0644") +
		putFile("/etc/systemd/system.conf.d/limits.conf", nofileConf, "0644") +
		`systemctl restart systemd-journald
systemctl daemon-reexec
`
	if r := ctx.Cli.Sudo(script); r.RC != 0 {
		return StatusFail, "maintenance apply failed: " + firstLine(r.Stderr), nil
	}

	// M06: chrony is installed on 22.04/24.04 (26.04 already ships it) and verified
	// everywhere. Non-fatal by construction — see timeSyncScript.
	ctx.Cli.Sudo(timeSyncScript(ctx.Facts.Is2204 || ctx.Facts.Is2404))
	track := ctx.Cli.Sudo("chronyc tracking 2>/dev/null | head -1").Out()
	timeNote := "ntp on"
	if track != "" {
		timeNote = "chrony (" + track + ")"
	}

	// M05: weekly TRIM where the storage supports discard.
	trim := firstLine(ctx.Cli.Sudo(fstrimScript).Out())

	aa := ctx.Cli.Sudo("aa-status --enabled 2>/dev/null && echo apparmor-ok || echo apparmor-absent").Out()
	return StatusOK, "journald capped, needrestart=auto, NOFILE=65536:524288, " +
		timeNote + ", " + trim + " (" + aa + ")", nil
}
