package xdm

import (
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// NodeKind enumerates the seven node kinds of the XDM.
type NodeKind int

const (
	KindDocument NodeKind = iota
	KindElement
	KindAttribute
	KindText
	KindComment
	KindPI
	KindNamespace
)

func (k NodeKind) String() string {
	switch k {
	case KindDocument:
		return "document-node()"
	case KindElement:
		return "element()"
	case KindAttribute:
		return "attribute()"
	case KindText:
		return "text()"
	case KindComment:
		return "comment()"
	case KindPI:
		return "processing-instruction()"
	case KindNamespace:
		return "namespace-node()"
	}
	return "node()"
}

// Node is a node in an XDM tree.
//
// This is a concrete struct rather than an interface. Every node kind shares
// most of its fields, the evaluator switches on Kind rather than dispatching,
// and the axes need to walk parent/sibling links tens of thousands of times
// per document — an interface would add a pointer chase and an indirect call
// to each step for no expressiveness gained.
//
// Trees are built by the parser in this package and are immutable afterwards.
// That immutability is what makes it safe to share one compiled stylesheet
// tree across concurrent transforms.
type Node struct {
	Kind NodeKind
	Name QName

	// Value is the text content for text, comment, PI and attribute nodes,
	// and the namespace URI for namespace nodes. Element and document nodes
	// derive their string value from descendants; see StringValue.
	Value string

	Parent   *Node
	Children []*Node

	// Attrs and Namespaces hold attribute and namespace nodes for elements.
	// They are kept out of Children because the child axis must not return
	// them — a fact that a single mixed slice makes easy to get wrong.
	Attrs      []*Node
	Namespaces []*Node

	// order is the document-order position, assigned in a single pre-order
	// walk when the tree is finalised. Comparing two nodes' document order is
	// then an integer compare rather than a walk to the common ancestor,
	// which matters because union, "except", "intersect", and every
	// path expression that must return nodes in document order sort by it.
	//
	// int32 rather than int: this struct is allocated once per element,
	// attribute and text run, so four bytes saved here is four bytes per
	// node of a document. Two billion nodes is far past what the rest of
	// the design survives, and Order() still returns int so no caller sees
	// the narrower type.
	order int32

	// A byte offset is stored rather than a line and column because it is one
	// int32 rather than two ints on a struct that a large document allocates
	// hundreds of thousands of. Position resolves it on demand, which happens
	// only for the handful of nodes an error actually names.
	//
	// It sits beside order so that the two int32 fields share a single
	// eight-byte word. Separated, each was padded out to its own word and
	// the narrowing bought nothing.
	offset int32

	// tree identifies the containing tree, so that nodes from different
	// documents compare consistently (the spec requires a stable but
	// implementation-defined order between trees).
	tree *Tree

	// BaseURI is the resolved base URI, used by fn:document and fn:doc.
	BaseURI string

	// DocumentURI is the data model's dm:document-uri property, which
	// fn:document-uri returns. It is meaningful only on a document node.
	//
	// It is deliberately NOT the same field as BaseURI, and not derived from
	// it. dm:base-uri and dm:document-uri are separate accessors in the XDM,
	// and the difference is observable: dm:document-uri is the absolute URI a
	// document was RETRIEVED BY, so it is empty for any document that was not
	// retrieved by URI at all — a temporary tree built by xsl:variable, a
	// document node constructed by xsl:document, a tree parsed from a string.
	// Those trees still need a base URI, for fn:base-uri and for resolving a
	// relative reference written inside them, so BaseURI on a temporary tree
	// is set on purpose (xslt/runtime.go does this) and cannot double as the
	// document URI. XPath F&O fn:document-uri: "returns the empty sequence if
	// $arg is not a document node, or if the document node was not retrieved
	// via a URI".
	//
	// The invariant a caller must maintain: set this ONLY when the document
	// was fetched by that URI and registered in the document pool, so that
	// fn:doc of this value returns this same node. Setting it on a tree that
	// fn:doc cannot retrieve would make "doc(document-uri($d)) is $d" false
	// while claiming it should be true. Parse sets it from
	// ParseOptions.DocumentURI, which defaults to empty.
	DocumentURI string

	// TypeAnnotation records a schema type when the document has been
	// validated. Untyped documents leave this empty, and atomisation then
	// yields xs:untypedAtomic, which is the schemaless default.
	//
	// It holds an ANNOTATION NAME, which AnnotationName builds and
	// SplitAnnotationName takes apart: a type in the XML Schema namespace
	// keys under its bare local name ("string", "QName"), and any other type
	// under Clark notation, {uri}local. Producers must go through
	// AnnotationName; consumers comparing against a qualified name must use
	// SplitAnnotationName rather than SplitQName, which would cut a Clark key
	// at the colon inside its URI and yield nonsense without an error.
	//
	// The namespace is load-bearing rather than decorative. This string is
	// the key into a process-global derivation table, so a bare local name
	// let one schema's type displace a built-in of the same name for every
	// later schema in the process — see the commentary on derivedPrimitives.
	TypeAnnotation string

	// UnionMember records which member type of a union simple type actually
	// accepted this node's value, when TypeAnnotation names (or has simple
	// content of) a union.
	//
	// It is separate state from TypeAnnotation because the two facts are
	// different and both are needed. XSD 1.0 §3.14.4 makes member selection a
	// property of the *value*, not of the type: "100" validated against
	// union(my:partNumberType, xs:integer) is an xs:integer while "123-AB" is
	// a my:partNumberType, and the same annotation covers both. Folding the
	// winner into TypeAnnotation would answer "instance of xs:integer" at the
	// cost of "instance of my:partIntegerUnion", which the union's own
	// identity requires; keeping only the union answers the second and loses
	// the first. A node must answer both, so both are recorded.
	//
	// It is an annotation name, like TypeAnnotation, and for the same reason:
	// it is compared against, and walked through, the same registries.
	//
	// Empty for every node whose type is not a union, which is almost all of
	// them, so the common path pays only the field.
	UnionMember string

	// IsID and IsIDREFS are the data model's is-id and is-idrefs properties
	// (XDM §5.2, §6.2). They are deliberately *separate* state from
	// TypeAnnotation rather than being derived from it, because XSLT 2.0
	// §3.5 requires them to survive input-type-annotations="strip": that
	// setting turns every annotation into xs:untyped/xs:untypedAtomic while
	// leaving is-id and is-idrefs exactly as they were. Deriving them from
	// the annotation would lose them at precisely the point the
	// specification says they must be kept, and fn:id/fn:idref — which are
	// defined over these properties, not over the annotation — would then
	// find nothing in a stripped document whose ID attributes happen not to
	// be spelled "id".
	//
	// Two bools rather than one enum: an attribute of a union type can in
	// principle be neither, and nothing in the model makes them exclusive.
	// They are set wherever an annotation is assigned (schema assessment,
	// DTD attribute types) by whoever knows the declared type; a node whose
	// type was never determined leaves both false, which is the correct
	// answer for an unvalidated document.
	IsID     bool
	IsIDREFS bool

	// IsNilled is the data model's dm:nilled property (XDM 5.10): true for an
	// element that a schema assessment found nil, false for every other node.
	//
	// It is separate state from TypeAnnotation, and separate from the xsi:nil
	// attribute, for the same reason IsID is. The property is fixed by the
	// assessment that produced the node and does not follow from what the
	// node looks like afterwards: xsi:nil on an element whose declaration is
	// not nillable is an ERROR rather than a nilled element, and only the
	// validator can tell those apart.
	//
	// Inferring it from "carries an annotation AND has xsi:nil='true'" gets
	// xsl:copy validation="preserve" wrong, which is what validation-1204 is
	// written to catch. That instruction CONSTRUCTS an element and preserves
	// the annotation onto it without assessing anything, so the new element
	// had both halves of that test while nothing had ever assessed it. The
	// property is a fact about an event that either happened or did not, so
	// it is recorded when it happens and copied when the node is.
	//
	// A node whose type was never determined leaves this false, which is the
	// correct answer for an unvalidated document.
	IsNilled bool

	// detachedID numbers a node that roots a tree which was never finalized,
	// assigned on the first cross-tree comparison. Zero means unassigned.
	detachedID int64

	// offset is the byte position where this node starts in the source text,
	// stored one greater than the true offset so that the zero value means
	// "unknown". Nodes are built by a transform in two dozen places with a
	// plain struct literal; if zero meant offset 0, every one of them would
	// silently claim to start at line 1, and a new construction site added
	// later would inherit the same bug without anyone noticing.
	//
}

// Position returns the 1-based line and column where the node starts, and
// false if the position is unknown — the node was built by a transform rather
// than parsed, or the source text was not retained.
func (n *Node) Position() (line, col int, ok bool) {
	if n == nil || n.offset <= 0 || n.tree == nil {
		return 0, 0, false
	}
	return n.tree.positionAt(int(n.offset) - 1)
}

// Tree owns a document and the counter used to assign document order.
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
	// id orders nodes from different trees against each other. The spec
	// requires only that the order be stable within a transform.
	id      int
	counter int32
}

