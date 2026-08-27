package xslt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// runtime holds per-transform state.
//
// One runtime is created per Transform call and is never shared, which is what
// lets a single compiled Stylesheet serve concurrent transforms: everything
// mutable — the variable stack, the key index, the recursion depth — lives
// here, and the Stylesheet itself is read-only.
type runtime struct {
	sheet *Stylesheet

	// funcResults memoises the results of stylesheet functions declared
	// new-each-time="no". Section 10.3 makes that a promise that two calls
	// with the same arguments return the SAME result, which for a function
	// building nodes means the same nodes rather than merely equal ones --
	// so the promise is only kept by evaluating once and reusing. Keyed by
	// functionCallKey; see apply.go.
	funcResults map[string]xdm.Sequence
	ctx   *xpath.Context

	// globalCtx is the context as it stood once the global variables were
	// bound, holding those and nothing local. xsl:attribute-set bodies are
	// evaluated against it; see the comment where it is set.
	globalCtx *xpath.Context

	// keyIndex caches xsl:key lookups per document. Building an index is a
	// full document scan, so it is done once per (key, document) pair on
	// first use rather than eagerly for every declared key.
	keyIndex map[keyCacheKey]map[string]xdm.Sequence

	// keyBuilding marks the (key, document) pairs whose index is currently
	// being built. keyIndex is only written once a build has *finished*, so
	// a key whose match or use expression calls key() for a name already
	// under construction would re-enter the builder and recurse until the
	// depth guard fired — reporting XPDY0001 where XTDE0640 is due. 5.7:
	// "it is a non-recoverable dynamic error if the use or match attribute
	// of an xsl:key declaration contains a call to the key function".
	keyBuilding map[keyCacheKey]bool

	// accumValues caches each accumulator's value at every node of a tree,
	// and accumBuilding guards the circular case. Both mirror keyIndex and
	// keyBuilding, and for the same reason: computing one value means
	// walking everything before it, so the walk is done once per pair.
	accumValues   map[accumCacheKey]*accumulatorValues
	accumBuilding map[accumCacheKey]bool
	// accumOrigin maps a node produced by a copy-accumulators="yes" copy to
	// the node it was copied from, which is the only thing that can say what
	// an accumulator's value at the copy should be. It is a map on the
	// runtime rather than a field on the node so that copying leaves the
	// tree itself untouched.
	accumOrigin map[*xdm.Node]*xdm.Node

	// treeAccums records, per document root, which accumulators 18.2.2 makes
	// applicable to that tree. Only a document read by an
	// xsl:merge-source/@for-each-source populates it — that is the one place
	// this engine can say the set is anything narrower than "all of them" —
	// and a root that is absent from the map is unrestricted.
	//
	// The map is shared with every derived runtime because the runtime struct
	// is copied by value: a document loaded inside an xsl:merge must stay
	// restricted for the whole of the action that reads it.
	treeAccums map[*xdm.Node]*modeAccumulators

	// streamedTrees records the roots that xsl:source-document was asked to
	// read in streamed mode, which XTDE3362 bars a non-streamable accumulator
	// from being read over.
	streamedTrees map[*xdm.Node]bool

	// depth bounds apply-templates recursion, which the spec does not bound
	// and which a stylesheet with a cycle would otherwise run forever.
	depth int
	// maxDepth is the ceiling depth may reach, from TransformOptions.
	maxDepth int

	// temporary marks that the runtime is building a temporary tree — the
	// content of a variable, a function's body, or a grouping key — rather
	// than a final result tree.
	//
	// It exists for XTDE1480: xsl:result-document may not be evaluated in
	// temporary output state, because there is no final result tree for it
	// to be a sibling of. The flag is on the runtime rather than the output
	// builder because the state is inherited by everything the constructor
	// calls, however deeply.
	temporary bool

	// baseOutputURI is TransformOptions.BaseOutputURI, kept for resolving a
	// relative xsl:result-document/@href and for the value
	// fn:current-output-uri reports while the principal result is being
	// written.
	baseOutputURI string

	// secondary collects xsl:result-document outputs. Like messages it is a
	// pointer, because the runtime struct is copied on every focus change:
	// a plain slice would leave a result-document written inside a template
	// appending to a copy that the caller never sees.
	secondary *[]SecondaryResult

	// baseURIUsed records that an xsl:result-document claimed the base output
	// URI — the one an absent or empty @href names. The principal result tree
	// has that URI too, so a stylesheet that writes to both is producing two
	// documents at one URI, which is XTDE1490. It is a pointer for the same
	// reason secondary is: the runtime is copied on every focus change.
	baseURIUsed *bool

	// messages collects xsl:message output rather than writing to stderr.
	//
	// It is a pointer to a slice because the runtime struct is copied on
	// every focus change and template dispatch; a plain slice would leave
	// each copy appending to its own, and messages emitted inside a template
	// would never reach the caller.
	messages *[]string

	// warnings collects the recoverable-condition warnings xsl:mode asks for,
	// and is a pointer for the same reason messages is.
	warnings *[]string

	// tunnel holds tunnel parameters, which pass through templates that do
	// not declare them.
	tunnel map[string]xdm.Sequence

	// sel records how the currently-executing template was selected, so that
	// xsl:next-match and xsl:apply-imports in its body can resume the search
	// where it left off rather than starting over and picking the same
	// template forever.
	sel selection
}

