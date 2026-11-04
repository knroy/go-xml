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
	v, ok := cursorAccums.Load(c)
	if !ok {
		return c.Ground()
	}
	sa := v.(*streamAccums)
	d := c.At().Depth
	if d <= 0 || d >= len(sa.frames) {
		return c.Ground()
	}
	f := &sa.frames[d]
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
		return nil, err
	}
	if err := m.attach(sa.rt, g, g); err != nil {
		return nil, err
	}
	return g, nil
}
