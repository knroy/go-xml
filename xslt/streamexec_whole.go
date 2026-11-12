package xslt

// Whole-document copies (lane MM6): a select that needs a grounded copy of
// the whole document element before it can make anything streams holding
// that one copy, which costs what the tree costs and saves nothing.

import (
	"slices"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// wholeMatch reports whether a rule body's select whose split grounds the
// rule's match itself (f(snapshot(.)), sf-unparsed-entity-03) may run when
// the match may be the document element (minD 1): the memory rule refuses
// it there, but the copy is of one element, the document element at most
// once, holding its records as the tree does. Deeper matches the memory
// rule admits already; a group's or a choice's split stays refused.
func wholeMatch(x *streamExec, s *streamSplit, minD int) bool {
	return x.rule && minD == 1 && s.ctxItem && s.class() == "subtree" && s.cond == nil && !s.group
}

// noteWholeVar records el, a local xsl:variable read at the document node,
// in x.whole when its select is copy-of or snapshot of ".", "/" or "/E" (E
// a child step without predicates) and nothing in its scope binds its name
// again (wholeAt reads it only in that scope): the split is docCopySplit's or wholeDocSplit's, so the variable
// holds a document built off the cursor, or a copy made of one, where the
// tree holds its own copy. Either is a tree apart from the stream, read
// whole (wholeRead).
func (x *streamExec) noteWholeVar(op *streamOp) {
	el := op.el
	q, err := resolveQNameAttr(el, el.AttrValue("name"))
	sel := el.AttrValue("select")
	if err != nil || sel == "" {
		return
	}
	f, ok := parseAt(el, sel).(*xpath.FuncCall)
	if !ok || f.Name.URI != fnNS || f.Name.Local != "copy-of" && f.Name.Local != "snapshot" || len(f.Args) != 1 {
		return
	}
	switch a := f.Args[0].(type) {
	case *xpath.ContextItem:
	case *xpath.PathExpr:
		if _, one := oneChildStep(a); !one && !(a.Root && len(a.Steps) == 0) {
			return
		}
	default:
		return
	}
	others := false
	for _, sib := range followingElements(el) {
		walkElements(sib, func(n *xdm.Node) bool {
			if isXSL(n, "variable") || isXSL(n, "param") {
				if nq, err := resolveQNameAttr(n, n.AttrValue("name")); err != nil || nq == q {
					others = true
				}
			}
			return !others
		})
	}
	if others {
		return
	}
	if x.whole == nil {
		x.whole = map[xdm.QName]*streamOp{}
	}
	x.whole[q] = op
}

// followingElements are el's following sibling elements: its scope.
func followingElements(el *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	seen := false
	for _, ch := range el.Parent().ChildElements() {
		if seen {
			out = append(out, ch)
		}
		seen = seen || ch == el
	}
	return out
}

// wholeAt is x.whole narrowed to the variables in scope at el: el is, or
// is inside, a following sibling of the variable's element.
func (x *streamExec) wholeAt(el *xdm.Node) map[xdm.QName]*streamOp {
	var in map[xdm.QName]*streamOp
	for q, op := range x.whole {
		for a := el; a != nil && a.Kind() == xdm.KindElement; a = a.Parent() {
			if a.Parent() == op.el.Parent() && a != op.el && slices.Contains(followingElements(op.el), a) {
				if in == nil {
					in = map[xdm.QName]*streamOp{}
				}
				in[q] = op
				break
			}
		}
	}
	return in
}

// readyWhole checks that each variable of x.whole is read as noteWholeVar
// says: its split builds the document (docCopy).
func (x *streamExec) readyWhole() string {
	for _, op := range x.whole {
		if op.split == nil || !op.split.docCopy || op.tee != nil {
			return "a variable of the whole document not built off the cursor"
		}
	}
	return ""
}

// wholeRead reports whether p, a path from a variable of whole, reads
// nothing but that variable's tree: no other variable (nor one of whole
// rebound in p), no current(), and no function burstFnBanned names (an
// accumulator's value or a generated id is not the copy's to give).
func wholeRead(p *xpath.PathExpr, whole map[xdm.QName]*streamOp) bool {
	ok := true
	walkExprNodes(p, func(v any) {
		switch x := v.(type) {
		case *xpath.VarRef:
			ok = ok && whole[x.Name] != nil
		case *xpath.FuncCall:
			ok = ok && !(x.Name.URI == fnNS && (burstFnBanned[x.Name.Local] || x.Name.Local == "current")) && !stylesheetFn(x.Name)
		case *xpath.ForExpr, *xpath.LetExpr, *xpath.QuantifiedExpr, *xpath.InlineFunctionExpr, *xpath.DynamicCall:
			ok = false
		}
	})
	return ok
}
