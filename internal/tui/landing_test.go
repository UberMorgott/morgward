package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/version"
)

// formModel builds a form-phase model sized for layout tests.
func formModel(w, h int) model {
	m := newModel()
	m.w, m.h = w, h
	return m
}

// TestLandingFormPhaseExists asserts the form phase still renders without panic
// and that advancedOpen defaults to false on a fresh model.
func TestLandingFormPhaseExists(t *testing.T) {
	m := newModel()
	if m.phase != phaseForm {
		t.Fatalf("newModel phase=%v want phaseForm", m.phase)
	}
	if m.advancedOpen {
		t.Fatalf("advancedOpen should default to false")
	}
	m.w, m.h = 80, 24
	if s := m.formView(); s == "" {
		t.Fatalf("formView returned empty string")
	}
}

// TestDisclosureKeysExist asserts the disclosure label/state keys translate
// non-empty in both languages.
func TestDisclosureKeysExist(t *testing.T) {
	for _, lang := range []Lang{langRU, langEN} {
		if s := t2(lang, kDisclosureLabel); s == "" {
			t.Fatalf("lang %d kDisclosureLabel empty", lang)
		}
		if s := t2(lang, kDisclosureOpen); s == "" {
			t.Fatalf("lang %d kDisclosureOpen empty", lang)
		}
	}
}

// t2 is a thin alias for t so tests read clearly (t shadows *testing.T name).
func t2(lang Lang, k stringKey) string { return t(lang, k) }

// TestFramedInputRender3Rows asserts framedInputRow returns exactly three lines
// (top border, label+value, bottom border) and that no line is wider than the
// box inner width.
func TestFramedInputRender3Rows(t *testing.T) {
	m := formModel(80, 24)
	lines := m.framedInputRow("Хост", m.inputs[fHost], true)
	if len(lines) != 3 {
		t.Fatalf("framedInputRow returned %d lines, want 3", len(lines))
	}
	innerW := innerWidth(m.boxWidth())
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w > innerW {
			t.Fatalf("line %d width %d > innerW %d: %q", i, w, innerW, ln)
		}
	}
	// Middle line must carry the label text.
	if !strings.Contains(lines[1], "Хост") {
		t.Fatalf("middle line missing label: %q", lines[1])
	}
}

// rowFields returns, for the given kind, the set of field indices present among
// frInput rows (deduplicated, in order of first appearance).
func inputFieldsInRows(rows []formRow) []int {
	seen := map[int]bool{}
	var out []int
	for _, r := range rows {
		if r.kind == frInput && !seen[r.field] {
			seen[r.field] = true
			out = append(out, r.field)
		}
	}
	return out
}

func hasKind(rows []formRow, k formRowKind) bool {
	for _, r := range rows {
		if r.kind == k {
			return true
		}
	}
	return false
}

// disclosureRowIndex returns the slice index of the first frDisclosure row, or -1.
func disclosureRowIndex(rows []formRow) int {
	for i, r := range rows {
		if r.kind == frDisclosure {
			return i
		}
	}
	return -1
}

// TestDisclosureToggleClickable verifies the disclosure row exists, is clickable,
// and toggling advancedOpen reveals the Port/User/Key inputs.
func TestDisclosureToggleClickable(t *testing.T) {
	m := formModel(80, 24)
	m.advancedOpen = false
	rows := m.formRows()
	// Novice default: only Host + Password inputs are present.
	got := inputFieldsInRows(rows)
	want := []int{fHost, fPass}
	if len(got) != len(want) {
		t.Fatalf("advancedOpen=false input fields=%v want %v", got, want)
	}
	di := disclosureRowIndex(rows)
	if di < 0 {
		t.Fatalf("no frDisclosure row found")
	}
	// Click the disclosure row.
	hit := m.formHitAtClick(0, formBodyTopRow+di)
	if !hit.ok || hit.kind != frDisclosure {
		t.Fatalf("click on disclosure row: ok=%v kind=%v", hit.ok, hit.kind)
	}
	// Apply the click and confirm advanced inputs appear.
	m2, _ := m.formClick(0, formBodyTopRow+di)
	mm := m2.(model)
	if !mm.advancedOpen {
		t.Fatalf("disclosure click did not set advancedOpen")
	}
	got2 := inputFieldsInRows(mm.formRows())
	if len(got2) != nInputs {
		t.Fatalf("advancedOpen=true input fields=%v want all %d", got2, nInputs)
	}
}

