package wiki

import "testing"

// IDs that orderedSteps applies; keep in sync with engine.orderedSteps().
var wantIDs = []string{"PRE", "A1", "A8", "A2", "A2.5", "A3", "A4", "A5", "A6", "A6.5", "A6.7", "A7", "A9", "A10"}

func TestEveryStepHasDoc(t *testing.T) {
	for _, lang := range []Lang{RU, EN} {
		for _, id := range wantIDs {
			d, ok := Doc(lang, id)
			if !ok {
				t.Fatalf("lang=%d id=%s: no doc", lang, id)
			}
			if d.Title == "" || d.What == "" || d.Why == "" || d.RiskWithout == "" || d.OnBox == "" || d.Revert == "" {
				t.Errorf("lang=%d id=%s: empty field in %+v", lang, id, d)
			}
		}
	}
}
