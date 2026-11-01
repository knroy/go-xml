package xdm

import (
	"io"

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
// element names, from the document element down, is path. An error from fn
// stops the parse and is returned.
func ParseBursts(r io.Reader, opts ParseOptions, path []QName, fn func(*Node) error) error {
	if len(path) == 0 {
		panic("xdm: ParseBursts needs a non-empty path")
	}
	opts.bursts = &burster{path: path, fn: fn}
	_, err := parse(r, "", opts)
	return err
}

// burster is ParseBursts' state inside parse.
type burster struct {
	path []QName
	fn   func(*Node) error
	// spine is how many levels of the open chain match path and are kept.
	spine int
	// drop is the depth of the element being built only to be dropped (a
	// burst, or an element off the path), or 0.
	drop int
	// marks[d] is the tree's state once the spine element at depth d (0:
	// the document node) had stored its attributes. Everything stored after
	// it belongs to content that is dropped.
	marks []burstMark
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
	if d == b.spine+1 && b.matches(el, d) {
		if d < len(b.path) {
			b.spine = d
			b.marks = append(b.marks, markOf(el.tree))
			return
		}
	}
	b.drop = d
}

func (b *burster) matches(el *Node, d int) bool {
	n := el.Name()
	return n.URI == b.path[d-1].URI && n.Local == b.path[d-1].Local
}

// end handles the close of el, at depth d, whose parent is now current. It
// delivers a burst and drops whatever is not spine.
func (b *burster) end(el *Node, d int, spaces *spaceTable, names *nameCache) error {
	switch {
	case b.drop == d:
		b.drop = 0
		if d == len(b.path) && d == b.spine+1 && b.matches(el, d) {
			if err := b.deliver(el); err != nil {
				return err
			}
		}
	case b.drop == 0 && d == b.spine && d > 0:
		b.spine--
		b.marks = b.marks[:len(b.marks)-1]
	default:
		return nil
	}
	b.discardLast(el.Parent(), spaces, names)
	return nil
}

// trimLeaves drops the leaves completed directly inside the spine element
// cur, at depth d: text, comments and processing instructions no path step
// selects.
func (b *burster) trimLeaves(cur *Node, d int, spaces *spaceTable, names *nameCache) {
	if b.drop != 0 || d == 0 || d != b.spine {
		return
	}
	for last := cur.LastChild(); last != nil && last.isLeaf(); last = cur.LastChild() {
		b.discardLast(cur, spaces, names)
	}
}

// discardLast removes p's last child and the text, names and namespace
// frames stored since p's spine mark; names is the parse's name cache.
func (b *burster) discardLast(p *Node, spaces *spaceTable, names *nameCache) {
	p.RemoveLastChild()
	m := b.marks[len(b.marks)-1]
	p.tree.text.rewind(m.text)
	rewindNames(p.tree, m.names, names)
	p.tree.rewindFrames(m.frames, m.frameData)
	// The whitespace table remembers ranges in the store, and some of them
	// were just released. Its map is not rebuilt for the next burst.
	*spaces = spaceTable{noMap: true}
}

// deliver hands el to fn in a tree of its own. By now the parse's tree holds
// nothing but the document node, the spine and el (a burst parse does not
// keep the prolog's comments and processing instructions, which every burst
// would copy), which is exactly the burst's tree: it is
// copied in bulk, sharing the names, frames and text the records point into.
func (b *burster) deliver(el *Node) error {
	m := cloneSubtree(el.tree.Root, xdmclone.Options{})
	c := m(el)
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
