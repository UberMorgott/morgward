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
	conf := build99(ctx)

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
	write := buildDangerWrite()
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

	// RequiredRSASize (OpenSSH 9.1+) must be present on confirmed 24.04/26.04
	// (both ship >= 9.1), and ABSENT on the both-false fallback so the config
	// validates on an off-target <24.04 sshd (e.g. 22.04 jammy / 8.9p1).
	if c := mk(false, true); !strings.Contains(c, "RequiredRSASize 3072") {
		t.Errorf("26.04 must emit RequiredRSASize:\n%s", c)
	}
	if c := mk(true, false); !strings.Contains(c, "RequiredRSASize 3072") {
		t.Errorf("24.04 must emit RequiredRSASize:\n%s", c)
	}
	if c := mk(false, false); strings.Contains(c, "RequiredRSASize") {
		t.Errorf("both-false fallback must OMIT RequiredRSASize (9.1+) for jammy/8.9 compat:\n%s", c)
	}
}

// M07: both idle timeouts are GREENFIELD-ONLY. ChannelTimeout closes an idle
// session channel; UnusedConnectionTimeout reaps a connection with no open channel
// — and OpenSSH does not count forwarding listeners as channels, so an idle
// `ssh -N -R` reverse tunnel would die 600s after connect. Neither may touch a
// brownfield operator's long-lived sessions/tunnels. Both need OpenSSH >= 9.2, so
// 22.04 gets neither.
func TestCryptoBlockIdleTimeouts(t *testing.T) {
	ctx := newTestCtx()
	mk := func(is2404, greenfield bool, maj, min int) string {
		ctx.Facts.Is2404, ctx.Facts.Is2604 = is2404, false
		ctx.Facts.Is2204 = false
		ctx.Facts.Greenfield = greenfield
		ctx.Facts.SSHDMajor, ctx.Facts.SSHDMinor = maj, min
		return cryptoBlock(ctx)
	}

	green := mk(true, true, 9, 6)
	if !strings.Contains(green, "UnusedConnectionTimeout 600") || !strings.Contains(green, "ChannelTimeout session:*=1800") {
		t.Errorf("greenfield 24.04 must emit both idle timeouts:\n%s", green)
	}
	brown := mk(true, false, 9, 6)
	if strings.Contains(brown, "UnusedConnectionTimeout") {
		t.Errorf("brownfield must NOT reap idle connections (kills `ssh -N -R` tunnels):\n%s", brown)
	}
	if strings.Contains(brown, "ChannelTimeout") {
		t.Errorf("brownfield must NOT cut idle session channels:\n%s", brown)
	}
	old := mk(false, true, 8, 9) // 22.04 / OpenSSH 8.9 rejects both directives
	if strings.Contains(old, "UnusedConnectionTimeout") || strings.Contains(old, "ChannelTimeout") {
		t.Errorf("sshd 8.9 must emit neither idle timeout (sshd -t would reject them):\n%s", old)
	}
}

// TestCryptoBlockSSHDVersionGate covers the 22.04 support range: the sshd knobs
// gate on the PROBED OpenSSH version as well as the release flag, so jammy
// (8.9p1) gets none of them and a backported sshd on an unconfirmed release gets
// exactly the ones its version supports.
func TestCryptoBlockSSHDVersionGate(t *testing.T) {
	ctx := newTestCtx()
	mk := func(is2204 bool, maj, min int) string {
		ctx.Facts.Is2404, ctx.Facts.Is2604 = false, false
		ctx.Facts.Is2204 = is2204
		ctx.Facts.SSHDMajor, ctx.Facts.SSHDMinor = maj, min
		return cryptoBlock(ctx)
	}

	// 22.04 jammy / OpenSSH 8.9p1 — none of the version-gated directives.
	jammy := mk(true, 8, 9)
	for _, forbidden := range []string{"mlkem768x25519-sha256", "PerSourcePenalties", "RequiredRSASize"} {
		if strings.Contains(jammy, forbidden) {
			t.Errorf("22.04 / sshd 8.9 must NOT emit %q:\n%s", forbidden, jammy)
		}
	}
	// Backported sshd 9.8 on an unconfirmed release: RequiredRSASize (9.1+) and
	// PerSourcePenalties (9.8+) yes, mlkem (9.9+) no.
	bp := mk(false, 9, 8)
	if !strings.Contains(bp, "RequiredRSASize 3072") || !strings.Contains(bp, "PerSourcePenalties") {
		t.Errorf("sshd 9.8 must emit RequiredRSASize + PerSourcePenalties:\n%s", bp)
	}
	if strings.Contains(bp, "mlkem768x25519-sha256") {
		t.Errorf("sshd 9.8 must NOT emit mlkem (9.9+):\n%s", bp)
	}
}

// TestA2SSHBuild99NoLockdown guards the security invariant: the full-run A2SSH
// step is crypto-only — build99 must NEVER impose the access lockdown (no
// AllowGroups and no PermitRootLogin line at all), so the default `run` can't lock
// the operator out. Crypto + a session knob must still be present. The opt-in
// lockdown lives in A2Danger (danger99), asserted separately.
func TestA2SSHBuild99NoLockdown(t *testing.T) {
	ctx := newTestCtx()
	conf := build99(ctx)
	if strings.Contains(conf, "AllowGroups") {
		t.Errorf("build99 must NOT emit AllowGroups:\n%s", conf)
	}
	if strings.Contains(conf, "PermitRootLogin") {
		t.Errorf("build99 must NOT emit any PermitRootLogin line:\n%s", conf)
	}
	if !strings.Contains(conf, "Ciphers ") {
		t.Errorf("build99 must still emit crypto (Ciphers):\n%s", conf)
	}
	if !strings.Contains(conf, "MaxAuthTries 3") {
		t.Errorf("build99 must still emit session knob (MaxAuthTries 3):\n%s", conf)
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
