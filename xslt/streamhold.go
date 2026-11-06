package xslt

// Holding the nodes a select at the document node collects (lane g1).
//
// A split whose outer needs the matches themselves -- remove(P, 3),
// P treat as node()+, deep-equal(P, $v), (P, $v) -- collects them, as
// grounded copies (matchGround) or views (matchView), before the outer runs
// once. The memory rule (DESIGN section 2.1, rule 3) refused that at the
// document node, because what is collected could cost more than the tree.
// It no longer refuses: it caps. The copies are of disjoint matches, made
// into shared batch trees (GroundLater) that hold the matches' records and
// their ancestors', so they cost what the tree costs for the same nodes; a
// tree-mode parse holds every record read so far. holdOn counts the records
// of each tree an item lives in (once a tree) against the records a tree-mode
// parse holds by the latest item -- its ordinal plus its own subtree -- and
// gives up (errSplitGiveUp) when the held ones are more. At the document node
// a give-up falls back to the tree (streamRun.fallback): the copies are let
// go, the document is read as a tree, the select is evaluated there. So a
// streamed run never holds more records than the tree would, and in the
// usual case (the matches are a part of the document) holds a part of them.
// A principal input has no tree to fall back to: such a split gives up
// (givesUp) and the main plan refuses it, as before, unless it can match
// one node only (streammain_onematch.go).

import "github.com/knroy/go-xml/v2/xdm"

// splitHold is what a holding split has collected so far.
type splitHold struct {
	// roots are the roots of the trees the items live in, each with the
	// records counted for it: a tree small grounded copies are handed out
	// of grows after (xdm's ground batch).
	roots map[*xdm.Node]int64
	held  int64 // records of the trees the items live in
	ord   uint64
	last  *xdm.Node // the item element with the greatest ordinal
	tree  int64     // records a tree-mode parse holds by last's end
	// copies counts the grounded copies, each charged half a record more.
	copies int64
}

// holdSlack is how far held may pass tree: each batch tree copies its
// matches' ancestors once more, a few records in thousands.
//
// ponytail: a relative 1/64 plus 64 records; measured bytes per record are
// the tree's (batched copies share the parse's names, frames and text), a
// byte count would need xdm's chunk accounting.
func holdSlack(tree int64) int64 { return tree>>6 + 64 }

// holdOn accounts for items, which the split is about to collect.
func (r *splitRun) holdOn(items xdm.Sequence) error {
	h := r.hold
	if h == nil {
		h = &splitHold{roots: map[*xdm.Node]int64{}}
		r.hold = h
	}
	for _, it := range items {
		n, ok := it.(*xdm.Node)
		if !ok {
			continue
		}
		root := n.Root()
		if t := n.Tree(); t != nil {
			if k := int64(t.Len()); k > h.roots[root] {
				h.held += k - h.roots[root]
				h.roots[root] = k
				// The parse has read the batch's last record, which the
				// tree holds too.
				h.reach(r, t.NodeAt(t.Len()-1))
			}
		} else if _, ok := h.roots[root]; !ok {
			h.roots[root] = records(root, true)
			h.held += h.roots[root]
		}
		if r.s.mode == matchGround && n.Kind() == xdm.KindElement {
			// Each copy carries its origin in the batch (a segment):
			// measured 26 B a copy above the tree's records, charged as
			// half a record (40 B) here and the other half below.
			h.copies++
		}
		h.reach(r, n)
	}
	held := h.held + (h.copies+1)/2
	if held <= h.tree+holdSlack(h.tree) {
		return nil
	}
	if h.last != nil && r.s.mode == matchGround {
		// A copy holds the original's subtree, which the tree holds too.
		h.tree = int64(h.ord) + records(h.last, true)
	}
	if held <= h.tree+holdSlack(h.tree) {
		return nil
	}
	return errSplitGiveUp
}

// reach moves the tree's side to n's element (n, or the nearest above it),
// when that comes later in the document than any before.
func (h *splitHold) reach(r *splitRun, n *xdm.Node) {
	e := n
	for e != nil && e.Kind() != xdm.KindElement {
		e = e.Parent()
	}
	if e == nil {
		return
	}
	if o, ok := r.c.Ordinal(e); ok && (h.last == nil || o > h.ord) {
		h.ord, h.last, h.tree = o, e, int64(o)+1+int64(e.NumAttrs())
	}
}

// records counts n's subtree: n, its attributes, its descendants and theirs
// (deep), or n and its attributes. A view (shallow) is never asked deep.
func records(n *xdm.Node, deep bool) int64 {
	k := int64(1 + n.NumAttrs())
	if !deep || n.Kind() != xdm.KindElement && n.Kind() != xdm.KindDocument {
		return k
	}
	for d := range n.Descendants() {
		k += int64(1 + d.NumAttrs())
	}
	return k
}

// holdAt admits s, a split at the document node that collects nodes,
// under the cap: a match that may be the document element gives up at once
// (that copy is the tree).
func (s *streamSplit) holdAt() {
	if s.countAt() {
		return // counted, not held (streamfold.go)
	}
	s.noRoot, s.hold = s.minDepth() < 2 && !s.itemsOnlyCopied(), true // streamfn_copies.go
}
