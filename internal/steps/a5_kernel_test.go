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
