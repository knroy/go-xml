package xdm

import (
	"iter"
	"math/bits"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"
)

// The node record.
//
// A Node is a 40-byte record in its tree's record array, and a *Node is a
// pointer into that array: identity, ==, map keys and boxing into an Item
// stay what they were for a pointer to a struct, while the tree is stored as a
// parser lays it out. The array holds the nodes in document order -- each
// element followed by its attributes, then its children -- so a subtree is a
// contiguous range of indices: document order within a tree is an integer
// compare, the descendant axis is a scan, and an element's next sibling is the
// record just past its subtree.
//
// Each record holds one pointer, to its tree. Names are indices into the
// tree's name table, values are ranges of its text store, and the properties
// most nodes do not have -- a base URI that differs from the one inherited,
// typing, a source position -- live in side tables keyed by index. Namespace
// declarations are frames shared by every element that declares the same
// bindings; the namespace nodes the data model shows are made on demand, once
// per element and prefix.
//
// Construction appends, in document order, to a node that is still open (see
// build.go). Nothing is ever inserted, moved or removed, except that the last
// subtree appended may be taken back.
type Node struct {
	tree   *Tree
	kind   uint8
	flags  uint8
	nattr  uint16 // attributes, which follow the element (see attrCount)
	name   uint32 // index into tree.names
	self   uint32 // own index; for a namespace node, its sequence in tree.side
	parent uint32 // parent's index, or noIdx
	end    uint32 // one past the subtree; openEnd while being built; a namespace node's slot
	prev   uint32 // previous sibling's index, or noIdx
	// v0, v1: a leaf's value, as a text store range. An element's
	// namespace frame (0 = none) and last child; a document's last child in
	// v1. A namespace node's frame and binding index.
	v0, v1 uint32
}

const (
	noIdx   = ^uint32(0)
	openEnd = ^uint32(0) - 1

	// The binding index a namespace node holds for the implicit xml binding.
	xmlBinding = ^uint32(0)
)

// Node flags.
const (
	fSide      = 1 << iota // a namespace node made on demand, outside the array
	fBase                  // has a base URI of its own in tree.bases
	fTyped                 // has an entry in tree.typing
	fManyAttrs             // attribute count is in tree.attrCounts
)

// Tree owns the records of one document, or of a fragment: the parentless
// nodes a constructor built, each the root of its own tree in the data model.
type Tree struct {
	Root *Node
	// DocType is the DOCTYPE declaration's text, when the document had one
	// and AllowDOCTYPE permitted it. Empty otherwise.
	//
	// It is retained because the internal subset is the only place a
	// document's own DTD lives, and validating against it needs the text —
	// encoding/xml hands the declaration over as one opaque token and keeps
	// nothing. The dtd package parses it; this package applies only the two
	// declarations whose absence is visible in the data model.
	DocType string
	// XMLVersion is the version the document's XML declaration names: "1.0"
	// or "1.1", and "1.0" when there is no declaration, since that is the
	// version such a document is read as. It is empty for a tree that was
	// not parsed from text, which has no declaration to name one.
	//
	// It is recorded because some consumers are defined for one version
	// only: Canonical XML is not defined for XML 1.1, and package c14n
	// refuses a tree this reports as 1.1.
	XMLVersion string
	// externalSubset is the text of the external DTD subset, and of any
	// parameter-entity module it pulled in, when one was read.
	//
	// It is separate from DocType because DocType is the declaration AS
	// WRITTEN — that is what a caller re-serialising the document needs —
	// while the declarations that govern the document may live in a file the
	// directive merely names. fn:unparsed-entity-uri is the visible case: a
	// document whose NDATA entities are declared externally reports none of
	// them if only the directive is consulted.
	//
	// Empty unless ParseOptions.ExternalEntities permitted the read.
	externalSubset string
	// src is the document text, retained only when the caller asks for
	// positions. It is what makes Position able to count lines.
	src string
	// lineStarts holds the byte offset of each line, built on first use.
	// Resolving a position is then a binary search rather than a scan from
	// the start of the document, which matters when a validator reports
	// thousands of failures over one large file.
	lineStarts []int
	lineOnce   sync.Once

	// id orders nodes of different trees against each other; the spec asks
	// only for a stable order. A document is numbered when it is made. A
	// fragment is numbered the first time its order or identity is asked
	// for, so that constructed nodes order after the documents a transform
	// read before comparing them, as they always have; see ident.
	id atomic.Int64
	// fragment marks a tree with no document node, holding constructed
	// parentless nodes. Tree() answers nil for its nodes, as it did for
	// constructed nodes before trees held them.
	fragment bool
	// frozen marks a parsed document: it is complete and shared, and
	// appending to it is a programming error.
	frozen bool

	chunks [][]Node
	n      uint32 // records in use
	// open is the path of nodes still being built, outermost first: the
	// nodes something may be appended to.
	open []uint32

	names  []QName
	nameIx map[QName]uint32
	text   textStore

	// frames holds the namespace declarations of elements, interned by
	// content, as ranges of frameData; frame 0 is the empty one.
	frames    []frameRange
	frameData []nsBinding
	frameOne  map[nsBinding]uint32 // single-declaration frames
	frameMany map[string]uint32    // the rest, keyed by their text

	// Side tables for the properties few nodes have.
	bases      map[uint32]string // base URIs that differ from the inherited one
	docURIs    map[uint32]string // dm:document-uri of document nodes
	typing     [][]nodeTyping    // by chunk, made on first use
	offsets    [][]int32         // by chunk: source offset + 1, made on first use
	foreignPos map[uint32][2]int32
	attrCounts map[uint32]uint32 // attribute counts past 0xFFFE

	// Namespace nodes, made on demand.
	sideMu sync.Mutex
	side   map[sideKey]*Node
}

