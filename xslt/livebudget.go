package xslt

// What a streamed loop holds, rather than everything it built.
//
// The result-node and byte budgets (xpath.MaxNodes, xpath.MaxBytes) are
// cumulative: nothing tells them a temporary tree or a string has been
// dropped, so they bound what a transform builds. Over a tree that is the
// same thing as what it holds, near enough -- the whole document is held
// anyway. A streamed loop is the one place that changes: it reads a document
// of any size in bursts, and a correct stream with a two-node temporary
// variable per record was refused after a million records at 3 MB of heap.
//
// So an item of a streamed xsl:for-each or xsl:iterate (a burst feed drives
// it) hands back, when it finishes, what it built and does not hold. Nothing
// is tracked per tree; the item is judged as a whole, conservatively. Its
// charges are the rise of the two counters across it. It holds nothing it
// built when all of these are so:
//
//   - it wrote nothing to the loop's output -- no item, child, attribute or
//     namespace, and no text merged into the last text node (outSig). Output
//     is held for good, and anything written may refer to what it built;
//   - it forced no global variable, whose value outlives it;
//   - the runtime records no copies (accumOrigin, made only for a stylesheet
//     with accumulators), since that map keeps every copy alive;
//   - what xsl:next-iteration carries on is nodes and atomic values only,
//     which are measured: each distinct tree in full (treeSize) and each
//     value's string. A map, an array or a function item could hold
//     anything, so it ends the hand-back.
//
// The burst admission rules (streamburst.go) are what make that list
// complete: a burst body calls no template or stylesheet function, uses no
// key, accumulator, message or result document, and carries only atomic
// values or a new temporary tree from one item to the next. Any other item
// keeps everything charged, which is the old cumulative behaviour.
//
// The carried state is charged while it is carried: it stays on the counters
// (liveLoop.nodes and .bytes say how much of them it is) and is handed back
// by the item that replaces it. A stream whose carried state grows is thus
// refused once that state crosses the bound, as it should be.
//
// Unless the caller bounds the total itself (MaxNodesBuilt, MaxBytesBuilt),
// an item hands back at most liveNodeAllowance nodes and liveByteAllowance
// bytes. The total built is then at most the bound plus the allowance per
// item: linear in the input, and a stream whose work per item keeps growing
// -- carrying a tree and copying it into the next one -- is still refused in
// bounded time rather than only once what it holds reaches the bound, which
// at quadratic work could take hours.

import (
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
)

// The most a streamed item hands back when the caller sets no total. Far
// above what a record's temporaries take (a few nodes and a few hundred bytes
// in every streamed shape measured), and small enough that a stream whose
// per-item work grows is refused within seconds.
const (
	liveNodeAllowance = 4096
	liveByteAllowance = 64 << 10
)

// liveLoop is one streamed loop's accounting across its items.
type liveLoop struct {
	on bool
	// nodes and bytes are the part of the counters that stands for the
	// iteration parameters being carried.
	nodes, bytes int64
	// The state at the start of the current item.
	n0, b0 int64
	forced int
	sig    outSig
}

// outSig is what an item could change by writing to its output.
type outSig struct{ items, kids, attrs, ns, text int }

func signature(out *outputBuilder) outSig {
	s := outSig{items: len(out.Items())}
	var last *xdm.Node
	if e := out.Open(); e != nil {
		s.kids, s.attrs, s.ns = e.NumChildren(), e.NumAttrs(), e.NumNamespaceDecls()
		last = e.LastChild()
	} else if s.items > 0 {
		last, _ = out.Items()[s.items-1].(*xdm.Node)
	}
	if last != nil && last.Kind() == xdm.KindText {
		s.text = len(last.Value())
	}
	return s
}

// liveLoop starts the accounting of a streamed loop writing to out.
func (rt *runtime) liveLoop() *liveLoop {
	return &liveLoop{on: rt.accumOrigin == nil}
}

// begin marks the start of an item.
func (l *liveLoop) begin(rt *runtime, out *outputBuilder) {
	if !l.on {
		return
	}
	l.n0, l.b0 = xpathleaf.Charged(rt.ctx)
	l.forced, l.sig = rt.globalsForced, signature(out)
}

// end hands back what the item that began at the last begin built and does
// not hold, carried being the iteration parameters it leaves for the next.
func (l *liveLoop) end(rt *runtime, out *outputBuilder, carried []xdm.Sequence) {
	if !l.on {
		return
	}
	n1, b1 := xpathleaf.Charged(rt.ctx)
	cn, cb, ok := carriedSize(carried)
	if !ok || n1 < l.n0 || b1 < l.b0 || rt.globalsForced != l.forced || signature(out) != l.sig {
		// Everything it built stays charged, and so does what the previous
		// items left carried: none of it is known to be dropped.
		l.nodes, l.bytes = 0, 0
		return
	}
	an, ab := int64(liveNodeAllowance), int64(liveByteAllowance)
	if rt.opts.MaxNodesBuilt != 0 {
		an = -1
	}
	if rt.opts.MaxBytesBuilt != 0 {
		ab = -1
	}
	xpathleaf.Refund(rt.ctx, settle(n1-l.n0, &l.nodes, cn, an), settle(b1-l.b0, &l.bytes, cb, ab))
}

// settle returns how much of an item's charges to hand back: of built, and of
// held (what the previous items left charged for the carried state), all but
// need (what the carried state now takes), and at most allow when that is not
// negative. held becomes what stays charged for the carried state.
func settle(built int64, held *int64, need, allow int64) int64 {
	pool := built + *held
	r := max(pool-need, 0)
	if allow >= 0 {
		r = min(r, allow)
	}
	*held = min(need, pool-r)
	return r
}

// carriedSize measures the iteration parameters an item carries on: nodes and
// string bytes, each tree counted once in full. ok is false for an item it
// cannot see into.
func carriedSize(carried []xdm.Sequence) (nodes, bytes int64, ok bool) {
	var seen map[*xdm.Node]bool
	for _, seq := range carried {
		for _, it := range seq {
			switch v := it.(type) {
			case *xdm.Atomic:
				bytes += int64(len(v.Str()))
			case *xdm.Node:
				r := v
				for p := r.Parent(); p != nil; p = p.Parent() {
					r = p
				}
				if seen[r] {
					continue
				}
				if seen == nil {
					seen = map[*xdm.Node]bool{}
				}
				seen[r] = true
				n, b := treeSize(r)
				nodes, bytes = nodes+n, bytes+b
			default:
				return 0, 0, false
			}
		}
	}
	return nodes, bytes, true
}

// treeSize is the nodes of n's subtree, counted as the builder charges them
// (countSubtree), and the bytes of the text, attribute, comment and
// processing-instruction values in it.
func treeSize(n *xdm.Node) (nodes, bytes int64) {
	nodes = int64(1 + n.NumAttrs() + n.NumNamespaceDecls())
	for a := range n.Attrs() {
		bytes += int64(len(a.Value()))
	}
	switch n.Kind() {
	case xdm.KindText, xdm.KindComment, xdm.KindPI, xdm.KindAttribute:
		bytes += int64(len(n.Value()))
	}
	for ch := range n.Children() {
		cn, cb := treeSize(ch)
		nodes, bytes = nodes+cn, bytes+cb
	}
	return nodes, bytes
}
