package xslt

// Accumulator values for the copies of grounded matches (lane L6).
//
// xsl:apply-templates over copy-of(P) or snapshot(P) (executeCopies)
// dispatches the copies once the population has been read; a rule reading
// accumulator-before on a node of a copy asks the copy's original
// (copy-accumulators: §18.2.10, accumulatorOrigin), a node of a grounded
// match, whose values the observer's frames no longer hold. While such a
// population is read, the observer keeps every node's values by ordinal
// (kept); a read finds them there. Memory: one value pair per accumulator
// for each node read in that span, which the tree's walk keeps for every node
// of the document (accumulatorValuesFor's tables) on top of the tree:
// never above the tree mode (accumulator-046s, -047s).

import "github.com/knroy/go-xml/v2/xdm"

// keptAccums are one node's values.
type keptAccums struct {
	before, after []xdm.Sequence
}

// keepObserved records, while keeping, the values of the element at hand:
// before at its start, after at its end.
func (sa *streamAccums) keepObserved(n *xdm.Node, d int, end bool, ord uint64) {
	if sa.keep == 0 || n == nil || n.Kind() != xdm.KindElement {
		return
	}
	f := &sa.frames[d]
	if sa.kept == nil {
		sa.kept = map[uint64]*keptAccums{}
	}
	if !end {
		sa.kept[f.ord] = &keptAccums{before: append([]xdm.Sequence(nil), f.before...)}
		return
	}
	if k := sa.kept[f.ord]; k != nil {
		k.after = append([]xdm.Sequence(nil), f.after...)
	}
}

// keptLeaf returns, while keeping, the slices a leaf's values go into.
func (sa *streamAccums) keptLeaf(ord uint64) (before, after []xdm.Sequence) {
	if sa.keep == 0 {
		return nil, nil
	}
	if sa.kept == nil {
		sa.kept = map[uint64]*keptAccums{}
	}
	k := &keptAccums{before: make([]xdm.Sequence, len(sa.defs)), after: make([]xdm.Sequence, len(sa.defs))}
	sa.kept[ord] = k
	return k.before, k.after
}

// keptValue is the kept value of accumulator i at node (post: after), when
// one is kept for it.
func (sa *streamAccums) keptValue(i int, node *xdm.Node, post bool) (xdm.Sequence, bool) {
	if sa.kept == nil || node == sa.root {
		return nil, false
	}
	ord, ok := sa.run.c.Ordinal(node)
	if !ok {
		return nil, false
	}
	k := sa.kept[ord]
	switch {
	case k == nil:
		return nil, false
	case post && k.after == nil:
		return nil, false // not closed yet: the frames answer
	case post:
		return k.after[i], true
	}
	return k.before[i], true
}

// runAccums is the observer of run, or nil.
func (rt *runtime) runAccums(run *streamRun) *streamAccums {
	for _, sa := range rt.streamAccums {
		if sa.run == run {
			return sa
		}
	}
	return nil
}