// TestActionRemovedFromForm asserts the run/detect/verify selector is gone from
// the form while m.command stays "run" (still used by the engine).
func TestActionRemovedFromForm(t *testing.T) {
	m := formModel(80, 24)
	if hasKind(m.formRows(), frAction) {
		t.Fatalf("frAction row still present in formRows")
	}
	if m.command != "run" {
		t.Fatalf("m.command=%q want \"run\"", m.command)
	}
	// also with advanced open
	m.advancedOpen = true
	if hasKind(m.formRows(), frAction) {
		t.Fatalf("frAction row present when advancedOpen")
	}
}

// kindIndex returns the slice index of the first row of the given kind, or -1.
func kindIndex(rows []formRow, k formRowKind) int {
	for i, r := range rows {
		if r.kind == k {
			return i
		}
	}
	return -1
}

// TestSaveLogTogglePosition asserts the save-log toggle carries its label and is
// positioned after the disclosure toggle and before the Connect button.
func TestSaveLogTogglePosition(t *testing.T) {
	m := formModel(80, 24)
	m.advancedOpen = false
	m.saveLog = false
	rows := m.formRows()
	logIdx := kindIndex(rows, frLog)
	if logIdx < 0 {
		t.Fatalf("no frLog row")
	}
	if !strings.Contains(rows[logIdx].text, t2(m.lang, kSaveLogLabel)) {
		t.Fatalf("frLog row missing save-log label: %q", rows[logIdx].text)
	}
	disIdx := kindIndex(rows, frDisclosure)
	startIdx := kindIndex(rows, frStart)
	if !(logIdx > disIdx && logIdx < startIdx) {
		t.Fatalf("frLog idx=%d should be after frDisclosure=%d and before frStart=%d", logIdx, disIdx, startIdx)
	}
}

// TestCatalogLinkKeyExists asserts the catalog-link label translates non-empty.
func TestCatalogLinkKeyExists(t *testing.T) {
	for _, lang := range []Lang{langRU, langEN} {
		if s := t2(lang, kCatalogLink); s == "" {
			t.Fatalf("lang %d kCatalogLink empty", lang)
		}
	}
}

// TestCatalogLinkRendered asserts the catalog-link label appears as a form row and
// (P5) is a clickable navigation target that opens the catalog (catalogReturn=form).
func TestCatalogLinkRendered(t *testing.T) {
	m := formModel(80, 24)
	rows := m.formRows()
	idx := kindIndex(rows, frCatalogLink)
	if idx < 0 {
		t.Fatalf("no frCatalogLink row in formRows")
	}
	if !strings.Contains(rows[idx].text, t2(m.lang, kCatalogLink)) {
		t.Fatalf("frCatalogLink row missing label: %q", rows[idx].text)
	}
	// P5: clicking it is a registered hit target that navigates to phaseCatalog.
	hit := m.formHitAtClick(0, formBodyTopRow+idx)
	if !hit.ok || hit.kind != frCatalogLink {
		t.Fatalf("catalog link should be clickable, got ok=%v kind=%v", hit.ok, hit.kind)
	}
}

// TestFormHitTestAccuracy verifies a click at (x, formBodyTopRow+rowIdx) resolves
// to the correct row kind for every row, with 3-row framed inputs.
func TestFormHitTestAccuracy(t *testing.T) {
	m := formModel(80, 24)
	m.advancedOpen = true // exercise all five framed inputs
	rows := m.formRows()
	pillX := m.pillColStart() + 1 // inside the first pill of any toggle row

	for i, r := range rows {
		y := formBodyTopRow + i
		switch r.kind {
		case frInput:
			hit := m.formHitAtClick(0, y)
			if !hit.ok || hit.kind != frInput || hit.field != r.field {
				t.Fatalf("row %d (frInput field %d): hit=%+v", i, r.field, hit)
			}
		case frDisclosure:
			hit := m.formHitAtClick(0, y)
			if !hit.ok || hit.kind != frDisclosure {
				t.Fatalf("row %d (frDisclosure): hit=%+v", i, hit)
			}
		case frLog:
			hit := m.formHitAtClick(pillX, y)
			if !hit.ok || hit.kind != frLog {
				t.Fatalf("row %d (frLog): hit=%+v", i, hit)
			}
		case frStart:
			hit := m.formHitAtClick(pillX, y)
			if !hit.ok || hit.kind != frStart {
				t.Fatalf("row %d (frStart): hit=%+v", i, hit)
			}
		}
	}

	// All three Y rows of the Host input map to the same field.
	hostFirst := -1
	for i, r := range rows {
		if r.kind == frInput && r.field == fHost {
			hostFirst = i
			break
		}
	}
	if hostFirst < 0 {
		t.Fatalf("no host input row")
	}
	for d := 0; d < 3; d++ {
		hit := m.formHitAtClick(5, formBodyTopRow+hostFirst+d)
		if !hit.ok || hit.field != fHost {
			t.Fatalf("host row offset %d did not map to fHost: %+v", d, hit)
		}
	}
}

