// Package tui is a Bubble Tea front-end: a form to enter host/port/user/
// password-or-key/mode, then a live streaming view of the engine run.
package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"os"
	"path/filepath"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/monitor"
	"github.com/UberMorgott/morgward/internal/tweaks"
	"github.com/UberMorgott/morgward/internal/version"
	"github.com/creativeprojects/go-selfupdate"
)

const (
	defaultAdminUser = "vpsadmin"
)

// updateRepo is the GitHub "owner/repo" slug self-update checks for releases.
const updateRepo = "UberMorgott/morgward"

// self-update state machine (model.updateState). Plain int constants so the model
// stays fully value-copyable (the model is copied by value every Update).
const (
	updChecking  = iota // check in flight (Init fired checkUpdateCmd, no result yet)
	updCurrent          // DetectLatest found no newer release → running the latest
	updAvailable        // a newer release exists (model.updateVer holds its version)
	updErr              // the check failed (e.g. offline / GitHub unreachable)
)

type phase int

const (
	phaseForm phase = iota
	phaseRun
	phaseSummary   // post-finish stats summary + clickable fix list
	phaseWiki      // a single fix's what/why/risk description
	phaseKey       // shows the generated SSH private key + a clipboard "Copy key" button
	phaseMatrix    // per-tweak audit table (the "анализ" action result)
	phaseDashboard // post-connect server card + live tweak audit + apply/security buttons
	phaseSecurity  // security + access menu: access-state card + SAFE actions + DANGER key-only lock
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

type logMsg string

type doneMsg struct{ err error }

// updateCheckMsg carries the result of the one-shot self-update check spawned by
// Init(). It holds ONLY value types (no *Release / no maps) so the model stays
// value-copyable when stashed: found reports whether DetectLatest found a NEWER
// release (found==false is "up-to-date", NOT an error); ver is that version when
// found; err is non-nil only when the check itself failed (e.g. offline).
type updateCheckMsg struct {
	found bool
	ver   string
	err   error
}

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
	// phase to return to on esc (phaseSummary). wikiStep is the wiki.Doc key (a
	// real step id, e.g. "A2"). wikiTweak, when non-empty, is the header label of
	// the specific tweak the page was opened from ("[a2.permitroot] <name>"); the
	// body is still the step-level doc resolved via wikiStep. Empty => summary path.
	wikiStep   string
	wikiTweak  string
	wikiReturn phase
	// wikiProbeID, when non-empty (Dashboard audit-row path), is the clicked
	// probe's tweaks.Probe.ID. It selects the per-PROBE description in
	// wikiBodyLines (so the 3 A3 probes show distinct text) instead of the
	// step-level wiki.Doc. Empty on the summary path => step-level doc. Plain
	// string, value-copyable.
	wikiProbeID string

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
	dashAuditResults []tweaks.Result // display set: informational probes filtered out
	dashAuditRaw     []tweaks.Result // unfiltered set; populateSecurityState reads this
	dashFacts        *detect.Facts
	dashScroll       int  // audit list scroll offset (clamped like sumScroll)
	dashApplyConfirm bool // true while the A8 reboot-warning confirm is shown

	// Security menu state (phaseSecurity). All plain strings/bool — value-copy safe.
	// The three sec*State fields are derived from the audit BEFORE entering the menu
	// (populateSecurityState) so the access-state card shows the current posture.
	// secDangerConfirm gates the explicit blocking lockout warning before the danger
	// key-only flow routes to phaseKey + applies A2-danger/A2.5.
	secRootLoginState string // "разрешён по паролю" / "no" / "только по ключу" (or placeholder)
	secKeyOnlyState   string // "да" / "нет" (or placeholder)
	secAdminState     string // "vpsadmin@host" / "отсутствует" (or placeholder)
	secDangerConfirm  bool   // true while the danger key-only lockout confirm is shown

	// self-update state (landing strip). All plain value types so the model is
	// safe to copy by value every Update: updateState is the updChecking/updCurrent/
	// updAvailable/updErr machine, updateVer the latest version string when available,
	// wantUpdate the flag set when the operator clicks "Обновить" (read by Run()).
	updateState int
	updateVer   string
	wantUpdate  bool

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
	// Best-effort: a prior Windows self-update renames the running exe to a hidden
	// ".<base>.old" dotfile in the exe dir (go-selfupdate cannot delete a running
	// binary), so clean up that leftover on the next launch. Errors are ignored — it
	// simply won't exist on first run / non-Windows.
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(filepath.Join(filepath.Dir(exe), "."+filepath.Base(exe)+".old"))
	}
	// v2 has no tea.SetWindowTitle Cmd; the window title is a tea.View field built
	// per-frame in View() from m.titleK + m.lang (see windowTitle).
	// Start the always-on resize poll (Windows has no SIGWINCH — see resizeTick) and
	// fire the one-shot self-update check (result arrives as updateCheckMsg).
	return tea.Batch(textinput.Blink, resizeTick(), checkUpdateCmd())
}

// checkUpdateCmd runs the one-shot self-update check off the bubbletea loop and
// returns the outcome as an updateCheckMsg. A short timeout keeps an unreachable
// GitHub from hanging the strip in "checking…" — it resolves to updErr instead.
// found==false (no newer release) is up-to-date, NOT an error.
func checkUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		updater, err := selfupdate.NewUpdater(selfupdate.Config{})
		if err != nil {
			return updateCheckMsg{err: err}
		}
		latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(updateRepo))
		if err != nil {
			return updateCheckMsg{err: err}
		}
		// DetectLatest reports found==true for any matching release; it does NOT
		// compare against the running version. An update is available ONLY when the
		// latest is strictly newer than version.Version. No matching release
		// (found==false) is "nothing to update to" → up-to-date, not an error.
		if !found || latest == nil || !latest.GreaterThan(version.Version) {
			return updateCheckMsg{found: false}
		}
		return updateCheckMsg{found: true, ver: latest.Version()}
	}
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
	case phaseSecurity:
		return m.securityView()
	case phaseWiki:
		return m.wikiView()
	case phaseKey:
		return m.keyView()
	default:
		return m.formView()
	}
}

// Result is the outcome of a TUI session, returned by Run() so the caller (main)
// can act on a user-requested self-update after the alt-screen has torn down.
type Result struct {
	DoUpdate  bool   // operator clicked "Обновить" → caller should self-update
	TargetVer string // the version to update to (set only when DoUpdate)
}

// Run launches the TUI program and returns the session Result. In v2 alt-screen +
// mouse are per-View fields (set in View()), not NewProgram options.
func Run() (Result, error) {
	p := tea.NewProgram(newModel())
	out, err := p.Run()
	if err != nil {
		return Result{}, err
	}
	final, _ := out.(model)
	return Result{DoUpdate: final.wantUpdate, TargetVer: final.updateVer}, nil
}
