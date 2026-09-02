package xdmbuild

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// Builder accumulates the result of a sequence constructor.
//
// XSLT output is a sequence of nodes and atomic values, not a string: an
// attribute added after an element has children is an error, adjacent text
// must be merged, and the result may be a temporary tree that later
// instructions navigate. Building a string directly would make all of that
// impossible.
type Builder struct {
	items xdm.Sequence
	// open is the element currently being built, if any. Attributes and
	// namespaces are added to it until it is closed.
	open *xdm.Node
	// parent chains open elements so that nested construction works.
	parent *Builder
	tree   *xdm.Tree
	// declared records the prefixes xsl:namespace has bound on the open
	// element, and to what.
	//
	// It is kept separately from open.Namespaces because XTDE0430 is about
	// "the result sequence", meaning the namespace nodes the sequence
	// constructor produced — not the binding the element's own name already
	// carries. A clash with that one is resolved by renaming the prefix, and
	// the suite requires it to be: namespace-alias-1903 constructs
	// <ns:e xmlns:ns="one"> and then binds ns to "two", and the expected
	// result renames the element's prefix rather than reporting an error.
	declared map[string]string

	// lastAtomic records that the item most recently appended was an atomic
	// value rather than a node.
	//
	// Section 5.7.1 turns a run of adjacent atomic values into one text node
	// with a single space between each, but adjacent *text nodes* are merged
	// with no separator at all. Only the builder knows which of the two just
	// happened, so the flag lives here rather than being rediscovered by
	// looking at the trailing text — which cannot tell "a" "b" from "ab".
	lastAtomic bool

	// itemSep is xsl:output/@item-separator for the result tree this builder
	// is producing, or nil when the attribute is absent.
	//
	// It is set only on the builder for a final result tree or a result
	// document, never on one building a temporary tree: 5.7.1's separator
	// rule is part of sequence normalisation, which is what turns the result
	// *of a transformation* into a document, and a variable's content is not
	// normalised that way.
	itemSep *string

	// policy names the structural faults this builder detects, and carries
	// the namespace and type rules a copy is made under. It is never nil: New
	// requires one, and a nested builder inherits its parent's.
	policy Policy
}

// SetItemSeparator records the item-separator that applies to the tree this
// builder produces. A nil argument leaves the default 5.7.1 rules in force.
func (b *Builder) SetItemSeparator(sep *string) { b.itemSep = sep }

// New returns a builder that reports faults through p.
//
// p must not be nil: every fault the builder detects is named by the caller's
// language, and there is no default to fall back on.
func New(p Policy) *Builder {
	if p == nil {
		panic("xdmbuild.New: nil Policy")
	}
	return &Builder{tree: xdm.NewTree(), policy: p}
}

// AppendNode adds a node to the current output position.
//
// A node that already belongs to a tree is copied first. AppendChild rewrites
// the node's Parent and tree pointers and Finalize renumbers its document
// order, so adopting a source node in place *mutates the source document* —
// evaluating an unused variable containing xsl:sequence was enough to reorder
// the input, and two goroutines transforming a shared parsed tree raced on it.
// xsl:copy-of already copied; xsl:sequence and xsl:perform-sort did not, and
// the guard belongs here where every caller is covered.
func (b *Builder) AppendNode(n *xdm.Node) {
	b.lastAtomic = false
	// A namespace node joins the element's bindings, not its children. Any
	// instruction may put one in the result sequence — xsl:sequence selecting
	// namespace::* is the usual way — and appending it as a child both lost
	// it and made the element look as if it already had content, so a later
	// xsl:attribute was rejected with XTDE0410.
	if n.Kind == xdm.KindNamespace && b.open != nil {
		_ = b.AddNamespace(n.Name.Local, n.Value)
		return
	}
	// An attribute node in a result sequence joins the element's attributes,
	// not its children — section 5.7.1 prepends such nodes to the content
	// rather than treating them as content. A parentless attribute is an
	// ordinary item, so xsl:call-template on a template declared
	// as="attribute()*" delivers one here; appending it as a child produced
	// an element with an attribute node among its children, which no
	// serialiser can write.
	if n.Kind == xdm.KindAttribute && b.open != nil {
		_ = b.AddAttribute(n.Name, n.Value)
		return
	}
	if n.Kind == xdm.KindDocument && b.open != nil {
		// A document node used as the content of an element contributes its
		// children, not itself (5.7.1): a result tree may not contain a
		// document node below the root.
		for _, ch := range append([]*xdm.Node(nil), n.Children...) {
			b.AppendNode(ch)
		}
		return
	}
	if n.Kind == xdm.KindText && b.open != nil {
		// A text node becoming the content of an element is merged with the
		// text beside it and dropped when it is empty, which is section
		// 5.7.1's rule for constructing complex content and XQuery §3.9.1.3's
		// for the same thing. AppendText is where both live, and appending
		// the node as a child instead left two text nodes adjacent — which
		// no result tree may hold. "document {'def', <anode/>, 'ghi'}" used
		// as element content beside 'abc' and 'jkl' gave four children where
		// the merged tree has three.
		b.AppendText(n.Value)
		return
	}
	if b.open != nil {
		// Copying is only needed when the node is about to be re-parented:
		// AppendChild rewrites Parent and tree pointers, so adopting a source
		// node in place would mutate the source document.
		n = detach(n)
		Rebase(n, b.open.BaseURI)
		b.open.AppendChild(n)
		return
	}
	// At the top of a sequence nothing is re-parented, so the node itself is
	// the result. xsl:sequence is defined to preserve node identity — a
	// variable declared as="item()*" holding (doc/one, doc/two) must answer
	// true to "is" against the source nodes — and copying here answered false.
	b.items = append(b.items, n)
}