// TestFocusRenderingFramed verifies a focused framed input uses the accent border
// (57) while an unfocused one uses the dim border (240), and that formRows applies
// focus to the input matching m.focus.
func TestFocusRenderingFramed(t *testing.T) {
	m := formModel(80, 24)
	focused := m.framedInputRow("Хост", m.inputs[fHost], true)
	dim := m.framedInputRow("Хост", m.inputs[fHost], false)
	if strings.Join(focused, "\n") == strings.Join(dim, "\n") {
		t.Fatalf("focused and unfocused framed inputs render identically")
	}
	// Accent border color 57 must appear in the focused top border, not the dim one.
	if !strings.Contains(focused[0], "57") {
		t.Fatalf("focused top border missing accent color 57: %q", focused[0])
	}
	if !strings.Contains(dim[0], "240") {
		t.Fatalf("unfocused top border missing dim color 240: %q", dim[0])
	}

	// Via formRows: focus on Password → its block carries the accent border, Host dim.
	m.focus = fPass
	rows := m.formRows()
	var passTop, hostTop string
	for _, r := range rows {
		if r.kind == frInput && r.field == fPass && passTop == "" {
			passTop = r.text
		}
		if r.kind == frInput && r.field == fHost && hostTop == "" {
			hostTop = r.text
		}
	}
	if !strings.Contains(passTop, "57") {
		t.Fatalf("focused Password block missing accent border: %q", passTop)
	}
	if !strings.Contains(hostTop, "240") {
		t.Fatalf("unfocused Host block missing dim border: %q", hostTop)
	}
}

// TestFormViewPadding asserts formView fills exactly m.h screen rows (top border +
// switcher + body + pad + hint + bottom border), with advancedOpen=false.
func TestFormViewPadding(t *testing.T) {
	m := formModel(80, 30) // tall enough that pad >= 0
	m.advancedOpen = false
	out := m.formView()
	lines := strings.Count(out, "\n") + 1 // last line has no trailing newline
	if lines != m.h {
		t.Fatalf("formView rendered %d rows, want m.h=%d", lines, m.h)
	}
	// every line must fit the box width (no overflow past the border)
	bw := m.boxWidth()
	for i, ln := range strings.Split(out, "\n") {
		if w := lipgloss.Width(ln); w > bw {
			t.Fatalf("line %d width %d > boxWidth %d", i, w, bw)
		}
	}
}

// TestFormValidationFramed asserts an empty Host fails validation, sets errMsg,
// and surfaces a frErr row after the inputs and Start button.
func TestFormValidationFramed(t *testing.T) {
	m := formModel(80, 24)
	m.inputs[fHost].SetValue("")
	m.inputs[fPass].SetValue("secret") // provide auth so host is the failing field
	next, _ := m.start()
	mm := next.(model)
	if mm.errMsg == "" {
		t.Fatalf("empty host did not set errMsg")
	}
	if mm.phase != phaseForm {
		t.Fatalf("validation failure should stay on phaseForm, got %v", mm.phase)
	}
	rows := mm.formRows()
	errIdx := kindIndex(rows, frErr)
	if errIdx < 0 {
		t.Fatalf("no frErr row after failed validation")
	}
	startIdx := kindIndex(rows, frStart)
	if errIdx < startIdx {
		t.Fatalf("frErr idx=%d should be after frStart=%d", errIdx, startIdx)
	}
}

// TestVersionFrameHeader asserts the landing renders a version frame carrying the
// "Morgward v<ver>" name and the localized tagline.
func TestVersionFrameHeader(t *testing.T) {
	m := formModel(80, 24)
	out := m.formView()
	wantName := version.Name + " v" + version.Version
	if !strings.Contains(out, wantName) {
		t.Fatalf("formView missing version name %q", wantName)
	}
	if !strings.Contains(out, t2(m.lang, kVersionTagline)) {
		t.Fatalf("formView missing tagline %q", t2(m.lang, kVersionTagline))
	}
}