func (n *Node) isItem() {}

// TypeName implements Item.
func (n *Node) TypeName() string {
	if n.TypeAnnotation != "" {
		return n.TypeAnnotation
	}
	return n.Kind.String()
}

// Order returns a number that identifies the node uniquely within the process.
//
// It is the document-order index within the node's own tree, combined with the
// tree's identity so that nodes from two documents cannot collide. Callers
// wanting relative position must use Compare: this value orders nodes within
// one tree but says nothing across trees.
//
// The combination is what fn:generate-id() needs. Returning the bare per-tree
// index gave the same answer to the first node of every document, so a
// stylesheet comparing generated identities across documents — the case
// key-042 in the XSLT suite exists to check — saw distinct nodes as identical.
//
// A tree built by a sequence constructor is never finalized and has no tree of
// its own; those nodes take the identity assigned on demand to their root, the
// same one cross-tree comparison uses, so two parentless elements are also
// distinguished.
func (n *Node) Order() int {
	base := 0
	if n.tree != nil {
		base = n.tree.id
	} else {
		root := n
		for root.Parent != nil {
			root = root.Parent
		}
		base = int(detachedRootID(root)) + detachedIDBias
	}
	// The tree component is shifted well clear of any plausible document
	// size. A document with more than a million nodes would overlap the next
	// tree's range, which costs uniqueness but nothing else: the value is an
	// identity, and Compare — not this — decides order.
	return base*treeIDStride + int(n.order)
}

// treeIDStride separates one tree's identity range from the next.
const treeIDStride = 1 << 20

// detachedIDBias keeps identities handed to unfinalized roots clear of the
// tree ids, which are drawn from a separate counter starting at one.
const detachedIDBias = 1 << 20

// SetSynthesizedOrder places a node the parser did not build into the document
// order of an existing tree, immediately after owner.
//
// The namespace axis is the case this exists for: its nodes are synthesized on
// demand from the in-scope bindings, so they have no order of their own. Left
// at zero they sort before every real node, and — because generate-id() is
// derived from the order — every one of them answers "N0", colliding with each
// other and with the document node.
//
// The offset separates the bindings of one element from each other while
// keeping them all adjacent to their owner. It is deliberately not an attempt
// at a spec-defined position: XPath leaves the relative order of namespace
// nodes implementation-dependent, and what a caller needs is that the order is
// stable and the identities distinct.
func (n *Node) SetSynthesizedOrder(owner *Node, offset int) {
	if owner == nil {
		return
	}
	n.tree = owner.tree
	n.order = owner.order + int32(offset) + 1
}

// Tree returns the containing tree.
func (n *Node) Tree() *Tree { return n.tree }

