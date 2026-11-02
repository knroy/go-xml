package xdm

import (
	"io"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/internal/xdmclone"
)

// Burst-mode parsing: the incremental half of streamed XSLT execution.
//
// ParseBursts reads a document exactly as Parse does -- the same tokeniser,
// the same well-formedness checks, the same DOCTYPE handling, and the same
// MaxBytes, MaxNodes and MaxDepth, counted over the whole document -- but it
// never holds the whole tree. Only the chain of open ancestors (the "spine")
// is kept. Each element at the end of path is handed to fn once it is
// complete, as the document element's descendant in a small tree of its own,
// and is then dropped, records and text both. Everything else -- elements off
// the path, and the text, comments and processing instructions directly
// inside a spine element -- is built, checked, and dropped the same way.
//
// The tree fn receives holds the document node (with the document's base and
// document URIs), a shallow copy of each ancestor (name, namespace
// declarations, attributes, base URI) and a deep copy of the element. So the
// element's base URI, in-scope namespaces, attributes, parent and ancestors
// read as they would in the whole tree; its siblings, and anything else
// outside its ancestry -- the comments and processing instructions outside
// the document element too -- do not exist there. Each burst is a new tree, so
// nodes from different bursts are distinct and order by creation, which is
// document order; a node fn keeps stays valid after fn returns.

// ParseBursts parses r and calls fn with each element whose ancestry of
// element names, from the document element down, is path, of at most
// xdmburst.MaxSteps names. An error from fn stops the parse and is returned.
func ParseBursts(r io.Reader, opts ParseOptions, path []QName, fn func(*Node) error) error {
	steps := make([]xdmburst.Step, len(path))
	for i, q := range path {
		steps[i] = xdmburst.Step{URI: q.URI, Local: q.Local}
	}
	return parseBursts(r, opts, steps, fn)
}

func init() {
	xdmburst.Parse = func(r io.Reader, opts any, steps []xdmburst.Step, fn func(any) error) error {
		return parseBursts(r, opts.(ParseOptions), steps, func(n *Node) error { return fn(n) })
	}
}

// parseBursts is ParseBursts over a pattern that may have wildcards and
// descendant steps. An element whose ancestry matches it is a burst; one
// whose ancestry could still lead to a match below it is kept on the spine;
// any other is dropped. A match inside a burst is delivered with it, so the
// bursts are the outermost matches, in document order.
func parseBursts(r io.Reader, opts ParseOptions, steps []xdmburst.Step, fn func(*Node) error) error {
	if len(steps) == 0 || len(steps) > xdmburst.MaxSteps {
		panic("xdm: ParseBursts needs a path of 1 to 63 names")
	}
	opts.bursts = &burster{steps: steps, fn: fn, states: []uint64{1}, gens: []uint64{0}}
	_, err := parse(r, "", opts)
	return err
}

// burster is ParseBursts' state inside parse.
type burster struct {
	steps []xdmburst.Step
	fn    func(*Node) error
	// spine is how many levels of the open chain could lead to a match and
	// are kept.
	spine int
	// drop is the depth of the element being built only to be dropped (a
	// burst, or an element off the path), or 0; burst says it is a burst.
	drop  int
	burst bool
	// states[d] is the set of pattern steps the spine element at depth d
	// (0: the document node) has matched, as bits: bit k means the first k
	// steps are matched by it and its ancestors.
	states []uint64
	// marks[d] is the tree's state once the spine element at depth d (0:
	// the document node) had stored its attributes. Everything stored after
	// it belongs to content that is dropped.
	marks []burstMark
	// gens[d] tells the spine element at depth d from the others that have
	// been there: gen counts the spine elements opened.
	gens []uint64
	gen  uint64
	// batch holds the small bursts not yet delivered (burstBatch), or is nil.
	batch *burstBatch
	// spineCopies counts the records copied as ancestors of a burst, for
	// the test that keeps it in proportion to the elements read.
	spineCopies int
	// moved is the first of the parse's chunks the last burst's tree took
	// over, or 0.
	moved int
	v11   bool
}

