package xslt

// xsl:copy-of copy-accumulators="yes" of streamed matches (lane L4; XSLT
// 3.0 §18.3, accumulator-048s).
//
// The copy answers with its original's values (noteCopiedAccumulators).
// The original is a match the split grounds; its values, and its
// descendants', exist only while the cursor passes it. So the split of
// such a copy-of grounds each match at once (not later), and around the
// Ground the run's observer records the values of the match and its
// subtree in document order (the merge recorder, streammerge_accum.go),
// which are paired with the grounded copy and kept in accumHeld, where
// fnAccumulator finds them after following the copy back to the grounded
// node. What is kept is the values of the copies the stylesheet asked to
// carry them; the tree keeps those copies' origins too (accumOrigin), and
// the whole document.

import (
	"slices"
	"sync"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// cursorAccums maps a cursor to the accumulator observer of its run.
var cursorAccums sync.Map // *xdmstream.Cursor -> *streamAccums

// readyCopyAccums marks the splits of the xsl:copy-of ops with
// copy-accumulators="yes" in ops (and the ops inside them).
func readyCopyAccums(ops []*streamOp) string {
	for _, op := range ops {
		if isXSL(op.el, "copy-of") && yesAttr(op.el, "copy-accumulators") {
			if op.split == nil {
				return "xsl:copy-of copy-accumulators=\"yes\" with no split"
			}
			op.split.markRecAccums()
		}
		if why := readyCopyAccums(op.inner); why != "" {
			return why
		}
	}
	return ""
}

func (s *streamSplit) markRecAccums() {
	if s == nil {
		return
	}
	s.recAccums, s.later = true, false
	s.then.markRecAccums()
	s.els.markRecAccums()
}

// groundRecording is c.Ground, recording the accumulator values of what it
// grounds when s asks for them and the run computes accumulators.
func groundRecording(c *xdmstream.Cursor, s *streamSplit) (*xdm.Node, error) {
	if !s.recAccums {
		return c.Ground()
	}
	g, ok, err := groundAccums(c)
	if !ok {
		return c.Ground()
	}
	return g, err
}

// groundAccums is c.Ground recording the accumulator values of what it
// grounds; ok false, nothing read, when the run computes no accumulators
// at c's depth.
func groundAccums(c *xdmstream.Cursor) (_ *xdm.Node, ok bool, _ error) {
	v, ok := cursorAccums.Load(c)
	if !ok {
		return nil, false, nil
	}
	sa := v.(*streamAccums)
	d := c.At().Depth
	if d <= 0 || d >= len(sa.frames) {
		return nil, false, nil
	}
	f := &sa.frames[d]
	if err := sa.holdSlots(1); err != nil {
		return nil, true, err
	}
	m := &mergeAccums{sa: sa, minD: d, open: []int{0}}
	m.lists = []mergeAccumList{{top: f.ord, entries: []mergeAccumEntry{{ord: f.ord, before: append([]xdm.Sequence(nil), f.before...), beforeAt: f.beforeAt}}}}
	prev := sa.onStep
	sa.onStep = func(n *xdm.Node, nd int, post bool, ord uint64, cur []xdm.Sequence) {
		if prev != nil {
			prev(n, nd, post, ord, cur)
		}
		if nd > d || nd == d && post {
			m.note(n, nd, post, ord, cur)
		}
	}
	g, err := c.Ground()
	sa.onStep = prev
	if err != nil {
		return nil, true, err
	}
	if err := m.attach(sa.rt, g, g); err != nil {
		return nil, true, err
	}
	return g, true, nil
}

// recordCopy is build, which copies the node the cursor is at (at depth d:
// the document node at 0) off its events, with the accumulator values of the
// original's nodes recorded as the cursor passes them and paired with the
// copy's nodes (accumHeld), as groundAccums pairs a grounded copy: §18.2.10
// gives a copy its original's values, where the copy's own tree would
// compute others (audit A2-1). ok false, nothing read, when the run computes
// no accumulators at c's depth.
func recordCopy(rt *runtime, c *xdmstream.Cursor, d int, build func() (*xdm.Node, error)) (_ *xdm.Node, ok bool, _ error) {
	v, ok := cursorAccums.Load(c)
	if !ok {
		return nil, false, nil
	}
	sa := v.(*streamAccums)
	if d >= len(sa.frames) {
		return nil, false, nil
	}
	f := &sa.frames[d]
	// Each node's values are charged as recorded (holdSlots); herr is the
	// refusal, after which nothing is.
	herr := sa.holdSlots(1)
	if herr != nil {
		return nil, true, herr
	}
	// es are the subtree's values in document order; open, per depth below
	// d, the entry of the element open there.
	es := []heldAccum{{sa: sa, before: slices.Clone(f.before), beforeAt: f.beforeAt}}
	open := []int{0}
	prev := sa.onStep
	sa.onStep = func(n *xdm.Node, nd int, post bool, ord uint64, cur []xdm.Sequence) {
		if prev != nil {
			prev(n, nd, post, ord, cur)
		}
		k := nd - d
		if k < 0 || k == 0 && !post || herr != nil {
			return
		}
		if !post {
			if herr = sa.holdSlots(1); herr != nil {
				return
			}
		}
		vals, at := slices.Clone(cur), sa.rd.phase
		switch el := n.Kind() == xdm.KindElement; {
		case !post:
			es = append(es, heldAccum{sa: sa, before: vals, beforeAt: at})
			if el {
				open = append(open[:k], len(es)-1)
			}
		case el:
			es[open[k]].after, es[open[k]].afterAt = vals, at
		default:
			es[len(es)-1].after, es[len(es)-1].afterAt = vals, at
		}
	}
	g, err := build()
	sa.onStep = prev
	if err == nil {
		err = herr
	}
	if err != nil {
		return nil, true, err
	}
	if d == 0 { // the document's end steps no onStep
		if f = &sa.frames[0]; !f.closed { // the frames may have moved
			return nil, true, errAccumOffStream
		}
		es[0].after, es[0].afterAt = slices.Clone(f.after), f.afterAt
	}
	tbl, i := rt.accumHeld.at(g.Root()), 0
	var pair func(a *xdm.Node) bool
	pair = func(a *xdm.Node) bool {
		if i == len(es) {
			return false
		}
		tbl[a.Index()] = &es[i]
		i++
		for ch := a.FirstChild(); ch != nil; ch = ch.NextSibling() {
			if !pair(ch) {
				return false
			}
		}
		return true
	}
	if !pair(g) || i != len(es) {
		return nil, true, errAccumOffStream
	}
	return g, true, nil
}
