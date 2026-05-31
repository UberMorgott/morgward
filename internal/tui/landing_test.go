package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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
	lines := m.framedInputRow(fHost, m.lang, "Хост", m.inputs[fHost], true)
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
// positioned in the lower-right cluster (after Mode and its contextual Help row).
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
	modeIdx := kindIndex(rows, frMode)
	helpIdx := kindIndex(rows, frHelp)
	if !(logIdx > modeIdx && logIdx > helpIdx) {
		t.Fatalf("frLog idx=%d should be after frMode=%d and frHelp=%d", logIdx, modeIdx, helpIdx)
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

// TestCatalogLinkRendered asserts the catalog-link label appears as a form row
// (a P5 stub: a static label, not yet a clickable navigation target).
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
	// Stub: clicking it must NOT be a registered hit target (no navigation yet).
	hit := m.formHitAtClick(0, formBodyTopRow+idx)
	if hit.ok {
		t.Fatalf("catalog link should be a non-clickable stub, got hit kind=%v", hit.kind)
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
		case frMode:
			hit := m.formHitAtClick(pillX, y)
			if !hit.ok || hit.kind != frMode {
				t.Fatalf("row %d (frMode): hit=%+v", i, hit)
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
	focused := m.framedInputRow(fHost, m.lang, "Хост", m.inputs[fHost], true)
	dim := m.framedInputRow(fHost, m.lang, "Хост", m.inputs[fHost], false)
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

func init() {
	// keep lipgloss import referenced for later tasks even before first use
	_ = lipgloss.Width
	_ = strings.TrimSpace
}
