package steps

import "fmt"

// A5Kernel implements §A5: kernel/sysctl hardening, THP=madvise, core_pattern
// lockdown. rp_filter is lockout-capable, so a live rpf-revert timer is armed
// and a fresh session is verified before the timer is disarmed.
type A5Kernel struct{}

func (A5Kernel) ID() string    { return "A5" }
func (A5Kernel) Title() string { return "Kernel hardening (sysctl, THP=madvise, core_pattern)" }

// kernelHardenConf renders 99-zz-kernel-harden.conf. rp_filter is the only
// routing-sensitive knob: strict reverse-path (=1) drops asymmetric-routed return
// packets and breaks WireGuard/OpenVPN peers and multi-egress routers, so a
// brownfield/forwarding box gets LOOSE mode (=2) instead. Everything else is
// router-safe and identical on both paths. routing=false ⇒ greenfield strict (=1).
func kernelHardenConf(routing bool) string {
	rpf := 1
	if routing {
		rpf = 2
	}
	return fmt.Sprintf(`kernel.kptr_restrict = 2
kernel.dmesg_restrict = 1
kernel.yama.ptrace_scope = 1
kernel.randomize_va_space = 2
fs.suid_dumpable = 0
kernel.unprivileged_bpf_disabled = 1
net.core.bpf_jit_harden = 2
fs.protected_symlinks = 1
fs.protected_hardlinks = 1
fs.protected_fifos = 1
fs.protected_regular = 2
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.default.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv4.conf.default.send_redirects = 0
net.ipv4.conf.all.accept_source_route = 0
net.ipv4.conf.default.accept_source_route = 0
net.ipv6.conf.all.accept_redirects = 0
net.ipv6.conf.default.accept_redirects = 0
net.ipv6.conf.all.accept_source_route = 0
net.ipv6.conf.default.accept_source_route = 0
net.ipv4.conf.all.rp_filter = %[1]d
net.ipv4.conf.default.rp_filter = %[1]d
net.ipv4.icmp_echo_ignore_broadcasts = 1
net.ipv4.conf.all.log_martians = 1
net.ipv4.conf.default.log_martians = 1
kernel.sysrq = 0
kernel.core_pattern = |/bin/false
`, rpf)
}

func (a A5Kernel) Run(ctx *Context) (Status, string, error) {
	// Write the sysctl file (zz prefix sorts after distro defaults). rp_filter is
	// loose (=2) when forwarding/routing is active so asymmetric VPN/router return
	// paths survive; greenfield keeps strict (=1).
	conf := kernelHardenConf(ctx.Facts.Forwarding)
	if r := ctx.Cli.Sudo(putFile("/etc/sysctl.d/99-zz-kernel-harden.conf", conf, "0644")); r.RC != 0 {
		return StatusFail, "writing kernel sysctl failed: " + firstLine(r.Stderr), nil
	}

	// Arm a LIVE rp_filter revert (rp_filter=1 can sever asymmetric-routed sessions).
	ctx.Cli.Sudo(`systemctl stop rpf-revert.timer 2>/dev/null || true
systemctl reset-failed 'rpf-revert.*' 2>/dev/null || true
systemd-run --on-active=300 --unit=rpf-revert sh -c 'sysctl -w net.ipv4.conf.all.rp_filter=2 net.ipv4.conf.default.rp_filter=2 net.ipv4.route.flush=1'`)

	apply := `sysctl --system >/dev/null 2>&1
sysctl -w net.ipv4.route.flush=1 >/dev/null 2>&1
sysctl -w net.ipv6.route.flush=1 >/dev/null 2>&1`
	if r := ctx.Cli.Sudo(apply); r.RC != 0 {
		ctx.Log.Warn("sysctl --system reported rc=%d (a non-existent key is dropped, not fatal)", r.RC)
	}

	// Verify the session survives rp_filter=1 from a fresh connection, then disarm.
	if err := freshLogin(ctx, ctx.Cli.User); err != nil {
		ctx.Cli.Sudo("sysctl -w net.ipv4.conf.all.rp_filter=2 net.ipv4.conf.default.rp_filter=2 net.ipv4.route.flush=1")
		return StatusFail, "session lost after rp_filter=1 — reverted to loose: " + err.Error(), nil
	}
	ctx.Cli.Sudo(`systemctl stop rpf-revert.timer 2>/dev/null || true
systemctl reset-failed 'rpf-revert.*' 2>/dev/null || true`)

	// THP = madvise (immediate + persist via grub).
	thp := `if ! grep -q '\[madvise\]' /sys/kernel/mm/transparent_hugepage/enabled 2>/dev/null; then
  echo madvise > /sys/kernel/mm/transparent_hugepage/enabled 2>/dev/null || true
  if ! grep -q transparent_hugepage /etc/default/grub; then
    sed -i 's/^GRUB_CMDLINE_LINUX_DEFAULT="\(.*\)"/GRUB_CMDLINE_LINUX_DEFAULT="\1 transparent_hugepage=madvise"/' /etc/default/grub && update-grub >/dev/null 2>&1
  fi
fi`
	ctx.Cli.Sudo(thp)

	cp := ctx.Cli.Run("sysctl -n kernel.core_pattern").Out()
	rpf := ctx.Cli.Run("sysctl -n net.ipv4.conf.all.rp_filter").Out()
	return StatusOK, "sysctl hardening applied (core_pattern=" + cp + ", rp_filter=" + rpf + "), THP=madvise", nil
}
