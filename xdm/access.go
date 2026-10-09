package xdm

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
	return &Node{Kind: kind, Name: name, Value: value}
}

// NumChildren returns the number of children of n.
func (n *Node) NumChildren() int { return len(n.Children) }

// ChildAt returns the i'th child of n, counting from zero. It panics if i is
// out of range, as indexing a slice does.
func (n *Node) ChildAt(i int) *Node { return n.Children[i] }

// FirstChild returns the first child of n, or nil if it has none.
func (n *Node) FirstChild() *Node {
	if len(n.Children) == 0 {
		return nil
	}
	return n.Children[0]
}

// LastChild returns the last child of n, or nil if it has none.
func (n *Node) LastChild() *Node {
	if len(n.Children) == 0 {
		return nil
	}
	return n.Children[len(n.Children)-1]
}

// NumAttrs returns the number of attributes of n.
func (n *Node) NumAttrs() int { return len(n.Attrs) }

// AttrAt returns the i'th attribute of n, in the order they were added.
func (n *Node) AttrAt(i int) *Node { return n.Attrs[i] }

// NumNamespaceDecls returns the number of namespace nodes held on n itself.
func (n *Node) NumNamespaceDecls() int { return len(n.Namespaces) }

// NamespaceDeclAt returns the i'th namespace node held on n itself.
func (n *Node) NamespaceDeclAt(i int) *Node { return n.Namespaces[i] }

// Builder-side mutation.
//
// The setters below are for code that builds or rewrites a tree: xdmbuild,
// and the few transforms that edit a tree they own. Each does exactly the
// field write it replaced: no re-parenting, no tree pointer, no document
// order. A slice passed in is kept, not copied.

// SetName renames n.
func (n *Node) SetName(name QName) { n.Name = name }

// SetValue sets the string value n holds: the text of a text, comment or
// processing-instruction node, an attribute's value, a namespace node's URI.
func (n *Node) SetValue(v string) { n.Value = v }

// SetParent sets n's parent link and nothing else.
func (n *Node) SetParent(p *Node) { n.Parent = p }

// SetChildren replaces n's children. They are not re-parented.
func (n *Node) SetChildren(kids []*Node) { n.Children = kids }

// SetAttrs replaces n's attributes. They are not re-parented.
func (n *Node) SetAttrs(attrs []*Node) { n.Attrs = attrs }

// SetNamespaceDecls replaces the namespace nodes held on n. They are not
// re-parented.
func (n *Node) SetNamespaceDecls(ns []*Node) { n.Namespaces = ns }

// SetBaseURI sets n's own base URI, leaving its descendants as they are.
func (n *Node) SetBaseURI(base string) { n.BaseURI = base }

// SetDocumentURI sets dm:document-uri on a document node. See the
// invariant on the field: only for a document fetched by that URI.
func (n *Node) SetDocumentURI(uri string) { n.DocumentURI = uri }