// burstMark is how much of the text store, the name table and the frame
// table a spine element and its ancestors use.
type burstMark struct {
	text                     textMark
	names, frames, frameData int
}

func markOf(t *Tree) burstMark {
	return burstMark{t.text.mark(), len(t.names), len(t.frames), len(t.frameData)}
}

// start classifies the element just built at depth d and opened as el.
func (b *burster) start(el *Node, d int) {
	if b.drop != 0 {
		return
	}
	n := b.advance(b.states[b.spine], el.Name())
	switch {
	case n&(1<<len(b.steps)) != 0:
		b.drop, b.burst = d, true
	case n != 0:
		b.spine = d
		b.states = append(b.states, n)
		b.marks = append(b.marks, markOf(el.tree))
		b.gen++
		b.gens = append(b.gens, b.gen)
	default:
		b.drop = d
	}
}

// advance is the set of steps matched by an element named q whose parent
// matched the set s.
func (b *burster) advance(s uint64, q QName) uint64 {
	var n uint64
	for k, st := range b.steps {
		if s&(1<<k) == 0 {
			continue
		}
		if st.Descendant {
			n |= 1 << k // q may be on the way to step k
		}
		if (st.AnyURI || st.URI == q.URI) && (st.AnyLocal || st.Local == q.Local) {
			n |= 1 << (k + 1)
		}
	}
	return n
}

// end handles the close of el, at depth d, whose parent is now current. It
// delivers a burst and drops whatever is not spine.
func (b *burster) end(el *Node, d int, spaces *spaceTable, names *nameCache) error {
	switch {
	case b.drop == d:
		b.drop = 0
		if b.burst {
			b.burst = false
			if err := b.take(el); err != nil {
				return err
			}
		}
	case b.drop == 0 && d == b.spine && d > 0:
		b.spine--
		b.marks = b.marks[:len(b.marks)-1]
		b.states = b.states[:len(b.states)-1]
		b.gens = b.gens[:len(b.gens)-1]
	default:
		return nil
	}
	if d == 1 {
		// The document element is done: so is the last batch.
		if err := b.flush(); err != nil {
			return err
		}
	}
	b.discardLast(el.Parent(), spaces, names)
	return nil
}

// trimLeaves drops the leaves completed directly inside the spine element
// cur, at depth d: text, comments and processing instructions no path step
// selects.
//
// It is also where a batch is delivered early: what the parse stored since
// cur's mark is all garbage now, and once a child of cur opens it cannot be
// taken back until cur closes, so it is not let grow past burstFrozen.
func (b *burster) trimLeaves(cur *Node, d int, spaces *spaceTable, names *nameCache) error {
	if b.drop != 0 || d == 0 || d != b.spine {
		return nil
	}
	for last := cur.LastChild(); last != nil && last.isLeaf(); last = cur.LastChild() {
		b.discardLast(cur, spaces, names)
	}
	if b.batch != nil && textSince(cur.tree, b.marks[len(b.marks)-1].text.blocks) >= burstFrozen {
		if err := b.flush(); err != nil {
			return err
		}
		b.rewind(cur.tree, spaces, names)
	}
	return nil
}

// textSince is how many bytes t's text store holds in blocks from the
// block numbered from on.
func textSince(t *Tree, from int) int {
	n := 0
	for _, blk := range t.text.blocks[from:] {
		n += len(blk)
	}
	return n
}

// discardLast removes p's last child and the text, names and namespace
// frames stored since p's spine mark; names is the parse's name cache.
func (b *burster) discardLast(p *Node, spaces *spaceTable, names *nameCache) {
	p.RemoveLastChild()
	if t := p.tree; b.moved > 0 {
		// Those chunks are the burst's now; the parse allocates afresh.
		clear(t.chunks[b.moved:])
		t.chunks = t.chunks[:b.moved]
		b.moved = 0
	}
	if b.batch != nil {
		// The batch's records point into the stores: they are taken back
		// once it has been delivered.
		return
	}
	b.rewind(p.tree, spaces, names)
}

