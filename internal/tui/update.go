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
	"github.com/UberMorgott/morgward/internal/sshx"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Streamed run messages (logMsg/doneMsg/connMsg/statMsg/progMsg) arrive wrapped
	// in a genMsg tagged with the run generation that produced them. Drop — and do
	// NOT re-issue the listener for — any message from a stale generation: a prior
	// in-session run's listeners stay parked on the (reused) channels because the
	// re-run paths funnel through launchEngine without goBack, and a goBack-cancelled
	// run can still emit a late message. Both must not corrupt the current run. A
	// current-generation message is unwrapped and handled by the type switch below;
	// its handler re-issues the listener via m.listen()/etc., which re-captures the
	// (unchanged) current generation.
	if gm, ok := msg.(genMsg); ok {
		if gm.gen != m.runGen {
			return m, nil
		}
		msg = gm.msg
	}
	switch msg := msg.(type) {
	case tea.FocusMsg:
		// Host-terminal window gained focus (DEC ?1004) → resume cursor blinking.
		m.focused = true
		return m, nil
	case tea.BlurMsg:
		// Host-terminal window lost focus → draw a steady hollow cursor (no blink).
		m.focused = false
		return m, nil

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
		// Keep the live terminal's emulator + remote pty matched to the new content
		// area so vim/top re-flow on a window resize.
		if m.term != nil {
			cols, rows := m.termContentSize()
			m.term.resize(cols, rows)
		}
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
		case phaseTerminal:
			// Tab-strip click switches workspace tabs (mirrors the ctrl+1/ctrl+2 keys in
			// workspaceKey): a Terminal-strip click shows the shell; a Files-strip click
			// ensures the FM session first and only switches if one exists (nil termClient on
			// a dial-failed workspace creates nothing — stay on Terminal).
			if tab, ok := m.wsTabAtClick(mc.X, mc.Y); ok {
				if tab == wsFiles {
					m = m.ensureFiles()
					if m.files != nil {
						m.wsTab = wsFiles
					}
				} else {
					m.wsTab = wsTerminal
				}
				return m, nil
			}
			// On the Files tab, a click on a listing row selects that entry (operations
			// land in a later task). Index space is the VISIBLE slice (filesRowAtClick),
			// matching sel + the view.
			if m.wsTab == wsFiles && m.files != nil && !m.files.prompting() {
				if act := m.filesActionAtClick(mc.X, mc.Y); act != fmActNone {
					return m.filesActionClick(act)
				}
				if idx, ok := m.filesRowAtClick(mc.X, mc.Y); ok {
					m.files.sel = idx
				}
			}
			return m, nil
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
			// The pinned [ На главную ] button navigates to the post-connect home
			// (Dashboard if connected, else the form). Checked first; its row never
			// overlaps the scrollable fix/access rows.
			if m.summaryHomeAtClick(mc.X, mc.Y) {
				return m.summaryGoHome()
			}
			// The right-column "ключ ‹показать›" row re-opens the key viewer (read-only;
			// keyReturn=phaseSummary so Esc comes back here).
			if m.summaryKeyShowAtClick(mc.X, mc.Y) {
				m.keyPreRun = false
				m.keyReturn = phaseSummary
				m.phase = phaseKey
				return m, nil
			}
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
			m.sumScroll = clampScroll(m.sumScroll+d, len(m.summaryBodyLines()), m.summaryBodyViewH())
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
		case phaseSecurity:
			d := 0
			if up {
				d = -wheelStep
			} else if down {
				d = wheelStep
			}
			iw := innerWidth(m.boxWidth())
			m.dashScroll = clampScroll(m.dashScroll+d, len(m.securityBodyLines(iw)), m.bodyViewH())
		case phaseTerminal:
			// Wheel scrolls LOCAL scrollback (not forwarded to the remote app). Disabled
			// while on the alternate screen (vim/top own the screen) — see terminalScrollable.
			// Mouse click/drag forwarding to the remote app is still DEFERRED for 2a.
			if m.terminalScrollable() {
				d := 0
				if up {
					d = -wheelStep
				} else if down {
					d = wheelStep
				}
				m.termScrollBy(d)
			}
		}
		return m, nil

	case tea.KeyPressMsg:
		// The Terminal screen (2a) is FULLY raw: every keypress must reach the remote
		// shell (Ctrl+C=SIGINT, 'q'/'l' are literal input, Esc is needed by vim/less).
		// Handle it FIRST so none of the global hotkeys (language toggle, q/ctrl+c quit)
		// below intercept input meant for the shell. Only termExitKey leaves the screen.
		if m.phase == phaseTerminal {
			return m.workspaceKey(msg)
		}
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
			// On the summary, enter/esc/b go to the post-connect home (Dashboard when
			// connected, else the form); ↑↓/k/j scroll the two-column body when it
			// overflows the (button-reserved) middle region.
			switch msg.String() {
			case "enter", "esc", "b":
				return m.summaryGoHome()
			case "up", "k":
				m.sumScroll = clampScroll(m.sumScroll-1, len(m.summaryBodyLines()), m.summaryBodyViewH())
			case "down", "j":
				m.sumScroll = clampScroll(m.sumScroll+1, len(m.summaryBodyLines()), m.summaryBodyViewH())
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
			case "t":
				// Open the interactive terminal (2a). Disabled while an apply-confirm is
				// armed so the keystroke can't slip past the modal.
				if !m.dashApplyConfirm {
					return m.openTerminal(phaseDashboard, wsTerminal)
				}
				return m, nil
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
			// 'c' copies the key to the system clipboard. On the PRE-RUN key modal
			// (CHANGE 2) Enter STARTS the run with the prepared key, while Esc/b aborts
			// back to the form; on the post-run/read-only key viewer, any "back" key
			// returns to wherever the screen was opened from.
			switch msg.String() {
			case "c":
				m = m.copyKey()
				return m, nil
			case "enter":
				if m.keyPreRun {
					return m.confirmPreRunKey()
				}
				m = m.dismissKeyViewer()
				return m, nil
			case "esc", "b":
				if m.keyPreRun {
					// Abort before the run even starts: clear the staged key and go home.
					m.keyPreRun = false
					m.pendingKey = nil
					return m.goBack()
				}
				m = m.dismissKeyViewer()
				return m, nil
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
		// Engine signaled key auth is active — start the live sampler. Tear down any
		// sampler from a PRIOR in-session run first: the re-run paths (summaryGoHome
		// dashStale re-audit, launchApplyTweaks, startSteps, startRevert) funnel into
		// launchEngine WITHOUT going through goBack, so without this the previous run's
		// sampler goroutine + its dedicated SSH connection would leak until process exit
		// on every reconnect. stopSampler is nil-safe + idempotent (first run is a no-op).
		m.stopSampler()
		m.statsCh = make(chan monitor.Sample, 4)
		ctx, cancel := context.WithCancel(context.Background())
		m.stopSample = cancel
		m.sampler = monitor.New(monitor.ConnInfo(msg))
		go m.sampler.Run(ctx, m.statsCh)
		// The engine hands over the private-key PEM here. Stash it for other screens
		// (the summary's "ключ ‹показать›" row). CHANGE 2: the generated key is now
		// shown as a PRE-RUN modal (start() routes to phaseKey before launching), so
		// we DO NOT auto-route here on connect — that would double-show the key after
		// the run already started. We only record that a key was generated so the
		// summary can offer to re-show it. With a user-supplied --key, KeyGenerated is
		// false and keyPEM stays the operator's own key, never auto-shown.
		if len(msg.KeyPEM) > 0 {
			m.keyPEM = string(msg.KeyPEM)
		}
		if msg.KeyGenerated {
			m.keyGenerated = true
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
			// A mutating run (step/revert/run) changed the box, so the audit checkmarks
			// captured at connect time are stale. Mark them so the next Dashboard entry
			// re-audits. Set even on a failed/aborted run — a partial apply still mutated
			// the box. The engine's RunSteps Summary carries no Tweaks/Facts, so a real
			// re-audit (not a capture here) is required.
			if mutatingCmd(m.command) {
				m.dashStale = true
			}
			// Auto-advance once BOTH completion signals have landed (see doneMsg).
			// Guard on finished so a summary that somehow precedes doneMsg waits, and
			// on phaseRun so the key view isn't yanked away.
			if m.finished && m.phase == phaseRun {
				m = m.advanceFromRun()
			}
			// Done is the LAST progress event for this run — do NOT re-issue
			// listenProg. Re-issuing would leave a listener parked on the (reused)
			// progCh; on the next in-session run (which reuses the same channel)
			// that stale-generation receiver would steal an event and drop it, so
			// the new run's progress could stall. The fresh run issues its own
			// listenProg from launchEngine under the bumped generation.
			return m, nil
		}
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

	case termTickMsg:
		// Terminal repaint heartbeat (2a). Drop a tick from a stale/closed session
		// (gen mismatch) or when the terminal is no longer the active screen — that
		// stops the ticker on leave with no busy loop. While open, just re-schedule:
		// the View() re-renders the emulator each frame, and dirty() clears damage so
		// an idle shell does not force needless work. (We always reschedule rather
		// than gate the re-render on dirty(), because Bubble Tea repaints on every
		// Update; the dirty check exists for callers that want to skip work, and we
		// keep the tick cheap.)
		if msg.gen != m.termGen || m.phase != phaseTerminal || m.term == nil {
			return m, nil
		}
		// Advance the cursor blink: flip on/off at the ~530ms boundary and reset the
		// counter. termTickInterval (25ms) divides termBlinkPeriod into ~21 ticks/flip.
		m.termBlinkTicks++
		if m.termBlinkTicks*int(termTickInterval) >= int(termBlinkPeriod) {
			m.termBlinkOn = !m.termBlinkOn
			m.termBlinkTicks = 0
		}
		// Follow mode: re-pin to the bottom each tick so newly-arrived output stays
		// visible. When the user has scrolled up (termFollow=false) the offset is held.
		m.termPinIfFollowing()
		return m, termTick(m.termGen)

	case fmXferDoneMsg:
		// An async FM Download/Upload finished. Clear the in-flight flag (so a new transfer
		// can start), then surface the outcome: an error → f.err; success → the notice. An
		// upload reloads the listing so the new remote file appears. Guard a nil session
		// (the workspace could have been torn down mid-transfer).
		if m.files == nil {
			return m, nil
		}
		m.files.transferring = false
		m.files.xferLabel = ""
		if msg.err != nil {
			m.files.err = msg.label + ": " + msg.err.Error()
			return m, nil
		}
		if msg.upload {
			_ = m.files.reload() // refresh so the uploaded file appears
		}
		m.files.err = msg.label // success notice (reload above may have set err; overwrite)
		return m, nil

	case tea.PasteMsg:
		// Bracketed paste into the terminal screen → feed the pasted text to the
		// remote shell verbatim. Other screens ignore paste here (the form's inputs
		// receive their own paste via the focused-input fallthrough below).
		if m.phase == phaseTerminal && m.term != nil {
			m.term.write([]byte(msg.Content))
		}
		return m, nil
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
	// Defensively tear down a live terminal session (2a) if one is somehow still open
	// when navigating home — cancel + reap its goroutines + close the pipe AND close the
	// underlying SSH client (its keepalive goroutine + transport, which the session does
	// not own) so nothing leaks across the long-lived TUI. Nil-safe. Normal exits go
	// through closeTerminal already.
	if m.term != nil {
		m.term.close()
		m.term = nil
		m.termGen++
	}
	// Mirror closeTerminal: close the Files session (its sftp client only) BEFORE the shared
	// transport, since sftp rides that transport. Asymmetry here would orphan an opened sftp
	// client on a home-navigation that bypasses closeTerminal. Nil-safe.
	if m.files != nil {
		m.files.close()
		m.files = nil
	}
	if m.termClient != nil {
		m.termClient.Close()
		m.termClient = nil
	}
	// Halt any in-flight engine run: cancel its context so it stops at the next step
	// boundary (F03), and close abort so a hook send parked on a now-stale full
	// channel unblocks and the engine goroutine reaches its deferred cleanup (F11).
	// Done before swapping in fresh channels so the OLD run's closure sees the close.
	if m.engineCancel != nil {
		m.engineCancel()
		m.engineCancel = nil
	}
	if m.abort != nil {
		close(m.abort)
		m.abort = nil
	}
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
	// Reset the pre-run key staging (CHANGE 2) so a fresh run re-generates and
	// re-shows its own key; clear the in-memory PEM + flags so nothing leaks across runs.
	m.keyPreRun = false
	m.keyGenerated = false
	m.pendingKey = nil
	m.keyPEM = ""
	m.keyCopied = false
	m.keyCopyFailed = false
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
	m.dashStale = false
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

// start is the FRONT half of a run launch (CHANGE 2): it validates the form, then
// branches. On the PASSWORD path of a MUTATING command (run/step/revert) it
// pre-generates the ephemeral ed25519 keypair and shows it as a modal (phaseKey,
// keyPreRun=true) FIRST — the engine launches only when the operator presses Enter
// on that screen (launchEngineCmd). On the --key path, or for the read-only audit
// (which never generates a key), it launches the engine immediately. The CLI is
// unaffected (it never calls this).
func (m model) start() (tea.Model, tea.Cmd) {
	cfg := &config.Config{
		Host:      strings.TrimSpace(m.inputs[fHost].Value()),
		User:      strings.TrimSpace(m.inputs[fUser].Value()),
		Password:  m.inputs[fPass].Value(),
		KeyPath:   strings.TrimSpace(m.inputs[fKey].Value()),
		AdminUser: defaultAdminUser,
		Port:      atoiDefault(strings.TrimSpace(m.inputs[fPort].Value()), 22),
		Lang:      m.langCode(), // engine-streamed messages follow the active UI language
	}
	if err := cfg.Validate(); err != nil {
		m.errMsg = m.localizeValidateErr(err)
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

	// Decide whether to show the generated key BEFORE the run. The key is generated
	// only on the PASSWORD path (no --key) of a MUTATING command (the read-only audit
	// never touches the box, so it produces no key). On that path, pre-generate the
	// keypair, stash it, and show the pre-run key modal; Enter there launches the
	// engine with this prepared key. Otherwise launch immediately.
	if cfg.KeyPath == "" && mutatingCmd(m.command) {
		kp, err := sshx.GenerateKeyPair("morgward@" + cfg.Host)
		if err != nil {
			m.errMsg = fmt.Sprintf(t(m.lang, kErrValidationFail), err.Error())
			return m, nil
		}
		m.pendingKey = kp
		m.keyPEM = string(kp.PrivatePEM)
		m.keyGenerated = true
		m.keyCopied = false
		m.keyCopyFailed = false
		m.host = strings.TrimSpace(m.inputs[fHost].Value())
		m.keyReturn = phaseForm // Esc on the pre-run modal aborts back to the form
		m.keyPreRun = true
		m.phase = phaseKey
		return m, nil
	}
	return m.launchEngine(cfg)
}

// mutatingCmd reports whether an engine command actually applies changes (and thus
// generates an ephemeral key on the password path). The read-only "audit"/"detect"/
// "verify" commands never mutate the box and produce no key.
func mutatingCmd(cmd string) bool {
	switch cmd {
	case "run", "step", "revert":
		return true
	}
	return false
}

// dismissKeyViewer closes the post-run/read-only key viewer (NOT the pre-run modal).
// CHANGE 3 hardening: if the run finished with a summary while this viewer was up
// (keyReturn would send us back to the now-stale run log), route via advanceFromRun
// so a finished run always lands on the rich summary/dashboard, never stranded on the
// run log. Otherwise return to wherever the viewer was opened from (e.g. the summary).
func (m model) dismissKeyViewer() model {
	if m.finished && m.haveSummary && m.keyReturn == phaseRun {
		return m.advanceFromRun()
	}
	m.phase = m.keyReturn
	return m
}

// confirmPreRunKey is invoked when the operator presses Enter on the PRE-RUN key
// modal (CHANGE 2): it rebuilds the validated config from the form (re-running the
// same checks start() did) and launches the engine with the already-staged
// m.pendingKey handed over as Hooks.PreparedKey. keyPreRun is cleared so the run's
// log view replaces the modal. Any validation regression here returns to the form
// with a localized error rather than launching half-formed.
func (m model) confirmPreRunKey() (tea.Model, tea.Cmd) {
	cfg := &config.Config{
		Host:      strings.TrimSpace(m.inputs[fHost].Value()),
		User:      strings.TrimSpace(m.inputs[fUser].Value()),
		Password:  m.inputs[fPass].Value(),
		KeyPath:   strings.TrimSpace(m.inputs[fKey].Value()),
		AdminUser: defaultAdminUser,
		Port:      atoiDefault(strings.TrimSpace(m.inputs[fPort].Value()), 22),
		Lang:      m.langCode(),
	}
	if err := cfg.Validate(); err != nil {
		m.keyPreRun = false
		m.pendingKey = nil
		m.phase = phaseForm
		m.errMsg = m.localizeValidateErr(err)
		return m, nil
	}
	m.keyPreRun = false
	return m.launchEngine(cfg)
}

// resetRunState clears every per-run completion/progress field to its zero value so a
// SECOND run in the same session (e.g. audit → dashboard "apply tweaks" → start →
// launchEngine) cannot inherit the prior run's state. Without this the run view would
// immediately render the previous run's finished tail / "✓ done" summary line before
// this run's own doneMsg/progMsg(Done) lands (BUG 1). It clears: the finished tail
// gates (finished/finalErr), the summary-line gates (haveSummary/summary), the
// progress-bar gates (index/total/curID/curTitle), the streamed log (content), the
// spinner, and the dash audit counters (so a re-audit re-counts cleanly). Pure value
// receiver — the model is copied by value every Update.
func (m model) resetRunState() model {
	m.content = ""
	m.finished = false
	m.finalErr = nil
	m.haveSummary = false
	m.summary = engine.Summary{}
	m.index = 0
	m.total = 0
	m.curID = ""
	m.curTitle = ""
	m.spin = 0
	m.dashAuditRunning = false
	m.dashAuditDone = false
	m.dashAuditTotal = 0
	m.dashAuditApplied = 0
	return m
}

// launchEngine is the BACK half of a run launch (CHANGE 2): it builds the run-scoped
// state, spawns the engine goroutine, and returns the streaming listeners. Called
// directly on the --key / audit path, or from the pre-run key modal's Enter once the
// operator has acknowledged the generated key. cfg carries the validated config; when
// m.pendingKey is non-nil it is handed to the engine as Hooks.PreparedKey.
func (m model) launchEngine(cfg *config.Config) (tea.Model, tea.Cmd) {
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

	// Bump the run generation so the listeners issued below are tagged with a fresh
	// generation. Any listener still parked from a PRIOR in-session run (the re-run
	// paths reach here without goBack, reusing the same channels) tagged its messages
	// with the OLD generation; Update now drops those and retires the stale listener,
	// so N in-session runs no longer leave N concurrent receivers splitting events off
	// one channel. goBack swaps in fresh channels for its abort path; this guards the
	// no-goBack re-run path that reuses them.
	m.runGen++

	// Reset per-run completion/progress state (BUG 1) so a SECOND run never inherits
	// the prior run's finished/summary flags.
	m = m.resetRunState()

	// A full `run` hardens SSH crypto but leaves access policy at the image default —
	// show the operator an info notice up front (password login STAYS ON + a key is
	// also generated) and again in the finished tail. detect/verify don't run the
	// step, so the notice would be misleading there.
	if m.command == "run" {
		m.content = t(m.lang, kPwOnInfo) + "\n\n"
	}
	m.vp.SetContent(m.wrapped())
	// Start the live elapsed timer + spinner heartbeat for the run.
	m.runStart = time.Now()
	m.elapsed = 0
	m.running = true
	cmd := m.command

	// Engine runs in a goroutine; log lines stream into m.logs, finish into m.done.
	// Hook callbacks run on the engine goroutine, so they must NOT touch the model —
	// each only hands its value to the bubbletea loop via a buffered channel.
	//
	// runCtx is cancelled by goBack so the engine stops at the next step boundary
	// (F03). abort is closed by goBack too; the hook sends select on it so a send
	// parked on a full channel (after goBack swapped in fresh ones) unblocks and the
	// engine goroutine reaches its deferred cli.Close() instead of leaking (F11).
	// Capture logs/connCh/progCh/done/abort as LOCALS: goBack reassigns the model's
	// fields, but this closure must keep talking to the channels this run owns.
	ids := m.cmds
	// preparedKey is the keypair the TUI pre-generated on the password path (CHANGE 2);
	// hand it to the engine so it reuses it instead of generating its own. nil on the
	// --key / audit path ⇒ unchanged engine behavior. Captured as a LOCAL like the
	// channels below so goBack reassigning model fields can't disturb this run.
	preparedKey := m.pendingKey
	runCtx, cancel := context.WithCancel(context.Background())
	m.engineCancel = cancel
	m.abort = make(chan struct{})
	abort := m.abort
	logs, connCh, progCh, done := m.logs, m.connCh, m.progCh, m.done
	go func() {
		err := engine.Execute(runCtx, cfg, cmd, ids, engine.Hooks{
			PreparedKey: preparedKey,
			Sink: func(line string) {
				select {
				case logs <- line:
				case <-abort:
				}
			},
			OnConnect: func(info monitor.ConnInfo) {
				select {
				case connCh <- info:
				default: // buffered size 1; OnConnect fires once, so this won't block
				}
			},
			OnProgress: func(p engine.Progress) {
				select {
				case progCh <- p:
				case <-abort:
				}
			},
		})
		select {
		case done <- err:
		case <-abort:
		}
	}()
	m.titleK = titleWarding
	return m, tea.Batch(
		m.listen(), m.listenConn(), m.listenProg(), tickEvery(),
	)
}

// localizeValidateErr maps config.Validate()'s sentinel errors to a localized
// message for the form's error line, so a RU session never sees raw English. An
// unmapped error falls back to the generic localized "config error: <text>".
func (m model) localizeValidateErr(err error) string {
	switch {
	case errors.Is(err, config.ErrHostRequired):
		return t(m.lang, kErrHostRequired)
	case errors.Is(err, config.ErrUserRequired):
		return t(m.lang, kErrUserRequired)
	case errors.Is(err, config.ErrAuthRequired):
		return t(m.lang, kErrAuthRequired)
	default:
		return fmt.Sprintf(t(m.lang, kErrValidationFail), err.Error())
	}
}

// listen blocks on the next log line or completion (re-issued after each line).
// The closure captures the channels AND the run generation as locals so a listener
// issued for a previous run keeps talking to that run's channels and tags every
// message with its own generation; Update drops + retires it once m.runGen moves on.
func (m model) listen() tea.Cmd {
	gen, logs, done := m.runGen, m.logs, m.done
	return func() tea.Msg {
		select {
		case l := <-logs:
			return genMsg{gen, logMsg(l)}
		case e := <-done:
			return genMsg{gen, doneMsg{e}}
		}
	}
}

// listenConn blocks on the engine's one-shot connection notification.
func (m model) listenConn() tea.Cmd {
	gen, connCh := m.runGen, m.connCh
	return func() tea.Msg {
		return genMsg{gen, connMsg(<-connCh)}
	}
}

// listenStats blocks on the next monitor Sample (re-issued after each), mirroring
// listen() for logs. Guards a nil channel (sampler not started yet).
func (m model) listenStats() tea.Cmd {
	gen, ch := m.runGen, m.statsCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		s, ok := <-ch
		if !ok {
			return nil // sampler stopped & closed the channel — end this listener
		}
		return genMsg{gen, statMsg(s)}
	}
}

// listenProg blocks on the next engine Progress event (re-issued after each),
// mirroring listen() for logs. Guards a nil channel.
func (m model) listenProg() tea.Cmd {
	gen, ch := m.runGen, m.progCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return genMsg{gen, progMsg(p)}
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
