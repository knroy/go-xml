package xslt

// Populations of two more shapes (lane L6).
//
// A choice whose branches both read the stream, if ($c) then P else Q
// (sx-if-115, -118): each branch has its split, built by a builder of its
// own; the condition is motionless and is evaluated first, as the tree
// evaluates the select, and the run reads the chosen branch's split
// (streamRun.popSplit). Both splits are readied alike (readyAlt).
//
// A union of a stream path and a fresh operand, F union P (sx-union-115,
// -118): F's nodes are in trees other than the document's, so in the
// tree's union each lies before or after all of the document's nodes. A
// view, a leaf's copy and a grounded copy order as their originals do
// (xdm streamview.go: the tree of a copy out of a stream orders among the
// others where the parse tree would), so F's nodes are handed, in order,
// those before the stream's nodes when the first match arrives, the others
// after the last. F union () is evaluated first: it sorts F and raises the
// operator's type error for a value, as the tree's union does.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// splitOf is the split the population of op reads in this run.
func (run *streamRun) splitOf(op *streamOp) *streamSplit {
	if run.popSplit != nil {
		return run.popSplit
	}
	return op.split
}

// choiceOfStreams builds if (c) then P else Q, both reading the stream, as
// a choice of two plain populations with a split each; nil when it is not
// one (pop then takes the shapes it took).
func (pb *popBuilder) choiceOfStreams(x *xpath.IfExpr) *streamPop {
	if pb.split != nil {
		return nil
	}
	tb, eb := *pb, *pb
	t, e := tb.pop(x.Then), eb.pop(x.Else)
	if t == nil || e == nil || tb.split == nil || eb.split == nil || !plainPop(t) || !plainPop(e) {
		return nil
	}
	t.split, e.split = tb.split, eb.split
	pb.split = tb.split
	return &streamPop{cond: pb.part(x.Cond), then: t, els: e}
}

// plainPop reports whether p hands the matches themselves, with the views
// the loop's mode decides: no choice, function, copies, values or union.
func plainPop(p *streamPop) bool {
	return p.cond == nil && !p.values && !p.off && !p.ground && p.fn == nil && p.union == nil
}

// altSplit is the split of the else branch of op's choice, when it is not
// op's own.
func altSplit(op *streamOp) *streamSplit {
	if p := op.pop; p != nil && p.cond != nil && p.els != nil && p.els.split != nil && p.els.split != op.split {
		return p.els.split
	}
	return nil
}

// readyAlt readies the else branch's split of a loop op as op.split was
// (readyOps): the same mode, under the same conditions; ctxD is the
// context's depth, k the first branch's least match depth.
func (op *streamOp) readyAlt(ctxD, k int) string {
	s2 := altSplit(op)
	if s2 == nil {
		return ""
	}
	k2 := ctxD + s2.minDepth()
	switch op.split.mode {
	case matchView:
	case matchBody:
		if s2.nests && (!s2.outermost || s2.predFail) {
			return "loop: the other branch's matches may lie inside each other"
		}
		if k2 < k {
			return "loop: the other branch's matches may lie higher"
		}
	case matchGround:
		if k2 < 1 {
			return "loop body: neither streamed nor grounded (a match may be the document node)"
		}
	default:
		return "loop: a choice of two streamed populations whose first is read as a select's"
	}
	s2.mode = op.split.mode
	return ""
}

// readyAltApply readies the else branch's split of an apply op as
// readyApply readies op.split.
func (op *streamOp) readyAltApply() string {
	s2 := altSplit(op)
	if s2 == nil {
		return ""
	}
	if s2.nests && (!s2.outermost || s2.predFail) {
		return "apply-templates select: matches may lie inside each other"
	}
	if why := leafApplyRefusal(s2); why != "" {
		return why
	}
	s2.mode = matchBody
	return ""
}

// unionPop builds F union P (either order), F fresh (streamsplit_l6.go:
// focus-free, reading no document, holding no node of the stream) and P a
// plain stream part.
func (pb *popBuilder) unionPop(x *xpath.BinaryOp) *streamPop {
	if x.Op != "union" || pb.split != nil || pb.el == nil {
		return nil
	}
	f, s := x.Left, x.Right
	if !freshOperand(pb.el, f) {
		f, s = s, f
	}
	if !freshOperand(pb.el, f) || freshOperand(pb.el, s) {
		return nil
	}
	p := &streamPop{at: 0, parts: []*xpath.Compiled{nil}}
	if !pb.stream(p, s) || !plainPop(p) || len(p.wins) != 0 || p.oneOrMore != nil {
		return nil
	}
	p.union = pb.part(&xpath.BinaryOp{Op: "union", Left: f, Right: &xpath.SequenceExpr{}})
	return p
}

// freshOperand reports whether e is an operand whose nodes are fresh.
func freshOperand(el *xdm.Node, e xpath.Expr) bool {
	return freeOfFocus(e) && !readsDocs(e) && fresh(el, e)
}

// unionMerger hands F's nodes around the stream's: before at the first
// match, rest after the last.
type unionMerger struct {
	run   *streamRun
	each  func(xdm.Item) error
	nodes xdm.Sequence // F's, sorted, not yet handed
	met   bool         // a match of the stream has been met
}

// unionMerge evaluates p's union operand, if any (nil merger otherwise).
func (p *streamPop) unionMerge(run *streamRun, rt *runtime, each func(xdm.Item) error) (*unionMerger, error) {
	if p.union == nil {
		return nil, nil
	}
	seq, err := p.union.Eval(rt.ctx)
	if err != nil {
		return nil, err
	}
	return &unionMerger{run: run, each: each, nodes: seq}, nil
}

// before hands, at the first match n, F's nodes that lie before it.
func (m *unionMerger) before(it xdm.Item) error {
	if m == nil || m.met {
		return nil
	}
	n, ok := it.(*xdm.Node)
	if !ok {
		return nil
	}
	m.met = true
	k := 0
	for k < len(m.nodes) {
		f, ok := m.nodes[k].(*xdm.Node)
		if !ok || f.Compare(n) > 0 {
			break
		}
		if err := m.each(f); err != nil {
			return err
		}
		k++
	}
	m.nodes = m.nodes[k:]
	return nil
}

// atCursor is before for the match at hand on the cursor (a matchBody
// walk, which hands the gate no node): its view, or its leaf's copy.
func (m *unionMerger) atCursor() error {
	if m == nil || m.met {
		return nil
	}
	if v, err := m.run.c.View(); err == nil {
		return m.before(v)
	}
	if l, err := m.run.c.Leaf(); err == nil {
		return m.before(l)
	}
	return errStreamPlan
}

// rest hands F's nodes not yet handed, after the matches.
func (m *unionMerger) rest() error {
	if m == nil {
		return nil
	}
	for _, f := range m.nodes {
		if err := m.each(f); err != nil {
			return err
		}
	}
	m.nodes = nil
	return nil
}