// Rebase recomputes the base URIs of a subtree that has just been re-parented.
//
// A copied element keeps whatever xml:base attribute it carried, and XSLT 2.0
// section 11.9 makes the base URI of the copy a function of that attribute and
// of the *new* parent, not of the document it came from. So a source element
// written xml:base="/xml/" under a document based at http://a.example/ becomes
// based at http://b.example/xml/ once copied under a parent based at
// http://b.example/main/ — carrying the resolved http://a.example/xml/ across
// unchanged is the one answer that is wrong in every case.
//
// An element with no xml:base of its own simply inherits, which is the same
// rule with an empty reference.
func Rebase(n *xdm.Node, parentBase string) {
	if n == nil || n.Kind != xdm.KindElement {
		return
	}
	base := parentBase
	for _, a := range n.Attrs {
		if a.Name.URI == xdm.NSXML && a.Name.Local == "base" {
			base = ResolveAgainst(parentBase, a.Value)
			if a.Value != "" && base == a.Value {
				// ResolveAgainst returns the reference unchanged when it
				// cannot resolve — an absolute reference, or a parent with no
				// usable base. Either way the reference is the answer.
				base = a.Value
			}
			break
		}
	}
	if base == "" {
		return
	}
	n.BaseURI = base
	for _, ch := range n.Children {
		Rebase(ch, base)
	}
}

// RebaseDetached recomputes the base URI of a copy that has no parent.
//
// Section 11.9: "the base URI of a node is copied, except in the case of an
// element node having an xml:base attribute, in which case the base URI of
// the new node is taken as the value of the xml:base attribute, resolved if
// it is relative against the base URI of the xsl:copy/xsl:copy-of
// instruction". So an element without its own xml:base keeps the source's
// base URI unchanged — which is why this cannot just call Rebase, whose
// empty-reference case makes the node inherit from its new parent.
func RebaseDetached(n *xdm.Node, instrBase string) {
	if n == nil || n.Kind != xdm.KindElement {
		return
	}
	for _, a := range n.Attrs {
		if a.Name.URI == xdm.NSXML && a.Name.Local == "base" {
			Rebase(n, instrBase)
			return
		}
	}
}

// detach returns a node safe to re-parent: n itself when it is freshly
// constructed, a deep copy when it belongs to a tree already.
func detach(n *xdm.Node) *xdm.Node {
	if n == nil || (n.Tree() == nil && n.Parent == nil) {
		return n
	}
	return DeepCopy(n)
}

