package steps

import "github.com/UberMorgott/morgward/internal/verify"

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
	script := "export DEBIAN_FRONTEND=noninteractive\n" +
		aptInstall("unattended-upgrades") +
		putFile("/etc/apt/apt.conf.d/20auto-upgrades", autoUpgrades, "0644") +
		putFile("/etc/apt/apt.conf.d/52-unattended-upgrades-local", unattendedLocal, "0644")
	if r := ctx.Cli.Sudo(script); r.RC != 0 {
		return StatusFail, "unattended-upgrades setup failed: " + firstLine(r.Stderr), nil
	}
	// Locale-independent confirmation, shared verbatim with the §V matrix row (see
	// verify.AutoUpdatesCmd). A failure here means the tweak did NOT take effect, so
	// it is reported as a failure instead of a Warn-and-claim-success. Not
	// lockout-capable ⇒ nil error, the run continues.
	if ctx.Cli.Sudo(verify.AutoUpdatesCmd).Out() != "ok" {
		return StatusFail, "unattended-upgrades did not confirm as enabled (dry-run failed or APT::Periodic::Unattended-Upgrade != 1)", nil
	}
	return StatusOK, "unattended-upgrades enabled (auto-reboot off, agent controls reboots)", nil
}
