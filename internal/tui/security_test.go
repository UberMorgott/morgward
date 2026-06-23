package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/tweaks"
)

// secModel builds a phaseSecurity model with a populated access-state card, sized
// for layout/hit-test tests. It mirrors dashModel but enters the Security menu.
func secModel(w, h int) model {
	m := newModel()
	m.w, m.h = w, h
	m.host = "1.2.3.4"
	m.phase = phaseSecurity
	m.dashFacts = &detect.Facts{ID: "ubuntu", VersionID: "24.04"}
	// A safe-default posture: access-policy probes present but NOT applied (image
	// default untouched), so the card shows password root login + key-only=no.
	m.dashAuditRaw = []tweaks.Result{
		{Probe: tweaks.Probe{ID: "a2.permitroot", Step: "A2", Name: "PermitRootLogin"}, Applied: false, Informational: true},
		{Probe: tweaks.Probe{ID: "a2.passauth", Step: "A2", Name: "Password auth"}, Applied: false, Informational: true},
		{Probe: tweaks.Probe{ID: "a2.allowgroups", Step: "A2", Name: "AllowGroups sshusers"}, Applied: false, Informational: true},
	}
	m.populateSecurityState()
	return m
}

// TestSecurityI18nKeysExist asserts every Security-menu key translates to non-empty
// text in BOTH languages (RU + EN parity).
func TestSecurityI18nKeysExist(t *testing.T) {
	keys := []stringKey{
		kSecMenuTitle, kSecRootLogin, kSecKeyOnly, kSecAdmin,
		kSecSafeHeader, kSecCreateAdmin, kSecCryptoKey,
		kSecDangerHeader, kSecKeyOnlyBtn, kSecDangerConfirm,
		kSecHint, kSecRootByPassword, kSecAdminAbsent,
	}
	for _, k := range keys {
		for _, lang := range []Lang{langRU, langEN} {
			if s := t2(lang, k); strings.TrimSpace(s) == "" {
				t.Fatalf("Security key %d empty for lang %d", k, lang)
			}
		}
	}
}

// TestSecurityPhaseRender drives securityView() and asserts the access-state card
// labels + values, the SAFE/DANGER section headers, and all three button labels
// render. It also confirms the danger confirm text renders once secDangerConfirm
// is set (the explicit blocking lockout warning).
func TestSecurityPhaseRender(t *testing.T) {
	m := secModel(100, 40)
	out := m.securityView()
	if strings.TrimSpace(out) == "" {
		t.Fatalf("securityView returned empty content")
	}
	checks := []string{
		strings.TrimSpace(t2(m.lang, kSecMenuTitle)),
		t2(m.lang, kSecRootLogin),
		t2(m.lang, kSecKeyOnly),
		t2(m.lang, kSecAdmin),
		t2(m.lang, kSecSafeHeader),
		t2(m.lang, kSecCreateAdmin),
		t2(m.lang, kSecCryptoKey),
		t2(m.lang, kSecKeyOnlyBtn),
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("securityView missing %q\n%s", want, out)
		}
	}
	// The DANGER header carries a leading "⚠" glyph; assert the localized text minus
	// the glyph appears (truncation on a narrow box could otherwise hide it).
	if !strings.Contains(out, "Опасная") && !strings.Contains(out, "Danger") {
		t.Fatalf("securityView missing DANGER header\n%s", out)
	}

	// Confirm dialog: once raised, the hint line shows the explicit lockout warning.
	m.secDangerConfirm = true
	out2 := m.securityView()
	if !strings.Contains(out2, "потеряете") && !strings.Contains(out2, "lose") {
		t.Fatalf("securityView with secDangerConfirm missing the lockout warning\n%s", out2)
	}
}

