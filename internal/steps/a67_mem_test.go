package steps

import (
	"strings"
	"testing"
)

// A failed systemd-zram-generator install (no candidate) must not still swapoff the
// disk swap and leave a greenfield box with NO swap at all.
func TestMemScriptSwapoffGatedOnGenerator(t *testing.T) {
	g := memScript(true)
	gate := strings.Index(g, "if "+pkgInstalledCheck("systemd-zram-generator")+" >/dev/null && swapon")
	off := strings.Index(g, "swapoff -a")
	if gate < 0 || off < 0 || gate > off {
		t.Errorf("greenfield swapoff must be gated on the generator being installed:\n%s", g)
	}
	if strings.Contains(memScript(false), "swapoff") {
		t.Error("brownfield must never swapoff")
	}
}
