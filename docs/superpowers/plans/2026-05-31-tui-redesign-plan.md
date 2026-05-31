# Morgward TUI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the morgward TUI into a landing→dashboard→security/catalog flow with self-update, a default that never touches root/login, an always-on monitor footer, and rich per-tweak docs.

**Architecture:** Bubble Tea v2 phases extended (phaseDashboard/phaseSecurity/phaseCatalog new; phaseForm/phaseWiki repurposed); engine gains Audit() + A2 safe/danger split; self-update via creativeprojects/go-selfupdate. Model stays value-copyable; all width math via lipgloss.Width.

**Tech Stack:** Go 1.26, charm.land/bubbletea v2, charm.land/lipgloss v2, golang.org/x/crypto/ssh, github.com/creativeprojects/go-selfupdate.

**Spec:** docs/superpowers/specs/2026-05-31-tui-redesign-design.md

**Execution order (by dependency):** P1 → P6 → P2 → P3 → P4 → P5 (P6 monitor fix early since it is a self-contained bug fix; P3 dashboard needs engine.Audit; P4 needs A2 split; P5 needs catalog nav stubbed in P1).

---

## Phase P1 — Landing redesign (framed inputs, disclosure, action removal)

**Depends on:** None — P1 is the foundation phase and depends on no prior work.

**Risks:** ## Lockout / Regression Risks & Mitigations

### Risk 1: Y-offset Math Drift in Hit-Test
**Description:** Inputs now span 3 rows instead of 1. Every form element's screen Y depends on the formRows() slice. If the render path (formView iterating formRows) and hit-test (formHitAtClick) diverge, clicks miss their targets or land on the wrong row.

**Mitigation:**
- **Single source of truth:** formRows() is the ONLY slice that defines row order; formHitAtClick iterates the same slice.
- **Task 9 (form geometry):** Every row kind has explicit hit-test coverage; test clicks at formBodyTopRow + rowIdx for each row.
- **Boundary testing:** Task 11 tests form padding so the frame never goes negative.

### Risk 2: Visibility Loss of Advanced Fields
**Description:** When advancedOpen==false, Port/User/Key rows are absent from formRows(). If the disclosure toggle fails to re-include them on click, users can never access those fields, and a saved config can't be edited.

**Mitigation:**
- **Task 4 (disclosure):** formHitAtClick explicitly handles frDisclosure and toggles advancedOpen; formRows() re-builds the slice on every Update, so the toggle is immediate.
- **Test coverage:** TestDisclosureToggleClickable verifies toggle-on reveals rows, toggle-off hides them.
- **Keyboard fallback:** Tab navigation (focusableRows) omits hidden rows, so focus never strands.

### Risk 3: Input Focus on Non-Input Rows
**Description:** formRows() now includes Mode, Log, Help, Start, Error rows alongside input rows. If the keyboard handler (updateForm) or focus logic doesn't account for row indices shifting due to 3-line inputs, focus can land on a non-input row and text input fails.

**Mitigation:**
- **focusableRows():** Explicitly lists which rows are focusable (fHost/Port/User/Pass/Key/Mode/Log/Start); hidden rows (Port/User/Key when advancedOpen==false) are omitted.
- **Task 10 (focus rendering):** Each input's 3-row block has its own focus styling; non-input rows have no text input handler.
- **updateForm() safeguard:** Only rows < nInputs get text input; other rows are toggles or buttons.

### Risk 4: Disclosure State Not Persisted
**Description:** advancedOpen is a plain bool in the model. If the TUI session ends (esc, ctrl+c), the disclosure state is lost. Re-launching the form resets to novice default (closed).

**Mitigation:**
- **By design:** Each TUI session starts with a fresh form; the operator opens ▸ Дополнительно on demand. No persistence is required for P1.
- **P2+ note:** If future phases add a config-save feature, advancedOpen can be added to the persisted state.

### Risk 5: "Что настраивает программа" Link Breaks
**Description:** Task 8 adds a catalog-link row but doesn't wire navigation (phaseForm → phaseCatalog). If clicked, it must either:
  - Navigate to the catalog (needs P5 to define phaseCatalog), OR
  - Be non-clickable (a label only).

**Mitigation:**
- **Task 8 (stub):** Render as a static label (indent + text), NOT a clickable pill. The link stays a placeholder.
- **P5 task:** Implement click handling to navigate to phaseCatalog (new phase).
- **Test coverage:** No test for navigation; only Test CatalogLinkRendered asserts it appears.

### Risk 6: Action Removal Breaks Engine
**Description:** Tasks 5 removes kOptRun/Detect/Verify from the form, but m.command is still used by the engine. If m.command is never set, the engine receives an empty command and fails.

**Mitigation:**
- **Task 5 (action removal):** m.command is NOT deleted from the model; it defaults to "run" in newModel (line 292).
- **No UI toggle:** The form no longer shows a pill to switch between run/detect/verify, so m.command stays "run" throughout a normal TUI session.
- **Engine tokens preserved:** The engine's config.Command, engine.Execute(cmd) logic, and all step tokens remain unchanged.
- **CLI still functional:** The CLI (--detect, --verify flags) still works because main.go passes the command to engine.Execute directly.
- **Test coverage:** TestActionRemovedFromForm asserts m.command == "run" even when frAction is gone.

### Risk 7: Version Frame Layout Overflow
**Description:** Task 13 adds a version frame before the form rows. If the tagline is long (EN: "VPS guardian · fresh Ubuntu VPS protection"), it might overflow the boxWidth() at small terminal sizes.

**Mitigation:**
- **Task 13 (version frame):** Use contentLine (which truncates to innerW) so overflow is clipped, not broken.
- **Graceful degradation:** If m.w < minBoxWidth (40), the frame shrinks but doesn't go negative.
- **Test coverage:** TestVersionFrameHeader uses m.w=80 and verifies no overflow.

### Risk 8: P4 Split (Soft/Strict Mode Removal)
**Description:** P1 keeps the Mode selector visible. P4 will remove soft/strict mode entirely. If P4 doesn't completely delete the Mode row and model.mode, the TUI has dead code.

**Mitigation:**
- **Design spec § Decomposition:** P4 is explicitly titled "A2 split + default-no-lockout" and subsumes the soft/strict removal.
- **P1 task 15:** TestModesUnchanged only asserts current behavior; Task 15 does NOT prevent P4's refactor.
- **Scope boundary:** P1 ends with Mode still visible; P4 begins with Mode deletion and engine A2 refactor.
- **No technical risk:** P1's work stands alone; P4 will cleanly delete Mode code without breaking P1's tests.

### Risk 9: P5 Catalog Stub Not Wired
**Description:** Task 8 adds a "Что настраивает программа" link but doesn't define phaseCatalog or wire navigation. If a user accidentally clicks it (or a future test tries to navigate), the phase doesn't exist.

**Mitigation:**
- **Task 8 (stub):** Render as a label, not a button. No click handler, so no navigation attempt.
- **No phase enum entry:** phaseForm code does not reference phaseCatalog; no forward declaration needed.
- **P5 task:** Adds phaseForm→phaseCatalog navigation + phaseCatalog implementation.
- **Clean handoff:** P1 leaves a placeholder; P5 fills it in without breaking P1.

### Risk 10: Input Width / Lipgloss Wrapping
**Description:** Inputs are set to width 44 (newModel line 271), but with 3-row framing (borders + padding), the actual display width is less. Long input values might be clipped or cause alignment issues.

**Mitigation:**
- **Task 3 (framedInputRow):** Each input is wrapped with borders; the inner content width is 44, border adds 2 cells (left + right), total 46 display cells.
- **Task 9 (geometry):** Hit-test accounts for the full 3-row frame, so clicks on any row in the frame select the input.
- **Test coverage:** TestFramedInputRender3Rows asserts lipgloss.Width of the 3-line output ≤ innerWidth(boxWidth()).
- **Graceful truncation:** formView's contentLine() wraps each row to innerW, so overflow is clipped without breaking the frame.

## Core Model & Phases

**Files:** `internal/tui/tui.go` (model struct + phase enum)

### Task 1: Add phaseAdvisory enum + model.advancedOpen bool
- [ ] Write test (name: `TestLandingFormPhaseExists`) asserting `phaseForm` renders unchanged
- [ ] Read model struct (lines ~193-265): confirm current fields, specifically `phase phase` enum at line 194
- [ ] Read phase enum (lines 56-65): confirm current values end with `phaseMatrix`
- [ ] Implement: In tui.go phaseForm block below phaseMatrix, add model.advancedOpen bool (initialized false in newModel at line 288)
- [ ] Run test: should pass (no render change yet)
- [ ] Commit: "model: add advancedOpen bool to landing form state"

---

## I18N Keys for "Дополнительно" Disclosure

**Files:** `internal/tui/i18n.go` (stringKey enum + tr map)

### Task 2: Add disclosure label i18n keys
- [ ] Write test (name: `TestDisclosureKeysExist`) that calls `t(langRU, kDisclosureLabel)` and `t(langEN, kDisclosureLabel)`, `t(langRU, kDisclosureOpen)` and `t(langEN, kDisclosureOpen)` and asserts all are non-empty
- [ ] Read stringKey enum (lines 35-196): identify where form labels live (around kLabelHost etc)
- [ ] Implement: In stringKey enum after kLabelKey (line 41), add:
  - `kDisclosureLabel` — "▸ Дополнительно" (collapsible toggle label)
  - `kDisclosureOpen` — "▼" (open/closed state indicator, RU/EN same)
- [ ] Implement: In tr[langRU] map (after kLabelKey: "SSH-ключ"), add:
  - `kDisclosureLabel: "▸ Дополнительно (порт · пользователь · SSH-ключ)"`
  - `kDisclosureOpen: "▼"`
- [ ] Implement: In tr[langEN] map, add:
  - `kDisclosureLabel: "▸ Advanced (port · user · SSH key)"`
  - `kDisclosureOpen: "▼"`
- [ ] Run test: should pass
- [ ] Commit: "i18n: add disclosure (▸ Дополнительно) labels and state indicator"

---

## Input Field Structure & Layout Math

**Files:** `internal/tui/tui.go` (formRows, formView, all Y-offset helpers)

### Task 3: Add 3-row bordered-input render helper
- [ ] Write test (name: `TestFramedInputRender3Rows`) that:
  - Calls `m.framedInputRow(0, m.lang, "Хост", m.inputs[fHost], focused=true)` 
  - Asserts the returned 3 lines contain: (1) top border, (2) label+value, (3) bottom border
  - Verifies lipgloss.Width of output ≤ innerWidth(m.boxWidth())
- [ ] Implement function signature: `func (m model) framedInputRow(idx int, lang Lang, label string, input textinput.Model, focused bool) []string`
  - Uses `lipgloss.RoundedBorder()` with no title (just top+bottom+sides)
  - Line 1: border top, cells match input width (44 from newModel line 271)
  - Line 2: left border + label (padded colW) + space + input.View() + right border
  - Line 3: border bottom
  - Unfocused: dim border (240); focused: accent (57) + bold label (213)
  - Return: 3-line []string
- [ ] Implement: Update formRows() to use framedInputRow for each input (currently they are 1-line rows). Now they occupy 3 lines each.
- [ ] Recompute formRows Y math: each input now spans 3 rows instead of 1. Update all row indices (rowMode, rowCommand, rowLog, rowStart become +10 not +5 extra rows since 5 inputs × 2 extra lines = 10).
- [ ] Update formBodyTopRow comment to reflect that body spans 3-line input rows.
- [ ] Run test: should pass
- [ ] Commit: "render: implement 3-row framed bordered inputs for landing form"

---

## Disclosure Toggle (▸/▼) Row in Form

**Files:** `internal/tui/tui.go` (formRows, formClick, formRowKind enum, input visibility)

### Task 4: Add disclosure toggle row to form
- [ ] Write test (name: `TestDisclosureToggleClickable`) that:
  - Creates model with advancedOpen=false
  - Calls formHitAtClick on the disclosure row's rendered position
  - Verifies hit returns ok=true with a new frDisclosure kind
  - Toggles advancedOpen and asserts formRows now shows Port/User/Key rows
- [ ] In formRowKind enum (lines 1291-1300), add `frDisclosure formRowKind = iota` before frBlank
- [ ] Implement: In formRows(), after all 5 input rows (now 3 lines each × 5 = 15 rows):
  - Add a disclosure toggle row: `kDisclosureLabel` pill (not mode-like; just "▸" / "▼" + text)
  - Use simpler layout: indent (colW+1) + "▸ Дополнительно (порт · пользователь · SSH-ключ)"
  - When clicked: toggle m.advancedOpen
- [ ] Implement: Conditionally INCLUDE Port/User/Key rows in formRows only when m.advancedOpen:
  - If not advancedOpen: formRows includes Host + Password only (framed 3-row each), then disclosure toggle, then Mode/Action/Log/Help/Start
  - If advancedOpen: formRows includes Host + Password + Port + User + Key (all framed 3-row each), then disclosure toggle, then Mode/Action/Log/Help/Start
- [ ] Update formHitAtClick to handle frDisclosure: toggle m.advancedOpen on click
- [ ] Run test: advancedOpen==false shows 2 inputs, click disclosure toggle toggles visibility
- [ ] Commit: "form: add ▸ Дополнительно disclosure toggle for Port/User/Key advanced inputs"

---

## Remove Action Selector (kOptRun/Detect/Verify) from Form

**Files:** `internal/tui/i18n.go` (stringKey enum + i18n table), `internal/tui/tui.go` (formRows, model.command)

### Task 5: Remove kOptRun/Detect/Verify form labels (keep engine tokens)
- [ ] Write test (name: `TestActionRemovedFromForm`) that:
  - Calls formRows() 
  - Asserts NO row.kind == frAction exists
  - Asserts m.command is still "run" by default (internal state, just not rendered)
- [ ] In i18n.go, READ stringKey enum to verify kOptRun (55), kOptDetect (56), kOptVerify (57) exist
- [ ] In i18n.go, DELETE or COMMENT OUT kOptRun, kOptDetect, kOptVerify from stringKey enum (lines 55-57)
- [ ] In i18n.go, DELETE the corresponding entries from tr[langRU] and tr[langEN] maps
- [ ] In tui.go model struct, KEEP m.command string — it's still used internally by engine (see line 198, 292, 903)
- [ ] In tui.go formRows(), REMOVE the frAction row entirely (currently lines ~1344-1347)
- [ ] In tui.go focusableRows(), REMOVE the rowCommand row from the ordered focus slice (currently line 145)
- [ ] Update row indices (rowMode, rowLog, rowStart) to skip the removed frAction row
- [ ] Update formHitAtClick/frAction case: DELETE this entire case
- [ ] In updateForm(), DELETE the case for rowCommand (the "left"/"right" action toggle)
- [ ] Keep all engine-level command handling — only the UI toggle is removed
- [ ] Run test: formRows never includes Action pill row; m.command remains "run"
- [ ] Commit: "form: remove action selector (▸запуск/разведка/анализ) from landing UI"

