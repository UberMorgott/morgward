package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/tweaks"
	"github.com/UberMorgott/morgward/internal/wiki"
)

// mouseClickAt builds a left-button MouseClickMsg at (x,y) for hit-test dispatch tests.
func mouseClickAt(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// TestFormConnectable asserts the Enter-to-connect predicate: a host plus at least
// one credential (password OR key) makes the form connectable; missing either does not.
func TestFormConnectable(t *testing.T) {
	cases := []struct {
		name, host, pass, key string
		want                  bool
	}{
		{"empty", "", "", "", false},
		{"host only", "1.2.3.4", "", "", false},
		{"host+pass", "1.2.3.4", "secret", "", true},
		{"host+key", "1.2.3.4", "", "/k/id_ed25519", true},
		{"pass only, no host", "", "secret", "", false},
		{"whitespace host", "   ", "secret", "", false},
		{"whitespace pass", "1.2.3.4", "   ", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := formModel(80, 24)
			m.inputs[fHost].SetValue(c.host)
			m.inputs[fPass].SetValue(c.pass)
			m.inputs[fKey].SetValue(c.key)
			if got := m.formConnectable(); got != c.want {
				t.Fatalf("formConnectable(host=%q pass=%q key=%q)=%v want %v",
					c.host, c.pass, c.key, got, c.want)
			}
		})
	}
}

// TestTweakDocResolvesViaStep asserts a tweak Probe whose Step is a real wiki key
// resolves to an existing wiki.Doc (not the empty fallback), and that the page
// header names the specific tweak via tweakWikiHeader.
func TestTweakDocResolvesViaStep(t *testing.T) {
	p := tweaks.Probe{ID: "a2.permitroot", Step: "A2", Name: "PermitRootLogin"}
	for _, lang := range []Lang{langRU, langEN} {
		if _, ok := wiki.Doc(wiki.Lang(int(lang)), p.Step); !ok {
			t.Fatalf("lang=%d: wiki.Doc(%q) missing — step key must resolve a real doc", lang, p.Step)
		}
		h := tweakWikiHeader(lang, p)
		if !strings.Contains(h, p.ID) {
			t.Fatalf("lang=%d: header %q does not name the tweak id %q", lang, h, p.ID)
		}
	}
}

// TestWikiBodyUsesTweakHeader asserts that when wikiTweak is set the body header is
// the tweak header AND the body is the real step doc (no kWikiNoDoc fallback), and
// that the summary path (wikiTweak empty, real step id) still renders the step title.
func TestWikiBodyUsesTweakHeader(t *testing.T) {
	innerW := innerWidth(80)

	// Tweak path: step "A2" doc with a specific tweak header.
	m := formModel(80, 24)
	m.wikiStep = "A2"
	m.wikiTweak = tweakWikiHeader(m.lang, tweaks.Probe{ID: "a2.permitroot", Step: "A2", Name: "PermitRootLogin"})
	body := m.wikiBodyLines(innerW)
	joined := strings.Join(body, "\n")
	if !strings.Contains(joined, "a2.permitroot") {
		t.Fatalf("tweak-path header missing tweak id; body=%q", joined)
	}
	if strings.Contains(joined, t2(m.lang, kWikiNoDoc)) {
		t.Fatalf("tweak-path body fell back to no-doc; body=%q", joined)
	}

	// Summary path: real step id, no tweak header → plain step doc renders.
	m2 := formModel(80, 24)
	m2.wikiStep = "A2"
	m2.wikiTweak = ""
	body2 := m2.wikiBodyLines(innerW)
	if strings.Contains(strings.Join(body2, "\n"), t2(m2.lang, kWikiNoDoc)) {
		t.Fatalf("summary-path body fell back to no-doc")
	}
}

// TestWikiBackButtonHitTest asserts the rendered "← Назад" pill hit-tests at its
// geometry and returns the model to wikiReturn on click.
func TestWikiBackButtonHitTest(t *testing.T) {
	m := formModel(100, 40)
	m.phase = phaseWiki
	m.wikiReturn = phaseDashboard
	m.wikiStep = "A2"

	row := m.wikiBackRow()
	// A click inside the pill (start col + 1) hits; far-right does not.
	if !m.wikiBackAtClick(wikiBackStartCol+1, row) {
		t.Fatalf("click at pill start+1 did not hit the back button")
	}
	if m.wikiBackAtClick(1, row) {
		t.Fatalf("click on the border col unexpectedly hit the back button")
	}
	if m.wikiBackAtClick(wikiBackStartCol+1, row+1) {
		t.Fatalf("click one row below the button unexpectedly hit it")
	}

	// Dispatching the click through Update returns to wikiReturn.
	next, _ := m.Update(mouseClickAt(wikiBackStartCol+1, row))
	mm := next.(model)
	if mm.phase != phaseDashboard {
		t.Fatalf("back-button click → phase %v, want phaseDashboard (wikiReturn)", mm.phase)
	}
}
