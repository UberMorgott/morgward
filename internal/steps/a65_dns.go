package steps

import "strings"

// A65DNS implements §A6.5: DNS hardening on systemd-resolved.
//
// The fastest upstream resolver DIFFERS per host/datacenter — live calibration
// on an Aeza/Frankfurt box measured Cloudflare 1.1.1.1 at 46 ms (the SLOWEST
// there) while AdGuard / Quad9 came in at ~22-24 ms (a ~2x win). So instead of
// hardcoding a primary, this step MEASURES a fixed set of public resolvers (plus
// the provider's DHCP-pushed DNS when discoverable) with `dig` from the box and
// PINS the two fastest reliable ones as DNS=/secondary, keeping DoT opportunistic
// (survives captive portals / non-DoT resolvers) and DNSSEC allow-downgrade.
//
// It is not lockout-capable: on any failure it falls back to sane defaults or
// rolls back to the system default and returns StatusOK/Skip — never a hard error.
type A65DNS struct{}

func (A65DNS) ID() string    { return "A6.5" }
func (A65DNS) Title() string { return "DNS hardening (calibrated upstream + DoT/DNSSEC)" }

// dnsCalibrateScript measures every candidate resolver with `dig` and either
// pins the two fastest reliable ones or rolls back, all on the remote box. The
// Go side stays thin and just parses the RESULT/REVERTED line.
//
// It is delivered via base64 (putFile) and executed with bash, so it uses NO
// heredoc (§A1 stdin caveat: the outer script is piped to bash over stdin).
// It deliberately does NOT `set -e` — the measurement loop must tolerate failed
// digs, and all arithmetic is guarded against empty/zero values.
const dnsCalibrateScript = `#!/usr/bin/env bash
# DNS upstream calibration for systemd-resolved. No set -e (failed digs are normal).
CONF=/etc/systemd/resolved.conf.d/99-morgward-dns.conf
mkdir -p /etc/systemd/resolved.conf.d

# Ensure dig is available; if not, install dnsutils non-interactively. If it is
# still missing we fall back to hardcoded defaults rather than failing the step.
if ! command -v dig >/dev/null 2>&1; then
  DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=300 install -y -q dnsutils >/dev/null 2>&1 || true
fi

write_conf() {
  # $1 = "DNS line value". Written with printf (no heredoc) to stay quoting-safe.
  printf '%s\n' "[Resolve]" "DNS=$1" "FallbackDNS=1.1.1.1 8.8.8.8" "DNSOverTLS=opportunistic" "DNSSEC=allow-downgrade" "Cache=yes" > "$CONF"
  chmod 0644 "$CONF"
}

restart_resolved() {
  systemctl restart systemd-resolved >/dev/null 2>&1
}

verify_resolve() {
  getent hosts github.com >/dev/null 2>&1 && return 0
  resolvectl query github.com >/dev/null 2>&1 && return 0
  return 1
}

# Defaults used when dig is unavailable (old hardcoded behaviour).
if ! command -v dig >/dev/null 2>&1; then
  write_conf "1.1.1.1 9.9.9.9" "defaults"
  restart_resolved
  if verify_resolve; then
    echo "RESULT default cloudflare 1.1.1.1 0 quad9 9.9.9.9 dig-unavailable"
  else
    rm -f "$CONF"; restart_resolved
    echo "REVERTED resolve-broke"
  fi
  exit 0
fi

# Candidate set: name<TAB-less>:ip pairs. EXCLUDE 127.0.0.53/.54 (local stub —
# misleadingly fast cache, not a real upstream).
CAND_NAMES=(cloudflare google quad9 quad9b adguard controld)
CAND_IPS=(1.1.1.1 8.8.8.8 9.9.9.9 149.112.112.112 94.140.14.14 76.76.2.0)

# Add the provider's DHCP-pushed DNS if discoverable and not a local stub / dup.
DHCP_DNS=$(resolvectl status 2>/dev/null | awk '/DNS Servers/{print $3; exit}')
case "$DHCP_DNS" in
  127.0.0.53|127.0.0.54|"") DHCP_DNS="" ;;
esac
if [ -n "$DHCP_DNS" ]; then
  dup=0
  for ip in "${CAND_IPS[@]}"; do [ "$ip" = "$DHCP_DNS" ] && dup=1; done
  if [ "$dup" -eq 0 ]; then
    CAND_NAMES+=(provider)
    CAND_IPS+=("$DHCP_DNS")
  fi
fi

DOMAINS=(google.com github.com ubuntu.com cloudflare.com wikipedia.org debian.org)
ROUNDS=2

best_avg=-1; best_name=""; best_ip=""
second_avg=-1; second_name=""; second_ip=""

i=0
while [ "$i" -lt "${#CAND_IPS[@]}" ]; do
  name="${CAND_NAMES[$i]}"; ip="${CAND_IPS[$i]}"
  i=$((i+1))
  sum=0; cnt=0; fails=0
  r=0
  while [ "$r" -lt "$ROUNDS" ]; do
    r=$((r+1))
    for d in "${DOMAINS[@]}"; do
      qt=$(dig @"$ip" "$d" +tries=1 +time=2 +stats 2>/dev/null | awk '/Query time:/{print $4; exit}')
      if [ -n "$qt" ] 2>/dev/null && [ "$qt" -ge 0 ] 2>/dev/null; then
        sum=$((sum+qt)); cnt=$((cnt+1))
      else
        fails=$((fails+1))
      fi
    done
  done
  if [ "$cnt" -eq 0 ]; then
    echo "PROBE $name $ip avg=NA fails=$fails"
    continue
  fi
  avg=$((sum/cnt))
  echo "PROBE $name $ip avg=${avg} fails=$fails"
  # Unreliable if it failed >= 4 queries — not eligible to win.
  if [ "$fails" -ge 4 ]; then
    continue
  fi
  if [ "$best_avg" -lt 0 ] || [ "$avg" -lt "$best_avg" ]; then
    # demote current best to second (different IP)
    if [ "$best_avg" -ge 0 ] && [ "$best_ip" != "$ip" ]; then
      second_avg=$best_avg; second_name=$best_name; second_ip=$best_ip
    fi
    best_avg=$avg; best_name=$name; best_ip=$ip
  elif [ "$ip" != "$best_ip" ]; then
    if [ "$second_avg" -lt 0 ] || [ "$avg" -lt "$second_avg" ]; then
      second_avg=$avg; second_name=$name; second_ip=$ip
    fi
  fi
done

# Cloudflare's measured avg for the "[was: cloudflare Xms]" comparison is read by
# the Go side from the PROBE lines printed above, so nothing to compute here.

if [ "$best_avg" -lt 0 ]; then
  # No reliable candidate measured — keep sane defaults rather than nothing.
  write_conf "1.1.1.1 9.9.9.9" "no-reliable"
  restart_resolved
  if verify_resolve; then
    echo "RESULT default cloudflare 1.1.1.1 0 quad9 9.9.9.9 no-reliable-candidate"
  else
    rm -f "$CONF"; restart_resolved
    echo "REVERTED resolve-broke"
  fi
  exit 0
fi

# Build the DNS= line: winner first, secondary if we found a distinct one.
if [ -n "$second_ip" ] && [ "$second_ip" != "$best_ip" ]; then
  DNSLINE="$best_ip $second_ip"
else
  DNSLINE="$best_ip"
  second_name="-"; second_ip="-"; second_avg=-1
fi

write_conf "$DNSLINE" "calibrated"
restart_resolved

# Verify connectivity; rollback to system default if it broke.
if verify_resolve; then
  echo "RESULT calibrated $best_name $best_ip $best_avg $second_name $second_ip"
else
  rm -f "$CONF"; restart_resolved
  echo "REVERTED resolve-broke"
fi
exit 0
`