// Compare orders two nodes in document order, returning -1, 0 or 1. Nodes in
// different trees are ordered by tree id, which is stable within a transform.
func (n *Node) Compare(o *Node) int {
	if n == o {
		return 0
	}
	if n.tree == nil && o.tree == nil {
		// A parentless element built by a sequence constructor is the root of
		// its own tree, but nothing ever calls Finalize on it: it is handed
		// out as a bare item, not wrapped in a Tree. Both nodes then carry
		// tree nil and order zero, and comparing those fields alone declares
		// every such node equal to every other — so a union of a variable
		// holding <a><x/></a><b><x/></b> with its own descendants came out as
		// a, b, then the three x's, instead of interleaved.
		//
		// Walking the parent links answers it without a tree: nodes in the
		// same constructed tree compare by their position in it, and nodes in
		// different ones fall back to a stable identity order.
		return compareDetached(n, o)
	}
	if n.tree != o.tree {
		ni, oi := 0, 0
		if n.tree != nil {
			ni = n.tree.id
		}
		if o.tree != nil {
			oi = o.tree.id
		}
		switch {
		case ni < oi:
			return -1
		case ni > oi:
			return 1
		default:
			return 0
		}
	}
	switch {
	case n.order < o.order:
		return -1
	case n.order > o.order:
		return 1
	}
	return 0
}

// compareDetached orders two nodes that belong to no Tree, by walking their
// ancestry.
//
// The two are in the same constructed tree exactly when their ancestor chains
// end at the same root. In that case the answer is the ordinary XDM rule:
// find the nearest common ancestor and compare the two branches beneath it by
// child index, with attributes and namespaces preceding children, and with an
// ancestor preceding its own descendant. In different trees the spec requires
// only a stable order, and roots that have never been numbered have nothing to
// order them by, so they compare equal — the same answer the tree-id branch
// gives for two untracked trees.
func compareDetached(n, o *Node) int {
	na := ancestorChain(n)
	oa := ancestorChain(o)
	if na[0] != oa[0] {
		// Different constructed trees. The spec asks only for a stable order,
		// but it must also be a *total* one: sort.SliceStable is fed this
		// comparator, and answering "equal" for two roots while answering
		// "before" for a root and the other root's descendant is not an order
		// at all — the sort then leaves the roots bunched ahead of every
		// descendant instead of interleaving them. Numbering each root on
		// first comparison gives a consistent answer that never changes for a
		// given pair.
		switch ra, ro := detachedRootID(na[0]), detachedRootID(oa[0]); {
		case ra < ro:
			return -1
		case ra > ro:
			return 1
		}
		return 0
	}
	// Skip the shared prefix. The chains run root-first, so the first index
	// at which they differ is the pair of siblings to compare.
	i := 0
	for i < len(na) && i < len(oa) && na[i] == oa[i] {
		i++
	}
	if i == len(na) {
		// n is an ancestor of o, and an ancestor precedes its descendants.
		return -1
	}
	if i == len(oa) {
		return 1
	}
	p := na[i-1]
	switch ra, rb := siblingRank(p, na[i]), siblingRank(p, oa[i]); {
	case ra < rb:
		return -1
	case ra > rb:
		return 1
	}
	return 0
}

// detachedRootIDs numbers the roots of trees that were never finalized, so
// that nodes from two of them have a stable total order.
//
// The numbers are handed out on first comparison rather than at construction:
// the vast majority of constructed nodes are never compared across trees, and
// the alternative is a counter increment on every element a transform builds.
var detachedIDNext int64

func detachedRootID(root *Node) int64 {
	// The number lives on the node rather than in a side table so that it
	// dies with the node: a table keyed by *Node would pin every constructed
	// root for the life of the process.
	if id := atomic.LoadInt64(&root.detachedID); id != 0 {
		return id
	}
	id := atomic.AddInt64(&detachedIDNext, 1)
	if !atomic.CompareAndSwapInt64(&root.detachedID, 0, id) {
		return atomic.LoadInt64(&root.detachedID)
	}
	return id
}

// numberDetachedRoot stamps an identity number on the root of the untracked
// tree n belongs to, if it has none yet.
//
// It exists so that the number can be assigned before any comparison, in the
// order the caller holds the nodes in, rather than in the order a sort's
// comparisons happen to reach them. See SortDocumentOrder.
func numberDetachedRoot(n *Node) {
	root := n
	for root.Parent != nil {
		root = root.Parent
	}
	if root.tree != nil {
		return
	}
	detachedRootID(root)
}

// ancestorChain returns n's ancestors root-first, ending with n itself.
func ancestorChain(n *Node) []*Node {
	var up []*Node
	for c := n; c != nil; c = c.Parent {
		up = append(up, c)
	}
	// Reverse in place so the root comes first.
	for i, j := 0, len(up)-1; i < j; i, j = i+1, j-1 {
		up[i], up[j] = up[j], up[i]
	}
	return up
}

// siblingRank gives a node its position among all of p's children, attributes
// and namespace nodes, in document order.
//
// Namespace nodes precede attribute nodes, which precede children — the same
// order Tree.assign lays down, so a detached subtree that is finalized later
// does not change any answer this gave before.
func siblingRank(p, n *Node) int {
	for i, ns := range p.Namespaces {
		if ns == n {
			return i
		}
	}
	base := len(p.Namespaces)
	for i, a := range p.Attrs {
		if a == n {
			return base + i
		}
	}
	base += len(p.Attrs)
	for i, c := range p.Children {
		if c == n {
			return base + i
		}
	}
	return base + len(p.Children)
}

// StringValue returns the node's string value per XDM: the concatenation of
// all descendant text for document and element nodes, and the value itself for
// the leaf kinds.
func (n *Node) StringValue() string {
	switch n.Kind {
	case KindDocument, KindElement:
		var sb strings.Builder
		n.appendText(&sb)
		return sb.String()
	default:
		return n.Value
	}
}

func (n *Node) appendText(sb *strings.Builder) {
	for _, c := range n.Children {
		switch c.Kind {
		case KindText:
			sb.WriteString(c.Value)
		case KindElement:
			c.appendText(sb)
		}
		// Comments and PIs contribute nothing to an ancestor's string value.
	}
}

