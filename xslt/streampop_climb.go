package xslt

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// A for-each over the ancestors a path climbs to (lane MM3: function-5017,
// function-5018): doc//section//p/ancestor-or-self::section. The split takes
// each climbed element once, in document order, off the matches' views
// (climbing, streamleaf.go); such an element is a view of an open ancestor:
// its name, attributes, namespaces, and its own ancestors' views. A body that
// is motionless on an element's view (planned, no consuming op) reads only
// that, so it runs on each climbed view off the cursor as it is taken, which
// is where the tree's for-each puts it: the tree's items in document order.

// climbedWhy is splitStreamIn's refusal of items that are climbed views.
const climbedWhy = "split: ancestors climbed to read beyond their views: "

// climbLoop makes op, a for-each splitFor refused for its climbed views, a
// population of those views with the body run off the cursor; it says
// whether it did.
func (c *compiler) climbLoop(op *streamOp, ns xpath.NamespaceResolver) bool {
	if !strings.HasPrefix(op.dead, climbedWhy) || !isXSL(op.el, "for-each") || !op.planned ||
		len(op.inner) != 0 || viewsBeyond(op.el) {
		return false
	}
	e := reparse(op.sel, ns)
	if e == nil {
		return false
	}
	e, checks := foreignSets(c.inlineCalls(e))
	if len(checks) != 0 {
		return false
	}
	s, _ := splitStreamAt(e, op.sel, op.doc, op.inGroup, op.key, true)
	if s == nil || s.cond != nil || s.ctxItem || s.red != "" || s.mode != matchView || !s.climb ||
		!s.climbElems || s.climbSort || s.climbSet != nil || s.climbEach != nil || s.skip != 0 ||
		s.outer == nil || !isVarRef(s.outer.Expr(), splitItems) {
		return false
	}
	op.split, op.pop, op.dead = s, &streamPop{at: 0, parts: []*xpath.Compiled{nil}, off: true}, ""
	return true
}

// viewsBeyond reports, textually, whether the body of el may ask a view what
// only a match's own view at its start tag answers: has-children (a Peek of
// the cursor) or an accumulator's value (the observer's, at the match).
func viewsBeyond(el *xdm.Node) bool {
	for n := range el.Descendants() {
		var vals []string
		switch n.Kind() {
		case xdm.KindText:
			vals = append(vals, n.Value())
		case xdm.KindElement:
			if isXSL(n, "call-template") || isXSL(n, "apply-templates") || isXSL(n, "next-match") || isXSL(n, "apply-imports") {
				return true
			}
			for a := range n.Attrs() {
				vals = append(vals, a.Value())
			}
		}
		for _, v := range vals {
			if strings.Contains(v, "has-children") || strings.Contains(v, "accumulator-") {
				return true
			}
		}
	}
	return false
}
