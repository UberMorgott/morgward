package steps

// A9Unattended implements §A9: unattended security updates. Uses a SEPARATE
// drop-in (52-*) so the shipped 50unattended-upgrades Allowed-Origins survive.
type A9Unattended struct{}

func (A9Unattended) ID() string    { return "A9" }
func (A9Unattended) Title() string { return "Unattended security updates" }

const autoUpgrades = `APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
`

const unattendedLocal = `Unattended-Upgrade::Automatic-Reboot "false";
Unattended-Upgrade::Remove-Unused-Kernel-Packages "true";
Unattended-Upgrade::Remove-New-Unused-Dependencies "true";
`

func (a A9Unattended) Run(ctx *Context) (Status, string, error) {
	script := `export DEBIAN_FRONTEND=noninteractive
stdbuf -oL -eL apt-get -o DPkg::Lock::Timeout=300 install -y unattended-upgrades
` + putFile("/etc/apt/apt.conf.d/20auto-upgrades", autoUpgrades, "0644") +
		putFile("/etc/apt/apt.conf.d/52-unattended-upgrades-local", unattendedLocal, "0644")
	if r := ctx.Cli.Sudo(script); r.RC != 0 {
		return StatusFail, "unattended-upgrades setup failed: " + firstLine(r.Stderr), nil
	}
	dry := ctx.Cli.Sudo("unattended-upgrade --dry-run --debug 2>&1 | grep -qi 'allowed origins' && echo ok").Out()
	if dry != "ok" {
		ctx.Log.Warn("could not confirm Allowed-Origins in dry-run (continuing)")
	}
	return StatusOK, "unattended-upgrades enabled (auto-reboot off, agent controls reboots)", nil
}
