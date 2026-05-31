package steps

import (
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
)

// newTestCtx builds a minimal Context sufficient for the pure script builders
// (no SSH client — the builders never touch ctx.Cli).
func newTestCtx() *Context {
	return &Context{
		Cfg:   &config.Config{AdminUser: "vpsadmin", Host: "example", Port: 22},
		Facts: &detect.Facts{Is2404: true},
	}
}

// TestA2SafeScriptNoLockdown asserts the SAFE drop-in writer leaves the access
// policy at the image default: it must NEVER emit AllowGroups, PermitRootLogin
// no, or PasswordAuthentication no, and must NOT lock the root password. Crypto
// hardening (ciphers/KEX/host-key/forwarding) MUST still be present.
func TestA2SafeScriptNoLockdown(t *testing.T) {
	ctx := newTestCtx()
	write := buildSafeWrite(ctx)
	conf := safe99(ctx)

	all := write + "\n" + conf

	// Must NOT lock down access.
	if strings.Contains(all, "AllowGroups") {
		t.Errorf("safe path must NOT write AllowGroups:\n%s", all)
	}
	if strings.Contains(conf, "PermitRootLogin no") {
		t.Errorf("safe path must NOT set PermitRootLogin no:\n%s", conf)
	}
	if strings.Contains(all, "PasswordAuthentication no") {
		t.Errorf("safe path must NOT set PasswordAuthentication no:\n%s", all)
	}
	if strings.Contains(all, "passwd -l root") {
		t.Errorf("safe path must NOT lock root password:\n%s", all)
	}
	// 00-hardening.conf must be image-default-preserving: no PasswordAuthentication
	// line written at all (conf00 is not called on the safe path).
	if strings.Contains(write, "00-hardening.conf") && strings.Contains(write, "PasswordAuthentication") {
		t.Errorf("safe path must not write any PasswordAuthentication line:\n%s", write)
	}

	// Crypto hardening MUST be present.
	for _, want := range []string{"Ciphers ", "KexAlgorithms ", "MACs ", "DisableForwarding yes", "99-hardening.conf"} {
		if !strings.Contains(all, want) {
			t.Errorf("safe path missing crypto component %q:\n%s", want, all)
		}
	}
}

// TestA2SafeID resolves the IDs/titles.
func TestA2SafeID(t *testing.T) {
	if (A2Safe{}).ID() != "A2-safe" {
		t.Errorf("A2Safe.ID()=%q want A2-safe", (A2Safe{}).ID())
	}
	if (A2Danger{}).ID() != "A2-danger" {
		t.Errorf("A2Danger.ID()=%q want A2-danger", (A2Danger{}).ID())
	}
}

// TestA2DangerScript asserts the DANGER drop-in writer DOES emit the lockdown
// directives: AllowGroups sshusers, PermitRootLogin no, PasswordAuthentication no.
func TestA2DangerScript(t *testing.T) {
	ctx := newTestCtx()
	write := buildDangerWrite(ctx)
	conf := danger99()

	all := write + "\n" + conf

	for _, want := range []string{"AllowGroups sshusers", "PermitRootLogin no", "PasswordAuthentication no"} {
		if !strings.Contains(all, want) {
			t.Errorf("danger path missing lockdown directive %q:\n%s", want, all)
		}
	}
}

// TestA2SSHUnchanged guards the legacy full-run step: build99(strict=false) still
// emits AllowGroups + prohibit-password (CLI back-compat — A2SSH behavior frozen).
func TestA2SSHUnchanged(t *testing.T) {
	ctx := newTestCtx()
	soft := build99(ctx, false)
	if !strings.Contains(soft, "AllowGroups sshusers") {
		t.Errorf("legacy build99(false) must still emit AllowGroups (CLI back-compat):\n%s", soft)
	}
	if !strings.Contains(soft, "PermitRootLogin prohibit-password") {
		t.Errorf("legacy build99(false) must still emit prohibit-password:\n%s", soft)
	}
	strict := build99(ctx, true)
	if !strings.Contains(strict, "PermitRootLogin no") {
		t.Errorf("legacy build99(true) must emit PermitRootLogin no:\n%s", strict)
	}
}