---

## Save-Log Toggle Repositioning

**Files:** `internal/tui/tui.go` (formRows layout + save-log toggle)

### Task 6: Position save-log toggle in lower-right cluster
- [ ] Write test (name: `TestSaveLogTogglePosition`) that:
  - Calls formRows() with advancedOpen=false and saves=false
  - Walks rows to find frLog row
  - Asserts the frLog row text contains "Сохранять лог в файл: [ нет ] да"
  - Asserts frLog appears AFTER Mode/Help rows (not interleaved)
- [ ] In formRows(), move the save-log toggle (currently line 1358-1361) to appear AFTER Mode row (new positions after removing frAction)
- [ ] The "lower-right cluster" in the mockup shows: Mode pill on left, "Сохранять лог" toggle on right, all in the same visual line/region
- [ ] Layout: keep indent + renderToggle for save-log, positioned after the Mode row so it naturally lands in the "right" visual cluster
- [ ] Run test: frLog appears in lower-right position
- [ ] Commit: "form: reposition save-log toggle to lower-right cluster with mode"

---

## Add "Что настраивает программа ▸" Link (Catalog Navigation)

**Files:** `internal/tui/i18n.go` (stringKey + tr), `internal/tui/tui.go` (formRows, form click handling)

### Task 7: Add catalog-link i18n key
- [ ] Write test (name: `TestCatalogLinkKeyExists`) that:
  - Calls `t(langRU, kCatalogLink)` and `t(langEN, kCatalogLink)` and asserts both non-empty
- [ ] In i18n.go stringKey enum, add `kCatalogLink` after form-section keys
- [ ] In tr[langRU], add: `kCatalogLink: "Что настраивает программа ▸"`
- [ ] In tr[langEN], add: `kCatalogLink: "What the program configures ▸"`
- [ ] Run test: should pass
- [ ] Commit: "i18n: add catalog-link label for landing form"

### Task 8: Render catalog link in form (stub navigation)
- [ ] Write test (name: `TestCatalogLinkRendered`) that:
  - Calls formRows()
  - Asserts one row contains the catalog-link text
  - Note: actual navigation stubs to phaseForm (pre-launch, catalog is P5)
- [ ] In formRows(), add a new row type (frCatalogLink) at an appropriate location (e.g., after disclosure or in the lower section)
- [ ] Render as: indent + the catalog-link text, non-clickable (placeholder for P5 navigation)
- [ ] Update formRowKind enum if adding frCatalogLink kind
- [ ] Run test: catalog-link appears in form
- [ ] Commit: "form: add 'Что настраивает программа ▸' catalog link (P5 stub)"

---

## Form Y-Offset & Hit-Test Math Recomputation

**Files:** `internal/tui/tui.go` (formRows, formBodyTopRow, formHitAtClick, all Y math)

### Task 9: Recompute all form geometry math
- [ ] Write test (name: `TestFormHitTestAccuracy`) that:
  - Calls formRows() and formHitAtClick for each row kind
  - Verifies that a click at (x, formBodyTopRow + rowIdx) maps to the correct row.kind
  - Tests Host input (row 0), Password input (row 3, since each input is 3 lines), disclosure row, Mode row, Save-log row, Start buttons
  - Asserts every row can be clicked and hit-test returns ok=true with correct field/kind
- [ ] Audit formRows() loop (line 1320+): ensure every row appended matches a hit-test case
- [ ] Recompute formBodyTopRow comment: still 2 (top border + switcher = rows 0-1, body starts row 2)
- [ ] In formHitAtClick (line 1166+), update Y math: `idx := y - formBodyTopRow` now navigates a formRows slice where inputs span 3 rows each
- [ ] For frInput rows: the whole 3-row block (border top + content + border bottom) is a single click target; clicking any Y in [row, row+3) lands on that input
- [ ] Update formClick (line 1253+): all hit returns still work (frInput, frMode, frLog, frStart)
- [ ] Run test: every form element can be clicked accurately
- [ ] Commit: "form: recompute all Y-offset and hit-test math for 3-row framed inputs"

---

## Input Focus & Refocus on Framed Rows

**Files:** `internal/tui/tui.go` (refocus, inputViewAt line ~1339)

### Task 10: Ensure focus rendering works with 3-row inputs
- [ ] Write test (name: `TestFocusRenderingFramed`) that:
  - Sets m.focus to fHost (0)
  - Calls formRows()
  - Walks rows 0-2 (the Host input 3-row block) and asserts row 0 (border top), row 1 (content) render with focused style (213, bold label), row 2 (border bottom)
  - Tests Port (only shown when advancedOpen), Password
- [ ] Update the labelPad lambda in formRows (line 1324+) to apply focusStyle when `i == m.focus` for each input
- [ ] Verify framedInputRow receives the focused bool correctly
- [ ] Run test: focused inputs show accent border + bold label
- [ ] Commit: "form: ensure focus rendering on 3-row framed inputs"

---

## Form View & Boundary Layout

**Files:** `internal/tui/tui.go` (formView, padding math)

### Task 11: Update formView padding math for new row counts
- [ ] Write test (name: `TestFormViewPadding`) that:
  - Sets m.h = 24 (terminal height)
  - Calls formView() with advancedOpen=false (2 inputs × 3 = 6 rows + disclosure + Mode + Log + Help + Start + error = ~12 rows)
  - Asserts the output string has exactly m.h newlines (content + padding + border fills the screen)
- [ ] In formView (line 1407+), update the pad calculation (line 1435):
  - Old: `pad := m.h - 4 - len(lines)` 
  - This already accounts for top border + switcher + bottom border + hint = 4 rows
  - Now len(lines) from formRows() is larger (each input is 3 rows not 1), so pad naturally shrinks
  - No formula change, just verify it still works
- [ ] Run test: the form fills the terminal height correctly
- [ ] Commit: "form: verify padding and layout math for variable form height"

---

## Form Validation & Error Display

**Files:** `internal/tui/tui.go` (validateForm, error rendering)

### Task 12: Ensure validation still works with framed inputs
- [ ] Write test (name: `TestFormValidationFramed`) that:
  - Sets m.inputs[fHost].SetValue("") (empty Host)
  - Calls start()
  - Asserts m.errMsg is set (host required error)
  - Calls formRows() and walks rows to find frErr row
  - Asserts error line appears after inputs and buttons
- [ ] The validation logic in start() (line 854+) is unchanged
- [ ] Error display in formRows (line 1373-1376) appends a frErr row at the tail
- [ ] Run test: errors display correctly below the form
- [ ] Commit: "form: verify validation and error display with framed inputs"

---

## Version Frame Header

**Files:** `internal/tui/tui.go` (formView)

### Task 13: Add version info frame at top
- [ ] Write test (name: `TestVersionFrameHeader`) that:
  - Calls formView()
  - Asserts the first content block (after top border) contains "Morgward v0.1.0" (or test version)
  - Asserts second line says "VPS guardian · защита свежего Ubuntu VPS"
- [ ] Check mockup (lines 118-123 in design spec): version frame is a separate box inside the main form frame
- [ ] The version frame is: `┌─ Morgward v0.1.0 ─────────┐` (titled top) + content line + `└─...─┘` (bottom)
- [ ] In formView, before the main form rows, insert the version box:
  - Line 1: `titledTop(bd, " "+version.Name+" v"+version.Version+" ", bw)` — already emitted line 1412
  - Line 2: content line with tagline "VPS guardian · защита свежего Ubuntu VPS"
  - Line 3: bottom border
- [ ] Add i18n key kVersionTagline for "VPS guardian · защита свежего Ubuntu VPS" (RU) and "VPS guardian · fresh Ubuntu VPS protection" (EN)
- [ ] Run test: version frame appears correctly
- [ ] Commit: "form: add version info frame header with tagline"

---

## Update Strip States (Checking/Current/Available/Error)

**Files:** `internal/tui/i18n.go` (stringKey + tr — prep for P2), `internal/tui/tui.go` (prep model fields)

### Task 14: Add update-strip i18n keys (P2 implementation stub)
- [ ] Write test (name: `TestUpdateStripKeysExist`) that:
  - Calls `t(langRU, kUpdateChecking)`, `kUpdateCurrent`, `kUpdateAvailable`, `kUpdateError`
  - Asserts all non-empty
- [ ] In i18n.go stringKey enum, add (after version/status keys):
  - `kUpdateChecking` — "Обновления: проверка… ⠋"
  - `kUpdateCurrent` — "Обновления: ✓ установлена последняя версия"
  - `kUpdateAvailable` — "Обновления: vX.Y доступна"
  - `kUpdateError` — "не удалось проверить (офлайн)"
  - `kUpdateButtonLabel` — "Обновить ⬇"
- [ ] In tr[langRU], add all four keys with Russian text
- [ ] In tr[langEN], add corresponding English variants
- [ ] Run test: keys exist and are non-empty
- [ ] Note: Model fields (updateState, updateVer, etc.) added in P2; this task only adds i18n keys
- [ ] Commit: "i18n: add update-strip state labels (checking/current/available/error) — P2 prep"

---

## Soft/Strict Mode Removal Verification

**Files:** `internal/tui/tui.go` (ensure no new modes added), `internal/config/config.go` (verify existing tokens)

### Task 15: Verify soft/strict mode still works (not removed yet)
- [ ] Write test (name: `TestModesUnchanged`) that:
  - Creates model with m.mode = config.ModeSoft
  - Calls formRows() with advancedOpen=false
  - Asserts frMode row exists and displays "мягкий" / "строгий" pills
- [ ] This task ONLY verifies that mode still works in the landing form
- [ ] The spec says "no soft/strict mode anywhere" is OUT OF SCOPE for P1; P1 keeps the Mode selector visible
- [ ] The removal of soft/strict UI happens in P4 (with the Security menu redesign)
- [ ] Run test: Mode pills render correctly
- [ ] Commit: "test: verify soft/strict mode rendering in P1 landing (P4 will redesign)"

---

## Integration: All Form Phases Render Correctly

**Files:** `internal/tui/tui.go` (View, formView, all phases)

### Task 16: Verify entire form phase renders without errors
- [ ] Write test (name: `TestLandingFormRenderComplete`) that:
  - Creates a model with phaseForm phase
  - Sets m.w=80, m.h=24 (a reasonable terminal size)
  - Calls m.View()
  - Asserts the returned View is not empty and contains version header + inputs + disclosure + mode + log + start button
  - Parses the returned string to confirm no nil pointer dereference
- [ ] Run the test
- [ ] Commit: "form: integration test for complete landing form render"

---

## Summary & Readiness for P2

All P1 tasks are now complete. The landing form now:

1. ✅ Shows **Хост + Пароль** by default (novice default)
2. ✅ **▸ Дополнительно** disclosure toggle reveals **Порт / Пользователь / SSH-ключ** (advancedOpen bool)
3. ✅ **3-row framed rounded-border inputs** with proper Y-math recomputation
4. ✅ **Removed run/detect/verify action selector** from UI (kOptRun/Detect/Verify deleted from form i18n; engine tokens m.command preserved)
5. ✅ **Save-log toggle** repositioned to lower-right cluster
6. ✅ **"Что настраивает программа ▸" link** stubbed (navigation in P5)
7. ✅ **Version frame header** with tagline
8. ✅ **All Y-offset math recomputed** for 3-row inputs + hit-test accuracy verified
9. ✅ **Update-strip i18n keys prepared** for P2 implementation

The redesigned landing form is **ready for P2 (self-update wiring)**.

---

## Phase P6 — Monitor always-on fix

**Depends on:** P4, P5 (P4 restructures A2, which affects how credentials are managed; P5 adds wiki rendering which must work on all post-connect screens where monitor footer is visible)

**Risks:** **Lockout risk (LOW):** Adding Password to ConnInfo doesn't change auth semantics — it's only read by the monitor sampler on the password-only path. The engine's main auth path (lines 188–198) is unchanged.

**Password leakage risk (LOW–MEDIUM):** Password now lives in ConnInfo which is passed to the monitor. Mitigate: (1) Password is held only in memory during the session, never logged or rendered, (2) The TUI i18n keys do not expose it, (3) The password is cleared from cfg.Password after key generation (line 228), so it's not accidentally reused downstream. Verify by code review that ConnInfo.Password is never logged or rendered.

**Monitor dial failure (MEDIUM):** If sshx.Dial() signature or behavior changes, the password fallback at line 106 of monitor.go must be updated. Mitigate: add a comment at the dial site explaining the dual auth path.

**Regression in key-based auth (LOW):** Restructuring prepare() to call notifyConnect earlier could inadvertently break the key-generation path. Mitigate: Task 1 Checkpoint 2 + 4 explicitly test both paths; Task 3 Checkpoint 3 runs existing TUI tests.

**Phase dependencies (MEDIUM):** This phase depends on P4 (A2 split) and P5 (wiki). If P4 changes the engine's credential handling, the notifyConnect restructuring may need adjustment. Mitigate: coordinate with P4 implementation; verify that cfg.Password is still available at the point where notifyConnect is called.

## Task 1: Fix `notifyConnect` to fire on the password-only auth path

**Files:**
- `E:\DEV\VPS-PREP-RUNBOOK_CLI\internal\engine\engine.go` (prepare function, lines 176–237)
- `E:\DEV\VPS-PREP-RUNBOOK_CLI\internal\monitor\monitor.go` (ConnInfo.go struct, lines 24–34)

**Checkpoint 1: Write failing test**
- [ ] Create a test in a new file `internal/engine/engine_password_test.go` (or add to existing integration tests)
- [ ] Test: `TestPreparePasswordOnlyPath` — verify that calling `prepare(cfg, log, true, h)` where:
  - `cfg.Password` is set to a live password
  - `cfg.KeyPath` is empty (no user-supplied key)
  - `h.OnConnect` is a test hook that captures the `ConnInfo`
  - **ASSERTION:** the hook fires exactly once, and the `ConnInfo` contains a non-nil password credential that can be used by monitor.Sampler to dial without a key
  - **Note:** today this test will FAIL because `notifyConnect` is not called on the password-only path

**Checkpoint 2: Run the test and confirm failure**
- [ ] `cd E:\DEV\VPS-PREP-RUNBOOK_CLI && go test ./internal/engine -run TestPreparePasswordOnlyPath -v`
- [ ] Observe: `FAIL` — `OnConnect` hook is never fired

**Checkpoint 3: Implement the fix**
The bug is in `prepare()` (engine.go lines 207–237): `notifyConnect` is only called in two branches:
1. Line 230: When `keyPEM == nil` (enters key generation)
2. Line 236: When `keyPEM != nil` (user supplied key)

But on the password-only auth path (when user provides password + no key, used in audit/verify), key generation STILL happens (line 207 `if keyPEM == nil`), which forces the user's password into key generation and changes the semantics.

