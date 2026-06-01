// Package tweaks audits morgward's own per-tweak changes. It builds a probe
// registry filtered to the box (mode + Ubuntu version), runs every probe in a
// single privileged round-trip, and scores each via its Want predicate. It is
// honest about scope: it audits the tweaks morgward applies, not arbitrary
// system state. View-only — no rollback.
package tweaks

import (
	"fmt"
	"strings"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/ui"
)

// Probe is one auditable tweak. Cmd is a shell snippet (single logical line, NO
// heredoc) whose stdout is captured and passed to Want. Step groups probes for
// display. The TUI localizes the display name by ID (see localTweakName),
// falling back to Name (English) when no localized entry exists.
type Probe struct {
	ID   string
	Step string
	Name string
	Cmd  string
	Want func(out string) bool
	// Informational marks a probe whose unset state is EXPECTED on the default
	// (safe) path — the value is reported but a non-match is NOT a failure. The
	// access-policy probes (PermitRootLogin / PasswordAuthentication / AllowGroups)
	// are informational because A2-safe deliberately leaves the image default
	// untouched; they only become "applied" after the opt-in A2-danger lockdown.
	// Renderers must show these as a neutral state line, never red "не применён".
	Informational bool
}

// Result pairs a probe with its observed value and applied verdict.
type Result struct {
	Probe   Probe
	Applied bool
	Detail  string
	// Informational mirrors Probe.Informational so renderers that only carry the
	// Result can treat the row as a neutral state report (not a pass/fail verdict).
	Informational bool
}

// eq matches when the trimmed output equals want.
func eq(want string) func(string) bool {
	return func(o string) bool { return strings.TrimSpace(o) == want }
}

// has matches when the output contains sub.
func has(sub string) func(string) bool {
	return func(o string) bool { return strings.Contains(o, sub) }
}

// rpFilterWant returns the rp_filter verdict matching what A5 actually applies:
// strict (=1) on a non-forwarding box, loose (=2) when forwarding/routing is
// active (A5 sets loose there so asymmetric VPN/router paths survive). A
// coexisting box thus reads "applied", not a false failure.
func rpFilterWant(facts *detect.Facts) func(string) bool {
	if facts.Forwarding {
		return eq("2")
	}
	return eq("1")
}

// fileExists is the shell snippet for "is this path present".
func fileExists(path string) string {
	return fmt.Sprintf("test -e %s && echo 1 || echo 0", path)
}

// grepFile is the shell snippet for "does this file contain pat".
func grepFile(pat, path string) string {
	return fmt.Sprintf("grep -qsF %q %s && echo 1 || echo 0", pat, path)
}

// pkgInstalled is the shell snippet for "is this dpkg package installed".
func pkgInstalled(pkg string) string {
	return fmt.Sprintf("dpkg-query -W -f='${Status}' %s 2>/dev/null | grep -q 'install ok installed' && echo 1 || echo 0", pkg)
}