func (A65DNS) Run(ctx *Context) (Status, string, error) {
	if active := ctx.Cli.Run("systemctl is-active systemd-resolved").Out(); active != "active" {
		return StatusSkip, "systemd-resolved not active (different resolver)", nil
	}

	// Deliver the calibration script as a file (base64, stdin-safe) and execute
	// it. Doing the measure→pick→write→verify→rollback entirely on the box keeps
	// the Go side thin and the result a single parseable line.
	const scriptPath = "/tmp/morgward-dns-calibrate.sh"
	deliver := putFile(scriptPath, dnsCalibrateScript, "0755") +
		"bash " + scriptPath + "\n" +
		"rm -f " + scriptPath + "\n"

	r := ctx.Cli.Sudo(deliver)
	out := r.Stdout
	if r.RC != 0 && out == "" {
		return StatusFail, "DNS calibration failed: " + firstLine(r.Stderr), nil
	}

	// Surface the per-resolver probe lines for the run log, and capture
	// Cloudflare's measured avg for the "[was: …]" comparison.
	cfAvg := ""
	var result, reverted string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "PROBE "):
			ctx.Log.Detail("%s", l)
			f := strings.Fields(l)
			// PROBE <name> <ip> avg=<n> fails=<n>
			if len(f) >= 4 && f[2] == "1.1.1.1" {
				cfAvg = strings.TrimPrefix(f[3], "avg=")
			}
		case strings.HasPrefix(l, "RESULT "):
			result = l
		case strings.HasPrefix(l, "REVERTED"):
			reverted = l
		}
	}

	if reverted != "" {
		// DNS broke after re-pinning; the script already removed the drop-in and
		// restarted resolved. Never leave the box with broken DNS, never hard-fail.
		return StatusOK, "DNS calibration reverted (resolve broke), kept system default", nil
	}

	if result == "" {
		// Script produced no decision line — treat as inconclusive, not a failure.
		return StatusOK, "DNS calibration inconclusive — kept system default", nil
	}

	// RESULT <kind> <winName> <winIP> <winAvg> <secName> <secIP>
	f := strings.Fields(result)
	if len(f) < 7 {
		return StatusOK, "DNS calibrated (unparsed result kept)", nil
	}
	kind, winName, winIP, winAvg := f[1], f[2], f[3], f[4]
	secName, secIP := f[5], f[6]

	var b strings.Builder
	if kind == "default" {
		// dig unavailable or no reliable candidate — old hardcoded defaults applied.
		b.WriteString("DNS defaults applied: ")
		b.WriteString(winName)
		b.WriteString(" ")
		b.WriteString(winIP)
		if len(f) >= 8 {
			b.WriteString(" (")
			b.WriteString(f[7]) // reason: dig-unavailable / no-reliable-candidate
			b.WriteString(")")
		}
		return StatusOK, b.String(), nil
	}

	// Calibrated.
	b.WriteString("DNS calibrated: ")
	b.WriteString(winName)
	b.WriteString(" ")
	b.WriteString(winIP)
	b.WriteString(" (")
	b.WriteString(winAvg)
	b.WriteString("ms)")
	if secIP != "-" && secIP != "" {
		b.WriteString(" + ")
		b.WriteString(secName)
		b.WriteString(" ")
		b.WriteString(secIP)
	}
	if cfAvg != "" && cfAvg != "NA" && winIP != "1.1.1.1" {
		b.WriteString(" [was: cloudflare ")
		b.WriteString(cfAvg)
		b.WriteString("ms]")
	}
	return StatusOK, b.String(), nil
}
