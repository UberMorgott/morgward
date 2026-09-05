package steps

import (
	"strings"
	"testing"
)

// TestKernelHardenConfRouting asserts rp_filter is LOOSE (=2) on a routing/
// forwarding box so asymmetric VPN/router return paths survive.
func TestKernelHardenConfRouting(t *testing.T) {
	got := kernelHardenConf(true)
	for _, want := range []string{
		"net.ipv4.conf.all.rp_filter = 2",
		"net.ipv4.conf.default.rp_filter = 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("routing conf missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "rp_filter = 1") {
		t.Errorf("routing conf must not set strict rp_filter:\n%s", got)
	}
	// The rest of the hardening must be unchanged.
	if !strings.Contains(got, "kernel.core_pattern = |/bin/false") {
		t.Errorf("routing conf lost core_pattern lockdown:\n%s", got)
	}
}

// The A5 extras must be FEATURE-GATED on the box, not written blind: the io_uring
// key only exists from kernel 6.6 (22.04's 5.15 has no such sysctl) and the
// coredump drop-in is meaningless without systemd-coredump. panic/panic_on_oops is
// universal and is written unconditionally.
func TestExtraSysctlScriptGates(t *testing.T) {
	s := extraSysctlScript(true)
	if !strings.Contains(s, "if [ -e /proc/sys/kernel/io_uring_disabled ]; then") {
		t.Errorf("io_uring conf must be gated on the key existing (5.15 kernels lack it):\n%s", s)
	}
	if !strings.Contains(s, "rm -f "+ioUringConf) {
		t.Errorf("a kernel without the key must not keep a stale io_uring conf:\n%s", s)
	}
	if !strings.Contains(s, "command -v coredumpctl") {
		t.Errorf("coredump drop-in must be gated on systemd-coredump being present:\n%s", s)
	}
	// Content is delivered base64-encoded (no heredoc — §A1 stdin caveat), so assert
	// on the destination paths rather than the literal bodies.
	for _, path := range []string{ioUringConf, panicConf, coredumpConf} {
		if !strings.Contains(s, path) {
			t.Errorf("script must write %s:\n%s", path, s)
		}
	}
	if strings.Contains(s, "<<") {
		t.Errorf("no heredoc may appear in a remote script (§A1 stdin caveat):\n%s", s)
	}
}

// A BROWNFIELD box may already run io_uring consumers (modern PostgreSQL/nginx/
// QEMU, Rust async runtimes), so M01 must not touch the knob there at all — not
// even to remove a file. panic/coredump stay universal.
func TestExtraSysctlScriptBrownfieldKeepsIOUring(t *testing.T) {
	s := extraSysctlScript(false)
	if strings.Contains(s, ioUringConf) {
		t.Errorf("brownfield must not write OR remove the io_uring drop-in:\n%s", s)
	}
	for _, path := range []string{panicConf, coredumpConf} {
		if !strings.Contains(s, path) {
			t.Errorf("brownfield must still write %s:\n%s", path, s)
		}
	}
}

// TestKernelHardenConfGreenfield asserts rp_filter stays STRICT (=1) when there
// is no forwarding (greenfield default).
func TestKernelHardenConfGreenfield(t *testing.T) {
	got := kernelHardenConf(false)
	for _, want := range []string{
		"net.ipv4.conf.all.rp_filter = 1",
		"net.ipv4.conf.default.rp_filter = 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("greenfield conf missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "rp_filter = 2") {
		t.Errorf("greenfield conf must keep strict rp_filter:\n%s", got)
	}
}
