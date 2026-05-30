package monitor

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestParseCPU(t *testing.T) {
	// Two /proc/stat snapshots. Fields: user nice system idle iowait irq softirq...
	// prev: idle_all = 100+0 = 100, total = 100+0+100+100+0 = 300
	// cur:  idle_all = 150+0 = 150, total = 200+0+150+150+0 = 500
	// Δtotal = 200, Δidle = 50, Δbusy = 150 → 150/200*100 = 75%
	prev := parseCPUStat("cpu  100 0 100 100 0\n")
	cur := parseCPUStat("cpu  200 0 150 150 0\n")

	if got := parseCPU(prev, cur); !approx(got, 75) {
		t.Fatalf("parseCPU = %v, want 75", got)
	}
	// First tick (no prev) must report -1.
	if got := parseCPU(Stat{}, cur); got != -1 {
		t.Fatalf("parseCPU(first) = %v, want -1", got)
	}
}

func TestParseMem(t *testing.T) {
	blob := "MemTotal:       1000 kB\n" +
		"MemFree:         200 kB\n" +
		"MemAvailable:    250 kB\n"
	// used% = (1 - 250/1000)*100 = 75
	if got := parseMem(blob); !approx(got, 75) {
		t.Fatalf("parseMem = %v, want 75", got)
	}
	// Missing MemAvailable → -1.
	if got := parseMem("MemTotal: 1000 kB\n"); got != -1 {
		t.Fatalf("parseMem(missing) = %v, want -1", got)
	}
}

func TestParseMemKB(t *testing.T) {
	blob := "MemTotal:       2048000 kB\n" +
		"MemAvailable:     512000 kB\n"
	used, total, ok := parseMemKB(blob)
	if !ok {
		t.Fatal("parseMemKB: expected ok")
	}
	if !approx(total, 2048000) {
		t.Errorf("totalKB = %v, want 2048000", total)
	}
	if !approx(used, 1536000) {
		t.Errorf("usedKB = %v, want 1536000", used)
	}
	// Missing MemAvailable → ok=false.
	if _, _, ok := parseMemKB("MemTotal: 1000 kB\n"); ok {
		t.Error("parseMemKB(missing): expected ok=false")
	}
}

func TestParseDiskPct(t *testing.T) {
	blob := "Filesystem     1024-blocks    Used Available Capacity Mounted on\n" +
		"/dev/sda1         41152736 17000000  24000000      42% /\n"
	if got := parseDiskPct(blob); !approx(got, 42) {
		t.Fatalf("parseDiskPct = %v, want 42", got)
	}
	// No `/` row → -1.
	noRoot := "Filesystem 1024-blocks Used Available Capacity Mounted on\n" +
		"/dev/sdb1 100 50 50 50% /data\n"
	if got := parseDiskPct(noRoot); got != -1 {
		t.Fatalf("parseDiskPct(no root) = %v, want -1", got)
	}
}

func TestParseDiskKB(t *testing.T) {
	blob := "Filesystem     1024-blocks    Used Available Capacity Mounted on\n" +
		"/dev/vda1         41943040 7340032  34603008      18% /\n"
	used, total, ok := parseDiskKB(blob)
	if !ok {
		t.Fatal("parseDiskKB: expected ok")
	}
	if !approx(total, 41943040) {
		t.Errorf("totalKB = %v, want 41943040", total)
	}
	if !approx(used, 7340032) {
		t.Errorf("usedKB = %v, want 7340032", used)
	}
	// No `/` row → ok=false.
	if _, _, ok := parseDiskKB("nope\n"); ok {
		t.Error("parseDiskKB(no root): expected ok=false")
	}
}

func TestParseCPUMHz(t *testing.T) {
	blob := "processor\t: 0\ncpu MHz\t\t: 2400.000\nprocessor\t: 1\ncpu MHz\t\t: 2401.000\n"
	if got := parseCPUMHz(blob); !approx(got, 2400.000) {
		t.Fatalf("parseCPUMHz = %v, want 2400.000", got)
	}
	// ARM/virtualized hosts may omit cpu MHz entirely → 0.
	if got := parseCPUMHz("processor\t: 0\nBogoMIPS\t: 48.00\n"); got != 0 {
		t.Fatalf("parseCPUMHz(no-mhz) = %v, want 0", got)
	}
}

func TestParseCombinedBlob(t *testing.T) {
	// Sanity: the concatenated output (proc/stat + meminfo + df + cpuinfo) parses per metric.
	blob := "cpu  200 0 150 150 0\n" +
		"cpu0 100 0 75 75 0\n" +
		"MemTotal:       1000 kB\n" +
		"MemAvailable:    250 kB\n" +
		"Filesystem 1024-blocks Used Available Capacity Mounted on\n" +
		"/dev/sda1 100 75 25 80% /\n" +
		"cpu MHz\t\t: 3200.000\n"
	if got := parseMem(blob); !approx(got, 75) {
		t.Fatalf("parseMem(combined) = %v, want 75", got)
	}
	if got := parseDiskPct(blob); !approx(got, 80) {
		t.Fatalf("parseDiskPct(combined) = %v, want 80", got)
	}
	if got := parseCPUMHz(blob); !approx(got, 3200) {
		t.Fatalf("parseCPUMHz(combined) = %v, want 3200", got)
	}
	if used, total, ok := parseMemKB(blob); !ok || !approx(used, 750) || !approx(total, 1000) {
		t.Fatalf("parseMemKB(combined) = %v,%v,%v want 750,1000,true", used, total, ok)
	}
	if used, total, ok := parseDiskKB(blob); !ok || !approx(used, 75) || !approx(total, 100) {
		t.Fatalf("parseDiskKB(combined) = %v,%v,%v want 75,100,true", used, total, ok)
	}
	// cpu line: 200+0+150+150+0 = 500 total; idle_all = idle(150)+iowait(0) = 150.
	if s := parseCPUStat(blob); !s.OK || s.Total != 500 || s.Idle != 150 {
		t.Fatalf("parseCPUStat(combined) = %+v, want OK total=500 idle=150", s)
	}
}
