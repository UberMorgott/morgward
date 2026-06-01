package tui

import (
	tea "charm.land/bubbletea/v2"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/UberMorgott/morgward/internal/version"
)

// populateSecurityState derives the three access-state card fields from the audit
// results already in the model (m.dashAuditRaw), so the Security menu shows the
// current posture without a fresh probe. Mapping uses the access-policy probes:
//   - root login: a2.permitroot Applied → "no"; else "разрешён по паролю"/"by password"
//   - key-only:   a2.passauth   Applied → yes; else no
//   - admin:      a2.allowgroups Applied → "vpsadmin@host" (an sshusers group exists,
//     so the admin handoff is in place); else a neutral "отсутствует"/"absent"
//
// When a probe is missing from the audit, the field shows a neutral placeholder
// ("—") rather than asserting a state we did not observe.
func (m *model) populateSecurityState() {
	const placeholder = "—"
	m.secRootLoginState = placeholder
	m.secKeyOnlyState = placeholder
	m.secAdminState = placeholder

	applied := map[string]bool{}
	seen := map[string]bool{}
	for _, r := range m.dashAuditRaw {
		applied[r.Probe.ID] = r.Applied
		seen[r.Probe.ID] = true
	}

	if seen["a2.permitroot"] {
		if applied["a2.permitroot"] {
			m.secRootLoginState = t(m.lang, kNoWord)
		} else {
			m.secRootLoginState = t(m.lang, kSecRootByPassword)
		}
	}
	if seen["a2.passauth"] {
		m.secKeyOnlyState = m.boolWordL(applied["a2.passauth"])
	}
	if seen["a2.allowgroups"] {
		if applied["a2.allowgroups"] {
			m.secAdminState = defaultAdminUser + "@" + m.host
		} else {
			m.secAdminState = t(m.lang, kSecAdminAbsent)
		}
	}
}

// --- Security menu (phaseSecurity) --------------------------------------------
//
// securityView renders the Security + access menu: a framed access-state card (3
// lines: root login / key-only / admin), a SAFE section (Create-admin + Strengthen-
// crypto buttons) and a DANGER section (one key-only-lock button). Chrome (titled
// top, switcher, scroll region, hint, bottom border, monitor box) mirrors
// dashboardView exactly so the footer never moves. The two button rows are resolved
// against the SAME ordered body slice the renderer iterates (securityBodyLines), so
// the hit-test geometry can never drift.
func (m model) securityView() string {
	bw := m.boxWidth()
	innerW := innerWidth(bw)
	b := lipgloss.RoundedBorder()

	body := m.securityBodyLines(innerW)

	var sb strings.Builder
	sb.WriteString(titledTop(b, " "+version.Name+" v"+version.Version+" ", bw))
	sb.WriteByte('\n')
	sb.WriteString(m.switcherLine(b, innerW))
	sb.WriteByte('\n')

	viewH := m.bodyViewH()
	off := clampScroll(m.dashScroll, len(body), viewH)
	m.renderScrollRegion(&sb, b, body, innerW, viewH, off)

	hint := t(m.lang, kSecHint)
	if m.secDangerConfirm {
		hint = t(m.lang, kSecDangerConfirm)
	}
	sb.WriteString(contentLine(b, helpStyle.Render(hint), innerW))
	sb.WriteByte('\n')
	sb.WriteString(borderLine(b.BottomLeft, b.Bottom, b.BottomRight, bw))
	sb.WriteByte('\n')

	sb.WriteString(m.monitorBox(innerW))
	return sb.String()
}

// securitySafeButtonNames is the ordered SAFE-section button labels (Create admin,
// Strengthen crypto). Single source for the render path and the hit-test.
func (m model) securitySafeButtonNames() []string {
	return []string{
		t(m.lang, kSecCreateAdmin),
		t(m.lang, kSecCryptoKey),
	}
}

