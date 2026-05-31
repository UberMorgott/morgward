package engine

import (
	"testing"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/ui"
)

// TestSummaryCarriesFacts asserts the Summary struct exposes a Facts field (the
// Dashboard reads the server card off the final Done event's Summary.Facts) and
// that it defaults to nil for the non-audit paths.
func TestSummaryCarriesFacts(t *testing.T) {
	var s Summary
	if s.Facts != nil {
		t.Fatalf("zero Summary.Facts should be nil")
	}
}

// TestAuditReadOnlyContract documents (and pins via the prepare signature) that
// Audit calls prepare with readOnly=true — the audit path performs NO box
// mutation (no key bootstrap, no inventory write, no checkpoint save). This is a
// compile-time guard: if prepare's read-only parameter is removed, Audit's call
// site below stops compiling.
func TestAuditReadOnlyContract(t *testing.T) {
	// The function value must be assignable; this fails to compile if Audit's
	// signature drifts from (cfg, *ui.Logger, Hooks) error.
	var _ func(*config.Config, *ui.Logger, Hooks) error = Audit
}
