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
	*b.ops = append(*b.ops, op)
	b.x.exprOps = append(b.x.exprOps, op)
	b.kinds[streamtrace.KindSelect]++
	return true
}