// rewind takes back what the parse's tree t stored since the deepest
// spine element's mark.
func (b *burster) rewind(t *Tree, spaces *spaceTable, names *nameCache) {
	m := b.marks[len(b.marks)-1]
	t.text.rewind(m.text)
	rewindNames(t, m.names, names)
	t.rewindFrames(m.frames, m.frameData)
	// The whitespace table remembers ranges in the store, and some of them
	// were just released. Its map is not rebuilt for the next burst.
	*spaces = spaceTable{noMap: true}
}

// take delivers the burst el: a large one at once, in a tree of its own
// (deliver), and a small one with the bursts before it (burstBatch).
func (b *burster) take(el *Node) error {
	t := el.tree
	if t.endOf(el)-el.self > burstSmall {
		if err := b.flush(); err != nil {
			return err
		}
		return b.deliver(el)
	}
	b.add(el)
	if bt := b.batch; int(bt.t.n) >= max(burstBatchRecords, 4*len(b.gens)) ||
		textSince(t, bt.blocks) >= burstBatchText {
		return b.flush()
	}
	return nil
}

// A burst of more than burstSmall records is delivered on its own; smaller
// ones are batched until the batch holds burstBatchRecords records (or four
// times the spine's depth), or the parse has stored burstBatchText bytes of
// text since the batch began. The text a spine element's content stored
// before a child of it opened waits for the element to close: burstFrozen
// bounds it.
//
// ponytail: per level, so a batch open under a deep spine can leave up to
// depth x burstFrozen (64 MB at MaxDepth 1000) waiting; a total would need
// the levels' shares tracked.
const (
	burstSmall        = chunkMax
	burstBatchRecords = 4096
	burstBatchText    = 1 << 20
	burstFrozen       = 64 << 10
)

// burstBatch is a tree that small bursts are copied into, each under copies
// of its ancestors, until it is delivered: its bursts are handed to fn one
// after another, all in the one tree. A burst under the same ancestors as
// the one before it shares their copies, so the ancestors are copied once
// for each that is opened, and once more for each batch, rather than once
// for every burst: a burst under a deep spine cost the spine's depth.
//
// Its records are copied verbatim, sharing the parse's names, frames and
// text, which the parse therefore does not take back (discardLast) until
// the batch has been delivered. Bursts in one tree are still disjoint
// subtrees in document order, each with its whole ancestry, and nothing a
// burst may read tells it from a tree of its own (see the xslt gate).
type burstBatch struct {
	t *Tree
	// path[d] is the copy of the spine element at depth d (0: the document
	// node), and gens[d] that element's gens entry.
	path []uint32
	gens []uint64
	// els are the bursts, in order.
	els []uint32
	// blocks is how many text blocks the parse had when the batch began.
	blocks int
	src    *Tree
}

// add copies el, a small burst, into the batch, under copies of the spine
// elements it does not share with the burst before it.
func (b *burster) add(el *Node) {
	t := el.tree
	bt := b.batch
	if bt == nil {
		bt = &burstBatch{t: &Tree{}, blocks: len(t.text.blocks), src: t}
		b.batch = bt
		bt.t.rec(bt.copyRange(t, 0, 1, noIdx, noIdx)).v1 = noIdx
		bt.path, bt.gens = append(bt.path, 0), append(bt.gens, 0)
	}
	d := len(b.gens) - 1 // el's parent
	k := 1
	for k < len(bt.path) && k <= d && bt.gens[k] == b.gens[k] {
		k++
	}
	bt.closeTo(k)
	anc := make([]*Node, 0, d-k+1)
	for p := el.Parent(); len(anc) < d-k+1; p = p.Parent() {
		anc = append(anc, p)
	}
	for i := len(anc) - 1; i >= 0; i-- {
		s := anc[i]
		j := bt.appendUnder(t, s.self, s.self+1+s.attrCount())
		e := bt.t.rec(j)
		e.end, e.v1 = openEnd, noIdx
		bt.path, bt.gens = append(bt.path, j), append(bt.gens, b.gens[k])
		b.spineCopies += 1 + int(s.attrCount())
		k++
	}
	bt.els = append(bt.els, bt.appendUnder(t, el.self, t.endOf(el)))
}

