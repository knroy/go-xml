package xslt

// A crawling grouping population under a motionless body (lane L5):
// select="(//*)[position() = 1 to 6]" with a body of text value templates
// reading position() and name() (si-group-A05/B05/C05).
//
// popWalk hands over elements of one depth, a member it is told was not
// read skipped whole. A population whose steps descend (P nests: a match
// may lie inside another) is walked as the split walks P (enter, hidden,
// the frames of the open elements), each match handed over at its start
// tag, and a member never skipped -- the next ones may lie inside it. That
// is exact only when nothing reads a member to its end: the group's body has
// no consuming instruction at all (op.inner is empty), so the cursor is at
// the member's start tag whenever the next one is asked for. The keys and
// patterns are decided on the view, as for popWalk; a window applies as
// there (streamgroup_window.go).

import (
	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xpath"
)

// crawlable reports whether op, a grouping of a streamed population, is
// one crawlWalk reads: a path to elements whose steps nest, no consumer in
// the body, the predicates decided on the view without has-children.
func crawlable(op *streamOp) bool {
	s := op.split
	if s == nil || len(op.inner) > 0 || !s.nests || s.group || s.outermost || s.innermost ||
		s.hasBranches() || s.kids {
		return false
	}
	if _, win := groupWindow(s); !s.bare() && !win {
		return false
	}
	for _, st := range s.steps {
		switch st.axis {
		case xpath.AxisChild, xpath.AxisDescendant, xpath.AxisDescendantOrSelf:
		default:
			return false
		}
		if st.axis != xpath.AxisDescendantOrSelf {
			if leaf, elem := testKinds(st.test); leaf || !elem {
				return false
			}
		}
		for _, p := range st.preds {
			if p.kids {
				return false
			}
		}
	}
	return true
}

// crawlWalk is a crawlable population: the matches of s from the context at
// depth d, in document order, nested ones too.
type crawlWalk struct {
	s     *streamSplit
	r     splitRun
	d     int
	filt  xdmstream.Filter
	stack []splitFrame // the context's frame, then each open element's
	skip  bool         // the element at hand holds no candidate: pass over it
	done  bool
	win   groupWin
	// predErr is the error a predicate raised, the select's.
	predErr error
}

// newCrawlWalk starts the walk of s at depth d; ok is false when the context
// itself would be a member (the plan does not cover it).
func newCrawlWalk(s *streamSplit, ctx *xpath.Context, c *xdmstream.Cursor, d int) (*crawlWalk, bool) {
	w := &crawlWalk{s: s, d: d, r: splitRun{s: s, ctx: ctx, c: c}}
	w.filt = xdmstream.Filter{Accept: s.accept, Depth: d}
	w.win, _ = groupWindow(s)
	var root splitFrame
	if s.rootFrame(&root) {
		return nil, false
	}
	w.stack = []splitFrame{root}
	return w, true
}

// next implements memberSource. read is not looked at: nothing reads a
// member (crawlable), so the cursor is at the start tag of the one handed
// over last, whose content holds the next ones.
func (w *crawlWalk) next(bool) (xdmstream.Event, bool, error) {
	if w.done {
		return xdmstream.Event{}, false, nil
	}
	c, s := w.r.c, w.s
	for {
		var (
			ev  xdmstream.Event
			err error
		)
		if w.skip {
			ev, err = c.SkipTag(&w.filt)
		} else {
			ev, err = c.NextTag(&w.filt)
		}
		w.skip = false
		if err != nil {
			return ev, false, err
		}
		if ev.Kind == xdmstream.EndDocument || ev.Kind == xdmstream.EndElement && ev.Depth == w.d {
			w.done = true
			return xdmstream.Event{}, false, nil
		}
		// As splitRun.walk: what closed is gone, those above the event get
		// their frames.
		for ev.Closed > 0 && w.d+len(w.stack)-1 >= ev.Closed {
			w.stack = w.stack[:len(w.stack)-1]
		}
		at := ev.Depth
		if ev.Kind == xdmstream.EndElement {
			at++
		}
		for w.d+len(w.stack) > at {
			w.stack = w.stack[:len(w.stack)-1]
		}
		for w.d+len(w.stack) < at {
			w.stack = append(w.stack, s.hidden(&w.stack[len(w.stack)-1]))
		}
		if ev.Kind == xdmstream.EndElement {
			w.stack = w.stack[:len(w.stack)-1]
			continue
		}
		if ev.Kind != xdmstream.StartElement {
			continue
		}
		cand := splitAt{r: &w.r, n: ev.Node}
		f, match, err := s.enter(&w.stack[len(w.stack)-1], &cand)
		if err != nil {
			w.predErr = err
			return ev, false, err
		}
		if f.live() {
			w.stack = append(w.stack, f)
		} else {
			w.skip = true
		}
		if !match {
			continue
		}
		out, err := w.win.outside(w.r.ctx)
		if err != nil {
			w.predErr = err
			return ev, false, err
		}
		if !out {
			return ev, true, nil
		}
	}
}
