// Package wiki holds localized "what/why" descriptions for each hardening fix.
// It is dependency-free (no TUI import) so other screens can reuse it.
package wiki

type Lang int

const (
	RU Lang = iota // must match tui/i18n langRU value (0)
	EN             // must match tui/i18n langEN value (1)
)

// FixDoc is the human description of one runbook fix, shown on a wiki page.
type FixDoc struct {
	Title       string
	What        string
	Why         string
	RiskWithout string
}

// Doc returns the localized doc for a step ID; ok=false if unknown.
func Doc(lang Lang, stepID string) (FixDoc, bool) {
	m, ok := docs[lang]
	if !ok {
		return FixDoc{}, false
	}
	d, ok := m[stepID]
	return d, ok
}
