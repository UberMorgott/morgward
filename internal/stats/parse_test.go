package stats

import (
	"reflect"
	"testing"
)

func TestParseDiskKB(t *testing.T) {
	out := "Used 1K-blocks\n2831052 30828540\n"
	used, total := parseDiskKB(out)
	if used != 2831052 || total != 30828540 {
		t.Fatalf("got used=%d total=%d", used, total)
	}
}

func TestParsePkgCount(t *testing.T) {
	if n := parsePkgCount("431\n"); n != 431 {
		t.Fatalf("got %d", n)
	}
}

func TestParsePingMs(t *testing.T) {
	out := "rtt min/avg/max/mdev = 10.2/12.1/14.0/1.3 ms\n"
	if ms := parsePingMs(out); ms < 12.0 || ms > 12.2 {
		t.Fatalf("got %v", ms)
	}
}

func TestParseSSHD(t *testing.T) {
	out := "permitrootlogin no\npasswordauthentication no\n"
	root, keyOnly := parseSSHD(out)
	if root != "no" || !keyOnly {
		t.Fatalf("got root=%q keyOnly=%v", root, keyOnly)
	}
}

func TestParseDiskKBEmpty(t *testing.T) {
	used, total := parseDiskKB("")
	if used != 0 || total != 0 {
		t.Fatalf("got used=%d total=%d", used, total)
	}
}

func TestParsePingMsAbsent(t *testing.T) {
	if ms := parsePingMs("100% packet loss\n"); ms != 0 {
		t.Fatalf("got %v, want 0", ms)
	}
}

func TestParseSSHDProhibitPassword(t *testing.T) {
	out := "permitrootlogin prohibit-password\npasswordauthentication yes\n"
	root, keyOnly := parseSSHD(out)
	if root != "prohibit-password" || keyOnly {
		t.Fatalf("got root=%q keyOnly=%v", root, keyOnly)
	}
}

const freeFixture = `              total        used        free      shared  buff/cache   available
Mem:        4012345     512345     2500000       12345     1000000     3300000
Swap:       2097148           0     2097148
`

func TestParseMemKB(t *testing.T) {
	if got := parseMemKB(freeFixture); got != 4012345 {
		t.Fatalf("got %d", got)
	}
}

func TestParseSwapKB(t *testing.T) {
	if got := parseSwapKB(freeFixture); got != 2097148 {
		t.Fatalf("got %d", got)
	}
}

func TestParseMemSwapMissing(t *testing.T) {
	if got := parseMemKB(""); got != 0 {
		t.Fatalf("mem got %d", got)
	}
	if got := parseSwapKB(""); got != 0 {
		t.Fatalf("swap got %d", got)
	}
}

// ss -tlnH (no header) sample: State Recv-Q Send-Q Local:Port Peer:Port ...
const ssFixture = `LISTEN 0      4096         0.0.0.0:22         0.0.0.0:*
LISTEN 0      4096      127.0.0.53%lo:53         0.0.0.0:*
LISTEN 0      511          0.0.0.0:80         0.0.0.0:*
LISTEN 0      4096            [::]:22            [::]:*
LISTEN 0      511             [::]:80            [::]:*
`

func TestParsePorts(t *testing.T) {
	got := parsePorts(ssFixture)
	want := []string{"22", "53", "80"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParsePortsEmpty(t *testing.T) {
	if got := parsePorts(""); len(got) != 0 {
		t.Fatalf("got %v want empty", got)
	}
}

func TestParseSnapshot(t *testing.T) {
	raw := "KERNEL\t6.8.0-31-generic\n" +
		"PKGS\t431\n" +
		"DISK\t2831052 30828540\n" +
		"MEM\t4012345\n" +
		"SWAP\t2097148\n" +
		"ZRAM\tyes\n" +
		"PORTS\t0.0.0.0:22 0.0.0.0:80\n" +
		"SSHD\tpermitrootlogin no\n" +
		"SSHD\tpasswordauthentication no\n" +
		"FW\tyes\n" +
		"F2B\tactive\n" +
		"PING\trtt min/avg/max/mdev = 10.2/12.1/14.0/1.3 ms\n"

	s := parseSnapshot(raw)
	if s.KernelVer != "6.8.0-31-generic" {
		t.Errorf("kernel %q", s.KernelVer)
	}
	if s.PkgInstalled != 431 {
		t.Errorf("pkgs %d", s.PkgInstalled)
	}
	if s.DiskUsedKB != 2831052 || s.DiskTotalKB != 30828540 {
		t.Errorf("disk used=%d total=%d", s.DiskUsedKB, s.DiskTotalKB)
	}
	if s.MemKB != 4012345 {
		t.Errorf("mem %d", s.MemKB)
	}
	if s.SwapKB != 2097148 {
		t.Errorf("swap %d", s.SwapKB)
	}
	if !s.ZramActive {
		t.Errorf("zram not active")
	}
	if want := []string{"22", "80"}; !reflect.DeepEqual(s.OpenPorts, want) {
		t.Errorf("ports %v want %v", s.OpenPorts, want)
	}
	if s.RootLogin != "no" || !s.KeyOnly {
		t.Errorf("sshd root=%q keyOnly=%v", s.RootLogin, s.KeyOnly)
	}
	if !s.FirewallActive {
		t.Errorf("firewall not active")
	}
	if !s.Fail2banActive {
		t.Errorf("fail2ban not active")
	}
	if s.PingMs < 12.0 || s.PingMs > 12.2 {
		t.Errorf("ping %v", s.PingMs)
	}
}

func TestParseSnapshotPartial(t *testing.T) {
	// Missing/empty markers must yield zero values, never panic.
	s := parseSnapshot("KERNEL\t6.8.0-31-generic\nF2B\tinactive\nFW\tno\n")
	if s.KernelVer != "6.8.0-31-generic" {
		t.Errorf("kernel %q", s.KernelVer)
	}
	if s.PkgInstalled != 0 || s.MemKB != 0 || len(s.OpenPorts) != 0 {
		t.Errorf("expected zero values, got %+v", s)
	}
	if s.Fail2banActive || s.FirewallActive || s.ZramActive {
		t.Errorf("expected inactive flags, got %+v", s)
	}
}
