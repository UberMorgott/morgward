// Package stats captures a lightweight before/after system snapshot for the
// post-run summary. Best-effort: a failed capture must not abort a run.
package stats

import (
	"fmt"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// Snapshot is a point-in-time picture used to compute before/after deltas.
// Zero/empty fields mean "unknown" and are hidden in the summary.
type Snapshot struct {
	KernelVer      string
	PkgInstalled   int
	DiskUsedKB     int64
	DiskTotalKB    int64
	MemKB          int64
	SwapKB         int64
	ZramActive     bool
	OpenPorts      []string
	RootLogin      string // yes | no | prohibit-password
	KeyOnly        bool
	FirewallActive bool
	Fail2banActive bool
	PingMs         float64
	SpeedMBs       float64 // filled by the engine from A4 BenchResult, not the script
}

// snapScript is one stdin-safe bash script (NO heredocs — the script itself is
// piped to bash over stdin, so a heredoc would contend for it; see §A1). It
// echoes marker lines "<KEY>\t<value>" consumed by parseSnapshot. Every probe is
// best-effort and guarded so a missing tool just omits its marker.
const snapScript = `set +e
export LC_ALL=C
printf 'KERNEL\t%s\n' "$(uname -r 2>/dev/null)"
printf 'PKGS\t%s\n' "$(dpkg -l 2>/dev/null | grep -c '^ii')"
printf 'DISK\t%s\n' "$(df -k --output=used,size / 2>/dev/null | tail -1)"
printf 'MEM\t%s\n' "$(free -k 2>/dev/null | awk '/^Mem:/{print $2}')"
printf 'SWAP\t%s\n' "$(free -k 2>/dev/null | awk '/^Swap:/{print $2}')"
if swapon --show=NAME --noheadings 2>/dev/null | grep -q zram; then
  printf 'ZRAM\t%s\n' yes
else
  printf 'ZRAM\t%s\n' no
fi
printf 'PORTS\t%s\n' "$(ss -tlnH 2>/dev/null | awk '{print $4}' | tr '\n' ' ')"
sshd -T 2>/dev/null | grep -Ei '^(permitrootlogin|passwordauthentication) ' | while IFS= read -r line; do
  printf 'SSHD\t%s\n' "$line"
done
if iptables -S 2>/dev/null | grep -q -- '-P INPUT DROP' || ufw status 2>/dev/null | grep -qi active || nft list ruleset 2>/dev/null | grep -qi 'policy drop'; then
  printf 'FW\t%s\n' yes
else
  printf 'FW\t%s\n' no
fi
printf 'F2B\t%s\n' "$(systemctl is-active fail2ban 2>/dev/null)"
printf 'PING\t%s\n' "$(ping -c3 -w5 1.1.1.1 2>/dev/null | tail -1)"
`

// Capture runs the snapshot script (via Sudo, so it works whether the client is
// connected as root — for the "before" snapshot — or as the admin user, for the
// "after" snapshot) and parses the result. Best-effort: on a transport error it
// returns whatever partial snapshot it parsed plus the error (for logging). The
// caller must NOT abort the run on a non-nil error.
func Capture(cli *sshx.Client) (*Snapshot, error) {
	r := cli.Sudo(snapScript)
	snap := parseSnapshot(r.Stdout)
	if r.Err != nil {
		return snap, fmt.Errorf("stats capture transport error: %w", r.Err)
	}
	if r.RC != 0 {
		return snap, fmt.Errorf("stats capture exited rc=%d: %s", r.RC, r.Out())
	}
	return snap, nil
}