// TestUpdateStripKeysExist asserts the update-strip state labels translate
// non-empty in both languages (P2 will wire them to the model).
func TestUpdateStripKeysExist(t *testing.T) {
	keys := []stringKey{kUpdateChecking, kUpdateCurrent, kUpdateAvailable, kUpdateError, kUpdateButtonLabel}
	for _, k := range keys {
		for _, lang := range []Lang{langRU, langEN} {
			if s := t2(lang, k); s == "" {
				t.Fatalf("lang %d key %d empty", lang, k)
			}
		}
	}
}

// TestModeSelectorAbsent asserts the soft/strict Mode selector is NOT rendered on
// the landing form (it moved out of the landing — access lockdown lives in the
// Security menu), while m.mode is still defaulted so the engine can read it. The
// primary button label is now "Подключиться" / "Connect".
func TestModeSelectorAbsent(t *testing.T) {
	m := formModel(80, 24)
	// The model still carries a mode (engine reads it); it just must default to soft
	// and never appear on the landing.
	if m.mode != config.ModeSoft {
		t.Fatalf("m.mode=%q want default config.ModeSoft", m.mode)
	}
	for _, adv := range []bool{false, true} {
		m.advancedOpen = adv
		rows := m.formRows()
		// The soft/strict mode pill labels must not leak onto the form via any row.
		// (The mode selector keys were removed from the TUI form in P4; access
		// lockdown moved to the Security menu. Assert against the literal labels so
		// the guard survives even though the i18n keys no longer exist.)
		for _, r := range rows {
			for _, leak := range []string{"строгий", "мягкий", "strict", "soft"} {
				if strings.Contains(r.text, leak) {
					t.Fatalf("mode pill %q leaked into row kind=%v: %q", leak, r.kind, r.text)
				}
			}
		}
	}
	// Primary button label is "Подключиться" / "Connect".
	if got := t2(langRU, kStart); got != "Подключиться" {
		t.Fatalf("RU primary button = %q, want \"Подключиться\"", got)
	}
	if got := t2(langEN, kStart); got != "Connect" {
		t.Fatalf("EN primary button = %q, want \"Connect\"", got)
	}
	// The Connect label must actually appear on the rendered Connect row.
	rows := m.formRows()
	si := kindIndex(rows, frStart)
	if si < 0 {
		t.Fatalf("no frStart row")
	}
	if !strings.Contains(rows[si].text, t2(m.lang, kStart)) {
		t.Fatalf("frStart row missing connect label %q: %q", t2(m.lang, kStart), rows[si].text)
	}
}

// TestLandingFormRenderComplete drives the whole View() path on a phaseForm model
// and asserts the rendered content carries the version header, an input frame, the
// disclosure toggle, the save-log toggle, and the Connect button — and that the
// Mode label is ABSENT (the selector was removed from the landing).
func TestLandingFormRenderComplete(t *testing.T) {
	m := newModel()
	m.w, m.h = 80, 24
	v := m.View()
	out := v.Content
	if strings.TrimSpace(out) == "" {
		t.Fatalf("View() returned empty content")
	}
	checks := map[string]string{
		"version name":   version.Name + " v" + version.Version,
		"tagline":        t2(m.lang, kVersionTagline),
		"host label":     t2(m.lang, kLabelHost),
		"password":       t2(m.lang, kLabelPassword),
		"disclosure":     "Дополнительно",
		"save-log":       t2(m.lang, kSaveLogLabel),
		"connect button": t2(m.lang, kStart),
		"catalog link":   t2(m.lang, kCatalogLink),
	}
	for name, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered landing missing %s (%q)", name, want)
		}
	}
	// The Mode label must NOT appear anywhere on the landing (the selector was
	// removed from the TUI form in P4; assert against the literal labels since the
	// i18n keys no longer exist).
	for _, leak := range []string{"Режим", "Mode"} {
		if strings.Contains(out, leak) {
			t.Fatalf("rendered landing still shows the Mode label %q", leak)
		}
	}
}

func init() {
	// keep lipgloss import referenced for later tasks even before first use
	_ = lipgloss.Width
	_ = strings.TrimSpace
}
