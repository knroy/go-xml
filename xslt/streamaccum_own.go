package xslt

// Lane MM8 (from MM3's evaluate-046 work, fs-notes/mm3/logs/eval046): what
// an accumulator rule reads of itself and of its element.

import (
	"strings"

	"github.com/knroy/go-xml/v2/xpath"
)

// accumOwnValue reports whether e is accumulator-before of def itself, by a
// literal name (evaluate-046's with-params): a value def's rules computed,
// each of which accumStreamWhyIn checks holds no node of the parse.
func accumOwnValue(e xpath.Expr, def *accumulatorDef) bool {
	x, ok := e.(*xpath.FuncCall)
	if !ok || x.Name.URI != fnNS || x.Name.Local != "accumulator-before" || len(x.Args) != 1 {
		return false
	}
	lit, ok := x.Args[0].(*xpath.Literal)
	if !ok || lit.Val == nil || def.el == nil {
		return false
	}
	n, err := resolveQNameAttr(def.el, strings.TrimSpace(lit.Val.String()))
	return err == nil && n == def.name
}

// accumResolveQNameOK reports whether x is resolve-QName(s, .) in an
// accumulator rule (vc.accum) at an element (evaluate-046's
// resolve-QName(@name, .)): the observer runs the rule on the parse's own
// element, open with its ancestors, so its in-scope namespaces are the
// tree's; the result is a QName, which holds no node.
func accumResolveQNameOK(vc viewCheck, x *xpath.FuncCall, f vfocus) bool {
	return vc.accum && f == vElem && x.Name.URI == fnNS && x.Name.Local == "resolve-QName" &&
		len(x.Args) == 2 && isContextItem(x.Args[1])
}
