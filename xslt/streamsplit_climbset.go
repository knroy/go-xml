package xslt

// intersect and except of two climbing paths (lane L5):
// A/T intersect B/U, A/T except B/U, T and U climbing (ancestor::*,
// ancestor-or-self::*/@a), as sx-intersect-331..335 and sx-except-331..335.
//
// A and B are the two branches of one P (streamsplit_union.go), every end of
// either a match. Each branch keeps its own tail: a match evaluates the tail
// of each branch it ends on its view, and each node that gives is recorded
// with that branch's bit, once per branch -- the tail climbs in order
// (inOrderClimb), so across a branch's matches a node not after the last one
// it took is one taken already, as climbing drops it for one path. A node
// reached by both branches has both bits: its views are one node to
// Identity. At the context's end the nodes are sorted in document order and
// the operator keeps those whose bits it holds -- the tree's intersect or
// except of the two sorted, de-duplicated operands.
//
// Memory: what is held is each climbed node once, with a mask; that is what
// A/T union B/U collects for its result (which streams: the climbed nodes are
// the result's own, class "output"), so never more than the tree's nodes
// (each a node of the document the tree holds, the tree holding the rest
// too). A held split (s.hold) accounts for each node as it is recorded.

import (
	"slices"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// splitClimbSet is what a set operator of climbing paths keeps on its
// branches: the tail of each, and the operator over their bits.
type splitClimbSet struct {
	tails []xpath.Expr
	set   *splitSet
}

// climbSetPlan is splitClimbSet compiled, on a streamSplit.
type climbSetPlan struct {
	tails []*xpath.Compiled
	set   *splitSet
}

// climbSetOf takes l op r, op intersect or except, each a plain consuming
// path with a tail that climbs in order and cannot fail, as two branches
// with their tails (setOf's case); done says x is one.
func climbSetOf(op string, l, r splitPathParts) (pp splitPathParts, done bool) {
	if op != "intersect" && op != "except" {
		return pp, false
	}
	var tails []xpath.Expr
	for _, side := range []splitPathParts{l, r} {
		if side.br != nil || side.oi != "" || side.group || side.ctxItem || side.mapR != nil ||
			side.skip != 0 || len(side.tail) == 0 || len(side.steps) == 0 {
			return pp, false
		}
		for _, st := range side.tail {
			if !isStep(st) {
				return pp, false // a value step: XPTY0004 the operator raises
			}
		}
		var t xpath.Expr = &xpath.PathExpr{Steps: side.tail}
		if len(side.tail) == 1 {
			t = side.tail[0]
		}
		if !climbs(t) || !inOrderClimb(t) || !splitCannotFail(t) {
			return pp, false
		}
		tails = append(tails, t)
	}
	b := &splitBranches{n: 2, climb: &splitClimbSet{tails: tails,
		set: &splitSet{op: op, l: &splitSet{lo: 0, hi: 1}, r: &splitSet{lo: 1, hi: 2}}}}
	for range l.steps {
		b.bits = append(b.bits, 0)
	}
	for range r.steps {
		b.bits = append(b.bits, 1)
	}
	pp.steps = append(l.steps[:len(l.steps):len(l.steps)], r.steps...)
	pp.br = b
	return pp, true
}

// climbBranched reports whether p is a set operator of climbing paths.
func climbBranched(p splitPathParts) bool { return p.br != nil && p.br.climb != nil }

// climbSetPath finishes path for P with climbing branches, s.steps built:
// each match is read on its view (matchView), its branches' tails taken by
// climbSetTake.
func (s *streamSplit) climbSetPath(w *splitter, p splitPathParts, part func(xpath.Expr) *xpath.Compiled) string {
	if len(p.tail) > 0 || p.mapR != nil {
		return "a path after a set operator of climbing paths"
	}
	if leaf, _ := branchKinds(p); leaf {
		return "text, comment or PI matches of a set operator of climbing paths"
	}
	plan := &climbSetPlan{set: p.br.climb.set}
	for _, t := range p.br.climb.tails {
		if !(viewCheck{}).safe(t, vElem, true) {
			return "tail " + t.String() + ": climbs to ancestors' nodes"
		}
		plan.tails = append(plan.tails, part(t))
		if !endsInLeafNodes(t) {
			s.climbElems = true
		}
	}
	// s.tail stands for the tails where only its presence is read (not a
	// bare path, a tail per match); tailOf hands the views to climbSetTake.
	s.climbSet, s.climb, s.mode, s.tail = plan, true, matchView, plan.tails[0]
	s.atomize = w.use == useAtomize || splitFolds[s.red]
	if why := s.branchChecks(); why != "" {
		return why
	}
	s.accepts()
	s.folds(w, part)
	return ""
}

// climbSetRun is what a run of such a split has recorded: per branch the
// last node its tail took, and each node taken with the branches it was
// taken for.
type climbSetRun struct {
	last  []*xdm.Node
	at    map[xdm.IdentityKey]int
	nodes []*xdm.Node
	masks []uint64
}

// climbSetTake records what the tails of the branches the match v ends
// (r.hit) give.
func (r *splitRun) climbSetTake(v *xdm.Node) error {
	plan := r.s.climbSet
	cs := r.cset
	if cs == nil {
		cs = &climbSetRun{last: make([]*xdm.Node, len(plan.tails)), at: map[xdm.IdentityKey]int{}}
		r.cset = cs
	}
	ctx := r.ctx.WithFocus(v, 1, 1)
	for k, t := range plan.tails {
		if r.hit&(1<<k) == 0 {
			continue
		}
		seq, err := t.Eval(ctx)
		if err != nil {
			return err
		}
		for _, it := range seq {
			n, ok := it.(*xdm.Node)
			if !ok {
				return errSplitGiveUp
			}
			if cs.last[k] != nil && n.Compare(cs.last[k]) <= 0 {
				continue
			}
			cs.last[k] = n
			id := n.Identity()
			if i, ok := cs.at[id]; ok {
				cs.masks[i] |= 1 << k
				continue
			}
			if r.s.hold {
				if err := r.holdOn(xdm.One(n)); err != nil {
					return err
				}
			}
			cs.at[id] = len(cs.nodes)
			cs.nodes, cs.masks = append(cs.nodes, n), append(cs.masks, 1<<k)
		}
	}
	return nil
}

// climbSetFinish takes the nodes the operator keeps, in document order.
func (r *splitRun) climbSetFinish() error {
	cs := r.cset
	if cs == nil {
		return nil
	}
	r.cset = nil
	idx := make([]int, len(cs.nodes))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(a, b int) int { return cs.nodes[a].Compare(cs.nodes[b]) })
	var out xdm.Sequence
	for _, i := range idx {
		if r.s.climbSet.set.holds(cs.masks[i]) {
			out = append(out, cs.nodes[i])
		}
	}
	if len(out) == 0 {
		return nil
	}
	return r.take(out)
}