type nsBinding struct{ prefix, uri string }

type frameRange struct{ start, n uint32 }

type sideKey struct {
	owner  uint32
	prefix string
}

// nodeTyping is the PSVI a schema assessment (or a DTD, or an XSLT
// validation instruction) recorded on a node. See the typing accessors.
type nodeTyping struct {
	annotation, unionMember, derivedPrimitive, listItem  string
	env                                                  *TypeEnvironment
	isID, isIDREFS, isNilled, noTypedValue, mixedContent bool
}

// --- Records ----------------------------------------------------------------

// Chunk k holds 4<<k records up to 512, then 512 each, so a small tree stays
// small and a large one allocates in 20 KB steps. Records never move.
//
// The cap keeps every chunk a small-object allocation (at most 32 KB): a
// larger one is allocated and freed as whole pages, which the runtime hands
// back to the OS and faults in again, and on macOS that round trip costs more
// than the parse saves.
const (
	chunkFirst   = 4
	chunkMaxLog  = 7 // 4<<7 = 512
	chunkMax     = chunkFirst << chunkMaxLog
	growingTotal = chunkFirst * (1<<(chunkMaxLog+1) - 1) // records in the growing chunks
)

func chunkLen(k int) int {
	if k > chunkMaxLog {
		return chunkMax
	}
	return chunkFirst << k
}

func chunkOf(i uint32) (k int, off uint32) {
	if i < growingTotal {
		k = bits.Len32(i/chunkFirst+1) - 1
		return k, i - chunkFirst*(1<<k-1)
	}
	j := i - growingTotal
	return chunkMaxLog + 1 + int(j/chunkMax), j % chunkMax
}

// rec returns the record at index i.
func (t *Tree) rec(i uint32) *Node {
	k, off := chunkOf(i)
	return &t.chunks[k][off]
}

// alloc appends a record and returns it.
func (t *Tree) alloc() *Node {
	i := t.n
	k, off := chunkOf(i)
	if k == len(t.chunks) {
		t.chunks = append(t.chunks, make([]Node, chunkLen(k)))
	}
	t.n++
	r := &t.chunks[k][off]
	*r = Node{tree: t, self: i, parent: noIdx, prev: noIdx, end: i + 1, v1: noIdx}
	return r
}

// endOf is one past the last record of n's subtree, also while it is being
// built.
func (t *Tree) endOf(n *Node) uint32 {
	if n.end == openEnd {
		return t.n
	}
	return n.end
}

// intern returns the name table index of q.
func (t *Tree) intern(q QName) uint32 {
	if q == (QName{}) {
		return 0
	}
	// A small tree -- a constructed fragment, mostly -- has a handful of
	// names, and finding one by scanning costs less than building a map.
	if t.nameIx == nil {
		for i, x := range t.names {
			if x == q {
				return uint32(i)
			}
		}
		if t.names == nil {
			t.names = make([]QName, 1, 4)
		}
		i := uint32(len(t.names))
		t.names = append(t.names, q)
		if len(t.names) > smallNames {
			t.nameIx = make(map[QName]uint32, 2*len(t.names))
			for j, x := range t.names[1:] {
				t.nameIx[x] = uint32(j + 1)
			}
		}
		return i
	}
	if i, ok := t.nameIx[q]; ok {
		return i
	}
	i := uint32(len(t.names))
	t.names = append(t.names, q)
	t.nameIx[q] = i
	return i
}

// smallNames is how many names a tree holds before it indexes them.
const smallNames = 8

// nextTreeID hands out tree identifiers. Trees created concurrently may
// interleave, which is fine: the spec requires only a stable order, and each
// tree's id is fixed once assigned.
var nextTreeID = newCounter()

// NewTree creates an empty tree with a document node as its root, open for
// appending.
func NewTree() *Tree {
	t := &Tree{}
	t.id.Store(int64(nextTreeID()))
	t.Root = t.newRoot(KindDocument)
	return t
}

// NewFragment creates a tree for parentless constructed nodes: each NewRoot
// starts a new one, closing whatever was being built before it.
func NewFragment() *Tree {
	return &Tree{fragment: true}
}

// ident returns t's number, giving a fragment one on first use.
func (t *Tree) ident() int64 {
	if id := t.id.Load(); id != 0 {
		return id
	}
	t.id.CompareAndSwap(0, int64(nextTreeID()))
	return t.id.Load()
}

// NewRoot appends a parentless node to the fragment t and returns it, open
// when it is an element or a document node.
func (t *Tree) NewRoot(kind NodeKind, name QName, value string) *Node {
	r := t.newRoot(kind)
	r.name = t.intern(name)
	if value != "" {
		r.v0, r.v1 = t.text.add(value)
	}
	return r
}

func (t *Tree) newRoot(kind NodeKind) *Node {
	if t.frozen {
		panic("xdm: appending to a parsed document")
	}
	t.closeAll()
	r := t.alloc()
	r.kind = uint8(kind)
	if kind == KindElement || kind == KindDocument {
		r.end = openEnd
		t.open = append(t.open, r.self)
	} else {
		r.v1 = 0
	}
	return r
}