// AppendText adds text, merging with a preceding text node so that the XDM
// invariant of no adjacent text nodes holds in constructed trees too.
func (b *Builder) AppendText(s string) {
	b.lastAtomic = false
	if s == "" && b.open != nil {
		// Section 5.7.1 discards zero-length text nodes only while
		// *constructing complex content*, that is, inside an element. At the
		// top level of a sequence constructor they all survive: section
		// 11.10's own example gives a function body of three xsl:text
		// instructions and says it "returns a sequence of three text nodes",
		// and function-1009 asserts count() = 3 over exactly that body with
		// every one of them zero-length. Dropping the empty ones once any
		// other item was present answered 1.
		//
		// Nothing downstream is harmed by keeping them: ToTree skips
		// zero-length children when it builds a variable's document node,
		// and constructedText skips them when it joins a sequence into a
		// string, so neither a temporary tree nor a separator gains anything
		// from them.
		return
	}
	if b.open != nil {
		if k := len(b.open.Children); k > 0 {
			if last := b.open.Children[k-1]; last.Kind == xdm.KindText {
				last.Value += s
				return
			}
		}
		b.open.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: s})
		return
	}
	// At the top level of a sequence constructor the text nodes stay
	// separate. Section 5.7.1's merging rule applies when *constructing
	// complex content* — inside an element, which the branch above handles —
	// and section 11.10's own example is explicit that a function whose body
	// is three text instructions "returns a sequence of three text nodes".
	// Merging them here made xsl:perform-sort over such a body see a single
	// item and sort nothing.
	b.items = append(b.items, &xdm.Node{Kind: xdm.KindText, Value: s})
}

// AppendValue adds an atomic value to the output sequence.
func (b *Builder) AppendValue(a *xdm.Atomic) {
	// Inside an element being built, an atomic value becomes text; at the top
	// level it stays an atomic item, because xsl:sequence can return one.
	if b.open != nil {
		sep := ""
		if b.lastAtomic {
			sep = " "
		}
		b.AppendText(sep + a.String())
		b.lastAtomic = true
		return
	}
	b.items = append(b.items, a)
	b.lastAtomic = true
}

// EndAtomicRun declares that the values appended so far form a complete run,
// so that the next atomic value starts a new one and takes no separator.
//
// Both languages separate a run of adjacent atomic values with single spaces,
// and both scope the run to one sequence — XSLT §5.7.1 to the sequence an
// instruction returns, XQuery §3.9.1.3 to the value of one enclosed
// expression. The builder cannot see either boundary, because a caller
// appends a sequence item by item and nothing in the calls says where one
// ended and the next began.
//
// XSLT does not call this: it appends the whole of an instruction's sequence
// before anything else can intervene, so its runs already end where they
// should. XQuery needs it because "<e>{1}{2}</e>" is two enclosed
// expressions, each a one-item sequence, and the two items must abut rather
// than be separated.
func (b *Builder) EndAtomicRun() { b.lastAtomic = false }

// AddAttribute attaches an attribute to the element under construction.
func (b *Builder) AddAttribute(name xdm.QName, value string) error {
	return b.AddAttributeTyped(name, value, "")
}

// AddAttributeTyped adds an attribute carrying a type annotation.
//
// The annotation has to travel with the attribute rather than be applied to a
// throwaway node: xsl:attribute assesses the value it is about to write, and a
// pattern such as schema-attribute(A) matches only a node that was actually
// validated against the declaration, so an attribute that lost its annotation
// on the way into the element could never match however it was named.
func (b *Builder) AddAttributeTyped(name xdm.QName, value string,
	typeAnnotation string) error {

	if b.open == nil {
		// A parentless attribute is a legal item in the data model, and a
		// sequence constructor may produce one: xsl:function as="attribute()"
		// with an xsl:attribute body is the ordinary way to write one, and
		// XTDE0410 is not about this at all. The error is about *ordering*
		// within element content — an attribute preceded by a node that is
		// neither an attribute nor a namespace — which is checked below where
		// there is an element to check it against.
		// The prefix rule applies to a parentless attribute too. §3.9.3.3
		// requires a namespaced attribute to carry a prefix, because XML
		// gives an unprefixed attribute no namespace at all and the name
		// would not survive being written out. There is no element here to
		// hang the declaration on, but the *name* is still asked about:
		// K2-ComputeConAttr-54 reads prefix-from-QName(node-name(...)) off a
		// standalone attribute and requires a prefix to be there, and -55
		// requires the XML namespace to have got "xml" rather than an
		// invented one.
		n := &xdm.Node{
			Kind: xdm.KindAttribute, Name: name, Value: value,
			TypeAnnotation: typeAnnotation,
		}
		fixupOrphanAttrPrefix(n)
		b.items = append(b.items, n)
		return nil
	}
	// Adding an attribute after children exist is an error the spec calls out,
	// because it usually means the stylesheet's instruction order is wrong.
	if len(b.open.Children) > 0 {
		return b.policy.Err(FaultAttrAfterChild,
			fmt.Sprintf("attribute %q added after the element already has children",
				name.Lexical()))
	}
	// A repeated attribute is where the two languages genuinely differ rather
	// than merely naming the same fault differently. XSLT discards the
	// earlier one; XQuery raises an error. The policy decides by whether it
	// returns one, so the check is the same either way.
	for _, a := range b.open.Attrs {
		if a.Name.URI == name.URI && a.Name.Local == name.Local {
			if err := b.policy.Err(FaultDuplicateAttribute,
				fmt.Sprintf("attribute %q appears more than once",
					name.Lexical())); err != nil {
				return err
			}
			a.Value = value
			a.TypeAnnotation = typeAnnotation
			return nil
		}
	}
	b.open.AddAttr(&xdm.Node{Kind: xdm.KindAttribute, Name: name, Value: value,
		TypeAnnotation: typeAnnotation})
	fixupAttrPrefix(b.open, b.open.Attrs[len(b.open.Attrs)-1])
	return nil
}

