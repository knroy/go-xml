package xslt

// Grouping populations popWalk cannot hand over (lane L5): attributes
// (account/transaction/@value, si-group-A11/B11/C11), text, comment or PI
// nodes (P/text()), and outermost(P) (si-group-A12/B12/C12).
//
// popWalk hands over elements of one depth, each at its start tag, for a
// group's body to read lazily. These populations are instead taken as
// heldGroup takes a group-by's (streamgroup_held.go): the split of the select
// collects them -- attributes of the matches' views, kept leaves' copies,
// grounded copies of the outermost matches, each the original to identity
// and order -- and the tree's grouping runs over them (groundedGroup). The
// memory rule is the select's (readySelect, s.memory): at the document node
// held under the cap of streamhold.go, past which the tree answers; below
// the document element as any collected copies; at the document element's
// level refused.
//
// The body runs on the tree's code over the items, so it must not reach
// instructions a plan of the run may hold (byInstr): template rules
// (apply-templates, next-match, apply-imports), named templates, another
// streamed document; nor accumulators, whose values the items' copies do
// not carry.

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// heldPopulation reports whether el, an xsl:for-each-group that is not a
// group's own, its focus f, selects a population of attributes, leaves or
// outermost matches, and its body reads nothing beyond the items.
//
// Attributes only at the document node: each is an attribute of its
// match's view, a small tree of its own with copies of the ancestors, which
// only the cap there (holdOn counts each view's records) keeps within the
// tree's; leaves' copies share their ancestors' copies, and outermost
// matches are grounded with GroundLater, whose batches share them.
func (b *streamBuilder) heldPopulation(el *xdm.Node, f vfocus) bool {
	if b.grp {
		return false
	}
	e := parseAt(el, el.AttrValue("select"))
	if e == nil {
		return false
	}
	switch attrs, ok := heldShape(e); {
	case !ok, attrs && f != vNone:
		return false
	}
	return heldBodyOK(el)
}

// heldShape reports whether e is outermost(P), or a path whose last step
// gives attributes (attrs) or, a child step, text, comment or PI nodes.
func heldShape(e xpath.Expr) (attrs, ok bool) {
	if f, isCall := e.(*xpath.FuncCall); isCall {
		return false, outerInner(f) && f.Name.Local == "outermost"
	}
	var last xpath.Expr = e
	if pe, ok := e.(*xpath.PathExpr); ok && len(pe.Steps) > 0 {
		last = pe.Steps[len(pe.Steps)-1]
	}
	st, isStep := last.(*xpath.Step)
	if !isStep {
		return false, false
	}
	if st.Axis == xpath.AxisAttribute {
		return true, true
	}
	// Not node(): its elements would be grounded one by one (Ground, each
	// copying its ancestors), not batched.
	leaf, elem := testKinds(st.Test)
	return false, leaf && !elem && st.Axis == xpath.AxisChild
}

// heldBodyOK reports whether nothing under el reaches a template, another
// document's stream or an accumulator.
func heldBodyOK(el *xdm.Node) bool {
	for ch := range el.Children() {
		if ch.Kind() != xdm.KindElement {
			continue
		}
		for _, name := range []string{"apply-templates", "call-template", "next-match", "apply-imports",
			"source-document", "evaluate"} {
			if isXSL(ch, name) {
				return false
			}
		}
		for a := range ch.Attrs() {
			if strings.Contains(a.Value(), "accumulator-") {
				return false
			}
		}
		if !heldBodyOK(ch) {
			return false
		}
	}
	return true
}
