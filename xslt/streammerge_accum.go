package xslt

// Accumulators on streamed merge sources (lane L4; XSLT 3.0 §15.4, §18.2.2).
//
// A merge source's use-accumulators makes those accumulators applicable to
// the documents it reads, and the tree computes them over each document
// (streamable or not: the tree raises no XTDE3362 for a merge document). The
// keys and the action read them of the items' snapshots, which the tree
// pairs with the original nodes (noteCopy), so a snapshot answers with the
// value its original has in the document.
//
// The stream computes them with an observer on each document's cursor
// (observeAccumsOf, every applicable accumulator), and records, for the
// nodes at or below the select's least depth, the values after each step
// (mergeAccums.note): one list per element at that depth, in document
// order. When a match is delivered, grounded, its snapshot is paired with
// the list of its subtree as the tree pairs it with its original
// (attach), and the values are kept for the snapshot's nodes in a table
// fnAccumulator consults (transformState.accumHeld). Lists of elements no
// match came from are dropped as the next match is delivered: what is held
// is the gap between two matches, and the snapshots' values.
//
// Errors: the tree fails a read when the accumulator's walk fails anywhere.
// So a read of a failed accumulator, or a merge that read an accumulator
// whose walk failed later, fails the streamed merge, which then runs again
// on the tree (executeStream): the tree's answer. The check of the
// declarations (accumStreamWhy) is made at link time, once every
// accumulator has compiled (linkMergeAccums).

import (
	"fmt"
	"slices"

	"github.com/knroy/go-xml/v2/xdm"
)

// linkMergeAccums leaves on the tree each streamed merge with a source
// whose applicable accumulators the observer cannot compute.
func (c *compiler) linkMergeAccums() {
	for _, i := range c.mergeLinks {
		for _, src := range i.sources {
			if !src.streamed || src.sourceURIs == nil || src.accums == nil || len(c.sheet.accumulators) == 0 {
				continue
			}
			if why := c.sheet.mergeAccumRefusal(src.accums); why != "" {
				i.streams = false
				if traceCore != nil && src.split != nil {
					traceCore(nil, why)
				}
			}
		}
	}
	c.mergeLinks = nil
}

// mergeAccumRefusal is accumRefusal for a merge source's set: every
// applicable accumulator, declared streamable or not, is computed.
func (s *Stylesheet) mergeAccumRefusal(set *modeAccumulators) string {
	reads := map[*accumulatorDef][]string{}
	for _, def := range s.accumulators {
		if !accumApplies(set, def) {
			continue
		}
		why, deps := accumStreamWhy(def)
		if why != "" {
			return why
		}
		reads[def] = deps
	}
	return s.accumReadsWhy(reads)
}

// mergeAccums records the values of one merge document's nodes.
type mergeAccums struct {
	sa    *streamAccums
	minD  int
	lists []mergeAccumList
	open  []int // per depth below minD, the index in the last list of the open element
}

type mergeAccumList struct {
	top     uint64 // the ordinal of the element at depth minD
	entries []mergeAccumEntry
}

type mergeAccumEntry struct {
	ord               uint64 // 0 for a leaf
	before, after     []xdm.Sequence
	beforeAt, afterAt uint64 // the steps they were computed at
	closed            bool
}

// heldAccum is the values of a snapshot's node (accumHeld).
type heldAccum struct {
	sa                *streamAccums
	before, after     []xdm.Sequence
	beforeAt, afterAt uint64
}

// observeMergeAccums starts computing the accumulators of src's document on
// run's cursor, recording below depth minD, or returns nil when src
// declares none.
func observeMergeAccums(rt *runtime, run *streamRun, src *mergeSource, minD int) *mergeAccums {
	if src.accums == nil {
		return nil
	}
	sa := run.observeAccumsOf(rt, src.accums, false, true, 0)
	if sa == nil {
		return nil
	}
	m := &mergeAccums{sa: sa, minD: max(minD, 1)}
	sa.onStep = m.note
	return m
}

