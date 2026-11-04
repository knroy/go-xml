package xslt

// Lane L6: shapes and populations the planner left on the tree.

import (
	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// leafLoopBody plans the body of el, an xsl:for-each whose select gives
// only attributes, text, comment or processing-instruction nodes, or values
// (leafItems), when every instruction of it reads of each item only what a
// complete parentless node has (focus vLeaf): its value, its name, a copy of
// it. Each item the loop is handed is such a node -- an attribute of its
// match's view, or a leaf's copy (Cursor.Leaf) -- and the body runs off the
// cursor (matchView, or a population's off), as a template rule on a leaf
// does (planRule). Planned at an element's view (members), data(.) and
// copy-of(.) are refused there; a body that climbs (`..`) is refused here
// and is left to members, as before.
//
// The check runs in a builder of its own on a plan of its own: nothing it
// meets is registered in b's, so a refusal leaves b as it was. A body in
// which anything would be registered (a consumer, a value template's op) is
// refused.
func (b *streamBuilder) leafLoopBody(el *xdm.Node, body []*xdm.Node) bool {
	if !isXSL(el, "for-each") || !leafItems(parseAt(el, el.AttrValue("select"))) {
		return false
	}
	var ops []*streamOp
	var kinds [streamtrace.NumKinds]int64
	sb := &streamBuilder{pl: &streamPlan{recs: map[any]*planRec{}}, x: newStreamExec(),
		ops: &ops, kinds: &kinds, noIdent: true, key: b.key}
	n := int64(0)
	for _, ch := range body {
		if fallbackIgnored(ch) {
			continue
		}
		if !sb.motionless(ch, vLeaf) {
			return false
		}
		n++
	}
	if len(ops) != 0 || len(sb.x.ops) != 0 || len(sb.x.exprOps) != 0 || len(sb.x.tries) != 0 {
		return false
	}
	b.kinds[streamtrace.KindSeq]++
	b.kinds[streamtrace.KindTree] += n
	return true
}

// leafItems reports whether e gives no element and no document node: a
// path whose last step selects attributes, or text, comment or PI nodes by
// kind, a value, or a sequence of those.
func leafItems(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !leafItems(it) {
				return false
			}
		}
		return len(x.Items) > 0
	case *xpath.PathExpr:
		return len(x.Steps) > 0 && leafStep(x.Steps[len(x.Steps)-1])
	case *xpath.Step:
		return leafStep(x)
	case nil:
		return false
	}
	return atomicValued(e)
}

func leafStep(e xpath.Expr) bool {
	st, ok := e.(*xpath.Step)
	if !ok {
		return false
	}
	if st.Axis == xpath.AxisAttribute {
		return true
	}
	leaf, elem := testKinds(st.Test)
	return leaf && !elem
}

// atomicSelect reports whether el, an xsl:sequence or xsl:variable, has a
// select that gives only values: it cannot hand a copied node on as the node
// it stands for (the noIdent refusal in motionless).
func atomicSelect(el *xdm.Node) bool {
	if el.Attr("", "select") == nil || el.Attr("", "as") != nil {
		return false
	}
	e := parseAt(el, el.AttrValue("select"))
	return e != nil && atomicValued(e)
}