// Atomize returns the typed value of a node. Without schema validation every
// node atomises to xs:untypedAtomic, which is what makes untyped comparison
// rules apply throughout a schemaless transform.
func (n *Node) Atomize() *Atomic {
	// A comment, processing instruction or namespace node has xs:string for
	// its typed value, not xs:untypedAtomic. XPath 2.0 appendix I.2 says so
	// outright, and gives the consequence: because the value is a string
	// rather than untyped, no implicit conversion applies, so using a PI as
	// an operand of an arithmetic operator is a type error where XPath 1.0
	// would have coerced it. These kinds are never schema-validated, so
	// there is no annotation to consult and the answer does not depend on
	// one.
	switch n.Kind {
	case KindComment, KindPI, KindNamespace:
		return NewString(n.StringValue())
	}

	// A node validated against a schema atomises as its annotated type;
	// one that was not is xs:untypedAtomic, the schemaless default.
	//
	// This is what makes "@length eq count(entry)" work in a schema-aware
	// context: without it the attribute is a string, and comparing a string
	// with an integer is XPTY0004 rather than a comparison. The conversion
	// is deliberately narrow — the numeric, boolean and date types, whose
	// lexical forms this package can already parse — because a type it
	// cannot construct is better left untyped than guessed at.
	if n.TypeAnnotation != "" {
		// xs:QName and xs:NOTATION are handled here rather than in
		// atomicForAnnotation because resolving the prefix needs the
		// node's in-scope namespaces, which a lexical form alone does
		// not carry. That is the whole difference between a QName and
		// the string that spells it.
		//
		// These are the BUILT-INS, which key under their bare local names
		// (see AnnotationName). A schema type that merely shares one of
		// those local names is qualified, does not match here, and takes
		// the derivation walk below instead. That distinction is the point:
		// schema-for-xslt20.xsd declares an xsl:QName that restricts
		// xs:Name and holds no QName value at all, and matching it here
		// atomised it as a QName it is not.
		switch n.TypeAnnotation {
		case "QName", "NOTATION":
			if q, ok := n.resolveQNameValue(); ok {
				return NewQNameValue(q)
			}
			return NewUntypedAtomic(n.StringValue())
		}
		// A union-typed node is atomised from the member that accepted its
		// value, which is the only thing that knows what the value *is*: the
		// union's own derivation chain runs to xs:anySimpleType and stops, so
		// the walk below would return nil and the node would atomise to
		// xs:untypedAtomic. The value keeps the annotation as its derived name
		// so the union's identity survives, and carries the member alongside.
		if a := atomicForUnionAnnotation(n); a != nil {
			return a
		}
		if a := atomicForAnnotation(n.TypeAnnotation, n.StringValue()); a != nil {
			// The annotation is kept on the value as its derived type, so
			// that "instance of" can answer for a user-defined type. Without
			// it the value knows only the primitive it erased to, and every
			// question about the schema type it was validated against
			// answered false.
			return a.WithDerived(n.TypeAnnotation)
		}
		// A user-defined type this package cannot construct still atomises:
		// it is the primitive its schema type derives from, and the schema
		// name is what "instance of" needs. Returning a bare untypedAtomic
		// discarded the annotation entirely.
		if a := atomicForDerivedAnnotation(n); a != nil {
			return a
		}
	}
	return NewUntypedAtomic(n.StringValue())
}

// AtomizeList returns the typed value of a node whose annotation is a list
// type, as one atomic value per whitespace-separated token.
//
// The second result reports whether the annotation is in fact a list type; a
// caller that gets false must fall back to Atomize, which yields the single
// value that every non-list node has.
//
// Only the three built-in list types are recognised here. A user-defined list
// type is registered by the schema layer with its item type, and that
// derivation chain is what DerivedBase walks; a list type derived by
// restriction from one of these three therefore resolves to it and is
// expanded with its item type.
//
// The empty string atomizes to the empty sequence rather than to one
// zero-length token, which is what "a list of no items" means and what
// strings.Fields already produces.
func (n *Node) AtomizeList() (Sequence, bool) {
	item := listItemType(n.TypeAnnotation)
	if item == "" {
		return nil, false
	}
	fields := strings.Fields(n.StringValue())
	out := make(Sequence, 0, len(fields))
	for _, f := range fields {
		if a := atomicForLexical(item, f); a != nil {
			// The item carries the LIST's item type as its derived name, so
			// that "data(@nmtokens) instance of xs:NMTOKEN*" is true. Without
			// it each token is only the xs:string that NMTOKEN erases to and
			// the instance-of test answers false.
			out = append(out, a.WithDerived(item))
			continue
		}
		out = append(out, NewUntypedAtomic(f))
	}
	return out, true
}

// atomicForLexical builds a typed value for one lexical form annotated with
// the named type, walking the derivation chain the schema registered when the
// name is not itself a built-in this package constructs.
//
// AtomizeList needs this and atomicForDerivedAnnotation cannot serve: that
// function reads the whole node's string value, while a list item is one
// token out of many. The walk is the same, bounded the same way, and the
// value keeps the ITEM type's own name so that
// "data(@list) instance of my:itemType*" answers true.
func atomicForLexical(typeName, value string) *Atomic {
	if a := atomicForAnnotation(typeName, value); a != nil {
		return a
	}
	name := typeName
	for i := 0; i < 32; i++ {
		derivedMu.RLock()
		prim, ok := derivedPrimitives[name]
		derivedMu.RUnlock()
		if !ok {
			return nil
		}
		if a := atomicForAnnotation(prim, value); a != nil {
			return a
		}
		name = prim
	}
	return nil
}

// listItemType maps a list type annotation to the type of its items, or ""
// when the annotation does not name a list type.
func listItemType(annotation string) string {
	for i := 0; i < 32 && annotation != ""; i++ {
		switch annotation {
		case "NMTOKENS":
			return "NMTOKEN"
		case "IDREFS":
			return "IDREF"
		case "ENTITIES":
			return "ENTITY"
		}
		// A user-defined list type is not reachable through DerivedBase: the
		// schema layer registers it here with the item type it was declared
		// with, because that is information the data model has no other way to
		// obtain. It is consulted before the derivation walk so that a list
		// whose base happens to be registered as something atomic does not
		// lose its list-ness one step in.
		if item := ListItemOf(annotation); item != "" {
			return item
		}
		next := DerivedBase(annotation)
		if next == annotation {
			return ""
		}
		annotation = next
	}
	return ""
}

var (
	listMu    sync.RWMutex
	listItems = map[string]string{}
)

