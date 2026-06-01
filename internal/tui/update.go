package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/monitor"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// The resize poll (resizeTickMsg) delivers this every ~0.5s even when the
		// size is unchanged; rebuilding the viewport each time would needlessly
		// reset the scroll position. Only react when the size actually changed.
		if msg.Width == m.w && msg.Height == m.h {
			return m, nil
		}
		m.w, m.h = msg.Width, msg.Height
		m.vp = viewport.New(viewport.WithWidth(m.vpWidth()), viewport.WithHeight(m.vpHeight()))
		m.vp.SetContent(m.wrapped())
		m.vp.GotoBottom()
		return m, nil

	case tea.MouseClickMsg:
		// Manual rectangle hit-test (lipgloss/v2 has no hit-test API). Handle press
		// (MouseClickMsg), not release; left button only. All zone fns share geometry
		// with the render path (formRows/pillRanges/runView line order) so they cannot
		// drift. The RU/EN switcher is handled first in both phases.
		mc := msg.Mouse()
		if mc.Button != tea.MouseLeft {
			return m, nil
		}
		if lang, ok := m.langAtClick(mc.X, mc.Y); ok {
			if lang != m.lang {
				m.toggleLang()
			}
			return m, nil
		}
		switch m.phase {
		case phaseForm:
			return m.formClick(mc.X, mc.Y)
		case phaseDashboard:
			return m.dashboardClick(mc.X, mc.Y)
		case phaseSecurity:
			return m.securityClick(mc.X, mc.Y)
		case phaseWiki:
			// On the per-PROBE detail, the [Применить] / [Откатить] / [Обновить и
			// перезагрузить] action pills take priority over the back pill (their rows
			// never overlap). [Применить] applies THIS probe's step; [Откатить] reverts it;
			// [Обновить] runs A8 (upgrade+reboot) behind a reboot confirm.
			//
			// [Обновить] is a two-step confirm (A8 reboots): the FIRST click arms the
			// confirm (hint switches to the prompt); the launch happens only on Enter in
			// the key handler. While the confirm is armed, every OTHER click cancels it and
			// is otherwise harmless (no apply/revert/back fires on the arming interaction).
			if m.wikiUpdateAtClick(mc.X, mc.Y) {
				m.wikiUpdateConfirm = true
				return m, nil
			}
			if m.wikiUpdateConfirm {
				// A pending reboot confirm swallows any other click (cancel it; resolve via
				// Enter/Esc on the hint). This keeps apply/revert/back harmless while armed.
				m.wikiUpdateConfirm = false
				return m, nil
			}
			if m.wikiApplyAtClick(mc.X, mc.Y) {
				if step, ok := m.wikiProbeStep(); ok {
					return m.startSteps([]string{step})
				}
				return m, nil
			}
			if m.wikiRevertAtClick(mc.X, mc.Y) {
				if step, ok := m.wikiProbeStep(); ok {
					return m.startRevert([]string{step})
				}
				return m, nil
			}
			// The clickable "← Назад" pill returns to wherever the wiki was opened from.
			if m.wikiBackAtClick(mc.X, mc.Y) {
				m.phase = m.wikiReturn
			}
			return m, nil
		case phaseSummary:
			// A click on a fix row opens its wiki description.
			if id, ok := m.fixAtClick(mc.X, mc.Y); ok {
				m.wikiStep = id
				m.wikiTweak = ""   // summary path: real step id, plain step header
				m.wikiProbeID = "" // summary path: step-level doc, no per-probe text
				m.wikiReturn = phaseSummary
				m.wikiScroll = 0            // fresh page starts at the top
				m.wikiUpdateConfirm = false // fresh page never carries a stale reboot confirm
				m.phase = phaseWiki
			}
			return m, nil
		case phaseRun:
			// Only the "Back to main" button is clickable (when finished); it opens
			// the summary the same way enter/esc does.
			if m.backToMainAtClick(mc.X, mc.Y) {
				if m.finished && m.haveSummary {
					m.phase = phaseSummary
					return m, nil
				}
				return m.goBack()
			}
		case phaseKey:
			// The "Copy key" button is the only click target on this screen.
			if m.keyCopyAtClick(mc.X, mc.Y) {
				m = m.copyKey()
			}
			return m, nil
		}
		return m, nil

	case tea.MouseWheelMsg:
		// Mouse-wheel scrolls the scrollable region of the current screen: the run
		// log viewport in phaseRun, the directly-rendered body in phaseSummary/phaseWiki
		// (the form has no scrollable region). v2 delivers wheel events as MouseWheelMsg
		// with a MouseWheelUp/Down button (mouse.go), distinct from MouseClickMsg.
		const wheelStep = 3
		up := msg.Mouse().Button == tea.MouseWheelUp
		down := msg.Mouse().Button == tea.MouseWheelDown
		switch m.phase {
		case phaseRun:
			if up {
				m.vp.ScrollUp(wheelStep)
			} else if down {
				m.vp.ScrollDown(wheelStep)
			}
		case phaseSummary:
			d := 0
			if up {
				d = -wheelStep
			} else if down {
				d = wheelStep
			}
			m.sumScroll = clampScroll(m.sumScroll+d, len(m.summaryBodyLines()), m.bodyViewH())
		case phaseWiki:
			d := 0
			if up {
				d = -wheelStep
			} else if down {
				d = wheelStep
			}
			m.wikiScroll = clampScroll(m.wikiScroll+d, len(m.wikiBodyLines(innerWidth(m.boxWidth()))), m.wikiBodyViewH())
		case phaseMatrix:
			d := 0
			if up {
				d = -wheelStep
			} else if down {
				d = wheelStep
			}
			m.matScroll = clampScroll(m.matScroll+d, len(m.matrixBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
		case phaseDashboard:
			d := 0
			if up {
				d = -wheelStep
			} else if down {
				d = wheelStep
			}
			iw := innerWidth(m.boxWidth())
			m.dashScroll = clampScroll(m.dashScroll+d, len(m.dashBodyLines(iw)), m.dashScrollViewH(iw))
		}
		return m, nil

	case tea.KeyPressMsg:
		// Language hotkey works in BOTH phases (form + run). Use 'l' to cycle ru<->en;
		// ctrl+l also toggles. In the form phase 'l' is only intercepted when focus is
		// NOT on a text input, so typing 'l' into a field still works.
		if msg.String() == "ctrl+l" ||
			(msg.String() == "l" && !(m.phase == phaseForm && m.focus < nInputs)) {
			m.toggleLang()
			return m, nil
		}
		if m.phase == phaseForm {
			return m.updateForm(msg)
		}
		// ctrl+c / q quit on every post-form screen.
		if s := msg.String(); s == "ctrl+c" || s == "q" {
			m.stopSampler()
			return m, tea.Quit
		}
		switch m.phase {
		case phaseWiki:
			// A pending A8 reboot confirm is resolved here FIRST: Enter launches the
			// upgrade+reboot (A8), Esc/any other key cancels it (and does NOT navigate
			// back — the confirm owns esc while armed). This mirrors dashApplyConfirm.
			if m.wikiUpdateConfirm {
				switch msg.String() {
				case "enter":
					m.wikiUpdateConfirm = false
					return m.startSteps([]string{"A8"})
				default:
					// Esc / b / any other key cancels the confirm and stays on the page.
					m.wikiUpdateConfirm = false
				}
				return m, nil
			}
			// Any "back" key returns to wherever the wiki was opened from (summary);
			// ↑↓/k/j scroll the description when it overflows the middle region.
			switch msg.String() {
			case "enter", "esc", "b":
				m.phase = m.wikiReturn
			case "a":
				// Apply THIS probe's step — only when the [Применить] row is actually shown.
				if _, shown := m.wikiActionRowY(wikiRowApplyButton); shown {
					if step, ok := m.wikiProbeStep(); ok {
						return m.startSteps([]string{step})
					}
				}
			case "r":
				// Revert THIS probe's step — only when the [Откатить] row is actually shown.
				if _, shown := m.wikiActionRowY(wikiRowRevertButton); shown {
					if step, ok := m.wikiProbeStep(); ok {
						return m.startRevert([]string{step})
					}
				}
			case "u":
				// Update & reboot (A8) — arm the reboot confirm only when the [Обновить] row
				// is actually shown; the launch happens on the next Enter (see above).
				if _, shown := m.wikiActionRowY(wikiRowUpdateButton); shown {
					m.wikiUpdateConfirm = true
				}
			case "up", "k":
				m.wikiScroll = clampScroll(m.wikiScroll-1, len(m.wikiBodyLines(innerWidth(m.boxWidth()))), m.wikiBodyViewH())
			case "down", "j":
				m.wikiScroll = clampScroll(m.wikiScroll+1, len(m.wikiBodyLines(innerWidth(m.boxWidth()))), m.wikiBodyViewH())
			}
			return m, nil
		case phaseSummary:
			// On the summary, "back" returns to the form/menu (stops the sampler);
			// ↑↓/k/j scroll the stats + fix list when it overflows the middle region.
			switch msg.String() {
			case "enter", "esc", "b":
				return m.goBack()
			case "up", "k":
				m.sumScroll = clampScroll(m.sumScroll-1, len(m.summaryBodyLines()), m.bodyViewH())
			case "down", "j":
				m.sumScroll = clampScroll(m.sumScroll+1, len(m.summaryBodyLines()), m.bodyViewH())
			}
			return m, nil
		case phaseMatrix:
			// анализ audit table: "back" returns to the form/menu (stops the sampler);
			// ↑↓/k/j scroll the table when it overflows the middle region.
			switch msg.String() {
			case "enter", "esc", "b":
				return m.goBack()
			case "up", "k":
				m.matScroll = clampScroll(m.matScroll-1, len(m.matrixBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
			case "down", "j":
				m.matScroll = clampScroll(m.matScroll+1, len(m.matrixBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
			}
			return m, nil
		case phaseDashboard:
			// Dashboard: "back" returns to the form/menu (stops the sampler); ↑↓/k/j
			// scroll the audit list. enter confirms the pending A8-reboot apply, esc
			// cancels it; with no pending confirm, esc/b go back.
			switch msg.String() {
			case "enter":
				if m.dashApplyConfirm {
					m.dashApplyConfirm = false
					return m.launchApplyTweaks()
				}
				return m, nil
			case "esc", "b":
				if m.dashApplyConfirm {
					m.dashApplyConfirm = false
					return m, nil
				}
				return m.goBack()
			case "up", "k":
				iw := innerWidth(m.boxWidth())
				m.dashScroll = clampScroll(m.dashScroll-1, len(m.dashBodyLines(iw)), m.dashScrollViewH(iw))
			case "down", "j":
				iw := innerWidth(m.boxWidth())
				m.dashScroll = clampScroll(m.dashScroll+1, len(m.dashBodyLines(iw)), m.dashScrollViewH(iw))
			}
			return m, nil
		case phaseSecurity:
			// Security menu: 1/2 trigger the SAFE actions, 3 the DANGER action. enter
			// confirms a pending danger lockout (after the explicit warning), routing
			// through phaseKey so the generated key is shown BEFORE A2-danger applies;
			// esc cancels the confirm, or with no pending confirm returns to the
			// Dashboard. ↑↓/k/j scroll if the body overflows.
			if m.secDangerConfirm {
				switch msg.String() {
				case "enter":
					m.secDangerConfirm = false
					return m.launchKeyOnlyDanger()
				case "esc", "b":
					m.secDangerConfirm = false
					return m, nil
				}
				return m, nil
			}
			switch msg.String() {
			case "1":
				return m.securityAction(secBtnCreateAdmin)
			case "2":
				return m.securityAction(secBtnCryptoKey)
			case "3":
				return m.securityAction(secBtnKeyOnlyDanger)
			case "esc", "b":
				m.phase = phaseDashboard
				return m, nil
			case "up", "k":
				m.dashScroll = clampScroll(m.dashScroll-1, len(m.securityBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
			case "down", "j":
				m.dashScroll = clampScroll(m.dashScroll+1, len(m.securityBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
			}
			return m, nil
		case phaseKey:
			// 'c' copies the key to the system clipboard; any "back" key returns to
			// wherever the key screen was opened from (the run, or the summary).
			switch msg.String() {
			case "c":
				m = m.copyKey()
			case "enter", "esc", "b":
				m.phase = m.keyReturn
			}
			return m, nil
		}
		// run/done phase
		switch msg.String() {
		case "enter", "esc", "b":
			// First advance from a FINISHED run opens the stats summary (the sampler
			// keeps living so the monitor footer stays alive on the summary screen).
			if m.finished && m.haveSummary {
				m.phase = phaseSummary
				return m, nil
			}
			if m.finished {
				return m.goBack()
			}
			// Run still in flight (dialing / auditing): esc or b aborts back to the
			// form so a stuck or slow connection attempt is never a dead end. enter is
			// a no-op here to avoid an accidental abort. The engine goroutine detaches
			// and finishes on its own; goBack swaps in fresh channels and connMsg is
			// phase-guarded so a late connect can't start a sampler on the form.
			if msg.String() != "enter" {
				return m.goBack()
			}
		case "up", "k":
			m.vp.ScrollUp(1)
		case "down", "j":
			m.vp.ScrollDown(1)
		}
		return m, nil

	case logMsg:
		// Sanitize on arrival so the on-screen viewport can never be corrupted by
		// raw server output (carriage returns from apt/dpkg progress, tabs, ANSI
		// cursor moves). The log FILE stays raw (ui.Logger writes it independently);
		// only what reaches the screen is cleaned. wrapped() then hard-wraps the
		// sanitized text to the viewport width so nothing crosses the border.
		m.content += sanitizeStreamLine(string(msg))
		m.vp.SetContent(m.wrapped())
		m.vp.GotoBottom()
		return m, m.listen()

	case doneMsg:
		m.finished = true
		m.vp.SetHeight(m.vpHeight())
		m.finalErr = msg.err
		// The finished tail is NOT baked into m.content (frozen scrollback) — it is
		// rendered fresh from m.lang in runView each frame (see finishedTail) so a
		// post-finish language switch re-renders it. We still want the viewport
		// pinned to the bottom of the streamed log.
		m.vp.SetContent(m.wrapped())
		m.vp.GotoBottom()
		// v2: no SetWindowTitle Cmd — stash the title KIND; View() builds the
		// localized title per-frame so the chrome follows a live language switch.
		if msg.err != nil {
			m.titleK = titleFailed
		} else {
			m.titleK = titleHardened
		}
		// Auto-advance to the summary once BOTH completion signals have landed.
		// doneMsg and progMsg(Done) travel on separate channels, so either may be
		// last; whichever runs last performs the transition. Guard on haveSummary so
		// an early connect/auth abort (no summary) stays on the run log for the
		// operator to read, and on phaseRun so a generated-key view isn't yanked away.
		if m.haveSummary && m.phase == phaseRun {
			m = m.advanceFromRun()
		}
		return m, nil

	case updateCheckMsg:
		// Resolve the landing update strip from the one-shot check. An err is a failed
		// check (offline); found==true means a newer release; found==false is up-to-date.
		switch {
		case msg.err != nil:
			m.updateState = updErr
			m.updateVer = ""
		case msg.found:
			m.updateState = updAvailable
			m.updateVer = msg.ver
		default:
			m.updateState = updCurrent
			m.updateVer = ""
		}
		return m, nil

	case connMsg:
		// Ignore a late connect signal once the operator has left the run — e.g.
		// pressed esc to abort a slow dial and is back on the landing form. Without
		// this guard the abandoned engine goroutine's OnConnect would start a live
		// sampler dialing in the background while sitting on the form.
		if m.phase == phaseForm {
			return m, nil
		}
		// Engine signaled key auth is active — start the live sampler.
		m.statsCh = make(chan monitor.Sample, 4)
		ctx, cancel := context.WithCancel(context.Background())
		m.stopSample = cancel
		m.sampler = monitor.New(monitor.ConnInfo(msg))
		go m.sampler.Run(ctx, m.statsCh)
		// The engine hands over the private-key PEM here. Stash it for other screens,
		// but auto-route to the key screen ONLY for a freshly GENERATED ephemeral key
		// (password path). With a user-supplied --key, KeyGenerated is false and we
		// must NOT flash the operator their own private key.
		m.keyPEM = string(msg.KeyPEM)
		if msg.KeyGenerated && m.keyPEM != "" && !m.keyShown {
			m.keyShown = true
			m.keyReturn = m.phase
			m.phase = phaseKey
		}
		return m, m.listenStats()

	case statMsg:
		s := monitor.Sample(msg)
		if s.Connected {
			// Good sample: store it as the last-good metrics and reset the miss run.
			m.sample = s
			m.haveSample = true
			m.statMiss = 0
		} else {
			// Transient miss (slow/failed sample or a reconnect attempt): do NOT
			// discard the last-good metrics — just count the miss. The footer keeps
			// rendering the held sample until statMiss reaches the threshold (see
			// renderMonitor), so jitter no longer blanks it.
			m.statMiss++
		}
		return m, m.listenStats()

	case progMsg:
		p := engine.Progress(msg)
		if p.Done {
			m.summary = p.Summary
			m.haveSummary = true
			m.running = false
			// Audit (read-only) lands on the Dashboard, carrying the server facts +
			// the full tweak audit. Capture them from the final Done's Summary.
			if m.command == "audit" {
				m.captureAudit(p.Summary)
			}
			// Auto-advance once BOTH completion signals have landed (see doneMsg).
			// Guard on finished so a summary that somehow precedes doneMsg waits, and
			// on phaseRun so the key view isn't yanked away.
			if m.finished && m.phase == phaseRun {
				m = m.advanceFromRun()
			}
		} else {
			m.index = p.Index
			m.total = p.Total
			m.curID = p.ID
			m.curTitle = p.Title
			m.running = p.Status == "running"
			// During an audit, the per-tweak progress drives the connecting-state
			// counter/spinner shown on the Dashboard once it opens.
			if m.command == "audit" {
				m.dashAuditRunning = true
				m.dashAuditTotal = p.Total
			}
		}
		return m, m.listenProg()

	case tickMsg:
		// Advance the live timer + spinner only while the run is in flight; stop
		// re-issuing the tick once finished so there is no busy loop afterward.
		if m.finished {
			return m, nil
		}
		m.elapsed = time.Since(m.runStart)
		m.spin = (m.spin + 1) % len(spinnerFrames)
		return m, tickEvery()

	case resizeTickMsg:
		// Poll the terminal size and re-schedule, in EVERY phase and after finish.
		// On Windows v2 delivers no resize events, so this is the only way a
		// maximize is noticed; the delivered WindowSizeMsg rebuilds the viewport in
		// its own handler above. tea.RequestWindowSize is a func() Msg (a tea.Cmd).
		return m, tea.Batch(tea.RequestWindowSize, resizeTick())
	}

	// pass other messages to the focused input during the form phase
	if m.phase == phaseForm && m.focus < nInputs {
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "tab", "down":
		m.focus = stepFocus(m.focusableRows(), m.focus, +1)
		return m, m.refocus()
	case "shift+tab", "up":
		m.focus = stepFocus(m.focusableRows(), m.focus, -1)
		return m, m.refocus()
	case "left", "right":
		switch m.focus {
		case rowDisclosure:
			m.advancedOpen = !m.advancedOpen
		case rowLog:
			m.saveLog = !m.saveLog
		}
		return m, nil
	case "enter":
		if m.focus == rowUpdateButton && m.updateState == updAvailable {
			// Operator chose to self-update: record intent + keep the target version,
			// then quit so the alt-screen tears down before main() relaunches the
			// updated binary (Run() returns these via Result).
			m.wantUpdate = true
			return m, tea.Quit
		}
		if m.focus == rowStart {
			// The landing "Подключиться" button is READ-ONLY: it runs the audit
			// (dial → detect → tweaks audit → Dashboard), never the apply path.
			m.command = "audit"
			return m.start()
		}
		if m.focus == rowDisclosure {
			m.advancedOpen = !m.advancedOpen
			return m, nil
		}
		// Inside a text input: Enter connects ONLY from the LAST focusable input
		// (Password normally, Key when advanced is open) — the natural end of
		// typing — and only when the form is connectable. Earlier inputs just
		// advance focus. This restores the multiline-paste guard: on terminals
		// without bracketed paste a paste arrives as a KeyPressMsg stream with
		// embedded "enter" events, so an Enter mid-paste in a non-final field
		// must never auto-start the connect.
		if m.focus < nInputs {
			if m.focus == m.lastFocusableInput() && m.formConnectable() {
				m.command = "audit"
				return m.start()
			}
			m.focus = stepFocus(m.focusableRows(), m.focus, +1)
			return m, m.refocus()
		}
		return m, nil
	}

	if m.focus < nInputs {
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		// Filter out junk (e.g. multiline paste) so a bad Host/Port can't reach the engine.
		if clean := sanitizeField(m.focus, m.inputs[m.focus].Value()); clean != m.inputs[m.focus].Value() {
			m.inputs[m.focus].SetValue(clean)
		}
		return m, cmd
	}
	return m, nil
}

// formConnectable reports whether the landing form carries enough to dial: a
// non-empty Host plus at least one credential (Password OR SSH key). Used by the
// Enter-to-connect shortcut so typing Host + Password then pressing Enter dials.
func (m model) formConnectable() bool {
	host := strings.TrimSpace(m.inputs[fHost].Value())
	pass := strings.TrimSpace(m.inputs[fPass].Value())
	key := strings.TrimSpace(m.inputs[fKey].Value())
	return host != "" && (pass != "" || key != "")
}

// goBack returns from the run/done view to the form, resetting run state so a
// new run can be started (e.g. to fix credentials after a failed connection).
func (m model) goBack() (tea.Model, tea.Cmd) {
	m.stopSampler()
	m.phase = phaseForm
	m.finished = false
	m.finalErr = nil
	m.content = ""
	m.errMsg = ""
	m.logs = make(chan string, 4096)
	m.done = make(chan error, 1)
	m.connCh = make(chan monitor.ConnInfo, 1)
	m.progCh = make(chan engine.Progress, 256)
	// Reset run-progress so a fresh run starts clean.
	m.total, m.index = 0, 0
	m.curID, m.curTitle = "", ""
	m.running = false
	m.haveSummary = false
	m.summary = engine.Summary{}
	m.elapsed = 0
	m.spin = 0
	m.vp.SetContent("")
	m.sumScroll = 0
	m.wikiScroll = 0
	m.wikiProbeID = ""
	m.wikiUpdateConfirm = false
	m.matScroll = 0
	m.titleK = titleIdle
	// Reset Dashboard audit state so a fresh connect re-audits cleanly.
	m.dashAuditRunning = false
	m.dashAuditDone = false
	m.dashAuditTotal = 0
	m.dashAuditApplied = 0
	m.dashAuditResults = nil
	m.dashAuditRaw = nil
	m.dashFacts = nil
	m.dashScroll = 0
	m.dashApplyConfirm = false
	// command resets to the read-only audit so a subsequent Connect never applies.
	m.command = "audit"
	return m, m.refocus()
}

// advanceFromRun performs the post-finish phase transition out of phaseRun once
// both completion signals (doneMsg + progMsg Done) have landed. The destination
// depends on the command: an "audit" lands on the Dashboard (read-only), a verify/
// tweak audit with results lands on the matrix, otherwise the stats summary.
func (m model) advanceFromRun() model {
	if m.command == "audit" {
		m.phase = phaseDashboard
		m.dashScroll = 0
		return m
	}
	if len(m.summary.Tweaks) > 0 {
		m.phase = phaseMatrix
		m.matScroll = 0
		return m
	}
	m.phase = phaseSummary
	return m
}

// captureAudit folds the audit's final Summary into the Dashboard state: the server
// facts, the full per-tweak results, and the applied/total tally. Called when an
// "audit" command finishes (its Done carries Summary.Facts + Summary.Tweaks).
func (m *model) captureAudit(sum engine.Summary) {
	m.dashFacts = sum.Facts
	m.dashAuditRaw = sum.Tweaks
	// Informational access-policy probes are shown on the Security screen, not in the
	// tweaks audit grid — drop them from the display set.
	disp := sum.Tweaks[:0:0]
	for _, r := range sum.Tweaks {
		if r.Informational {
			continue
		}
		disp = append(disp, r)
	}
	m.dashAuditResults = disp
	m.dashAuditTotal = len(disp)
	applied := 0
	for _, r := range disp {
		if r.Applied {
			applied++
		}
	}
	m.dashAuditApplied = applied
	m.dashAuditRunning = false
	m.dashAuditDone = true
}

func (m *model) refocus() tea.Cmd {
	var cmd tea.Cmd
	for i := range m.inputs {
		if i == m.focus {
			cmd = m.inputs[i].Focus()
		} else {
			m.inputs[i].Blur()
		}
	}
	return cmd
}

func (m model) start() (tea.Model, tea.Cmd) {
	cfg := &config.Config{
		Host:      strings.TrimSpace(m.inputs[fHost].Value()),
		User:      strings.TrimSpace(m.inputs[fUser].Value()),
		Password:  m.inputs[fPass].Value(),
		KeyPath:   strings.TrimSpace(m.inputs[fKey].Value()),
		Mode:      m.mode,
		AdminUser: defaultAdminUser,
		Port:      atoiDefault(strings.TrimSpace(m.inputs[fPort].Value()), 22),
		Lang:      m.langCode(), // engine-streamed messages follow the active UI language
	}
	if err := cfg.Validate(); err != nil {
		m.errMsg = m.localizeValidateErr(err, cfg)
		return m, nil
	}
	if !validHost(cfg.Host) {
		m.errMsg = fmt.Sprintf(t(m.lang, kErrInvalidHost), cfg.Host)
		return m, nil
	}
	if cfg.KeyPath != "" {
		if fi, err := os.Stat(cfg.KeyPath); err != nil || fi.IsDir() {
			m.errMsg = fmt.Sprintf(t(m.lang, kErrKeyNotFound), cfg.KeyPath)
			return m, nil
		}
	}

	host := strings.TrimSpace(m.inputs[fHost].Value())
	m.host = host
	// Save-log toggle (default off): point cfg.LogFile at a per-host timestamped
	// file so the engine's ui.Logger writes the full run log there; empty disables it.
	if m.saveLog {
		cfg.LogFile = fmt.Sprintf("morgward-%s-%s.log", fsSafeHost(host), time.Now().Format("20060102-150405"))
	} else {
		cfg.LogFile = ""
	}
	m.phase = phaseRun
	m.vp = viewport.New(viewport.WithWidth(m.vpWidth()), viewport.WithHeight(m.vpHeight()))
	// A full `run` changes SSH auth policy — show the operator a mode-aware notice up
	// front (strict: password login OFF, key-only; soft: password stays ON + key added)
	// (and again in the finished tail) how to log in afterward. detect/verify don't
	// change auth, so the warning would be misleading there.
	if m.command == "run" {
		m.content = m.pwOffWarning() + "\n\n"
		m.vp.SetContent(m.wrapped())
	}
	// Start the live elapsed timer + spinner heartbeat for the run.
	m.runStart = time.Now()
	m.elapsed = 0
	m.running = true
	cmd := m.command

	// Engine runs in a goroutine; log lines stream into m.logs, finish into m.done.
	// Hook callbacks run on the engine goroutine, so they must NOT touch the model —
	// each only hands its value to the bubbletea loop via a buffered channel.
	ids := m.cmds
	go func() {
		err := engine.Execute(cfg, cmd, ids, engine.Hooks{
			Sink: func(line string) { m.logs <- line },
			OnConnect: func(info monitor.ConnInfo) {
				select {
				case m.connCh <- info:
				default: // buffered size 1; OnConnect fires once, so this won't block
				}
			},
			OnProgress: func(p engine.Progress) { m.progCh <- p },
		})
		m.done <- err
	}()
	m.titleK = titleWarding
	return m, tea.Batch(
		m.listen(), m.listenConn(), m.listenProg(), tickEvery(),
	)
}

// localizeValidateErr maps config.Validate()'s sentinel errors to a localized
// message for the form's error line, so a RU session never sees raw English. An
// unmapped error falls back to the generic localized "config error: <text>".
func (m model) localizeValidateErr(err error, cfg *config.Config) string {
	switch {
	case errors.Is(err, config.ErrHostRequired):
		return t(m.lang, kErrHostRequired)
	case errors.Is(err, config.ErrUserRequired):
		return t(m.lang, kErrUserRequired)
	case errors.Is(err, config.ErrAuthRequired):
		return t(m.lang, kErrAuthRequired)
	case errors.Is(err, config.ErrBadMode):
		return fmt.Sprintf(t(m.lang, kErrBadMode), cfg.Mode)
	default:
		return fmt.Sprintf(t(m.lang, kErrValidationFail), err.Error())
	}
}

// listen blocks on the next log line or completion (re-issued after each line).
func (m model) listen() tea.Cmd {
	return func() tea.Msg {
		select {
		case l := <-m.logs:
			return logMsg(l)
		case e := <-m.done:
			return doneMsg{e}
		}
	}
}

// listenConn blocks on the engine's one-shot connection notification.
func (m model) listenConn() tea.Cmd {
	return func() tea.Msg {
		return connMsg(<-m.connCh)
	}
}

// listenStats blocks on the next monitor Sample (re-issued after each), mirroring
// listen() for logs. Guards a nil channel (sampler not started yet).
func (m model) listenStats() tea.Cmd {
	ch := m.statsCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		s, ok := <-ch
		if !ok {
			return nil // sampler stopped & closed the channel — end this listener
		}
		return statMsg(s)
	}
}

// listenProg blocks on the next engine Progress event (re-issued after each),
// mirroring listen() for logs. Guards a nil channel.
func (m model) listenProg() tea.Cmd {
	ch := m.progCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return progMsg(p)
	}
}

// stopSampler cancels the live sampler (if running) and clears its state.
func (m *model) stopSampler() {
	if m.stopSample != nil {
		m.stopSample()
		m.stopSample = nil
	}
	m.sampler = nil
	m.statsCh = nil
	m.haveSample = false
	m.sample = monitor.Sample{}
	m.statMiss = 0
}
