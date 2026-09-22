package findings

import (
	"errors"
	"testing"

	"github.com/UberMorgott/morgward/internal/sshx"
)

func TestIsWildcard(t *testing.T) {
	cases := map[string]bool{
		"0.0.0.0":         true,
		"*":               true,
		"[::]":            true,
		"::":              true,
		"0:0:0:0:0:0:0:0": true,
		"[0:0::0]":        true,
		"127.0.0.1":       false,
		"[::1]":           false,
		"10.0.0.5":        false,
		"[2001:db8::1]":   false,
		"0.0.0.0%eth0":    false,
		"[::]%lo":         false,
		"":                false,
	}
	for host, want := range cases {
		if got := isWildcard(host); got != want {
			t.Errorf("isWildcard(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestDBWildcard(t *testing.T) {
	ls := []string{
		`tcp   LISTEN 0 151  0.0.0.0:3306   0.0.0.0:* users:(("mysqld",pid=10,fd=20))`,
		`tcp   LISTEN 0 151  [::]:3306      [::]:*    users:(("mysqld",pid=10,fd=21))`,
		`tcp   LISTEN 0 244  127.0.0.1:5432 0.0.0.0:* users:(("postgres",pid=11,fd=5))`,
		`tcp   LISTEN 0 511  10.0.0.5:6379  0.0.0.0:* users:(("redis",pid=12,fd=6))`,
		`tcp   LISTEN 0 4096 *:27017        *:*       users:(("mongod",pid=13,fd=7))`,
		`tcp   LISTEN 0 4096 :::9200        :::*      users:(("java",pid=14,fd=8))`,
		`udp   UNCONN 0 0    0.0.0.0:11211  0.0.0.0:* users:(("memcached",pid=15,fd=9))`,
		`tcp   LISTEN 0 4096 0.0.0.0:8080   0.0.0.0:* users:(("nginx",pid=16,fd=3))`,
		`udp   UNCONN 0 0    0.0.0.0:3306   0.0.0.0:*`,
		`garbage`,
	}
	got := DBWildcard(ls)
	want := []string{"tcp/3306 MySQL", "tcp/27017 MongoDB", "tcp/9200 Elasticsearch", "udp/11211 Memcached"}
	if len(got) != len(want) {
		t.Fatalf("got %d findings %+v, want %v", len(got), got, want)
	}
	for i, f := range got {
		if f.Subject != want[i] || f.ID != "db.wildcard_bind" || f.Severity != SevWarn {
			t.Errorf("finding %d = %+v, want subject %q", i, f, want[i])
		}
	}
	if got[0].Detail != "listens on all interfaces (0.0.0.0:3306, [::]:3306)" {
		t.Errorf("mirror addresses not merged: %q", got[0].Detail)
	}
}

func TestParse(t *testing.T) {
	out := "F\tuid0\ttoor\n" +
		"R\tuid0\t0\n" +
		"F\tempty_password\tguest\n" +
		"R\tempty_password\t0\n" +
		"F\tnopasswd\t/etc/sudoers.d/90-vpsadmin\tvpsadmin ALL=(ALL) NOPASSWD:ALL\n" +
		"F\tnopasswd\t/etc/sudoers.d/90-vpsadmin\tbob ALL=(ALL) NOPASSWD:ALL\n" +
		"F\tnopasswd\t/etc/sudoers.d/90-cloud-init-users\tubuntu ALL=(ALL) NOPASSWD:ALL\n" +
		"R\tnopasswd\t0\n" +
		"F\tdocker\talice\nF\tdocker\talice\n" +
		"R\tdocker\t2\n" +
		"F\treboot\t3\n"
	items, errs := Parse(sshx.Result{Stdout: out}, "vpsadmin")

	ids := map[string]int{}
	for _, f := range items {
		ids[f.ID]++
	}
	want := map[string]int{"acct.uid0": 1, "acct.empty_password": 1, "sudo.nopasswd": 2, "group.docker": 1, "os.reboot_required": 1}
	for id, n := range want {
		if ids[id] != n {
			t.Errorf("%s: got %d items, want %d (%+v)", id, ids[id], n, items)
		}
	}
	for _, f := range items {
		if f.ID == "os.reboot_required" && (f.Severity != SevInfo || f.Detail != "reboot required by 3 package(s)") {
			t.Errorf("reboot finding = %+v", f)
		}
	}
	// docker rc=2 and reboot without an R line are check errors, not silence.
	if len(errs) != 2 || errs[0].Check != checkDocker || errs[0].Err != "exit status 2" ||
		errs[1].Check != checkReboot || errs[1].Err != "check did not complete" {
		t.Errorf("errs = %+v", errs)
	}
}

func TestParseClean(t *testing.T) {
	out := "R\tuid0\t0\nR\tempty_password\t0\nR\tnopasswd\t0\nR\tdocker\t0\nR\treboot\t0\n"
	items, errs := Parse(sshx.Result{Stdout: out}, "vpsadmin")
	if len(items) != 0 || len(errs) != 0 {
		t.Errorf("clean box: items=%+v errs=%+v", items, errs)
	}
}

func TestParseNoOutputIsError(t *testing.T) {
	for name, res := range map[string]sshx.Result{
		"empty":     {RC: 1, Stderr: "sudo: a password is required\n"},
		"transport": {RC: -1, Err: errors.New("EOF")},
	} {
		items, errs := Parse(res, "vpsadmin")
		if len(items) != 0 || len(errs) != len(scriptChecks) {
			t.Errorf("%s: items=%+v errs=%+v", name, items, errs)
		}
	}
}

func TestParseSanitizes(t *testing.T) {
	out := "F\tuid0\tev\x1b[31mil\x07\nR\tuid0\t0\n"
	items, _ := Parse(sshx.Result{Stdout: out}, "")
	if len(items) != 1 || items[0].Subject != "evil" {
		t.Errorf("items = %+v", items)
	}
}

func TestIsOwnSudoersRule(t *testing.T) {
	const own = "/etc/sudoers.d/90-vpsadmin"
	cases := []struct {
		file, rule, admin string
		want              bool
	}{
		{own, "vpsadmin ALL=(ALL) NOPASSWD:ALL", "vpsadmin", true},
		{own, "vpsadmin   ALL=(ALL)  NOPASSWD:ALL", "vpsadmin", true},
		{own, "bob ALL=(ALL) NOPASSWD:ALL", "vpsadmin", false},
		{own, "vpsadmin ALL=(ALL) NOPASSWD:/bin/sh", "vpsadmin", false},
		{"/etc/sudoers.d/91-vpsadmin", "vpsadmin ALL=(ALL) NOPASSWD:ALL", "vpsadmin", false},
		{"/etc/sudoers", "vpsadmin ALL=(ALL) NOPASSWD:ALL", "vpsadmin", false},
		{own, "vpsadmin ALL=(ALL) NOPASSWD:ALL", "", false},
	}
	for _, c := range cases {
		if got := IsOwnSudoersRule(c.file, c.rule, c.admin); got != c.want {
			t.Errorf("IsOwnSudoersRule(%q, %q, %q) = %v, want %v", c.file, c.rule, c.admin, got, c.want)
		}
	}
}