// appendUnder copies the records [lo, hi) of t, a node's own and those after
// it, as the last child of the batch's deepest copy, and returns its index.
func (bt *burstBatch) appendUnder(t *Tree, lo, hi uint32) uint32 {
	p := bt.t.rec(bt.path[len(bt.path)-1])
	j := bt.copyRange(t, lo, hi, p.self, p.v1)
	p.v1 = j
	return j
}

// closeTo ends the copies at depths k and deeper.
func (bt *burstBatch) closeTo(k int) {
	for i := len(bt.path) - 1; i >= k; i-- {
		bt.t.rec(bt.path[i]).end = bt.t.n
	}
	bt.path, bt.gens = bt.path[:k], bt.gens[:k]
}

// copyRange appends copies of the records [lo, hi) of t, whose links within
// the range are renumbered; the first's parent and previous sibling are
// parent and prev. It returns the first's index.
func (bt *burstBatch) copyRange(t *Tree, lo, hi, parent, prev uint32) uint32 {
	c := bt.t
	base := c.n
	remap := func(x uint32) uint32 {
		if x >= lo && x <= hi {
			return x - lo + base
		}
		return x
	}
	for i := lo; i < hi; i++ {
		s := t.rec(i)
		r := *s
		d := c.alloc()
		j := d.self
		r.tree, r.self = c, j
		if i == lo {
			r.parent, r.prev = parent, prev
		} else {
			r.parent = remap(r.parent)
			if r.prev != noIdx {
				r.prev = remap(r.prev)
			}
		}
		if r.isLeaf() {
			r.end = j + 1
		} else {
			r.end = remap(t.endOf(s))
			if r.v1 != noIdx {
				r.v1 = remap(r.v1)
			}
		}
		*d = r
		if s.flags&fTyped != 0 {
			*d.ownTyping() = *s.typ() // the names are shared, not copied
		}
		if s.flags&fBase != 0 {
			if c.bases == nil {
				c.bases = map[uint32]string{}
			}
			c.bases[j] = t.bases[i]
		}
		if s.flags&fManyAttrs != 0 {
			if src := c.ownSource(); src.attrCounts == nil {
				src.attrCounts = map[uint32]uint32{}
			}
			c.source.attrCounts[j] = t.source.attrCounts[i]
		}
		if s.kind == uint8(KindDocument) {
			if u := t.docURIs[i]; u != "" {
				if c.docURIs == nil {
					c.docURIs = map[uint32]string{}
				}
				c.docURIs[j] = u
			}
		}
	}
	return base
}

// flush delivers the batch, if there is one: it becomes a document as a
// burst's tree is (deliver), sharing the parse's stores as they now are,
// and fn is called with each of its bursts in order.
func (b *burster) flush() error {
	bt := b.batch
	if bt == nil {
		return nil
	}
	b.batch = nil
	bt.closeTo(0)
	c, t := bt.t, bt.src
	c.rec(0).end = c.n
	c.Root = c.rec(0)
	c.id.Store(int64(nextTreeID()))
	c.DocType, c.XMLVersion = t.DocType, "1.0"
	if b.v11 {
		c.XMLVersion = "1.1"
	}
	if s := t.source; s != nil {
		c.ownSource().externalSubset = s.externalSubset
	}
	c.names = t.names[:len(t.names):len(t.names)]
	c.text.blocks = make([][]byte, len(t.text.blocks))
	for i, blk := range t.text.blocks {
		c.text.blocks[i] = blk[:len(blk):len(blk)]
	}
	c.text.size = t.text.size
	c.frames = t.frames[:len(t.frames):len(t.frames)]
	c.frameData = t.frameData[:len(t.frameData):len(t.frameData)]
	for _, i := range bt.els {
		if err := b.fn(c.rec(i)); err != nil {
			return err
		}
	}
	return nil
}