// fixupOrphanAttrPrefix is fixupAttrPrefix for an attribute with no element
// to declare anything on: only the name can be repaired, so an invented
// prefix is chosen without consulting bindings that do not exist yet.
func fixupOrphanAttrPrefix(attr *xdm.Node) {
	switch {
	case attr.Name.URI == "":
		attr.Name.Prefix = ""
	case attr.Name.URI == xdm.NSXML:
		attr.Name.Prefix = "xml"
	case attr.Name.Prefix == "":
		attr.Name.Prefix = "ns0"
	}
}

// fixupAttrPrefix gives a namespaced attribute a usable prefix and declares
// it on the element.
//
// This is the namespace fixup of section 5.7.3, restricted to the case that
// actually needs repairing. An attribute in a namespace cannot borrow the
// default namespace declaration — XML gives an unprefixed attribute no
// namespace at all — so a computed attribute whose name carries a URI but no
// prefix has to be given one. A prefix already bound to a different URI on
// this element is the same problem, so it is replaced rather than trusted.
func fixupAttrPrefix(el, attr *xdm.Node) {
	if attr.Name.URI == "" {
		// An attribute in no namespace needs no declaration, and must not
		// acquire a prefix: it would then be in one.
		attr.Name.Prefix = ""
		return
	}
	if attr.Name.URI == xdm.NSXML {
		attr.Name.Prefix = "xml"
		return
	}
	if p := attr.Name.Prefix; p != "" {
		uri, ok := el.LookupPrefix(p)
		if ok && uri == attr.Name.URI {
			return
		}
		if ok {
			// The prefix is already bound to something else on this element,
			// so it cannot be reused: two xmlns:p declarations on one element
			// is not a document any parser will read back.
			attr.Name.Prefix = freshPrefixOn(el, p)
		}
		el.AddNamespace(attr.Name.Prefix, attr.Name.URI)
		return
	}
	// An existing prefix for this URI is reused, so that a stylesheet adding
	// several attributes in one namespace does not accumulate a declaration
	// each.
	for cur := el; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			continue
		}
		for _, ns := range cur.Namespaces {
			if ns.Value == attr.Name.URI && ns.Name.Local != "" {
				if uri, ok := el.LookupPrefix(ns.Name.Local); ok && uri == attr.Name.URI {
					attr.Name.Prefix = ns.Name.Local
					return
				}
			}
		}
	}
	// An invented prefix is numbered from zero — ns0, ns1 — which is the
	// form the suite's expected results are written against.
	for i := 0; ; i++ {
		p := fmt.Sprintf("ns%d", i)
		if _, taken := el.LookupPrefix(p); taken {
			continue
		}
		attr.Name.Prefix = p
		el.AddNamespace(p, attr.Name.URI)
		return
	}
}

// freshPrefixOn returns a prefix not already bound on el, derived from want.
//
// The suffix starts at 1 rather than 0 so that the first rename of "p" reads
// as "p_1", which is the form the suite's expected results use.
func freshPrefixOn(el *xdm.Node, want string) string {
	if _, taken := el.LookupPrefix(want); !taken {
		return want
	}
	for i := 1; ; i++ {
		p := fmt.Sprintf("%s_%d", want, i)
		if _, taken := el.LookupPrefix(p); !taken {
			return p
		}
	}
}

