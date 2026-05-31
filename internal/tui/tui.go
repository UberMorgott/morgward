// Package tui is a Bubble Tea front-end: a form to enter host/port/user/
// password-or-key/mode, then a live streaming view of the engine run.
package tui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/monitor"
	"github.com/UberMorgott/morgward/internal/stats"
	"github.com/UberMorgott/morgward/internal/steps"
	"github.com/UberMorgott/morgward/internal/tweaks"
	"github.com/UberMorgott/morgward/internal/version"
	"github.com/UberMorgott/morgward/internal/wiki"
)

const (
	defaultAdminUser = "vpsadmin"
)

var (
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252")) // form labels: brighter than the dim footer for clear hierarchy
	focusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))              // bottom control hint: stays dim gray
	tipStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Italic(true) // contextual toggle help: accent-tinted + italic so it reads as form body, not footer
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	pillStyle   = lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color("236"))
	pillOnStyle = lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color("57")).Foreground(lipgloss.Color("231")).Bold(true)

	// run-phase box chrome: the rounded border drawn by hand (lipgloss v1.1 has
	// no native border labels), tinted to match the form's accent.
	borderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("57"))

	// framed-input chrome (landing form): a dim rounded border when unfocused (240)
	// and an accent rounded border when focused (57), matching the design spec.
	inputBorderDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	inputBorderFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("57"))

	// monitor footer styles: dim chrome + threshold-colored percent.
	monDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	monLabelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	monGreenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	monYellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	monRedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

type phase int

const (
	phaseForm phase = iota
	phaseRun
	phaseSummary   // post-finish stats summary + clickable fix list
	phaseWiki      // a single fix's what/why/risk description
	phaseKey       // shows the generated SSH private key + a clipboard "Copy key" button
	phaseMatrix    // per-tweak audit table (the "анализ" action result)
	phaseDashboard // post-connect server card + live tweak audit + apply/security/catalog buttons
)

// titleKind is the window-title state. The actual localized title string is built
// per-frame in View() from m.lang so the terminal chrome follows a live language
// switch instead of being frozen at the moment the event fired.
type titleKind int

const (
	titleIdle     titleKind = iota // form / pre-run: "Name — Tagline"
	titleWarding                   // run in flight: "⚔ Name · <warding> host"
	titleHardened                  // finished OK:   "✓ Name · host <hardened>"
	titleFailed                    // finished err:  "✗ Name · host — <failed>"
)

// labelColW is the form's left label-column width for the CURRENT language: the
// MAX display width (lipgloss.Width, NOT byte len — Cyrillic is 2 bytes/rune) over
// every label rendered in that column (the 5 inputs + Mode + Action). Computed
// once per render and threaded into labelPad / the indent / renderToggle so the
// whole form shares one left edge in both ru and en. Replaces the old fixed
// const labelW=9, which misaligned the longer localized RU labels.
func (m model) labelColW() int {
	keys := []stringKey{
		kLabelHost, kLabelPort, kLabelUser, kLabelPassword, kLabelKey,
		kLabelMode, kSaveLogLabel,
	}
	w := 0
	for _, k := range keys {
		if lw := lipgloss.Width(t(m.lang, k)); lw > w {
			w = lw
		}
	}
	return w
}

// padLabel left-pads label to colW DISPLAY cells (lipgloss.Width, not byte len, so
// multibyte Cyrillic labels still align). Used by both labelPad and renderToggle.
func padLabel(label string, colW int) string {
	if pad := colW - lipgloss.Width(label); pad > 0 {
		return label + strings.Repeat(" ", pad)
	}
	return label
}

