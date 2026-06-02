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

// TestCryptoBlockVersionGate guards F20: only a CONFIRMED 26.04 box gets the
// OpenSSH-10-only tokens (mlkem KEX + PerSourcePenalties). A 24.04 box and an
// UNKNOWN/misdetected version (both flags false) both fall back to the
// conservative 24.04 set so a glitched os-release probe degrades safely.
func TestCryptoBlockVersionGate(t *testing.T) {
	ctx := newTestCtx()

	mk := func(is2404, is2604 bool) string {
		ctx.Facts.Is2404, ctx.Facts.Is2604 = is2404, is2604
		return cryptoBlock(ctx)
	}

	// Confirmed 26.04 → newest.
	if c := mk(false, true); !strings.Contains(c, "mlkem768x25519-sha256") || !strings.Contains(c, "PerSourcePenalties") {
		t.Errorf("26.04 must emit mlkem + PerSourcePenalties:\n%s", c)
	}
	// Confirmed 24.04 → conservative.
	if c := mk(true, false); strings.Contains(c, "mlkem768x25519-sha256") || strings.Contains(c, "PerSourcePenalties") {
		t.Errorf("24.04 must NOT emit mlkem/PerSourcePenalties:\n%s", c)
	}
	// Unknown/misdetected (both false) → conservative fallback, NOT newest.
	if c := mk(false, false); strings.Contains(c, "mlkem768x25519-sha256") || strings.Contains(c, "PerSourcePenalties") {
		t.Errorf("unknown version must fall back to conservative 24.04 set, not 26.04:\n%s", c)
	}
}

// TestA2SSHSoftNoLockdown guards the security fix: the legacy full-run step in
// SOFT mode must NOT impose the access lockdown — no AllowGroups and no
// PermitRootLogin line at all (image-default access preserved, so the default
// `run` can't lock the operator out). Crypto + a session knob must still be
// present. STRICT mode still applies the full lockdown.
func TestA2SSHSoftNoLockdown(t *testing.T) {
	ctx := newTestCtx()
	soft := build99(ctx, false)
	if strings.Contains(soft, "AllowGroups") {
		t.Errorf("soft build99(false) must NOT emit AllowGroups:\n%s", soft)
	}
	if strings.Contains(soft, "PermitRootLogin") {
		t.Errorf("soft build99(false) must NOT emit any PermitRootLogin line:\n%s", soft)
	}
	if !strings.Contains(soft, "Ciphers ") {
		t.Errorf("soft build99(false) must still emit crypto (Ciphers):\n%s", soft)
	}
	if !strings.Contains(soft, "MaxAuthTries 3") {
		t.Errorf("soft build99(false) must still emit session knob (MaxAuthTries 3):\n%s", soft)
	}
	strict := build99(ctx, true)
	if !strings.Contains(strict, "PermitRootLogin no") {
		t.Errorf("strict build99(true) must emit PermitRootLogin no:\n%s", strict)
	}
	if !strings.Contains(strict, "AllowGroups sshusers") {
		t.Errorf("strict build99(true) must emit AllowGroups sshusers:\n%s", strict)
	}
}

// TestPreserveKeyUsers asserts the coexistence helper emits a usermod -aG
// sshusers for each existing key user EXCEPT root, shell-quotes the name, and is
// best-effort (|| true). Root and empty entries are skipped.
func TestPreserveKeyUsers(t *testing.T) {
	script, added := preserveKeyUsers([]string{"root", "deploy", "", "alice"})

	wantAdded := []string{"deploy", "alice"}
	if len(added) != len(wantAdded) {
		t.Fatalf("added = %v, want %v", added, wantAdded)
	}
	for i, u := range wantAdded {
		if added[i] != u {
			t.Errorf("added[%d] = %q, want %q", i, added[i], u)
		}
	}
	for _, want := range []string{
		"usermod -aG sshusers 'deploy' 2>/dev/null || true",
		"usermod -aG sshusers 'alice' 2>/dev/null || true",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q\n---\n%s", want, script)
		}
	}
	// root must NEVER be granted via this path (it has its own policy).
	if strings.Contains(script, "sshusers 'root'") {
		t.Errorf("root must not be added to sshusers:\n%s", script)
	}
}

// TestPreserveKeyUsersEmpty asserts no work / no script when there are no
// non-root key users (the greenfield / nothing-to-do case).
func TestPreserveKeyUsersEmpty(t *testing.T) {
	if script, added := preserveKeyUsers([]string{"root"}); script != "" || added != nil {
		t.Errorf("expected empty result for root-only input, got script=%q added=%v", script, added)
	}
	if script, added := preserveKeyUsers(nil); script != "" || added != nil {
		t.Errorf("expected empty result for nil input, got script=%q added=%v", script, added)
	}
}
