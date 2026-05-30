package steps

// A6Maint implements §A6: journald cap, needrestart non-interactive,
// DefaultLimitNOFILE, time sync. AppArmor is verified, not disabled.
type A6Maint struct{}

func (A6Maint) ID() string    { return "A6" }
func (A6Maint) Title() string { return "Maintenance (journald, needrestart, NOFILE, ntp)" }

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
timedatectl status | grep -q 'synchronized: yes' || timedatectl set-ntp true 2>/dev/null || true
`
	if r := ctx.Cli.Sudo(script); r.RC != 0 {
		return StatusFail, "maintenance apply failed: " + firstLine(r.Stderr), nil
	}
	aa := ctx.Cli.Sudo("aa-status --enabled 2>/dev/null && echo apparmor-ok || echo apparmor-absent").Out()
	return StatusOK, "journald capped, needrestart=auto, NOFILE=65536:524288 (" + aa + ")", nil
}
