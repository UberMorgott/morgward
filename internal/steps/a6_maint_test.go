package steps

import (
	"strings"
	"testing"
)

// M06: chrony is INSTALLED only on 22.04/24.04 (26.04 ships it), and never when it
// is already active. Either way the script falls back to `timedatectl set-ntp true`,
// so time sync can never end up off, and the drop-in lands under conf.d so the
// distro chrony.conf and any operator sources.d/ pool stay untouched.
func TestTimeSyncScriptInstallGate(t *testing.T) {
	with := timeSyncScript(true)
	without := timeSyncScript(false)

	if !strings.Contains(with, " install -y") || !strings.Contains(with, "chrony") {
		t.Errorf("22.04/24.04 must install chrony:\n%s", with)
	}
	if strings.Contains(without, " install -y") {
		t.Errorf("26.04 already ships chrony — the script must not install it:\n%s", without)
	}
	// An absent install candidate must never fail A6: the install is optional.
	if !strings.Contains(with, "|| true") {
		t.Errorf("the chrony install must be optional (|| true):\n%s", with)
	}
	for _, s := range []string{with, without} {
		if !strings.Contains(s, "systemctl is-active chrony") {
			t.Errorf("an already-active chrony must be left alone:\n%s", s)
		}
		if !strings.Contains(s, "timedatectl set-ntp true") {
			t.Errorf("every branch must keep a time-sync fallback:\n%s", s)
		}
		if !strings.Contains(s, "/etc/chrony/conf.d/90-morgward.conf") {
			t.Errorf("the drop-in must land under conf.d, never in chrony.conf:\n%s", s)
		}
		if strings.Contains(s, "<<") {
			t.Errorf("no heredoc may appear in a remote script:\n%s", s)
		}
	}
}

// M05: fstrim is enabled only where a block device reports discard support, so a
// box on plain passthrough storage is not shown a timer we deliberately skipped.
func TestFstrimScriptGatedOnDiscard(t *testing.T) {
	if !strings.Contains(fstrimScript, "lsblk --discard") {
		t.Errorf("fstrim must be gated on real discard support:\n%s", fstrimScript)
	}
	if !strings.Contains(fstrimScript, "systemctl enable --now fstrim.timer") {
		t.Errorf("fstrim script must enable the timer:\n%s", fstrimScript)
	}
}