// saveLogToken maps the save-log bool to the canonical pill token ("on"/"off")
// renderToggle matches against, mirroring how mode/command use their string tokens.
func saveLogToken(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// field indices in the form.
const (
	fHost = iota
	fPort
	fUser
	fPass
	fKey
	nInputs // number of text inputs
)

// extra focusable rows after the inputs.
const (
	rowDisclosure = nInputs     // "▸ Дополнительно" advanced-inputs toggle
	rowLog        = nInputs + 1 // save-log-to-file toggle
	rowStart      = nInputs + 2 // connect button
	nRows         = nInputs + 3
)

// focusableRows returns the ordered list of currently-focusable row indices.
// Navigation runs over this slice so focus never lands on a hidden row (the
// advanced Port/User/Key inputs are included only when advancedOpen).
func (m model) focusableRows() []int {
	rows := make([]int, 0, nRows)
	// Visible inputs: Host + Password always; Port/User/Key only when advancedOpen,
	// so focus never strands on a hidden field.
	for i := range nInputs {
		if !m.advancedOpen && (i == fPort || i == fUser || i == fKey) {
			continue
		}
		rows = append(rows, i)
	}
	rows = append(rows, rowDisclosure)
	rows = append(rows, rowLog)
	rows = append(rows, rowStart)
	return rows
}

type logMsg string
type doneMsg struct{ err error }
type connMsg monitor.ConnInfo
type statMsg monitor.Sample
type progMsg engine.Progress
type tickMsg time.Time
type resizeTickMsg time.Time

// statMissThreshold is how many CONSECUTIVE disconnected monitor samples must
// arrive before the footer treats the box as genuinely gone (reboot) and shows
// the "reconnecting…" line. The sampler emits one sample per second
// (monitor.sampleInterval), so 3 ≈ 3s of real outage — long enough to ride out a
// single slow/failed sample without blanking the footer on jitter.
const statMissThreshold = 3

// spinnerFrames is the small braille spinner shown in the progress line while a
// run is in flight; it advances once per tick.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// tickEvery is the 1s heartbeat that drives the live elapsed timer + spinner.
func tickEvery() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// resizeTickInterval is how often we re-poll the terminal size. Bubble Tea v2 on
// Windows has no SIGWINCH (listenForResize is a no-op there), so a WindowSizeMsg
// arrives only ONCE at startup and a maximize/resize would otherwise never be
// picked up. We poll RequestWindowSize on this cadence in EVERY phase so the UI
// re-stretches within ~0.5s of a resize.
const resizeTickInterval = 500 * time.Millisecond

// resizeTick schedules the next resize-poll. It is issued from Init() and
// re-issued from its own handler regardless of phase/finished, so size polling
// runs for the whole program lifetime (unlike the spinner tick, which stops on
// finish).
func resizeTick() tea.Cmd {
	return tea.Tick(resizeTickInterval, func(t time.Time) tea.Msg { return resizeTickMsg(t) })
}

type model struct {
	phase   phase
	inputs  []textinput.Model
	focus   int
	mode    config.Mode
	command string   // engine command token; the form no longer exposes a selector (stays "run")
	cmds    []string // step IDs for the "step" command (Dashboard "Применить твики"); nil otherwise
	errMsg  string
	saveLog bool // form toggle: write the full run log to a file (sets cfg.LogFile)
	// advancedOpen is the landing "▸ Дополнительно" disclosure state: when true the
	// framed Port/User/Key inputs are included in formRows; novice default is false
	// (Host + Password only). Plain bool — the model is copied by value every Update.
	advancedOpen bool

	logs    chan string
	done    chan error
	vp      viewport.Model
	content string // accumulated log text (NOT strings.Builder — model is copied by value)
	w, h    int

	// live monitor: own sshx connection sampled on the bubbletea loop.
	sample     monitor.Sample
	haveSample bool
	// statMiss counts CONSECUTIVE disconnected samples (Connected==false). The
	// footer keeps showing the last-good metrics until statMiss reaches
	// statMissThreshold (a genuine outage, e.g. a reboot), so a single slow/failed
	// sample no longer blanks the footer on transient jitter.
	statMiss   int
	statsCh    chan monitor.Sample
	connCh     chan monitor.ConnInfo
	sampler    *monitor.Sampler
	stopSample context.CancelFunc

	// run progress: per-step events streamed from the engine over progCh.
	total, index int
	curID        string
	curTitle     string
	running      bool
	summary      engine.Summary
	haveSummary  bool
	progCh       chan engine.Progress

	// wiki navigation: which step's description is shown (phaseWiki) and which
	// phase to return to on esc (phaseSummary).
	wikiStep   string
	wikiReturn phase

	// SSH key screen (phaseKey): the generated private-key PEM (lives only in
	// memory; never logged), the copy-to-clipboard status, where esc returns to,
	// and whether the auto-route to this screen has already fired once. All plain
	// copyable types — the model is copied by value every Update.
	keyPEM        string
	keyCopied     bool
	keyCopyFailed bool
	keyReturn     phase
	keyShown      bool

	// scroll offsets for the directly-rendered summary + wiki screens (the run
	// screen scrolls its own m.vp instead). They are clamped to the current body
	// length and middle-region height on every use (clampScroll), so growing the
	// window auto-reduces the offset and the monitor footer stays pinned.
	sumScroll  int
	wikiScroll int
	matScroll  int // анализ matrix scroll offset, clamped like sumScroll

	// Dashboard state (phaseDashboard). All value-copyable: bools/ints, a plain
	// []tweaks.Result (slice of plain structs), and a *detect.Facts (read-only after
	// detection — never mutated, so the pointer copy is safe). dashFacts is nil until
	// the Audit's final Done lands. dashApplyConfirm gates the A8 reboot warning before
	// "Применить твики" launches the apply.
	dashAuditRunning bool
	dashAuditDone    bool
	dashAuditTotal   int
	dashAuditApplied int
	dashAuditResults []tweaks.Result
	dashFacts        *detect.Facts
	dashScroll       int  // audit list scroll offset (clamped like sumScroll)
	dashApplyConfirm bool // true while the A8 reboot-warning confirm is shown

	finalErr error
	finished bool
	host     string    // target host, stashed at start() for window-title updates
	titleK   titleKind // window-title state; the localized title is built per-frame in View() from m.lang
	lang     Lang      // active UI language (ru/en); every shown string is keyed on this

	// live top-area heartbeat: elapsed timer + spinner, driven by a 1s tea.Tick
	// that runs only while the run is in flight (m.running && !m.finished).
	runStart time.Time
	elapsed  time.Duration
	spin     int
}

// newModel builds the initial TUI model.
func newModel() model {
	ins := make([]textinput.Model, nInputs)
	for i := range ins {
		ti := textinput.New()
		ti.SetWidth(44) // visible width: longer values scroll inside the field, never overflow the form
		ins[i] = ti
	}
	// Placeholders are set per-language in syncPlaceholders (called here and on
	// every language switch). CharLimits/echo are language-independent.
	ins[fHost].CharLimit = 253 // max hostname length
	ins[fHost].Focus()
	ins[fPort].SetValue("22")
	ins[fPort].CharLimit = 5
	ins[fUser].SetValue("root")
	ins[fUser].CharLimit = 64
	ins[fPass].CharLimit = 256
	ins[fPass].EchoMode = textinput.EchoPassword
	ins[fPass].EchoCharacter = '•'
	ins[fKey].CharLimit = 512

	m := model{
		phase:   phaseForm,
		inputs:  ins,
		mode:    config.ModeSoft,
		command: "run",
		logs:    make(chan string, 4096),
		done:    make(chan error, 1),
		connCh:  make(chan monitor.ConnInfo, 1),
		progCh:  make(chan engine.Progress, 256),
		lang:    defaultLang,
		titleK:  titleIdle,
	}
	m.syncPlaceholders()
	return m
}

// syncPlaceholders re-sets the input placeholders for the current language. The
// placeholder is the only per-field string that lives on the textinput model, so
// it must be refreshed whenever m.lang changes (the labels are rendered fresh in
// View each frame, but placeholders are stored state).
func (m *model) syncPlaceholders() {
	m.inputs[fHost].Placeholder = t(m.lang, kPhHost)
	m.inputs[fPort].Placeholder = t(m.lang, kPhPort)
	m.inputs[fUser].Placeholder = t(m.lang, kPhUser)
	m.inputs[fPass].Placeholder = t(m.lang, kPhPass)
	m.inputs[fKey].Placeholder = t(m.lang, kPhKey)
}

// toggleLang flips the language and refreshes any stored per-language state.
// Wired to the 'l' / ctrl+l hotkey and the RU/EN click handler; works in both
// the form and run phases.
func (m *model) toggleLang() {
	m.lang = m.lang.next()
	m.syncPlaceholders()
}

func (m model) Init() tea.Cmd {
	// v2 has no tea.SetWindowTitle Cmd; the window title is a tea.View field built
	// per-frame in View() from m.titleK + m.lang (see windowTitle).
	// Start the always-on resize poll (Windows has no SIGWINCH — see resizeTick).
	return tea.Batch(textinput.Blink, resizeTick())
}

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
		case phaseSummary:
			// A click on a fix row opens its wiki description.
			if id, ok := m.fixAtClick(mc.X, mc.Y); ok {
				m.wikiStep = id
				m.wikiReturn = phaseSummary
				m.wikiScroll = 0 // fresh page starts at the top
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
			m.wikiScroll = clampScroll(m.wikiScroll+d, len(m.wikiBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
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
			m.dashScroll = clampScroll(m.dashScroll+d, len(m.dashBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
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
			// Any "back" key returns to wherever the wiki was opened from (summary);
			// ↑↓/k/j scroll the description when it overflows the middle region.
			switch msg.String() {
			case "enter", "esc", "b":
				m.phase = m.wikiReturn
			case "up", "k":
				m.wikiScroll = clampScroll(m.wikiScroll-1, len(m.wikiBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
			case "down", "j":
				m.wikiScroll = clampScroll(m.wikiScroll+1, len(m.wikiBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
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
				m.dashScroll = clampScroll(m.dashScroll-1, len(m.dashBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
			case "down", "j":
				m.dashScroll = clampScroll(m.dashScroll+1, len(m.dashBodyLines(innerWidth(m.boxWidth()))), m.bodyViewH())
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

	case connMsg:
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
		// Enter advances focus only WITHIN the text inputs and stops at the first
		// non-input row. This prevents a multiline paste (which on terminals without
		// bracketed paste arrives as a keystroke stream with embedded Enters) from
		// walking through every field and auto-pressing Start. Reach Start via tab/↑↓.
		if m.focus < nInputs {
			m.focus++
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

// sanitizeField strips characters that don't belong in a given input. Newlines/
// control chars are always removed; Host keeps only IP/hostname chars, Port digits.
func sanitizeField(idx int, s string) string {
	keep := func(r rune) bool {
		if r < 0x20 { // control chars incl. \n \r \t
			return false
		}
		switch idx {
		case fHost:
			return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') || r == '.' || r == '-'
		case fPort:
			return r >= '0' && r <= '9'
		default:
			return true
		}
	}
	var b strings.Builder
	for _, r := range s {
		if keep(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizeStreamLine cleans one chunk of streamed output (which may contain
// several "\n"-separated lines) so it can never break the box frame:
//   - carriage returns are collapsed: apt/dpkg redraw a progress line by emitting
//     "...30%\r...60%\r...100%" — keep only the LAST \r-segment of each line, which
//     is what a terminal would have shown after the redraws settled.
//   - tabs expand to a single space (the box has no tab stops; a raw \t would
//     advance the cursor unpredictably and overflow innerW).
//   - ALL ANSI escape / CSI sequences and other C0 control chars are stripped.
//     The viewport re-renders plain text through lipgloss, so colour here would be
//     lost on wrap anyway; stripping it removes every cursor-move/erase sequence
//     that would otherwise shift the frame. The raw log file is untouched.
//
// It is a pure function (no model state) so it is unit-testable; wrapping to the
// content width happens afterwards in wrapped().
func sanitizeStreamLine(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		// Collapse \r redraws: keep the segment after the final \r.
		if idx := strings.LastIndex(ln, "\r"); idx >= 0 {
			ln = ln[idx+1:]
		}
		ln = stripControlAndANSI(ln)
		lines[i] = ln
	}
	return strings.Join(lines, "\n")
}

// stripControlAndANSI removes ANSI escape sequences (ESC[…] CSI and ESC-prefixed
// two-byte sequences) and other C0 control characters, expanding tabs to a space.
// Newlines are NOT seen here (sanitizeStreamLine splits on them first).
func stripControlAndANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == 0x1b { // ESC: skip the whole escape sequence
			i++
			if i >= len(rs) {
				break
			}
			if rs[i] == '[' { // CSI: ESC '[' params... final-byte in 0x40..0x7e
				i++
				for i < len(rs) && (rs[i] < 0x40 || rs[i] > 0x7e) {
					i++
				}
				// loop's i++ skips the final byte
			}
			// other ESC x two-byte sequence: the i++ above already consumed x
			continue
		}
		if r == '\t' {
			b.WriteByte(' ')
			continue
		}
		if r < 0x20 || r == 0x7f { // other C0 controls (incl. stray \r) — drop
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// wrapped soft-wraps the accumulated (already-sanitized) log text to the viewport
// width so long lines (e.g. SSH error messages or server output) hard-wrap inside
// the box instead of overflowing. The wrap width equals innerW (vp.Width()), and
// the per-line "  │ " prefix added upstream is part of the wrapped text, so every
// wrapped segment — first or continuation — is ≤ innerW and never crosses the border.
func (m model) wrapped() string {
	w := m.vp.Width()
	if w < 1 {
		w = maxi(innerWidth(m.w), 1)
	}
	return lipgloss.NewStyle().Width(w).Render(m.content)
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
	m.matScroll = 0
	m.titleK = titleIdle
	// Reset Dashboard audit state so a fresh connect re-audits cleanly.
	m.dashAuditRunning = false
	m.dashAuditDone = false
	m.dashAuditTotal = 0
	m.dashAuditApplied = 0
	m.dashAuditResults = nil
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
	m.dashAuditResults = sum.Tweaks
	m.dashAuditTotal = len(sum.Tweaks)
	applied := 0
	for _, r := range sum.Tweaks {
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

// vpWidth/vpHeight compute the bounded inner viewport size for the run-phase box
// so the log never overflows the box or overlaps the contextual hints.
func (m model) vpWidth() int { return maxi(innerWidth(m.w), 1) }

func (m model) vpHeight() int {
	// main chrome: top border + switcher + progress + blank + hints + bottom = 6
	// monitor box: top border + content + bottom = 3
	base := m.h - 6 - 3
	if m.finished {
		base-- // reserve a row for the "Back to main" button line
		base -= m.finishedTailRows()
	}
	return maxi(base, 3)
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

// View renders the UI as a tea.View (v2: View returns a value, not a string),
// with alt-screen + cell-motion mouse + window title set per-frame.
func (m model) View() tea.View {
	v := tea.NewView(m.viewString())
	// Per-View frame fields: set on EVERY returned View in BOTH phases so
	// alt-screen + mouse + title never drop for a frame. The title is built from
	// m.lang here (not stashed as a string) so the terminal chrome follows a live
	// language switch.
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = m.windowTitle()
	return v
}

// windowTitle builds the terminal-title-bar text for the current frame, localized
// to m.lang. Driven by m.titleK (set at the run lifecycle events) so the chrome
// re-renders in the active language on every frame, including after a switch.
func (m model) windowTitle() string {
	switch m.titleK {
	case titleWarding:
		return "⚔ " + version.Name + " · " + t(m.lang, kTitleWarding) + " " + m.host
	case titleHardened:
		return "✓ " + version.Name + " · " + m.host + " " + t(m.lang, kTitleHardened)
	case titleFailed:
		return "✗ " + version.Name + " · " + m.host + " — " + t(m.lang, kTitleFailed)
	default:
		return version.Name + " — " + version.Tagline
	}
}

// viewString builds the screen content exactly as before (hand-drawn boxes +
// local strings.Builder), dispatching by phase. The clickable RU/EN switcher is
// overlaid on the first content line by both branches via switcherLine.
func (m model) viewString() string {
	switch m.phase {
	case phaseRun:
		return m.runView()
	case phaseSummary:
		return m.summaryView()
	case phaseMatrix:
		return m.matrixView()
	case phaseDashboard:
		return m.dashboardView()
	case phaseWiki:
		return m.wikiView()
	case phaseKey:
		return m.keyView()
	default:
		return m.formView()
	}
}

// --- RU/EN language switcher ---------------------------------------------
//
// Geometry note: a content line is rendered as borderLeft + " " + content, so the
// content area starts at absolute column 2 (0-based). The switcher is drawn as the
// FIRST content line under the top border, i.e. screen row 1 (0-based). It is
// right-aligned within the innerW-wide content area.

const switcherRow = 1 // 0-based screen row of the first content line (under top border)

// switcherText returns the styled "RU | EN" with the active language highlighted,
// plus the plain (unstyled) RU and EN cell ranges relative to the start of the
// switcher text. ruLen/enLen are each 2 cells; sep " | " is 3 cells.
func (m model) switcherText() (styled string, ruStart, ruEnd, enStart, enEnd int) {
	on := focusStyle  // 213 focus pink — active language
	off := helpStyle  // 240 dim — inactive
	sep := helpStyle  // 240 dim separator
	ru, en := off, on // langEN active by default-branch below
	if m.lang == langRU {
		ru, en = on, off
	}
	styled = ru.Render("RU") + sep.Render(" | ") + en.Render("EN")
	ruStart, ruEnd = 0, 2 // "RU"
	enStart, enEnd = 5, 7 // after "RU" (2) + " | " (3)
	return styled, ruStart, ruEnd, enStart, enEnd
}

// switcherLine renders the first content line of a box: the RU/EN switcher
// right-aligned inside the innerW content area. It returns a full content line
// (already wrapped with borders) so callers emit it directly.
func (m model) switcherLine(b lipgloss.Border, innerW int) string {
	styled, _, _, _, _ := m.switcherText()
	const swWidth = 7 // "RU | EN"
	pad := max(innerW-swWidth, 0)
	content := strings.Repeat(" ", pad) + styled
	return contentLine(b, content, innerW)
}

// langZones computes the absolute on-screen cell ranges of the RU and EN labels
// for the current frame (pure function of m.w — layout is deterministic), so the
// mouse hit-test in Update matches exactly what View drew. Returns the row and the
// [start,end) X ranges for RU and EN.
func (m model) langZones() (row, ruX0, ruX1, enX0, enX1 int) {
	innerW := innerWidth(m.boxWidth())
	const swWidth = 7
	pad := max(innerW-swWidth, 0)
	// Absolute column where content begins: borderLeft(1) + space(1) = 2.
	base := 2 + pad
	_, ruS, ruE, enS, enE := m.switcherText()
	return switcherRow, base + ruS, base + ruE, base + enS, base + enE
}

// langAtClick returns which language label (if any) the click at (x,y) hit.
func (m model) langAtClick(x, y int) (Lang, bool) {
	row, ruX0, ruX1, enX0, enX1 := m.langZones()
	if y != row {
		return m.lang, false
	}
	switch {
	case x >= ruX0 && x < ruX1:
		return langRU, true
	case x >= enX0 && x < enX1:
		return langEN, true
	}
	return m.lang, false
}

// --- Form-phase mouse hit-test ------------------------------------------------
//
// All form click targets are resolved against the SAME ordered slice the renderer
// iterates (m.formRows): a row at slice index i renders at screen Y = formBodyTopRow
// + i, so the hit-test reproduces the exact geometry View drew. Framed inputs now
// occupy THREE consecutive slice entries (border-top / label+value / border-bottom),
// each tagged frInput with the same field — so a click on any of those three Y rows
// resolves to that input with no extra math. For per-pill targets (Mode/Start/Cancel)
// pillRanges computes the x-ranges with the same offsets the render path uses. The
// value/pill column begins at absolute X = 2 (frame: left border + space) + colW + 1
// (label column + one space).

// formHit is the resolved click target inside the form body.
type formHit struct {
	kind  formRowKind
	field int  // frInput: input index
	log   bool // frLog: true=on (save log), false=off
	pill  int  // frStart: 0=Connect, 1=Cancel
	ok    bool
}

// pillColStart is the absolute X where the value/pill column begins: 2 (frame) +
// colW + 1 (label column + one space).
func (m model) pillColStart() int { return 2 + m.labelColW() + 1 }

// formHitAtClick maps a click at (x,y) to a form element, iterating the same slice
// the renderer used. Returns ok=false when the click missed every target.
func (m model) formHitAtClick(x, y int) formHit {
	rows := m.formRows()
	idx := y - formBodyTopRow
	if idx < 0 || idx >= len(rows) {
		return formHit{}
	}
	r := rows[idx]
	switch r.kind {
	case frInput:
		// A whole input row is clickable (any X) to focus that field.
		return formHit{kind: frInput, field: r.field, ok: true}
	case frDisclosure:
		// The whole disclosure line is one click target (toggles advancedOpen).
		return formHit{kind: frDisclosure, ok: true}
	case frLog:
		names := []string{t(m.lang, kSaveLogOn), t(m.lang, kSaveLogOff)}
		if i := pillIndexAt(names, m.pillColStart(), x); i >= 0 {
			return formHit{kind: frLog, log: i == 0, ok: true} // pill 0 = on
		}
	case frStart:
		// Start + Cancel share this line; pillRanges uses the same labels the render
		// path rendered (startCancelLabels), so the hit ranges match exactly.
		if i := pillIndexAt(m.startCancelLabels(), m.pillColStart(), x); i >= 0 {
			return formHit{kind: frStart, pill: i, ok: true}
		}
	}
	return formHit{}
}

// pillIndexAt returns the index of the pill containing absolute X x (pills starting
// at startCol, geometry from pillRanges), or -1 if x is outside every pill.
func pillIndexAt(names []string, startCol, x int) int {
	for i, r := range pillRanges(names, startCol) {
		if x >= r[0] && x < r[1] {
			return i
		}
	}
	return -1
}

// --- Run-phase "Back to main" mouse hit-test ----------------------------------
//
// Mirrors runView's exact line emission order to derive the button's screen Y.
// runView emits (0-based screen rows):
//
//	row 0            : main box top border (titledTop)
//	row 1            : switcher line
//	row 2            : progress line
//	row 3            : blank spacer
//	rows 4..4+V-1    : V viewport lines (V = m.vp.Height())
//	then, when finished:
//	  rows ..        : finishedTailRows() completion-tail lines
//	  row backRow    : the pillOn "Back to main" button  ← target
//	...              : hints, borders, monitor box
//
// So backRow = 4 + V + finishedTailRows().
func (m model) backToMainRow() int {
	return 4 + m.vp.Height() + m.finishedTailRows()
}

// backToMainAtClick reports whether the click at (x,y) hit the "Back to main"
// button (only shown when finished). X spans the rendered button width starting at
// the content column (absolute X = 2: left border + space).
func (m model) backToMainAtClick(x, y int) bool {
	if !m.finished {
		return false
	}
	if y != m.backToMainRow() {
		return false
	}
	const contentX0 = 2 // borderLeft(1) + space(1)
	w := lipgloss.Width(t(m.lang, kBackToMain))
	return x >= contentX0 && x < contentX0+w
}

// formClick applies a form-phase click: focus an input, flip a Mode/Action pill,
// press Start, or quit via Cancel. It reuses the SAME state transitions as the
// keyboard handlers (refocus / start / the detect→Mode focus guard) so mouse and
// key paths stay consistent.
func (m model) formClick(x, y int) (tea.Model, tea.Cmd) {
	hit := m.formHitAtClick(x, y)
	if !hit.ok {
		return m, nil
	}
	switch hit.kind {
	case frInput:
		m.focus = hit.field
		return m, m.refocus()
	case frDisclosure:
		m.advancedOpen = !m.advancedOpen
		m.focus = rowDisclosure
		return m, nil
	case frLog:
		m.saveLog = hit.log
		m.focus = rowLog
		return m, nil
	case frStart:
		if hit.pill == 1 { // Cancel
			return m, tea.Quit
		}
		m.focus = rowStart
		// The landing "Подключиться" button is READ-ONLY: it runs the audit
		// (dial → detect → tweaks audit → Dashboard), never the apply path.
		m.command = "audit"
		return m.start()
	}
	return m, nil
}

// formRowKind tags each entry in the ordered formRows slice so the hit-test can
// map a click to the right element while iterating the SAME slice the renderer does.
type formRowKind int

const (
	frInput       formRowKind = iota // a text-input row; field holds the input index
	frBlank                          // spacer line (kept in the slice so Y math stays exact)
	frMode                           // soft/strict pill row
	frAction                         // run/detect/verify pill row
	frLog                            // save-log-to-file on/off pill row
	frHelp                           // contextual toggle-help line
	frStart                          // Start + Cancel button line
	frErr                            // validation error line
	frDisclosure                     // "▸ Дополнительно" toggle revealing Port/User/Key
	frCatalogLink                    // "Что настраивает программа ▸" label (P5 nav stub)
	frVersion                        // version-frame line (titled top / tagline / bottom)
)

// formRow is one rendered body line plus its kind (+ field index for inputs). The
// formRows slice is the single source of truth for BOTH the renderer (formView)
// and the mouse hit-test (formRowAtClick): a row's screen Y is fixed by its index
// in this slice, so the two cannot diverge when the Mode row is hidden/shown.
type formRow struct {
	kind  formRowKind
	field int    // valid only for frInput
	text  string // the rendered content line (without borders)
}

// formBodyTopRow is the 0-based screen Y of the FIRST form body line: top border
// (row 0) + switcher (row 1) → body starts at row 2. A body row at slice index i
// renders at screen Y = formBodyTopRow + i. Each framed input now contributes THREE
// consecutive slice entries (border-top / label+value / border-bottom), all tagged
// frInput with the same field, so the hit-test maps any of those three Y's to that
// input. The disclosure/version frame rows shift this slice but not this constant.
const formBodyTopRow = 2

// framedInputWidth is the inner box width (between the border runes) of a framed
// input: the shared label column + one space + the textinput visible width (44),
// clamped so the whole 3-row frame (border adds 2 cells) never exceeds the box
// content width innerW. All widths are display cells (lipgloss.Width).
func (m model) framedInputWidth() int {
	innerW := innerWidth(m.boxWidth())
	want := m.labelColW() + 1 + 44
	if max := innerW - 2; want > max { // -2 for the left+right border cells
		want = max
	}
	if want < 1 {
		want = 1
	}
	return want
}

// framedInputRow renders ONE landing input as a 3-line rounded-border box:
//
//	line 0: ╭───────────────╮         (top border)
//	line 1: │ Label  value… │         (left border + label + space + input.View + right)
//	line 2: ╰───────────────╯         (bottom border)
//
// Unfocused → dim border (240); focused → accent border (57) + bold label (213).
// Every line's display width equals the frame outer width (inner + 2) so the
// caller (formRows → contentLine) pads/truncates to innerW without breaking the
// frame. All width math is via lipgloss.Width (Cyrillic-safe), never %-*s.
func (m model) framedInputRow(idx int, lang Lang, label string, input textinput.Model, focused bool) []string {
	bd := lipgloss.RoundedBorder()
	bs := inputBorderDim
	if focused {
		bs = inputBorderFocus
	}
	inner := m.framedInputWidth()
	colW := m.labelColW()

	top := bs.Render(bd.TopLeft + strings.Repeat(bd.Top, inner) + bd.TopRight)
	bottom := bs.Render(bd.BottomLeft + strings.Repeat(bd.Bottom, inner) + bd.BottomRight)

	ls := labelStyle
	if focused {
		ls = focusStyle
	}
	// content = " " + label(colW) + " " + input.View(), truncated/padded to inner.
	content := " " + ls.Render(padLabel(label, colW)) + " " + input.View()
	content = truncDisplay(content, inner)
	if pad := inner - lipgloss.Width(content); pad > 0 {
		content += strings.Repeat(" ", pad)
	}
	mid := bs.Render(bd.Left) + content + bs.Render(bd.Right)

	return []string{top, mid, bottom}
}

// versionFrame renders the landing version sub-box as three content lines (titled
// top "─ Morgward vX.Y ─", a tagline line, bottom border), each sized to fit the
// box content width innerW. It is emitted as the first rows of formRows so the
// switcher/formBodyTopRow geometry is unchanged and the hit-test stays aligned.
func (m model) versionFrame(innerW int) []string {
	bd := lipgloss.RoundedBorder()
	// Inner frame width: full content width, but bounded so titledTop/borderLine
	// (which clamp to minBoxWidth) never draw wider than the content area.
	fw := innerW
	if fw < minBoxWidth {
		fw = minBoxWidth
	}
	finner := fw - 2 // cells between the frame's border runes
	title := " " + version.Name + " v" + version.Version + " "
	top := titledTop(bd, title, fw)
	// Middle line: left border + " "+tagline padded to (finner) + right border, so
	// the line is exactly fw cells (2 border + finner content).
	tagline := " " + labelStyle.Render(t(m.lang, kVersionTagline))
	tagline = truncDisplay(tagline, finner)
	if pad := finner - lipgloss.Width(tagline); pad > 0 {
		tagline += strings.Repeat(" ", pad)
	}
	mid := borderStyle.Render(bd.Left) + tagline + borderStyle.Render(bd.Right)
	bottom := borderLine(bd.BottomLeft, bd.Bottom, bd.BottomRight, fw)
	return []string{top, mid, bottom}
}

// formRows builds the ordered form body as a slice of row specs (INCLUDING blank
// rows) in exact render order. formView renders by iterating this; the hit-test
// iterates the same slice, so geometry can never drift.
func (m model) formRows() []formRow {
	colW := m.labelColW()
	// indent aligns the toggle/help/button content to the shared value column
	// (col colW+1), the same left edge the framed input labels use.
	indent := strings.Repeat(" ", colW+1)

	var rows []formRow
	// Version sub-frame (name + tagline) at the very top of the body, so the
	// switcher/formBodyTopRow geometry stays fixed and the hit-test slice still
	// includes every rendered line.
	for _, ln := range m.versionFrame(innerWidth(m.boxWidth())) {
		rows = append(rows, formRow{kind: frVersion, text: ln})
	}
	rows = append(rows, formRow{kind: frBlank})

	labels := []stringKey{kLabelHost, kLabelPort, kLabelUser, kLabelPassword, kLabelKey}
	// appendFramedInput emits the 3 rows of one framed input; all three carry the
	// same field index so a click on any of them focuses that input (the hit-test
	// maps the whole 3-row block to one target).
	appendFramedInput := func(i int) {
		framed := m.framedInputRow(i, m.lang, t(m.lang, labels[i]), m.inputs[i], i == m.focus)
		for _, ln := range framed {
			rows = append(rows, formRow{kind: frInput, field: i, text: ln})
		}
	}
	// Novice default: Host + Password only. The disclosure toggle reveals the
	// advanced Port/User/SSH-key inputs.
	appendFramedInput(fHost)
	appendFramedInput(fPass)
	if m.advancedOpen {
		appendFramedInput(fPort)
		appendFramedInput(fUser)
		appendFramedInput(fKey)
	}

	// "▸ Дополнительно" disclosure toggle: clicking/▶ toggles m.advancedOpen. The
	// caret reflects state (▸ closed, ▼ open). Focusable (rowDisclosure) and aligned
	// to the value column.
	disLabel := t(m.lang, kDisclosureLabel)
	if m.advancedOpen {
		// swap the leading "▸" for the open glyph "▼"
		disLabel = t(m.lang, kDisclosureOpen) + strings.TrimPrefix(disLabel, "▸")
	}
	disStyle := tipStyle
	if m.focus == rowDisclosure {
		disStyle = focusStyle
	}
	rows = append(rows, formRow{kind: frDisclosure, text: indent + disStyle.Render(disLabel)})

	rows = append(rows, formRow{kind: frBlank})
	// The soft/strict Mode selector and the run/detect/verify action selector are
	// intentionally NOT shown on the landing form: m.mode stays config.ModeSoft and
	// m.command stays "run" (engine tokens are unaffected). Access lockdown moves to
	// the Security menu in a later phase.
	// Save-log-to-file toggle: a session preference. Writes the full run log to
	// cfg.LogFile when on, off by default. Canonical tokens on/off; localized
	// yes/no pill names.
	rows = append(rows, formRow{kind: frLog, text: renderToggle(t(m.lang, kSaveLogLabel),
		[]string{"on", "off"},
		[]string{t(m.lang, kSaveLogOn), t(m.lang, kSaveLogOff)},
		saveLogToken(m.saveLog), m.focus == rowLog, colW)})
	rows = append(rows, formRow{kind: frBlank})

	// Start + Cancel buttons on one line, aligned to the value column (col colW+1).
	// Start uses pillOn when focused; Cancel always uses the dim pill (clickable, not
	// focusable). Both pill labels are wrapped by pillStyle/pillOnStyle, so their
	// x-geometry is recovered by pillRanges over the same names in the zone mapper.
	rows = append(rows, formRow{kind: frStart, text: indent + m.startCancelPills()})

	// "Что настраивает программа ▸" — a static catalog-link label (P5 will wire it to
	// phaseCatalog navigation). Rendered as a label, NOT a clickable pill, so the
	// hit-test has no case for it and a click is a no-op until P5.
	rows = append(rows, formRow{kind: frCatalogLink, text: indent + tipStyle.Render(t(m.lang, kCatalogLink))})

	if m.errMsg != "" {
		rows = append(rows, formRow{kind: frBlank})
		rows = append(rows, formRow{kind: frErr, text: indent + errStyle.Render("✗ "+m.errMsg)})
	}
	return rows
}

// startCancelLabels returns the two pill display names (Connect, Cancel) — the single
// source the render path and pillRanges/the hit-test both consume. The Connect name
// carries the focus caret/leading space so its rendered width matches what the user
// clicks; Cancel is a plain padded label.
func (m model) startCancelLabels() []string {
	start := "  " + t(m.lang, kStart) + "  "
	if m.focus == rowStart {
		start = "▶" + start
	} else {
		start = " " + start
	}
	return []string{start, t(m.lang, kCancel)}
}

// startCancelPills renders the Connect + Cancel button line (pills joined by one
// space), matching the geometry pillRanges assumes.
func (m model) startCancelPills() string {
	names := m.startCancelLabels()
	startPill := pillStyle.Render(names[0])
	if m.focus == rowStart {
		startPill = pillOnStyle.Render(names[0])
	}
	cancelPill := pillStyle.Render(names[1])
	return startPill + " " + cancelPill
}

// formView builds the form-phase screen content (was View()'s body).
func (m model) formView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	bd := lipgloss.RoundedBorder()
	var out strings.Builder
	// Plain outer top border: the program name + version now lives in the inner
	// version frame (see versionFrame), so the outer box stays untitled per the
	// landing mockup.
	out.WriteString(borderLine(bd.TopLeft, bd.Top, bd.TopRight, bw) + "\n")

	// First content line (screen row 1): the RU/EN switcher, right-aligned. Drawn
	// before the form body so the click hit-test row (switcherRow=1) always matches.
	out.WriteString(m.switcherLine(bd, innerW) + "\n")

	// Main form content: iterate the SAME ordered slice the hit-test uses, so the
	// rendered Y of each row equals formBodyTopRow + its index.
	rows := m.formRows()
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = r.text
	}
	for _, line := range lines {
		out.WriteString(contentLine(bd, line, innerW) + "\n")
	}

	// Then pad the vertical space, THEN the control hint as the last content line
	// directly above the bottom border, pinning it to the bottom of the window.
	// Layout budget: 1 top border + 1 switcher + len(lines) content + pad + 1 hint
	// + 1 bottom border = m.h. So pad = m.h − 4 − len(lines), clamped ≥0 (maxi
	// guard) so when m.h is unset/too small we simply emit content then hint.
	hint := helpStyle.Render(t(m.lang, kFormHint))
	pad := maxi(m.h-4-len(lines), 0)
	for range pad {
		out.WriteString(contentLine(bd, "", innerW) + "\n")
	}
	out.WriteString(contentLine(bd, hint, innerW) + "\n")
	out.WriteString(borderLine(bd.BottomLeft, bd.Bottom, bd.BottomRight, bw))
	return out.String()
}

// renderToggle draws a labelled pill row. opts are the canonical (engine) tokens
// used for selection-matching against cur; names are the localized display
// strings shown in the pills (same order/length as opts). colW is the shared
// label-column width (see labelColW); the pills start at col colW+1.
func renderToggle(label string, opts, names []string, cur string, focused bool, colW int) string {
	s := labelStyle
	if focused {
		s = focusStyle
	}
	lbl := s.Render(padLabel(label, colW)) // same label column as the inputs
	var pills []string
	for i, o := range opts {
		name := o
		if i < len(names) {
			name = names[i]
		}
		if o == cur {
			pills = append(pills, pillOnStyle.Render(name)) // selected: accent bg 57
		} else {
			pills = append(pills, pillStyle.Render(name)) // unselected: dim
		}
	}
	// One space after the label (→ col colW+1, same as input values) and an even
	// single space between pills.
	return lbl + " " + strings.Join(pills, " ")
}

// pillRanges is the SINGLE source of pill x-geometry, used by BOTH the render path
// (renderToggle / the Start–Cancel line, indirectly via the same layout: label +
// space + pills joined by single spaces) and the mouse hit-test zone mappers, so
// they cannot drift. Given the localized pill display names and the absolute column
// where the first pill begins (startCol), it returns the absolute [start,end) X
// range of each pill. Accounts for pillStyle/pillOnStyle's Padding(0,1) (= +2 cells
// per pill, one each side) and the single-space separator between pills. Widths are
// display cells (lipgloss.Width), so multibyte localized names stay aligned.
func pillRanges(names []string, startCol int) [][2]int {
	const pad = 2 // Padding(0,1): one cell left + one cell right
	ranges := make([][2]int, len(names))
	x := startCol
	for i, n := range names {
		w := lipgloss.Width(n) + pad
		ranges[i] = [2]int{x, x + w}
		x += w + 1 // + single-space separator before the next pill
	}
	return ranges
}

// minBoxWidth clamps the box width so the hand-drawn border never goes negative.
const minBoxWidth = 40

// boxWidth is the outer width of both boxes (the full terminal width, clamped).
func (m model) boxWidth() int { return maxi(m.w, minBoxWidth) }

// innerWidth is the content width inside a box: total − 2 border − 2 padding.
func innerWidth(w int) int {
	if w < minBoxWidth {
		w = minBoxWidth
	}
	return w - 4
}

// runView hand-draws the titled main box (progress + viewport + hints) and the
// bottom monitor box, sized to the terminal and budgeted so nothing overflows.
func (m model) runView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	var sb strings.Builder

	// --- MAIN BOX ---
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')

	// First content line (screen row 1): the RU/EN switcher, right-aligned — so the
	// language is switchable during a run too, and the click hit-test row matches.
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// Progress / summary line.
	sb.WriteString(contentLine(b, m.progressLine(innerW), innerW))
	sb.WriteByte('\n')

	// Blank spacer line.
	sb.WriteString(contentLine(b, "", innerW))
	sb.WriteByte('\n')

	// Viewport (server output). Each rendered line padded/truncated to innerW.
	for ln := range strings.SplitSeq(m.vp.View(), "\n") {
		sb.WriteString(contentLine(b, ln, innerW))
		sb.WriteByte('\n')
	}

	// When finished, the localized completion tail rendered fresh from m.lang each
	// frame (NOT baked into m.content) so a post-finish language switch re-renders
	// it, then an explicit highlighted "Back to main" button above the hints.
	if m.finished {
		for ln := range strings.SplitSeq(m.finishedTail(), "\n") {
			sb.WriteString(contentLine(b, ln, innerW))
			sb.WriteByte('\n')
		}
		sb.WriteString(contentLine(b, pillOnStyle.Render(t(m.lang, kBackToMain)), innerW))
		sb.WriteByte('\n')
	}

	// Contextual hints.
	sb.WriteString(contentLine(b, helpStyle.Render(m.runHints()), innerW))
	sb.WriteByte('\n')

	// Main box bottom border.
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	// --- MONITOR BOX (bottom-most) --- shared with the summary + wiki screens.
	sb.WriteString(m.monitorBox(innerW))

	return sb.String()
}

// monitorBox renders the bottom-most live monitor box (title + the full-width
// CPU/RAM/DISK footer): top border, one content line, bottom border. Shared by the
// run, summary, and wiki screens (every post-connect screen keeps the footer alive).
// No trailing newline — it is the last block on the screen.
func (m model) monitorBox(innerW int) string {
	bw := m.boxWidth()
	b := lipgloss.RoundedBorder()
	var sb strings.Builder
	sb.WriteString(titledTop(b, t(m.lang, kMonTitle), bw))
	sb.WriteByte('\n')
	sb.WriteString(contentLine(b, m.renderMonitor(innerW), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	return sb.String()
}

// summaryBodyTopRow is the 0-based screen Y of the FIRST summary body line:
// top border (row 0) + switcher (row 1) → body starts at row 2. Mirrors
// formBodyTopRow; the fix-list hit-test derives each fix row's Y from this.
const summaryBodyTopRow = 2

// fixRowStyle renders a clickable fix-list row; a small status glyph + "[ID] title".
var (
	sumHeadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true) // section headers
	sumOKStyle   = monGreenStyle                                                    // OK glyph
	sumFailStyle = monRedStyle                                                      // FAIL glyph
	sumSkipStyle = monYellowStyle                                                   // SKIP glyph
)

// humanKB64 renders an int64 KB value via the float helper (engine snapshots use
// int64; the monitor footer uses float64). Empty string when total is unknown is
// handled by the caller — this always returns a value.
func humanKB64(kb int64) string { return humanKB(float64(kb)) }

// sumRowIndent is the leading indent of every metric row under a group header, and
// sumRowGap is the spacing between the (padded) label column and the value column.
const (
	sumRowIndent = "  "
	sumRowGap    = "  "
)

// sumOldStyle dims the pre-run ("old") value of a before→after pair so the eye lands
// on the new value; the arrow stays plain. Reuses the monitor's dim color (240).
var sumOldStyle = monDimStyle

// sumValue renders the value column of a metric row with the same suppression rules
// as the engine's formatDelta: "" when both sides are empty (caller drops the row),
// a lone value when only one side is known or both are equal, and a dimmed
// "old → new" pair when both are known and differ.
func sumValue(before, after string) string {
	switch {
	case before == "" && after == "":
		return ""
	case before == "":
		return after
	case after == "":
		return before
	case before == after:
		return before
	default:
		return sumOldStyle.Render(before) + " → " + after
	}
}

// sumRow lays out one aligned metric row: indent + label padded to labelW display
// cells + gap + value. The label pad is clamped so the value column never starts past
// innerW, and the whole line is truncated as a final guard so it can never overflow
// the box. Returns "" when value is empty so the caller skips the row.
func sumRow(label, value string, labelW, innerW int) string {
	if value == "" {
		return ""
	}
	// Clamp the label column so indent + label + gap + value fits innerW; never pad
	// the label narrower than its own text.
	rawW := lipgloss.Width(label)
	maxLabelW := innerW - lipgloss.Width(sumRowIndent) - lipgloss.Width(sumRowGap) - lipgloss.Width(value)
	if labelW > maxLabelW {
		labelW = maxLabelW
	}
	if labelW < rawW {
		labelW = rawW
	}
	line := sumRowIndent + padLabel(label, labelW) + sumRowGap + value
	return truncDisplay(line, innerW)
}

// summaryHeader builds the localized one-line tally:
// "applied X/Y · N skipped · N failed · reboots N · verify P/T".
func (m model) summaryHeader() string {
	s := m.summary
	verifyTotal := s.VerifyPassed + s.VerifyFailed
	return fmt.Sprintf("%s · %s · %s · %s · %s",
		fmt.Sprintf(t(m.lang, kSumApplied), s.Applied(), s.Total()),
		fmt.Sprintf(t(m.lang, kSumSkipped), s.Skip),
		fmt.Sprintf(t(m.lang, kSumFailed), s.Fail),
		fmt.Sprintf(t(m.lang, kSumReboots), s.Reboots),
		fmt.Sprintf(t(m.lang, kSumVerify), s.VerifyPassed, verifyTotal),
	)
}

// boolWordL renders a posture bool as the localized yes/no token.
func (m model) boolWordL(v bool) string {
	if v {
		return t(m.lang, kYesWord)
	}
	return t(m.lang, kNoWord)
}

// summaryStatLines builds the four before/after metric blocks (packages/kernel,
// disk/memory, network, security). Both snapshots may be nil — when both are nil
// it returns nil so summaryView shows only the header + fix list. Mirrors the
// engine's statsLines suppression: rows with unknown data are dropped.
func (m model) summaryStatLines(innerW int) []string {
	s := m.summary
	if s.Before == nil && s.After == nil {
		return nil
	}
	b, a := s.Before, s.After
	empty := stats0()
	if b == nil {
		b = empty
	}
	if a == nil {
		a = empty
	}

	// statRow is a label + already-rendered value column; empty values are dropped.
	type statRow struct{ label, value string }

	// emitGroup renders a header followed by its rows, aligning every row's value to
	// the group's widest label (display cells, Cyrillic-aware). A group with no
	// non-empty rows still emits its header (mirrors the prior always-on sections);
	// the network block decides on its own whether to call this at all.
	var out []string
	emitGroup := func(headK stringKey, rows []statRow) {
		if len(out) > 0 {
			out = append(out, "") // one blank line between groups
		}
		out = append(out, sumHeadStyle.Render(t(m.lang, headK)))
		labelW := 0
		for _, r := range rows {
			if r.value == "" {
				continue
			}
			if w := lipgloss.Width(r.label); w > labelW {
				labelW = w
			}
		}
		for _, r := range rows {
			if line := sumRow(r.label, r.value, labelW, innerW); line != "" {
				out = append(out, line)
			}
		}
	}

	// ПАКЕТЫ И ЯДРО.
	var pkg []statRow
	if s.UpgradedPkgs > 0 {
		pkg = append(pkg, statRow{t(m.lang, kRowUpgraded), strconv.Itoa(s.UpgradedPkgs)})
	}
	pkg = append(pkg, statRow{t(m.lang, kRowKernel), sumValue(b.KernelVer, a.KernelVer)})
	if s.PurgedPkgs > 0 {
		pkg = append(pkg, statRow{t(m.lang, kRowPurged), strconv.Itoa(s.PurgedPkgs)})
	}
	emitGroup(kSecPkgKernel, pkg)

	// ДИСК И ПАМЯТЬ.
	disk := []statRow{{t(m.lang, kRowDiskUsed), sumValue(sumDiskStr(b), sumDiskStr(a))}}
	if !b.ZramActive && a.ZramActive {
		disk = append(disk, statRow{t(m.lang, kRowZram), t(m.lang, kZramAdded)})
	}
	emitGroup(kSecDiskMem, disk)

	// СЕТЬ (whole block dropped when there is no speed/ping data on either side).
	var net []statRow
	if b.SpeedMBs > 0 || a.SpeedMBs > 0 {
		net = append(net, statRow{t(m.lang, kRowSpeed), sumValue(sumSpeedStr(b.SpeedMBs), sumSpeedStr(a.SpeedMBs))})
	}
	if b.GatewayPingMs > 0 || a.GatewayPingMs > 0 {
		net = append(net, statRow{t(m.lang, kRowPingGW), sumValue(sumSpeedStr(b.GatewayPingMs), sumSpeedStr(a.GatewayPingMs))})
	}
	if b.InternetPingMs > 0 || a.InternetPingMs > 0 {
		net = append(net, statRow{t(m.lang, kRowPingNet), sumValue(sumSpeedStr(b.InternetPingMs), sumSpeedStr(a.InternetPingMs))})
	}
	if len(net) > 0 {
		emitGroup(kSecNetwork, net)
	}

	// БЕЗОПАСНОСТЬ.
	sec := []statRow{
		{t(m.lang, kRowPorts), sumValue(sumPortsStr(b.OpenPorts), sumPortsStr(a.OpenPorts))},
		{t(m.lang, kRowRootLogin), sumValue(b.RootLogin, a.RootLogin)},
		{t(m.lang, kRowKeyOnly), sumValue(m.boolWordL(b.KeyOnly), m.boolWordL(a.KeyOnly))},
		{t(m.lang, kRowFirewall), sumValue(m.boolWordL(b.FirewallActive), m.boolWordL(a.FirewallActive))},
		{t(m.lang, kRowFail2ban), sumValue(m.boolWordL(b.Fail2banActive), m.boolWordL(a.Fail2banActive))},
	}
	emitGroup(kSecSecurity, sec)

	return out
}

// fixListLines builds one rendered line per applied fix in m.summary.Results order:
// "<glyph> [ID] <localized title>". The slice index equals the fix's index in
// Results, so fixAtClick can recover each row's Y deterministically.
func (m model) fixListLines() []string {
	out := make([]string, 0, len(m.summary.Results))
	for _, r := range m.summary.Results {
		out = append(out, "  "+fixGlyph(r.Status)+" "+m.fixRowText(r))
	}
	return out
}

// fixRowText is the (unstyled-glyph) "[ID] title" portion of a fix row: the wiki
// doc title when present, else the localized short step title, else the engine Title.
func (m model) fixRowText(r engine.StepResult) string {
	var title string
	if d, ok := wiki.Doc(wiki.Lang(int(m.lang)), r.ID); ok && d.Title != "" {
		title = d.Title
	} else {
		title = localStepTitle(m.lang, r.ID, r.Title)
	}
	return fmt.Sprintf("[%s] %s", r.ID, title)
}

// fixGlyph returns a small colored status marker for a fix row.
func fixGlyph(st steps.Status) string {
	switch st {
	case steps.StatusOK:
		return sumOKStyle.Render("✓")
	case steps.StatusFail:
		return sumFailStyle.Render("✗")
	case steps.StatusSkip:
		return sumSkipStyle.Render("∅")
	default:
		return " "
	}
}

// summaryView renders the post-finish stats summary + clickable fix list inside the
// same bordered frame as runView. Layout (0-based screen rows):
//
//	row 0                 : main box top border
//	row 1                 : RU/EN switcher
//	rows 2..2+viewH-1     : the scrollable middle region — header, blank, stat blocks,
//	                        blank, fix-list header, then one clickable row per fix,
//	                        windowed to body[sumScroll : sumScroll+viewH]
//	...                   : hint, bottom border, then the 3-row monitor box (pinned)
//
// The middle region always emits exactly viewH rows (blank-padded when the body is
// shorter), so the monitor footer is ALWAYS pinned to the bottom regardless of the
// terminal size. When the body overflows viewH a scrollbar is drawn in the right
// border (renderScrollRegion) and the wheel / ↑↓ scroll it; fixAtClick reproduces the
// windowed geometry so clicks stay accurate.
func (m model) summaryView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body := m.summaryBodyLines() // header + blocks + fix-list header + fix rows

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// Scrollable middle region (the only resizable part); the chrome above (2 rows)
	// and below (hint + bottom + 3-row monitor) is fixed, so the footer never moves.
	viewH := m.bodyViewH()
	off := clampScroll(m.sumScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	// Hint + main box bottom border.
	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, kSummaryHint)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	// Monitor box (kept alive — sampler still running on the summary screen).
	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// matrixView renders phaseMatrix: the scrollable анализ table with the monitor
// footer, mirroring summaryView's chrome exactly (same outer titled box, switcher
// line, scroll region, hint line, bottom border, and monitor footer).
func (m model) matrixView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body := m.matrixBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	viewH := m.bodyViewH()
	off := clampScroll(m.matScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, kMatrixHint)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// --- Dashboard (phaseDashboard) -----------------------------------------------
//
// dashboardView renders the post-connect Dashboard: a framed server card
// (OS/kernel/RAM/disk/ports/IPv6 from m.dashFacts + the live monitor sample), the
// live tweak-audit status line + a ✓/• grid (from m.dashAuditResults), and three
// action button pills. The monitor footer is pinned at the bottom. Chrome (titled
// top, switcher, scroll region, hint, bottom border, monitor box) mirrors
// summaryView/matrixView exactly so the footer never moves and the body scrolls.
//
// The clickable rows (audit grid rows → wiki detail, the three button pills) are
// resolved against the SAME ordered body slice the renderer iterates
// (dashBodyLines), so the hit-test geometry can never drift.
func (m model) dashboardView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body := m.dashBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	viewH := m.bodyViewH()
	off := clampScroll(m.dashScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	hint := t(m.lang, kDashHint)
	if m.dashApplyConfirm {
		hint = t(m.lang, kDashApplyConfirm)
	}
	sb.WriteString(contentLine(b, helpStyle.Render(hint), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// dashButtonNames is the ordered list of the three Dashboard action-button labels
// (Apply / Security / Catalog). It is the SINGLE source consumed by both the
// render path (dashButtonsLine) and the hit-test (dashButtonAtClick), so their
// x-geometry cannot diverge.
func (m model) dashButtonNames() []string {
	return []string{
		t(m.lang, kDashApplyButton),
		t(m.lang, kDashSecButton),
		t(m.lang, kDashCatalogButton),
	}
}

// dashButtonStartCol is the absolute X where the first button pill begins on the
// buttons row: 2 (left border + space) + 1 (the leading indent space in the row).
const dashButtonStartCol = 3

// dashButtonsLine renders the three action pills joined by a single space, with a
// one-space indent so the first pill begins at dashButtonStartCol. All three use
// the dim pill style; pillRanges over dashButtonNames recovers their x-geometry.
func (m model) dashButtonsLine() string {
	names := m.dashButtonNames()
	pills := make([]string, len(names))
	for i, n := range names {
		pills[i] = pillStyle.Render(n)
	}
	return " " + strings.Join(pills, " ")
}

// dashBodyLines builds the ordered Dashboard body slice — the single source of
// truth for BOTH dashboardView's render and the hit-tests (dashAuditRowAtClick /
// dashButtonRowIndex). Order: server card (framed), blank, audit status line,
// blank, audit grid rows (one per result), blank, buttons line. Every width uses
// lipgloss.Width.
func (m model) dashBodyLines(innerW int) []string {
	var body []string
	body = append(body, m.dashServerCard(innerW)...)
	body = append(body, "")

	// Live audit status line: "Анализ твиков ⠹  применено N из M · можно применить K".
	applied, total := m.dashAuditApplied, m.dashAuditTotal
	canApply := total - applied
	if canApply < 0 {
		canApply = 0
	}
	label := t(m.lang, kDashAuditLabel)
	if m.dashAuditRunning && !m.dashAuditDone {
		label += " " + string(spinnerFrames[m.spin%len(spinnerFrames)])
	}
	status := fmt.Sprintf("%s  %s · %s",
		label,
		fmt.Sprintf(t(m.lang, kDashAuditStatus), applied, total),
		fmt.Sprintf(t(m.lang, kDashCanApply), canApply),
	)
	body = append(body, truncDisplay(status, innerW))
	body = append(body, "")

	// Audit grid: one row per result, "  ✓/•  name". The rows are the clickable
	// tail used by dashAuditRowAtClick (each maps to its Probe.ID → wiki detail).
	okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	canStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	for _, r := range m.dashAuditResults {
		glyph := canStyle.Render("•")
		if r.Applied {
			glyph = okStyle.Render("✓")
		}
		name := localTweakName(m.lang, r.Probe.ID, r.Probe.Name)
		body = append(body, truncDisplay("  "+glyph+" "+name, innerW))
	}

	body = append(body, "")
	body = append(body, m.dashButtonsLine())
	return body
}

// dashServerCard renders the framed "Сервер: HOST" card with the OS/kernel/RAM/
// disk/ports/IPv6 facts (from m.dashFacts + the live monitor sample), as content
// lines fitted to innerW. Missing facts are omitted, never rendered as blanks.
func (m model) dashServerCard(innerW int) []string {
	bd := lipgloss.RoundedBorder()
	fw := innerW
	if fw < minBoxWidth {
		fw = minBoxWidth
	}
	finner := fw - 2 // cells between the card's border runes

	title := " " + t(m.lang, kDashTitle) + ": " + m.host + " "
	top := titledTop(bd, title, fw)
	bottom := borderLine(bd.BottomLeft, bd.Bottom, bd.BottomRight, fw)

	// Build the facts line(s) from whatever is known. Order mirrors the mockup.
	var parts []string
	f := m.dashFacts
	if f != nil {
		if f.ID != "" {
			osStr := f.ID
			if f.VersionID != "" {
				osStr += " " + f.VersionID
			}
			parts = append(parts, t(m.lang, kDashOS)+" "+osStr)
		}
		if f.HasIPv6 {
			parts = append(parts, t(m.lang, kDashIPv6)+" "+m.boolWordL(true))
		}
	}
	// RAM/disk from the live monitor sample (best-effort; the sampler fills them
	// once connected). The sample is the same source the footer uses.
	s := m.sample
	if m.haveSample {
		if s.RAMTotalKB > 0 {
			parts = append(parts, t(m.lang, kDashMemory)+" "+humanKB(s.RAMUsedKB)+"/"+humanKB(s.RAMTotalKB))
		}
		if s.DiskTotalKB > 0 {
			parts = append(parts, t(m.lang, kDashDisk)+" "+humanKB(s.DiskUsedKB)+"/"+humanKB(s.DiskTotalKB))
		}
	}

	mid := func(content string) string {
		content = " " + content
		content = truncDisplay(content, finner)
		if pad := finner - lipgloss.Width(content); pad > 0 {
			content += strings.Repeat(" ", pad)
		}
		return borderStyle.Render(bd.Left) + content + borderStyle.Render(bd.Right)
	}

	lines := []string{top}
	if len(parts) == 0 {
		lines = append(lines, mid(labelStyle.Render("…")))
	} else {
		lines = append(lines, mid(strings.Join(parts, "   ")))
	}
	lines = append(lines, bottom)
	return lines
}

// dashGridStartIndex is the body-slice index of the FIRST audit grid row. The body
// prefix is: server card (N lines) + blank + status + blank, so the grid begins at
// len(card)+3. dashButtonsIndex is the body index of the buttons row: grid start +
// number of results + 1 (the blank before the buttons line).
func (m model) dashGridStartIndex(innerW int) int {
	return len(m.dashServerCard(innerW)) + 3
}

func (m model) dashButtonsIndex(innerW int) int {
	return m.dashGridStartIndex(innerW) + len(m.dashAuditResults) + 1
}

// dashRowYToBodyIdx maps a screen Y to a Dashboard body-slice index, honoring the
// scroll offset, or returns ok=false when Y is in the chrome (switcher/hint/border/
// monitor) rather than the scrollable body region.
func (m model) dashRowYToBodyIdx(y int) (int, bool) {
	body := m.dashBodyLines(innerWidth(m.boxWidth()))
	viewH := m.bodyViewH()
	off := clampScroll(m.dashScroll, len(body), viewH)
	rowInRegion := y - summaryBodyTopRow
	if rowInRegion < 0 || rowInRegion >= viewH {
		return 0, false
	}
	idx := off + rowInRegion
	if idx < 0 || idx >= len(body) {
		return 0, false
	}
	return idx, true
}

// dashAuditRowAtClick maps a click at (x,y) to an audit result's Probe.ID (for the
// wiki detail), or ok=false on a miss. The grid rows are the contiguous block of
// len(results) lines starting at dashGridStartIndex.
func (m model) dashAuditRowAtClick(x, y int) (string, bool) {
	if m.phase != phaseDashboard || len(m.dashAuditResults) == 0 {
		return "", false
	}
	innerW := innerWidth(m.boxWidth())
	bodyIdx, ok := m.dashRowYToBodyIdx(y)
	if !ok {
		return "", false
	}
	gridStart := m.dashGridStartIndex(innerW)
	resIdx := bodyIdx - gridStart
	if resIdx < 0 || resIdx >= len(m.dashAuditResults) {
		return "", false
	}
	// X must fall within the rendered row width (rows are content from column 2).
	const contentX0 = 2
	r := m.dashAuditResults[resIdx]
	glyph := "•"
	if r.Applied {
		glyph = "✓"
	}
	row := "  " + glyph + " " + localTweakName(m.lang, r.Probe.ID, r.Probe.Name)
	w := lipgloss.Width(truncDisplay(row, innerW))
	if x >= contentX0 && x < contentX0+w {
		return r.Probe.ID, true
	}
	return "", false
}

// dashButton enumerates the three Dashboard actions resolved by dashButtonAtClick.
type dashButton int

const (
	dashBtnNone dashButton = iota
	dashBtnApply
	dashBtnSecurity
	dashBtnCatalog
)

// dashButtonAtClick maps a click at (x,y) to one of the three action buttons, using
// pillRanges over dashButtonNames (the same geometry dashButtonsLine renders), or
// dashBtnNone on a miss.
func (m model) dashButtonAtClick(x, y int) dashButton {
	if m.phase != phaseDashboard {
		return dashBtnNone
	}
	innerW := innerWidth(m.boxWidth())
	bodyIdx, ok := m.dashRowYToBodyIdx(y)
	if !ok || bodyIdx != m.dashButtonsIndex(innerW) {
		return dashBtnNone
	}
	switch pillIndexAt(m.dashButtonNames(), dashButtonStartCol, x) {
	case 0:
		return dashBtnApply
	case 1:
		return dashBtnSecurity
	case 2:
		return dashBtnCatalog
	}
	return dashBtnNone
}

// tweakBucketIDs is the canonical Tweaks-bucket step ID set applied by the
// Dashboard "Применить твики" action (everything EXCEPT the security/access steps
// A2/A2.5, which live behind the Security menu). selectSteps re-orders these into
// the load-bearing apply order, so the literal order here is not significant.
func tweakBucketIDs() []string {
	return []string{"A1", "A3", "A4", "A5", "A6", "A6.5", "A6.7", "A7", "A8", "A9", "A10"}
}

// bucketHasA8 reports whether the apply bucket includes A8 (full upgrade + reboot),
// which warrants the explicit reboot-warning confirm before launching.
func bucketHasA8(ids []string) bool {
	for _, id := range ids {
		if strings.EqualFold(id, "A8") {
			return true
		}
	}
	return false
}

// launchApplyTweaks starts the apply over the Tweaks-bucket IDs via the engine's
// "step" command (RunSteps, allowBrownfield=true). It reuses start() so the run
// streams into the same log view and lands on the summary on completion. The
// credentials are taken from the still-populated landing inputs.
func (m model) launchApplyTweaks() (tea.Model, tea.Cmd) {
	m.command = "step"
	m.cmds = tweakBucketIDs()
	return m.start()
}

// dashboardClick resolves a Dashboard click: an audit row opens its wiki detail; a
// button pill triggers its action. "Применить твики" first shows the A8 reboot
// warning (Enter to confirm). "Безопасность ▸" and "Каталог твиков" navigate to
// their phases — until those phases land (P4/P5) they are harmless no-op stubs.
func (m model) dashboardClick(x, y int) (tea.Model, tea.Cmd) {
	// A pending apply-confirm swallows clicks (use Enter/esc on the hint to resolve).
	if m.dashApplyConfirm {
		return m, nil
	}
	// Button pills take priority over the (overlapping-Y-impossible) audit grid.
	switch m.dashButtonAtClick(x, y) {
	case dashBtnApply:
		ids := tweakBucketIDs()
		if bucketHasA8(ids) {
			// Show the explicit reboot warning; the apply launches on Enter (see the
			// phaseDashboard key handler), not on this click.
			m.dashApplyConfirm = true
			return m, nil
		}
		return m.launchApplyTweaks()
	case dashBtnSecurity:
		// P4 stub: phaseSecurity is not built yet — harmless no-op. MUST NOT apply
		// anything. (Wired to navigate in P4.)
		return m, nil
	case dashBtnCatalog:
		// P5 stub: phaseCatalog is not built yet — harmless no-op nav.
		return m, nil
	}
	// Audit row → wiki detail for that tweak.
	if id, ok := m.dashAuditRowAtClick(x, y); ok {
		m.wikiStep = id
		m.wikiReturn = phaseDashboard
		m.wikiScroll = 0
		m.phase = phaseWiki
		return m, nil
	}
	return m, nil
}

// matrixBodyLines renders the анализ audit: results grouped by step, each row
// "  <name> <leaders> <status>" right-aligned to innerW. Mirrors summaryBodyLines.
func (m model) matrixBodyLines(innerW int) []string {
	res := m.summary.Tweaks
	if len(res) == 0 {
		return []string{t(m.lang, kMatrixHint)}
	}

	applied := 0
	for _, r := range res {
		if r.Applied {
			applied++
		}
	}

	okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	noStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	var lines []string
	lines = append(lines, fmt.Sprintf(t(m.lang, kTweakSummary), applied, len(res)-applied), "")

	var curStep string
	for _, r := range res {
		if r.Probe.Step != curStep {
			curStep = r.Probe.Step
			lines = append(lines, localStepTitle(m.lang, curStep, curStep))
		}
		name := localTweakName(m.lang, r.Probe.ID, r.Probe.Name)
		statusTxt := t(m.lang, kTweakNotApplied)
		style := noStyle
		if r.Applied {
			statusTxt = t(m.lang, kTweakApplied)
			style = okStyle
		}
		// "  name <spaces> status" padded to innerW display cells.
		left := "  " + name
		gap := innerW - lipgloss.Width(left) - lipgloss.Width(statusTxt)
		if gap < 1 {
			gap = 1
		}
		lines = append(lines, left+strings.Repeat(" ", gap)+style.Render(statusTxt))
	}
	return lines
}

// summaryBodyLines builds the ordered body line slice (the single source of truth
// for BOTH summaryView's render and fixAtClick's geometry): header, blank, stat
// blocks (possibly empty), blank, fix-list header, then one row per fix.
func (m model) summaryBodyLines() []string {
	var body []string
	body = append(body, m.summaryHeader())
	if blocks := m.summaryStatLines(innerWidth(m.boxWidth())); len(blocks) > 0 {
		body = append(body, "")
		body = append(body, blocks...)
	}
	if len(m.summary.Results) > 0 {
		body = append(body, "")
		body = append(body, sumHeadStyle.Render(t(m.lang, kSecFixes)))
		body = append(body, m.fixListLines()...)
	}
	return body
}

// fixAtClick maps a click at (x,y) to a fix-list row's step ID, accounting for the
// scroll offset. The middle region occupies screen rows [summaryBodyTopRow,
// summaryBodyTopRow+viewH); a click there maps to body index sumScroll+(y-top), and
// the fix rows are the tail of the body (after the header + stat blocks + fix-list
// header). X must fall within the rendered row width. Returns ok=false on a miss.
func (m model) fixAtClick(x, y int) (string, bool) {
	if m.phase != phaseSummary || len(m.summary.Results) == 0 {
		return "", false
	}
	body := m.summaryBodyLines()
	viewH := m.bodyViewH()
	off := clampScroll(m.sumScroll, len(body), viewH)

	rowInRegion := y - summaryBodyTopRow
	if rowInRegion < 0 || rowInRegion >= viewH {
		return "", false // click is in the chrome (switcher/hint/border/monitor), not the body
	}
	bodyIdx := off + rowInRegion
	fixStart := len(body) - len(m.summary.Results) // fix rows are the body tail
	idx := bodyIdx - fixStart
	if idx < 0 || idx >= len(m.summary.Results) {
		return "", false
	}
	const contentX0 = 2 // borderLeft(1) + space(1)
	row := "  " + fixGlyph(m.summary.Results[idx].Status) + " " + m.fixRowText(m.summary.Results[idx])
	w := lipgloss.Width(truncDisplay(row, innerWidth(m.boxWidth())))
	if x >= contentX0 && x < contentX0+w {
		return m.summary.Results[idx].ID, true
	}
	return "", false
}

// stats0 returns a pointer to a zero Snapshot, used when one side is nil so the
// delta helpers see "unknown" rather than dereferencing nil.
func stats0() *stats.Snapshot { return &stats.Snapshot{} }

func sumDiskStr(s *stats.Snapshot) string {
	if s.DiskTotalKB <= 0 {
		return ""
	}
	return humanKB64(s.DiskUsedKB) + "/" + humanKB64(s.DiskTotalKB)
}

func sumSpeedStr(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func sumPortsStr(p []string) string {
	if len(p) == 0 {
		return ""
	}
	return strconv.Itoa(len(p))
}

// wrap word-wraps s to at most w display cells per line (lipgloss.Width-aware so
// multibyte Cyrillic wraps correctly), returning the lines. A single word longer
// than w is hard-split. w<1 yields a single (unwrapped) line.
func wrap(s string, w int) []string {
	if w < 1 {
		return []string{s}
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		cur := ""
		for _, word := range words {
			// Hard-split a word that alone exceeds the width.
			for lipgloss.Width(word) > w {
				head := truncDisplay(word, w)
				if cur != "" {
					lines = append(lines, cur)
					cur = ""
				}
				lines = append(lines, head)
				word = word[len(head):]
			}
			switch {
			case cur == "":
				cur = word
			case lipgloss.Width(cur)+1+lipgloss.Width(word) <= w:
				cur += " " + word
			default:
				lines = append(lines, cur)
				cur = word
			}
		}
		if cur != "" {
			lines = append(lines, cur)
		}
	}
	return lines
}

// wikiView renders one fix's what/why/risk description inside the run frame: a
// "[ID] Title" header, then three word-wrapped labeled blocks (WHAT IT DOES / WHY /
// WITHOUT IT). On an unknown step it shows the localized no-description line. The
// monitor footer stays alive (sampler still running). Text reads m.lang every frame,
// so the RU|EN toggle re-renders the description in the other language.
func (m model) wikiView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body := m.wikiBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// Same fixed-chrome layout as summaryView: a scrollable middle region of exactly
	// bodyViewH rows (scrollbar drawn on overflow), then the hint + bottom border +
	// the 3-row monitor box, so the footer stays pinned at any terminal size.
	viewH := m.bodyViewH()
	off := clampScroll(m.wikiScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, kWikiHint)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')
	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// wikiBodyLines builds the wiki page body: the "[ID] Title" header then the three
// labeled, word-wrapped blocks. Falls back to the localized no-description line when
// the step has no wiki entry.
func (m model) wikiBodyLines(innerW int) []string {
	doc, ok := wiki.Doc(wiki.Lang(int(m.lang)), m.wikiStep)
	if !ok {
		return []string{sumHeadStyle.Render("[" + m.wikiStep + "]"), "", t(m.lang, kWikiNoDoc)}
	}
	var body []string
	body = append(body, sumHeadStyle.Render(fmt.Sprintf("[%s] %s", m.wikiStep, doc.Title)))

	block := func(labelKey stringKey, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		body = append(body, "")
		body = append(body, monLabelStyle.Render(t(m.lang, labelKey)))
		body = append(body, wrap(text, innerW)...)
	}
	block(kWikiWhat, doc.What)
	block(kWikiWhy, doc.Why)
	block(kWikiRisk, doc.RiskWithout)
	return body
}

// finishedTail returns the localized completion banner shown below the viewport
// once the run ends: a success line, or an error line carrying the engine's error
// text. Rendered fresh from m.lang each frame (see runView) rather than baked into
// the frozen m.content, so it re-translates on a post-finish language switch. The
// quit/back hint is intentionally omitted here — runHints already shows it.
func (m model) finishedTail() string {
	if m.finalErr != nil {
		return "❌ " + t(m.lang, kFinishedErr) + m.finalErr.Error()
	}
	// Successful run → repeat the mode-aware SSH-login notice so the last thing the
	// operator sees is how to reconnect (strict: key-only; soft: password OR key).
	// Only a full `run` touches auth policy; detect/verify leave it untouched.
	tail := "✅ " + t(m.lang, kFinishedOK)
	// Internet benchmark (Feature G): only when A4 produced a comparable PRE→POST
	// pair (BenchOK); omitted cleanly for detect/verify or a skipped/no-sample A4.
	if m.haveSummary && m.summary.BenchOK {
		tail += "\n" + fmt.Sprintf(t(m.lang, kBenchLine),
			m.summary.BenchPreMBs, m.summary.BenchPostMBs, m.summary.BenchRatio)
	}
	// Skip reasons (Feature F): list WHY each step was skipped, not just a count.
	if m.haveSummary && len(m.summary.Skips) > 0 {
		tail += "\n" + t(m.lang, kSkipsHeader)
		for _, sk := range m.summary.Skips {
			tail += "\n  " + fmt.Sprintf(t(m.lang, kSkipLine), sk.ID, sk.Reason)
		}
	}
	if m.command == "run" {
		tail += "\n" + m.pwOffWarning()
	}
	return tail
}

// pwOffWarning builds the localized SSH-login notice. Shared by the pre-run
// content (start) and the post-run finished tail. MODE-AWARE: strict shows the
// loud two-line "password login is now OFF, connect with the generated key"
// notice; soft shows a single info line — password login STAYS ON, and a key is
// also generated so either works. The key lives only in memory and is shown on
// the key screen; there is no on-disk path to reference.
func (m model) pwOffWarning() string {
	if m.mode == config.ModeStrict {
		return t(m.lang, kPwOffWarn) + "\n" + t(m.lang, kPwOffLogin)
	}
	return t(m.lang, kPwOnInfo)
}

// --- SSH key screen (phaseKey) ------------------------------------------------
//
// Shows the generated private-key PEM (in memory only — never logged) so the
// operator can copy it before it is lost, with a clickable "Copy key" button and
// a `c` hotkey. Built in the same bordered frame as runView/wikiView, with the
// monitor footer pinned at the bottom (every post-connect screen keeps it alive).

const keyBodyTopRow = 2 // top border (0) + switcher (1) → body starts at row 2

// keyButtonLabel returns the rendered "Copy key" button text (with brackets), the
// SINGLE source shared by keyView (render) and keyCopyAtClick (hit-test) so their
// geometry cannot drift.
func (m model) keyButtonLabel() string { return "[ " + t(m.lang, kKeyCopyBtn) + " ]" }

// keyConnLine builds the localized connect hint: the label + an ssh command that
// uses the admin user the executor switched to (root SSH is blocked post-harden).
// The "<key-file>" is a placeholder — the key is saved nowhere, so the operator
// chooses a path when they paste the copied PEM.
func (m model) keyConnLine() string {
	host := m.host
	if host == "" {
		host = strings.TrimSpace(m.inputs[fHost].Value())
	}
	return t(m.lang, kKeyConnHint) + " ssh -i <key-file> " + defaultAdminUser + "@" + host
}

// keyBodyLines builds the ordered key-screen body (warning, PEM, connect hint,
// button, status) wrapped/clipped to innerW, and returns the slice index of the
// button line so keyCopyAtClick can recover its screen Y. Every PEM line is
// rendered (the OpenSSH PEM is multi-line, ~400 chars); long lines are clipped to
// innerW so they never cross the border.
func (m model) keyBodyLines(innerW int) (lines []string, buttonIdx int) {
	// Mode-aware warning: strict is KEY-ONLY (root password locked), so losing the
	// key loses access — render it loud (errStyle). Soft KEEPS password login, so
	// the key is an optional convenience — render it non-alarming (labelStyle) and
	// it never implies the operator is locked out.
	if m.mode == config.ModeStrict {
		lines = append(lines, wrap(errStyle.Render(t(m.lang, kKeyWarnStrict)), innerW)...)
	} else {
		lines = append(lines, wrap(labelStyle.Render(t(m.lang, kKeyWarnSoft)), innerW)...)
	}
	lines = append(lines, "")
	for _, ln := range strings.Split(strings.TrimRight(m.keyPEM, "\n"), "\n") {
		lines = append(lines, truncDisplay(ln, innerW))
	}
	lines = append(lines, "")
	lines = append(lines, wrap(m.keyConnLine(), innerW)...)
	lines = append(lines, "")
	buttonIdx = len(lines)
	lines = append(lines, pillOnStyle.Render(m.keyButtonLabel()))
	switch {
	case m.keyCopied:
		lines = append(lines, monGreenStyle.Render(t(m.lang, kKeyCopied)))
	case m.keyCopyFailed:
		lines = append(lines, errStyle.Render(t(m.lang, kKeyCopyFail)))
	default:
		lines = append(lines, "")
	}
	return lines, buttonIdx
}

// keyView renders the SSH key screen inside the shared bordered frame, then the
// localized control hint and the live monitor box. Layout (0-based screen rows):
//
//	row 0              : main box top border
//	row 1              : RU/EN switcher
//	rows 2..2+viewH-1  : scrollable body (warning, PEM, connect hint, button, status)
//	...                : hint, bottom border, then the 3-row monitor box (pinned)
func (m model) keyView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body, _ := m.keyBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+t(m.lang, kKeyTitle)+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	// Same fixed-chrome layout as summaryView/wikiView: a scrollable middle region of
	// exactly bodyViewH rows (no scroll state here — the PEM almost always fits, but
	// renderScrollRegion keeps the footer pinned regardless), then hint + bottom
	// border + the 3-row monitor box.
	viewH := m.bodyViewH()
	m.renderScrollRegion(&sb, b, body, innerW, viewH, 0)

	sb.WriteString(contentLine(b, helpStyle.Render(t(m.lang, kKeyHint)), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')
	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// copyKey copies the private-key PEM to the system clipboard, recording success or
// failure for the on-screen status line. Pure value-receiver (model copied by value).
func (m model) copyKey() model {
	if err := clipboard.WriteAll(m.keyPEM); err != nil {
		m.keyCopied = false
		m.keyCopyFailed = true
		return m
	}
	m.keyCopied = true
	m.keyCopyFailed = false
	return m
}

// keyCopyAtClick reports whether the click at (x,y) hit the "Copy key" button. It
// derives the button's screen row from the SAME body layout keyView renders
// (keyBodyTopRow + buttonIdx) and the X range from the rendered button width, so
// the hit-test matches the draw exactly.
func (m model) keyCopyAtClick(x, y int) bool {
	if m.phase != phaseKey {
		return false
	}
	_, buttonIdx := m.keyBodyLines(innerWidth(m.boxWidth()))
	if y != keyBodyTopRow+buttonIdx {
		return false
	}
	const contentX0 = 2 // borderLeft(1) + space(1)
	w := lipgloss.Width(m.keyButtonLabel())
	return x >= contentX0 && x < contentX0+w
}

// finishedTailRows is the number of content rows finishedTail occupies; runView
// emits one box line per "\n"-split segment, so vpHeight must reserve the same.
func (m model) finishedTailRows() int {
	if !m.finished {
		return 0
	}
	return strings.Count(m.finishedTail(), "\n") + 1
}

// runHints returns the contextual hint line: while running, auto-follow overrides
// manual scroll so only quit is shown; once finished/idle, enter/esc back + scroll + quit.
func (m model) runHints() string {
	if m.running && !m.finished {
		return t(m.lang, kRunHintRunning)
	}
	return t(m.lang, kRunHintIdle)
}

// progressLine renders the top progress line: a step counter + bar + percent +
// current step name while running, a summary once finished, or an action label
// when there is no step list (detect/verify pre-finish).
func (m model) progressLine(innerW int) string {
	switch {
	case m.haveSummary:
		return m.summaryLine(innerW)
	case m.total > 0:
		return m.barLine(innerW)
	default:
		// No step list yet (before the first step). Still show the live spinner +
		// elapsed timer so the view never looks frozen.
		label := strings.TrimSpace(m.inputs[fHost].Value())
		if p := m.livePrefix(); p != "" {
			label = p + label
		}
		return truncDisplay(label, innerW)
	}
}

// livePrefix returns "⠙ 1m23s · " (spinner + running elapsed) while the run is in
// flight, or "" once finished — used to keep the top progress line visibly alive.
func (m model) livePrefix() string {
	if !m.running || m.finished {
		return ""
	}
	return fmt.Sprintf("%c %s · ", spinnerFrames[m.spin], fmtElapsed(m.elapsed))
}

// fmtElapsed renders a duration compactly: "45s" under a minute, else "2m05s".
func fmtElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// barLine builds "Step N/M [bar] PP%  <name>" so the ENTIRE line (including the live
// "⠙ 1m23s · " prefix and the localized "Шаг/Step" word) is clamped to innerW. The
// step name is the flexible part: it is the localized SHORT title (RU when langRU),
// truncated with an ellipsis to whatever width is left after the counter+bar+percent,
// so it never crosses the border. The bar is capped so a reasonable name (≥12 cells,
// budget permitting) always fits beside it. All widths use lipgloss.Width (display
// cells, multibyte-safe), not len.
func (m model) barLine(innerW int) string {
	left := m.livePrefix() + fmt.Sprintf("%s %d/%d ", t(m.lang, kStepN), m.index, m.total)
	pct := 0
	if m.total > 0 {
		pct = m.index * 100 / m.total
	}
	pctStr := fmt.Sprintf("%3d%%", pct) // up to "100%"

	// localized short title — RU when the UI is Russian — with the engine Title as
	// the fallback for any unmapped ID.
	title := localStepTitle(m.lang, m.curID, m.curTitle)

	// Fixed (non-name, non-bar) cells: the counter prefix + " PPP%" + the two gaps
	// (one before the percent, two before the name) — measured in display cells.
	const gapBeforePct = 1  // " " between bar and percent
	const gapBeforeName = 2 // "  " between percent and name
	fixed := lipgloss.Width(left) + gapBeforePct + lipgloss.Width(pctStr) + gapBeforeName

	// Width left for bar + name together. Cap the bar so a reasonable name still fits.
	const (
		maxBarW     = 24 // don't let the bar hog the whole line
		minNameW    = 12 // try to keep room for at least this many name cells
		minBarW     = 3  // below this the bar reads as noise — drop it
		minNameSlot = 1
	)
	avail := max(innerW-fixed, 0)

	// Give the bar up to maxBarW, but never so much that the name slot drops below
	// minNameW (until the line is simply too narrow to honor both).
	barW := maxBarW
	barW = min(barW, avail-minNameW)
	barW = min(barW, avail)

	if barW < minBarW {
		// Too tight for a bar — drop it, keep counter + percent + truncated name.
		nameW := innerW - lipgloss.Width(left) - gapBeforePct - lipgloss.Width(pctStr) - gapBeforeName
		name := truncDisplay(title, maxi(nameW, 0))
		line := left + pctStr
		if name != "" {
			line += "  " + name
		}
		return truncDisplay(line, innerW)
	}

	nameW := max(avail-barW, minNameSlot)
	name := truncDisplay(title, nameW)

	filled := pct * barW / 100
	if filled < 0 {
		filled = 0
	}
	if filled > barW {
		filled = barW
	}
	bar := monGreenStyle.Render(strings.Repeat("█", filled)) +
		monDimStyle.Render(strings.Repeat("░", barW-filled))
	out := left + bar + " " + pctStr
	if name != "" {
		out += "  " + name
	}
	// Final hard clamp (defensive): the math above keeps it within innerW, but a
	// multibyte rounding edge must never cross the border.
	return truncDisplay(out, innerW)
}

// summaryLine renders the finished-run summary that replaces the progress bar.
func (m model) summaryLine(innerW int) string {
	s := m.summary
	mark := "✓"
	if s.Fail > 0 {
		mark = "✗"
	}
	verifyTotal := s.VerifyPassed + s.VerifyFailed
	line := fmt.Sprintf("%s %s · %d OK · %d SKIP · %d FAIL · %s · %s %d/%d",
		mark, t(m.lang, kDoneWord), s.OK, s.Skip, s.Fail, s.Elapsed.Round(time.Second),
		t(m.lang, kVerifyTag), s.VerifyPassed, verifyTotal)
	return truncDisplay(line, innerW)
}

// titledTop draws a box top border with the title centered, breaking the border:
// TopLeft + left dashes + title + right dashes + TopRight, total width = w.
func titledTop(b lipgloss.Border, title string, w int) string {
	if w < minBoxWidth {
		w = minBoxWidth
	}
	tw := lipgloss.Width(title)
	dashTotal := w - 2 - tw // minus the two corner runes
	if dashTotal < 0 {
		// Title too wide for the border — clip it and use no dashes.
		title = truncDisplay(title, w-2)
		tw = lipgloss.Width(title)
		dashTotal = w - 2 - tw
		if dashTotal < 0 {
			dashTotal = 0
		}
	}
	leftN := dashTotal / 2
	rightN := dashTotal - leftN
	return borderStyle.Render(b.TopLeft) +
		borderStyle.Render(strings.Repeat(b.Top, leftN)) +
		title +
		borderStyle.Render(strings.Repeat(b.Top, rightN)) +
		borderStyle.Render(b.TopRight)
}

// borderLine draws a plain horizontal border edge: left + dashes + right, width w.
func borderLine(left, mid, right string, w int) string {
	if w < minBoxWidth {
		w = minBoxWidth
	}
	n := w - 2
	if n < 0 {
		n = 0
	}
	return borderStyle.Render(left + strings.Repeat(mid, n) + right)
}

// contentLine wraps one content line in the box: Left + " " + padded line + " " +
// Right, where the line is truncated/padded to exactly innerW display cells.
func contentLine(b lipgloss.Border, line string, innerW int) string {
	return contentLineR(b, line, innerW, borderStyle.Render(b.Right))
}

// contentLineR is contentLine with an explicit right-border cell, so a scrollable
// region can substitute a scrollbar thumb/track glyph there (see renderScrollRegion)
// while every other row keeps the plain border. right must be exactly one display
// cell wide (already styled).
func contentLineR(b lipgloss.Border, line string, innerW int, right string) string {
	if innerW < 0 {
		innerW = 0
	}
	line = truncDisplay(line, innerW)
	if pad := innerW - lipgloss.Width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return borderStyle.Render(b.Left) + " " + line + " " + right
}

// bodyViewH is the height (in rows) of the scrollable middle region on the summary
// and wiki screens: the terminal height minus the fixed chrome — top border +
// switcher (2) + hint + main-box bottom (2) + the 3-row monitor box = 7 — floored at
// 1 so the region never vanishes on a tiny terminal.
func (m model) bodyViewH() int { return max(m.h-7, 1) }

// clampScroll bounds a scroll offset to [0, max(0,total-viewH)] so it can never
// scroll past the end (or before the start). Recomputed on every use, so a resize
// that grows the window (raising viewH) automatically pulls the offset back.
func clampScroll(off, total, viewH int) int {
	maxOff := total - viewH
	if maxOff < 0 {
		maxOff = 0
	}
	if off < 0 {
		return 0
	}
	if off > maxOff {
		return maxOff
	}
	return off
}

// renderScrollRegion emits exactly viewH box content rows showing body[off:off+viewH]
// (blank-padded when the body is shorter), so the caller's footer stays pinned. When
// the body overflows viewH it draws a proportional scrollbar in the RIGHT border —
// a bright thumb (█) over a dim track (│) whose size and position reflect viewH/total
// and off — so the user sees there is hidden content and where they are; when it all
// fits the plain border is drawn and there is no scrollbar. off is assumed already
// clamped (clampScroll).
func (m model) renderScrollRegion(sb *strings.Builder, b lipgloss.Border, body []string, innerW, viewH, off int) {
	total := len(body)
	overflow := total > viewH

	// Thumb extent in region rows [thumbStart, thumbEnd).
	thumbStart, thumbEnd := 0, 0
	if overflow {
		thumb := max(viewH*viewH/total, 1) // proportion of content visible, ≥1 cell
		maxOff := total - viewH
		pos := 0
		if maxOff > 0 {
			pos = off * (viewH - thumb) / maxOff
		}
		if pos < 0 {
			pos = 0
		}
		if pos > viewH-thumb {
			pos = viewH - thumb
		}
		thumbStart, thumbEnd = pos, pos+thumb
	}

	for i := range viewH {
		var line string
		if off+i < total {
			line = body[off+i]
		}
		right := borderStyle.Render(b.Right)
		if overflow {
			if i >= thumbStart && i < thumbEnd {
				right = borderStyle.Render("█") // thumb
			} else {
				right = monDimStyle.Render("│") // track
			}
		}
		sb.WriteString(contentLineR(b, line, innerW, right))
		sb.WriteByte('\n')
	}
}

// truncDisplay truncates s to at most w display cells (ANSI/Unicode-safe). w<=0
// returns "".
func truncDisplay(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// monSep is the dim vertical separator drawn BETWEEN the CPU/RAM/DISK segments,
// padded one space each side (" │ ", 3 cells). It uses the box border color so the
// monitor row reads as the same chrome family as the surrounding frame, and visually
// divides the segments (so e.g. CPU's "4.2GHz" extra can't be misread as RAM's).
const monSepCells = 3 // display width of " │ "

func monSep() string { return " " + borderStyle.Render("│") + " " }

// renderMonitor renders the full-width live CPU/RAM/DISK footer sized to width.
// !haveSample or disconnected → a dim "reconnecting…" line. Otherwise three labelled
// bar+percent segments, divided by a dim "│" separator, that consume the available
// width: the fixed parts (labels, percents, extras) and the two separators are
// subtracted, and the REMAINING width is split evenly across the three bars so they
// are wide and the row spans edge to edge. It degrades gracefully on a narrow box:
// drop the used/total + freq extras first, then shrink bars (min 3), then fall back
// to a compact percent-only line — never overflowing width.
func (m model) renderMonitor(width int) string {
	if width < 1 {
		width = 1
	}
	// Blank to "reconnecting…" ONLY on a genuine outage: either we never got a
	// first sample, or we've seen statMissThreshold consecutive disconnected
	// samples (≈3s — a reboot, not jitter). Below the threshold we keep rendering
	// the last-good held sample so a single slow/failed sample doesn't blank the
	// footer. (m.sample holds the last good sample; it is only overwritten on a
	// Connected==true sample, so it never carries a zeroed disconnected snapshot.)
	if !m.haveSample || m.statMiss >= statMissThreshold {
		return monDimStyle.Render(clip(t(m.lang, kMonReconnecting), width))
	}

	s := m.sample
	labels := []string{"CPU", "RAM", "DISK"}
	pcts := []float64{s.CPU, s.RAM, s.Disk}
	extras := []string{
		cpuFreq(s.CPUMHz),
		pairKB(s.RAMUsedKB, s.RAMTotalKB),
		pairKB(s.DiskUsedKB, s.DiskTotalKB),
	}

	const minBars = 3
	// Try the richest layout first (with extras), then drop extras if too tight. For
	// each candidate, sum the FIXED (non-bar) cells of all three segments plus the two
	// separators; the bars consume ALL the remaining width so the row spans edge to
	// edge. The remainder of the even split is handed to the leftmost segments (one
	// extra cell each) so the total exactly fills width with no trailing void.
	for _, withExtra := range []bool{true, false} {
		fixed := 2 * monSepCells // the two " │ " separators
		for i := range labels {
			ex := ""
			if withExtra {
				ex = extras[i]
			}
			fixed += monSegFixed(labels[i], pcts[i], ex)
		}
		barBudget := width - fixed
		if barBudget < len(labels)*minBars {
			continue // not even minimum bars fit at this richness — go leaner
		}
		base := barBudget / len(labels)
		rem := barBudget % len(labels) // distribute leftover cells across segments
		segs := make([]string, len(labels))
		for i := range labels {
			ex := ""
			if withExtra {
				ex = extras[i]
			}
			bars := base
			if i < rem {
				bars++ // leftmost segments absorb the remainder so the row fills width
			}
			segs[i] = monitorSeg(labels[i], pcts[i], bars, ex)
		}
		return strings.Join(segs, monSep())
	}

	// Last resort: compact percent-only form, separated by the same dim │.
	var parts []string
	for i, l := range labels {
		parts = append(parts, monLabelStyle.Render(l)+" "+pctStyle(pcts[i]).Render(fmtPct(pcts[i])))
	}
	return clip(strings.Join(parts, monSep()), width)
}

// monSegFixed returns the FIXED (non-bar) display width of a segment built by
// monitorSeg with the same label/pct/extra: label + " " + " " + pct [+ " " + extra].
// Used to budget how much width is left for the bars.
func monSegFixed(label string, pct float64, extra string) int {
	w := lipgloss.Width(label) + 1 + 1 + lipgloss.Width(fmtPct(pct))
	if extra != "" {
		w += 1 + lipgloss.Width(extra)
	}
	return w
}

// monitorSeg renders one labelled bar+percent segment plus an optional extra.
func monitorSeg(label string, pct float64, bars int, extra string) string {
	if bars < 3 {
		bars = 3
	}
	seg := monLabelStyle.Render(label) + " " + renderBar(pct, bars) +
		" " + pctStyle(pct).Render(fmtPct(pct))
	if extra != "" {
		seg += " " + monDimStyle.Render(extra)
	}
	return seg
}

// humanKB renders a KB value (1024 base): G with 1 decimal when ≥1 GiB, else M
// as an integer. e.g. 1468006→"1.4G", 524288→"512M".
func humanKB(kb float64) string {
	if kb < 0 {
		kb = 0
	}
	if kb >= 1024*1024 {
		return fmt.Sprintf("%.1fG", kb/(1024*1024))
	}
	return fmt.Sprintf("%.0fM", kb/1024)
}

// pairKB renders "used/total" sharing the unit suffix when both land on the same
// unit (e.g. "1.4/2.0G"); otherwise each value keeps its own suffix. Empty when
// total is unknown (≤0).
func pairKB(usedKB, totalKB float64) string {
	if totalKB <= 0 {
		return ""
	}
	u := humanKB(usedKB)
	t := humanKB(totalKB)
	if u[len(u)-1] == t[len(t)-1] {
		return u[:len(u)-1] + "/" + t
	}
	return u + "/" + t
}

// cpuFreq renders a CPU frequency from MHz as "2.4GHz", or "" when unknown.
func cpuFreq(mhz float64) string {
	if mhz <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1fGHz", mhz/1000)
}

// renderBar draws a barW-wide bar filled proportional to pct (-1 → empty/dim).
func renderBar(pct float64, barW int) string {
	if barW < 1 {
		barW = 1
	}
	if pct < 0 {
		return monDimStyle.Render(strings.Repeat("░", barW))
	}
	filled := int(math.Round(pct / 100 * float64(barW)))
	if filled < 0 {
		filled = 0
	}
	if filled > barW {
		filled = barW
	}
	return pctStyle(pct).Render(strings.Repeat("█", filled)) +
		monDimStyle.Render(strings.Repeat("░", barW-filled))
}

// fmtPct formats a percent as "NN%"; -1 (unknown/parse-failed) → "--%".
func fmtPct(pct float64) string {
	if pct < 0 {
		return "--%"
	}
	return fmt.Sprintf("%2.0f%%", pct)
}

// pctStyle picks the threshold color: green <70, yellow <90, red ≥90; dim if unknown.
func pctStyle(pct float64) lipgloss.Style {
	switch {
	case pct < 0:
		return monDimStyle
	case pct < 70:
		return monGreenStyle
	case pct < 90:
		return monYellowStyle
	default:
		return monRedStyle
	}
}

// clip truncates s (by display width) to at most w cells so the footer never
// overflows the terminal width.
func clip(s string, w int) string {
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// Run launches the TUI program. In v2 alt-screen + mouse are per-View fields
// (set in View()), not NewProgram options.
func Run() error {
	p := tea.NewProgram(newModel())
	_, err := p.Run()
	return err
}

// stepFocus advances cur by dir (+1/-1) within the ordered rows slice, wrapping
// around. If cur isn't in rows (e.g. it was just hidden), it lands on the first
// row so focus is never stranded.
func stepFocus(rows []int, cur, dir int) int {
	at := -1
	for i, r := range rows {
		if r == cur {
			at = i
			break
		}
	}
	if at < 0 {
		return rows[0]
	}
	n := len(rows)
	return rows[(at+dir+n)%n]
}

// validHost accepts an IP literal or a syntactically valid hostname.
func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			ok := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') || r == '-'
			if !ok {
				return false
			}
		}
	}
	return true
}

// fsSafeHost makes a host string safe for use in a log filename: the form already
// restricts Host to [0-9a-zA-Z.-] (sanitizeField), so only the dot needs replacing;
// colon is handled too for defensiveness (e.g. a future IPv6 literal).
func fsSafeHost(h string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(h)
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
