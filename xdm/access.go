package xdm

import "iter"

// Node accessors.
//
// Code outside this package reads a node through these methods and never
// through its fields, so that the storage behind them can change without a
// caller noticing. Each method is small enough to inline, so a call costs what
// the field read it replaced did.
//
// The children, attributes and namespace nodes of an element are reached by
// count and index, or by iterator, but never as a slice: a layout that does not
// keep them in a slice can answer all of these cheaply, and could not hand out
// a slice without building one.

// NewNode returns a detached node of the given kind, name and value, with no
// parent, children or tree. Link it with AppendChild, AddAttr or the setters
// below; a Builder is the usual way to make a tree.
func NewNode(kind NodeKind, name QName, value string) *Node {
	return &Node{kind: kind, name: name, value: value}
}

// Kind returns n's node kind.
func (n *Node) Kind() NodeKind { return n.kind }

// Name returns n's name: an element's or attribute's expanded QName, a
// processing instruction's target, a namespace node's prefix as Local. Other
// kinds have the zero QName.
func (n *Node) Name() QName { return n.name }

// Value is the text content of a text, comment, processing-instruction or
// attribute node, and the namespace URI of a namespace node. Element and
// document nodes derive their string value from descendants; see
// StringValue.
func (n *Node) Value() string { return n.value }

// Parent returns n's parent, or nil for a root. An attribute's or namespace
// node's parent is its element.
func (n *Node) Parent() *Node { return n.parent }

// Children iterates over n's children in document order. Attributes and
// namespace nodes are not children.
func (n *Node) Children() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		for _, c := range n.children {
			if !yield(c) {
				return
			}
		}
	}
}

// Attrs iterates over n's attributes in the order they were added.
func (n *Node) Attrs() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		for _, a := range n.attrs {
			if !yield(a) {
				return
			}
		}
	}
}

// NamespaceDecls iterates over the namespace nodes held on n itself. On a
// parsed element these are its declarations; the namespace axis, which also
// has the inherited bindings, is InScopeNamespaces.
func (n *Node) NamespaceDecls() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		for _, ns := range n.namespaces {
			if !yield(ns) {
				return
			}
		}
	}
}

// BaseURI returns n's own resolved base URI, used by fn:base-uri, fn:doc and
// fn:document.
func (n *Node) BaseURI() string { return n.baseURI }

// DocumentURI returns dm:document-uri: set only on a document node retrieved
// by that URI, empty otherwise.
func (n *Node) DocumentURI() string { return n.documentURI }

// The typing getters answer the PSVI properties schema assessment (or a DTD,
// or an XSLT validation instruction) recorded on n. An untyped node answers
// the zero value of each. TypingOf returns them together; ApplyTyping and
// the SetTypeAnnotation family set them.

// TypeAnnotation returns n's type annotation name (see AnnotationName), or
// "" when n is untyped.
func (n *Node) TypeAnnotation() string { return n.typeAnnotation }

// UnionMember returns the member type of a union that accepted n's value.
func (n *Node) UnionMember() string { return n.unionMember }

// DerivedPrimitive returns the built-in type n's annotation erases to, as
// recorded at assessment, or "" when not recorded.
func (n *Node) DerivedPrimitive() string { return n.derivedPrimitive }

// ListItem returns the item type when n's annotation is a list type, as
// recorded at assessment, or "".
func (n *Node) ListItem() string { return n.listItem }

// IsID reports the dm:is-id property.
func (n *Node) IsID() bool { return n.isID }

// IsIDREFS reports the dm:is-idrefs property.
func (n *Node) IsIDREFS() bool { return n.isIDREFS }

// IsNilled reports the dm:nilled property.
func (n *Node) IsNilled() bool { return n.isNilled }

// NoTypedValue reports that n's typed value is absent (element-only or empty
// content), so fn:data on it is FOTY0012.
func (n *Node) NoTypedValue() bool { return n.noTypedValue }

// MixedContent reports that n was validated against a mixed complex type
// other than xs:anyType.
func (n *Node) MixedContent() bool { return n.mixedContent }

// NumChildren returns the number of children of n.
func (n *Node) NumChildren() int { return len(n.children) }