// selection is the template-selection state of the enclosing apply-templates.
type selection struct {
	// template is the one currently running; nil outside any match template.
	template *Template
	// next is the index into Stylesheet.templates at which to resume.
	next int
	// mode, params and tunnels are carried so a resumed dispatch behaves
	// like the original one.
	mode    string
	params  map[string]xdm.Sequence
	tunnels map[string]xdm.Sequence
	// item is the item the rule matched, kept so xsl:next-match can tell
	// whether it is still looking at it. Section 6.7 makes the current
	// template rule and the context item two separate conditions, and an
	// instruction that changes the focus without ending the rule leaves the
	// first satisfied and the second not; see nextMatchInstr.Execute.
	item xdm.Item
}

type keyCacheKey struct {
	name string
	tree *xdm.Tree
}

// DefaultMaxDepth bounds template recursion when TransformOptions.MaxDepth is
// zero. It matches xdm.DefaultMaxDepth so that a document the parser accepts is
// one an identity transform can copy: the recursion counted here is the
// ordinary descent through the tree, not only a stylesheet calling itself.
const DefaultMaxDepth = 1000

func (rt *runtime) descend() error {
	rt.depth++
	if rt.maxDepth > 0 && rt.depth > rt.maxDepth {
		return fmt.Errorf("template recursion exceeded %d levels", rt.maxDepth)
	}
	return nil
}

func (rt *runtime) ascend() { rt.depth-- }

// withFocus returns a runtime whose XPath context has a new focus.
//
// The runtime struct is copied rather than mutated so that a nested
// instruction cannot disturb its caller's focus — a bug that manifests as
// sibling elements being processed against the wrong context node.
func (rt *runtime) withFocus(item xdm.Item, pos, size int) *runtime {
	n := *rt
	n.ctx = rt.ctx.WithFocus(item, pos, size)
	return &n
}

// withCurrent sets the focus and records it as the value fn:current returns.
//
// Only instructions that establish a new "node being processed" call this —
// xsl:for-each, xsl:apply-templates, xsl:for-each-group. Predicate and step
// evaluation use withFocus, which deliberately leaves current() alone.
func (rt *runtime) withCurrent(item xdm.Item, pos, size int) *runtime {
	n := *rt
	n.ctx = rt.ctx.WithFocus(item, pos, size)
	if item != nil {
		n.ctx = n.ctx.WithVar(currentVar, xdm.One(item))
	}
	return &n
}

// withSelection records the template-selection state for the body about to run.
func (rt *runtime) withSelection(t *Template, next int, mode string,
	params, tunnels map[string]xdm.Sequence) *runtime {
	n := *rt
	var item xdm.Item
	if rt.ctx != nil {
		item = rt.ctx.Item
	}
	n.sel = selection{
		template: t, next: next, mode: mode,
		params: params, tunnels: tunnels, item: item,
	}
	return &n
}

func (rt *runtime) withVar(name xdm.QName, val xdm.Sequence) *runtime {
	n := *rt
	n.ctx = rt.ctx.WithVar(name, val)
	return &n
}

// --- Output construction ----------------------------------------------------

// outputBuilder accumulates the result of a sequence constructor.
//
// XSLT output is a sequence of nodes and atomic values, not a string: an
// attribute added after an element has children is an error, adjacent text
// must be merged, and the result may be a temporary tree that later
// instructions navigate. Building a string directly would make all of that
// impossible.
type outputBuilder struct {
	items xdm.Sequence
	// open is the element currently being built, if any. Attributes and
	// namespaces are added to it until it is closed.
	open *xdm.Node
	// parent chains open elements so that nested construction works.
	parent *outputBuilder
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
}

// setItemSeparator records the item-separator that applies to the tree this
// builder produces. A nil argument leaves the default 5.7.1 rules in force.
func (b *outputBuilder) setItemSeparator(sep *string) { b.itemSep = sep }

func newOutputBuilder() *outputBuilder {
	return &outputBuilder{tree: xdm.NewTree()}
}

// appendNode adds a node to the current output position.
//
// A node that already belongs to a tree is copied first. AppendChild rewrites
// the node's Parent and tree pointers and Finalize renumbers its document
// order, so adopting a source node in place *mutates the source document* —
// evaluating an unused variable containing xsl:sequence was enough to reorder
// the input, and two goroutines transforming a shared parsed tree raced on it.
// xsl:copy-of already copied; xsl:sequence and xsl:perform-sort did not, and
// the guard belongs here where every caller is covered.
func (b *outputBuilder) appendNode(n *xdm.Node) {
	b.lastAtomic = false
	// A namespace node joins the element's bindings, not its children. Any
	// instruction may put one in the result sequence — xsl:sequence selecting
	// namespace::* is the usual way — and appending it as a child both lost
	// it and made the element look as if it already had content, so a later
	// xsl:attribute was rejected with XTDE0410.
	if n.Kind == xdm.KindNamespace && b.open != nil {
		_ = b.addNamespaceNode(n.Name.Local, n.Value)
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
		_ = b.addAttribute(n.Name, n.Value)
		return
	}
	if n.Kind == xdm.KindDocument && b.open != nil {
		// A document node used as the content of an element contributes its
		// children, not itself (5.7.1): a result tree may not contain a
		// document node below the root.
		for _, ch := range append([]*xdm.Node(nil), n.Children...) {
			b.appendNode(ch)
		}
		return
	}
	if b.open != nil {
		// Copying is only needed when the node is about to be re-parented:
		// AppendChild rewrites Parent and tree pointers, so adopting a source
		// node in place would mutate the source document.
		n = detach(n)
		rebase(n, b.open.BaseURI)
		b.open.AppendChild(n)
		return
	}
	// At the top of a sequence nothing is re-parented, so the node itself is
	// the result. xsl:sequence is defined to preserve node identity — a
	// variable declared as="item()*" holding (doc/one, doc/two) must answer
	// true to "is" against the source nodes — and copying here answered false.
	b.items = append(b.items, n)
}

