package steps

// A65DNS implements §A6.5: DNS hardening on systemd-resolved (DNSSEC
// allow-downgrade, DNSOverTLS opportunistic — not strict, to survive captive
// portals / non-DoT resolvers).
type A65DNS struct{}

func (A65DNS) ID() string    { return "A6.5" }
func (A65DNS) Title() string { return "DNS hardening (systemd-resolved DoT/DNSSEC)" }

const resolvedConf = `[Resolve]
DNSSEC=allow-downgrade
DNSOverTLS=opportunistic
`

func (A65DNS) Run(ctx *Context) (Status, string, error) {
	if active := ctx.Cli.Run("systemctl is-active systemd-resolved").Out(); active != "active" {
		return StatusSkip, "systemd-resolved not active (different resolver)", nil
	}
	script := "mkdir -p /etc/systemd/resolved.conf.d\n" +
		putFile("/etc/systemd/resolved.conf.d/99-dns-hardening.conf", resolvedConf, "0644") +
		"systemctl restart systemd-resolved\n"
	if r := ctx.Cli.Sudo(script); r.RC != 0 {
		return StatusFail, "resolved hardening failed: " + firstLine(r.Stderr), nil
	}
	if q := ctx.Cli.Run("resolvectl query example.com >/dev/null 2>&1 && echo ok").Out(); q != "ok" {
		ctx.Log.Warn("resolvectl query example.com did not resolve (check connectivity)")
	}
	return StatusOK, "DNSSEC=allow-downgrade, DNSOverTLS=opportunistic", nil
}
