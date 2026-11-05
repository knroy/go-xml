package xslt

// Lane MM7: copies of a streamed group held as a value (si-group-040).

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// readsGroup reports whether e calls fn:current-group(). In a streamed
// group's body that is the consuming path (the group's members), though no
// focus is read: copy-of(current-group()) is not a copy of a variable's
// value, which the view check takes it for.
func readsGroup(e xpath.Expr) bool {
	cg := false
	walkExprCalls(e, func(x *xpath.FuncCall) { cg = cg || isCurrentGroup(x) })
	return cg
}

// groupParam reports whether ch, an xsl:with-param in a streamed group's
// body, reads the current group: a consuming parameter, read off the
// cursor at the instruction as an expression op whose split takes the
// group's members (foreignParams), not a motionless value.
func (b *streamBuilder) groupParam(ch *xdm.Node) bool {
	return b.grp && ch.Attr("", "select") != nil && readsGroup(parseAt(ch, ch.AttrValue("select")))
}
