package xslt

// Lane MM7: a call on an instruction-body function given a match itself
// (function-5012: the tail sf:absorbing(.)[self::new]/p, the body
// <new><xsl:copy-of select="$node/section/p[2]"/></new>).
//
// A burst call (streamsplit_calls.go) is one whose one-xsl:sequence body
// reads of the nodes it is given nothing a grounded copy lacks (burstSafe).
// A body of instructions is the same when it has no focus of its own --
// no instruction that sets one (xsl:for-each and the like), xsl:number,
// templates -- so that every expression in it reads only through the
// parameters, and every such expression is burstSafe and goes only down
// (no step climbing or beside the node given, whose grounded copy has its
// ancestors as names and attributes only). Then on a match's grounded copy
// the call returns what the tree's returns on the match.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// instrBurstNames are the instructions such a body may hold: none sets a
// focus, none reads a node but by its expressions.
var instrBurstNames = map[string]bool{
	"param": true, "variable": true, "sequence": true, "copy-of": true, "value-of": true,
	"if": true, "choose": true, "when": true, "otherwise": true, "element": true,
	"attribute": true, "text": true, "comment": true, "processing-instruction": true,
}

// instrBurstBody reports whether fn, an xsl:function whose body is not one
// xsl:sequence (copiesOnlyBody already holds), reads of its arguments only
// what a grounded copy has.
func instrBurstBody(fn *xdm.Node) bool {
	ok := true
	check := func(el *xdm.Node, src string) {
		e := parseAt(el, src)
		ok = ok && e != nil && burstSafe(e, false) && downOnly(e)
	}
	walkElements(fn, func(el *xdm.Node) bool {
		if el == fn {
			return true
		}
		xsl := el.Name().URI == xdm.NSXSL
		if xsl && !instrBurstNames[el.Name().Local] {
			ok = false
			return false
		}
		for a := range el.Attrs() {
			n, v := a.Name(), a.Value()
			switch {
			case n.URI != "":
			case xsl && (n.Local == "select" || n.Local == "test"):
				check(el, v)
			case !xsl || n.Local == "name" && !isXSL(el, "param") && !isXSL(el, "variable") || n.Local == "namespace":
				srcs, isAVT := avtExpressions(v)
				if !isAVT {
					ok = false
				}
				for _, s := range srcs {
					check(el, s)
				}
			}
		}
		return ok
	})
	return ok
}

// downOnly reports whether every step in e goes down from its context or
// stays on it, and no path starts at the root.
func downOnly(e xpath.Expr) bool {
	ok := true
	walkExprNodes(e, func(v any) {
		switch x := v.(type) {
		case *xpath.PathExpr:
			ok = ok && !x.Root
		case *xpath.Step:
			switch x.Axis {
			case xpath.AxisChild, xpath.AxisDescendant, xpath.AxisDescendantOrSelf, xpath.AxisSelf,
				xpath.AxisAttribute, xpath.AxisNamespace:
			default:
				ok = false
			}
		}
	})
	return ok
}