// AddNamespace attaches a namespace node to the element under
// construction.
//
// It is shared by xsl:namespace and by xsl:copy-of of a namespace node: both
// add a binding to the element being built, and a namespace node appended as
// if it were a child would silently vanish from the result.
func (b *Builder) AddNamespace(prefix, uri string) error {
	if b.open == nil {
		b.items = append(b.items, &xdm.Node{
			Kind:  xdm.KindNamespace,
			Name:  xdm.QName{Local: prefix},
			Value: uri,
		})
		return nil
	}
	// XTDE0440: "the result sequence contains a namespace node with no name
	// and the element node being constructed has a null namespace URI (that
	// is, it is an error to define a default namespace when the element is in
	// no namespace)". Such a binding would put the element's own unprefixed
	// name into that namespace when the result was read back, which is not
	// the element that was constructed.
	if prefix == "" && b.open.Name.URI == "" {
		return b.policy.Err(FaultDefaultNSOnNoNS,
			fmt.Sprintf("a default namespace cannot be declared on %s, "+
				"which is in no namespace", b.open.Name.Local))
	}
	// XTDE0430: "the result sequence contains two or more namespace nodes
	// having the same name but different string values". Re-declaring a
	// prefix to the *same* URI is harmless and common — an element and its
	// content may each ask for it — so only a conflicting one is an error.
	if was, ok := b.declared[prefix]; ok && was != uri {
		return b.policy.Err(FaultConflictingPrefix,
			fmt.Sprintf("the prefix %q is bound to both %q and %q on the "+
				"same element", prefix, was, uri))
	}
	if b.declared == nil {
		b.declared = map[string]string{}
	}
	b.declared[prefix] = uri
	// The element's own name may already have claimed this prefix for a
	// different URI. Section 11.7 resolves that in favour of the namespace
	// node and renames the element's prefix — the specification's own example
	// turns <xsl:element name="p:item"> plus <xsl:namespace name="p"> into
	// <ns0:item xmlns:ns0="…p" xmlns:p="…q">. Leaving both bindings in place
	// produced an element whose prefix pointed at the wrong URI.
	if b.open.Name.Prefix == prefix && b.open.Name.URI != uri {
		b.open.Name.Prefix = b.freshPrefix(prefix)
	}
	for _, ns := range b.open.Namespaces {
		if ns.Name.Local == prefix {
			ns.Value = uri
			return nil
		}
	}
	b.open.AddNamespace(prefix, uri)
	return nil
}

// freshPrefix returns a prefix not yet bound on the element under
// construction, derived from want so that the result still reads as the
// stylesheet's choice.
func (b *Builder) freshPrefix(want string) string {
	for i := 0; ; i++ {
		p := fmt.Sprintf("%s_%d", want, i)
		taken := false
		for _, ns := range b.open.Namespaces {
			if ns.Name.Local == p {
				taken = true
				break
			}
		}
		if _, ok := b.declared[p]; ok {
			taken = true
		}
		if !taken {
			return p
		}
	}
}

// NoteDeclared records a namespace node the element was constructed with, so
// that a later xsl:namespace binding the same prefix elsewhere is seen as the
// XTDE0430 conflict it is.
//
// The distinction is which namespaces reach "the result sequence". A prefix
// carried only by the element's *name* is not a namespace node the sequence
// constructor produced, and section 11.7 resolves a clash with it by renaming
// — namespace-alias-1903 writes xsl:element name="ns:e" with xmlns:ns on the
// instruction, which is not copied to the result, and requires the rename.
// A literal result element is the other case: 11.7 copies its namespace nodes
// into the result, so two conflicting bindings for one prefix really are two
// namespace nodes with the same name and different values. namespace-2618 is
// the spec's own "Conflicting Namespace Prefixes" example and expects the
// error.
func (b *Builder) NoteDeclared(prefix, uri string) {
	if b.declared == nil {
		b.declared = map[string]string{}
	}
	b.declared[prefix] = uri
}

// StartElement opens a new element, returning a builder scoped to it.
func (b *Builder) StartElement(name xdm.QName) *Builder {
	el := &xdm.Node{Kind: xdm.KindElement, Name: name}
	b.AppendNode(el)
	return &Builder{open: el, parent: b, tree: b.tree, policy: b.policy}
}