// TestSecurityButtonHitTest asserts each Security-menu button resolves to the right
// action when clicked at its rendered pill position (SAFE row: Create/Crypto; DANGER
// row: key-only lockdown).
func TestSecurityButtonHitTest(t *testing.T) {
	m := secModel(100, 40)
	innerW := innerWidth(m.boxWidth())

	safeRow := summaryBodyTopRow + m.secSafeButtonsIndex(innerW)
	safeRanges := pillRanges(m.securitySafeButtonNames(), secButtonStartCol)
	wantSafe := []secButton{secBtnCreateAdmin, secBtnCryptoKey}
	for i, r := range safeRanges {
		x := r[0] + 1
		if got := m.secButtonAtClick(x, safeRow); got != wantSafe[i] {
			t.Fatalf("SAFE button %d click at x=%d row=%d → %v, want %v", i, x, safeRow, got, wantSafe[i])
		}
	}

	dangerRow := summaryBodyTopRow + m.secDangerButtonsIndex(innerW)
	dx := pillRanges(m.securityDangerButtonNames(), secButtonStartCol)[0][0] + 1
	if got := m.secButtonAtClick(dx, dangerRow); got != secBtnKeyOnlyDanger {
		t.Fatalf("DANGER button click at x=%d row=%d → %v, want secBtnKeyOnlyDanger", dx, dangerRow, got)
	}
}

// TestSecurityScrollHitTest is the F10 guard: on a terminal too short to show the
// whole menu, the bottom DANGER button is below the fold at scroll 0 (not hittable),
// and scrolling down brings it into the view region where its hit-test resolves at the
// SCROLLED screen Y. Covers both the offset-aware render geometry and secRowYToBodyIdx
// tracking m.dashScroll (the bug: render+hit-test used a hardcoded offset 0).
func TestSecurityScrollHitTest(t *testing.T) {
	// h=16 → bodyViewH = max(16-7,1) = 9; the menu body is 11 rows, so it overflows.
	m := secModel(100, 16)
	innerW := innerWidth(m.boxWidth())
	body := m.securityBodyLines(innerW)
	viewH := m.secBodyViewH()
	if len(body) <= viewH {
		t.Fatalf("test precondition: body (%d) must overflow viewH (%d)", len(body), viewH)
	}

	dangerIdx := m.secDangerButtonsIndex(innerW)
	dx := pillRanges(m.securityDangerButtonNames(), secButtonStartCol)[0][0] + 1

	// At scroll 0 the DANGER row sits below the fold: even a click at the absolute
	// (unscrolled) row must NOT resolve to the button — it is not rendered there.
	m.dashScroll = 0
	if got := m.secButtonAtClick(dx, summaryBodyTopRow+dangerIdx); got == secBtnKeyOnlyDanger {
		t.Fatalf("DANGER button hittable at scroll 0 while below the fold")
	}

	// Scroll so the DANGER row enters the view region, then click at its SCROLLED Y.
	m.dashScroll = clampScroll(len(body)-viewH, len(body), viewH)
	off := clampScroll(m.dashScroll, len(body), viewH)
	scrolledY := summaryBodyTopRow + (dangerIdx - off)
	if got := m.secButtonAtClick(dx, scrolledY); got != secBtnKeyOnlyDanger {
		t.Fatalf("DANGER button click at scrolled y=%d → %v, want secBtnKeyOnlyDanger", scrolledY, got)
	}
}

// TestSecuritySafeButtonsRunSteps asserts the SAFE buttons start the apply over the
// EXACT engine step IDs: Create admin → ["PRE"], Strengthen crypto → ["PRE","A2-safe"].
// The host is left empty so start()'s validation short-circuits before any dial; the
// handler sets m.command/m.cmds BEFORE calling start().
func TestSecuritySafeButtonsRunSteps(t *testing.T) {
	cases := []struct {
		btn  secButton
		want []string
	}{
		{secBtnCreateAdmin, []string{"PRE"}},
		{secBtnCryptoKey, []string{"PRE", "A2-safe"}},
	}
	for _, tc := range cases {
		m := secModel(100, 40)
		m.inputs[fHost].SetValue("")
		m.inputs[fPass].SetValue("secret")
		next, _ := m.securityAction(tc.btn)
		mm := next.(model)
		if mm.command != "step" {
			t.Fatalf("btn %v: command=%q want \"step\"", tc.btn, mm.command)
		}
		if !reflect.DeepEqual(mm.cmds, tc.want) {
			t.Fatalf("btn %v: cmds=%v want %v", tc.btn, mm.cmds, tc.want)
		}
	}
}

