package xslt

// Lane MM8: a body of copies may return arrays and maps of atomic values
// (si-fork-815: array { current-group()!@value!xs:decimal(.) }).
//
// copyFreeBody refuses an xsl:sequence that could hand a member on as the
// node itself, where the tree's would be the original: one whose value is
// atomic (leafAtomic) cannot. Nor can an array or a map whose members are
// atomic, nor a simple map whose right operand is: none holds a node.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// nodeFreeSelect reports whether el's select holds no node: arrays, maps
// and simple maps of atomic values (atomicValued), as leafAtomic reads it.
func nodeFreeSelect(el *xdm.Node, tabs *moduleTabs) bool {
	e := parseAt(el, el.AttrValue("select"))
	if e == nil {
		return false
	}
	markSheetCalls(e, tabs.of(documentElementOf(el)).funcs()) // streamsplit_calls.go
	return nodeFree(e)
}

func nodeFree(e xpath.Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *xpath.ArrayConstructor:
		for _, m := range x.Members {
			if !nodeFree(m) {
				return false
			}
		}
		return true
	case *xpath.MapConstructor:
		for _, v := range x.Values {
			if !nodeFree(v) {
				return false
			}
		}
		for _, k := range x.Keys {
			if !nodeFree(k) {
				return false
			}
		}
		return true
	case *xpath.SimpleMap:
		return nodeFree(x.Right)
	case *xpath.FuncCall:
		if x.Name.URI == fnNS && x.Name.Local == "current-grouping-key" && len(x.Args) == 0 {
			return true // atomic values (§14.2.2)
		}
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !nodeFree(it) {
				return false
			}
		}
		return true
	}
	return atomicValued(e)
}