// Sequence returns the accumulated items.
func (b *Builder) Sequence() xdm.Sequence { return b.items }

// ToTree wraps the accumulated items in a document node, which is what a
// variable with content produces.
// ToDocument is ToTree with the check XTDE0420 requires.
//
// "It is a non-recoverable dynamic error if the result sequence used to
// construct the content of a document node contains a namespace node or
// attribute node." A document node has no attributes and carries no namespace
// declarations of its own, so such an item has nowhere to go: appending it
// silently discarded it, and the stylesheet saw a document that was missing
// what it had just built.
//
// It is separate from ToTree because not every temporary builder becomes a
// document. A sequence constructor producing a bare attribute is legitimate —
// xsl:function as="attribute()" is written that way — and only wrapping the
// result in a document node makes it wrong.
func (b *Builder) ToDocument() (*xdm.Node, error) {
	for _, it := range b.items {
		n, ok := it.(*xdm.Node)
		if !ok {
			continue
		}
		switch n.Kind {
		case xdm.KindAttribute:
			return nil, b.policy.Err(FaultAttrOnDocument,
				fmt.Sprintf("an attribute node (%s) cannot be the content of "+
					"a document node", n.Name.Lexical()))
		case xdm.KindNamespace:
			return nil, b.policy.Err(FaultAttrOnDocument,
				fmt.Sprintf("a namespace node (%s) cannot be the content of "+
					"a document node", n.Name.Local))
		}
	}
	return b.ToTree(), nil
}

func (b *Builder) ToTree() *xdm.Node {
	tree := xdm.NewTree()
	// Adjacent atomic values become one text node separated by single spaces,
	// exactly as they do inside a constructed element: a variable holding a
	// sequence constructor is a document node built by the same rule, and
	// giving each value its own text node would both break the no-adjacent-
	// text invariant and lose the separators.
	prevAtomic := false
	// 5.7.1 step 3: when an item separator is in force it goes between every
	// pair of adjacent items, replacing the default rules entirely — the
	// single space between adjacent atomic values, and the nothing between
	// adjacent nodes. So the separator is emitted from the second item on,
	// and prevAtomic's run-merging is switched off: two atomic values with an
	// explicit separator between them are no longer one run.
	//
	// The separator is a text node in the constructed tree, not something
	// the serialiser paints on. validation-0214 depends on that: the result
	// document it builds is (comment, html, comment), so the separators land
	// as text at document level and validation="strict" reports XTTE1550 for
	// a document node whose children are not exactly one element.
	sep := b.itemSep
	emitted := 0
	for _, it := range b.items {
		if sep != nil && emitted > 0 {
			// A zero-length separator inserts nothing, which is exactly what
			// item-separator="" asks for; it must still suppress the default
			// space between atomic values, which it does because prevAtomic
			// is cleared below.
			if *sep != "" {
				if kids := tree.Root.Children; len(kids) > 0 &&
					kids[len(kids)-1].Kind == xdm.KindText {
					kids[len(kids)-1].Value += *sep
				} else {
					tree.Root.AppendChild(&xdm.Node{
						Kind: xdm.KindText, Value: *sep})
				}
			}
			prevAtomic = false
		}
		if sep != nil {
			emitted++
		}
		if n, ok := it.(*xdm.Node); ok {
			if n.Kind == xdm.KindText && n.Value == "" {
				// Section 5.7.1 removes zero-length text nodes when
				// constructing complex content, and a variable's implicit
				// document node is complex content. The builder keeps a lone
				// zero-length node so that a variable declared as="text()"
				// can be satisfied by it, but a document node built from the
				// same body must be childless.
				continue
			}
			// Every node becomes a child of the new document node, which
			// re-parents it, so a copy is made unconditionally. detach only
			// copies nodes that already belong to a tree, and a freshly
			// constructed parentless element would otherwise be adopted in
			// place — making "$x/lre is $y" true for a node xsl:document is
			// defined to have copied.
			if n.Kind == xdm.KindDocument {
				// A document node cannot be the child of a document node.
				// Section 5.7.1 makes it contribute its children instead —
				// the same flattening AppendNode performs for the content of
				// an element. xsl:copy-of now delivers the wrapper (11.9.1
				// says the copy of a document node is a document node), so
				// this is the point where a variable's implicit document
				// node absorbs it.
				// The absorbed document node's own tree properties come
				// with it when the destination has none of its own. The
				// DOCTYPE is what fn:unparsed-entity-uri reads, and it lives
				// on the tree rather than on the node, so absorbing only the
				// children left a variable declared as="document-node()" with
				// the right children and an empty entity table.
				if src := n.Tree(); src != nil && tree.DocType == "" {
					tree.DocType = src.DocType
				}
				if tree.Root.BaseURI == "" {
					tree.Root.BaseURI = n.BaseURI
				}
				for _, ch := range n.Children {
					appendMergingText(tree.Root, ch)
				}
				prevAtomic = false
				continue
			}
			// XDM forbids two adjacent text children, and complex content
			// is where that has to be enforced: a body of two text nodes —
			// "document {text {'te'}, text {'xt'}}", or two xsl:text
			// instructions in a variable — contributes one text child, not
			// two. AppendText already merges this way inside an element;
			// a document node is complex content by the same rule.
			appendMergingText(tree.Root, n)
			prevAtomic = false
		} else if a, ok := it.(*xdm.Atomic); ok {
			text := a.String()
			kids := tree.Root.Children
			switch {
			case prevAtomic:
				kids[len(kids)-1].Value += " " + text
			case len(kids) > 0 && kids[len(kids)-1].Kind == xdm.KindText:
				// The child beside it is text and XDM forbids two adjacent
				// text nodes, so this value joins it rather than becoming a
				// second one. It takes no separator: prevAtomic is false, so
				// the previous run ended -- at a separator text node written
				// above, or at the text a nested document node contributed,
				// which "document {'abc', document {'def'}, 'ghi'}" ends with
				// and which Constr-cont-document-5 counts as one child.
				kids[len(kids)-1].Value += text
			default:
				tree.Root.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: text})
			}
			// With a separator in force there are no runs of adjacent atomic
			// values left to merge with a space: the separator already sits
			// between them.
			prevAtomic = sep == nil
		}
	}
	tree.Finalize()
	return tree.Root
}

