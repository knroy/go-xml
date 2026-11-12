package xslt

// What a view answers beyond the split's own checks (lane K2, bucket MV).

import "github.com/knroy/go-xml/v2/xpath"

// viewIdentOK reports whether x is generate-id() or generate-id(.) of an
// element's view in an instruction's motionless part (pos: its focus is the
// instruction's, not a candidate of P's). A view carries its original's
// origin (xdm streamview.go), and generate-id reads that: the stream's id
// and the ordinal a tree-mode parse numbers the element with (DESIGN
// section 1.4). Not under noIdent: a copied leaf stands in for another node.
func viewIdentOK(vc viewCheck, x *xpath.FuncCall, f vfocus) bool {
	if x.Name.URI != fnNS || x.Name.Local != "generate-id" || !vc.pos || vc.noIdent || vc.items || f != vElem {
		return false
	}
	return len(x.Args) == 0 || len(x.Args) == 1 && isContextItem(x.Args[0])
}

// patternAccOK says whether a pattern's predicate on step i of n may call
// accumulator-before: on the last step only, whose node is an element
// matched at its start tag, where the run's observer has the accumulator's
// value before the rule is chosen (as for a rule body's motionless parts).
func patternAccOK(i, n int, elem, leaf, attr bool) bool {
	return i == n-1 && elem && !leaf && !attr
}
