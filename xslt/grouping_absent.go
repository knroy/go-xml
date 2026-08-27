package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// groupingScopeVar marks that a grouping is in scope, so that
// current-group() can tell "the group is empty" from "there is no group".
//
// A separate binding is needed because the group itself is bound to the empty
// sequence in both cases: xsl:for-each-group over an empty population gives
// an empty current group, and clearFunctionContext unbinds the group by
// binding it to nil. LookupVar cannot distinguish the two, and section 14.4
// gives them different answers -- the empty sequence for the first, XTDE1061
// for the second.
var groupingScopeVar = xdm.QName{URI: internalNS, Local: "grouping-in-scope"}

// withGroupingScope binds one group and marks the scope it establishes.
func (rt *runtime) withGroupingScope(items, key xdm.Sequence) *runtime {
	sub := rt.withVar(currentGroupVar, items)
	sub = sub.withVar(currentGroupingKeyVar, key)
	return sub.withVar(groupingScopeVar, xdm.One(xdm.NewBoolean(true)))
}

// withoutGroupingScope removes the grouping from the context, as section
// 5.4's table says an invocation construct does.
func (rt *runtime) withoutGroupingScope() *runtime {
	sub := rt.withVar(currentGroupVar, nil)
	sub = sub.withVar(currentGroupingKeyVar, nil)
	return sub.withVar(groupingScopeVar, nil)
}

// groupingInScope reports whether a grouping is in scope at this point.
func groupingInScope(ctx *xpath.Context) bool {
	seq, _ := ctx.LookupVar(groupingScopeVar)
	return len(seq) > 0
}

// errNoGrouping is XTDE1061 / XTDE1071.
//
// XSLT 2.0 answered the empty sequence outside a grouping, and section 14.4
// of 3.0 made it an error instead; the version the expression was compiled
// under is what decides, so a 2.0 stylesheet keeps the answer it had.
func errNoGrouping(ctx *xpath.Context, code, fn string) error {
	if !ctx.Version.AtLeast31() {
		return nil
	}
	return fmt.Errorf("%s: %s() called where there is no current group", code, fn)
}
