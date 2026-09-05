package tui

import (
	"charm.land/bubbles/v2/viewport"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/ui"
)

// runModel is a phaseRun model with some streamed output, sized for a real frame.
func runModel(w, h int) model {
	m := newModel()
	m.w, m.h = w, h
	m.host = "1.2.3.4"
	m.phase = phaseRun
	m.command = "step"
	m.content = strings.Repeat("apt-get install …\n", 80)
	m.vp = viewport.New(viewport.WithWidth(m.vpWidth()), viewport.WithHeight(m.vpHeight()))
	m.vp.SetContent(m.wrapped())
	m.vp.GotoBottom()
	return m
}

// backPillRow finds the screen row the "Back to main" pill is actually DRAWN on.
func backPillRow(t *testing.T, m model) int {
	t.Helper()
	want := t2(m.lang, kBackToMain)
	for i, ln := range strings.Split(m.runView(), "\n") {
		if strings.Contains(ui.StripControlAndANSI(ln), want) {
			return i
		}
	}
	t.Fatalf("Back-to-main pill not drawn at all")
	return -1
}

// TestRunBackButtonRowMatchesEverySubState is the regression for "Enter/mouse stop
// working at some stage of applying tweaks": the run viewport's height was budgeted
// once, in the doneMsg handler, BEFORE finalErr was stored and BEFORE the summary
// (bench line + one row per skip reason) landed on its own channel. Both grow the
// finished tail, so the pill was drawn a few rows below where backToMainAtClick
// looked for it — the button stopped responding to the mouse. Every finished
// sub-state must line the two up.
func TestRunBackButtonRowMatchesEverySubState(t *testing.T) {
	cases := []struct {
		name  string
		drive func(t *testing.T, m model) model
	}{
		{"error, no summary", func(t *testing.T, m model) model {
			mm, _ := m.Update(genMsg{m.runGen, doneMsg{errors.New("A4: sysctl -p failed")}})
			return asModel(t, mm)
		}},
		{"ok, summary with skips", func(t *testing.T, m model) model {
			sum := engine.Summary{
				OK: 3,
				Skips: []engine.SkipReason{
					{ID: "A1", Reason: "ufw manages the firewall"},
					{ID: "A6.7", Reason: "disk swap preserved"},
				},
				BenchOK: true, BenchPreMBs: 11.5, BenchPostMBs: 24.25, BenchRatio: 2.1,
			}
			mm, _ := m.Update(genMsg{m.runGen, doneMsg{nil}})
			mm, _ = asModel(t, mm).Update(genMsg{m.runGen, progMsg(engine.Progress{Done: true, Summary: sum})})
			return asModel(t, mm)
		}},
		{"summary before done", func(t *testing.T, m model) model {
			sum := engine.Summary{OK: 1, Skips: []engine.SkipReason{{ID: "A3", Reason: "already configured"}}}
			mm, _ := m.Update(genMsg{m.runGen, progMsg(engine.Progress{Done: true, Summary: sum})})
			mm, _ = asModel(t, mm).Update(genMsg{m.runGen, doneMsg{nil}})
			return asModel(t, mm)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := c.drive(t, runModel(100, 40))
			m.phase = phaseRun // an auto-advance would leave the run screen; we test its geometry
			// The frame must still FIT: an over-long frame scrolls the alt-screen, so every
			// clickable row on it is displaced from the row the hit-test computes.
			if rows := strings.Count(m.runView(), "\n") + 1; rows > m.h {
				t.Fatalf("run frame is %d rows, terminal is %d — the screen scrolls and clicks land off-target", rows, m.h)
			}
			drawn := backPillRow(t, m)
			if got := m.backToMainRow(); got != drawn {
				t.Fatalf("backToMainRow() = %d, pill drawn on row %d", got, drawn)
			}
			if !m.backToMainAtClick(2, drawn) {
				t.Fatalf("click on the drawn pill row %d missed the button", drawn)
			}
			// And the click does what Enter does.
			next, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 2, Y: drawn})
			if p := asModel(t, next).phase; p == phaseRun {
				t.Fatalf("click on Back-to-main left the run screen at phase %v", p)
			}
		})
	}
}