// securityDangerButtonNames is the ordered DANGER-section button labels (just the
// key-only lockdown button). Single source for the render path and the hit-test.
func (m model) securityDangerButtonNames() []string {
	return []string{
		t(m.lang, kSecKeyOnlyBtn),
	}
}

// secButtonStartCol is the absolute X where a button-row's first pill begins: 2
// (left border + space) + 1 (the leading indent space in the row). Mirrors
// dashButtonStartCol.
const secButtonStartCol = 3

// secButtonsLine renders a row of pills (the leading indent puts the first pill at
// secButtonStartCol), mirroring dashButtonsLine so pillRanges recovers the geometry.
func secButtonsLine(names []string) string {
	pills := make([]string, len(names))
	for i, n := range names {
		pills[i] = pillStyle.Render(n)
	}
	return " " + strings.Join(pills, " ")
}

// securityAccessCard renders the framed "Безопасность и доступ" card with the three
// access-state lines (root login / key-only / admin) from the sec*State fields, as
// content lines fitted to innerW. Mirrors dashServerCard's framing.
func (m model) securityAccessCard(innerW int) []string {
	bd := lipgloss.RoundedBorder()
	fw := max(innerW, minBoxWidth)
	finner := fw - 2 // cells between the card's border runes

	top := titledTop(bd, t(m.lang, kSecMenuTitle), fw)
	bottom := borderLine(bd.BottomLeft, bd.Bottom, bd.BottomRight, fw)

	mid := func(content string) string {
		content = " " + content
		content = truncDisplay(content, finner)
		if pad := finner - lipgloss.Width(content); pad > 0 {
			content += strings.Repeat(" ", pad)
		}
		return borderStyle.Render(bd.Left) + content + borderStyle.Render(bd.Right)
	}

	rootV := m.secRootLoginState
	keyV := m.secKeyOnlyState
	adminV := m.secAdminState
	if rootV == "" {
		rootV = "—"
	}
	if keyV == "" {
		keyV = "—"
	}
	if adminV == "" {
		adminV = "—"
	}

	lines := []string{top}
	lines = append(lines, mid(labelStyle.Render(t(m.lang, kSecRootLogin)+": ")+rootV))
	lines = append(lines, mid(labelStyle.Render(t(m.lang, kSecKeyOnly)+": ")+keyV))
	lines = append(lines, mid(labelStyle.Render(t(m.lang, kSecAdmin)+": ")+adminV))
	lines = append(lines, bottom)
	return lines
}

// securityBodyLines builds the ordered Security-menu body slice — the single source
// of truth for BOTH securityView's render and the button hit-test. Order: access
// card (framed), blank, SAFE header, SAFE buttons, blank, DANGER header, DANGER
// button. The two button rows' body indices are recovered by secSafeButtonsIndex /
// secDangerButtonsIndex (computed from the dynamic card height).
func (m model) securityBodyLines(innerW int) []string {
	var body []string
	body = append(body, m.securityAccessCard(innerW)...)
	body = append(body, "")
	body = append(body, sumHeadStyle.Render(t(m.lang, kSecSafeHeader)))
	body = append(body, secButtonsLine(m.securitySafeButtonNames()))
	body = append(body, "")
	body = append(body, errStyle.Render(t(m.lang, kSecDangerHeader)))
	body = append(body, secButtonsLine(m.securityDangerButtonNames()))
	return body
}

// secSafeButtonsIndex / secDangerButtonsIndex are the body-slice indices of the SAFE
// and DANGER button rows. Prefix: card (N lines) + blank + SAFE header → SAFE buttons
// at len(card)+2; then blank + DANGER header → DANGER buttons at len(card)+5.
func (m model) secSafeButtonsIndex(innerW int) int {
	return len(m.securityAccessCard(innerW)) + 2
}

func (m model) secDangerButtonsIndex(innerW int) int {
	return len(m.securityAccessCard(innerW)) + 5
}

// secButton enumerates the three Security-menu actions resolved by secButtonAtClick.
type secButton int

