package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// --- compositor hit-testing ---------------------------------------------------
//
// phaseKey and phaseMatrix resolve clicks through lipgloss/v2's Compositor.Hit
// instead of hand-written rectangle comparisons. The layers are never Rendered:
// the drawn frame still comes from framedScrollView, so the golden frames stay
// byte-identical and the compositor serves purely as a coordinate→id lookup.
//
// Four properties of lipgloss/v2's Compositor (layer.go) drive the rules here:
//
//   - Hit tests a layer's BOUNDING RECTANGLE, sized Width(content) x Height(content).
//     Every target is therefore its own single-line spacer sized to exactly the cells
//     it covers — ragged or over-wide content would swallow neighbouring clicks.
//   - Layers with an EMPTY id are skipped by Hit rather than blocking it, so every
//     target below carries an id.
//   - Equal-z ordering is NOT insertion order (the flatten sort is unstable), so
//     every layer sets an explicit z and overlapping targets get distinct ones.
//   - Duplicate ids are silently accepted (Hit returns the top-most), so ids are
//     namespaced per screen and never reused.
const (
	idKeyCopy   = "key:copy"
	idKeyStart  = "key:start"
	idKeyCancel = "key:cancel"
	idKeyBack   = "key:back"

	idMatrixBack   = "matrix:back"
	idMatrixRowPfx = "matrix:row:" // + the index into m.summary.Tweaks
)

// contentX0 is the first content column inside a box: border(1) + space(1).
const contentX0 = 2

// hitLayer builds one click target: a w-cell single-line spacer at (x,y) with an
// explicit z. w<=0 yields a zero-width layer, which can never be hit — the wanted
// behaviour for a target whose label rendered empty.
func hitLayer(id string, x, y, w, z int) *lipgloss.Layer {
	return lipgloss.NewLayer(strings.Repeat(" ", max(w, 0))).X(x).Y(y).Z(z).ID(id)
}

// pillLayer builds a target over the i-th pill of a pillRanges row, so the target
// matches the rendered pill's Padding(0,1) exactly.
func pillLayer(id string, names []string, startCol, i, y, z int) *lipgloss.Layer {
	r := pillRanges(names, startCol)[i]
	return hitLayer(id, r[0], y, r[1]-r[0], z)
}

// hit resolves a click to the id of the top-most target under (x,y), or "" when the
// click misses every target or the phase has no compositor layers.
//
// The compositor is rebuilt per click rather than cached on the model: View() has a
// value receiver (the model is copied by value every Update), so it cannot write a
// freshly built compositor back and a cached one would go stale on every re-render.
// Rebuilding costs one layout pass per click — the same pass the hand-written
// hit-tests already performed.
func (m model) hit(x, y int) string {
	var layers []*lipgloss.Layer
	switch m.phase {
	case phaseKey:
		layers = m.keyLayers()
	case phaseMatrix:
		layers = m.matrixLayers()
	default:
		return ""
	}
	if len(layers) == 0 {
		return ""
	}
	return lipgloss.NewCompositor(layers...).Hit(x, y).ID()
}
