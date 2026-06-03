package tui

import (
	"strings"
	"testing"

	"github.com/UberMorgott/morgward/internal/detect"
)

// TestDashServerCardShowsServices asserts that when the audit surfaced detected
// services (FEATURE A), the server card renders the Services header + a service line,
// formatted "proto/port process", on a wide-enough terminal.
func TestDashServerCardShowsServices(t *testing.T) {
	m := dashModel(120, 40)
	m.dashFacts.ListenServices = []detect.ListenService{
		{Proto: "tcp", Port: 443, Process: "docker-proxy"},
		{Proto: "tcp", Port: 22, Process: "sshd"},
		{Proto: "udp", Port: 51820, Process: ""}, // no process → "udp/51820"
	}
	innerW := innerWidth(m.boxWidth())
	finner := max(innerW, minBoxWidth) - 2
	if !dashCardTwoCol(finner) {
		t.Fatalf("test precondition: card inner %d must allow two columns", finner)
	}

	card := strings.Join(m.dashServerCard(innerW), "\n")
	for _, want := range []string{
		t2(m.lang, kDashServicesTitle), // services header
		"tcp/22 sshd",                  // sorted-first service with process
		"tcp/443 docker-proxy",
		"udp/51820", // no-process service shows just proto/port
	} {
		if !strings.Contains(card, want) {
			t.Fatalf("server card missing %q\n%s", want, card)
		}
	}
}

// TestDashServiceLinesShowOrigin asserts each service line carries its universal
// origin tag as "proto/port process [origin]" (host/docker:name/k8s/systemd:unit).
func TestDashServiceLinesShowOrigin(t *testing.T) {
	m := dashModel(140, 40)
	m.dashFacts.ListenServices = []detect.ListenService{
		{Proto: "tcp", Port: 22, Process: "sshd", Origin: "host"},
		{Proto: "tcp", Port: 443, Process: "docker-proxy", Origin: "docker: amnezia-xray"},
		{Proto: "tcp", Port: 6443, Process: "kube-apiserver", Origin: "k8s"},
		{Proto: "tcp", Port: 5432, Process: "postgres", Origin: "systemd: postgresql"},
	}
	joined := strings.Join(m.dashServiceLines(), "\n")
	for _, want := range []string{
		"tcp/22 sshd [host]",
		"tcp/443 docker-proxy [docker: amnezia-xray]",
		"tcp/6443 kube-apiserver [k8s]",
		"tcp/5432 postgres [systemd: postgresql]",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("service line missing %q\n%s", want, joined)
		}
	}
	// A service with an empty Origin must NOT render an empty "[]" tag.
	m.dashFacts.ListenServices = []detect.ListenService{{Proto: "tcp", Port: 22, Process: "sshd", Origin: ""}}
	if strings.Contains(strings.Join(m.dashServiceLines(), "\n"), "[]") {
		t.Fatalf("empty origin rendered an empty [] tag")
	}
}

// TestDashServiceLinesSortedAndCapped asserts services are sorted by port and capped
// with an overflow "… +N more" marker when there are more than dashServicesCap.
func TestDashServiceLinesSortedAndCapped(t *testing.T) {
	m := dashModel(120, 40)
	// Build dashServicesCap+5 services in DESCENDING port order; expect ascending out.
	n := dashServicesCap + 5
	svcs := make([]detect.ListenService, 0, n)
	for i := 0; i < n; i++ {
		svcs = append(svcs, detect.ListenService{Proto: "tcp", Port: 9000 - i})
	}
	m.dashFacts.ListenServices = svcs

	lines := m.dashServiceLines()
	// header + (cap-1 services) + 1 overflow marker = cap+1 lines.
	if got := len(lines); got != dashServicesCap+1 {
		t.Fatalf("capped service lines = %d, want %d (header + cap-1 + overflow)", got, dashServicesCap+1)
	}
	// First service line (index 1) must be the LOWEST port (ascending sort).
	if !strings.Contains(lines[1], "tcp/"+itoaForTest(9000-(n-1))) {
		t.Fatalf("services not sorted ascending by port; first=%q", lines[1])
	}
	// Last line is the overflow marker.
	if !strings.Contains(lines[len(lines)-1], "+") {
		t.Fatalf("expected an overflow marker on the last line; got %q", lines[len(lines)-1])
	}
}

// TestDashServerCardFallbackNoServices asserts the card falls back to the single-
// column facts layout (no Services header) when ListenServices is empty, and that the
// card height equals the number of facts + 2 border rows (so the fixed-prefix hit-test
// math is unaffected).
func TestDashServerCardFallbackNoServices(t *testing.T) {
	m := dashModel(120, 40)
	m.dashFacts.ListenServices = nil

	card := m.dashServerCard(innerWidth(m.boxWidth()))
	joined := strings.Join(card, "\n")
	if strings.Contains(joined, t2(m.lang, kDashServicesTitle)) {
		t.Fatalf("empty-services card must not render the Services header\n%s", joined)
	}
	// ubuntu 24.04 + IPv6 facts → OS + IPv6 rows (2 facts) + top + bottom = 4 lines.
	if got := len(card); got != 4 {
		t.Fatalf("fallback card height = %d, want 4 (top + 2 facts + bottom)", got)
	}
}