// Open returns the element currently being built, or nil at the top level.
//
// A caller needs it for the work that happens after the content is in place
// but before the element is finished: namespace fixup, schema validation, and
// stamping a base URI all act on the element itself rather than on what the
// builder appended to it. It is the live node, not a copy — mutating it is
// the point.
func (b *Builder) Open() *xdm.Node { return b.open }

// Items returns the accumulated items without finishing the builder.
//
// Sequence is the ordinary way to read the result. This exists for the
// section 8.4.4 algorithm, which has to look at what a constructor produced
// so far in order to decide whether xsl:on-empty fires, and then carry on
// appending to the same builder.
func (b *Builder) Items() xdm.Sequence { return b.items }

// AppendOpaque adds an item that is neither a node nor an atomic value — a
// map, an array, or a function item.
//
// Such an item is a legal member of a sequence but has no representation
// inside element content, so it is accepted at the top level and refused
// under an open element. Both languages refuse it; they differ only in the
// code, which is why the fault is reported rather than named here.
func (b *Builder) AppendOpaque(it xdm.Item) error {
	if b.open != nil {
		return b.policy.Err(FaultFunctionItem,
			fmt.Sprintf("a %s cannot be added to the content of element %s",
				it.TypeName(), b.open.Name.Lexical()))
	}
	b.lastAtomic = false
	b.items = append(b.items, it)
	return nil
}

// appendMergingText adds one child to a node being built as complex content,
// merging it into the text beside it when both are text.
//
// XDM forbids two adjacent text children, and complex content is where that
// has to be enforced: "document {text {'te'}, text {'xt'}}" contributes one
// text child, not two. The rule holds across a nested document node's
// absorption too, which is what Constr-cont-document-4 and -5 test:
// "document {'abc', 'def', document {'ghi', 'jkl'}, 'mno'}" has one child,
// and appending the absorbed children without merging gave three.
func appendMergingText(parent, n *xdm.Node) {
	if n.Kind == xdm.KindText {
		if kids := parent.Children; len(kids) > 0 &&
			kids[len(kids)-1].Kind == xdm.KindText {
			kids[len(kids)-1].Value += n.Value
			return
		}
	}
	parent.AppendChild(DeepCopy(n))
}
