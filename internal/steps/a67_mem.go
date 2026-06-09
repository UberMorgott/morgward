package steps

// A67Memory implements §A6.7: ZRAM compressed swap (zstd) + tuned swappiness,
// and earlyoom. Zero overhead on large VPS, critical on 1-2 GB.
type A67Memory struct{}

func (A67Memory) ID() string    { return "A6.7" }
func (A67Memory) Title() string { return "Memory mgmt (ZRAM zstd + earlyoom)" }

const zramConf = `[zram0]
zram-size = ram / 2
compression-algorithm = zstd
`

const zramSysctl = `vm.swappiness = 180
vm.page-cluster = 0
`

func (a A67Memory) Run(ctx *Context) (Status, string, error) {
	// Greenfield disables any pre-existing disk swap so zram runs alone. On a
	// brownfield/in-use box the operator may rely on disk swap, and zram (default
	// priority 100) is used first regardless, so the recommended zram-high + disk-low
	// layout is kept intact — we skip the swapoff entirely.
	disableDiskSwap := ""
	if ctx.Facts.Greenfield {
		disableDiskSwap = `# Disable disk swap if present (don't run zram alongside disk swap).
# Tag each line we comment with a morgward marker so the A6.7 revert re-enables
# ONLY swap morgward disabled — never swap the operator turned off themselves.
if swapon --show=NAME --noheadings | grep -vq zram; then
  swapoff -a 2>/dev/null || true
  sed -i -E '/^[[:space:]]*#/!{/[[:space:]]swap[[:space:]]/{s/[[:space:]]*$/ # morgward-disabled-swap/;s/^/#/}}' /etc/fstab 2>/dev/null || true
fi
`
	}

	script := `export DEBIAN_FRONTEND=noninteractive
stdbuf -oL -eL apt-get -o DPkg::Lock::Timeout=300 install -y systemd-zram-generator earlyoom
` + putFile("/etc/systemd/zram-generator.conf", zramConf, "0644") +
		putFile("/etc/sysctl.d/99-zram.conf", zramSysctl, "0644") +
		disableDiskSwap +
		`systemctl daemon-reload
systemctl start systemd-zram-setup@zram0.service 2>/dev/null || true
systemctl enable --now earlyoom >/dev/null 2>&1 || true
sysctl --system >/dev/null 2>&1
`
	if r := ctx.Cli.Sudo(script); r.RC != 0 {
		ctx.Log.Warn("memory setup rc=%d (verifying anyway)", r.RC)
	}
	zram := ctx.Cli.Sudo("swapon --show 2>/dev/null | grep -q zram && echo yes").Out()
	eoom := ctx.Cli.Run("systemctl is-active earlyoom").Out()
	if zram != "yes" {
		return StatusFail, "ZRAM swap not active", nil
	}
	detail := "ZRAM zstd swap active, swappiness=180, earlyoom=" + eoom
	if !ctx.Facts.Greenfield && len(ctx.Facts.DiskSwap) > 0 {
		detail += " (disk swap preserved: brownfield)"
	}
	return StatusOK, detail, nil
}