// Registry returns the probes that apply to this box, filtered by mode and
// Ubuntu version so a tweak that never applies here is never shown as missing.
func Registry(facts *detect.Facts, cfg *config.Config) []Probe {
	strict := cfg.Mode == config.ModeStrict
	port := cfg.Port
	if port == 0 {
		port = 22
	}

	ps := []Probe{
		// --- A1 firewall ---
		{ID: "a1.input_drop", Step: "A1", Name: "INPUT policy DROP",
			Cmd: "iptables -S INPUT 2>/dev/null | grep -m1 '^-P INPUT'", Want: has("DROP")},
		{ID: "a1.ssh_accept", Step: "A1", Name: "SSH port accepted",
			Cmd: fmt.Sprintf("iptables -S INPUT 2>/dev/null | grep -- '--dport %d' | grep -m1 ACCEPT", port), Want: has("ACCEPT")},
		{ID: "a1.rules_v4", Step: "A1", Name: "rules.v4 persisted",
			Cmd: fileExists("/etc/iptables/rules.v4"), Want: eq("1")},
		{ID: "a1.persistent", Step: "A1", Name: "iptables-persistent installed",
			Cmd: pkgInstalled("iptables-persistent"), Want: eq("1")},

		// --- A2 ssh ---
		{ID: "a2.conf00", Step: "A2", Name: "00-hardening.conf",
			Cmd: fileExists("/etc/ssh/sshd_config.d/00-hardening.conf"), Want: eq("1")},
		{ID: "a2.conf99", Step: "A2", Name: "99-hardening.conf",
			Cmd: fileExists("/etc/ssh/sshd_config.d/99-hardening.conf"), Want: eq("1")},
		{ID: "a2.allowgroups", Step: "A2", Name: "AllowGroups sshusers",
			Cmd: "sshd -T 2>/dev/null | grep -i '^allowgroups'", Want: has("sshusers"), Informational: true},
		{ID: "a2.ecdsa_absent", Step: "A2", Name: "ECDSA host key removed",
			Cmd: "test -e /etc/ssh/ssh_host_ecdsa_key && echo present || echo absent", Want: eq("absent")},
		{ID: "a2.ssh_active", Step: "A2", Name: "ssh service active",
			Cmd: "systemctl is-active ssh 2>/dev/null || systemctl is-active sshd 2>/dev/null", Want: has("active")},
	}

	// PermitRootLogin + PasswordAuthentication are mode-dependent AND informational:
	// the default (safe) path leaves the image access policy untouched, so an unset
	// state is expected — only the opt-in A2-danger lockdown (or CLI strict) sets
	// PermitRootLogin no / PasswordAuthentication no. The Want predicate still
	// reflects the mode-expected value (so a locked-down box reads as "applied"),
	// but Informational tells renderers not to flag a non-match as a failure.
	rootWant := "prohibit-password"
	passWant := "yes"
	if strict {
		rootWant = "no"
		passWant = "no"
	}
	ps = append(ps,
		Probe{ID: "a2.permitroot", Step: "A2", Name: "PermitRootLogin",
			Cmd: "sshd -T 2>/dev/null | awk '/^permitrootlogin/{print $2}'", Want: eq(rootWant), Informational: true},
		Probe{ID: "a2.passauth", Step: "A2", Name: "PasswordAuthentication",
			Cmd: "sshd -T 2>/dev/null | awk '/^passwordauthentication/{print $2}'", Want: eq(passWant), Informational: true},
	)

	if facts.Is2604 {
		ps = append(ps, Probe{ID: "a2.kex_mlkem", Step: "A2", Name: "PQ KEX (mlkem768)",
			Cmd: "sshd -T 2>/dev/null | grep -i '^kexalgorithms'", Want: has("mlkem768x25519-sha256")})
	}

	ps = append(ps,
		// --- A2.5 cloud-init ---
		Probe{ID: "a25.disabled", Step: "A2.5", Name: "cloud-init disabled",
			Cmd:  "test -f /etc/cloud/cloud-init.disabled && echo 1 || { command -v cloud-init >/dev/null 2>&1 && echo 0 || echo na; }",
			Want: func(o string) bool { o = strings.TrimSpace(o); return o == "1" || o == "na" }},

		// --- A3 fail2ban ---
		Probe{ID: "a3.installed", Step: "A3", Name: "fail2ban installed",
			Cmd: pkgInstalled("fail2ban"), Want: eq("1")},
		Probe{ID: "a3.jail_local", Step: "A3", Name: "jail.local",
			Cmd: fileExists("/etc/fail2ban/jail.local"), Want: eq("1")},
		Probe{ID: "a3.sshd_jail", Step: "A3", Name: "sshd jail active",
			Cmd: "fail2ban-client status sshd >/dev/null 2>&1 && echo active || echo down", Want: eq("active")},

		// --- A4 network ---
		Probe{ID: "a4.net_tune", Step: "A4", Name: "99-net-tune.conf",
			Cmd: fileExists("/etc/sysctl.d/99-net-tune.conf"), Want: eq("1")},
		Probe{ID: "a4.bbr_conf", Step: "A4", Name: "99-bbr.conf",
			Cmd: fileExists("/etc/sysctl.d/99-bbr.conf"), Want: eq("1")},
		Probe{ID: "a4.bbr_module", Step: "A4", Name: "tcp_bbr loaded",
			Cmd: "lsmod | grep -q '^tcp_bbr' && echo 1 || echo 0", Want: eq("1")},
		Probe{ID: "a4.bbr_active", Step: "A4", Name: "BBR congestion control",
			Cmd: "sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null", Want: has("bbr")},
		Probe{ID: "a4.qdisc", Step: "A4", Name: "fq qdisc default",
			Cmd: "sysctl -n net.core.default_qdisc 2>/dev/null", Want: has("fq")},
		Probe{ID: "a4.io_sched", Step: "A4", Name: "I/O scheduler (udev)",
			Cmd:  "ls /sys/block 2>/dev/null | grep -q '^vd' && { test -f /etc/udev/rules.d/60-io-scheduler.rules && echo 1 || echo 0; } || echo na",
			Want: func(o string) bool { o = strings.TrimSpace(o); return o == "1" || o == "na" }},

		// --- A5 kernel ---
		Probe{ID: "a5.harden_conf", Step: "A5", Name: "99-zz-kernel-harden.conf",
			Cmd: fileExists("/etc/sysctl.d/99-zz-kernel-harden.conf"), Want: eq("1")},
		Probe{ID: "a5.core_pattern", Step: "A5", Name: "core_pattern disabled",
			Cmd: "sysctl -n kernel.core_pattern 2>/dev/null", Want: has("/bin/false")},
		Probe{ID: "a5.rp_filter", Step: "A5", Name: "rp_filter strict",
			Cmd: "sysctl -n net.ipv4.conf.all.rp_filter 2>/dev/null", Want: rpFilterWant(facts)},
		Probe{ID: "a5.kptr", Step: "A5", Name: "kptr_restrict",
			Cmd: "sysctl -n kernel.kptr_restrict 2>/dev/null", Want: eq("2")},
		Probe{ID: "a5.thp", Step: "A5", Name: "THP madvise",
			Cmd: "cat /sys/kernel/mm/transparent_hugepage/enabled 2>/dev/null", Want: has("[madvise]")},

		// --- A6 maintenance ---
		Probe{ID: "a6.journald", Step: "A6", Name: "journald cap",
			Cmd: fileExists("/etc/systemd/journald.conf.d/99-vps-cap.conf"), Want: eq("1")},
		Probe{ID: "a6.needrestart", Step: "A6", Name: "needrestart auto",
			Cmd: fileExists("/etc/needrestart/conf.d/50-autorestart.conf"), Want: eq("1")},
		Probe{ID: "a6.nofile", Step: "A6", Name: "NOFILE limit",
			Cmd: fileExists("/etc/systemd/system.conf.d/limits.conf"), Want: eq("1")},
		Probe{ID: "a6.ntp", Step: "A6", Name: "NTP enabled",
			Cmd: "timedatectl show -p NTP --value 2>/dev/null", Want: has("yes")},

		// --- A6.5 DNS ---
		Probe{ID: "a65.dns_conf", Step: "A6.5", Name: "resolved DNS hardening",
			Cmd:  "test -f /etc/systemd/resolved.conf.d/99-morgward-dns.conf && echo 1 || { systemctl is-active systemd-resolved >/dev/null 2>&1 && echo 0 || echo na; }",
			Want: func(o string) bool { o = strings.TrimSpace(o); return o == "1" || o == "na" }},
		Probe{ID: "a65.dot", Step: "A6.5", Name: "DNSOverTLS opportunistic",
			Cmd: grepFile("DNSOverTLS=opportunistic", "/etc/systemd/resolved.conf.d/99-morgward-dns.conf"), Want: eq("1")},

		// --- A6.7 memory ---
		Probe{ID: "a67.zram_conf", Step: "A6.7", Name: "zram-generator.conf",
			Cmd: fileExists("/etc/systemd/zram-generator.conf"), Want: eq("1")},
		Probe{ID: "a67.zram_sysctl", Step: "A6.7", Name: "zram swappiness",
			Cmd: fileExists("/etc/sysctl.d/99-zram.conf"), Want: eq("1")},
		Probe{ID: "a67.zram_active", Step: "A6.7", Name: "zram swap active",
			Cmd: "swapon --show=NAME --noheadings 2>/dev/null | grep -q zram && echo 1 || echo 0", Want: eq("1")},
		Probe{ID: "a67.earlyoom", Step: "A6.7", Name: "earlyoom active",
			Cmd: "systemctl is-active earlyoom 2>/dev/null", Want: has("active")},

		// --- A9 unattended-upgrades ---
		Probe{ID: "a9.installed", Step: "A9", Name: "unattended-upgrades installed",
			Cmd: pkgInstalled("unattended-upgrades"), Want: eq("1")},
		Probe{ID: "a9.auto", Step: "A9", Name: "20auto-upgrades",
			Cmd: fileExists("/etc/apt/apt.conf.d/20auto-upgrades"), Want: eq("1")},
		Probe{ID: "a9.local", Step: "A9", Name: "52-unattended-upgrades-local",
			Cmd: fileExists("/etc/apt/apt.conf.d/52-unattended-upgrades-local"), Want: eq("1")},

		// --- A10 detection ---
		Probe{ID: "a10.auditd", Step: "A10", Name: "auditd installed",
			Cmd: pkgInstalled("auditd"), Want: eq("1")},
		Probe{ID: "a10.audit_rules", Step: "A10", Name: "99-vps.rules",
			Cmd: fileExists("/etc/audit/rules.d/99-vps.rules"), Want: eq("1")},
		Probe{ID: "a10.auditd_active", Step: "A10", Name: "auditd active",
			Cmd: "systemctl is-active auditd 2>/dev/null", Want: has("active")},
		Probe{ID: "a10.notify", Step: "A10", Name: "ssh-login-notify",
			Cmd: fileExists("/usr/local/sbin/ssh-login-notify.sh"), Want: eq("1")},
		Probe{ID: "a10.pam", Step: "A10", Name: "pam.d/sshd notify line",
			Cmd: "grep -q ssh-login-notify /etc/pam.d/sshd && echo 1 || echo 0", Want: eq("1")},
		Probe{ID: "a10.log_rule", Step: "A10", Name: "inbound LOG rule",
			Cmd: "iptables -S INPUT 2>/dev/null | grep -q 'ipt-drop-in' && echo 1 || echo 0", Want: eq("1")},
	)

	if facts.HasIPv6 {
		ps = append(ps, Probe{ID: "a1.rules_v6", Step: "A1", Name: "rules.v6 persisted",
			Cmd: fileExists("/etc/iptables/rules.v6"), Want: eq("1")})
	}

	// Locked decision: the strict-only A10 extras (module blacklist + /dev/shm
	// hardening) are dropped — they are no longer probed in either mode.

	return ps
}