// NewNode returns a parentless node of the given kind, name and value, in a
// fragment of its own. An element or document node is open: a tree is grown
// from it with the Append calls.
func NewNode(kind NodeKind, name QName, value string) *Node {
	return NewFragment().NewRoot(kind, name, value)
}

// closeAll closes every node still being built.
func (t *Tree) closeAll() {
	for _, i := range t.open {
		t.rec(i).end = t.n
	}
	t.open = t.open[:0]
}

// closeLast closes the innermost node being built.
func (t *Tree) closeLast() {
	k := len(t.open) - 1
	t.rec(t.open[k]).end = t.n
	t.open = t.open[:k]
}

// openTo closes the nodes being built inside p, which must itself be open.
func (t *Tree) openTo(p *Node) {
	if t.frozen {
		panic("xdm: appending to a parsed document")
	}
	for k := len(t.open) - 1; k >= 0; k-- {
		i := t.open[k]
		if i == p.self && p.flags&fSide == 0 {
			t.open = t.open[:k+1]
			return
		}
		t.rec(i).end = t.n
	}
	t.open = t.open[:0]
	panic("xdm: appending to a node that is no longer being built")
}

// appendChild appends a record of the given kind as p's last child.
func (p *Node) appendChild(kind NodeKind) *Node {
	t := p.tree
	t.openTo(p)
	c := t.alloc()
	c.kind = uint8(kind)
	c.parent = p.self
	c.prev = p.v1
	p.v1 = c.self
	if kind == KindElement || kind == KindDocument {
		c.end = openEnd
		t.open = append(t.open, c.self)
	} else {
		c.v1 = 0
	}
	return c
}

// --- Accessors ---------------------------------------------------------------
//
// Code outside this package reads a node through these methods and never
// through its fields. The children, attributes and namespace nodes of an
// element are reached by iterator, by sibling link, or by count and index; a
// slice of them does not exist to be handed out.

// Kind returns n's node kind.
func (n *Node) Kind() NodeKind { return NodeKind(n.kind) }

// Name returns n's name: an element's or attribute's expanded QName, a
// processing instruction's target, a namespace node's prefix as Local. Other
// kinds have the zero QName.
func (n *Node) Name() QName {
	if n.flags&fSide != 0 {
		return QName{Local: n.binding().prefix}
	}
	if n.name == 0 {
		return QName{}
	}
	return n.tree.names[n.name]
}

// Value is the text content of a text, comment, processing-instruction or
// attribute node, and the namespace URI of a namespace node. Element and
// document nodes derive their string value from descendants; see
// StringValue.
func (n *Node) Value() string {
	switch {
	case n.flags&fSide != 0:
		// The declaration the node was made from, while its element still
		// holds it; the declarations of an element still being built can
		// change after its namespace nodes are made, and then the binding is
		// looked up afresh.
		if n.v1 == xmlBinding {
			return NSXML
		}
		if n.prev != noIdx && n.tree.rec(n.prev).v0 == n.v0 {
			return n.binding().uri
		}
		return n.tree.rec(n.parent).boundURI(n.binding().prefix)
	case n.kind == uint8(KindElement) || n.kind == uint8(KindDocument):
		return ""
	}
	return n.tree.text.str(n.v0, n.v1)
}

// binding is the declaration a namespace node made on demand stands for.
func (n *Node) binding() nsBinding {
	if n.v1 == xmlBinding {
		return nsBinding{"xml", NSXML}
	}
	return n.tree.frameAt(n.v0)[n.v1]
}

// frameAt returns frame i's declarations.
func (t *Tree) frameAt(i uint32) []nsBinding {
	if i == 0 {
		return nil
	}
	r := t.frames[i]
	return t.frameData[r.start : r.start+r.n : r.start+r.n]
}

// Parent returns n's parent, or nil for a root. An attribute's or namespace
// node's parent is its element.
func (n *Node) Parent() *Node {
	if n.parent == noIdx {
		return nil
	}
	return n.tree.rec(n.parent)
}

// attrCount is the number of attributes of n.
func (n *Node) attrCount() uint32 {
	if n.flags&fManyAttrs != 0 {
		return n.tree.attrCounts[n.self]
	}
	return uint32(n.nattr)
}

// firstChildIdx is the index n's first child has, if it has one.
func (n *Node) firstChildIdx() uint32 { return n.self + 1 + n.attrCount() }

// Children iterates over n's children in document order. Attributes and
// namespace nodes are not children.
func (n *Node) Children() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		if n.v1 == noIdx || n.flags&fSide != 0 || n.isLeaf() {
			return
		}
		t := n.tree
		end := t.endOf(n)
		for i := n.firstChildIdx(); i < end; {
			c := t.rec(i)
			if !yield(c) {
				return
			}
			i = t.endOf(c)
		}
	}
}

func (n *Node) isLeaf() bool {
	return n.kind != uint8(KindElement) && n.kind != uint8(KindDocument)
}

// Attrs iterates over n's attributes in the order they were added.
func (n *Node) Attrs() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		if n.isLeaf() || n.flags&fSide != 0 {
			return
		}
		t := n.tree
		for i, k := n.self+1, n.attrCount(); k > 0; i, k = i+1, k-1 {
			if !yield(t.rec(i)) {
				return
			}
		}
	}
}

