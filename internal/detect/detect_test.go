package detect

import (
	"reflect"
	"testing"
)

func TestPortFromLocal(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"0.0.0.0:8080", 8080, true},
		{"[::]:8080", 8080, true},
		{"192.168.0.1:53", 53, true},
		{"*:80", 80, true},
		{"[fe80::1]:51820", 51820, true},
		{"0.0.0.0:*", 0, false}, // listening wildcard port (no numeric port)
		{"nocolon", 0, false},
		{"1.2.3.4:", 0, false},
		{"1.2.3.4:0", 0, false},     // port 0 is not a usable service port
		{"1.2.3.4:70000", 0, false}, // out of range
	}
	for _, c := range cases {
		got, ok := portFromLocal(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("portFromLocal(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSortedKeys(t *testing.T) {
	if got := sortedKeys(nil); got != nil {
		t.Errorf("sortedKeys(nil) = %v, want nil", got)
	}
	got := sortedKeys(map[int]bool{8443: true, 22: true, 9099: true})
	want := []int{22, 8443, 9099}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sortedKeys = %v, want %v", got, want)
	}
}

// Representative `ss -tulpnH` block modeled on the live brownfield test box:
// docker-proxy 8080 on v4+v6, ncat 9099/8443/9092(kafka)/5678(n8n), wireguard udp
// 51820 on v4+v6, systemd-resolved stub on loopback (must be skipped), and the
// box's own sshd. The arbitrary kafka/n8n ports exercise the role-agnostic path.
const liveSS = `tcp   LISTEN 0      4096   0.0.0.0:8080       0.0.0.0:*    users:(("docker-proxy",pid=900,fd=4))
tcp   LISTEN 0      4096      [::]:8080          [::]:*    users:(("docker-proxy",pid=905,fd=4))
tcp   LISTEN 0      10      0.0.0.0:9099       0.0.0.0:*    users:(("ncat",pid=1001,fd=3))
tcp   LISTEN 0      10      0.0.0.0:8443       0.0.0.0:*    users:(("ncat",pid=1002,fd=3))
tcp   LISTEN 0      10      0.0.0.0:9092       0.0.0.0:*    users:(("ncat",pid=1003,fd=3))
tcp   LISTEN 0      10      0.0.0.0:5678       0.0.0.0:*    users:(("ncat",pid=1004,fd=3))
udp   UNCONN 0      0       0.0.0.0:51820      0.0.0.0:*    users:(("wg",pid=0))
udp   UNCONN 0      0          [::]:51820         [::]:*    users:(("wg",pid=0))
udp   UNCONN 0      0    127.0.0.53%lo:53          0.0.0.0:*    users:(("systemd-resolve",pid=700,fd=12))
tcp   LISTEN 0      128     0.0.0.0:22         0.0.0.0:*    users:(("sshd",pid=800,fd=3))
tcp   LISTEN 0      128        [::]:22            [::]:*    users:(("sshd",pid=801,fd=4))`

func TestParseListeners(t *testing.T) {
	tcp, udp, _, listeners := parseListeners(liveSS)

	wantTCP := []int{22, 5678, 8080, 8443, 9092, 9099}
	if !reflect.DeepEqual(tcp, wantTCP) {
		t.Errorf("tcp ports = %v, want %v", tcp, wantTCP)
	}
	wantUDP := []int{51820}
	if !reflect.DeepEqual(udp, wantUDP) {
		t.Errorf("udp ports = %v, want %v", udp, wantUDP)
	}
	// The loopback resolved stub (127.0.0.53) and both sshd lines must NOT appear in
	// the foreign-service Listeners signal (4x ncat + 2x docker-proxy + 2x wg = 8).
	if len(listeners) != 8 {
		t.Errorf("listeners count = %d, want 8 (2x docker-proxy, 4x ncat, 2x wg); got %v", len(listeners), listeners)
	}
	for _, l := range listeners {
		if got := contains(l, "sshd"); got {
			t.Errorf("listeners must not include sshd line: %q", l)
		}
		if got := contains(l, "127.0.0.53"); got {
			t.Errorf("listeners must not include loopback stub: %q", l)
		}
	}
}

// TestParseListenServices asserts the role-agnostic (proto,port,process) records
// are deduped across the v4/v6 mirror and carry the owning process name, for
// arbitrary services (kafka 9092, n8n 5678) — proving no service whitelist.
func TestParseListenServices(t *testing.T) {
	_, _, services, _ := parseListeners(liveSS)

	want := []ListenService{
		{Proto: "tcp", Port: 22, Process: "sshd", PID: 800},
		{Proto: "tcp", Port: 5678, Process: "ncat", PID: 1004},
		{Proto: "tcp", Port: 8080, Process: "docker-proxy", PID: 900},
		{Proto: "tcp", Port: 8443, Process: "ncat", PID: 1002},
		{Proto: "tcp", Port: 9092, Process: "ncat", PID: 1003},
		{Proto: "tcp", Port: 9099, Process: "ncat", PID: 1001},
		{Proto: "udp", Port: 51820, Process: "wg", PID: 0},
	}
	if !reflect.DeepEqual(services, want) {
		t.Errorf("ListenServices =\n%v\nwant\n%v", services, want)
	}
}

// TestPIDFromSS asserts the owning pid is parsed from the ss process column, and 0
// when there is no attribution.
func TestPIDFromSS(t *testing.T) {
	cases := map[string]int{
		`tcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=1234,fd=3))`:     1234,
		`tcp LISTEN 0 10 0.0.0.0:9092 0.0.0.0:* users:(("ncat",pid=42,fd=3))`:      42,
		`udp UNCONN 0 0 0.0.0.0:51820 0.0.0.0:*`:                                   0, // no process column
		`udp UNCONN 0 0 0.0.0.0:51820 0.0.0.0:* users:(("wg",pid=0))`:              0,
		`tcp LISTEN 0 4096 [::]:8080 [::]:* users:(("docker-proxy",pid=987,fd=4))`: 987,
	}
	for line, want := range cases {
		if got := pidFromSS(line); got != want {
			t.Errorf("pidFromSS(%q) = %d, want %d", line, got, want)
		}
	}
}

func TestProcessFromSS(t *testing.T) {
	cases := map[string]string{
		`tcp LISTEN 0 10 0.0.0.0:9092 0.0.0.0:* users:(("ncat",pid=1,fd=3))`:           "ncat",
		`tcp LISTEN 0 4096 0.0.0.0:8080 0.0.0.0:* users:(("docker-proxy",pid=9,fd=4))`: "docker-proxy",
		`udp UNCONN 0 0 0.0.0.0:51820 0.0.0.0:*`:                                       "", // no process column
	}
	for line, want := range cases {
		if got := processFromSS(line); got != want {
			t.Errorf("processFromSS(%q) = %q, want %q", line, got, want)
		}
	}
}

// TestClassifyFirewallMgr table-tests the manager classification, including the
// conservative nftables guard (a docker/iptables box must NOT classify as
// nftables even when the nftables unit is reported active).
func TestClassifyFirewallMgr(t *testing.T) {
	const defaultIpt = "-P INPUT ACCEPT\n-P FORWARD ACCEPT\n-P OUTPUT ACCEPT"
	const dockerIpt = "-P INPUT ACCEPT\n-P FORWARD DROP\n-N DOCKER\n-A FORWARD -j DOCKER-USER"

	cases := []struct {
		name       string
		ufw        string
		firewalld  bool
		nftables   bool
		nftRuleset string
		iptablesS  string
		iptPersist bool
		want       string
	}{
		{"ufw active wins", "Status: active", false, false, "", defaultIpt, false, "ufw"},
		{"ufw inactive ignored", "Status: inactive", false, false, "", defaultIpt, false, "none"},
		{"firewalld active", "", true, false, "", defaultIpt, false, "firewalld"},
		{"nftables native", "", false, true, "table inet filter { }", defaultIpt, false, "nftables"},
		{"nftables but iptables in use -> iptables", "", false, true, "table ip filter {}", dockerIpt, false, "iptables"},
		{"nftables active but empty ruleset -> none", "", false, true, "", defaultIpt, false, "none"},
		{"docker/iptables box", "", false, false, "", dockerIpt, false, "iptables"},
		{"iptables-persistent only", "", false, false, "", defaultIpt, true, "iptables"},
		{"clean box", "", false, false, "", defaultIpt, false, "none"},
	}
	for _, c := range cases {
		got := classifyFirewallMgr(c.ufw, c.firewalld, c.nftables, c.nftRuleset, c.iptablesS, c.iptPersist)
		if got != c.want {
			t.Errorf("%s: classifyFirewallMgr = %q, want %q", c.name, got, c.want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestClassifyCgroupOrigin table-tests the provenance classifier over real cgroup v1
// AND v2 first-line strings: host/docker/k8s/podman/lxc/systemd-unit, with the
// generic container-manager units normalized to "docker".
func TestClassifyCgroupOrigin(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		// cgroup v2 (single "0::/path").
		{"v2 root host", "0::/", "host"},
		{"v2 init.scope host", "0::/init.scope", "host"},
		{"v2 user slice host", "0::/user.slice/user-1000.slice/session-3.scope", "host"},
		{"v2 docker", "0::/system.slice/docker-3f9a1b2c.scope", "docker"},
		{"v2 moby", "0::/system.slice/docker.service/moby/abcdef0123", "docker"},
		{"v2 kubepods", "0::/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc.slice/cri-containerd-deadbeef.scope", "k8s"},
		{"v2 podman libpod", "0::/machine.slice/libpod-1122334455.scope/container", "podman"},
		{"v2 lxc payload", "0::/lxc.payload.mycontainer/system.slice/foo.service", "lxc"},
		{"v2 lxc path", "0::/lxc/ct1", "lxc"},
		{"v2 systemd unit", "0::/system.slice/postgresql.service", "systemd: postgresql"},
		{"v2 systemd nested unit", "0::/system.slice/system-postgresql.slice/postgresql@16-main.service", "systemd: postgresql@16-main"},
		{"v2 generic docker unit → docker", "0::/system.slice/docker.service", "docker"},
		{"v2 generic containerd unit → docker", "0::/system.slice/containerd.service", "docker"},

		// cgroup v1 ("N:controller:/path") — only the path part matters.
		{"v1 docker", "11:cpuset:/docker/3f9a1b2c3d4e", "docker"},
		{"v1 kubepods", "8:memory:/kubepods/burstable/podXYZ/abcd", "k8s"},
		{"v1 systemd unit", "1:name=systemd:/system.slice/nginx.service", "systemd: nginx"},
		{"v1 host root", "1:name=systemd:/", "host"},

		// degenerate.
		{"empty → host", "", "host"},
		{"whitespace → host", "   ", "host"},
		{"unrecognized → host", "0::/weird/path/segment", "host"},
	}
	for _, c := range cases {
		if got := classifyCgroupOrigin(c.in); got != c.want {
			t.Errorf("%s: classifyCgroupOrigin(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestDockerPortNames parses sample `docker ps` Ports columns: single, multi, IPv6
// (:::/[::]:), and host port ranges; bare exposed ports (no "->") are ignored.
func TestDockerPortNames(t *testing.T) {
	psOut := "amnezia-xray\t0.0.0.0:443->443/tcp, :::443->443/tcp\n" +
		"web\t0.0.0.0:80->80/tcp, [::]:80->80/tcp, 0.0.0.0:8000-8001->8000-8001/tcp\n" +
		"exposed-only\t9000/tcp\n" +
		"db\t127.0.0.1:5432->5432/tcp\n"
	got := dockerPortNames(psOut)

	want := map[int]string{
		443:  "amnezia-xray",
		80:   "web",
		8000: "web",
		8001: "web",
		5432: "db",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dockerPortNames =\n%v\nwant\n%v", got, want)
	}
	// The exposed-only container must contribute NO port.
	if _, ok := got[9000]; ok {
		t.Errorf("exposed-only port 9000 must not be published")
	}
}

// TestParseCgroupBatch parses the "<pid>\t<cgroup line>" batch output into a map,
// tolerating an empty cgroup column (unreadable /proc) and skipping malformed lines.
func TestParseCgroupBatch(t *testing.T) {
	out := "900\t0::/system.slice/docker-abc.scope\n" +
		"1001\t0::/system.slice/postgresql.service\n" +
		"700\t\n" + // unreadable /proc → empty cgroup
		"garbage-no-tab\n" +
		"notapid\t0::/\n"
	got := parseCgroupBatch(out)
	want := map[int]string{
		900:  "0::/system.slice/docker-abc.scope",
		1001: "0::/system.slice/postgresql.service",
		700:  "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseCgroupBatch =\n%v\nwant\n%v", got, want)
	}
}
