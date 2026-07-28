package tui

import "testing"

// emptyOK lists stringKeys whose empty value is intentional: kFormTitleSuffix is
// a spacing-convention placeholder, not a missing translation.
var emptyOK = map[stringKey]bool{kFormTitleSuffix: true}

// TestEveryKeyResolvesInBothLangs is what keeps the {ru, en} pair tables honest:
// the pair shape makes a half-filled key a compile-time-visible `{"x", ""}`, and
// this test turns it into a build failure. tr, stepTitles and probeDescs require
// BOTH languages; skipReasons and tweakNames deliberately leave the EN slot empty
// (English reuses the raw skip detail / the probe's own English Name), so only the
// RU slot is asserted there.
func TestEveryKeyResolvesInBothLangs(tt *testing.T) {
	langs := []Lang{langRU, langEN}

	for k := kLabelHost; k <= kFmCopiedPath; k++ {
		if emptyOK[k] {
			continue
		}
		for _, lang := range langs {
			if t(lang, k) == "" {
				tt.Errorf("tr: stringKey %d has no translation for lang %d", k, lang)
			}
		}
	}

	for id := range stepTitles {
		for _, lang := range langs {
			if localStepTitle(lang, id, "") == "" {
				tt.Errorf("stepTitles[%q]: no translation for lang %d", id, lang)
			}
		}
	}

	// probeDescs completeness against the live registry is TestEveryProbeHasDesc;
	// this only asserts no table entry is half-filled.
	for id := range probeDescs {
		for _, lang := range langs {
			if d, ok := probeDesc(lang, id); !ok || d == "" {
				tt.Errorf("probeDescs[%q]: no description for lang %d", id, lang)
			}
		}
	}

	for id := range skipReasons {
		if localSkipReason(langRU, id) == id {
			tt.Errorf("skipReasons[%q]: empty RU entry", id)
		}
	}
	for id := range tweakNames {
		if localTweakName(langRU, id, "") == "" {
			tt.Errorf("tweakNames[%q]: empty RU entry", id)
		}
	}
}