// NumChildren returns the number of children of n. It walks them.
func (n *Node) NumChildren() int {
	k := 0
	for range n.Children() {
		k++
	}
	return k
}

// ChildAt returns the i'th child of n, counting from zero. It walks the
// children before it, so a loop over all of them belongs with Children or
// NextSibling. It panics if i is out of range, as indexing a slice does.
func (n *Node) ChildAt(i int) *Node {
	k := 0
	for c := range n.Children() {
		if k == i {
			return c
		}
		k++
	}
	panic("xdm: ChildAt index out of range")
}

// FirstChild returns the first child of n, or nil if it has none.
func (n *Node) FirstChild() *Node {
	if n.v1 == noIdx || n.flags&fSide != 0 || n.isLeaf() {
		return nil
	}
	return n.tree.rec(n.firstChildIdx())
}

// LastChild returns the last child of n, or nil if it has none.
func (n *Node) LastChild() *Node {
	if n.v1 == noIdx || n.flags&fSide != 0 || n.isLeaf() {
		return nil
	}
	return n.tree.rec(n.v1)
}

// NextSibling returns the child of n's parent that follows n, or nil when n
// is the last child, has no parent, or is an attribute or namespace node.
func (n *Node) NextSibling() *Node {
	if n.parent == noIdx || n.kind == uint8(KindAttribute) || n.flags&fSide != 0 || n.end == openEnd {
		return nil
	}
	t := n.tree
	if n.end < t.endOf(t.rec(n.parent)) {
		return t.rec(n.end)
	}
	return nil
}

// PrevSibling returns the child of n's parent that precedes n, or nil when n
// is the first child, has no parent, or is an attribute or namespace node.
func (n *Node) PrevSibling() *Node {
	if n.prev == noIdx || n.flags&fSide != 0 || n.kind == uint8(KindAttribute) {
		return nil
	}
	return n.tree.rec(n.prev)
}

// Descendants iterates over n's descendants in document order: children,
// their children, and so on. Attributes and namespace nodes are not
// descendants.
func (n *Node) Descendants() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		if n.v1 == noIdx || n.flags&fSide != 0 || n.isLeaf() {
			return
		}
		t := n.tree
		end := t.endOf(n)
		for i := n.firstChildIdx(); i < end; i++ {
			c := t.rec(i)
			if c.kind == uint8(KindAttribute) {
				continue
			}
			if !yield(c) {
				return
			}
		}
	}
}

// NumAttrs returns the number of attributes of n.
func (n *Node) NumAttrs() int {
	if n.isLeaf() || n.flags&fSide != 0 {
		return 0
	}
	return int(n.attrCount())
}

// AttrAt returns the i'th attribute of n, in the order they were added.
func (n *Node) AttrAt(i int) *Node {
	if i < 0 || i >= n.NumAttrs() {
		panic("xdm: AttrAt index out of range")
	}
	return n.tree.rec(n.self + 1 + uint32(i))
}

// frame is the namespace declarations held on n itself.
func (n *Node) frame() []nsBinding {
	if n.kind != uint8(KindElement) || n.flags&fSide != 0 || n.v0 == 0 {
		return nil
	}
	return n.tree.frameAt(n.v0)
}

// NumNamespaceDecls returns the number of namespace declarations held on n
// itself.
func (n *Node) NumNamespaceDecls() int { return len(n.frame()) }

// NamespaceDeclAt returns the namespace node for the i'th declaration held on
// n itself. It is the node the namespace axis gives for that prefix.
func (n *Node) NamespaceDeclAt(i int) *Node {
	return n.sideNode(n.v0, uint32(i), n.self)
}

// NamespaceDecls iterates over the namespace nodes for the declarations held
// on n itself. On a parsed element these are its declarations; the namespace
// axis, which also has the inherited bindings, is NamespaceNodes.
func (n *Node) NamespaceDecls() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		for i := range n.frame() {
			if !yield(n.NamespaceDeclAt(i)) {
				return
			}
		}
	}
}

// DeclaredNamespaces iterates over the namespace declarations held on n
// itself as prefix and URI, without making a namespace node for each. An
// empty URI undeclares the prefix.
func (n *Node) DeclaredNamespaces() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for _, b := range n.frame() {
			if !yield(b.prefix, b.uri) {
				return
			}
		}
	}
}

// BaseURI returns n's own resolved base URI, used by fn:base-uri, fn:doc and
// fn:document.
//
// It is stored only where it differs from the inherited one: an element
// inherits its parent's, and every other node has none of its own.
func (n *Node) BaseURI() string {
	for c := n; ; {
		if c.flags&fBase != 0 {
			return c.tree.bases[c.self]
		}
		if c.kind != uint8(KindElement) || c.parent == noIdx || c.flags&fSide != 0 {
			return ""
		}
		c = c.tree.rec(c.parent)
	}
}

// inheritedBase is the base URI n has when it has none of its own.
func (n *Node) inheritedBase() string {
	if n.kind != uint8(KindElement) || n.parent == noIdx || n.flags&fSide != 0 {
		return ""
	}
	return n.tree.rec(n.parent).BaseURI()
}

