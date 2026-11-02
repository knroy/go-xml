package xdm

import (
	"sort"
	"sync"
	"weak"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
)

// Views of a streamed document, the posture guard, and origin ordinals
// (docs: the full-streaming design, C2).
//
// A view is what the stream driver hands out for an element at its start
// tag, in place of the parse's own record, which is reused once the element
// is dropped: a node in a tree of its own holding a copy of the document
// node, a shallow copy of each ancestor (name, namespace declarations,
// attributes, base URI, typing) and a shallow copy of the element, without
// its content. A view stays valid for as long as it is held.
//
// Views are made into a batch tree, the one a burst batch is (bursts.go): an
// element viewed under the same ancestors as the one before it shares their
// copies, so a view costs the element's own records and those of the
// ancestors opened since the last view, not its depth. A batch is replaced
// once it holds viewBatchRecords records, or four times the records of the
// copies of the open path if that is more: what a retained view keeps alive
// is bounded, and the ancestors a new batch copies again are paid for by the
// views made in the old one. (Not four times all it ever copied for
// ancestors: under ancestors that are not themselves viewed, each one new,
// that grew with the batch, and the batch was never replaced.)
//
// Every copied element and document node is shallow (fShallow): asking it
// for anything its content would answer -- children, descendants, siblings,
// a string or typed value -- is a posture fault (shallowFault), never an
// empty answer. Its name, attributes, namespaces, base URI, ancestors and
// identity are what a motionless construct reads, and they are all there.
//
// Every record copied out of a stream, a view's or a grounded copy's, also
// carries its origin (fOrigin): the stream it came from and its ordinal, the
// index the record has in a tree-mode parse of the same document. Order,
// identity and generate-id read those instead of the copy's own tree and
// index, so two copies of one element are the same node, and copies of
// different elements order as their originals do.

// viewBatchRecords is the size a view batch is replaced at.
const viewBatchRecords = 4096

// streamOrigin is one stream: its id, which is its parse tree's (every tree
// copied out of it orders among other trees where the parse's would), and
// the canonical node of each ordinal Identity was asked for.
type streamOrigin struct {
	id    int64
	mu    sync.Mutex
	canon map[uint64]weak.Pointer[Node]
	sweep int // len(canon) at which dead entries are next dropped
}

// treeOrigin is where the records of a tree copied out of a stream come
// from: segs, by increasing at, says record i is the original numbered
// segs[k].ord + (i - segs[k].at), for the last k with segs[k].at <= i.
type treeOrigin struct {
	s    *streamOrigin
	segs []originSeg
}

type originSeg struct {
	at  uint32
	ord uint64
}

// add records that the records from at on are the originals from ord on.
func (o *treeOrigin) add(at uint32, ord uint64) {
	o.segs = append(o.segs, originSeg{at, ord})
}

// ord is the ordinal of record i.
func (o *treeOrigin) ord(i uint32) uint64 {
	k := sort.Search(len(o.segs), func(j int) bool { return o.segs[j].at > i }) - 1
	return o.segs[k].ord + uint64(i-o.segs[k].at)
}

// origin returns n's tree's origin; n must carry fOrigin.
func (n *Node) origin() *treeOrigin { return n.tree.source.orig }

// originKey places n, which carries fOrigin, in its stream's document order
// as orderKey places a node in its tree's: the key a tree-mode parse would
// give the original.
func (n *Node) originKey() uint64 {
	o := n.origin()
	if n.flags&fSide != 0 {
		return o.ord(n.parent)<<16 | uint64(1+n.end)
	}
	return o.ord(n.self) << 16
}

// sameOrigin reports whether n and o, which both carry fOrigin, are copies
// of one original.
func (n *Node) sameOrigin(o *Node) bool {
	return n.origin().s == o.origin().s && n.originKey() == o.originKey()
}

// canonical is the node Identity stands for when n carries fOrigin: the
// first copy of n's original still alive that Identity was asked about.
//
// ponytail: one weak handle per node asked, which the runtime keeps per
// span; fine for the set operators over streamed nodes, a chunked table as
// sideweak.go's if a profile shows it.
func (n *Node) canonical() *Node {
	s, k := n.origin().s, n.originKey()
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.canon[k]; ok {
		if c := w.Value(); c != nil {
			return c
		}
	}
	if s.canon == nil {
		s.canon = map[uint64]weak.Pointer[Node]{}
	}
	if len(s.canon) >= s.sweep {
		for x, w := range s.canon {
			if w.Value() == nil {
				delete(s.canon, x)
			}
		}
		s.sweep = max(64, 2*len(s.canon))
	}
	s.canon[k] = weak.Make(n)
	return n
}

// setOrigin gives t, a tree being copied out of the stream s, origin o.
func (t *Tree) setOrigin(s *streamOrigin) *treeOrigin {
	o := &treeOrigin{s: s}
	t.ownSource().orig = o
	t.id.Store(s.id)
	return o
}

