package xdm

import "io"

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
// outside its ancestry, do not exist there. Each burst is a new tree, so
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
	// marks[d] is the text store's state once the spine element at depth d
	// (0: the document node) had stored its attributes. Everything stored
	// after it belongs to content that is dropped.
	marks []textMark
	v11   bool
}

// start classifies the element just built at depth d and opened as el.
func (b *burster) start(el *Node, d int) {
	if b.drop != 0 {
		return
	}
	if d == b.spine+1 && b.matches(el, d) {
		if d < len(b.path) {
			b.spine = d
			b.marks = append(b.marks, el.tree.text.mark())
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
func (b *burster) end(el *Node, d int, spaces *spaceTable) error {
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
	b.discardLast(el.Parent(), spaces)
	return nil
}

// trimLeaves drops the leaves completed directly inside the spine element
// cur, at depth d: text, comments and processing instructions no path step
// selects.
func (b *burster) trimLeaves(cur *Node, d int, spaces *spaceTable) {
	if b.drop != 0 || d == 0 || d != b.spine {
		return
	}
	for last := cur.LastChild(); last != nil && last.isLeaf(); last = cur.LastChild() {
		b.discardLast(cur, spaces)
	}
}

// discardLast removes p's last child and the text stored since p's spine mark.
func (b *burster) discardLast(p *Node, spaces *spaceTable) {
	p.RemoveLastChild()
	p.tree.text.rewind(b.marks[len(b.marks)-1])
	// The whitespace table remembers ranges in the store, and some of them
	// were just rewound.
	*spaces = spaceTable{}
}

// deliver copies el and its ancestry into a tree of its own and hands it to
// fn.
func (b *burster) deliver(el *Node) error {
	src := el.tree
	t := NewTree()
	t.XMLVersion = "1.0"
	if b.v11 {
		t.XMLVersion = "1.1"
	}
	t.Root.SetBaseURI(src.Root.BaseURI())
	t.Root.SetDocumentURI(src.Root.DocumentURI())
	var chain []*Node
	for a := el.Parent(); a != nil && a.Kind() == KindElement; a = a.Parent() {
		chain = append(chain, a)
	}
	p := t.Root
	for i := len(chain) - 1; i >= 0; i-- {
		a := chain[i]
		c := p.AppendShallowCopy(a)
		for prefix, uri := range a.DeclaredNamespaces() {
			c.AddNamespace(prefix, uri)
		}
		for at := range a.Attrs() {
			c.AppendAttr(at.Name(), at.Value()).CopyTypingFrom(at)
		}
		p = c
	}
	c := p.AppendCopy(el)
	t.Finalize()
	return b.fn(c)
}

// textMark is a point in a textStore that rewind returns it to.
type textMark struct {
	blocks   int
	cur      int32
	size     int32
	curLen   int
	hasBlock bool
}

func (s *textStore) mark() textMark {
	m := textMark{blocks: len(s.blocks), cur: s.cur, size: s.size}
	if s.cur > 0 {
		m.curLen = len(s.blocks[s.cur-1])
		m.hasBlock = true
	}
	return m
}

// rewind forgets every value stored since m. Nothing may still read one of
// them: ParseBursts only rewinds over records it has removed, and the trees it
// hands out copy their values (a value long enough to be aliased rather than
// copied lives in a block of its own, which rewind releases and never
// rewrites).
func (s *textStore) rewind(m textMark) {
	for i := m.blocks; i < len(s.blocks); i++ {
		s.blocks[i] = nil
	}
	s.blocks = s.blocks[:m.blocks]
	s.cur, s.size = m.cur, m.size
	if m.hasBlock {
		s.blocks[s.cur-1] = s.blocks[s.cur-1][:m.curLen]
	}
}