// SetBaseURI sets n's own base URI, leaving its descendants as they are: a
// child that inherited the old one keeps it.
func (n *Node) SetBaseURI(base string) {
	if n.flags&fSide != 0 {
		return
	}
	old := n.BaseURI()
	if base == old {
		return
	}
	t := n.tree
	// The children that inherited the old base keep it.
	for c := range n.Children() {
		if c.kind == uint8(KindElement) && c.flags&fBase == 0 && old != "" {
			c.setOwnBase(old)
		}
	}
	if base == n.inheritedBase() {
		if n.flags&fBase != 0 {
			delete(t.bases, n.self)
			n.flags &^= fBase
		}
		return
	}
	n.setOwnBase(base)
}

func (n *Node) setOwnBase(base string) {
	t := n.tree
	if t.bases == nil {
		t.bases = map[uint32]string{}
	}
	t.bases[n.self] = base
	n.flags |= fBase
}

// DocumentURI returns dm:document-uri: set only on a document node retrieved
// by that URI, empty otherwise.
func (n *Node) DocumentURI() string {
	if n.kind != uint8(KindDocument) || n.flags&fSide != 0 {
		return ""
	}
	return n.tree.docURIs[n.self]
}

// SetDocumentURI sets dm:document-uri on a document node. Set it only for a
// document fetched by that URI and registered where fn:doc finds it, so that
// "doc(document-uri($d)) is $d" holds.
func (n *Node) SetDocumentURI(uri string) {
	t := n.tree
	if uri == "" {
		delete(t.docURIs, n.self)
		return
	}
	if t.docURIs == nil {
		t.docURIs = map[uint32]string{}
	}
	t.docURIs[n.self] = uri
}

// SetName renames n.
func (n *Node) SetName(name QName) {
	if n.flags&fSide == 0 {
		n.name = n.tree.intern(name)
	}
}

// SetValue sets the string value n holds: the text of a text, comment or
// processing-instruction node, an attribute's value. A namespace node's URI
// is its declaration's and is not set here.
func (n *Node) SetValue(v string) {
	if n.isLeaf() && n.flags&fSide == 0 {
		n.v0, n.v1 = n.tree.text.add(v)
	}
}

// AppendValue appends s to the value of n. Repeated appends to the text node
// a builder is still extending cost what the appended bytes do, not the
// whole value each time.
func (n *Node) AppendValue(s string) {
	if s == "" || !n.isLeaf() || n.flags&fSide != 0 {
		return
	}
	n.v0, n.v1 = n.tree.text.extend(n.v0, n.v1, s)
}

// --- Typing -------------------------------------------------------------------

// typ returns n's typing, or the zero value.
func (n *Node) typ() *nodeTyping {
	if n.flags&fTyped == 0 {
		return &zeroTyping
	}
	k, off := chunkOf(n.self)
	return &n.tree.typing[k][off]
}

var zeroTyping nodeTyping

// ownTyping returns n's typing entry, making it.
func (n *Node) ownTyping() *nodeTyping {
	if n.flags&fSide != 0 {
		return new(nodeTyping)
	}
	t := n.tree
	k, off := chunkOf(n.self)
	for len(t.typing) <= k {
		t.typing = append(t.typing, nil)
	}
	if t.typing[k] == nil {
		t.typing[k] = make([]nodeTyping, chunkLen(k))
	}
	n.flags |= fTyped
	return &t.typing[k][off]
}

// TypeAnnotation returns n's type annotation name (see AnnotationName), or
// "" when n is untyped.
func (n *Node) TypeAnnotation() string { return n.typ().annotation }

// UnionMember returns the member type of a union that accepted n's value.
func (n *Node) UnionMember() string { return n.typ().unionMember }

// DerivedPrimitive returns the built-in type n's annotation erases to, as
// recorded at assessment, or "" when not recorded.
func (n *Node) DerivedPrimitive() string { return n.typ().derivedPrimitive }

// ListItem returns the item type when n's annotation is a list type, as
// recorded at assessment, or "".
func (n *Node) ListItem() string { return n.typ().listItem }

// IsID reports the dm:is-id property.
func (n *Node) IsID() bool { return n.typ().isID }

// IsIDREFS reports the dm:is-idrefs property.
func (n *Node) IsIDREFS() bool { return n.typ().isIDREFS }

// IsNilled reports the dm:nilled property.
func (n *Node) IsNilled() bool { return n.typ().isNilled }

// NoTypedValue reports that n's typed value is absent (element-only or empty
// content), so fn:data on it is FOTY0012.
func (n *Node) NoTypedValue() bool { return n.typ().noTypedValue }

// MixedContent reports that n was validated against a mixed complex type
// other than xs:anyType.
func (n *Node) MixedContent() bool { return n.typ().mixedContent }

// TypeEnv returns the type environment of the schema that validated this node,
// or nil when no schema did.
//
// Unlike TypeEnvOf this does NOT fall back to the global table: it reports
// what the node actually carries, which is what a test asserting that the
// stamping happened needs to see.
func (n *Node) TypeEnv() *TypeEnvironment {
	if n == nil {
		return nil
	}
	return n.typ().env
}

// SetTypeEnv records the type environment of the schema whose assessment
// produced this node's annotation.
//
// The xsd package calls it as it annotates, so that later by-name questions
// about the node's type reach the definitions that schema actually made rather
// than whatever a later, unrelated schema registered under the same name.
func (n *Node) SetTypeEnv(e *TypeEnvironment) {
	if n == nil || (e == nil && n.flags&fTyped == 0) {
		return
	}
	n.ownTyping().env = e
}

// --- Namespaces -----------------------------------------------------------

