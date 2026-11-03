package xslt

import (
	"slices"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// Kept leaves and climbing (full-streaming gap lane G4).
//
// A text, comment or processing-instruction match of a split's P that is
// kept as a node (keepLeaves) is a copy the cursor makes (Cursor.Leaf): the
// original to order, identity and generate-id, complete, under the copies a
// view has of its ancestors. A loop's body runs on it off the cursor, as on
// a grounded copy.

// offEach hands it, a match read whole, to each with no instruction
// intercepted.
func (run *streamRun) offEach(each func(xdm.Item) error, it xdm.Item) error {
	return run.offCursor(func() error { return each(it) })
}

// leafApplyRefusal says why an apply-templates select, split as s, cannot
// stream: its matches are dispatched as elements at their start tags.
func leafApplyRefusal(s *streamSplit) string {
	if s.keepLeaves {
		return "apply-templates select: text, comment or PI matches"
	}
	return ""
}

// climbing readies s, whose tail climbs, or says why not. The matches'
// tails give nodes of their views: the match's ancestors, attributes,
// namespaces. Across matches in document order such a node that is not
// after the last one taken is one taken already (a node of a later match's
// tail that is no earlier match's is an ancestor-or-self of the later match
// only, so it starts after the earlier match), so the tree's union in
// document order is kept by dropping those.
func (s *streamSplit) climbing(tail xpath.Expr, part func(xpath.Expr) *xpath.Compiled) string {
	if s.innermost {
		return "climbs to ancestors' nodes under innermost()"
	}
	if !(viewCheck{}).safe(tail, vElem, true) {
		return "climbs to ancestors' nodes"
	}
	s.climb, s.climbSort = true, !inOrderClimb(tail)
	if pe, ok := tail.(*xpath.PathExpr); ok && len(pe.Steps) > 1 && !isStep(pe.Steps[len(pe.Steps)-1]) {
		n := len(pe.Steps) - 1
		var nodes xpath.Expr = pe.Steps[0]
		if n > 1 {
			nodes = &xpath.PathExpr{Steps: pe.Steps[:n]}
		}
		s.climbNodes, s.climbEach = part(nodes), part(pe.Steps[n])
		return ""
	}
	s.climbElems = !endsInLeafNodes(tail)
	return ""
}

// tailOf is the tail evaluated on v, a match's view: for a climbing tail,
// only the nodes no match before gave, and what each gives.
func (r *splitRun) tailOf(v *xdm.Node) (xdm.Sequence, error) {
	s := r.s
	ctx := r.ctx.WithFocus(v, 1, 1)
	if !s.climb {
		return s.tail.Eval(ctx)
	}
	e := s.tail
	if s.climbNodes != nil {
		e = s.climbNodes
	}
	nodes, err := e.Eval(ctx)
	if err != nil {
		return nil, err
	}
	var out xdm.Sequence
	for _, it := range nodes {
		n, ok := it.(*xdm.Node)
		if !ok {
			return nil, errSplitGiveUp // not a path of nodes after all
		}
		if s.climbSort {
			r.held = append(r.held, n)
			continue
		}
		if r.climbed != nil && n.Compare(r.climbed) <= 0 {
			continue
		}
		r.climbed = n
		if s.climbEach == nil {
			out = append(out, n)
			continue
		}
		vals, err := s.climbEach.Eval(r.ctx.WithFocus(n, 1, 1))
		if err != nil {
			return nil, err
		}
		out = append(out, vals...)
	}
	return out, nil
}

// splitPassFns return items of their first argument (insert-before: and of
// its third) as they are.
var splitPassFns = map[string]bool{"head": true, "tail": true, "subsequence": true, "remove": true,
	"insert-before": true, "unordered": true, "one-or-more": true, "exactly-one": true,
	"zero-or-one": true, "reverse": true}

// isSplitItems reports whether v is the split's $items or $value.
func isSplitItems(v *xpath.VarRef) bool { return v.Name == splitItems || v.Name == splitValue }

// firstVar is the variable a path starts at, if it does.
func firstVar(steps []xpath.Expr) (*xpath.VarRef, bool) {
	if len(steps) == 0 {
		return nil, false
	}
	v, ok := steps[0].(*xpath.VarRef)
	return v, ok
}

// inOrderClimb reports whether a climbing tail's nodes come, across matches
// in document order, in document order once the ones given already are
// dropped (climbing): an ancestor or ancestor-or-self step whose predicates
// read only the candidate, then perhaps an attribute or namespace step,
// then perhaps a step that makes values. A parent step is not: the parent
// of a later match may be an ancestor of an earlier one above its parent.
func inOrderClimb(tail xpath.Expr) bool {
	steps := []xpath.Expr{tail}
	if pe, ok := tail.(*xpath.PathExpr); ok {
		steps = pe.Steps
	}
	if n := len(steps); n > 1 && !isStep(steps[n-1]) {
		steps = steps[:n-1]
	}
	for i, x := range steps {
		st, ok := x.(*xpath.Step)
		if !ok {
			return false
		}
		switch {
		case i == 0 && (st.Axis == xpath.AxisAncestor || st.Axis == xpath.AxisAncestorOrSelf):
		case i == 1 && (st.Axis == xpath.AxisAttribute || st.Axis == xpath.AxisNamespace):
		default:
			return false
		}
		for _, p := range st.Predicates {
			if _, pos := positionAt(p); pos || !booleanShaped(p) {
				return false
			}
		}
	}
	return true
}

// climbFinish takes what a climbSort tail held: its nodes in document
// order, each once, and what each gives.
func (r *splitRun) climbFinish() error {
	s := r.s
	if !s.climbSort || len(r.held) == 0 {
		return nil
	}
	nodes := r.held
	r.held = nil
	slices.SortStableFunc(nodes, func(a, b *xdm.Node) int { return a.Compare(b) })
	nodes = slices.CompactFunc(nodes, func(a, b *xdm.Node) bool { return a.Is(b) })
	for _, n := range nodes {
		items := xdm.One(n)
		if s.climbEach != nil {
			var err error
			if items, err = s.climbEach.Eval(r.ctx.WithFocus(n, 1, 1)); err != nil {
				return err
			}
		}
		if err := r.take(items); err != nil {
			return err
		}
	}
	return nil
}

// mapClimb takes P ! R, R climbing from P's matches (ancestor::*, a name of
// one), as the consuming path P with R for its tail, evaluated on each
// match's view; it reports whether it did.
func (w *splitter) mapClimb(x *xpath.SimpleMap, put func(xpath.Expr), use splitUse) bool {
	if w.pe != nil || w.why != "" || !climbs(x.Right) {
		return false
	}
	pp, ok := w.pathOf(x.Left)
	if !ok || pp.ctxItem || pp.group || len(pp.tail) > 0 {
		w.why = ""
		return false
	}
	pp.tail = []xpath.Expr{x.Right}
	w.pe, w.put, w.use, w.path, w.mapped = x, put, use, pp, true
	return true
}

// itemsOut reports, conservatively, whether what e gives may hold the
// split's items.
func itemsOut(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Literal:
		return false
	case *xpath.Step:
		return x.Axis != xpath.AxisAttribute && x.Axis != xpath.AxisNamespace
	case *xpath.SimpleMap:
		return itemsOut(x.Right)
	case *xpath.PathExpr:
		return len(x.Steps) == 0 || itemsOut(x.Steps[len(x.Steps)-1])
	case *xpath.IfExpr:
		return itemsOut(x.Then) || itemsOut(x.Else)
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if itemsOut(it) {
				return true
			}
		}
		return false
	}
	return true
}

// endsInLeafNodes reports whether a path's last step gives attributes or
// namespace nodes, which a view has whole.
func endsInLeafNodes(e xpath.Expr) bool {
	if pe, ok := e.(*xpath.PathExpr); ok && len(pe.Steps) > 0 {
		e = pe.Steps[len(pe.Steps)-1]
	}
	st, ok := e.(*xpath.Step)
	return ok && (st.Axis == xpath.AxisAttribute || st.Axis == xpath.AxisNamespace)
}