// TestDashServicesNarrowFallback asserts a narrow card stays single-column (services
// omitted) even when ListenServices is set, since two columns won't fit.
func TestDashServicesNarrowFallback(t *testing.T) {
	m := dashModel(44, 40)
	m.dashFacts.ListenServices = []detect.ListenService{{Proto: "tcp", Port: 22, Process: "sshd"}}
	innerW := innerWidth(m.boxWidth())
	finner := max(innerW, minBoxWidth) - 2
	if dashCardTwoCol(finner) {
		t.Fatalf("test precondition: card inner %d should be too narrow for two columns", finner)
	}
	card := strings.Join(m.dashServerCard(innerW), "\n")
	if strings.Contains(card, t2(m.lang, kDashServicesTitle)) {
		t.Fatalf("narrow card must omit the Services column\n%s", card)
	}
}

// TestDashButtonsStillResolveWithServices is the geometry regression guard for
// FEATURE A: after adding the services column the fixed-prefix length still drives the
// buttons row + audit grid, so the Apply/Security button hit-tests must still land.
func TestDashButtonsStillResolveWithServices(t *testing.T) {
	m := dashModel(120, 40)
	m.dashFacts.ListenServices = []detect.ListenService{
		{Proto: "tcp", Port: 22, Process: "sshd"},
		{Proto: "tcp", Port: 443, Process: "nginx"},
	}
	innerW := innerWidth(m.boxWidth())
	btnRow := m.dashButtonsRowY(innerW)
	ranges := pillRanges(m.dashButtonNames(), dashButtonStartCol)
	want := []dashButton{dashBtnApply, dashBtnSecurity}
	for i, r := range ranges {
		x := r[0] + 1
		if got := m.dashButtonAtClick(x, btnRow); got != want[i] {
			t.Fatalf("button %d click at x=%d row=%d → %v, want %v", i, x, btnRow, got, want[i])
		}
	}
	// The audit grid's first row must still resolve to the first result.
	gridTop := m.dashScrollTopRow(innerW)
	if r, ok := m.dashAuditRowAtClick(4, gridTop); !ok || r.Probe.ID != "A4-bbr" {
		t.Fatalf("audit row click drifted after adding services: %q,%v", r.Probe.ID, ok)
	}
}

// TestDashSpacerBetweenButtonsAndStatus asserts the blank spacer between the buttons
// row and the audit status line is present in dashFixedLines AND that the buttons row
// + audit grid hit-tests still land after the spacer shifts the scroll region down.
func TestDashSpacerBetweenButtonsAndStatus(t *testing.T) {
	m := dashModel(100, 40)
	innerW := innerWidth(m.boxWidth())
	fixed := m.dashFixedLines(innerW)

	// Order: card (N) , buttonsLine , "" , statusLine. The buttons row is at len(card);
	// the next line must be the blank spacer; the status line follows it.
	cardLen := len(m.dashServerCard(innerW))
	if cardLen+2 >= len(fixed) {
		t.Fatalf("dashFixedLines too short for spacer: cardLen=%d fixed=%d", cardLen, len(fixed))
	}
	if strings.TrimSpace(fixed[cardLen+1]) != "" {
		t.Fatalf("expected a blank spacer at index %d, got %q", cardLen+1, fixed[cardLen+1])
	}
	if strings.TrimSpace(fixed[cardLen+2]) == "" {
		t.Fatalf("expected the status line at index %d, got blank", cardLen+2)
	}

	// Buttons row Y is unchanged (depends only on len(card)); both pills still resolve.
	btnRow := m.dashButtonsRowY(innerW)
	if btnRow != summaryBodyTopRow+cardLen {
		t.Fatalf("dashButtonsRowY=%d, want %d (spacer must not move the buttons row)", btnRow, summaryBodyTopRow+cardLen)
	}
	ranges := pillRanges(m.dashButtonNames(), dashButtonStartCol)
	want := []dashButton{dashBtnApply, dashBtnSecurity}
	for i, r := range ranges {
		if got := m.dashButtonAtClick(r[0]+1, btnRow); got != want[i] {
			t.Fatalf("after spacer, button %d → %v, want %v", i, got, want[i])
		}
	}
	// The audit grid's first row still resolves (scroll region shifted down by 1).
	gridTop := m.dashScrollTopRow(innerW)
	if r, ok := m.dashAuditRowAtClick(4, gridTop); !ok || r.Probe.ID != "A4-bbr" {
		t.Fatalf("audit grid hit-test drifted after spacer: %q,%v", r.Probe.ID, ok)
	}
}

// itoaForTest is a tiny strconv.Itoa shim kept local so the test file needs no extra
// import beyond detect.
func itoaForTest(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