// RegisterListType records that a schema type is a list, and what its items
// are.
//
// The xsd package calls this as it loads a schema, for the same reason it
// calls RegisterDerivedType: the typed value of a list-typed node is a
// SEQUENCE of one atomic per token, and nothing in the data model can work out
// from a bare type name that "numbers" is a list of xs:decimal. Without it a
// list-typed node atomises to one untypedAtomic holding the whole literal, so
// count(data(@list)) answers 1 and "data(@list) instance of xs:untypedAtomic"
// answers true for a node the schema plainly gave a typed value.
//
// Both arguments are annotation names, as RegisterDerivedType's are.
//
// itemType is the item type's own name, which may itself be a registered
// schema type; atomicForAnnotation and the derivation walk resolve it.
func RegisterListType(name, itemType string) {
	if name == "" || itemType == "" || name == itemType {
		return
	}
	listMu.Lock()
	listItems[name] = itemType
	listMu.Unlock()
}

// ListItemOf returns the item type registered for a list type, or "" when the
// name is not a registered list.
func ListItemOf(name string) string {
	listMu.RLock()
	item := listItems[name]
	listMu.RUnlock()
	return item
}

// atomicForAnnotation builds a typed value from a schema type annotation, or
// returns nil when the annotation names a type this package does not construct.
func atomicForAnnotation(typeName, value string) *Atomic {
	switch typeName {
	case "string", "normalizedString", "token", "language", "Name", "NCName",
		"ID", "IDREF", "ENTITY", "NMTOKEN":
		return NewString(value)

	case "boolean":
		// Trimmed, as every other branch here trims: xs:boolean carries
		// whiteSpace="collapse", so the value a validated node atomises to is
		// the collapsed form and not the characters as they were written.
		// Matching the raw text made "<e>   true   </e>" atomise to nothing
		// at all, so a value the schema had validated as a restriction of
		// xs:boolean fell back to untypedAtomic and compared as a string.
		switch strings.TrimSpace(value) {
		case "true", "1":
			return NewBoolean(true)
		case "false", "0":
			return NewBoolean(false)
		}
		return nil

	case "decimal", "integer", "long", "int", "short", "byte",
		"nonNegativeInteger", "positiveInteger", "nonPositiveInteger",
		"negativeInteger", "unsignedLong", "unsignedInt", "unsignedShort",
		"unsignedByte":
		// The integer family atomises as xs:integer and xs:decimal as
		// itself. Both parse exactly, through big.Rat rather than a
		// float, so that a value too large for a machine word keeps
		// every digit.
		r, ok := new(big.Rat).SetString(strings.TrimSpace(value))
		if !ok {
			return nil
		}
		if typeName == "decimal" {
			return NewDecimal(r)
		}
		return NewIntegerFromRat(r)

	case "float", "double":
		f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			switch strings.TrimSpace(value) {
			case "INF":
				f = math.Inf(1)
			case "-INF":
				f = math.Inf(-1)
			case "NaN":
				f = math.NaN()
			default:
				return nil
			}
		}
		if typeName == "float" {
			return NewFloat(f)
		}
		return NewDouble(f)

	case "anyURI":
		return NewAnyURI(value)

	case "date", "time", "dateTime", "dateTimeStamp":
		// xs:dateTimeStamp is xs:dateTime with a required timezone; the
		// lexical form is a dateTime's, and the requirement was already
		// enforced when the value was validated.
		code := TypeDate
		switch typeName {
		case "time":
			code = TypeTime
		case "dateTime", "dateTimeStamp":
			code = TypeDateTime
		}
		dt, err := ParseDateTime(strings.TrimSpace(value), code)
		if err != nil {
			return nil
		}
		return NewDateTime(dt, code)

	case "gYear", "gYearMonth", "gMonth", "gMonthDay", "gDay":
		code := TypeGYear
		switch typeName {
		case "gYearMonth":
			code = TypeGYearMonth
		case "gMonth":
			code = TypeGMonth
		case "gMonthDay":
			code = TypeGMonthDay
		case "gDay":
			code = TypeGDay
		}
		dt, err := ParseGregorian(strings.TrimSpace(value), code)
		if err != nil {
			return nil
		}
		return NewGregorian(dt, code)

	case "duration", "yearMonthDuration", "dayTimeDuration":
		code := TypeDuration
		switch typeName {
		case "yearMonthDuration":
			code = TypeYearMonthDuration
		case "dayTimeDuration":
			code = TypeDayTimeDuration
		}
		d, err := ParseDuration(strings.TrimSpace(value), code)
		if err != nil {
			return nil
		}
		return NewDuration(d, code)

	case "hexBinary":
		return NewBinary(value, TypeHexBinary)
	case "base64Binary":
		return NewBinary(value, TypeBase64Binary)
	}
	return nil
}

// resolveQNameValue expands the node's string value as a QName against the
// namespaces in scope at the node.
//
// An unprefixed name takes the default namespace, whichever kind of node
// carries it. The rule that leaves an unprefixed name in no namespace applies
// to an attribute's own *name*, not to a QName written as its *value*: XML
// Schema Part 2 §3.2.18 gives xs:QName and xs:NOTATION the value space of
// expanded names and resolves an unprefixed one against the default namespace.
//
// The suite pins the distinction. notation-03.xml writes NOTATION-attribute="mp3"
// under xmlns="http://notation.example.com" and expects it to be the same
// notation as one written "one:mp3"; treating the bare form as absent-namespace
// made distinct-values, xsl:for-each-group and key() all see two values.
func (n *Node) resolveQNameValue() (QName, bool) {
	value := strings.TrimSpace(n.StringValue())
	prefix, local := "", value
	if i := strings.IndexByte(value, ':'); i >= 0 {
		prefix, local = value[:i], value[i+1:]
	}
	if local == "" || strings.ContainsRune(local, ':') {
		return QName{}, false
	}
	scope := n
	if scope.Kind == KindAttribute && scope.Parent != nil {
		scope = scope.Parent
	}
	if prefix == "" {
		uri, _ := scope.LookupPrefix("")
		return QName{URI: uri, Local: local}, true
	}
	uri, ok := scope.LookupPrefix(prefix)
	if !ok {
		return QName{}, false
	}
	return QName{URI: uri, Local: local, Prefix: prefix}, true
}

