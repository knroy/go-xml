package xslt

// A local xsl:variable whose select consumes (lane J1).
//
// §19.8.4.41: the variable's value is grounded, the select its one consuming
// operand. The sequence constructor binds a variable as it reaches it
// (execSequence, eagerly: evalVariable), so the select is read off the
// cursor there, as a with-param of xsl:next-iteration is (an expression op:
// evalStreamExpr); what follows it in the sequence constructor is
// motionless (one consumer). Its split is held to the memory rule as any
// select's (readySelect): a fold, values, or copies under the cap at the
// document node.

import (
	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// variableOp plans el, a local xsl:variable with a consuming select.
func (b *streamBuilder) variableOp(el *xdm.Node, f vfocus) bool {
	for a := range el.Attrs() {
		if n := a.Name(); n.URI == "" && n.Local != "name" && n.Local != "select" && n.Local != "as" {
			return b.refuse("attribute " + n.Local + " of xsl:variable")
		}
	}
	op := &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key}
	if op.doc && !b.grp {
		b.x.noteWholeVar(op) // streamexec_whole.go
	}
	*b.ops = append(*b.ops, op)
	b.x.exprOps = append(b.x.exprOps, op)
	b.kinds[streamtrace.KindSelect]++
	return true
}

// motionlessLoop checks el, an xsl:for-each the analysis found motionless:
// its select reads only what the focus's view has. Over a variable's items
// (a select reading no focus and no document) the body's focus is never a
// node of the stream, and the body runs on it as the tree runs it; over
// nodes of the view (ancestor::*, @*), the body must read of each only
// what a view has.
func (b *streamBuilder) motionlessLoop(el *xdm.Node, f vfocus) bool {
	sel := el.Attr("", "select")
	if sel == nil {
		return b.refuse("motionless xsl:for-each")
	}
	for a := range el.Attrs() {
		if n := a.Name(); n.URI == "" && !streamAttrsOK[n.Local] {
			return b.refuse("attribute " + n.Local + " of xsl:for-each")
		}
	}
	for _, ch := range el.ChildElements() {
		if isXSL(ch, "sort") && !b.foreignSort(el, ch, f) { // streamsplit_l6.go
			return b.refuse("motionless xsl:for-each with xsl:sort")
		}
	}
	e := parseAt(el, sel.Value())
	if e == nil {
		return b.refuse("motionless xsl:for-each")
	}
	if foreign(e) {
		return true
	}
	if !(viewCheck{up: b.up}).safe(e, f, true) {
		return b.refuse("motionless xsl:for-each over " + sel.Value())
	}
	if b.leafBody(el, el.ChildElements()) { // streamsplit_l6.go
		return true
	}
	return b.loopContent(el) // streamlast_mm2.go
}
