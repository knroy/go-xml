package xslt

// Lane MM7: a call on an instruction-body function given a match itself
// (function-5012: the tail sf:absorbing(.)[self::new]/p, the body
// <new><xsl:copy-of select="$node/section/p[2]"/></new>).
//
// A burst call (streamsplit_calls.go) is one whose one-xsl:sequence body
// reads of the nodes it is given nothing a grounded copy lacks (burstSafe).
// A body of instructions is the same when its instructions read nodes only
// by their expressions (no xsl:number, templates, grouping or sorting), the
// one focus they set being xsl:for-each's, and every expression is
// burstSafe and goes only down (no step climbing or beside the node given,
// whose grounded copy has its ancestors as names and attributes only): so
// every node the body reads, and every focus, lies within the parameters'
// subtrees. Then on a match's grounded copy the call returns what the
// tree's returns on the match.

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
	// Its focus is what its select gives, downward from a parameter's
	// node like every expression here (function-5006).
	"for-each": true,
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

// oneMatchTail takes outermost(P/T) or innermost(P/T), where P has one
// match at most -- one child step to an element from the document node,
// which has one element child -- as P/outermost(T): over one match's T the
// two are the same nodes in the same order (function-5006:
// outermost(doc/sf:deep-descent(section[1]))). T then runs as any tail, on
// the match (the document element) as the split decides.
func (w *splitter) oneMatchTail(x *xpath.FuncCall, inner *splitPathParts) bool {
	if !w.doc || inner.oi != "" || inner.ctxItem || inner.group || inner.br != nil || inner.mapR != nil ||
		inner.skip > 0 || len(inner.steps) != 1 || len(inner.tail) == 0 {
		return false
	}
	st := inner.steps[0]
	if st.Axis != xpath.AxisChild {
		return false
	}
	switch t := st.Test.(type) {
	case *xpath.NameTest:
	case *xpath.KindTest:
		if t.Any || t.Kind != xdm.KindElement {
			return false // node(): a text or comment child matches too
		}
	default:
		return false
	}
	var arg xpath.Expr = &xpath.PathExpr{Steps: inner.tail}
	if len(inner.tail) == 1 {
		arg = inner.tail[0]
	}
	inner.tail = []xpath.Expr{&xpath.FuncCall{Name: x.Name, Args: []xpath.Expr{arg}}}
	return true
}

// groundTailPop reports whether the loop el's select, split as s, is a
// path whose tail runs on each match's grounded copy (matchGround), its
// items what the tails give, and el's body reads of them only what such a
// copy has (burstBody): the items are handed one by one off the cursor
// (pop.off), as the tree's loop runs over the select's value. The split
// grounds one match at a time; a match that is the document element is
// copied whole, which costs what its tree costs (function-5006).
func groundTailPop(s *streamSplit, el *xdm.Node) bool {
	return isXSL(el, "for-each") && s.cond == nil && s.mode == matchGround && s.tail != nil && s.tailText == "" &&
		s.red == "" && !s.nests && !s.group && !s.ctxItem && !s.hasBranches() && s.outer != nil &&
		isVarRef(s.outer.Expr(), splitItems) && burstBody(el)
}