// TestSecurityDangerRequiresConfirm is the lockout guard: the DANGER button must NOT
// apply on the first click — it raises the explicit blocking confirm. Only the
// subsequent Enter launches RunSteps(["A2-danger","A2.5"]).
func TestSecurityDangerRequiresConfirm(t *testing.T) {
	m := secModel(100, 40)
	m.inputs[fHost].SetValue("")
	m.inputs[fPass].SetValue("secret")

	// First trigger → confirm raised, NO apply.
	next, _ := m.securityAction(secBtnKeyOnlyDanger)
	mm := next.(model)
	if !mm.secDangerConfirm {
		t.Fatalf("DANGER action did not raise the lockout confirm")
	}
	if mm.command == "step" {
		t.Fatalf("DANGER action launched the apply before confirm")
	}

	// Enter confirms → launches RunSteps over the danger IDs.
	n2, _ := mm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := n2.(model)
	if m2.command != "step" {
		t.Fatalf("after confirm, command=%q want \"step\"", m2.command)
	}
	want := []string{"A2-danger", "A2.5"}
	if !reflect.DeepEqual(m2.cmds, want) {
		t.Fatalf("danger apply IDs = %v, want %v", m2.cmds, want)
	}
}

// TestSecurityDangerEscCancels asserts esc on the danger confirm cancels it without
// applying anything.
func TestSecurityDangerEscCancels(t *testing.T) {
	m := secModel(100, 40)
	next, _ := m.securityAction(secBtnKeyOnlyDanger)
	mm := next.(model)
	if !mm.secDangerConfirm {
		t.Fatalf("DANGER action did not raise the confirm")
	}
	n2, _ := mm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m2 := n2.(model)
	if m2.secDangerConfirm {
		t.Fatalf("esc did not cancel the danger confirm")
	}
	if m2.command == "step" {
		t.Fatalf("esc launched an apply")
	}
}

// TestDashboardSecurityButtonOpensMenu asserts the Dashboard "Безопасность ▸" button
// navigates to phaseSecurity and populates the access-state card from the audit.
func TestDashboardSecurityButtonOpensMenu(t *testing.T) {
	m := dashModel(100, 40)
	m.dashAuditRaw = []tweaks.Result{
		{Probe: tweaks.Probe{ID: "a2.permitroot", Step: "A2", Name: "PermitRootLogin"}, Applied: false},
		{Probe: tweaks.Probe{ID: "a2.passauth", Step: "A2", Name: "Password auth"}, Applied: false},
	}
	innerW := innerWidth(m.boxWidth())
	btnRow := m.dashButtonsRowY(innerW) // FIXED screen Y
	secX := pillRanges(m.dashButtonNames(), dashButtonStartCol)[1][0] + 1

	next, _ := m.dashboardClick(secX, btnRow)
	mm := next.(model)
	if mm.phase != phaseSecurity {
		t.Fatalf("Security button → phase %v, want phaseSecurity", mm.phase)
	}
	// Access-state card populated from the audit (root login by password, key-only no).
	if mm.secRootLoginState != t2(mm.lang, kSecRootByPassword) {
		t.Fatalf("secRootLoginState=%q want %q", mm.secRootLoginState, t2(mm.lang, kSecRootByPassword))
	}
	if mm.secKeyOnlyState != t2(mm.lang, kNoWord) {
		t.Fatalf("secKeyOnlyState=%q want %q", mm.secKeyOnlyState, t2(mm.lang, kNoWord))
	}
}