// setFrame gives n the declarations ns, interned. ns is not kept: a caller
// may reuse it.
func (n *Node) setFrame(ns []nsBinding) { n.v0 = n.tree.internFrame(ns) }

func (t *Tree) internFrame(ns []nsBinding) uint32 {
	if len(ns) == 0 {
		return 0
	}
	if t.frames == nil {
		t.frames = []frameRange{{}}
	}
	if len(t.frames) <= smallFrames {
		for i := 1; i < len(t.frames); i++ {
			if f := t.frameAt(uint32(i)); len(f) == len(ns) && sameBindings(f, ns) {
				return uint32(i)
			}
		}
		return t.addFrame(ns)
	}
	if len(t.frames) == smallFrames+1 && t.frameOne == nil && t.frameMany == nil {
		// Past the scan's reach: index what is there.
		for i := 1; i < len(t.frames); i++ {
			t.indexFrame(uint32(i))
		}
	}
	if len(ns) == 1 {
		if i, ok := t.frameOne[ns[0]]; ok {
			return i
		}
		i := t.addFrame(ns)
		if t.frameOne == nil {
			t.frameOne = map[nsBinding]uint32{}
		}
		t.frameOne[ns[0]] = i
		return i
	}
	var buf [256]byte
	key := buf[:0]
	for _, b := range ns {
		key = append(append(append(append(key, b.prefix...), 0), b.uri...), 0)
	}
	if i, ok := t.frameMany[string(key)]; ok {
		return i
	}
	i := t.addFrame(ns)
	if t.frameMany == nil {
		t.frameMany = map[string]uint32{}
	}
	t.frameMany[string(key)] = i
	return i
}

// smallFrames is how many frames a tree holds before it indexes them.
const smallFrames = 8

func sameBindings(a, b []nsBinding) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// indexFrame records frame i in the frame indexes.
func (t *Tree) indexFrame(i uint32) {
	f := t.frameAt(i)
	if len(f) == 1 {
		if t.frameOne == nil {
			t.frameOne = map[nsBinding]uint32{}
		}
		if _, dup := t.frameOne[f[0]]; !dup {
			t.frameOne[f[0]] = i
		}
		return
	}
	if t.frameMany == nil {
		t.frameMany = map[string]uint32{}
	}
	k := frameKey(f)
	if _, dup := t.frameMany[k]; !dup {
		t.frameMany[k] = i
	}
}

func frameKey(ns []nsBinding) string {
	var buf [256]byte
	key := buf[:0]
	for _, b := range ns {
		key = append(append(append(append(key, b.prefix...), 0), b.uri...), 0)
	}
	return string(key)
}

func (t *Tree) addFrame(ns []nsBinding) uint32 {
	i := uint32(len(t.frames))
	t.frames = append(t.frames, frameRange{uint32(len(t.frameData)), uint32(len(ns))})
	t.frameData = append(t.frameData, ns...)
	return i
}

// AddNamespace declares prefix as uri on n, an element of a tree that is not
// a parsed document. A namespace declaration is not a record, so it may be
// added after n's content.
func (n *Node) AddNamespace(prefix, uri string) {
	if n.kind != uint8(KindElement) || n.flags&fSide != 0 {
		return
	}
	if n.tree.frozen {
		panic("xdm: declaring a namespace in a parsed document")
	}
	old := n.frame()
	ns := make([]nsBinding, len(old), len(old)+1)
	copy(ns, old)
	n.setFrame(append(ns, nsBinding{prefix, uri}))
}

// SetNamespaceDecl binds prefix to uri on n: the declaration n already holds
// for prefix is changed in place, and otherwise one is added.
func (n *Node) SetNamespaceDecl(prefix, uri string) {
	old := n.frame()
	for i, b := range old {
		if b.prefix == prefix {
			if b.uri == uri {
				return
			}
			ns := append([]nsBinding(nil), old...)
			ns[i].uri = uri
			n.setFrame(ns)
			return
		}
	}
	n.AddNamespace(prefix, uri)
}

// RemoveNamespaceDecls drops the namespace declarations held on n for which
// drop reports true.
func (n *Node) RemoveNamespaceDecls(drop func(prefix, uri string) bool) {
	old := n.frame()
	if len(old) == 0 {
		return
	}
	if n.tree.frozen {
		panic("xdm: changing namespaces in a parsed document")
	}
	var kept []nsBinding
	for _, b := range old {
		if !drop(b.prefix, b.uri) {
			kept = append(kept, b)
		}
	}
	n.setFrame(kept)
}

// boundURI is the URI prefix is bound to on n as InScopeNamespaces reads it:
// the innermost element declaring it, the last of its declarations; "" when
// it is undeclared or unbound.
func (n *Node) boundURI(prefix string) string {
	if prefix == "xml" {
		return NSXML
	}
	for cur := n; cur != nil; cur = cur.Parent() {
		f := cur.frame()
		for i := len(f) - 1; i >= 0; i-- {
			if f[i].prefix == prefix {
				return f[i].uri
			}
		}
	}
	return ""
}

// LookupPrefix resolves a namespace prefix by walking up from n, the
// innermost declaration winning. The xml prefix is always bound.
func (n *Node) LookupPrefix(prefix string) (string, bool) {
	if prefix == "xml" {
		return NSXML, true
	}
	for cur := n; cur != nil; cur = cur.Parent() {
		if cur.kind != uint8(KindElement) || cur.flags&fSide != 0 {
			continue
		}
		for _, b := range cur.frame() {
			if b.prefix == prefix {
				if b.uri == "" && prefix != "" {
					return "", false
				}
				return b.uri, true
			}
		}
	}
	if prefix == "" {
		return "", true // no default namespace in scope: absent name
	}
	return "", false
}

