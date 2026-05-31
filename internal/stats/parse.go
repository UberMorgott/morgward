package stats

import (
	"strconv"
	"strings"
)

// parseDiskKB parses `df -k --output=used,size /` output: a header line followed
// by a data line like "2831052 30828540". Returns (0,0) if the data line is
// missing or malformed.
func parseDiskKB(out string) (used, total int64) {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		u, errU := strconv.ParseInt(f[0], 10, 64)
		t, errT := strconv.ParseInt(f[1], 10, 64)
		if errU == nil && errT == nil {
			return u, t
		}
	}
	return 0, 0
}

// parsePkgCount parses a bare integer line (from `dpkg -l | grep -c '^ii'`).
func parsePkgCount(out string) int {
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return n
}

// parsePingMs extracts the avg field from a ping summary line like
// "rtt min/avg/max/mdev = 10.2/12.1/14.0/1.3 ms". Returns 0 if absent.
func parsePingMs(out string) float64 {
	for _, l := range strings.Split(out, "\n") {
		i := strings.Index(l, "=")
		if i < 0 || !strings.Contains(l, "/") {
			continue
		}
		// "10.2/12.1/14.0/1.3 ms"
		rhs := strings.TrimSpace(l[i+1:])
		rhs = strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(rhs, "ms")), " ")
		parts := strings.Split(rhs, "/")
		if len(parts) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err == nil {
			return v
		}
	}
	return 0
}

// parseSSHD reads effective sshd config lines (from `sshd -T`) for
// permitrootlogin and passwordauthentication. keyOnly = passwordauthentication==no.
func parseSSHD(out string) (rootLogin string, keyOnly bool) {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(strings.ToLower(strings.TrimSpace(l)))
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "permitrootlogin":
			rootLogin = f[1]
		case "passwordauthentication":
			keyOnly = f[1] == "no"
		}
	}
	return rootLogin, keyOnly
}

// freeKB pulls column 2 (total) from the `free -k` line whose first field
// (case-insensitive) matches label, e.g. "Mem:" or "Swap:".
func freeKB(out, label string) int64 {
	label = strings.ToLower(label)
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		if strings.ToLower(f[0]) == label {
			n, err := strconv.ParseInt(f[1], 10, 64)
			if err == nil {
				return n
			}
		}
	}
	return 0
}

// parseMemKB returns total memory KB from a `free -k` Mem line (col 2).
func parseMemKB(out string) int64 { return freeKB(out, "Mem:") }

// parseSwapKB returns total swap KB from a `free -k` Swap line (col 2).
func parseSwapKB(out string) int64 { return freeKB(out, "Swap:") }

// parsePorts extracts listening local ports from `ss -tlnH` output (no header).
// Each line's local address is field 4 (State Recv-Q Send-Q Local Peer …); the
// port is the substring after the final ':'. Results are deduped and returned in
// first-seen order.
func parsePorts(out string) []string {
	seen := make(map[string]bool)
	var ports []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		local := f[3]
		i := strings.LastIndex(local, ":")
		if i < 0 || i == len(local)-1 {
			continue
		}
		p := local[i+1:]
		if _, err := strconv.Atoi(p); err != nil {
			continue
		}
		if !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	return ports
}

// parseSnapshot splits the combined marker output ("KEY\tvalue" lines) and
// dispatches each line to the matching helper. Unknown/missing markers leave the
// corresponding field at its zero value (rendered as "unknown" by the summary).
func parseSnapshot(raw string) *Snapshot {
	s := &Snapshot{}
	var sshdLines strings.Builder
	for _, l := range strings.Split(raw, "\n") {
		key, val, ok := strings.Cut(l, "\t")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "KERNEL":
			s.KernelVer = val
		case "PKGS":
			s.PkgInstalled = parsePkgCount(val)
		case "DISK":
			s.DiskUsedKB, s.DiskTotalKB = parseDiskKB(val)
		case "MEM":
			s.MemKB = parseMemKB("Mem: " + val)
		case "SWAP":
			s.SwapKB = parseSwapKB("Swap: " + val)
		case "ZRAM":
			s.ZramActive = val == "yes"
		case "PORTS":
			// Re-emit each space-separated local address on its own ss-shaped line
			// so parsePorts (field 4) can extract the port.
			var b strings.Builder
			for _, tok := range strings.Fields(val) {
				b.WriteString("LISTEN 0 0 " + tok + " *:*\n")
			}
			s.OpenPorts = parsePorts(b.String())
		case "SSHD":
			sshdLines.WriteString(val)
			sshdLines.WriteByte('\n')
		case "FW":
			s.FirewallActive = val == "yes"
		case "F2B":
			s.Fail2banActive = val == "active"
		case "PINGGW":
			s.GatewayPingMs = parsePingMs(val)
		case "PINGNET":
			s.InternetPingMs = parsePingMs(val)
		}
	}
	s.RootLogin, s.KeyOnly = parseSSHD(sshdLines.String())
	return s
}