// rebase recomputes the base URIs of a subtree that has just been re-parented.
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
func rebase(n *xdm.Node, parentBase string) {
	if n == nil || n.Kind != xdm.KindElement {
		return
	}
	base := parentBase
	for _, a := range n.Attrs {
		if a.Name.URI == xdm.NSXML && a.Name.Local == "base" {
			base = resolveAgainst(parentBase, a.Value)
			if a.Value != "" && base == a.Value {
				// resolveAgainst returns the reference unchanged when it
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
		rebase(ch, base)
	}
}

// rebaseDetached recomputes the base URI of a copy that has no parent.
//
// Section 11.9: "the base URI of a node is copied, except in the case of an
// element node having an xml:base attribute, in which case the base URI of
// the new node is taken as the value of the xml:base attribute, resolved if
// it is relative against the base URI of the xsl:copy/xsl:copy-of
// instruction". So an element without its own xml:base keeps the source's
// base URI unchanged — which is why this cannot just call rebase, whose
// empty-reference case makes the node inherit from its new parent.
func rebaseDetached(n *xdm.Node, instrBase string) {
	if n == nil || n.Kind != xdm.KindElement {
		return
	}
	for _, a := range n.Attrs {
		if a.Name.URI == xdm.NSXML && a.Name.Local == "base" {
			rebase(n, instrBase)
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
	return deepCopy(n)
}

// appendText adds text, merging with a preceding text node so that the XDM
// invariant of no adjacent text nodes holds in constructed trees too.
func (b *outputBuilder) appendText(s string) {
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
		// Nothing downstream is harmed by keeping them: toTree skips
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

// appendValue adds an atomic value to the output sequence.
func (b *outputBuilder) appendValue(a *xdm.Atomic) {
	// Inside an element being built, an atomic value becomes text; at the top
	// level it stays an atomic item, because xsl:sequence can return one.
	if b.open != nil {
		sep := ""
		if b.lastAtomic {
			sep = " "
		}
		b.appendText(sep + a.String())
		b.lastAtomic = true
		return
	}
	b.items = append(b.items, a)
	b.lastAtomic = true
}

// addAttribute attaches an attribute to the element under construction.
func (b *outputBuilder) addAttribute(name xdm.QName, value string) error {
	return b.addAttributeTyped(name, value, "")
}

// addAttributeTyped adds an attribute carrying a type annotation.
//
// The annotation has to travel with the attribute rather than be applied to a
// throwaway node: xsl:attribute assesses the value it is about to write, and a
// pattern such as schema-attribute(A) matches only a node that was actually
// validated against the declaration, so an attribute that lost its annotation
// on the way into the element could never match however it was named.
func (b *outputBuilder) addAttributeTyped(name xdm.QName, value string,
	typeAnnotation string) error {

	if b.open == nil {
		// A parentless attribute is a legal item in the data model, and a
		// sequence constructor may produce one: xsl:function as="attribute()"
		// with an xsl:attribute body is the ordinary way to write one, and
		// XTDE0410 is not about this at all. The error is about *ordering*
		// within element content — an attribute preceded by a node that is
		// neither an attribute nor a namespace — which is checked below where
		// there is an element to check it against.
		b.items = append(b.items, &xdm.Node{
			Kind: xdm.KindAttribute, Name: name, Value: value,
			TypeAnnotation: typeAnnotation,
		})
		return nil
	}
	// Adding an attribute after children exist is an error the spec calls out,
	// because it usually means the stylesheet's instruction order is wrong.
	if len(b.open.Children) > 0 {
		return fmt.Errorf("XTDE0410: attribute %q added after the element already has children",
			name.Lexical())
	}
	// A repeated attribute replaces the earlier one rather than duplicating.
	for _, a := range b.open.Attrs {
		if a.Name.URI == name.URI && a.Name.Local == name.Local {
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

// addNamespaceNode attaches a namespace node to the element under
// construction.
//
// It is shared by xsl:namespace and by xsl:copy-of of a namespace node: both
// add a binding to the element being built, and a namespace node appended as
// if it were a child would silently vanish from the result.
func (b *outputBuilder) addNamespaceNode(prefix, uri string) error {
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
		return fmt.Errorf(
			"XTDE0440: a default namespace cannot be declared on %s, "+
				"which is in no namespace", b.open.Name.Local)
	}
	// XTDE0430: "the result sequence contains two or more namespace nodes
	// having the same name but different string values". Re-declaring a
	// prefix to the *same* URI is harmless and common — an element and its
	// content may each ask for it — so only a conflicting one is an error.
	if was, ok := b.declared[prefix]; ok && was != uri {
		return fmt.Errorf(
			"XTDE0430: the prefix %q is bound to both %q and %q on the "+
				"same element", prefix, was, uri)
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
func (b *outputBuilder) freshPrefix(want string) string {
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

// noteDeclared records a namespace node the element was constructed with, so
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
func (b *outputBuilder) noteDeclared(prefix, uri string) {
	if b.declared == nil {
		b.declared = map[string]string{}
	}
	b.declared[prefix] = uri
}

// startElement opens a new element, returning a builder scoped to it.
func (b *outputBuilder) startElement(name xdm.QName) *outputBuilder {
	el := &xdm.Node{Kind: xdm.KindElement, Name: name}
	b.appendNode(el)
	return &outputBuilder{open: el, parent: b, tree: b.tree}
}

// sequence returns the accumulated items.
func (b *outputBuilder) sequence() xdm.Sequence { return b.items }

// toTree wraps the accumulated items in a document node, which is what a
// variable with content produces.
// toDocument is toTree with the check XTDE0420 requires.
//
// "It is a non-recoverable dynamic error if the result sequence used to
// construct the content of a document node contains a namespace node or
// attribute node." A document node has no attributes and carries no namespace
// declarations of its own, so such an item has nowhere to go: appending it
// silently discarded it, and the stylesheet saw a document that was missing
// what it had just built.
//
// It is separate from toTree because not every temporary builder becomes a
// document. A sequence constructor producing a bare attribute is legitimate —
// xsl:function as="attribute()" is written that way — and only wrapping the
// result in a document node makes it wrong.
func (b *outputBuilder) toDocument() (*xdm.Node, error) {
	for _, it := range b.items {
		n, ok := it.(*xdm.Node)
		if !ok {
			continue
		}
		switch n.Kind {
		case xdm.KindAttribute:
			return nil, fmt.Errorf(
				"XTDE0420: an attribute node (%s) cannot be the content of a "+
					"document node", n.Name.Lexical())
		case xdm.KindNamespace:
			return nil, fmt.Errorf(
				"XTDE0420: a namespace node (%s) cannot be the content of a "+
					"document node", n.Name.Local)
		}
	}
	return b.toTree(), nil
}

func (b *outputBuilder) toTree() *xdm.Node {
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
				// the same flattening appendNode performs for the content of
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
					tree.Root.AppendChild(deepCopy(ch))
				}
				prevAtomic = false
				continue
			}
			tree.Root.AppendChild(deepCopy(n))
			prevAtomic = false
		} else if a, ok := it.(*xdm.Atomic); ok {
			text := a.String()
			kids := tree.Root.Children
			switch {
			case prevAtomic:
				kids[len(kids)-1].Value += " " + text
			case sep != nil && len(kids) > 0 &&
				kids[len(kids)-1].Kind == xdm.KindText:
				// The separator just written is a text node, and XDM forbids
				// adjacent text nodes, so this value joins it rather than
				// becoming a second one.
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

// --- Instruction execution helpers -----------------------------------------

// execSequence runs a sequence constructor into out.
func execSequence(body []Instruction, rt *runtime, out *outputBuilder) error {
	// A constructor holding xsl:on-empty or xsl:on-non-empty cannot be run
	// left to right: whether either fires depends on what the whole
	// constructor produced, so it goes to the section 8.4.4 algorithm
	// instead. See onempty.go.
	if hasConditionalContent(body) {
		return execConditionalSequence(body, rt, out)
	}
	for _, instr := range body {
		if err := rt.ctx.Err(); err != nil {
			return err
		}
		// A variable declared mid-sequence is in scope for the instructions
		// that follow it, so it rebinds the runtime for the rest of the loop
		// rather than only for its own execution.
		if v, ok := instr.(*varInstr); ok {
			if v.unused {
				// Nothing after this declaration can name the variable, so
				// section 5.2's permission not to evaluate it applies and
				// the binding is skipped entirely. Forcing it here made
				// param-0301 report XTDE0640 for a circularity its own
				// comment says must not be reported, because the value the
				// variable would have taken is never demanded.
				continue
			}
			val, err := evalVariable(v.v, rt)
			if err != nil {
				return err
			}
			rt = rt.withVar(v.v.Name, val)
			continue
		}
		if err := instr.Execute(rt, out); err != nil {
			return err
		}
	}
	return nil
}

// evalVariable computes a variable's value from its select expression or its
// content.
func evalVariable(v *Variable, rt *runtime) (xdm.Sequence, error) {
	seq, err := evalVariableRaw(v, rt)
	if err != nil {
		return nil, err
	}
	// A variable or parameter whose value will not convert to its declared
	// type is XTTE0570, not the generic XPTY0004.
	return v.asType.convertAs(seq, "$"+v.Name.Lexical(), "XTTE0570")
}

// evalVariableRaw computes the value before the "as" declaration is applied.
func evalVariableRaw(v *Variable, rt *runtime) (xdm.Sequence, error) {
	if v.Select != nil {
		return v.Select.Eval(rt.ctx)
	}
	if len(v.Body) == 0 {
		// A variable with neither select nor content is the empty string, not
		// the empty sequence: this is what makes <xsl:variable name="x"/>
		// usable as "".
		//
		// With an "as" declaration the rule is the other way. Section 9.3's
		// table gives "value is an empty sequence, provided the as attribute
		// permits an empty sequence" for that row, so a zero-length string
		// would fail every declaration that is not a string type — including
		// the document-node()? that the empty body was written for.
		if v.asType != nil {
			return nil, nil
		}
		return xdm.One(xdm.NewString("")), nil
	}
	// Building a variable's content is temporary output state.
	sub := rt.temporaryOutput()
	out := newOutputBuilder()
	if err := execSequence(v.Body, sub, out); err != nil {
		return nil, err
	}
	// Section 9.3's table: with an "as" attribute the value is the sequence
	// the constructor produced, adjusted to the required type. Only *without*
	// one is a document node built to hold it.
	//
	// The difference is observable and large. as="element()*" over a body of
	// three literal elements is those three elements; wrapping them in a
	// document node made the value a single node that does not match the
	// declared type at all, so the variable failed rather than binding.
	if v.asType != nil {
		return out.sequence(), nil
	}
	// Content otherwise builds a temporary tree rooted at a document node,
	// whose base URI is the one in force at the declaration. Leaving it empty
	// made fn:base-uri return nothing for every node in a temporary tree.
	tree, err := out.toDocument()
	if err != nil {
		return nil, err
	}
	tree.BaseURI = v.baseURI
	// The document node's base is known only now, after its content was
	// built, so the children are rebased against it here rather than as they
	// were appended. Without this a copied element with a relative xml:base
	// keeps the base of the document it came from, and a copied element with
	// none keeps it too, instead of inheriting the temporary tree's.
	for _, ch := range tree.Children {
		rebase(ch, tree.BaseURI)
	}
	return xdm.One(tree), nil
}

// clearCurrentRule returns a runtime with the current template rule cleared.
//
// Section 5.2's table names what clears it: "xsl:for-each,
// xsl:for-each-group, and xsl:analyze-string, and calls on stylesheet
// functions. Also cleared while evaluating global variables or default values
// of stylesheet parameters, and the sequence constructors contained in
// xsl:key and xsl:sort."
//
// It exists for XTDE0560, which is an error "if xsl:apply-imports or
// xsl:next-match is evaluated when the current template rule is null". Both
// instructions resume the search that the current rule interrupted, and once
// an xsl:for-each has changed the focus there is no such search to resume —
// the node being processed is no longer the one any template rule matched.
func (rt *runtime) clearCurrentRule() *runtime {
	sub := *rt
	sub.sel = selection{mode: rt.sel.mode, tunnels: rt.sel.tunnels}
	return &sub
}

// temporaryOutput returns a runtime in temporary output state.
//
// XSLT 3.0 section 19.1 lists which instructions switch it on: "xsl:variable,
// xsl:param, xsl:with-param, xsl:function, xsl:key, xsl:sort,
// xsl:accumulator-rule, and xsl:merge-key always evaluate the instructions in
// their contained sequence constructor in temporary output state." Each of
// those calls this before executing its body, so that an xsl:result-document
// anywhere beneath is XTDE1480 however deep the call chain.
//
// The copy is by value because the state is a property of the evaluation, not
// of the runtime: the caller's own state must be unchanged when the body
// returns.
func (rt *runtime) temporaryOutput() *runtime {
	sub := *rt
	sub.temporary = true
	return &sub
}

// temporaryOutputBefore30 is temporaryOutput for the six instructions XSLT
// 2.0 put in that list and XSLT 3.0 took out again: xsl:attribute,
// xsl:comment, xsl:processing-instruction, xsl:namespace, xsl:value-of and
// xsl:message.
//
// All six build a string rather than a tree, so there was never a final
// result tree for a nested xsl:result-document to be written to -- which is
// what XTDE1480 is about. 3.0 decided the restriction bought nothing, since
// the nested instruction's own output goes to its own destination, and
// result-document-1130 is the stylesheet that walks all of them.
func (rt *runtime) temporaryOutputBefore30() *runtime {
	if rt.sheet != nil && (rt.sheet.maxVersion == 0 || rt.sheet.maxVersion >= 3.0) {
		return rt
	}
	return rt.temporaryOutput()
}

// stringJoin renders a sequence as a separated string, which is what
// xsl:value-of and attribute value templates produce.
func stringJoin(seq xdm.Sequence, sep string) string {
	parts := make([]string, 0, len(seq))
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			parts = append(parts, v.StringValue())
		case *xdm.Atomic:
			parts = append(parts, v.String())
		}
	}
	return strings.Join(parts, sep)
}

// constructedText renders a sequence constructor's result as the string
// content of an attribute, comment, processing instruction or namespace node.
//
// This is section 5.7.2 verbatim: zero-length text nodes are dropped,
// adjacent text nodes are merged, the sequence is atomized, and the resulting
// strings are joined with the separator. The merge step is what makes the
// separator behave as the specification's own example describes — five text
// nodes concatenate to "12345" while five atomic values become "1 2 3 4 5" —
// so it cannot be skipped by joining the raw items.
func constructedText(seq xdm.Sequence, sep string) string {
	var parts []string
	inText := false
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			if v.Kind == xdm.KindText {
				if v.Value == "" {
					continue
				}
				if inText {
					parts[len(parts)-1] += v.Value
					continue
				}
				parts = append(parts, v.Value)
				inText = true
				continue
			}
			// The sequence is atomized and every atomic value is then cast
			// to a string (XSLT 2.0 section 11.4.3). For an UNTYPED node the
			// typed value is its string value, so reading StringValue()
			// directly was right; for a schema-annotated node it is not. An
			// attribute annotated xs:integer whose lexical form is "003" has
			// the typed value 3, and casting that to a string gives "3" —
			// the canonical form — not the "003" that was written. Reading
			// the string value skipped atomization entirely and so always
			// produced the lexical form.
			for _, a := range xdm.Atomize(xdm.Sequence{v}) {
				if at, ok := a.(*xdm.Atomic); ok {
					parts = append(parts, at.String())
				}
			}
			inText = false
		case *xdm.Atomic:
			parts = append(parts, v.String())
			inText = false
		}
	}
	return strings.Join(parts, sep)
}

// newRuntime builds a runtime for one transform.
func newRuntime(s *Stylesheet, ctx context.Context, root *xdm.Node, opts TransformOptions) (*runtime, error) {
	maxDepth := opts.MaxDepth
	if maxDepth == 0 {
		maxDepth = DefaultMaxDepth
	}
	rt := &runtime{
		sheet:       s,
		maxDepth:    maxDepth,
		keyIndex:    map[keyCacheKey]map[string]xdm.Sequence{},
		keyBuilding: map[keyCacheKey]bool{},

		accumValues:   map[accumCacheKey]*accumulatorValues{},
		accumBuilding: map[accumCacheKey]bool{},
		accumOrigin:   map[*xdm.Node]*xdm.Node{},
		treeAccums:    map[*xdm.Node]*modeAccumulators{},
		streamedTrees: map[*xdm.Node]bool{},
		tunnel:        map[string]xdm.Sequence{},
		funcResults:   map[string]xdm.Sequence{},
		messages:      new([]string),
		warnings:      new([]string),
		secondary:     new([]SecondaryResult),
		baseURIUsed:   new(bool),
		baseOutputURI: opts.BaseOutputURI,
	}

	// A transform started from a named template has no source document, and
	// root is then a nil *xdm.Node. Handing that straight to NewContext puts
	// a non-nil interface holding a nil pointer in Context.Item: the "is
	// there a focus" tests all read true, and the first axis step to
	// dereference it panics instead of raising XPDY0002. The nil is widened
	// to a genuinely nil interface so that absence of a context item is
	// represented the one way the rest of the engine checks for it.
	var item xdm.Item
	if root != nil {
		item = root
	}
	// xsl:global-context-item use="absent" declares that the transformation
	// reads no global context item, so the globals are evaluated without one
	// however the transform was invoked. 3.10 leaves it open whether
	// supplying an item anyway is itself an error, and this takes the option
	// of not making it one -- but ignoring the DECLARATION is a different
	// thing from ignoring the item, and doing both left the declaration
	// meaning nothing. glob-cxt-item-003 declares use="absent" over a global
	// selecting /doc and requires that global to fail; it accepts XPDY0002,
	// which is what a global with no focus raises on its own.
	if g := s.globalContextItem; g != nil && g.decl.use == "absent" {
		item = nil
	}
	xctx := xpath.NewContext(item, s.funcs)
	// The regular-expression dialect follows the processor, not the module.
	// A pattern is a string read by fn:matches at the point of call rather
	// than by the parser, so a version="2.0" stylesheet run by a 3.0
	// processor may legitimately write "(?:...)" -- the regex-syntax set is
	// exactly that, 2.0 stylesheets scoped XSLT30+. Raising RegexVersion
	// rather than Version keeps every other 3.0 construct gated on the
	// module's own declaration, which is what the syntax rules require.
	if s.maxVersion == 0 || s.maxVersion >= 3.0 {
		xctx.RegexVersion = xpath.XPath31
		// Which functions exist follows the processor for the same reason:
		// calling one is ordinary syntax at every version, and only the name
		// has to resolve. A 3.0 processor running a version="2.0" stylesheet
		// must find fn:path for it, which accessor-050 and its siblings
		// require. The module's own version still governs the grammar.
		xctx.LibraryVersion = xpath.XPath31
	}
	xctx.Ctx = ctx
	xctx.Docs = opts.Documents
	xctx.Collections = opts.Collections
	xctx.Texts = opts.Texts
	// fn:json-to-xml with validate=true needs the schema layer to type the
	// tree it builds, and reaches it through this hook rather than by
	// importing xsd from xpath, which the dependency direction forbids. It is
	// installed unconditionally: whether the processor *can* validate is a
	// property of the processor, and this one always can — F&O 3.1 §17.5.3
	// reserves FOJS0004 for a processor that cannot. Whether the stylesheet
	// may then write "instance of element(j:map, j:mapType)" is the separate
	// question xsl:import-schema answers.
	xctx.Validator = jsonTreeValidator{}
	xctx.ImplicitTimezone = opts.ImplicitTimezone
	// The static base URI of every expression in the stylesheet. Without it
	// a relative reference in fn:doc or fn:resolve-uri has nothing to
	// resolve against when there is no context node — which is the case for
	// a transform started from a named template.
	xctx.StaticBaseURI = s.baseURI
	// One clock reading per transform, so fn:current-dateTime is stable
	// across every call the stylesheet makes.
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	xctx = xctx.WithNow(now)
	rt.ctx = xctx

	// The key() and current() functions need the runtime, so they are bound
	// per transform rather than living in the shared builtin library.
	lib := xpath.NewLibrary(s.funcs)
	registerRuntimeFuncs(lib, rt)
	registerOutputFuncs(lib)
	// The grouping, merge and position accessors go in here too, rather than
	// after the globals are bound, because a global may hold a *reference* to
	// one: for-each-group-078 writes `<xsl:variable name="f"
	// select="current-group#0"/>`, and a named function reference resolves
	// against the library in force where it is written. Registered later,
	// that was XPST0017 for a function this engine has. They read their state
	// through variable bindings that no global has yet, so one *called* from
	// a global still reports the XTDE1061 it should.
	registerGroupingFuncs(lib)
	registerMergeFuncs(lib)
	registerFormatNumber(lib, s)
	registerPositionFuncs(lib)
	rt.ctx.Funcs = lib

	// Global variables are evaluated in dependency order rather than
	// declaration order. Section 9.5 puts no ordering constraint on
	// declarations, so a global may legitimately be declared above the one it
	// refers to; evaluating in declaration order made that a spurious
	// "undeclared variable" instead of working. A variable is evaluated when
	// something needs it, and the ones nothing needs are evaluated at the end
	// so that their errors are still reported.
	// Bind the runtime before the globals are evaluated, not after. A global
	// variable's select expression may call a stylesheet function, and
	// xsl:function reaches the runtime through this binding — evaluating the
	// globals first left such a call reporting that it was made outside a
	// transform.
	rt.ctx = rt.ctx.WithVar(runtimeVar,
		xdm.One(&xdm.Opaque{Label: "runtime", Value: rt}))

	if err := rt.evalGlobals(s, opts); err != nil {
		return nil, err
	}
	// Section 10.2: only top-level variables and parameters are in scope
	// within an xsl:attribute-set declaration — a set is a declaration, not
	// part of the template that uses it, so a local variable at the point of
	// use must not be visible inside it. Snapshotting the context here, once
	// the globals are bound and before any template has pushed a local scope,
	// is what lets the set body be evaluated in that scope.
	rt.globalCtx = rt.ctx
	return rt, nil
}

// evalGlobals binds every global variable, resolving dependencies on demand.
func (rt *runtime) evalGlobals(s *Stylesheet, opts TransformOptions) error {
	byName := make(map[string]*Variable, len(s.globals))
	for _, g := range s.globals {
		if _, dup := byName[g.Name.Clark()]; !dup {
			byName[g.Name.Clark()] = g
		}
	}

	// state tracks which globals are done and which are being evaluated, so
	// that a cycle is reported rather than recursed into forever.
	const (
		pending = 0
		active  = 1
		done    = 2
	)
	state := make(map[string]int, len(s.globals))

	var bind func(g *Variable) error
	bind = func(g *Variable) error {
		key := g.Name.Clark()
		switch state[key] {
		case done:
			return nil
		case active:
			// XTDE0640: a global variable whose value depends on its own.
			return fmt.Errorf(
				"XTDE0640: global variable $%s depends on itself",
				g.Name.Lexical())
		}
		state[key] = active
		defer func() { state[key] = done }()

		// A static declaration was bound before static analysis began. Its
		// value cannot depend on anything the run supplies, and a static
		// xsl:param has already had its one chance to be set — through
		// CompileOptions.StaticParams, which is where the caller supplies a
		// value that has to be in hand before the stylesheet is analysed.
		if g.isStatic {
			rt.ctx = rt.ctx.WithVar(g.Name, g.staticValue)
			return nil
		}
		if supplied, ok := opts.Params[key]; ok {
			rt.ctx = rt.ctx.WithVar(g.Name, supplied)
			return nil
		}
		if g.Required {
			return fmt.Errorf("XTDE0050: required parameter $%s was not supplied",
				g.Name.Lexical())
		}

		// Everything this variable refers to is bound first, so that its own
		// evaluation finds each one already in the context.
		for _, dep := range globalRefs(g) {
			d, ok := byName[dep]
			if !ok {
				continue
			}
			if d == g {
				// A global variable is not in the scope of its own binding:
				// XSLT 3.0 §9.1 gives the scope of a global xsl:variable as
				// every stylesheet module in the package *except* the
				// variable's own select expression and sequence constructor.
				// A reference to the name from there is therefore a
				// reference to nothing at all, and the static error for an
				// unbound variable reference is XPST0008 rather than the
				// XTDE0640 that a genuine circularity between two distinct
				// declarations raises.
				//
				// higher-order-functions-070 writes the case that makes the
				// distinction visible: $gcd's select is an inline function
				// whose body calls $gcd, which "would make sense" as
				// recursion and is still an error because the name is not
				// in scope. The recursion below would never reach it -
				// bind() has already marked this one active - so it is
				// reported here directly.
				return fmt.Errorf(
					"XPST0008: undeclared variable $%s: a global variable is "+
						"not in scope within its own binding",
					g.Name.Lexical())
			}
			if err := bind(d); err != nil {
				return err
			}
		}

		val, err := evalVariable(g, rt)
		// globalRefs orders the obvious dependencies, but it only reads the
		// select expression: a reference reached through a sequence
		// constructor, a match pattern or the body of a stylesheet function
		// is invisible to it, and shows up here as XPST0008 for a name that
		// is in fact a declared global. Which of the two things that means
		// is decided by the state of the name it could not resolve.
		for err != nil {
			dep, ok := unresolvedGlobal(err, byName)
			if !ok {
				break
			}
			if state[dep.Name.Clark()] == active {
				// The name is a global already under evaluation further up
				// this same call chain, so its value depends on itself.
				// Section 3.10 makes a circularity in a stylesheet
				// XTDE0640, and reporting the reference as undeclared hid
				// the cycle behind a static-error code.
				return fmt.Errorf(
					"XTDE0640: global variable $%s depends on itself",
					dep.Name.Lexical())
			}
			// Not a cycle, merely an order globalRefs could not see. Bind
			// the dependency and evaluate this variable again; bind() is
			// idempotent through the done state, so the retry converges —
			// each pass either finishes or moves one more name to done.
			if berr := bind(dep); berr != nil {
				return berr
			}
			val, err = evalVariable(g, rt)
		}
		if err != nil {
			// A global xsl:param with an "as" type, no explicit default and
			// no supplied value takes the empty sequence as its default.
			// Section 10.1.1: if the empty sequence is not a valid instance
			// of the required type the parameter is treated as required, so
			// the caller supplying nothing is XTDE0610 rather than the type
			// error the conversion itself reports. This is the same rule
			// that governs template parameters in runTemplate.
			if g.IsParam && g.asType != nil && !hasExplicitDefault(g) {
				return fmt.Errorf("%s: no value was supplied for parameter $%s, "+
					"and the empty sequence is not a valid instance of %s",
					missingParamCode(rt.sheet),
					g.Name.Lexical(), g.asType.source())
			}
			// Only a failure of the *type conversion itself* becomes
			// XTTE0600. Evaluating the default can fail for reasons that
			// have nothing to do with the declared type — a schema
			// validation error inside the default's sequence constructor
			// carries its own code, and rebranding that as a type error
			// reported XTTE0600 where the suite expects XTTE1510.
			if g.IsParam && g.asType != nil &&
				strings.HasPrefix(err.Error(), "XTTE0570") {
				return fmt.Errorf("evaluating global $%s: %w",
					g.Name.Lexical(), recodeError(err, "XTTE0600"))
			}
			return fmt.Errorf("evaluating global $%s: %w", g.Name.Lexical(), err)
		}
		rt.ctx = rt.ctx.WithVar(g.Name, val)
		return nil
	}

	for _, g := range s.globals {
		if err := bind(g); err != nil {
			return err
		}
	}
	return nil
}

// unresolvedGlobal reports whether err is an XPST0008 naming a variable that
// the stylesheet does in fact declare globally, and if so returns that
// declaration.
//
// The name is recovered from the message rather than from a typed error
// because XPST0008 is raised in xpath, where nothing knows what an XSLT
// global is. A message that is not this shape, or that names something no
// global declares, yields false and is left to be reported as it stands.
func unresolvedGlobal(err error, byName map[string]*Variable) (*Variable, bool) {
	const marker = "XPST0008: undeclared variable $"
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		return nil, false
	}
	name := msg[i+len(marker):]
	if j := strings.IndexAny(name, " :\t\n"); j >= 0 {
		name = name[:j]
	}
	if name == "" {
		return nil, false
	}
	g, ok := byName[xdm.QName{Local: name}.Clark()]
	return g, ok
}

// globalRefs returns the names of the variables a global's select expression
// refers to.
//
// The scan is lexical rather than over the parsed tree. What it is for is
// *ordering*: binding a dependency before the variable that needs it. Naming
// one variable too many only evaluates something earlier than strictly
// necessary, which is harmless, and naming one too few leaves the old
// behaviour, which the cycle check still catches. A visitor over every
// expression node would be more precise and buy nothing.
//
// Names are returned unprefixed-Clark, because that is how a global is keyed
// when its name is in no namespace — the overwhelmingly common case. A
// prefixed reference simply does not match and is left to declaration order.
func globalRefs(g *Variable) []string {
	if g.Select == nil {
		return nil
	}
	src := g.Select.Source()
	var out []string
	var quote byte
	for i := 0; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c != '$' {
			continue
		}
		j := i + 1
		for j < len(src) && (src[j] == ':' || src[j] == '-' || src[j] == '_' ||
			src[j] == '.' ||
			(src[j] >= 'a' && src[j] <= 'z') ||
			(src[j] >= 'A' && src[j] <= 'Z') ||
			(src[j] >= '0' && src[j] <= '9')) {
			j++
		}
		if j > i+1 {
			out = append(out, xdm.QName{Local: src[i+1 : j]}.Clark())
		}
		i = j - 1
	}
	return out
}