// InScopeNamespaces returns every prefix-to-URI binding visible at n, with
// inner declarations shadowing outer ones. Used when copying elements and when
// resolving QNames in stylesheet attribute values.
func (n *Node) InScopeNamespaces() map[string]string {
	// Every element has the xml prefix bound, whether or not the document
	// declares it: the binding is fixed by the XML Namespaces specification
	// and, unlike every other prefix, it cannot be undeclared or rebound.
	out := map[string]string{"xml": NSXML}
	var chain []*Node
	for cur := n; cur != nil; cur = cur.Parent() {
		if cur.kind == uint8(KindElement) && cur.flags&fSide == 0 {
			chain = append(chain, cur)
		}
	}
	// Walk outermost-inward so that inner declarations overwrite outer ones.
	for i := len(chain) - 1; i >= 0; i-- {
		for _, b := range chain[i].frame() {
			if b.uri == "" {
				delete(out, b.prefix)
			} else {
				out[b.prefix] = b.uri
			}
		}
	}
	return out
}

// NamespaceNodes iterates over the namespace axis of n: one namespace node
// for every binding in scope on an element, the inherited ones included,
// ordered by prefix. Other kinds have none.
//
// Each node is made the first time it is asked for and is the same node
// every time after, by pointer as well as by Is.
func (n *Node) NamespaceNodes() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		if n.kind != uint8(KindElement) || n.flags&fSide != 0 {
			return
		}
		for _, s := range n.scopeSlots() {
			if !yield(n.sideNode(s.frame, s.index, s.owner)) {
				return
			}
		}
	}
}

// scopeSlot is one in-scope binding of an element: where it is declared.
type scopeSlot struct {
	prefix              string
	frame, index, owner uint32 // owner: the element declaring it
}

// scopeSlots returns n's in-scope bindings ordered by prefix.
func (n *Node) scopeSlots() []scopeSlot {
	var chain []*Node
	for cur := n; cur != nil; cur = cur.Parent() {
		if cur.kind == uint8(KindElement) && cur.flags&fSide == 0 {
			chain = append(chain, cur)
		}
	}
	m := map[string]scopeSlot{"xml": {"xml", 0, xmlBinding, noIdx}}
	for i := len(chain) - 1; i >= 0; i-- {
		f := chain[i].v0
		for j, b := range chain[i].frame() {
			if b.uri == "" {
				delete(m, b.prefix)
			} else {
				m[b.prefix] = scopeSlot{b.prefix, f, uint32(j), chain[i].self}
			}
		}
	}
	out := make([]scopeSlot, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].prefix < out[j].prefix })
	return out
}

// sideNode returns the namespace node of n for the binding at (frame,
// index), making it on first use. Making it is safe from any number of
// goroutines reading one tree.
func (n *Node) sideNode(frame, index, owner uint32) *Node {
	t := n.tree
	var prefix string
	if index == xmlBinding {
		prefix = "xml"
	} else {
		prefix = t.frameAt(frame)[index].prefix
	}
	key := sideKey{n.self, prefix}
	t.sideMu.Lock()
	defer t.sideMu.Unlock()
	if s, ok := t.side[key]; ok {
		return s
	}
	// The slot places the node among n's others, by prefix, for document
	// order; an undeclaring declaration, which has no place on the axis,
	// takes the place its prefix would have.
	slot := uint32(0)
	for _, s := range n.scopeSlots() {
		if s.prefix < prefix {
			slot++
		}
	}
	s := &Node{tree: t, kind: uint8(KindNamespace), flags: fSide,
		self: uint32(len(t.side)), parent: n.self, end: slot, prev: owner,
		v0: frame, v1: index}
	if t.side == nil {
		t.side = map[sideKey]*Node{}
	}
	t.side[key] = s
	return s
}

// --- Identity and order ---------------------------------------------------

// Tree returns the tree n belongs to, or nil for a node a constructor built
// outside any document.
func (n *Node) Tree() *Tree {
	if n.tree.fragment {
		return nil
	}
	return n.tree
}

// orderKey places n on its tree's document order: a namespace node follows
// its element and precedes the element's attributes.
func (n *Node) orderKey() uint64 {
	if n.flags&fSide != 0 {
		return uint64(n.parent)<<16 | uint64(1+n.end)
	}
	return uint64(n.self) << 16
}

