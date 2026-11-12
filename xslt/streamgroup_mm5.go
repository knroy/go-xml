package xslt

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// mapOfAtomics reports whether the select of el, an xsl:sequence, is a map
// constructor whose values are atomic (si-fork-814: map{'country':
// current-grouping-key(), 'sum-pop': sum(current-group()/@pop), 'cities':
// current-group()/@name/string(), $c: 'uk'}). Its keys are atomized
// (MapConstructor.Eval), so the map holds no node: in a body over a pruned
// population it hands no member on, as xsl:map with xsl:map-entry does not.
func mapOfAtomics(el *xdm.Node) bool {
	m, ok := parseAt(el, el.AttrValue("select")).(*xpath.MapConstructor)
	if !ok || el.FirstChild() != nil {
		return false
	}
	for _, v := range m.Values {
		if !atomicItems(v) {
			return false
		}
	}
	return true
}

// atomicItems reports whether e's items are atomic values: atomicValued, a
// grouping key (atomized: §14.3), or a path whose last step is such a call
// (current-group()/@name/string()).
func atomicItems(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.FuncCall:
		if x.Name.URI == fnNS && x.Name.Local == "current-grouping-key" && len(x.Args) == 0 {
			return true
		}
	case *xpath.PathExpr:
		return len(x.Steps) > 0 && atomicValued(x.Steps[len(x.Steps)-1])
	}
	return atomicValued(e)
}
