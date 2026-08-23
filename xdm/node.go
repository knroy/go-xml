package xdm

import (
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
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

	// TypeAnnotation records a schema type when the document has been
	// validated. Untyped documents leave this empty, and atomisation then
	// yields xs:untypedAtomic, which is the schemaless default.
	TypeAnnotation string

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

// Order returns the document-order index. Only meaningful against nodes from
// the same tree; use Compare for the general case.
func (n *Node) Order() int { return int(n.order) }

// Tree returns the containing tree.
func (n *Node) Tree() *Tree { return n.tree }

// Compare orders two nodes in document order, returning -1, 0 or 1. Nodes in
// different trees are ordered by tree id, which is stable within a transform.
func (n *Node) Compare(o *Node) int {
	if n == o {
		return 0
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
		switch n.TypeAnnotation {
		case "QName", "NOTATION":
			if q, ok := n.resolveQNameValue(); ok {
				return NewQNameValue(q)
			}
			return NewUntypedAtomic(n.StringValue())
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

// atomicForAnnotation builds a typed value from a schema type annotation, or
// returns nil when the annotation names a type this package does not construct.
func atomicForAnnotation(typeName, value string) *Atomic {
	switch typeName {
	case "string", "normalizedString", "token", "language", "Name", "NCName",
		"ID", "IDREF", "ENTITY", "NMTOKEN":
		return NewString(value)

	case "boolean":
		switch value {
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
// An unprefixed name takes the default namespace when the node is an element
// and the absent namespace when it is an attribute — the XPath rule, and the
// one the schema's own QName-valued attributes follow.
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
		if n.Kind == KindAttribute {
			return QName{Local: local}, true
		}
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
	for _, ns := range n.Namespaces {
		ns.tree = t
		ns.order = t.counter
		t.counter++
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

// DerivedBase returns the type a schema type derives from, or "" if the name
// is not a registered schema type.
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