// note is the observer's onStep.
func (m *mergeAccums) note(n *xdm.Node, d int, post bool, ord uint64, cur []xdm.Sequence) {
	if n == nil || d < m.minD {
		return
	}
	isEl := n.Kind() == xdm.KindElement
	if d == m.minD && isEl && !post {
		m.lists = append(m.lists, mergeAccumList{top: ord})
		m.open = m.open[:0]
	}
	if len(m.lists) == 0 {
		return
	}
	l := &m.lists[len(m.lists)-1]
	vals, at := slices.Clone(cur), m.sa.rd.phase
	k := d - m.minD
	switch {
	case !post:
		e := mergeAccumEntry{before: vals, beforeAt: at}
		if isEl {
			e.ord = ord
			for len(m.open) <= k {
				m.open = append(m.open, -1)
			}
			m.open[k] = len(l.entries)
		}
		l.entries = append(l.entries, e)
	case isEl:
		if k < len(m.open) && m.open[k] >= 0 {
			e := &l.entries[m.open[k]]
			e.after, e.afterAt, e.closed = vals, at, true
		}
	default:
		e := &l.entries[len(l.entries)-1]
		e.after, e.afterAt, e.closed = vals, at, true
	}
}

// attach pairs snap, the snapshot of it (a match grounded off m's cursor),
// with the values recorded for it, and drops the lists before its own.
func (m *mergeAccums) attach(rt *runtime, it, snap xdm.Item) error {
	g, ok := it.(*xdm.Node)
	s, ok2 := snap.(*xdm.Node)
	if !ok || !ok2 {
		return nil
	}
	ord, ok := m.sa.run.c.Ordinal(g)
	if !ok {
		return errAccumOffStream
	}
	for li := range m.lists {
		l := &m.lists[li]
		for ei := range l.entries {
			if l.entries[ei].ord != ord {
				continue
			}
			if rt.accumHeld == nil {
				rt.accumHeld = map[*xdm.Node]*heldAccum{}
			}
			es := l.entries[ei:]
			var pair func(a *xdm.Node) bool
			pair = func(a *xdm.Node) bool {
				if len(es) == 0 || !es[0].closed {
					return false
				}
				e := es[0]
				rt.accumHeld[a] = &heldAccum{sa: m.sa, before: e.before, after: e.after, beforeAt: e.beforeAt, afterAt: e.afterAt}
				es = es[1:]
				for ch := a.FirstChild(); ch != nil; ch = ch.NextSibling() {
					if !pair(ch) {
						return false
					}
				}
				return true
			}
			if !pair(s) {
				return errAccumOffStream
			}
			m.lists = m.lists[li:]
			return nil
		}
	}
	return errAccumOffStream
}

// value is fn:accumulator-before (or -after) of def at a snapshot's node
// with held values h; lex is the name as the call wrote it.
func (h *heldAccum) value(def *accumulatorDef, lex string, post bool) (xdm.Sequence, error) {
	sa := h.sa
	if set := sa.rt.treeAccums[sa.root]; set != nil && !accumApplies(set, def) {
		return nil, fmt.Errorf(
			"XTDE3362: accumulator %q is not applicable to this document, "+
				"whose use-accumulators list does not name it", lex)
	}
	i, ok := sa.at[def]
	if !ok {
		return nil, errAccumOffStream
	}
	if sa.read[i] == 0 {
		sa.reads++
		sa.read[i] = sa.reads
	}
	v, at := h.before[i], h.beforeAt
	if post {
		v, at = h.after[i], h.afterAt
	}
	if !sa.rd.valid(i, at) { // §18.2.5
		return nil, sa.errs[i]
	}
	return v, nil
}

// mergeAccumsPending is the error the tree would have raised for the
// accumulators the merge read, every document read to its end.
func mergeAccumsPending(ms []*mergeAccums) error {
	for _, m := range ms {
		if err := m.sa.pending(); err != nil {
			return err
		}
	}
	return nil
}
