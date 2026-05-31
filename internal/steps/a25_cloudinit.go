package steps

// A25CloudInit implements §A2.5: neutralize cloud-init so it can't undo
// hardening on future reboots.
type A25CloudInit struct{}

func (A25CloudInit) ID() string    { return "A2.5" }
func (A25CloudInit) Title() string { return "Cloud-init neutralization" }

func (A25CloudInit) Run(ctx *Context) (Status, string, error) {
	// Detect cloud-init by ANY footprint, not just the `cloud-init` CLI on $PATH:
	// a cloud image can carry the systemd unit / config tree while the binary is
	// not resolvable on the admin user's non-login PATH, and §A2.5 must still
	// neutralize it. Present if the binary, the systemd unit, OR the config exists.
	present := ctx.Cli.Run(`{ command -v cloud-init >/dev/null 2>&1 || systemctl cat cloud-init.service >/dev/null 2>&1 || [ -e /etc/cloud/cloud.cfg ]; } && echo yes`).Out()
	if present != "yes" {
		return StatusSkip, "cloud-init not installed", nil
	}
	if done := ctx.Cli.Run("test -f /etc/cloud/cloud-init.disabled && echo yes").Out(); done == "yes" {
		return StatusSkip, "cloud-init already disabled", nil
	}
	if r := ctx.Cli.Sudo("mkdir -p /etc/cloud && touch /etc/cloud/cloud-init.disabled"); r.RC != 0 {
		return StatusFail, "could not disable cloud-init", nil
	}
	return StatusOK, "cloud-init disabled (/etc/cloud/cloud-init.disabled)", nil
}
