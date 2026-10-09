package xdm

// Tree construction.
//
// A tree is built top-down, in document order, by appending to an open node:
// an element, or a document node, whose subtree is still being written. A
// node is open from the call that made it until something is appended to a
// node that is not it or one of its descendants; after that it is closed,
// and appending to it again is a programming error. Attributes are appended
// to an element before its first child. Nothing is ever inserted before or
// between existing nodes, removed, or moved to another parent: code that
// needs a tree with something changed builds a copy with the change made,
// through the same calls.
//
// What may change after a node is made is its scalar state: SetValue and
// AppendValue (merging adjacent text), SetName, SetBaseURI, the typing
// setters, and the namespace declarations of an element of a tree that is
// not a parsed document.
//
// The order is the one a parser produces and the one the record layout
// keeps: a tree built this way is laid out as a parsed one is.

// AppendElement appends a new element named name as the last child of p and
// returns it, open. It has no base URI of its own; see AppendElementInheriting.
func (p *Node) AppendElement(name QName) *Node {
	c := p.AppendElementInheriting(name)
	if c.inheritedBase() != "" {
		c.setOwnBase("")
	}
	return c
}

// AppendElementInheriting is AppendElement for an element whose base URI is
// its parent's, as a constructed element's is when a sequence constructor
// builds it inside another.
func (p *Node) AppendElementInheriting(name QName) *Node {
	c := p.appendChild(KindElement)
	c.name = p.tree.intern(name)
	return c
}

// AppendText appends a text node holding value as the last child of p. It
// does not merge with a text node already there; see AppendValue.
func (p *Node) AppendText(value string) *Node { return p.appendLeaf(KindText, QName{}, value) }

// AppendComment appends a comment node as the last child of p.
func (p *Node) AppendComment(value string) *Node { return p.appendLeaf(KindComment, QName{}, value) }

// AppendPI appends a processing instruction as the last child of p.
func (p *Node) AppendPI(target, value string) *Node {
	return p.appendLeaf(KindPI, QName{Local: target}, value)
}

func (p *Node) appendLeaf(kind NodeKind, name QName, value string) *Node {
	c := p.appendChild(kind)
	c.name = p.tree.intern(name)
	if value != "" {
		c.v0, c.v1 = p.tree.text.add(value)
	}
	return c
}

// AppendAttr appends an attribute to p, which must be an element with no
// children yet, and returns it. (A document node may carry one too, for a
// caller assembling a tree out of a result sequence that holds a parentless
// attribute; nothing the data model builds does that.)
func (p *Node) AppendAttr(name QName, value string) *Node {
	t := p.tree
	t.openTo(p)
	if p.isLeaf() || p.v1 != noIdx || t.n != p.self+1+p.attrCount() {
		panic("xdm: an attribute must be appended to an element before its children")
	}
	a := t.alloc()
	a.kind = uint8(KindAttribute)
	a.parent = p.self
	a.v1 = 0
	a.name = t.intern(name)
	if value != "" {
		a.v0, a.v1 = t.text.add(value)
	}
	if k := p.attrCount() + 1; k < 0xFFFF && p.flags&fManyAttrs == 0 {
		p.nattr = uint16(k)
	} else {
		if src := t.ownSource(); src.attrCounts == nil {
			src.attrCounts = map[uint32]uint32{}
		}
		t.source.attrCounts[p.self] = k
		p.flags |= fManyAttrs
	}
	return a
}

// AppendCopy appends a deep copy of src as the last child of p (or, for an
// attribute, as an attribute of p) and returns the copy. See Copy for what
// travels.
func (p *Node) AppendCopy(src *Node) *Node {
	// A subtree still being built in p's own tree grows as the copy is
	// appended; the copy stops where src ended when it began.
	limit := src.tree.n
	c := p.AppendShallowCopy(src)
	fillCopy(c, src, nil, limit)
	return c
}

// AppendShallowCopy appends a copy of src without its children, attributes or
// namespace declarations, and returns it open: the start of a copy the caller
// completes, changing what it needs to on the way.
func (p *Node) AppendShallowCopy(src *Node) *Node {
	var c *Node
	switch src.Kind() {
	case KindAttribute:
		c = p.AppendAttr(src.Name(), src.Value())
	case KindElement, KindDocument:
		c = p.appendChild(src.Kind())
		c.name = p.tree.intern(src.Name())
	default:
		c = p.appendLeaf(src.Kind(), src.Name(), src.Value())
	}
	c.SetBaseURI(src.BaseURI())
	c.CopyTypingFrom(src)
	return c
}

// Copy returns a deep copy of n with no parent.
//
// The copy keeps n's name, value, base URI and typing, its namespace
// declarations, its attributes (with their typing) and its children. It is a
// new node: it has its own identity, in a fragment of its own.
func Copy(n *Node) *Node { return CopyPruned(n, nil) }

// CopyPruned is Copy leaving out, with its subtree, every descendant of n for
// which drop reports true.
func CopyPruned(n *Node, drop func(*Node) bool) *Node {
	limit := n.tree.n
	c := ShallowCopy(n)
	fillCopy(c, n, drop, limit)
	return c
}