// deliver hands el to fn in a tree of its own. By now the parse's tree holds
// nothing but the document node, the spine and el (a burst parse does not
// keep the prolog's comments and processing instructions, which every burst
// would copy), which is exactly the burst's tree: it is
// copied in bulk, sharing the names, frames and text the records point into.
// The chunks wholly past el's first are not copied but taken over, so that
// one large burst is not held twice; discardLast drops them from the parse.
func (b *burster) deliver(el *Node) error {
	t := el.tree
	m := cloneMoving(t.Root, xdmclone.Options{}, el.self)
	k, _ := chunkOf(el.self)
	if last, _ := chunkOf(t.n - 1); last > k {
		b.moved = k + 1
	}
	c := m(el)
	b.spineCopies += int(el.self)
	c.tree.XMLVersion = "1.0"
	if b.v11 {
		c.tree.XMLVersion = "1.1"
	}
	return b.fn(c)
}

// textMark is a point in a textStore that rewind returns it to.
type textMark struct{ blocks int }

func (s *textStore) mark() textMark { return textMark{len(s.blocks)} }

// rewind forgets every value stored since m. A burst's tree shares the
// blocks those values are in, so they are released rather than reused: the
// block that was current at m is closed to further values, and the blocks
// made since are dropped from the store -- the burst trees that read them
// keep them alive as long as they need them.
func (s *textStore) rewind(m textMark) {
	for i := m.blocks; i < len(s.blocks); i++ {
		s.blocks[i] = nil
	}
	s.blocks = s.blocks[:m.blocks]
	s.cur, s.size = 0, 0
	// The shared whitespace values and any borrowed blocks may lie in the
	// blocks just dropped. The values share registers come from the DTD,
	// stored before any mark.
	if s.aux != nil {
		s.aux.ws = spaceTable{}
		s.aux.lent, s.aux.last = nil, nil
	}
}

// burstSlack is how many names, or frames, the content of the spine may add
// before they are taken back. Most documents repeat a few names, which are
// then interned once rather than again for every burst; distinct ones are
// held to this many.
const burstSlack = 1024

// rewindNames forgets the names interned since the table held n, and their
// indices in the tree's cache and in cache, once there are more than
// burstSlack. A burst's
// tree shares the table, so the table is clipped rather than overwritten:
// the next name appended copies it.
func rewindNames(t *Tree, n int, cache *nameCache) {
	if len(t.names) <= n+burstSlack {
		return
	}
	for _, q := range t.names[n:] {
		if i, ok := t.nameIx[q]; ok && int(i) >= n {
			delete(t.nameIx, q)
		}
	}
	t.names = t.names[:n:n]
	if n == 0 {
		t.names = nil // intern starts a table with the empty name at 0
	}
	if h := t.nameHot; h != nil {
		for i, x := range h {
			if int(x) >= n {
				h[i] = 0
			}
		}
	}
	for i := range cache {
		if int(cache[i].idx) >= n {
			cache[i].q, cache[i].idx = QName{}, 0
		}
	}
}

// rewindFrames forgets the namespace frames interned since the table held n
// frames and nd bindings, as rewindNames does names.
func (t *Tree) rewindFrames(n, nd int) {
	if len(t.frames) <= n+burstSlack {
		return
	}
	for i := n; i < len(t.frames); i++ {
		f := t.frameAt(uint32(i))
		if len(f) == 1 {
			if j, ok := t.frameOne[f[0]]; ok && int(j) >= n {
				delete(t.frameOne, f[0])
			}
		} else if t.frameMany != nil {
			k := frameKey(f)
			if j, ok := t.frameMany[k]; ok && int(j) >= n {
				delete(t.frameMany, k)
			}
		}
	}
	t.frames = t.frames[:n:n]
	if n == 0 {
		t.frames = nil
	}
	t.frameData = t.frameData[:nd:nd]
}