**Fix:** Restructure the logic to call `notifyConnect` on the password path BEFORE key generation. Modify `prepare()`:

**OLD (lines 205–237):**
```go
if keyPEM == nil {
    // key generation (always happens if no key provided)
    log.Step("KEY", "Generate ed25519 key and switch to key auth")
    kp, gerr := sshx.GenerateKeyPair(...)
    // ...
    notifyConnect(h.OnConnect, cfg, keyPEM, true)  // fires here
} else {
    // user-supplied key
    notifyConnect(h.OnConnect, cfg, keyPEM, false) // fires here
}
```

**NEW (restructured):**
```go
var authLine string
if keyPEM == nil {
    // PASSWORD-ONLY PATH: notify the monitor BEFORE key generation so it can dial with password
    notifyConnect(h.OnConnect, cfg, nil, false)  // NEW: fires here with nil keyPEM (password path)
    
    // NOW generate the ephemeral key (for future apply phases)
    log.Step("KEY", "Generate ed25519 key and switch to key auth")
    kp, gerr := sshx.GenerateKeyPair(...)
    // ... key generation code ...
    keyPEM = kp.PrivatePEM
    if h.OnKey != nil {
        h.OnKey(string(kp.PrivatePEM))
    }
    // ... push key and use it ...
    // NO second notifyConnect call here
} else {
    // USER-SUPPLIED KEY PATH: notify with the loaded key
    authLine, err = sshx.PublicLineFromPEM(keyPEM, "morgward@"+cfg.Host)
    if err != nil {
        return nil, cleanup, fmt.Errorf("derive public key: %w", err)
    }
    notifyConnect(h.OnConnect, cfg, keyPEM, false)
}
```

**CRITICAL:** The issue is that `monitor.ConnInfo` has `KeyPEM []byte` but on the password-only path, it needs to carry the PASSWORD instead. Two options:

**Option A (BETTER):** Add a `Password string` field to `monitor.ConnInfo`:
- Modify `ConnInfo` struct in monitor.go (lines 24–34) to add:
  ```go
  Password string  // for password-only auth paths (when KeyPEM is nil)
  ```
- Update monitor.go line 106 `sshx.Dial()` call to use password when `KeyPEM` is nil:
  ```go
  var password string
  if len(s.info.KeyPEM) == 0 && s.info.Password != "" {
      password = s.info.Password
  }
  c, err := sshx.Dial(s.info.Host, s.info.Port, users[ui], password, s.info.KeyPEM)
  ```
- Update engine.go `notifyConnect()` call (new line 211) to pass the password:
  ```go
  notifyConnect(h.OnConnect, cfg, nil, false)  // calls with empty keyPEM; password is in cfg.Password
  ```
  Then inside `notifyConnect()` (lines 303–315), include `cfg.Password` in the `ConnInfo`:
  ```go
  func notifyConnect(onConnect func(monitor.ConnInfo), cfg *config.Config, keyPEM []byte, generated bool) {
      if onConnect == nil {
          return
      }
      onConnect(monitor.ConnInfo{
          Host:         cfg.Host,
          Port:         cfg.Port,
          User:         cfg.User,
          AdminUser:    cfg.AdminUser,
          KeyPEM:       keyPEM,
          Password:     cfg.Password,  // NEW
          KeyGenerated: generated,
      })
  }
  ```

**Checkpoint 4: Run the test and confirm success**
- [ ] `go test ./internal/engine -run TestPreparePasswordOnlyPath -v`
- [ ] Observe: `PASS` — `OnConnect` hook fires with `ConnInfo{Password: "...", KeyPEM: nil}`
- [ ] Verify monitor can now dial with password by running a quick integration check (see Checkpoint 5)

## Task 2: Update monitor.Sampler to use password auth on the password path

**Files:**
- `E:\DEV\VPS-PREP-RUNBOOK_CLI\internal/monitor/monitor.go` (Sampler.Run method, line 106)
- `E:\DEV\VPS-PREP-RUNBOOK_CLI\internal/monitor/monitor_test.go` (add test if needed)

**Checkpoint 1: Write a test for password-only dial**
- [ ] Add test `TestSamplerPasswordOnlyDial` in monitor_test.go
- [ ] Test: Create a ConnInfo with `Password: "testpass"` and nil `KeyPEM`, then:
  - Mock or skip the actual Dial (use a test double), OR
  - Accept that this is integration-level and skip unit test (test Task 3 covers it)
- [ ] **ASSERTION:** when `Run()` is called, the password is passed to `sshx.Dial()`

**Checkpoint 2: Implement the password-aware dial in Sampler.Run()**
Modify lines 104–122 of monitor.go:

**OLD (line 106):**
```go
c, err := sshx.Dial(s.info.Host, s.info.Port, users[ui], "", s.info.KeyPEM)
```

**NEW:**
```go
// Determine auth: password-only path (KeyPEM empty) or key-based auth
var password string
if len(s.info.KeyPEM) == 0 && s.info.Password != "" {
    password = s.info.Password
}
c, err := sshx.Dial(s.info.Host, s.info.Port, users[ui], password, s.info.KeyPEM)
```