// TestSecurityStateNeutralPlaceholder asserts that when an access-policy probe is
// missing from the audit, the corresponding card field shows the neutral "—"
// placeholder rather than asserting an unobserved state.
func TestSecurityStateNeutralPlaceholder(t *testing.T) {
	m := newModel()
	m.host = "h"
	m.dashAuditRaw = nil // no audit yet
	m.populateSecurityState()
	for name, got := range map[string]string{
		"root":  m.secRootLoginState,
		"key":   m.secKeyOnlyState,
		"admin": m.secAdminState,
	} {
		if got != "—" {
			t.Fatalf("%s state=%q want neutral placeholder \"—\"", name, got)
		}
	}
}

// TestFormHasNoModeToggle is the form guard: the (removed) soft/strict mode
// selector must not render on the landing form. Asserts against literal mode
// labels (modes and their i18n keys were removed entirely).
func TestFormHasNoModeToggle(t *testing.T) {
	m := formModel(80, 24)
	for _, adv := range []bool{false, true} {
		m.advancedOpen = adv
		out := m.formView()
		for _, leak := range []string{"Режим", "Mode", "строгий", "strict", "мягкий"} {
			if strings.Contains(out, leak) {
				t.Fatalf("landing form (advancedOpen=%v) leaked mode label %q", adv, leak)
			}
		}
	}
}

// TestSecurityBackPillHitTest asserts the Security menu renders a clickable "← Назад"
// pill that resolves at its drawn position, misses off-target, and returns to the
// Dashboard when clicked.
func TestSecurityBackPillHitTest(t *testing.T) {
	m := secModel(100, 40)
	backY := m.secBackRow()
	backX := pillRanges([]string{t2(m.lang, kWikiBack)}, wikiBackStartCol)[0][0] + 1
	if !m.secBackAtClick(backX, backY) {
		t.Fatalf("security back pill click at x=%d y=%d did not register", backX, backY)
	}
	if m.secBackAtClick(backX, backY+1) {
		t.Fatalf("security back pill matched one row off the pill row")
	}
	if m.secBackAtClick(95, backY) {
		t.Fatalf("security back pill matched past its rendered width")
	}
	if !strings.Contains(m.securityView(), t2(m.lang, kWikiBack)) {
		t.Fatalf("securityView missing the back pill label")
	}
	next, _ := m.Update(tea.MouseClickMsg{X: backX, Y: backY, Button: tea.MouseLeft})
	if next.(model).phase != phaseDashboard {
		t.Fatalf("security back click → phase %v, want phaseDashboard", next.(model).phase)
	}
}

// TestSecurityBackPillSwallowedByDangerConfirm asserts that while the danger confirm is
// armed, a click on the back pill is swallowed (does not navigate), mirroring the
// existing modal-swallow behavior for the action buttons.
func TestSecurityBackPillSwallowedByDangerConfirm(t *testing.T) {
	m := secModel(100, 40)
	m.secDangerConfirm = true
	backY := m.secBackRow()
	backX := pillRanges([]string{t2(m.lang, kWikiBack)}, wikiBackStartCol)[0][0] + 1
	next, _ := m.Update(tea.MouseClickMsg{X: backX, Y: backY, Button: tea.MouseLeft})
	mm := next.(model)
	if mm.phase != phaseSecurity {
		t.Fatalf("back click while danger-confirm armed navigated to %v; want it swallowed (phaseSecurity)", mm.phase)
	}
}

// TestSecurityModelValueCopySafe guards the value-copy invariant: the Security state
// fields are plain strings/bool, so a copied model carries an independent snapshot.
func TestSecurityModelValueCopySafe(t *testing.T) {
	m := secModel(100, 40)
	cp := m // value copy
	cp.secRootLoginState = "MUTATED"
	cp.secDangerConfirm = true
	if m.secRootLoginState == "MUTATED" {
		t.Fatalf("value copy aliased secRootLoginState — model must be copy-safe")
	}
	if m.secDangerConfirm {
		t.Fatalf("value copy aliased secDangerConfirm")
	}
}
