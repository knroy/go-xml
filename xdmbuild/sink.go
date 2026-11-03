package xdmbuild

// Streamed output: a builder of the principal result that writes what is
// complete as it goes (internal/xdmsink, TransformOptions.Output).
//
// The builder is told of each new child or item before it is appended
// (note). At that point everything before it is complete. The sink keeps a
// stack of frames: builders whose node -- an element, a document node at the
// top of the result, or a level of items (the principal result's sequence,
// the sequence a document node is made from) -- has been opened, its start
// written. The innermost frame on the way to the builder appended to has its
// content written and taken back out of the tree (RemoveLastChild); frames
// that are not on that way have closed, and are finished; and the builder
// itself is opened when its parent is the innermost frame and the Writer can
// decide its start now. So at every append the tree holds the open frames and
// the one node being built, plus whatever a held element (one the Writer
// cannot open, or Hold) keeps until it closes, when it is written whole.
//
// A frame's earlier children are always gone when a new one is appended, so
// a frame has exactly one child, the newest, between appends: the builder's
// own checks (an attribute after a child, the merge of adjacent text) see what
// they saw before.

import (
	"github.com/knroy/go-xml/v2/internal/xdmsink"
	"github.com/knroy/go-xml/v2/internal/xdmtrim"
	"github.com/knroy/go-xml/v2/xdm"
)

// Builder.doc values: the builder is the content of a document node at the
// top of the principal result, built in place or as a sequence.
const (
	docInPlace = 1
	docSeq     = 2
)

type sink struct {
	w            xdmsink.Writer
	root         *Builder
	started, off bool
	frames       []*Builder
	depths       []int
	pending      *Builder // a document level not yet appended to root
	streamed     map[*xdm.Node]bool
	faults       xdm.Sequence // attributes and namespaces at the top of a sequence document
	stamp        int64
	items        int
	cnode        *xdm.Node // the root of the tree last compacted, and its block count after
	cblocks      int
	scratchSeq   xdm.Sequence
	path         []*Builder
}

func init() {
	xdmsink.Attach = func(b any, w xdmsink.Writer) {
		r := b.(*Builder)
		r.sink = &sink{w: w, root: r, streamed: map[*xdm.Node]bool{}}
	}
	xdmsink.Hold = func(b any) {
		if r := b.(*Builder); r.sink != nil {
			r.hold = true
		}
	}
	xdmsink.Document = func(out any, inPlace bool, p any) (any, *xdm.Node) {
		o := out.(*Builder)
		s := o.sink
		if s == nil || s.off || o != s.root || s.pending != nil {
			return nil, nil
		}
		var b *Builder
		var root *xdm.Node
		if inPlace {
			b, root = InPlaceDocument(p.(Policy))
			b.doc = docInPlace
		} else {
			b = New(p.(Policy))
			b.doc = docSeq
		}
		b.sink = s
		s.pending = b
		return b, root
	}
	xdmsink.Finish = func(out any, seq xdm.Sequence) error { return out.(*Builder).sink.finish(seq) }
	xdmsink.Stamp = func(out any) int64 {
		if s := out.(*Builder).sink; s != nil {
			return s.stamp
		}
		return 0
	}
	xdmsink.Items = func(out any) int {
		b := out.(*Builder)
		if s := b.sink; s != nil && b == s.root {
			return s.items
		}
		return len(b.items)
	}
}

// note tells b's sink that a node of kind (a text node holding text) is
// about to be appended to b.
func (b *Builder) note(kind xdm.NodeKind, text string) {
	if b.sink != nil {
		b.sink.before(b, kind, text)
	}
}

// up is the builder b's node belongs to: its parent's, or for a document
// level the principal result's.
func (s *sink) up(b *Builder) *Builder {
	if b.parent != nil {
		return b.parent
	}
	if b.doc != 0 {
		return s.root
	}
	return nil
}

func (s *sink) before(b *Builder, kind xdm.NodeKind, text string) {
	s.stamp++
	if b == s.root {
		s.items++
		s.pending = nil
	}
	if s.off {
		return
	}
	if !s.started && !s.begin() {
		return
	}
	a := b
	for a != nil && a.fr == 0 {
		a = s.up(a)
	}
	if a == nil {
		return
	}
	s.popTo(a)
	if a == b {
		s.flush(a)
		return
	}
	// The builders from a down to b, opened in turn as far as they can be:
	// one, b, in the ordinary case, and more when writing has just begun.
	path := s.path[:0]
	for x := b; x != a; x = s.up(x) {
		path = append(path, x)
	}
	for i := len(path) - 1; i >= 0; i-- {
		x, k, t := path[i], xdm.KindElement, ""
		if x == b {
			k, t = kind, text
		}
		if !s.open(x, k, t) {
			break
		}
		if x == b {
			s.flush(b)
		}
	}
	clear(path)
	s.path = path[:0]
}

// open opens x, whose parent is the innermost frame, if it can be: a child of
// kind (holding text) is about to be appended to it.
func (s *sink) open(x *Builder, kind xdm.NodeKind, text string) bool {
	if x.hold {
		return false
	}
	p := s.frames[len(s.frames)-1]
	depth := -1
	if x.doc == 0 {
		depth = s.depths[len(s.depths)-1] + 1
	}
	switch {
	case p.open != nil:
		if p.open.FirstChild() != x.open {
			return false
		}
	case x.doc != 0:
		s.items_(p, nil)
	default:
		s.items_(p, x.open)
	}
	if !s.w.Open(x.open, depth, kind, text) {
		return false
	}
	s.frames = append(s.frames, x)
	s.depths = append(s.depths, depth)
	x.fr = int32(len(s.frames))
	xdmsink.Opened.Add(1)
	return true
}

