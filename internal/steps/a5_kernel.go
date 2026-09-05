package steps

import (
	"fmt"
	"strings"
)

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

// Paths A5 owns beyond 99-zz-kernel-harden.conf. One file per concern so a revert
// can drop exactly what it wrote, and so a distro/operator file is never rewritten.
const (
	ioUringConf   = "/etc/sysctl.d/60-morgward-iouring.conf"
	panicConf     = "/etc/sysctl.d/60-morgward-panic.conf"
	coredumpConf  = "/etc/systemd/coredump.conf.d/99-morgward.conf"
	panicConfBody = "kernel.panic = 10\nkernel.panic_on_oops = 1\n"
	// M13: systemd-coredump writes crash dumps to /var/lib/systemd/coredump, which
	// fills a small VPS disk and persists process memory (secrets) on disk. A5
	// already sets core_pattern=|/bin/false, but the systemd-coredump package
	// re-asserts its own core_pattern on every upgrade — this drop-in makes the
	// policy stick on the systemd side instead of fighting that pipeline.
	coredumpConfBody = "[Coredump]\nStorage=none\nProcessSizeMax=0\n"
)

// extraSysctlScript writes the A5 sysctl drop-ins whose keys are NOT universal.
//
//   - M01 kernel.io_uring_disabled=1 blocks io_uring for unprivileged processes (a
//     recurring LPE surface) while leaving CAP_SYS_ADMIN users working — value 2
//     would break privileged containers. The key exists from kernel 6.6, so 22.04's
//     5.15 GA kernel does not have it: writing it unconditionally would make
//     `sysctl --system` fail. Landed only when the key is actually present.
//   - M02 kernel.panic=10 + panic_on_oops=1 turn a wedged kernel into a reboot,
//     which is the only useful outcome on a console-less VPS. Universal.
//   - M13 the systemd-coredump policy, only where systemd-coredump exists.
func extraSysctlScript() string {
	return "mkdir -p /etc/sysctl.d\n" +
		"if [ -e /proc/sys/kernel/io_uring_disabled ]; then\n" +
		putFile(ioUringConf, "kernel.io_uring_disabled = 1\n", "0644") +
		"else\n  rm -f " + ioUringConf + "\nfi\n" +
		putFile(panicConf, panicConfBody, "0644") +
		"if [ -x /usr/lib/systemd/systemd-coredump ] || [ -x /lib/systemd/systemd-coredump ] || command -v coredumpctl >/dev/null 2>&1; then\n" +
		"  mkdir -p /etc/systemd/coredump.conf.d\n" +
		putFile(coredumpConf, coredumpConfBody, "0644") +
		"fi\n"
}

func (a A5Kernel) Run(ctx *Context) (Status, string, error) {
	// Write the sysctl file (zz prefix sorts after distro defaults). rp_filter is
	// loose (=2) when forwarding/routing is active so asymmetric VPN/router return
	// paths survive; greenfield keeps strict (=1).
	conf := kernelHardenConf(ctx.Facts.Forwarding)
	if r := ctx.Cli.Sudo(putFile("/etc/sysctl.d/99-zz-kernel-harden.conf", conf, "0644")); r.RC != 0 {
		return StatusFail, "writing kernel sysctl failed: " + firstLine(r.Stderr), nil
	}

	// Version/feature-gated extras (io_uring, panic-on-oops, coredump policy). Their
	// keys/units are not present on every supported release, so the script probes for
	// each one rather than emitting a sysctl `sysctl --system` would reject.
	if r := ctx.Cli.Sudo(extraSysctlScript()); r.RC != 0 {
		ctx.Log.Warn("kernel sysctl extras rc=%d (continuing): %s", r.RC, firstLine(r.Stderr))
	}

	// Arm a LIVE rp_filter revert (rp_filter=1 can sever asymmetric-routed sessions).
	// Revert to the CONTEXT-APPROPRIATE baseline, not a blanket loose=2: on a
	// forwarding box loose (=2) is what the apply targets and what asymmetric
	// VPN/router routes need; on a greenfield/non-forwarding box the apply targets
	// strict (=1), so reverting to =2 there would silently loosen below baseline.
	rpfRevert := 1
	if ctx.Facts.Forwarding {
		rpfRevert = 2
	}
	ctx.Cli.Sudo(armTimer("rpf-revert", fmt.Sprintf(
		`sh -c 'sysctl -w net.ipv4.conf.all.rp_filter=%[1]d net.ipv4.conf.default.rp_filter=%[1]d net.ipv4.route.flush=1'`,
		rpfRevert)))

	apply := `sysctl --system >/dev/null 2>&1
sysctl -w net.ipv4.route.flush=1 >/dev/null 2>&1
sysctl -w net.ipv6.route.flush=1 >/dev/null 2>&1`
	if r := ctx.Cli.Sudo(apply); r.RC != 0 {
		ctx.Log.Warn("sysctl --system reported rc=%d (a non-existent key is dropped, not fatal)", r.RC)
	}

	// Verify the session survives the applied rp_filter from a fresh connection, then
	// disarm. On failure revert to the SAME context-appropriate baseline as the armed
	// timer (loose=2 when forwarding, strict=1 otherwise) — never blindly to loose.
	if err := freshLoginVerify(ctx, ctx.Cli.User); err != nil {
		ctx.Cli.Sudo(fmt.Sprintf("sysctl -w net.ipv4.conf.all.rp_filter=%[1]d net.ipv4.conf.default.rp_filter=%[1]d net.ipv4.route.flush=1", rpfRevert))
		return StatusFail, fmt.Sprintf("session lost after rp_filter apply — reverted to rp_filter=%d: %s", rpfRevert, err.Error()), nil
	}
	ctx.Cli.Sudo(disarmTimer("rpf-revert"))

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
	// Report the OBSERVED THP mode, never a blanket "THP=madvise": some kernels
	// expose no THP knob at all and the write above is a best-effort `|| true`.
	thpNow := ctx.Cli.Run("cat /sys/kernel/mm/transparent_hugepage/enabled 2>/dev/null").Out()
	thpNote := "THP=madvise"
	if !strings.Contains(thpNow, "[madvise]") {
		thpNote = "THP NOT madvise (" + firstLine(thpNow) + ") — takes effect after reboot if grub was updated"
	}
	iou := ctx.Cli.Run("sysctl -n kernel.io_uring_disabled 2>/dev/null").Out()
	if iou == "" {
		iou = "n/a (<6.6 kernel)"
	}
	return StatusOK, "sysctl hardening applied (core_pattern=" + cp + ", rp_filter=" + rpf +
		", panic=10, io_uring_disabled=" + iou + "), " + thpNote, nil
}