// Attr returns the attribute node with the given expanded name, or nil.
func (n *Node) Attr(uri, local string) *Node {
	for _, a := range n.Attrs {
		if a.Name.Local == local && a.Name.URI == uri {
			return a
		}
	}
	return nil
}

// AttrValue returns the value of a no-namespace attribute, or "". Most
// attributes the stylesheet compiler reads (match, select, name, test) are
// unprefixed, so this is the common case worth a helper.
func (n *Node) AttrValue(local string) string {
	if a := n.Attr("", local); a != nil {
		return a.Value
	}
	return ""
}

// Root returns the root of the containing tree, walking parent links. For a
// well-formed parsed document this is the document node.
func (n *Node) Root() *Node {
	cur := n
	for cur.Parent != nil {
		cur = cur.Parent
	}
	return cur
}

// IsElement reports whether n is an element with the given expanded name.
func (n *Node) IsElement(uri, local string) bool {
	return n.Kind == KindElement && n.Name.URI == uri && n.Name.Local == local
}

// ChildElements returns the element children, which is what almost every
// stylesheet-compilation walk wants.
func (n *Node) ChildElements() []*Node {
	var out []*Node
	for _, c := range n.Children {
		if c.Kind == KindElement {
			out = append(out, c)
		}
	}
	return out
}