// begin starts writing if the Writer can.
func (s *sink) begin() bool {
	seq := append(s.scratchSeq[:0], s.root.items...)
	if p := s.pending; p != nil {
		if p.doc == docInPlace {
			seq = append(seq, p.open)
		} else {
			seq = append(seq, p.items...)
		}
	}
	ok, never := s.w.Begin(seq)
	clear(seq)
	s.scratchSeq = seq[:0]
	if never {
		s.off = true
	}
	if !ok {
		return false
	}
	s.started = true
	s.frames = append(s.frames, s.root)
	s.depths = append(s.depths, -1)
	s.root.fr = 1
	return true
}

// popTo finishes the frames inside a, which have closed.
func (s *sink) popTo(a *Builder) {
	var done *xdm.Node
	for {
		k := len(s.frames) - 1
		t := s.frames[k]
		if t == a {
			return
		}
		s.close(t, done)
		p := s.frames[k-1]
		done = nil
		switch {
		case p.open != nil && p == a:
			p.open.RemoveLastChild()
			s.trim(p.open)
		case p.open != nil:
			done = t.open
		case t.open != nil:
			s.streamed[t.open] = true
		}
	}
}

// close writes the rest of the innermost frame t, all but done (a child
// written as a frame already), and its end, and pops it.
func (s *sink) close(t *Builder, done *xdm.Node) {
	k := len(s.frames) - 1
	d := s.depths[k]
	if t.open != nil {
		for c := range t.open.Children() {
			if c != done {
				s.w.Child(c, d+1)
			}
		}
	} else {
		s.items_(t, nil)
	}
	s.w.Close(t.open, d)
	t.fr = 0
	s.frames[k] = nil
	s.frames, s.depths = s.frames[:k], s.depths[:k]
}

// flush writes the content of t, the innermost frame, which is complete, and
// takes it back.
func (s *sink) flush(t *Builder) {
	if t.open == nil {
		s.items_(t, nil)
		return
	}
	d := s.depths[len(s.depths)-1]
	if t.open.FirstChild() == nil {
		return
	}
	for c := range t.open.Children() {
		s.w.Child(c, d+1)
	}
	for t.open.LastChild() != nil {
		t.open.RemoveLastChild()
	}
	s.trim(t.open)
}

// items_ writes the items of l, a level, up to keep (the item still being
// built, nil for all), and takes them back.
func (s *sink) items_(l *Builder, keep *xdm.Node) {
	n := 0
	for _, it := range l.items {
		nd, isNode := it.(*xdm.Node)
		if isNode && nd == keep {
			break
		}
		n++
		if isNode && s.streamed[nd] {
			delete(s.streamed, nd)
			continue
		}
		if isNode && l.doc == docSeq &&
			(nd.Kind() == xdm.KindAttribute || nd.Kind() == xdm.KindNamespace) {
			s.faults = append(s.faults, nd)
			continue
		}
		s.w.Item(it, l.doc == docSeq, isNode && l.frag != nil && nd.Tree() == l.frag)
	}
	if n == 0 {
		return
	}
	rest := copy(l.items, l.items[n:])
	clear(l.items[rest:])
	l.items = l.items[:rest]
	if rest == 0 {
		l.frag = nil
	}
}

// trim compacts the text of n's tree when the blocks written nodes left
// behind are most of it. (n, not its Tree: a fragment has none to name.)
func (s *sink) trim(n *xdm.Node) {
	k, r := xdmtrim.TextBlocks(n), n.Root()
	if r == s.cnode && k < 2*s.cblocks+32 || r != s.cnode && k < 32 {
		return
	}
	xdmtrim.CompactText(n)
	s.cnode, s.cblocks = r, xdmtrim.TextBlocks(n)
}

// toDocument is ToDocument for b, a sequence document level that has been
// opened: the rest of it is written and its frame popped, and the document
// node returned stands for what was written.
func (s *sink) toDocument(b *Builder) (*xdm.Node, error) {
	s.popTo(b)
	s.close(b, nil)
	faults := s.faults
	s.faults = nil
	for _, n := range faults {
		name := n.(*xdm.Node).Name()
		var err error
		if n.(*xdm.Node).Kind() == xdm.KindAttribute {
			err = b.policy.Err(FaultAttrOnDocument,
				"an attribute node ("+name.Lexical()+") cannot be the content of "+
					"a document node")
		} else {
			err = b.policy.Err(FaultAttrOnDocument,
				"a namespace node ("+name.Local+") cannot be the content of "+
					"a document node")
		}
		if err != nil {
			return nil, err
		}
	}
	b.items, b.frag = nil, nil
	doc := xdm.NewTree().Root
	s.streamed[doc] = true
	return doc, nil
}

// finish writes the rest of the result; seq is the transform's result.
func (s *sink) finish(seq xdm.Sequence) error {
	if !s.started {
		return s.w.End(seq)
	}
	var done *xdm.Node
	for len(s.frames) > 1 {
		t := s.frames[len(s.frames)-1]
		s.close(t, done)
		done = nil
		if p := s.frames[len(s.frames)-1]; p.open != nil {
			done = t.open
		} else if t.open != nil {
			s.streamed[t.open] = true
		}
	}
	s.items_(s.root, nil)
	return s.w.End(nil)
}