// ChildAt returns the i'th child of n, counting from zero. It panics if i is
// out of range, as indexing a slice does.
func (n *Node) ChildAt(i int) *Node { return n.children[i] }

// FirstChild returns the first child of n, or nil if it has none.
func (n *Node) FirstChild() *Node {
	if len(n.children) == 0 {
		return nil
	}
	return n.children[0]
}

// LastChild returns the last child of n, or nil if it has none.
func (n *Node) LastChild() *Node {
	if len(n.children) == 0 {
		return nil
	}
	return n.children[len(n.children)-1]
}

// NumAttrs returns the number of attributes of n.
func (n *Node) NumAttrs() int { return len(n.attrs) }

// AttrAt returns the i'th attribute of n, in the order they were added.
func (n *Node) AttrAt(i int) *Node { return n.attrs[i] }

// NumNamespaceDecls returns the number of namespace nodes held on n itself.
func (n *Node) NumNamespaceDecls() int { return len(n.namespaces) }

// NamespaceDeclAt returns the i'th namespace node held on n itself.
func (n *Node) NamespaceDeclAt(i int) *Node { return n.namespaces[i] }

// Builder-side mutation.
//
// The setters below are for code that builds or rewrites a tree: xdmbuild,
// and the few transforms that edit a tree they own. Each does exactly the
// field write it replaced: no re-parenting, no tree pointer, no document
// order. A slice passed in is kept, not copied.

// SetName renames n.
func (n *Node) SetName(name QName) { n.name = name }

// SetValue sets the string value n holds: the text of a text, comment or
// processing-instruction node, an attribute's value, a namespace node's URI.
func (n *Node) SetValue(v string) { n.value = v }

// SetParent sets n's parent link and nothing else.
func (n *Node) SetParent(p *Node) { n.parent = p }

// SetChildren replaces n's children. They are not re-parented.
func (n *Node) SetChildren(kids []*Node) { n.children = kids }

// SetAttrs replaces n's attributes. They are not re-parented.
func (n *Node) SetAttrs(attrs []*Node) { n.attrs = attrs }

// SetNamespaceDecls replaces the namespace nodes held on n. They are not
// re-parented.
func (n *Node) SetNamespaceDecls(ns []*Node) { n.namespaces = ns }

// SetBaseURI sets n's own base URI, leaving its descendants as they are.
func (n *Node) SetBaseURI(base string) { n.baseURI = base }

// SetDocumentURI sets dm:document-uri on a document node. See the
// invariant on the field: only for a document fetched by that URI.
func (n *Node) SetDocumentURI(uri string) { n.documentURI = uri }

// NextSibling returns the child of n's parent that follows n, or nil when n
// is the last child, has no parent, or is an attribute or namespace node.
func (n *Node) NextSibling() *Node {
	p := n.parent
	if p == nil || n.kind == KindAttribute || n.kind == KindNamespace {
		return nil
	}
	for i, c := range p.children {
		if c == n {
			if i+1 < len(p.children) {
				return p.children[i+1]
			}
			return nil
		}
	}
	return nil
}

// PrevSibling returns the child of n's parent that precedes n, or nil when n
// is the first child, has no parent, or is an attribute or namespace node.
func (n *Node) PrevSibling() *Node {
	p := n.parent
	if p == nil || n.kind == KindAttribute || n.kind == KindNamespace {
		return nil
	}
	for i, c := range p.children {
		if c == n {
			if i > 0 {
				return p.children[i-1]
			}
			return nil
		}
	}
	return nil
}

// Descendants iterates over n's descendants in document order: children,
// their children, and so on. Attributes and namespace nodes are not
// descendants.
func (n *Node) Descendants() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		n.descend(yield)
	}
}

func (n *Node) descend(yield func(*Node) bool) bool {
	for _, c := range n.children {
		if !yield(c) || !c.descend(yield) {
			return false
		}
	}
	return true
}

// DeclaredNamespaces iterates over the namespace declarations held on n
// itself as prefix and URI, without making a namespace node for each. An
// empty URI undeclares the prefix.
func (n *Node) DeclaredNamespaces() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for _, ns := range n.namespaces {
			if !yield(ns.name.Local, ns.value) {
				return
			}
		}
	}
}