// LookupPrefix resolves a namespace prefix against the in-scope namespaces of
// n, walking up the tree. Returns the URI and whether the prefix was bound.
func (n *Node) LookupPrefix(prefix string) (string, bool) {
	if prefix == "xml" {
		return NSXML, true
	}
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Kind != KindElement {
			continue
		}
		for _, ns := range cur.Namespaces {
			if ns.Name.Local == prefix {
				if ns.Value == "" && prefix != "" {
					// An undeclaration; the prefix is not in scope here.
					return "", false
				}
				return ns.Value, true
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
	// Omitting it left the namespace axis one node short on every element,
	// and made an expression naming the xml prefix fail to resolve in a tree
	// the stylesheet built rather than parsed.
	out := map[string]string{"xml": NSXML}
	var chain []*Node
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Kind == KindElement {
			chain = append(chain, cur)
		}
	}
	// Walk outermost-inward so that inner declarations overwrite outer ones.
	for i := len(chain) - 1; i >= 0; i-- {
		for _, ns := range chain[i].Namespaces {
			if ns.Value == "" {
				delete(out, ns.Name.Local)
			} else {
				out[ns.Name.Local] = ns.Value
			}
		}
	}
	return out
}

// --- Tree construction ------------------------------------------------------

// nextTreeID hands out tree identifiers. Trees created concurrently may
// interleave, which is fine: the spec requires only a stable order, and each
// tree's id is fixed once assigned.
var nextTreeID = newCounter()

// NewTree creates an empty tree with a document node as its root.
func NewTree() *Tree {
	t := &Tree{id: nextTreeID()}
	t.Root = &Node{Kind: KindDocument, tree: t}
	return t
}

// AppendChild links c as the last child of n, setting the parent link. It does
// not assign document order; call Finalize once the tree is complete.
func (n *Node) AppendChild(c *Node) {
	c.Parent = n
	c.tree = n.tree
	n.Children = append(n.Children, c)
}

// AddAttr links a as an attribute of n.
func (n *Node) AddAttr(a *Node) {
	a.Kind = KindAttribute
	a.Parent = n
	a.tree = n.tree
	n.Attrs = append(n.Attrs, a)
}

// AddNamespace links a namespace node to n.
func (n *Node) AddNamespace(prefix, uri string) {
	ns := &Node{
		Kind:   KindNamespace,
		Name:   QName{Local: prefix},
		Value:  uri,
		Parent: n,
		tree:   n.tree,
	}
	n.Namespaces = append(n.Namespaces, ns)
}

// Finalize assigns document-order indices across the whole tree in a single
// pre-order walk. It must be called after the tree is fully built and before
// any node comparison; every parser entry point in this package does so.
func (t *Tree) Finalize() {
	t.counter = 0
	t.assign(t.Root)
}

func (t *Tree) assign(n *Node) {
	n.tree = t
	n.order = t.counter
	t.counter++
	// Namespace nodes precede attribute nodes, which precede children. The
	// relative order of namespace and attribute nodes is implementation
	// defined; fixing it here keeps results reproducible across runs.
	//
	// The reservation is sized by the element's in-scope bindings, not by the
	// ones it declares itself. The namespace axis reports every binding in
	// scope, and the ones inherited from an ancestor have no node on this
	// element to number: the axis synthesizes them and places them with
	// SetSynthesizedOrder at owner.order+1 upwards. Reserving only the
	// declared bindings left those synthesized nodes sitting on the slots
	// already given to this element's attributes and first child, so
	// generate-id() answered the same string for a namespace node and an
	// unrelated attribute — which is what snapshot-0112 detects when it
	// compares the count of distinct identities against the node count.
	if n.Kind == KindElement {
		reserved := len(n.InScopeNamespaces())
		for _, ns := range n.Namespaces {
			ns.tree = t
			ns.order = t.counter
			t.counter++
			reserved--
		}
		// The declared bindings are a subset of the in-scope ones except
		// where a declaration undeclares a prefix, which removes it from
		// scope while still occupying a node; reserved can then go negative
		// and no extra slots are due.
		for ; reserved > 0; reserved-- {
			t.counter++
		}
	} else {
		for _, ns := range n.Namespaces {
			ns.tree = t
			ns.order = t.counter
			t.counter++
		}
	}
	for _, a := range n.Attrs {
		a.tree = t
		a.order = t.counter
		t.counter++
	}
	for _, c := range n.Children {
		t.assign(c)
	}
}

// HasPositions reports whether the tree was parsed with TrackPositions, and so
// can answer Node.Position for the elements it holds.
//
// A caller that caches trees needs it: one parsed without positions cannot
// serve a request that needs them, and the only way to tell is to ask.
func (t *Tree) HasPositions() bool { return t != nil && t.src != "" }

// positionAt converts a byte offset into a 1-based line and column.
//
// Column is counted in bytes, not runes: the offsets come from the XML
// decoder, and a caller pointing an editor at the failure wants the same
// units the decoder used.
func (t *Tree) positionAt(off int) (line, col int, ok bool) {
	if t.src == "" || off < 0 || off > len(t.src) {
		return 0, 0, false
	}
	t.lineOnce.Do(func() {
		// Line 1 starts at offset 0; every byte after a newline starts another.
		t.lineStarts = append(t.lineStarts, 0)
		for i := 0; i < len(t.src); i++ {
			if t.src[i] == '\n' {
				t.lineStarts = append(t.lineStarts, i+1)
			}
		}
	})
	// The line is the last one starting at or before off.
	i := sort.SearchInts(t.lineStarts, off+1) - 1
	if i < 0 {
		return 0, 0, false
	}
	return i + 1, off - t.lineStarts[i] + 1, true
}

// derivedPrimitives maps a schema type annotation to the built-in it erases to.
//
// It is keyed by ANNOTATION NAME (see AnnotationName): a built-in under its
// bare local name, a schema type under {uri}local. Keying it by the bare local
// name conflated the two, and because this map is package-level and
// process-global the conflation was permanent — one schema declaring its own
// type named "QName" rewrote the built-in's entry for every later schema in
// the process. See TestShadowedBuiltinCoexistsWithItsShadow.
//
// It is populated by the xsd package when a schema is loaded, which is the
// only place that knows a user-defined type's base. Keeping it here rather
// than in xsd is what lets xdm.Node.Atomize consult it without importing xsd,
// which it cannot: xsd already imports xdm.
//
// It is guarded by a mutex because schemas load concurrently — that is the
// documented use, and xsd has a test for it — while atomisation reads the
// map on every typed value. A sync.Map is not used because reads vastly
// outnumber writes only *after* loading, and a plain RWMutex makes the
// read path a single atomic in the common case where no schema is loaded.
var (
	derivedMu         sync.RWMutex
	derivedPrimitives = map[string]string{}
)

// RegisterDerivedType records that a schema type erases to a built-in one.
//
// Both arguments are annotation names, which AnnotationName builds; passing a
// bare local name for a type that has a namespace re-creates the conflation
// this keying exists to prevent.
//
// The xsd package calls this as it loads a schema, so that a node annotated
// with a user-defined type still atomises to a typed value rather than to
// untypedAtomic. Without it, "instance of my:partNumberType" could never be
// true for a value read out of a validated document, because the value would
// have discarded the annotation on the way out of the tree.
func RegisterDerivedType(name, primitive string) {
	if name == "" || primitive == "" || name == primitive {
		return
	}
	derivedMu.Lock()
	derivedPrimitives[name] = primitive
	derivedMu.Unlock()
}

var (
	unionMu      sync.RWMutex
	unionMembers = map[string][]string{}
)

// RegisterUnionType records that a schema type is a union, and what its member
// types are.
//
// The xsd package calls this as it loads a schema, for the reason it calls
// RegisterListType: a union's base is always xs:anySimpleType, so the
// derivation chain RegisterDerivedType records dead-ends immediately and
// carries no information about what the value actually is. Without the member
// list a union-typed node atomises to xs:untypedAtomic — the walk finds
// anySimpleType, cannot build a value for it, and gives up — which makes
// "data(u) instance of xs:untypedAtomic" true for a node the schema plainly
// gave a typed value, and makes every question about the member it validated
// as answer false.
//
// The name and every member are annotation names, as RegisterDerivedType's
// arguments are. This registry is keyed by the same strings as
// derivedPrimitives and listItems, so qualifying one of the three and not the
// others would leave unions silently unresolvable.
//
// The members are the *declared* members, in declaration order; which of them
// a given value belongs to is a per-value fact recorded on the node, because
// XSD 1.0 §3.14.4 chooses the member by trying each one's lexical space
// against the value in turn.
func RegisterUnionType(name string, members []string) {
	if name == "" || len(members) == 0 {
		return
	}
	cp := make([]string, 0, len(members))
	for _, m := range members {
		if m != "" && m != name {
			cp = append(cp, m)
		}
	}
	if len(cp) == 0 {
		return
	}
	unionMu.Lock()
	unionMembers[name] = cp
	unionMu.Unlock()
}

// UnionMembersOf returns the member types of a registered union type, or nil
// when the name does not denote one.
//
// The result must not be modified: it is the stored slice, shared with every
// other caller.
func UnionMembersOf(name string) []string {
	unionMu.RLock()
	m := unionMembers[name]
	unionMu.RUnlock()
	return m
}

// DerivedBase returns the type a schema type derives from, or "" if the name
// is not a registered schema type. The name is an annotation name, and so is
// the result, so a chain can be walked by feeding one back in.
//
// It is what makes the subtype relation work for schema types: a value
// annotated as a restriction of xs:NOTATION is an instance of xs:NOTATION as
// well as of its own type, and answering that means walking the chain the
// schema recorded.
func DerivedBase(name string) string {
	derivedMu.RLock()
	base := derivedPrimitives[name]
	derivedMu.RUnlock()
	return base
}

// atomicForUnionAnnotation builds a typed value for a node whose type is a
// union, using the member that validation recorded as having accepted it.
//
// It returns nil unless the node carries a member — a union with no recorded
// member is one this package cannot say anything about, and guessing a member
// here would be wrong: which member accepts "100" depends on the member list
// and on facets this package does not hold, and XSD 1.0 §3.14.4 makes the
// choice a property of the value rather than of the type. The schema layer
// already performs that selection while validating, so the answer is carried
// rather than recomputed.
//
// The member's own name may itself be a user-defined schema type, so the value
// is built through the same lexical walk a list item uses.
func atomicForUnionAnnotation(n *Node) *Atomic {
	member := n.UnionMember
	if member == "" {
		return nil
	}
	// xs:QName and xs:NOTATION members need the node's in-scope namespaces to
	// resolve the prefix, which a lexical form alone does not carry — the same
	// reason Atomize handles them before consulting the annotation table.
	switch member {
	case "QName", "NOTATION":
		if q, ok := n.resolveQNameValue(); ok {
			return NewQNameValue(q).WithDerivedUnion(n.TypeAnnotation, member)
		}
		return nil
	}
	a := atomicForLexical(member, n.StringValue())
	if a == nil {
		return nil
	}
	return a.WithDerivedUnion(n.TypeAnnotation, member)
}

// atomicForDerivedAnnotation builds a typed value for a user-defined schema
// type, using the built-in it derives from.
func atomicForDerivedAnnotation(n *Node) *Atomic {
	// The chain is walked rather than followed one step. A schema type is
	// often a restriction of another *user-defined* type — specialPartNumber
	// restricts partNumberType which restricts xs:string — and the registered
	// base of the annotation is then itself a schema name that
	// atomicForAnnotation cannot build. Stopping after one step returned nil
	// for exactly those types and the node atomised to xs:untypedAtomic,
	// which lost the annotation and made every "instance of" on the value
	// answer false.
	//
	// The walk is bounded so that a schema whose derivations somehow formed a
	// cycle cannot spin here.
	name := n.TypeAnnotation
	for i := 0; i < 32; i++ {
		derivedMu.RLock()
		prim, ok := derivedPrimitives[name]
		derivedMu.RUnlock()
		if !ok {
			return nil
		}
		// The bare names are the built-ins, for the same reason as in
		// Atomize: a qualified key naming a schema type never lands here,
		// and the walk continues past it to whatever it really derives from.
		switch prim {
		case "QName", "NOTATION":
			if q, ok := n.resolveQNameValue(); ok {
				return NewQNameValue(q).WithDerived(n.TypeAnnotation)
			}
			return nil
		}
		if a := atomicForAnnotation(prim, n.StringValue()); a != nil {
			// The value keeps the annotation it was *validated* as, not the
			// intermediate name the walk stopped at: that is what makes
			// "instance of my:specialPartNumber" true as well as
			// "instance of my:partNumberType".
			return a.WithDerived(n.TypeAnnotation)
		}
		name = prim
	}
	return nil
}

// annotationIDKind reports whether an annotation name is derived from xs:ID or
// from xs:IDREF/xs:IDREFS, walking the derivation chain a schema registered.
//
// The walk is bounded for the same reason the other derivation walks are: a
// schema whose derivations somehow formed a cycle must not spin here.
func annotationIDKind(annotation string) (isID, isIDREFS bool) {
	for i := 0; i < 32 && annotation != ""; i++ {
		switch annotation {
		case "ID":
			return true, false
		case "IDREF", "IDREFS":
			return false, true
		}
		annotation = DerivedBase(annotation)
	}
	return false, false
}

// SetTypeAnnotation records a type annotation and the is-id / is-idrefs
// properties that go with it.
//
// It exists so that every producer of annotations — schema assessment, DTD
// attribute types, the XSLT validation instructions — sets the two properties
// the same way. Assigning TypeAnnotation directly is still legal but leaves
// is-id and is-idrefs at whatever they were, which is what a caller
// deliberately preserving them across a strip wants and what a caller
// annotating a fresh node does not.
//
// The properties are only ever turned *on* here. A node that was already
// marked keeps its marking when re-annotated with a non-ID type, because the
// data model's properties describe how the node was validated originally and
// XSLT's stripping rules are the only thing entitled to change them — and
// those rules say the properties do not change at all.
func (n *Node) SetTypeAnnotation(annotation string) {
	n.TypeAnnotation = annotation
	if isID, isRefs := annotationIDKind(annotation); isID || isRefs {
		n.IsID = n.IsID || isID
		n.IsIDREFS = n.IsIDREFS || isRefs
	}
}

// HasSimpleTypeAnnotation reports whether an annotation names a simple type,
// or a complex type with simple content.
//
// XSLT 2.0 section 4.4 preserves whitespace-only text in such an element
// *regardless* of xsl:strip-space: that text is the element's entire typed
// value, which the schema validated, and stripping it would leave a node whose
// annotation describes a value it no longer holds. An element with
// element-only or mixed content has no such value and is stripped normally.
//
// The registration table is the oracle rather than a list of names, because
// the annotation on such an element is the built-in its content type erases
// to — "string" for both an element of type xs:string and one whose anonymous
// complex type extends xs:string, which is exactly the pair section 4.4 groups
// together. A complex type with element-only content registers no derivation
// to a built-in, so it answers false, which is the distinction being drawn.
func HasSimpleTypeAnnotation(annotation string) bool {
	if annotation == "" {
		return false
	}
	// A list type is a simple type: its typed value is a sequence of atomics,
	// so the same reasoning applies even though it is the item type that the
	// registry records.
	if ListItemOf(annotation) != "" {
		return true
	}
	// Bounded like every other derivation walk here, so that a schema whose
	// derivations somehow formed a cycle cannot spin.
	for i := 0; i < 32 && annotation != ""; i++ {
		if isBuiltinSimpleTypeName(annotation) {
			return true
		}
		next := DerivedBase(annotation)
		if next == annotation {
			return false
		}
		annotation = next
	}
	return false
}

// isBuiltinSimpleTypeName reports whether a name is one of the built-in simple
// types this package can build a typed value for.
//
// It asks atomicForAnnotation wherever a lexical form the type accepts exists,
// because that function is the single place the set is defined and a second
// list would drift from it. The types named directly are those whose lexical
// space excludes the empty string: their absence from that answer would be a
// property of the probe value rather than of the type.
func isBuiltinSimpleTypeName(name string) bool {
	switch name {
	case "QName", "NOTATION", "anySimpleType", "anyAtomicType", "untypedAtomic":
		return true
	}
	if atomicForAnnotation(name, "") != nil {
		return true
	}
	switch name {
	case "boolean", "decimal", "float", "double", "integer",
		"nonPositiveInteger", "negativeInteger", "long", "int", "short", "byte",
		"nonNegativeInteger", "unsignedLong", "unsignedInt", "unsignedShort",
		"unsignedByte", "positiveInteger":
		return atomicForAnnotation(name, "1") != nil
	case "date", "dateTime", "time", "duration", "dayTimeDuration",
		"yearMonthDuration", "gYear", "gYearMonth", "gMonth", "gMonthDay", "gDay",
		"hexBinary", "base64Binary", "anyURI":
		return true
	}
	return false
}
