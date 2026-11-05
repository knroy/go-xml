package xslt

// xsl:perform-sort select="copy-of(P)" (lane MM6, si-perform-sort-001):
// P a relative path of child steps. copy-of($s) copies each item of $s in
// order, and P's matches, in document order and unable to nest, are its
// items, so the select is P ! copy-of(): each match is grounded, copied as
// its tail, and its ground let go. What is held for the sort is the copies,
// which the tree-mode run makes too beside its document, and one match's
// ground at a time. Scoped to perform-sort's select: read this way in every
// select, it took copy-of(P) of xsl:for-each off the population path (lane
// MM4, wip/H-perform-sort.patch).

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// sortCopiesSplit is the split of n's select when n is an xsl:perform-sort
// whose select is copy-of(P) or snapshot(P) as above; nil otherwise.
func (c *compiler) sortCopiesSplit(n *xdm.Node, sel *xpath.Compiled, ns xpath.NamespaceResolver, op *streamOp) *streamSplit {
	if !isXSL(n, "perform-sort") || op.inGroup || sel.CompatMode() {
		return nil
	}
	x, ok := reparse(sel, ns).(*xpath.FuncCall)
	if !ok || x.Name.URI != fnNS || x.Name.Local != "copy-of" && x.Name.Local != "snapshot" || len(x.Args) != 1 {
		return nil
	}
	steps := []xpath.Expr{x.Args[0]}
	if p, ok := x.Args[0].(*xpath.PathExpr); ok {
		if p.Root {
			return nil
		}
		steps = p.Steps
	}
	for _, st := range steps {
		if s, ok := st.(*xpath.Step); !ok || s.Axis != xpath.AxisChild {
			return nil
		}
	}
	s, _ := splitStreamIn(&xpath.SimpleMap{Left: x.Args[0], Right: &xpath.FuncCall{Name: x.Name}}, sel, op.doc, false, op.key)
	if s == nil || s.nests || s.mode != matchGround || s.tail == nil || !groupCopyMap(s.tail.Expr()) {
		return nil
	}
	s.fresh = true // the copies are the tail's own nodes (memory)
	return s
}