// shallowFault reports a posture fault: what was asked of the shallow node
// n needs content a view does not have. The streamed path's static gate
// should never let it happen, so it is counted for the differential tests
// and raised as a panic that the streamed executor turns into a re-run on
// the tree.
func shallowFault(n *Node, what string) {
	streamtrace.Guard()
	panic(&streamtrace.PostureError{Op: what, Kind: n.Kind().String()})
}

// viewBatch is the tree views are being made into.
type viewBatch struct {
	t *Tree
	o *treeOrigin
	// path[d] is the copy of the open element at depth d (0: the document
	// node), and gens[d] that element's gen.
	path []*Node
	gens []uint64
	// recs[d] is how many records the copies path[:d+1] hold.
	recs []int
}

// view returns the view of the element the client is being asked about
// (see start): nil anywhere else.
func (b *burster) view() *Node {
	el, d := b.cur, b.curD
	if el == nil {
		return nil
	}
	if v := b.views; v != nil && len(v.gens) == d+1 && v.gens[d] == b.gen {
		return v.path[d] // asked again
	}
	t := el.tree
	vb := b.views
	if vb == nil || int(vb.t.n) >= max(viewBatchRecords, 4*vb.recs[len(vb.recs)-1]) {
		vb = b.newViews(t)
	}
	// The copies of the open elements this batch already has: a copy that
	// is of the open element at its depth has its ancestors' copies above
	// it, so the search goes up from the deepest, over the copies it drops.
	k := min(len(vb.path), d)
	for k > 1 && vb.gens[k-1] != b.gens[k-1] {
		k--
		b.viewScans++
	}
	vb.path, vb.gens, vb.recs = vb.path[:k], vb.gens[:k], vb.recs[:k]
	anc := make([]*Node, d-k)
	p := el
	for i := len(anc) - 1; i >= 0; i-- {
		p = p.Parent()
		anc[i] = p
	}
	for i, a := range anc {
		vb.push(a, b.ords[k+i], b.gens[k+i])
		b.viewCopies += 1 + a.NumAttrs()
	}
	vb.push(el, b.ordOf(el), b.gen)
	return vb.path[d]
}

// newViews starts a batch with a copy of t's document node.
func (b *burster) newViews(t *Tree) *viewBatch {
	c := &Tree{}
	vb := &viewBatch{t: c, o: c.setOrigin(b.stream(t))}
	c.Root = c.newRoot(KindDocument)
	r := c.Root
	r.flags |= fShallow | fOrigin
	src := t.Root
	if src.flags&fBase != 0 {
		r.setOwnBase(t.bases[src.self])
	}
	if u := t.docURIs[src.self]; u != "" {
		r.SetDocumentURI(u)
	}
	c.DocType, c.XMLVersion = t.DocType, "1.0"
	if b.v11 {
		c.XMLVersion = "1.1"
	}
	if s := t.source; s != nil {
		c.source.externalSubset = s.externalSubset
	}
	vb.o.add(0, 0)
	vb.path, vb.gens, vb.recs = append(vb.path, r), append(vb.gens, 0), append(vb.recs, 1)
	b.views = vb
	return vb
}

// push appends a shallow copy of s, whose ordinal is ord, under the batch's
// deepest copy, and makes it the deepest.
func (vb *viewBatch) push(s *Node, ord, gen uint64) {
	c := vb.path[len(vb.path)-1].appendChild(KindElement)
	vb.o.add(c.self, ord)
	ct := c.tree
	c.name = ct.intern(s.Name())
	c.flags |= fShallow | fOrigin
	if f := s.frame(); len(f) > 0 {
		c.setFrame(f)
	}
	copyProps(c, s)
	for a := range s.Attrs() {
		d := c.appendAttrFrom(a.Name(), a.Value(), nil)
		d.flags |= fOrigin
		copyProps(d, a)
	}
	vb.path, vb.gens = append(vb.path, c), append(vb.gens, gen)
	vb.recs = append(vb.recs, vb.recs[len(vb.recs)-1]+1+s.NumAttrs())
}

// copyProps gives d, a copy of s in another tree, s's own base URI and
// typing.
func copyProps(d, s *Node) {
	if s.flags&fBase != 0 {
		d.setOwnBase(s.tree.bases[s.self])
	}
	if s.flags&fTyped != 0 {
		*d.ownTyping() = *s.typ() // the names are shared, not copied
	}
}

// stream is the origin of what is copied out of the stream whose parse tree
// is t.
func (b *burster) stream(t *Tree) *streamOrigin {
	if b.origin == nil {
		b.origin = &streamOrigin{id: t.ident()}
	}
	return b.origin
}

// ordOf is the ordinal of el, a record of the parse's tree.
func (b *burster) ordOf(el *Node) uint64 { return uint64(el.self) + b.discarded }