// ShallowCopy returns a parentless copy of n without its children, attributes
// or namespace declarations: the root of a copy the caller completes.
func ShallowCopy(n *Node) *Node {
	c := NewFragment().NewRoot(n.Kind(), n.Name(), n.Value())
	c.SetBaseURI(n.BaseURI())
	c.CopyTypingFrom(n)
	return c
}

// fillCopy gives c, a shallow copy of src, copies of src's namespace
// declarations, attributes and children. Records at limit and beyond are
// not read: they were appended after the copy began.
func fillCopy(c, src *Node, drop func(*Node) bool, limit uint32) {
	if src.isLeaf() || src.flags&fSide != 0 {
		return
	}
	for prefix, uri := range src.DeclaredNamespaces() {
		c.AddNamespace(prefix, uri)
	}
	for a := range src.Attrs() {
		c.AppendAttr(a.Name(), a.Value()).CopyTypingFrom(a)
	}
	if src.v1 == noIdx {
		return
	}
	t := src.tree
	end := min(t.endOf(src), limit)
	for i := src.firstChildIdx(); i < end; {
		ch := t.rec(i)
		next := t.endOf(ch)
		if drop == nil || !drop(ch) {
			cc := c.AppendShallowCopy(ch)
			fillCopy(cc, ch, drop, limit)
		}
		i = next
	}
}

// RemoveLastChild takes back p's last child, which must be the last subtree
// appended to the tree: a node built and then found not to belong, such as
// an element whose conditional-inclusion test turned out false once its
// attributes were in place. A pointer to the node taken back must not be used
// again: its record is reused.
func (p *Node) RemoveLastChild() {
	t := p.tree
	t.openTo(p)
	last := t.rec(p.v1)
	prev := last.prev
	t.truncate(last.self)
	p.v1 = prev
}

// ReplaceLastChild puts a copy of c in place of p's last child, which must be
// the last subtree appended to the tree: the one place where a built subtree
// may still be exchanged, used to swap a constructed element for its
// validated, typed copy. It returns the copy, which carries c's typing and
// type environments.
func (p *Node) ReplaceLastChild(c *Node) *Node {
	p.RemoveLastChild()
	r := p.AppendCopy(c)
	CopyTypeEnvs(r, c)
	return r
}

// CopyTypeEnvs gives each node of dst, a copy of src, its original's type
// environment, which Copy does not carry.
func CopyTypeEnvs(dst, src *Node) {
	dst.SetTypeEnv(src.TypeEnv())
	for i := range min(dst.NumAttrs(), src.NumAttrs()) {
		dst.AttrAt(i).SetTypeEnv(src.AttrAt(i).TypeEnv())
	}
	d, s := dst.FirstChild(), src.FirstChild()
	for d != nil && s != nil {
		CopyTypeEnvs(d, s)
		d, s = d.NextSibling(), s.NextSibling()
	}
}

// truncate drops the records from index i on.
func (t *Tree) truncate(i uint32) {
	for len(t.open) > 0 && t.open[len(t.open)-1] >= i {
		t.open = t.open[:len(t.open)-1]
	}
	for j := i; j < t.n; j++ {
		r := t.rec(j)
		if r.flags&fBase != 0 {
			delete(t.bases, j)
		}
		if r.flags&fTyped != 0 {
			*r.typ() = nodeTyping{}
		}
		if r.flags&fManyAttrs != 0 {
			if t.source != nil {
				delete(t.source.attrCounts, j)
			}
		}
		r.setOffset(0)
		delete(t.docURIs, j)
		if t.source != nil {
			delete(t.source.foreignPos, j)
		}
	}
	if len(t.side) > 0 {
		t.sideMu.Lock()
		for k := range t.side {
			if k.owner >= i {
				delete(t.side, k)
			}
		}
		t.sideMu.Unlock()
	}
	t.n = i
}

// CopySourceFrom gives t the source context of src: its DTD (see
// CopyDTDFrom), its XML version, and the document text that positions are
// resolved against, so that a node given its original's position with
// CopyPosition reports the same line and column.
func (t *Tree) CopySourceFrom(src *Tree) {
	if t == nil || src == nil {
		return
	}
	t.CopyDTDFrom(src)
	t.XMLVersion = src.XMLVersion
	if s := src.srcText(); s != "" {
		t.ownSource().src = s
	} else if t.source != nil {
		t.source.src = ""
	}
}

// CopyPosition gives dst, a copy of src already appended to a tree, src's
// source position. Copies do not carry positions otherwise: a node a
// transform builds was not parsed from anywhere.
func CopyPosition(dst, src *Node) {
	if src.flags&fSide != 0 || dst.flags&fSide != 0 {
		return
	}
	line, col, ok := src.Position()
	if !ok {
		return
	}
	if _, foreign := src.tree.foreign(src.self); !foreign && dst.tree.srcText() == src.tree.srcText() {
		dst.setOffset(src.offset())
		return
	}
	s := dst.tree.ownSource()
	if s.foreignPos == nil {
		s.foreignPos = map[uint32][2]int32{}
	}
	s.foreignPos[dst.self] = [2]int32{int32(line), int32(col)}
}

// Finalize marks the end of building: every node still open is closed, and
// appending to the tree again is a programming error.
func (t *Tree) Finalize() { t.closeAll() }