// Compare orders two nodes in document order, returning -1, 0 or 1. Nodes in
// different trees are ordered by the order the trees were made, which is
// stable within a transform; parentless constructed nodes of one fragment by
// the order they were made in.
func (n *Node) Compare(o *Node) int {
	if n == o {
		return 0
	}
	if n.tree != o.tree {
		if n.tree.ident() < o.tree.ident() {
			return -1
		}
		return 1
	}
	a, b := n.orderKey(), o.orderKey()
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Is reports whether n and o are the same node, which is what the "is"
// operator asks and what XDM means by node identity. Every node, namespace
// nodes included, exists once, so this is pointer equality.
func (n *Node) Is(o *Node) bool { return n == o }

// IdentityKey is a comparable value equal for two node references exactly when
// Is reports them the same node.
type IdentityKey struct{ ptr *Node }

// Identity returns the key that stands for this node's identity.
func (n *Node) Identity() IdentityKey { return IdentityKey{n} }

// Order returns a number that orders n within its tree. Callers wanting
// relative position must use Compare: this value says nothing across trees.
func (n *Node) Order() int { return int(n.orderKey()) }

// generateID returns the string fn:generate-id() gives n: "N", the tree id,
// "x", and n's position in the tree.
//
// The spec requires it to be stable for a node, distinct between nodes and
// ASCII alphanumeric starting with a letter.
func (n *Node) generateID() string {
	var buf [48]byte
	b := append(buf[:0], 'N')
	b = strconv.AppendInt(b, n.tree.ident(), 10)
	b = append(b, 'x')
	k := uint64(n.self)
	if n.flags&fSide != 0 {
		k = 1<<48 | n.orderKey()
	}
	b = strconv.AppendUint(b, k, 10)
	return string(b)
}

// --- Positions ---------------------------------------------------------------

// Position returns the 1-based line and column where the node starts, and
// false if the position is unknown — the node was built by a transform rather
// than parsed, or the source text was not retained.
func (n *Node) Position() (line, col int, ok bool) {
	if n == nil || n.flags&fSide != 0 {
		return 0, 0, false
	}
	t := n.tree
	if p, found := t.foreignPos[n.self]; found {
		return int(p[0]), int(p[1]), true
	}
	off := n.offset()
	if off <= 0 {
		return 0, 0, false
	}
	return t.positionAt(int(off) - 1)
}

// offset is the stored source offset + 1, or 0.
func (n *Node) offset() int32 {
	k, off := chunkOf(n.self)
	if k >= len(n.tree.offsets) || n.tree.offsets[k] == nil {
		return 0
	}
	return n.tree.offsets[k][off]
}

func (n *Node) setOffset(v int32) {
	if v == 0 && n.offset() == 0 {
		return
	}
	t := n.tree
	k, off := chunkOf(n.self)
	for len(t.offsets) <= k {
		t.offsets = append(t.offsets, nil)
	}
	if t.offsets[k] == nil {
		t.offsets[k] = make([]int32, chunkLen(k))
	}
	t.offsets[k][off] = v
}

// --- Text store ----------------------------------------------------------

// textStore holds the values of a tree's nodes. A value is a range of one
// block, addressed by a 32-bit offset whose top bits name the block. Ordinary
// blocks start small and double, so a small document stays small; a value
// too large for one gets a block of its own. Bytes are only ever appended, so
// a value handed out as a string never changes under its reader.
type textStore struct {
	blocks [][]byte
	cur    int // the ordinary block values are appended to, + 1 (0: none)
	size   int // capacity of the last ordinary block made
}

// Ordinary blocks stop at 32 KB for the reason record chunks do.
const (
	textShift   = 15
	textMask    = 1<<textShift - 1
	textBlock   = 1 << textShift // largest ordinary block
	textOwnFrom = textBlock / 4  // values this long get a block of their own
)

// str returns the value at (off, n).
func (s *textStore) str(off, n uint32) string {
	if n == 0 {
		return ""
	}
	b := s.blocks[off>>textShift]
	return unsafe.String(&b[off&textMask], int(n))
}

// add stores v and returns its range.
func (s *textStore) add(v string) (uint32, uint32) {
	if v == "" {
		return 0, 0
	}
	return s.addBytes(unsafe.Slice(unsafe.StringData(v), len(v)), 0)
}

// addBytes stores b with room for extra bytes after it, which extend uses
// while nothing else has been stored behind it.
func (s *textStore) addBytes(b []byte, extra int) (uint32, uint32) {
	need := len(b) + extra
	if need >= textOwnFrom {
		blk := make([]byte, len(b), need)
		copy(blk, b)
		s.newBlock(blk)
		return uint32(len(s.blocks)-1) << textShift, uint32(len(b))
	}
	if s.cur == 0 || cap(s.blocks[s.cur-1])-len(s.blocks[s.cur-1]) < need {
		s.size = min(max(2*s.size, 64, need), textBlock)
		s.newBlock(make([]byte, 0, s.size))
		s.cur = len(s.blocks)
	}
	k := s.cur - 1
	off := len(s.blocks[k])
	s.blocks[k] = append(s.blocks[k], b...)
	return uint32(k)<<textShift | uint32(off), uint32(len(b))
}

func (s *textStore) newBlock(b []byte) {
	if len(s.blocks) >= 1<<(32-textShift) {
		panic("xdm: a tree's text exceeds the text store's addressable blocks")
	}
	s.blocks = append(s.blocks, b)
}

// extend returns the range of the value at (off, n) with v appended: in place
// when nothing has been stored after the value and its block has room, and
// otherwise copied somewhere with room to grow, so that a value built by many
// appends costs what its bytes do.
func (s *textStore) extend(off, n uint32, v string) (uint32, uint32) {
	if n == 0 {
		return s.add(v)
	}
	k := int(off >> textShift)
	blk := s.blocks[k]
	if int(off&textMask)+int(n) == len(blk) && cap(blk)-len(blk) >= len(v) {
		s.blocks[k] = append(blk, v...)
		return off, n + uint32(len(v))
	}
	total := int(n) + len(v)
	buf := make([]byte, 0, total)
	buf = append(append(buf, s.str(off, n)...), v...)
	return s.addBytes(buf, total)
}