// Run executes every applicable probe in ONE privileged round-trip. Each probe's
// stdout is captured tab-delimited (id<TAB>value) and scored by its Want. Best
// effort: a transport error yields all-not-applied rather than aborting.
func Run(cli *sshx.Client, log *ui.Logger, facts *detect.Facts, cfg *config.Config) []Result {
	probes := Registry(facts, cfg)
	if len(probes) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("set +e\n")
	for _, p := range probes {
		// Single-quote the literal ID; collapse the probe's stdout to one
		// trimmed line so the parser sees exactly "id<TAB>value".
		fmt.Fprintf(&b, "printf '%%s\\t' '%s'; { %s ; } 2>/dev/null | tr '\\n' ' ' | head -c 300; printf '\\n'\n", p.ID, p.Cmd)
	}

	res := cli.Sudo(b.String())

	vals := make(map[string]string, len(probes))
	for line := range strings.SplitSeq(res.Stdout, "\n") {
		id, val, found := strings.Cut(line, "\t")
		if !found {
			continue
		}
		vals[id] = strings.TrimSpace(val)
	}

	out := make([]Result, 0, len(probes))
	applied := 0
	for _, p := range probes {
		v := vals[p.ID]
		ok := p.Want(v)
		if ok {
			applied++
		}
		out = append(out, Result{Probe: p, Applied: ok, Detail: v, Informational: p.Informational})
	}
	if log != nil {
		log.Info("анализ: %d проб, %d применено", len(out), applied)
	}
	return out
}