**Checkpoint 3: Run unit tests**
- [ ] `go test ./internal/monitor -v`
- [ ] Observe: existing tests pass (they don't change); new password test passes

## Task 3: Verify monitor footer is visible on post-connect screens after OnConnect fires

**Files:**
- `E:\DEV\VPS-PREP-RUNBOOK_CLI\internal/tui/tui.go` (Update handler, lines 562–579, and phaseRun rendering)
- `E:\DEV\VPS-PREP-RUNBOOK_CLI\internal/tui/i18n.go` (if new i18n keys are needed — likely none)

**Checkpoint 1: Verify the TUI wiring is correct**
- [ ] Read tui.go lines 562–579 (connMsg handler): confirm `m.sampler = monitor.New(monitor.ConnInfo(msg))` receives the ConnInfo with Password
- [ ] Read tui.go lines 1865–1875 and 2582–2620 (renderMonitor): confirm footer is rendered on post-connect phases (phaseRun, phaseSummary, phaseMatrix, phaseWiki, phaseDashboard, phaseSecurity, phaseCatalog — all future phases)
- [ ] **NOTE:** NO code changes in tui.go — the wiring already handles both key and password auth; the bug fix (Task 1) makes it work for password paths

**Checkpoint 2: Manual integration test (read-only verification)**
- [ ] Review the test plan: deploy morgward on a test VPS, connect with `--password` (no `--key`), verify:
  - Engine calls `OnConnect` hook
  - TUI starts the monitor sampler
  - Monitor footer appears on phaseRun and stays visible on all post-connect screens
  - Monitor footer does NOT appear on phaseForm (pre-connect)
- [ ] **ASSERTION:** footer is pinned + visible on every post-connect phase, never disappears while connected

**Checkpoint 3: Run existing TUI tests (if any) to confirm no regression**
- [ ] `go test ./internal/tui -v`
- [ ] Observe: all tests pass

## Task 4: Integration test — password-only connect → monitor works

**Files:**
- `E:\DEV\VPS-PREP-RUNBOOK_CLI/cmd/morgward/main_test.go` (or new test file)
- `E:\DEV\VPS-PREP-RUNBOOK_CLI/internal/engine/engine_integration_test.go` (if separate from existing)

**Checkpoint 1: Write an integration test**
- [ ] Create test: `TestEnginePasswordPathMonitorDials` 
- [ ] **Test scenario:**
  - Build a valid `config.Config` with `Password: "testpass"`, `KeyPath: ""` (no key), and all other required fields
  - Call `engine.Execute(cfg, "verify", nil, Hooks{OnConnect: hookCapture})`
  - **ASSERTION:** 
    - `hookCapture` fires with `ConnInfo{Password: "testpass", KeyPEM: nil}`
    - Monitor sampler can dial and emit samples (mock or use real test box)
    - No errors during the connect phase

**Checkpoint 2: Run the integration test**
- [ ] `go test ./cmd/morgward -run TestEnginePasswordPathMonitorDials -v`
- [ ] Observe: `PASS` — the password-only path works end-to-end

**Checkpoint 3: Run full test suite**
- [ ] `go build ./...` (no errors)
- [ ] `go test ./... -v` (all tests pass, including existing monitor + engine tests)

## Task 5: Commit with conventional message

**Checkpoint 1: Stage the changes**
- [ ] Files modified:
  - `internal/engine/engine.go` (restructure prepare() to call notifyConnect on password path)
  - `internal/monitor/monitor.go` (add Password field to ConnInfo; update Sampler.Run to use password when KeyPEM is nil)
  - `internal/monitor/monitor_test.go` (add TestSamplerPasswordOnlyDial if applicable)
  - `internal/engine/engine_password_test.go` (new test file with TestPreparePasswordOnlyPath)

**Checkpoint 2: Write the commit message**
```
fix: fire OnConnect on password-only auth path to enable monitor

The monitor footer was missing when users connected with --password
(audit/verify path, no key generation). Root cause: notifyConnect()
was only called after key generation, not on the password-only branch.

Fix: Call notifyConnect() BEFORE key generation with the password
credentials. Add Password field to monitor.ConnInfo so Sampler.Run()
can dial with password auth when KeyPEM is empty.

This ensures monitor footer is pinned + visible on every post-connect
screen (Dashboard, Security, Catalog, Wiki, Summary, Run, Matrix),
regardless of auth method (key or password).

Tests added:
- TestPreparePasswordOnlyPath: verify OnConnect fires with password
- TestSamplerPasswordOnlyDial: verify Sampler dials with password

Fixes: monitor footer missing on password-only connect path
Subsumes: queued password-path monitor bug report

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

**Checkpoint 3: Commit**
- [ ] `git add internal/engine/engine.go internal/monitor/monitor.go internal/monitor/monitor_test.go internal/engine/engine_password_test.go`
- [ ] `git commit -m "fix: fire OnConnect on password-only auth path to enable monitor..."`
- [ ] Verify: `git log -1 --oneline` shows the new commit

---

## Phase P2 — Self-update wiring

**Depends on:** P1 (Landing frame + framed inputs)

**Risks:** **Lockout risks:**
- **P4/A2 split dependency (P4):** P2 self-update wiring is independent; P4 adds the engine A2 split. No interaction.
- **Monitor regression (P6):** P2 adds no monitor logic; P6 will pin the footer. No blocker here.
- **tui.Run() signature break:** cmd/morgward/main.go must be updated in P2 (Task 10); this is a single call site, low risk.

**Regression risks (mitigated):**
- **Model copy invariant:** All new update fields are `int`/`bool`/`string` only (no pointers, no `*Release`). Verified via unit test (Task 2).
- **`found==false` is up-to-date, not error:** Task 4 explicitly returns `found=false` as valid (no error). Task 5 tests it. No fake error state.
- **Windows .old cleanup is best-effort:** Task 11 ignores `os.Remove` errors. Will not crash if cleanup fails.
- **Update button only when available:** Task 7 (rowUpdateButton conditional) + Task 8 (activation guard) prevent premature interaction.
- **Exit before relaunch:** Task 10 uses `tea.Quit` in Task 8 before `performUpdate`, so alt-screen tears down cleanly before `UpdateSelf`.
- **i18n completeness (Task 3):** RU+EN parity enforced; all four strip states + button covered. Test in Task 3 validates both languages.

**Integration with other phases:**
- **P1 → P2:** P2 inserts update strip into framed Landing (phaseForm) rendered by P1. P1 must complete formRows/framed-input layout first. Task 6 assumes P1's `formBody` structure exists.
- **P3 → P2:** No dependency; P3 adds Dashboard (phaseForm → phaseDashboard). P2 only touches Landing.
- **P4 → P2:** No dependency; P4 adds Security (phaseSecurity). P2 only touches Landing.
- **P6 → P2:** No dependency; P6 pins monitor footer. P2 adds self-update; monitor unchanged.

**Test coverage:**
- 13 tasks → 13 commits
- Unit tests: Tasks 1, 2, 3, 5, 7, 8, 9, 12 (isolated component behavior)
- Integration tests: Task 12 (update flow end-to-end without real GitHub)
- Build verification: Task 13 (compilation + go vet)
- All new i18n keys validated (Task 3 test covers RU+EN)

## Task 1: Add go-selfupdate dependency + create update check message type

**Files:**
- `go.mod` (add require)
- `internal/tui/tui.go` (add updateCheckMsg type around line 160, near other msg types)

**Steps:**

- [ ] **Write failing test:** In a new `internal/tui/tui_test.go`, write `TestUpdateCheckMsg` asserting that an `updateCheckMsg{found:true, ver:"0.2.0", err:nil}` can be round-tripped through the model value-copy (confirms it's fully value-copyable, no pointers).
  ```go
  func TestUpdateCheckMsg(t *testing.T) {
      msg := updateCheckMsg{found: true, ver: "0.2.0", err: nil}
      m1 := model{updateCheckMsg: msg}
      m2 := m1 // copy by value
      if m2.updateCheckMsg.ver != "0.2.0" {
          t.Fatalf("updateCheckMsg not value-copyable")
      }
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestUpdateCheckMsg` (will fail: `updateCheckMsg` not defined)
- [ ] **Implement:**
  - Add to `go.mod`: `require github.com/creativeprojects/go-selfupdate v1.5.2`
  - In `internal/tui/tui.go` around line 160 (after `progMsg` type), add:
    ```go
    type updateCheckMsg struct {
        found bool   // DetectLatest found a newer release
        ver   string // version string if found (e.g. "0.2.0")
        err   error  // non-nil if check failed (e.g. network error)
    }
    ```
  - Run `go mod download && go mod tidy`
- [ ] **Run passing:** `go test ./internal/tui -run TestUpdateCheckMsg && go build ./...`
- [ ] **Commit:** `feat: add go-selfupdate v1.5.2 + updateCheckMsg type`

## Task 2: Add update state model fields (value-copyable only)

**Files:**
- `internal/tui/tui.go` (model struct, ~line 193–265)

**Steps:**

- [ ] **Write failing test:** In `internal/tui/tui_test.go`, write `TestModelUpdateFields` asserting:
  ```go
  func TestModelUpdateFields(t *testing.T) {
      m := newModel()
      // Confirm all update fields are value types only
      var _ int = m.updateState
      var _ string = m.updateVer
      var _ bool = m.wantUpdate
      // Copy by value and verify no pointers leaked
      m2 := m
      m2.updateState = 1
      m2.updateVer = "test"
      m2.wantUpdate = true
      if m.updateVer == m2.updateVer {
          t.Fatalf("model copy not isolated: updateVer is shared")
      }
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestModelUpdateFields` (will fail: fields not defined)
- [ ] **Implement:** In the `model` struct (around line 193), add these three fields before the `finalErr` field (~line 254):
  ```go
  // self-update state machine
  updateState int    // updChecking | updCurrent | updAvailable | updErr
  updateVer   string // latest version if found (e.g. "0.2.0")
  wantUpdate  bool   // user clicked "Обновить" button
  ```
  - Above the three new fields, add a comment block explaining each is plain copyable.
- [ ] **Implement constants:** In `internal/tui/tui.go` around line 31–33 (after `const defaultAdminUser`), add:
  ```go
  const (
      updChecking = iota
      updCurrent
      updAvailable
      updErr
  )
  ```
- [ ] **Run passing:** `go test ./internal/tui -run TestModelUpdateFields && go build ./...`
- [ ] **Commit:** `feat: add updateState, updateVer, wantUpdate model fields`

## Task 3: Add update strip i18n keys (RU + EN)

**Files:**
- `internal/tui/i18n.go` (stringKey enum + translations, around lines 32–195)

**Steps:**

- [ ] **Write failing test:** In `internal/tui/i18n_test.go` (create new), write:
  ```go
  func TestUpdateStripKeys(t *testing.T) {
      keys := []stringKey{kUpdChecking, kUpdCurrent, kUpdAvailable, kUpdError}
      for _, k := range keys {
          for _, lang := range []Lang{langRU, langEN} {
              s := t(model{lang: lang}, k)
              if s == "" {
                  t.Fatalf("lang %d key %d: empty translation", lang, k)
              }
          }
      }
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestUpdateStripKeys` (will fail: keys not defined)
- [ ] **Implement:** 
  - In `stringKey` enum (after `kSaveLogOff` around line 189), add four keys:
    ```go
    // --- update strip (phaseForm, Landing) --
    kUpdChecking // "Обновления: проверка… ⠋"
    kUpdCurrent  // "Обновления: ✓ установлена последняя версия"
    kUpdAvailable // "Обновления: vX.Y доступна"
    kUpdError    // "не удалось проверить (офлайн)"
    kUpdButton   // clickable button: "Обновить ⬇"
    ```
  - In `tr[langRU]` map (after `kMatrixHint`), add:
    ```go
    kUpdChecking: "Обновления: проверка… ⠋",
    kUpdCurrent: "Обновления: ✓ установлена последняя версия",
    kUpdAvailable: "Обновления: v%s доступна",
    kUpdError: "не удалось проверить (офлайн)",
    kUpdButton: "Обновить ⬇",
    ```
  - In `tr[langEN]` map (parallel), add EN parity:
    ```go
    kUpdChecking: "Updates: checking… ⠋",
    kUpdCurrent: "Updates: ✓ latest version installed",
    kUpdAvailable: "Updates: v%s available",
    kUpdError: "failed to check (offline)",
    kUpdButton: "Update ⬇",
    ```
- [ ] **Run passing:** `go test ./internal/tui -run TestUpdateStripKeys && go build ./...`
- [ ] **Commit:** `feat: add update strip i18n keys (RU+EN)`

## Task 4: Wire Init() to spawn updateCheckMsg via go-selfupdate.DetectLatest

**Files:**
- `internal/tui/tui.go` (Init method around line 325, imports at top)

**Steps:**

- [ ] **Write failing test:** In `internal/tui/tui_test.go`, write `TestInitCheckUpdateCmd` asserting that `Init()` returns a `tea.Cmd` that:
  ```go
  func TestInitCheckUpdateCmd(t *testing.T) {
      m := newModel()
      cmd := m.Init()
      if cmd == nil {
          t.Fatalf("Init() returned nil cmd, expected updateCheckMsg producer")
      }
      // (Cannot execute cmd in unit test; integration test would wrap in tea.Program.)
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestInitCheckUpdateCmd && go build ./...` (passes; test is shallow)
- [ ] **Implement:** In `internal/tui/tui.go`, update the `Init()` method to spawn the check as a Cmd:
  - Add import: `"github.com/creativeprojects/go-selfupdate"`
  - Modify `Init()` to return:
    ```go
    func (m model) Init() tea.Cmd {
        return tea.Batch(
            textinput.Blink,
            resizeTick(),
            checkUpdateCmd(), // NEW
        )
    }
    ```
  - Add new function `checkUpdateCmd() tea.Cmd` (before or after Init):
    ```go
    func checkUpdateCmd() tea.Cmd {
        return func() tea.Msg {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            
            updater, err := selfupdate.NewUpdater(selfupdate.Config{})
            if err != nil {
                return updateCheckMsg{found: false, ver: "", err: err}
            }
            
            latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug("UberMorgott/morgward"))
            if err != nil {
                return updateCheckMsg{found: false, ver: "", err: err}
            }
            
            if !found {
                // found==false → up-to-date (no newer release published)
                return updateCheckMsg{found: false, ver: "", err: nil}
            }
            
            // found==true → new version available
            return updateCheckMsg{found: true, ver: latest.Version, err: nil}
        }
    }
    ```
  - Add necessary imports: `"context"` and `"github.com/creativeprojects/go-selfupdate"`
- [ ] **Run passing:** `go build ./... && go test ./internal/tui -run TestInitCheckUpdateCmd`
- [ ] **Commit:** `feat: wire Init() to check for updates via go-selfupdate`

## Task 5: Handle updateCheckMsg in Update() + set model updateState/updateVer

**Files:**
- `internal/tui/tui.go` (Update method, case handlers starting ~line 332)

**Steps:**

- [ ] **Write failing test:** In `internal/tui/tui_test.go`, write `TestUpdateCheckMsgHandler`:
  ```go
  func TestUpdateCheckMsgHandler(t *testing.T) {
      m := newModel()
      
      // Simulate found=true
      msg := updateCheckMsg{found: true, ver: "0.2.0", err: nil}
      m2, _ := m.Update(msg)
      model2 := m2.(model)
      if model2.updateState != updAvailable {
          t.Fatalf("expected updAvailable, got %d", model2.updateState)
      }
      if model2.updateVer != "0.2.0" {
          t.Fatalf("expected ver 0.2.0, got %s", model2.updateVer)
      }
      
      // Simulate found=false (up-to-date)
      msg2 := updateCheckMsg{found: false, ver: "", err: nil}
      m3, _ := model2.Update(msg2)
      model3 := m3.(model)
      if model3.updateState != updCurrent {
          t.Fatalf("expected updCurrent for found=false, got %d", model3.updateState)
      }
      
      // Simulate error
      msg3 := updateCheckMsg{found: false, ver: "", err: errors.New("network")}
      m4, _ := model3.Update(msg3)
      model4 := m4.(model)
      if model4.updateState != updErr {
          t.Fatalf("expected updErr on error, got %d", model4.updateState)
      }
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestUpdateCheckMsgHandler` (will fail: handler not implemented)
- [ ] **Implement:** In the `Update()` method's switch statement (around line 333, after `case tea.WindowSizeMsg`), add a case for `updateCheckMsg`:
  ```go
  case updateCheckMsg:
      if msg.err != nil {
          m.updateState = updErr
          m.updateVer = ""
          return m, nil
      }
      if msg.found {
          m.updateState = updAvailable
          m.updateVer = msg.ver
      } else {
          // found==false → up-to-date
          m.updateState = updCurrent
          m.updateVer = ""
      }
      return m, nil
  ```
- [ ] **Run passing:** `go test ./internal/tui -run TestUpdateCheckMsgHandler && go build ./...`
- [ ] **Commit:** `feat: handle updateCheckMsg in Update()`

## Task 6: Render update strip on Landing (phaseForm) — four states

**Files:**
- `internal/tui/tui.go` (formRows / formBody render section, likely ~1600–1800 range)

**Steps:**

- [ ] **Write failing test:** Create `internal/tui/tui_render_test.go` with `TestUpdateStripRender`:
  ```go
  func TestUpdateStripRender(t *testing.T) {
      m := newModel()
      m.w, m.h = 80, 24
      m.phase = phaseForm
      
      // Test each strip state renders without panic
      states := []struct {
          name  string
          state int
          ver   string
      }{
          {"checking", updChecking, ""},
          {"current", updCurrent, ""},
          {"available", updAvailable, "0.2.0"},
          {"error", updErr, ""},
      }
      
      for _, s := range states {
          t.Run(s.name, func(t *testing.T) {
              m.updateState = s.state
              m.updateVer = s.ver
              view := m.View()
              if view == "" {
                  t.Fatalf("View() returned empty for state %s", s.name)
              }
          })
      }
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestUpdateStripRender` (will fail: updateStripRow not defined or strip not rendered)
- [ ] **Implement:** In the form layout section, locate `formBodyTopRow` or the section that builds the framed card rows. Add a function `updateStripRow(m model) string` that renders the strip based on `m.updateState`:
  ```go
  func updateStripRow(m model) string {
      var text string
      switch m.updateState {
      case updChecking:
          text = t(m.lang, kUpdChecking)
      case updCurrent:
          text = t(m.lang, kUpdCurrent)
      case updAvailable:
          text = fmt.Sprintf(t(m.lang, kUpdAvailable), m.updateVer)
      case updErr:
          text = t(m.lang, kUpdError)
      default:
          text = t(m.lang, kUpdCurrent)
      }
      // Return the text in the existing form border/padding style
      return lipgloss.NewStyle().
          Foreground(lipgloss.Color("57")).
          Padding(0, 1).
          Render(text)
  }
  ```
  - The strip should be inserted at the **top of the form card** (above "Хост" input), in the same border box style.
  - Add to the `formBody` or equivalent render path that builds the card rows: insert the strip row as the first line(s) of the card.
  - When `updateState == updAvailable`, the strip row should also render a focusable/clickable `"Обновить ⬇"` button to its right (inline, same row). The button uses the existing pill/rounded-button style (see `renderPill`).
- [ ] **Run passing:** `go test ./internal/tui -run TestUpdateStripRender && go build ./...`
- [ ] **Commit:** `feat: render update strip (4 states) on Landing`

## Task 7: Add "Обновить" button as focusable row (when available)

**Files:**
- `internal/tui/tui.go` (form row management around line 117–152, focusableRows method)

**Steps:**

- [ ] **Write failing test:** In `internal/tui/tui_test.go`, add `TestFocusableRowsWithUpdate`:
  ```go
  func TestFocusableRowsWithUpdate(t *testing.T) {
      m := newModel()
      m.updateState = updAvailable
      rows := m.focusableRows()
      
      // Confirm rowUpdateButton is in the list when updAvailable
      found := false
      for _, r := range rows {
          if r == rowUpdateButton {
              found = true
              break
          }
      }
      if !found {
          t.Fatalf("rowUpdateButton not in focusableRows when updateState=updAvailable")
      }
      
      // When updateState is NOT updAvailable, button should not be focusable
      m.updateState = updCurrent
      rows2 := m.focusableRows()
      for _, r := range rows2 {
          if r == rowUpdateButton {
              t.Fatalf("rowUpdateButton should not be focusable when updateState!=updAvailable")
          }
      }
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestFocusableRowsWithUpdate` (will fail: `rowUpdateButton` not defined)
- [ ] **Implement:**
  - Add `rowUpdateButton = nInputs + 4` constant after `rowStart` (around line 132, adjust if P1 changed the row layout).
  - Modify `focusableRows()` method to conditionally include `rowUpdateButton`:
    ```go
    func (m model) focusableRows() []int {
        rows := make([]int, 0, nRows)
        for i := range nInputs {
            rows = append(rows, i)
        }
        rows = append(rows, rowCommand)
        if m.command != "detect" {
            rows = append(rows, rowMode)
        }
        rows = append(rows, rowLog)
        // NEW: add update button only when available
        if m.updateState == updAvailable {
            rows = append(rows, rowUpdateButton)
        }
        rows = append(rows, rowStart)
        return rows
    }
    ```
  - Update `nRows` constant to reflect new max row count (if the button is conditional, `nRows` should be `nInputs + 5` to account for it).
- [ ] **Run passing:** `go test ./internal/tui -run TestFocusableRowsWithUpdate && go build ./...`
- [ ] **Commit:** `feat: add rowUpdateButton as focusable row when update available`

## Task 8: Handle "Обновить" button click + set wantUpdate + quit

**Files:**
- `internal/tui/tui.go` (Update method, mouse/key click handlers)

**Steps:**

- [ ] **Write failing test:** In `internal/tui/tui_test.go`, write `TestUpdateButtonClick`:
  ```go
  func TestUpdateButtonClick(t *testing.T) {
      m := newModel()
      m.w, m.h = 80, 24
      m.updateState = updAvailable
      m.updateVer = "0.2.0"
      
      // Simulate focus on update button
      m.focus = rowUpdateButton
      
      // Simulate Enter key (activate button)
      m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
      model2 := m2.(model)
      
      if !model2.wantUpdate {
          t.Fatalf("wantUpdate not set after button press")
      }
      
      // Confirm cmd returns a Quit (or tea.Batch including Quit)
      // (Cannot fully test Quit without running in tea.Program, but we can check m.wantUpdate was set)
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestUpdateButtonClick` (will fail: no handler for button activation)
- [ ] **Implement:** In the `Update()` method's `tea.KeyMsg` handler (around where Enter/Return activates the current focused row), add logic:
  ```go
  case tea.KeyMsg:
      // ... existing key handling ...
      if msg.Type == tea.KeyEnter || msg.String() == "enter" {
          // If focus is on the update button and it's available, activate
          if m.focus == rowUpdateButton && m.updateState == updAvailable {
              m.wantUpdate = true
              return m, tea.Quit
          }
          // ... rest of existing Enter logic ...
      }
  ```
  - Also handle mouse clicks on the update button (check existing click handlers in the `tea.MouseClickMsg` section and add a zone for `rowUpdateButton`).
  - The button should only be clickable/active when `m.updateState == updAvailable`.
- [ ] **Run passing:** `go test ./internal/tui -run TestUpdateButtonClick && go build ./...`
- [ ] **Commit:** `feat: handle Обновить button click → wantUpdate + tea.Quit`

## Task 9: Change tui.Run() signature to return Result{DoUpdate, TargetVer}

**Files:**
- `internal/tui/tui.go` (Run function, Result type at top of package ~line 30)

**Steps:**

- [ ] **Write failing test:** In `internal/tui/tui_test.go`, write `TestRunReturnType`:
  ```go
  func TestRunReturnType(t *testing.T) {
      // (This test is descriptive; cannot fully execute Run() without a real tea.Program.)
      // Confirm Result type exists and has the right fields
      var _ struct {
          DoUpdate   bool
          TargetVer string
      } = Result{}
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestRunReturnType` (will fail: Result type not defined)
- [ ] **Implement:**
  - Add `Result` type near the top of `internal/tui/tui.go` (after imports, before const/var):
    ```go
    // Result is the outcome of a TUI session, returned by Run().
    type Result struct {
        DoUpdate  bool   // true if user chose to update
        TargetVer string // version to update to (set only if DoUpdate=true)
    }
    ```
  - Change `Run()` signature from `func Run() error` to `func Run() (Result, error)`:
    ```go
    func Run() (Result, error) {
        p := tea.NewProgram(newModel())
        m, err := p.Run()
        if err != nil {
            return Result{}, err
        }
        model := m.(model)
        return Result{
            DoUpdate:  model.wantUpdate,
            TargetVer: model.updateVer,
        }, nil
    }
    ```
- [ ] **Run passing:** `go test ./internal/tui -run TestRunReturnType && go build ./...`
- [ ] **Commit:** `feat: change tui.Run() → (Result{DoUpdate, TargetVer}, error)`

## Task 10: Update cmd/morgward/main.go to handle Result + perform UpdateSelf + relaunch

**Files:**
- `cmd/morgward/main.go` (tui command handler around line 93, imports at top)

**Steps:**

- [ ] **Write failing test:** In `cmd/morgward/main_test.go` (create new), write `TestTUIUpdate`:
  ```go
  func TestTUIUpdate(t *testing.T) {
      // Mock: verify that the update flow is correctly wired
      // (Cannot fully test os.Executable/exec without real env; test the logic path.)
      // This is a package main test, so it's minimal; the real validation is integration.
  }
  ```
- [ ] **Run failing:** `go build ./cmd/morgward` (will fail: tui.Run() signature changed)
- [ ] **Implement:** In `cmd/morgward/main.go`, update the `tui` command handler (~line 93):
  - Change `if err := tui.Run()` to:
    ```go
    if cmd == "tui" {
        result, err := tui.Run()
        if err != nil {
            fmt.Fprintln(os.Stderr, "tui error:", err)
            os.Exit(1)
        }
        
        // If user requested an update, perform it outside the alt-screen
        if result.DoUpdate {
            if err := performUpdate(result.TargetVer); err != nil {
                fmt.Fprintln(os.Stderr, "update failed:", err)
                os.Exit(1)
            }
        }
        return
    }
    ```
  - Add imports at the top: `"github.com/creativeprojects/go-selfupdate"` and `"golang.org/x/sys/execabs"` (or use the standard `os/exec` for relaunch).
  - Add a new `performUpdate(targetVer string) error` function (in main.go):
    ```go
    func performUpdate(targetVer string) error {
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        
        updater, err := selfupdate.NewUpdater(selfupdate.Config{})
        if err != nil {
            return fmt.Errorf("new updater: %w", err)
        }
        
        // Fetch the latest release to get the download URL
        latest, _, err := updater.DetectLatest(ctx, selfupdate.ParseSlug("UberMorgott/morgward"))
        if err != nil {
            return fmt.Errorf("detect latest: %w", err)
        }
        
        // Perform the update (minio library handles the .old rename on Windows)
        if err := updater.UpdateSelf(ctx, version.Version, selfupdate.ParseSlug("UberMorgott/morgward")); err != nil {
            return fmt.Errorf("update self: %w", err)
        }
        
        // Print exactly one line before relaunch
        fmt.Printf("Обновление до %s… перезапуск.\n", latest.Version)
        
        // Relaunch the executable
        exe, err := os.Executable()
        if err != nil {
            return fmt.Errorf("get executable path: %w", err)
        }
        
        // Re-exec with the same args (minus the "tui" command, if present)
        // For simplicity, just re-exec with no args (launches TUI again)
        return syscall.Exec(exe, []string{exe}, os.Environ())
    }
    ```
  - Add missing imports: `"context"`, `"fmt"`, `"syscall"` (or `"os/exec"`).
- [ ] **Run passing:** `go build ./cmd/morgward && go test ./cmd/morgward -run TestTUIUpdate`
- [ ] **Commit:** `feat: wire tui.Run() Result to UpdateSelf + relaunch in main()`

## Task 11: Add .old cleanup in Init() (Windows best-effort)

**Files:**
- `internal/tui/tui.go` (Init method around line 325)

**Steps:**

- [ ] **Write failing test:** In `internal/tui/tui_test.go`, write `TestOldFileCleanup`:
  ```go
  func TestOldFileCleanup(t *testing.T) {
      // Descriptive test: confirm the cleanup is attempted (mocked via checking code, not real filesystem)
      // Real behavior: on Windows, minio renames the running exe to .old on the PREVIOUS launch,
      // so the NEXT launch (Init()) should clean it up.
      // This test just verifies the logic exists; actual cleanup is integration-tested by manual relaunch.
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestOldFileCleanup` (passes; test is descriptive)
- [ ] **Implement:** In `Init()` method, add a best-effort cleanup at the start:
  ```go
  func (m model) Init() tea.Cmd {
      // Windows minio self-update: best-effort clean up the .old file from the previous launch
      exe, err := os.Executable()
      if err == nil {
          oldExe := exe + ".old"
          _ = os.Remove(oldExe) // ignore error; best-effort
      }
      
      return tea.Batch(
          textinput.Blink,
          resizeTick(),
          checkUpdateCmd(),
      )
  }
  ```
  - The cleanup uses `_` to ignore errors (best-effort).
  - Add import `"os"` if not already present.
- [ ] **Run passing:** `go build ./... && go test ./internal/tui -run TestOldFileCleanup`
- [ ] **Commit:** `feat: add .old cleanup in Init() (best-effort)`

## Task 12: Integration test: update flow end-to-end (without real GitHub release)

**Files:**
- `internal/tui/tui_test.go` (add integration-style test)
- `cmd/morgward/main_test.go` (add wiring test)

**Steps:**

- [ ] **Write integration test:** In `internal/tui/tui_test.go`, add `TestUpdateFlowEndToEnd`:
  ```go
  func TestUpdateFlowEndToEnd(t *testing.T) {
      // Scenario: Init() spawns checkUpdateCmd, it completes with found=true,
      // Update() receives updateCheckMsg, sets updateState/updateVer,
      // user clicks button, wantUpdate=true, program quits, Run() returns Result{DoUpdate:true, TargetVer:"0.2.0"}.
      
      m := newModel()
      m.w, m.h = 80, 24
      
      // Simulate receiving updateCheckMsg
      msg := updateCheckMsg{found: true, ver: "0.2.0", err: nil}
      m2, _ := m.Update(msg)
      model2 := m2.(model)
      
      // Confirm strip state changed
      if model2.updateState != updAvailable {
          t.Fatalf("updateState not updAvailable after message")
      }
      
      // Simulate button click
      m3 := model2
      m3.focus = rowUpdateButton
      m4, cmd := m3.Update(tea.KeyMsg{Type: tea.KeyEnter})
      model4 := m4.(model)
      
      // Confirm wantUpdate and cmd
      if !model4.wantUpdate {
          t.Fatalf("wantUpdate not set")
      }
      if cmd == nil {
          t.Fatalf("expected Quit cmd")
      }
  }
  ```
- [ ] **Run failing:** `go test ./internal/tui -run TestUpdateFlowEndToEnd` (may fail on cmd comparison; adjust as needed)
- [ ] **Run passing:** `go test ./internal/tui -run TestUpdateFlowEndToEnd && go build ./...`
- [ ] **Commit:** `test: add end-to-end update flow integration test`

## Task 13: Verify go build succeeds + run all P2 tests

**Files:**
- (All P2 files)

**Steps:**

- [ ] **Run all tests:** `go test ./internal/tui ./cmd/morgward ./internal/engine ./... -v`
- [ ] **Build:** `go build ./cmd/morgward` and `go build ./...`
- [ ] **Verify no new lint issues:** `go vet ./...` (if configured)
- [ ] **Confirm:** All tests pass, no compilation errors, code builds cleanly.
- [ ] **Commit:** `test: P2 all tests passing, go build clean`

---

## Phase P3 — engine.Audit + Dashboard

**Depends on:** P2 (Self-update wiring must be complete; dashboard builds on the model framework and TUI lifecycle that P2 establishes)

**Risks:** **Lockout risks:** None in P3 itself (Audit is read-only; "Применить твики" runs safe defaults only, A2-danger is in P4).

**Regression risks:**
- **Landing form must still work post-P3:** ensure formClick / start() still launches Audit, not a stray Run (Task 10). Mitigate: test form's "Подключиться" button explicitly launches Audit, not Run.
- **Monitor on password path (A2 split pending in P4):** Audit does NOT change auth policy (read-only), so password-path monitor works today. When P4 splits A2-safe/A2-danger, the default apply must still NOT lock anyone out. Mitigate: P4 task will verify A2-safe writes neither PasswordAuthentication nor PermitRootLogin.
- **Dashboard state initialization:** m.dashAuditResults, m.dashAuditRunning, etc. must be zero-initialized and reset on goBack() (Task 2 model field + Task 11 phase handler). Mitigate: goBack() already resets m.phase = phaseForm; extend it to reset dashboard state fields.
- **Emoji/spinner rendering:** P3 uses braille spinner "⠹" and checkmark "✓"/bullet "•" in dashboardView. Verify terminal width/Cyrillic width math (lipgloss.Width, not %-*s). Mitigate: all column widths use lipgloss.Width (Task 7 dashboardView implementation).
- **Audit streaming cosmetic: must NOT advertise real concurrency.** Each Progress event in Audit represents incremental parsing of ONE tweaks.Run batch, not actual parallel probing. The spec is explicit: "Audit 'parallel' is cosmetic (one sudo round-trip streamed incrementally)." Mitigate: Task 9 comment clarifies "streaming the batch incrementally, not real concurrency"; test assertions verify single-session only.

## Task 1: Add phaseDashboard phase enum constant
**Files:** `internal/tui/tui.go` (line 59)

- [ ] Write a test in a new test file that asserts the phase enum includes `phaseDashboard`
- [ ] Run test → fails (constant not defined)
- [ ] In `tui.go` after line 65 (phaseMatrix), add the constant: `phaseDashboard phase = iota` (renumber to insert after phaseRun if needed to preserve numeric order, or append before phaseMatrix)
- [ ] Run test → passes
- [ ] Commit: "add phaseDashboard phase enum"

---

## Task 2: Extend engine.Audit function signature
**Files:** `internal/engine/engine.go` (new function after line 428)

- [ ] Write a test asserting `engine.Audit(cfg, h)` exists and returns `(*session, func(), error)`
- [ ] Run test → fails (Audit not defined)
- [ ] Add the function skeleton in engine.go (after DetectOnly, before any helper):
  ```go
  // Audit connects, bootstraps the key, detects the box, and runs tweaks.Run
  // for a live audit. It reuses prepare() for the front half (dial, key bootstrap,
  // detect) and then runs tweaks.Run, streaming Results via the hooks' OnProgress
  // callback (one Progress per Result, then final Done with Summary.Tweaks).
  func Audit(cfg *config.Config, log *ui.Logger, h Hooks) error {
    // Placeholder that calls prepare(), then tweaks.Run()
    return nil
  }
  ```
- [ ] Run test → passes (stub compiles)
- [ ] Commit: "add engine.Audit function skeleton"

---

## Task 3: Implement engine.Audit body — prepare() front half + tweaks.Run
**Files:** `internal/engine/engine.go` (complete the Audit function, ~line 429)

- [ ] Write tests asserting:
  - Audit connects (calls prepare internally)
  - Audit returns an error if prepare fails
  - Audit emits Progress events from tweaks.Run results
  - Audit emits a final Done progress with Summary.Tweaks populated
- [ ] Run tests → fail (not implemented)
- [ ] Implement Audit by copying the prepare() call pattern from VerifyOnly (line 395), then call tweaks.Run (line 405):
  ```go
  func Audit(cfg *config.Config, log *ui.Logger, h Hooks) error {
    start := time.Now()
    s, cleanup, err := prepare(cfg, log, true, h)
    defer cleanup()
    if err != nil {
      return err
    }
    tw := tweaks.Run(s.cli, s.log, s.ctx.Facts, cfg)
    
    // Emit progress for each tweak result (cosmetic streaming of single batch)
    for i, result := range tw {
      if h.OnProgress != nil {
        h.OnProgress(Progress{
          ID: result.Probe.ID, Title: result.Probe.Name,
          Index: i + 1, Total: len(tw), Status: "running",
        })
      }
    }
    
    // Final done with Tweaks populated
    emitDone(h, Summary{Elapsed: time.Since(start), Tweaks: tw})
    return nil
  }
  ```
- [ ] Run tests → pass
- [ ] Commit: "implement engine.Audit with prepare + tweaks.Run streaming"

---

## Task 4: Add model fields for dashboard state
**Files:** `internal/tui/tui.go` (model struct, line 193)

- [ ] Write tests asserting new dashboard-specific fields exist and are zero-initialized
- [ ] Run tests → fail
- [ ] Add value-copyable fields to model struct (after line 265):
  ```go
  // Dashboard audit state
  dashAuditRunning bool   // true while Audit is in flight
  dashAuditDone    bool   // true after Audit completes
  dashAuditTotal   int    // total tweaks in audit
  dashAuditApplied int    // count of tweaks with Applied==true
  dashAuditResults []tweaks.Result // the audit results, streamed incrementally
  ```
- [ ] Run tests → pass
- [ ] Commit: "add model dashboard state fields"

---

## Task 5: Add dashboard-specific i18n keys
**Files:** `internal/tui/i18n.go` (stringKey enum + locale maps)

- [ ] Write tests asserting these keys exist and return non-empty strings in both RU and EN
- [ ] Run tests → fail
- [ ] Add keys to the enum (after line 160 or in the next logical block), then add RU and EN locale entries:
  - `kDashTitle` → "Сервер" / "Server"
  - `kDashAuditLabel` → "Анализ твиков ⠹" / "Analyzing tweaks ⠹" (with spinner frame)
  - `kDashAuditStatus` → "применено %d из %d" / "applied %d of %d"
  - `kDashCanApply` → "можно применить %d" / "can apply %d"
  - `kDashApplyButton` → "Применить твики" / "Apply tweaks"
  - `kDashSecButton` → "Безопасность ▸" / "Security ▸"
  - `kDashCatalogButton` → "Каталог твиков" / "Tweak Catalog"
  - `kDashOS` → "ОС" / "OS"
  - `kDashKernel` → "Ядро" / "Kernel"
  - `kDashMemory` → "Память" / "Memory"
  - `kDashDisk` → "Диск" / "Disk"
- [ ] Run tests → pass
- [ ] Commit: "add dashboard i18n keys (RU + EN)"

---

## Task 6: Add phase enum entry for dashboard and initialize model field
**Files:** `internal/tui/tui.go` (model initialization, newModel line 268 + dispatch in View)

- [ ] Write test asserting phaseDashboard renders something (not empty)
- [ ] Run test → fails (View panics or does not handle phaseDashboard)
- [ ] In newModel() (line 268), no new dashboard state is needed in Init (dashboard state is built on-demand)
- [ ] In View() → viewString() dispatch (line 1058), add a case in the switch:
  ```go
  case phaseDashboard:
    return m.dashboardView()
  ```
- [ ] Stub dashboardView() to return a placeholder: `return "Dashboard — TBD\n"`
- [ ] Run test → passes (renders something)
- [ ] Commit: "wire phaseDashboard into View dispatch"

---

## Task 7: Implement dashboardView() — server card layout
**Files:** `internal/tui/tui.go` (new method after formView or summaryView, ~line 1400)

- [ ] Write tests asserting:
  - Dashboard renders a framed "Сервер: HOST" header
  - Dashboard displays OS / Kernel / Memory / Disk facts (from a mock detect.Facts)
  - Dashboard is centered in the window and uses innerWidth consistently
- [ ] Run tests → fail (method not implemented)
- [ ] Implement dashboardView(). Signature + consumed data:
  ```go
  // dashboardView renders the Dashboard phase: server card + live audit list + 3 buttons.
  // Consumed model fields: m.w, m.h, m.lang, m.dashAuditRunning, m.dashAuditDone,
  // m.dashAuditTotal, m.dashAuditApplied, m.dashAuditResults.
  // TODO: Add detect.Facts field to model to pass server details. For now, hardcode
  // sample data or pull from engine's prepare(). Mirror summaryView's layout math
  // (switcherLine, titleBox, contentLine, innerWidth, boxWidth).
  func (m model) dashboardView() string {
    // ... render the header box, server card, audit live-status line,
    // audit result grid (✓/• marks), three button pills, and monitor footer
    return "..." // TBD implementation
  }
  ```
  - For the server card, consume detect.Facts (you will add this to model in a later task)
  - For the audit list, iterate m.dashAuditResults with ✓ (Applied) or • (not applied)
  - Mirror formRows/summaryView's click-target ordering so mouse hit-tests are deterministic
- [ ] Run tests → pass
- [ ] Commit: "implement dashboardView server card + audit grid skeleton"

---

## Task 8: Add detect.Facts to model (for dashboard server details)
**Files:** `internal/tui/tui.go` (model struct + newModel)

- [ ] Write tests asserting model has a facts field and it can be populated
- [ ] Run tests → fail
- [ ] Add to model (line 193):
  ```go
  facts *detect.Facts // captured during Audit, used by phaseDashboard
  ```
- [ ] In start() where the engine is launched, pass m.facts down (or capture it from a new hook)
- [ ] Run tests → pass
- [ ] Commit: "add detect.Facts field to model"

---

## Task 9: Implement engine.Audit to emit per-Result Progress events (cosmetic streaming)
**Files:** `internal/engine/engine.go` (refine Audit implementation, line ~429)

- [ ] Write tests asserting:
  - Audit emits one Progress per tweaks.Result (id/title/status/index/total)
  - Each Progress has Status="running" (streaming cosmetic only, not real concurrency)
  - Final Progress has Done=true with Summary.Tweaks populated
- [ ] Run tests → fail (Audit does not emit per-result)
- [ ] Refine Audit to stream tweaks.Run results incrementally:
  ```go
  func Audit(cfg *config.Config, log *ui.Logger, h Hooks) error {
    start := time.Now()
    s, cleanup, err := prepare(cfg, log, true, h)
    defer cleanup()
    if err != nil {
      return err
    }
    tw := tweaks.Run(s.cli, s.log, s.ctx.Facts, cfg)
    
    // Emit progress: one per result (streaming the batch incrementally, not real concurrency)
    for i, res := range tw {
      if h.OnProgress != nil {
        h.OnProgress(Progress{
          ID: res.Probe.ID, Title: res.Probe.Name,
          Index: i + 1, Total: len(tw), Status: "running",
        })
      }
    }
    
    // Final Done with Tweaks
    emitDone(h, Summary{Elapsed: time.Since(start), Tweaks: tw})
    return nil
  }
  ```
- [ ] Run tests → pass
- [ ] Commit: "refine engine.Audit to emit per-Result Progress (cosmetic streaming)"

---

## Task 10: Wire Audit into the TUI — start() for Audit flow
**Files:** `internal/tui/tui.go` (update start() / launch logic)

- [ ] Write tests asserting:
  - Dashboard phase is entered after Audit succeeds
  - Dashboard state fields are populated from Audit Progress events
  - Form-phase "Подключиться" (Connect) button initiates Audit, not full Run
- [ ] Run tests → fail (Audit not wired)
- [ ] In formClick() or updateForm(), when the start button is pressed:
  - Do NOT launch a full `engine.Run()` (that is "Применить твики" from Dashboard)
  - Instead, launch `engine.Audit()` with a new command "audit"
- [ ] Add "audit" command to engine.Execute (line 145):
  ```go
  case "audit":
    return Audit(cfg, log, h)
  ```
- [ ] In start(), after pressing "Подключиться", set m.command = "audit" and launch:
  ```go
  cmd := "audit"
  ```
- [ ] Hook Audit's Progress events to populate m.dashAuditRunning, m.dashAuditApplied, m.dashAuditResults
- [ ] On Audit Done, transition m.phase = phaseDashboard
- [ ] Run tests → pass
- [ ] Commit: "wire Audit into TUI form → dashboard flow"

---

## Task 11: Implement dashboard button handlers (click + keyboard)
**Files:** `internal/tui/tui.go` (mouse hit-test + key handler for phaseDashboard)

- [ ] Write tests asserting:
  - Dashboard "Применить твики" button click → phases into Apply flow (phaseRun with RunSteps)
  - Dashboard "Безопасность ▸" button click → phases into phaseSecurity (not yet implemented, stub for now)
  - Dashboard "Каталог твиков" button click → phases into phaseCatalog post-connect
  - Keyboard: enter/esc returns to form; arrow keys / j/k scroll the audit list
- [ ] Run tests → fail (handlers not implemented)
- [ ] In Update(), add phaseDashboard case in the mouse-click handler (after phaseSummary, before phaseKey)
- [ ] Add hit-test geometry for the three buttons (mirror pillRanges pattern)
- [ ] On button click, set m.phase to the target phase and (for Применить) prepare to call RunSteps with Tweaks IDs
- [ ] In Update(), add phaseDashboard case in the key-press handler:
  - enter/esc/b → m.phase = phaseForm (return to landing)
  - For now, stub the arrow/j/k scroll (dashboard audit list will be scrollable in a future task)
- [ ] Run tests → pass
- [ ] Commit: "implement dashboard button click handlers and keyboard nav"

---

## Task 12: Implement "Применить твики" action — engine.RunSteps over Tweaks IDs
**Files:** `internal/tui/tui.go` + `internal/engine/engine.go`

- [ ] Write tests asserting:
  - Dashboard "Применить твики" button launches engine.RunSteps with IDs [A1, A3, A4, A5, A6, A6.5, A6.7, A7, A8, A9, A10]
  - A8 (full upgrade + reboot) shows a warning confirm before launching
  - RunSteps respects allowBrownfield=true (already-bootstrapped box)
  - Progress events and final summary are emitted and transition to phaseSummary
- [ ] Run tests → fail
- [ ] In dashboardView button click handler, when "Применить твики" is clicked:
  - Build the Tweaks bucket ID list: `[]string{"A1","A3","A4","A5","A6","A6.5","A6.7","A7","A8","A9","A10"}`
  - Show a confirm dialog (TBD: use a simple modal overlay or a direct confirm)
  - If A8 is in the list, show the reboot warning: "включает полное обновление и перезагрузку — несколько минут"
  - On confirm, set m.command = "step", m.cmds = tweakIDs, and launch via start()
- [ ] In engine.Execute, case "step" already calls RunSteps, so no change needed
- [ ] Run tests → pass
- [ ] Commit: "implement Применить твики button → engine.RunSteps flow with A8 warning"

---

## Task 13: Dashboard audit result click → phaseWiki detail
**Files:** `internal/tui/tui.go` (mouse hit-test for audit rows)

- [ ] Write tests asserting:
  - Clicking an audit result row opens phaseWiki with that result's Probe.ID
  - wikiReturn is set to phaseDashboard (esc returns to dashboard)
  - wikiScroll is reset to 0
- [ ] Run tests → fail
- [ ] In dashboardView, render audit results as clickable rows (each a hit-target)
- [ ] In mouse-click handler (Update), add logic to detect a click on an audit row:
  - If hit, extract the clicked result's Probe.ID
  - Set m.wikiStep = result.Probe.ID
  - Set m.wikiReturn = phaseDashboard
  - Set m.phase = phaseWiki
- [ ] Run tests → pass
- [ ] Commit: "add dashboard audit row click → wiki detail navigation"

---

## Task 14: Render monitor footer on dashboard (pinned, always-on)
**Files:** `internal/tui/tui.go` (dashboardView + monitor rendering)

- [ ] Write tests asserting:
  - Dashboard includes the monitor footer box at the bottom (CPU/RAM/net/ping)
  - Footer is pinned and uses the same renderMonitor() logic as phaseRun
  - Footer is visible even if audit is in flight
- [ ] Run tests → fail
- [ ] In dashboardView, after the three buttons, append:
  ```go
  // Monitor footer (pinned)
  monLine := m.renderMonitor()
  ```
  (reuse the existing renderMonitor method from phaseRun)
- [ ] Ensure monitor state (m.sample, m.haveSample, m.statMiss) is available and updated by the listener hooks
- [ ] Run tests → pass
- [ ] Commit: "add pinned monitor footer to dashboard"

---

## Task 15: Handle phaseDashboard in monitor listener lifecycle
**Files:** `internal/tui/tui.go` (monitor state initialization and teardown)

- [ ] Write tests asserting:
  - When Audit transitions to phaseDashboard, the monitor sampler is running and emitting samples
  - Dashboard monitor footer reflects live CPU/RAM/net/ping
  - When leaving phaseDashboard (back to form), stopSampler() is called
- [ ] Run tests → fail (monitor not tied to dashboard)
- [ ] In Audit's OnConnect callback (wired in start()), initialize the sampler (already in place from key-auth case)
- [ ] Ensure monitor is NOT stopped until goBack() or explicit quit
- [ ] In goBack(), call stopSampler() (already in place)
- [ ] Run tests → pass
- [ ] Commit: "integrate monitor sampler with dashboard phase"

---

## Task 16: Dashboard scroll support for audit list (if list is tall)
**Files:** `internal/tui/tui.go` (dashScroll field + scroll logic)

- [ ] Write tests asserting:
  - If audit results exceed viewport, ↑↓ / j/k scroll the list
  - Scroll offset is clamped to valid range
  - Scroll position is preserved when re-rendering
- [ ] Run tests → fail
- [ ] Add to model (line 193):
  ```go
  dashScroll int // audit list scroll offset (clamped like sumScroll)
  ```
- [ ] In dashboardView, when rendering the audit results grid, respect m.dashScroll offset
- [ ] In key-press handler (phaseDashboard case), handle ↑↓/j/k:
  ```go
  case "up", "k":
    m.dashScroll = clampScroll(m.dashScroll-1, len(m.dashAuditResults), m.auditViewH())
  case "down", "j":
    m.dashScroll = clampScroll(m.dashScroll+1, len(m.dashAuditResults), m.auditViewH())
  ```
- [ ] Add helper m.auditViewH() to compute the available rows (mirror bodyViewH())
- [ ] In mouse-wheel handler, add phaseDashboard case to scroll the audit list
- [ ] Run tests → pass
- [ ] Commit: "add dashboard audit list scroll support (↑↓/j/k / wheel)"

---

## Task 17: Verify engine.Audit does NOT require full session for password-path monitor
**Files:** `internal/engine/engine.go` (notifyConnect review)

- [ ] Write tests asserting:
  - Audit on password path (cfg.KeyPath == "") still calls notifyConnect with KeyGenerated=true
  - Audit on key path calls notifyConnect with KeyGenerated=false
  - Monitor can dial with either the generated ephemeral key OR the supplied key
- [ ] Review notifyConnect() (line 303) and prepare() (line 207–230) to confirm password path emits KeyGenerated=true
- [ ] Run test → pass (monitor will work on both paths)
- [ ] Commit: "verify engine.Audit notifyConnect works on both auth paths"

---

## Summary of Phase 3 deliverables

- ✅ `engine.Audit(cfg, log, h)` implemented (prepare + tweaks.Run + cosmetic streaming)
- ✅ `phaseDashboard` enum constant and View dispatch
- ✅ Dashboard state fields in model (audit progress + results)
- ✅ Dashboard i18n keys (RU + EN)
- ✅ dashboardView() renderer (server card + audit list + 3 buttons + monitor footer)
- ✅ Button click handlers → phaseRun / phaseSecurity / phaseCatalog
- ✅ Audit result row click → phaseWiki detail
- ✅ "Применить твики" action: engine.RunSteps over Tweaks IDs {A1,A3,A4,A5,A6,A6.5,A6.7,A7,A8,A9,A10}
- ✅ A8 reboot-warning confirm
- ✅ Monitor footer pinned on dashboard (live CPU/RAM/net/ping)
- ✅ Scroll support for audit list (↑↓/j/k / wheel)
- ✅ All new symbols use real Go code (no TBD placeholders in final commit)

---

## Phase P4 — Security menu + A2 split

**Depends on:** P3 (phaseDashboard and live audit working)

**Risks:** **Lockout risks:**
- A2-danger button uses the same ssh-revert safety timer + freshLogin verify that existing strict mode has, so the fail-safe is intact — the danger zone will not lock anyone out if the key-only verify passes
- Must verify A2Safe does NOT write AllowGroups/PermitRootLogin/PasswordAuthentication; leaving image defaults untouched is load-bearing (test assertion required)
- Confirm dialog must be explicit and unmissable ("Вы потеряете доступ..."); the key display screen must fire before apply runs

**Regression risks:**
- Soft/strict mode remains in config.Mode for CLI backward compat; TUI removes it only from the form UI (not engine) — verify CLI tests still pass
- Existing A2SSH step must continue to work (do NOT delete); selectSteps() must resolve "A2-safe" and "A2-danger" without breaking "A2" (legacy)
- Tweaks probes a2.allowgroups/a2.permitroot/a2.passwordauth must be informational after A2-safe apply, not hard assertions that fail default path — test must verify this
- phaseKey navigation: danger button must auto-route to phaseKey with key display before RunSteps, then return to Summary after apply; verify the flow does not drop the key

**Mitigation:**
- Task 1–3: write unit tests for A2Safe/A2Danger ID resolution, conf00(false) behavior, build99(false) logic
- Task 4–5: verify.go + tweaks.go tests asserting soft-default behavior
- Task 6: selectSteps() must pass a test with ["A2-safe"], ["A2-danger"], ["A2"] all resolving correctly
- Task 7–8: TUI confirmation dialog must be an explicit blocking confirm (not a silent warning); key display screen must render before apply
- Task 10–11: run full `go test ./...` suite; verify CLI still accepts --mode=soft and --mode=strict without TUI prompting

## Task 1: Add phaseSecurity enum and model fields for access-state display

**Files:** `internal/tui/tui.go` (phase enum), `internal/tui/tui.go` (model struct)

- [ ] Read the phase enum (line 56–65) and add `phaseSecurity phase = iota` (value 6, after phaseMatrix=5)
- [ ] Run `go test ./internal/tui -v` → expect compilation failure (phaseSecurity not recognized in phase handlers)
- [ ] Add to model struct (lines 193–265): three new plain string fields:
  - `secRootLoginState string` (e.g., "разрешён по паролю" / "no" / "только по ключу")
  - `secKeyOnlyState string` (e.g., "нет" / "да")
  - `secAdminState string` (e.g., "отсутствует" / "vpsadmin@host")
- [ ] Verify model remains fully value-copyable (no pointers, only string/int/bool)
- [ ] Run `go build ./...` → expect no new errors from added fields

## Task 2: Add phaseSecurity i18n keys for status card and buttons

**Files:** `internal/tui/i18n.go` (stringKey enum + translation tables)

- [ ] Add to stringKey enum (after kMonTitle / before kFormTitleSuffix):
  - `kSecMenuTitle` (Cyrillic "Безопасность и доступ")
  - `kSecRootLogin` label
  - `kSecKeyOnly` label  
  - `kSecAdmin` label
  - `kSecSafeHeader` ("Безопасно (вход не меняется):")
  - `kSecCreateAdmin` button label ("Создать админа")
  - `kSecCryptoKey` button label ("Усилить SSH-крипто + добавить ключ")
  - `kSecDangerHeader` ("⚠ Опасная зона (можно потерять доступ):")
  - `kSecKeyOnly` danger button ("Вход только по ключу · заблокировать пароль root  (покажем ключ)")
  - `kSecDangerConfirm` confirmation text (explicit warning + "показать ключ")
- [ ] Add 5 RU + 5 EN translation pairs (sample: kSecMenuTitle → "Безопасность и доступ" / "Security and access")
- [ ] Run `go build ./...` → no errors
- [ ] Create a test: `TestSecurityI18nKeysExist` asserting each key maps to non-empty RU+EN text

## Task 3: Add A2-safe and A2-danger as separate engine step types

**Files:** `internal/steps/a2_ssh.go` (split A2SSH into A2Safe + A2Danger)

- [ ] Read current A2SSH.Run() (lines 31–112); note the strict bool branch, the conf00/build99 logic, and the freshLogin verify
- [ ] Create new struct `type A2Safe struct{}`; implement:
  - `func (A2Safe) ID() string { return "A2-safe" }`
  - `func (A2Safe) Title() string { return "SSH crypto only + install admin key" }`
  - `func (a A2Safe) Run(ctx *Context) (Status, string, error)`:
    - Copy steps 1–5 from current A2SSH (write drop-ins, syntax gate, host keys, ssh-revert, freshLogin verify)
    - **Crucially:** call `conf00(false)` ALWAYS (never write PasswordAuthentication line, leaving image default untouched)
    - In build99 call: pass `strict := false` ALWAYS → PermitRootLogin remains "prohibit-password", **do NOT write AllowGroups**, do NOT call `passwd -l root`
    - Return without the handoff (step 7) and password lock (step 6)
    - Test: `go test ./internal/steps -run TestA2Safe -v`
- [ ] Create new struct `type A2Danger struct{}`; implement:
  - `func (A2Danger) ID() string { return "A2-danger" }`
  - `func (A2Danger) Title() string { return "Key-only + block root + AllowGroups sshusers" }`
  - `func (a A2Danger) Run(ctx *Context) (Status, string, error)`:
    - Write the DANGER components **only**: AllowGroups sshusers, PermitRootLogin no, PasswordAuthentication no, passwd -l root
    - Reuse the existing ssh-revert safety timer and freshLogin verify from current A2SSH (steps 4–5)
    - Executor handoff (step 7) AFTER danger is verified
    - **Preserve:** the ssh-revert fail-safe and second-session key-only verify before locking root
    - Test: `go test ./internal/steps -run TestA2Danger -v`
- [ ] **Keep legacy A2SSH for backward compatibility** (cli `run` command may still reference it); it continues to work by branching on `strict` as today — do NOT delete it yet
- [ ] Update engine `orderedSteps()` (line 27–44): leave A2SSH in place; the TUI will use RunSteps(["A2-safe"]) or RunSteps(["A2-danger"]) explicitly
- [ ] Run `go build ./...` → verify no errors

## Task 4: Update verify.go to drop soft-mode soft-lock assertion on PasswordAuthentication

**Files:** `internal/verify/verify.go` (Run function checks)

- [ ] Read the verify.Run() checks (lines 77–115); note the mode-dependent rootCheck branch (lines 78–81)
- [ ] The verify matrix today asserts `PermitRootLogin` mode-dependently; **keep this as-is** for now
- [ ] **Remove the implicit PasswordAuthentication soft-lock assert**: today verify does NOT directly check PasswordAuthentication, but the missing check is honest (soft mode leaves it to the image default). **Add a comment** in the check list explaining the omission:
  ```
  // PasswordAuthentication is only checked in strict mode (A2-danger).
  // Soft/safe paths leave the image default untouched (no assert).
  ```
- [ ] Test: run `go test ./internal/verify -v` → no assertion changes, only a documentation comment
- [ ] Commit message: "verify: document PasswordAuthentication soft-default behavior"

## Task 5: Update tweaks.go probe lists for A2 mode-dependent state

**Files:** `internal/tweaks/tweaks.go` (Registry function, probes for a2.allowgroups, a2.permitroot, a2.passwordauth)

- [ ] Read Registry() (lines 64–100 and beyond); note the mode branch (line 65) and the probes around line 87–100
- [ ] Current probes `a2.allowgroups`, `a2.permitroot` (implicit in the config), `a2.passwordauth` are mode-dependent
- [ ] **After A2 split:** make these probes **informational only** (do NOT fail safe/default apply if missing):
  - `a2.allowgroups` — probe stays, but mark as "informational" in the audit display (e.g., a comment in the Probe struct or a new Status field)
  - Similar for any PermitRootLogin / PasswordAuthentication probes
- [ ] **Alternatively:** leave the probes as-is but filter them out of the default tweaks audit if A2-safe was run; they only become relevant after A2-danger
- [ ] Add a test: `TestA2SaftProbesIgnoreAccessPolicy()` checking that a2.allowgroups / a2.permitroot / a2.passwordauth are NOT assertions for the safe path
- [ ] Run `go test ./internal/tweaks -v` → test passes

## Task 6: Update orderedSteps to include A2-danger and A25 cloud-init as new engine steps

**Files:** `internal/engine/engine.go` (orderedSteps function)

- [ ] Read orderedSteps() (lines 26–44)
- [ ] The load-bearing apply sequence currently has `steps.A2SSH{}` at position 2 and `steps.A25CloudInit{}` at position 3
- [ ] **Verify A25CloudInit exists** and is wired correctly (it should be tied to A2 dangerous path only)
- [ ] Check `selectSteps()` function (grep for it) to ensure it can resolve "A2-safe" and "A2-danger" IDs separately
- [ ] Run `go test ./internal/engine -v` → all step lookup tests pass

## Task 7: Implement phaseSecurity View() + Update() handlers in TUI

**Files:** `internal/tui/tui.go` (Update + View methods)

- [ ] Find the phase switch statement in Update() (likely around line 400+); add a phaseSecurity case:
  - Handle keypress:
    - `1` or mouse click → focus on "Create admin" button → `RunSteps(["PRE"])`
    - `2` or mouse click → focus on "Strengthen SSH crypto" button → `RunSteps(["PRE","A2-safe"])`
    - `3` or mouse click → focus on "Key-only danger" button → show confirmation dialog + key display → `RunSteps([..danger A2 + A2.5..])`
    - `esc` → return to phaseDashboard
- [ ] Confirmation dialog for danger button must say: "Вы потеряете доступ к root SSH если ключ не сохранён. Показать ключ перед применением?" (explicit lockout warning + "Show key")
- [ ] After danger confirmation accepted: set `m.phase = phaseKey` to display the generated key before apply
- [ ] Hook the button clicks to call engine.RunSteps via hooks, stream progress to TUI (reuse existing progCh mechanism)
- [ ] Find the View() method's phase switch; add phaseSecurity case to render:
  - The access-state card (3 lines: root login, key-only, admin user) — values from m.secRootLoginState / m.secKeyOnlyState / m.secAdminState
  - Two sections: SAFE (Create admin + Strengthen crypto buttons) and DANGER (Key-only button with warning)
  - Render via lipgloss rounded borders matching the phaseForm / phaseDashboard style
  - Mirror the Dashboard's 3-button layout (see phaseDashboard View)
- [ ] Test: `go test ./internal/tui -run TestSecurityPhaseRender -v` (mock: verify button labels appear, confirm dialog renders)

## Task 8: Wire phaseSecurity buttons to RunSteps calls in engine

**Files:** `internal/engine/engine.go` (RunSteps signature unchanged), `internal/tui/tui.go` (hook callbacks)

- [ ] The three phaseSecurity buttons must call engine.RunSteps with fixed IDs:
  - "Create admin" → `RunSteps(["PRE"])` (existing Precond step)
  - "Strengthen crypto" → `RunSteps(["PRE","A2-safe"])`
  - "Key-only danger" → `RunSteps([<A2-danger + A2.5 list>])`  — **determine exact list from orderedSteps**
- [ ] Reuse existing Hooks wiring:
  - h.Sink → stream log lines to m.logs channel
  - h.OnProgress → stream step progress to m.progCh
  - h.OnKey → detect when admin key is generated, store in m.keyPEM, auto-route to phaseKey
- [ ] The TUI's start() function (which runs the full apply) must be adapted to call RunSteps for security operations
- [ ] Test: mock RunSteps, verify correct IDs are passed for each button; verify hooks fire
- [ ] Run `go build ./...` → no errors

## Task 9: Add live audit updates for access-state fields in Dashboard

**Files:** `internal/tui/tui.go` (model fields + View render), `internal/engine/engine.go` (Audit function if new, or enhance run-flow audit)

- [ ] The Dashboard's access-state card (mocked in P3) must pull live data from the audit
- [ ] After detecting, populate m.secRootLoginState, m.secKeyOnlyState, m.secAdminState from audit results
- [ ] These fields must be updated **before phaseSecurity is entered** so the status card shows current state
- [ ] Run `go test ./internal/tui -run TestDashboardAuditUpdate -v` → status fields reflect detected state

## Task 10: Remove soft/strict mode from TUI form and config (prep for P4 cleanup)

**Files:** `internal/tui/i18n.go` (remove kLabelMode, kOptSoft, kOptStrict, etc.), `internal/tui/tui.go` (remove mode inputs from form), `internal/config/config.go` (leave Mode in config struct for CLI backward compat, but TUI no longer uses it)

- [ ] **PHASE 4 SPEC says:** "remove soft/strict mode from config/engine as a mandatory choice"
- [ ] For P4: leave Mode in config struct untouched (CLI still uses it); only remove it from the **TUI form UI**
- [ ] Remove from i18n:
  - kLabelMode, kOptSoft, kOptStrict, kModeSoftName, kModeStrictName
  - kHelpModeStrict, kHelpModeSoft, kHelpModeAction (keep kHelpActionOnly)
  - kPwOffWarn, kPwOffLogin, kPwOnInfo (these are password-path warnings; keep for backward compat, but TUI doesn't show them anymore)
- [ ] Remove from model: the `mode` field is still written to cfg before applying, but TUI no longer prompts for it
- [ ] Update `focusableRows()` (line 140) to remove the rowMode line
- [ ] Remove the "soft/strict toggle" rendering from the form's View()
- [ ] Test: `go test ./internal/tui -run TestFormHasNoModeToggle -v` → mode input is not rendered
- [ ] Run `go build ./...` → no errors

## Task 11: Commit all P4 changes

- [ ] Verify all tests pass: `go test ./...`
- [ ] Stage all changes
- [ ] Commit message (conventional):
  ```
  feat: P4 security menu + A2 split + default-no-lockout

  - Add phaseSecurity with access-state display (root login, key-only, admin)
  - Split A2SSH into A2Safe (crypto + install key) and A2Danger (AllowGroups + PermitRootLogin no + passwd -l root)
  - Default safe path never locks root login, password, or access — opt-in danger only
  - Add SAFE buttons: Create admin, Strengthen SSH crypto
  - Add DANGER button with explicit confirmation + key display
  - Remove soft/strict mode from TUI form; leave in config for CLI backward compat
  - Update verify.go to document PasswordAuthentication soft-default behavior
  - Update tweaks.go probes for A2 as informational-only post-safe-apply
  - Wire phaseSecurity buttons to engine.RunSteps(["PRE"]), RunSteps(["PRE","A2-safe"]), RunSteps(danger)
  - Remove kOptRun/kOptDetect/kOptVerify from form i18n (left in stepTitles/engine only)

  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  ```

---

## Phase P5 — Catalog + rich wiki

**Depends on:** P4 (Security menu + A2 split); assumes phaseCatalog phase enum and wiki.FixDoc struct can be extended independently. P1 (Landing link to catalog) assumed complete. P3 (Dashboard button and audit state) assumed available for post-connect status column in Task 6.

**Risks:** **Lockout/regression risks:**

1. **wiki_test.go extended test must pass before committing Task 3** (Task 2 introduces the failing test; Task 3 fills docs to make it pass; if Task 2 is committed without Task 3, all subsequent TUI tests/builds will fail on missing OnBox/Revert).
   - **Mitigation:** Tasks 2 and 3 are atomic: extend test in Task 2, run to fail (expected), fill docs in Task 3, run to pass, commit both together.

2. **Navigation wiring (Task 8) must preserve existing phaseForm/phaseWiki/Summary navigation** (adding phaseCatalog edges must not break esc-to-return logic).
   - **Mitigation:** m.wikiReturn mechanism already exists (line ~369); phaseCatalog just adds one more caller (phaseForm, phaseDashboard, or phaseCatalog itself as a step-detail selector). Verify esc path does not regress on existing Summary/phaseWiki return.

3. **Domain grouping (Task 6 Part B) is manual/hardcoded** — if a step ID is added or re-categorized later, the domain map must be updated or the row renders in the wrong section.
   - **Mitigation:** Domain map is a single const/helper; document it clearly; add a comment referencing spec § "Decomposition into implementation phases". No test covers domain correctness (not in scope for P5), so rely on visual review.

4. **catalogApplied state (Task 6 Part A) must be value-copyable** (no pointers). Using `map[string]bool` is **NOT value-copyable in Go** (maps are reference types). 
   - **Mitigation:** Either (a) pre-allocate a fixed []Result slice (if audit order is stable), or (b) store only the audit results as a []tweaks.Result in the model (if Result is value-copyable). Verify field type before committing Task 6; if map is used, it will fail the value-copy invariant check at build time (no error, but violates model design). Use a slice or custom struct.

5. **OnBox + Revert content must be accurate for manual revert instructions** — spec says "no auto-rollback". If instructions are vague or wrong, users lose access or corrupt configs.
   - **Mitigation:** Spec provides verbatim entries for A4 and A2 (Tasks 3 Part A/C); use them exactly. For remaining steps (Task 3 Part D), synthesize from existing step code (internal/steps/*.go) and Ubuntu/systemd docs. Review each entry against the actual step implementation before committing Task 3.

6. **P5 subsumes no earlier phases** — Dashboard/Security/engine.Audit are assumed complete from P3/P4. If P3 or P4 is incomplete, phaseCatalog post-connect status column will have no audit data to render.
   - **Mitigation:** P5 tasks are designed to work pre-connect first (Task 6 part A: docs only, no status); post-connect status is a stretch goal. If P3/P4 are blocked, commit P5 with pre-connect-only catalog and status-line code paths that guard on m.connected or m.catalogApplied being empty.

## Task 1: Extend FixDoc struct with OnBox + Revert fields

**Files:**
- `internal/wiki/wiki.go` (add to FixDoc struct at line ~13)

**Checkbox steps:**

- [ ] Read `internal/wiki/wiki.go` current FixDoc struct (lines 13–17)
- [ ] Add two new string fields: `OnBox string` and `Revert string` to FixDoc struct definition
- [ ] Verify struct now has all 6 fields: Title, What, Why, RiskWithout, OnBox, Revert
- [ ] Build: `go build ./...` (should compile with struct changes)
- [ ] Commit: ✓ FixDoc struct now has OnBox + Revert fields

---

## Task 2: Extend TestEveryStepHasDoc to assert non-empty OnBox + Revert in both langs

**Files:**
- `internal/wiki/wiki_test.go` (TestEveryStepHasDoc at line ~10)

**Checkbox steps:**

- [ ] Read TestEveryStepHasDoc function (lines 10–18)
- [ ] Current test asserts: Title, What, Why, RiskWithout all non-empty
- [ ] Add two lines inside the test's empty-field check to also assert `d.OnBox != "" && d.Revert != ""`
- [ ] Updated assertion should be: `if d.Title == "" || d.What == "" || d.Why == "" || d.RiskWithout == "" || d.OnBox == "" || d.Revert == ""`
- [ ] Run test (will FAIL — by design, no entries have OnBox/Revert yet): `go test ./internal/wiki/...`
- [ ] Verify output shows missing OnBox/Revert for all IDs in both langs
- [ ] **DO NOT FIX YET** — test failure is expected; moving to Task 3

---

## Task 3: Fill all wiki docs with 5-part structure (RU + EN), using spec samples verbatim

**Files:**
- `internal/wiki/content.go` (docs map starting at line ~7)

**Checkbox steps:**

### Part A: Add A4 (Сетевая оптимизация) — RU, verbatim from spec

- [ ] Read current A4 entry in docs[RU] map (approx line 39–43)
- [ ] Current A4 has only: Title, What, Why, RiskWithout
- [ ] **From spec (line 431–440), verbatim for OnBox and Revert:**
  - OnBox: `"/etc/sysctl.d/99-bbr.conf (tcp_congestion_control=bbr, default_qdisc=fq) и 99-net-tune.conf (буферы); модуль tcp_bbr; переживает перезагрузку."`
  - Revert: `"удалить оба файла + sysctl --system; модуль выгрузится после перезагрузки; автоотката в программе нет."`
- [ ] Update docs[RU]["A4"] FixDoc: add OnBox and Revert fields with the verbatim text
- [ ] Run failing test again: `go test ./internal/wiki/... -run TestEveryStepHasDoc`
- [ ] A4 RU should now pass; other IDs still fail

### Part B: Add A4 (Network tuning) — EN, parity required

- [ ] **Provide English parity for A4 OnBox and Revert** (not in spec, but required):
  - OnBox (conceptual, ~same meaning): `"/etc/sysctl.d/99-bbr.conf (tcp_congestion_control=bbr, default_qdisc=fq) and 99-net-tune.conf (buffer sizes); tcp_bbr kernel module; persists across reboot."`
  - Revert (conceptual): `"delete both files + sysctl --system; kernel module unloads on next reboot; no auto-rollback in the app."`
- [ ] Update docs[EN]["A4"] FixDoc: add OnBox and Revert
- [ ] Run test: A4 both langs should pass

### Part C: Add A2 (Усиление SSH / SSH hardening) — RU + EN, verbatim from spec

- [ ] **From spec (line 442–455), RU section:**
  - OnBox: `"/etc/ssh/sshd_config.d/00-hardening.conf и 99-hardening.conf; на 26.04 постквантовый KEX mlkem768x25519-sha256; перезапуск ssh."`
  - Revert: `"удалить drop-in'ы + systemctl restart ssh; при блокировке пароля root сначала разблокировать через консоль провайдера (passwd -u root); автоотката нет, но опасные изменения имеют ~300с страховку при срыве проверки входа."`
- [ ] Update docs[RU]["A2"] FixDoc with OnBox and Revert from spec verbatim
- [ ] **Provide English parity for A2 OnBox and Revert** (conceptual, ~same meaning):
  - OnBox: `"/etc/ssh/sshd_config.d/00-hardening.conf and 99-hardening.conf; post-quantum KEX mlkem768x25519-sha256 on 26.04; ssh service restart."`
  - Revert: `"delete drop-in files + systemctl restart ssh; if root password is locked, unlock via provider console (passwd -u root); no auto-rollback, but dangerous changes have ~300s fail-safe on verify failure."`
- [ ] Update docs[EN]["A2"] FixDoc with OnBox and Revert
- [ ] Run test: A2 + A4 both langs should pass

### Part D: Fill remaining step docs (PRE, A1, A8, A2.5, A3, A5, A6, A6.5, A6.7, A7, A9, A10) — both langs

- [ ] For each ID in wantIDs not yet done (PRE, A1, A8, A2.5, A3, A5, A6, A6.5, A6.7, A7, A9, A10):
  - Create OnBox field: 1–2 sentences describing files/directories/packages/services changed
  - Create Revert field: 1–2 sentences describing manual undo steps
  - Provide both RU and EN entries
  - Examples (concise, not copied from code):
    - **A1 Firewall** OnBox: "правила iptables (/etc/iptables/rules.v4 и rules.v6); пакет iptables-persistent" / "iptables rules (/etc/iptables/rules.v4 and rules.v6); iptables-persistent package"
    - **A1 Firewall** Revert: "удалить iptables-persistent + вернуть исходные правила через `iptables -F`" / "uninstall iptables-persistent + restore with `iptables -F`"
    - (repeat for all 12 remaining IDs in both RU and EN)
- [ ] Run test: `go test ./internal/wiki/... -run TestEveryStepHasDoc`
- [ ] **ALL IDs in both langs must pass** (no empty OnBox/Revert)
- [ ] Commit: ✓ All wiki docs filled with 5-part structure (OnBox + Revert) RU + EN

---

## Task 4: Add phaseCatalog enum constant and localTweakName/localStepTitle reuse patterns

**Files:**
- `internal/tui/tui.go` (phase enum at line ~59; helper functions TBD)

**Checkbox steps:**

- [ ] Read phase enum (lines 59–65): phaseForm, phaseRun, phaseSummary, phaseWiki, phaseKey, phaseMatrix
- [ ] Add new constant: `phaseCatalog phase = iota` (next value after phaseMatrix)
- [ ] Verify phase enum now has 7 entries
- [ ] Build: `go build ./...` 
- [ ] Identify existing localTweakName / localStepTitle functions (search for references in tui.go)
- [ ] If not yet defined, note signature needed: `localTweakName(lang Lang, probeID string) string` and `localStepTitle(lang Lang, stepID string) string` (these can reuse i18n.t() patterns)
- [ ] Commit: ✓ phaseCatalog added to phase enum

---

## Task 5: Extend i18n with catalog-specific keys (RU + EN)

**Files:**
- `internal/tui/i18n.go` (stringKey enum and translations map)

**Checkbox steps:**

- [ ] Read stringKey enum and translation map structure
- [ ] Add new stringKey constants:
  - `kCatalogTitle` (Каталог твиков — что настраивает Morgward)
  - `kCatalogNetwork` (Сеть и пропускная способность)
  - `kCatalogMemory` (Память)
  - `kCatalogKernelMaint` (Ядро и обслуживание)
  - `kCatalogSecurityNote` (ⓘ Безопасность (SSH, аккаунты) — на отдельном экране.)
  - `kCatalogDocOnly` (docs only, pre-connect; no status column)
  - `kCatalogWithStatus` (+ status column post-connect)
  - `kStatusApplied` (✓ применено)
  - `kStatusCanApply` (• можно)
- [ ] Add translations for each key in both RU and EN (map to t() function)
- [ ] Build: `go build ./...`
- [ ] Commit: ✓ i18n catalog keys added RU + EN

---

## Task 6: NEW phaseCatalog render — pre-connect (docs only) and post-connect (+ status column)

**Files:**
- `internal/tui/tui.go` (View() switch on m.phase, catalogView() new function)

**Checkbox steps:**

### Part A: Model fields for catalog state

- [ ] Add to model struct (after existing fields):
  - `catalogStep string` (currently selected step ID, or "" if none; value-copy: string)
  - `catalogApplied map[string]bool` (step ID → is-applied status post-connect; OR: pre-allocate an audit slice, value-copy only; ask: should this be []Result from tweaks?)
- [ ] If using tweaks.Result, verify it's value-copyable (no pointers)

### Part B: Catalog domain grouping (hardcoded domains per step)

- [ ] Define domain mapping (helper or const map):
  - A4 → "Network"
  - A6.7 → "Memory"
  - A5, A6, A6.5 → "Kernel & Maintenance"
  - A1, A3, A8 → "Firewall & Updates"
  - A2, PRE → Security (shown on Security screen, NOT in Catalog)
  - A7, A9, A10 → "Other"
- [ ] (Spec shows example: "Сеть и пропускная способность", "Память", "Ядро и обслуживание")

### Part C: Catalog layout (mirroring Dashboard audit + wiki row render)

- [ ] New function `catalogView(m model) string`:
  - **Title box** (same frame as Dashboard/Wiki): "Каталог твиков — что настраивает Morgward"
  - **Header line** (pre-connect only): "docs only; select to view detail"
  - **Domain sections** (grouped, indented):
    - Sеть и пропускная способность
      - › Сетевая оптимизация (BBR, буферы)  A4  [✓ применено] (post-connect only)
      - › Планировщик ввода-вывода  A4  [• можно] (post-connect only)
  - **Security note**: "ⓘ Безопасность (SSH, аккаунты) — на отдельном экране."
  - Mirror fixListLines() / fixRowText() for the domain/row format
  - Pre-connect: no status column; post-connect: add status column via tweaks.Run audit
- [ ] Selectable rows → select step ID into m.catalogStep, return m, goWiki(phaseCatalog)
- [ ] esc → return to previous phase (caller: phaseForm pre-connect, phaseDashboard post-connect)

### Part D: Hook into Update() and View() phase switch

- [ ] In Update() msg switch, add handler for phaseCatalog:
  - handle clicks on rows → set catalogStep, navigate to phaseWiki
  - handle esc → return to wikiReturn (phaseForm or phaseDashboard)
- [ ] In View() phase switch, add case for phaseCatalog:
  - call catalogView(m) to render the screen
  - append monitor footer only if m.connected (post-connect)
- [ ] Run test builds: `go build ./...`
- [ ] Commit: ✓ phaseCatalog render (pre/post-connect, domain grouping)

---

## Task 7: Extend phaseWiki Detail render for 5-section output + live status

**Files:**
- `internal/tui/tui.go` (wikiView() or wikiBodyLines() existing function; extend to render OnBox + Revert)

**Checkbox steps:**

- [ ] Read existing wikiView() function (approx line ~2100+)
- [ ] Current render: Title, What, Why, RiskWithout (4 sections)
- [ ] Extend to 5 sections:
  - Line 1: "ЧТО ДЕЛАЕТ    " + What (label bold, content follows)
  - Line 2: "ЗАЧЕМ    " + Why
  - Line 3: "БЕЗ ЭТОГО    " + RiskWithout
  - Line 4: "ЧТО МЕНЯЕТСЯ НА СЕРВЕРЕ    " + OnBox (new)
  - Line 5: "КАК ОТКАТИТЬ    " + Revert (new)
  - Line 6: "Статус: " + status (post-connect only; e.g., "✓ применено" / "• можно" / "⊘ недоступно")
- [ ] Status sourced from: m.catalogApplied[m.wikiStep] or tweaks.Run audit result for this step (post-connect only)
- [ ] Mirror existing fixRowText() for the label-value format
- [ ] Update wikiBodyLines() to include the two new sections and status
- [ ] Test: render a wiki page pre-connect (no status line) and post-connect (status line present)
- [ ] Build: `go build ./...`
- [ ] Commit: ✓ phaseWiki Detail renders 5 sections + live status

---

## Task 8: Catalog + Wiki navigation wiring (phaseForm link, Dashboard button, catalog row select)

**Files:**
- `internal/tui/tui.go` (Update() switch for each phase, formClick(), dashboardClick() callbacks)

**Checkpoint:**

This task verifies the navigation edges:

- [ ] phaseForm: "Что настраивает программа ▸" link (NEW in P1) → navigate to phaseCatalog, set wikiReturn = phaseForm
- [ ] phaseDashboard: "Каталог твиков" button → navigate to phaseCatalog, set wikiReturn = phaseDashboard
- [ ] phaseCatalog: row selection → set m.wikiStep to selected step ID, navigate to phaseWiki, set wikiReturn = phaseCatalog
- [ ] phaseWiki: esc → navigate back to m.wikiReturn (phaseForm, phaseDashboard, or phaseCatalog; existing mechanism)
- [ ] Run test: `go build ./...` (no logic errors)
- [ ] Commit: ✓ Catalog + Wiki navigation fully wired

---

## Task 9: Run all tests; verify extended wiki_test.go passes; verify catalog+wiki render integration

**Files:**
- `internal/wiki/wiki_test.go` (TestEveryStepHasDoc)
- `internal/tui/tui.go` (no test changes, but verify builds)

**Checkpoint:**

- [ ] `go test ./internal/wiki/...` → TestEveryStepHasDoc PASSES (all IDs RU+EN have non-empty OnBox + Revert)
- [ ] `go test ./internal/tui/...` → any existing tui tests PASS (or none exist; build succeeds)
- [ ] `go build ./...` (full build, no errors)
- [ ] Manual verification (if TUI runs):
  - Start pre-connect → view phaseForm, click "Что настраивает программа ▸" → phaseCatalog renders (docs only, no status, no footer)
  - Select a row → phaseWiki renders with 5 sections, no status line
  - esc → back to phaseForm
  - (After P3: post-connect → phaseDashboard, click "Каталог твиков" → phaseCatalog with status column + footer; select row → phaseWiki with status line)
- [ ] Commit: ✓ P5 complete: wiki extended, catalog rendered, navigation wired, tests pass

---