const (
	secBtnNone secButton = iota
	secBtnCreateAdmin
	secBtnCryptoKey
	secBtnKeyOnlyDanger
)

// secRowYToBodyIdx maps a screen Y to a Security-menu body-slice index, honoring the
// scroll offset (m.dashScroll, clamped against the body), or ok=false when Y is in the
// chrome. Mirrors dashRowYToBodyIdx so button hit-tests track the ↑↓/wheel scroll —
// otherwise the bottom DANGER/SAFE buttons clip unreachable on a short terminal.
func (m model) secRowYToBodyIdx(y int) (int, bool) {
	body := m.securityBodyLines(innerWidth(m.boxWidth()))
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

// secButtonAtClick maps a click at (x,y) to one of the Security-menu buttons, using
// pillRanges over the SAFE/DANGER button names (the same geometry secButtonsLine
// renders), or secBtnNone on a miss.
func (m model) secButtonAtClick(x, y int) secButton {
	if m.phase != phaseSecurity {
		return secBtnNone
	}
	innerW := innerWidth(m.boxWidth())
	bodyIdx, ok := m.secRowYToBodyIdx(y)
	if !ok {
		return secBtnNone
	}
	switch bodyIdx {
	case m.secSafeButtonsIndex(innerW):
		switch pillIndexAt(m.securitySafeButtonNames(), secButtonStartCol, x) {
		case 0:
			return secBtnCreateAdmin
		case 1:
			return secBtnCryptoKey
		}
	case m.secDangerButtonsIndex(innerW):
		if pillIndexAt(m.securityDangerButtonNames(), secButtonStartCol, x) == 0 {
			return secBtnKeyOnlyDanger
		}
	}
	return secBtnNone
}

// securityAction performs the Security-menu action for btn. SAFE actions start an
// apply immediately (RunSteps). The DANGER action is two-step: the first invocation
// raises the explicit blocking confirm (secDangerConfirm) and the apply only launches
// once the operator confirms (handled in the phaseSecurity key handler), which routes
// through phaseKey so the generated key is shown BEFORE the lockdown applies.
func (m model) securityAction(btn secButton) (tea.Model, tea.Cmd) {
	switch btn {
	case secBtnCreateAdmin:
		return m.startSteps([]string{"PRE"})
	case secBtnCryptoKey:
		return m.startSteps([]string{"PRE", "A2-safe"})
	case secBtnKeyOnlyDanger:
		// Raise the explicit lockout confirm; apply launches on Enter (key handler).
		m.secDangerConfirm = true
		return m, nil
	}
	return m, nil
}

// launchKeyOnlyDanger runs the opt-in key-only lockdown: A2-danger (AllowGroups +
// PermitRootLogin no + PasswordAuthentication no + passwd -l root, behind the
// existing ssh-revert safety timer + freshLogin key-only verify) then A2.5 (cloud-
// init neutralization). The generated key is surfaced to phaseKey by the connMsg
// handler (KeyGenerated path) BEFORE the lockdown applies, so the operator can copy
// it first.
func (m model) launchKeyOnlyDanger() (tea.Model, tea.Cmd) {
	return m.startSteps([]string{"A2-danger", "A2.5"})
}

// securityClick resolves a Security-menu click to one of the three buttons. A pending
// danger confirm swallows clicks (resolve with Enter/esc on the hint), mirroring the
// Dashboard apply-confirm pattern so a stray click can never bypass the lockout
// warning.
func (m model) securityClick(x, y int) (tea.Model, tea.Cmd) {
	if m.secDangerConfirm {
		return m, nil
	}
	switch m.secButtonAtClick(x, y) {
	case secBtnCreateAdmin:
		return m.securityAction(secBtnCreateAdmin)
	case secBtnCryptoKey:
		return m.securityAction(secBtnCryptoKey)
	case secBtnKeyOnlyDanger:
		return m.securityAction(secBtnKeyOnlyDanger)
	}
	return m, nil
}
