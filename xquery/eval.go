package xquery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
	"github.com/knroy/go-xml/xpath"
)

// evalContext is what a constructor needs while it runs.
type evalContext struct {
	// xp is the context expressions are evaluated in.
	xp *xpath.Context
	// sc is the static context the query was parsed under, which carries the
	// construction mode a copy is made with.
	sc *staticContext
}

// collation resolves a collation URI written on an "order by" or "group by"
// clause, resolving a relative reference against the static base URI as §5.2
// requires.
//
// The empty URI means the clause said nothing, in which case the comparison
// is made under the query's default collation — the one a "declare default
// collation" set, or nil, which every caller reads as codepoint. Falling
// straight to nil here made "group by" compare by codepoint in a query whose
// prolog had declared a case-blind default, so it formed three groups from
// "ABC", "abc" and "aBc" where distinct-values, which does consult the
// default, formed one.
func (ctx *evalContext) collation(uri string) (xpath.Collation, error) {
	if uri == "" {
		if ctx.sc == nil || ctx.sc.defaultCollation == "" {
			return nil, nil
		}
		uri = ctx.sc.defaultCollation
	}
	c, err := xpath.ResolveCollation(uri)
	if err != nil {
		return nil, fmt.Errorf("XQST0076: %v", err)
	}
	return c, nil
}

// implicitTimezone is the offset ordering and grouping compare date and time
// values against when one of them has no timezone of its own.
func (ctx *evalContext) implicitTimezone() int {
	if ctx.xp == nil {
		return 0
	}
	return ctx.xp.ImplicitTimezone
}

// languageVersion is the XPath/XQuery version expressions are judged under.
//
// Ordering needs it because which types are ordered at all is version
// dependent: the binary types carry equality only until 3.1 adds
// op:hexBinary-less-than and its siblings.
func (ctx *evalContext) languageVersion() xpath.Version {
	if ctx.xp == nil {
		return 0
	}
	return ctx.xp.Version
}

// builderRef is the builder a constructor appends to.
//
// It exists so that the nested builder StartElement returns can be threaded
// through the node tree without every eval method having to return one.
type builderRef struct{ b *xdmbuild.Builder }

// policy names the structural faults of content construction the way XQuery
// names them.
//
// The two languages sharing xdmbuild differ in five codes and one behaviour,
// and this is XQuery's half of that. The behaviour is the duplicate
// attribute: XSLT discards the earlier one silently, and XQuery raises
// XQDY0025, so this returns an error where the XSLT policy returns nil.
type policy struct{ sc *staticContext }

func (p policy) Err(f xdmbuild.Fault, detail string) error {
	switch f {
	case xdmbuild.FaultDuplicateAttribute:
		return fmt.Errorf("XQDY0025: %s", detail)
	case xdmbuild.FaultAttrAfterChild:
		return fmt.Errorf("XQTY0024: %s", detail)
	case xdmbuild.FaultAttrOnDocument:
		return fmt.Errorf("XPTY0004: %s", detail)
	case xdmbuild.FaultConflictingPrefix, xdmbuild.FaultDefaultNSOnNoNS:
		return fmt.Errorf("XQDY0102: %s", detail)
	case xdmbuild.FaultFunctionItem:
		return fmt.Errorf("XQTY0105: %s", detail)
	}
	return fmt.Errorf("XQTY0024: %s", detail)
}

// InheritNamespaces and PreserveNamespaces are the two halves of
// copy-namespaces, which vary independently and which "declare
// copy-namespaces" sets. A nil static context is the specification's default,
// which is preserve and inherit.
func (p policy) InheritNamespaces() bool  { return p.sc == nil || p.sc.inheritNS }
func (p policy) PreserveNamespaces() bool { return p.sc == nil || p.sc.preserveNS }

// PreserveTypes follows the construction mode, which "declare construction"
// sets and which defaults to preserve.
func (p policy) PreserveTypes() bool {
	return p.sc == nil || p.sc.construction == PreserveTypes
}

// DropEmptyText is true: a document node XQuery builds from zero-length
// values has no children.
//
// §3.9.1.3 removes zero-length text nodes when constructing complex content,
// and absorbing a nested document node does not exempt its children from the
// rule. Constr-docnode-nested-4 counts the text children of
// "document {'', document{''}, document {document {()}, document {''}}, ''}"
// and expects zero. XSLT answers the opposite, because it inserts the
// separating space first and that space is content.
func (policy) DropEmptyText() bool { return true }

func (n *literalText) eval(out *builderRef, ctx *evalContext) error {
	out.b.AppendText(n.text)
	return nil
}

// eval for an enclosed expression normalises its value into content, which is
// §3.9.1.3's rule and the same one XSLT applies: a node is copied in, an
// atomic value becomes text, and a run of adjacent atomic values is separated
// by single spaces. The builder does the separating, because only it knows
// what was appended last.
func (n *enclosed) eval(out *builderRef, ctx *evalContext) error {
	// §3.9.1.3 separates adjacent atomic values within the value of *one*
	// enclosed expression, so the run ends where the braces do: the items of
	// "{1,2,3}" are one value and are separated, while "<e>{1}{2}</e>" is two
	// values whose text abuts. Only the node standing for a whole "{ ... }"
	// closes the run — doing it per item would separate nothing, and not
	// doing it at all would run "{1}{2}" together into one separated pair.
	if n.braced {
		defer out.b.EndAtomicRun()
	}
	if n.items != nil {
		for _, it := range n.items {
			if err := it.eval(out, ctx); err != nil {
				return err
			}
		}
		return nil
	}
	if n.expr == nil {
		return nil
	}
	// bind rather than ctx.xp: compileExpr may have lifted an XQuery-only
	// primary out of this expression's source and left a variable reference
	// where it stood, and the value has to be bound before xpath is asked to
	// evaluate over it. Evaluating in the bare context instead reports the
	// invented variable as undeclared — which is what
	// "{<p:e/>/namespace-uri()}" in an attribute value did.
	xp, err := n.expr.bind(ctx)
	if err != nil {
		return err
	}
	seq, err := n.expr.compiled.Eval(xp)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}

// sequence evaluates an enclosed expression to a flat sequence, for the
// callers that need its value rather than its contribution to a tree.
func (n *enclosed) sequence(ctx *evalContext) (xdm.Sequence, error) {
	if n.expr != nil {
		xp, err := n.expr.bind(ctx)
		if err != nil {
			return nil, err
		}
		return n.expr.compiled.Eval(xp)
	}
	if n.items == nil {
		return nil, nil
	}
	// A node that has a value of its own answers with it directly. Routing it
	// through a builder would be lossy in exactly the way that matters here:
	// the builder's job is to turn a sequence into content, so it converts an
	// atomic value to text and merges it with its neighbours. "{ switch (1)
	// case 1 return 2 default return 3 }" is the xs:integer 2, not the text
	// node "2", and "instance of xs:integer" in the suite asks the question.
	if len(n.items) == 1 {
		if v, ok := n.items[0].(valueNode); ok {
			return v.sequence(ctx)
		}
	}
	inner := xdmbuild.New(policy{sc: ctx.sc})
	ref := &builderRef{b: inner}
	for _, it := range n.items {
		if err := it.eval(ref, ctx); err != nil {
			return nil, err
		}
	}
	return inner.Sequence(), nil
}

// valueNode is a node whose value is a sequence rather than a contribution to
// a tree.
//
// The XQuery-only expression forms are all of this kind: a switch returns
// whatever its chosen clause returns, which may be an atomic value, a
// function item or a map — none of which survives a round trip through the
// content builder. A constructor is deliberately not one of these: its value
// really is the node it builds, and the builder is how it is built.
type valueNode interface {
	node
	sequence(ctx *evalContext) (xdm.Sequence, error)
}

func appendSequence(out *builderRef, seq xdm.Sequence, sc *staticContext) error {
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			// XQTY0024: an attribute node in element content must come before
			// any child that is not one. The builder routes an attribute in a
			// sequence to the element's attributes and, for XSLT, deliberately
			// ignores the ordering complaint — §5.7.1 prepends such nodes to
			// the content rather than refusing them. XQuery does refuse them,
			// and the check has to happen here because AppendNode reports
			// nothing to a caller.
			if v.Kind == xdm.KindAttribute {
				if el := out.b.Open(); el != nil {
					if len(el.Children) > 0 {
						return fmt.Errorf("XQTY0024: the attribute %s follows a "+
							"node that is not an attribute in the content of "+
							"element %s", v.Name.Lexical(), el.Name.Lexical())
					}
					// The attribute is added here rather than through
					// AppendNode, which discards what AddAttribute returns
					// because XSLT has nothing to do with it: the fault it
					// reports is the duplicate attribute, and for XQuery the
					// policy turns that into XQDY0025 (§3.9.1.3). Two
					// attributes of the same name reaching element content in
					// a sequence — "<a>{$attr1, $attr2}</a>" — is exactly the
					// shape the suite tests, and the error has to reach the
					// caller to be raised at all.
					if err := out.b.AddAttributeTyped(
						v.Name, v.Value, v.TypeAnnotation); err != nil {
						return err
					}
					continue
				}
			}
			before := 0
			if el := out.b.Open(); el != nil {
				before = len(el.Children)
			}
			// The namespaces in scope at the *source* have to be read before
			// the node is appended: appending re-parents a copy of it, and
			// from there the ancestors that supplied most of those bindings
			// are no longer reachable.
			var srcScope map[string]string
			if v.Kind == xdm.KindElement && out.b.Open() != nil {
				srcScope = v.InScopeNamespaces()
			}
			if out.b.Open() != nil {
				// §3.9.1.3 step 4: a node in the content sequence
				// contributes a *copy* of itself, with a new identity, and
				// that is so however the node came about. The builder copies
				// what already belongs to a tree, because re-parenting would
				// otherwise mutate the source; a freshly constructed
				// parentless node is safe to adopt and was adopted, which is
				// right for XSLT's sequence rules and wrong here.
				// Constr-cont-nodeid-1 binds <a/> to $x, puts $x in
				// <elem>{$x}</elem>, and requires the child not to be $x.
				v = xdmbuild.DeepCopy(v)
			}
			out.b.AppendNode(v)
			if el := out.b.Open(); el != nil && len(el.Children) > before {
				applyCopyNamespaces(el.Children[len(el.Children)-1], srcScope, sc)
			}
		case *xdm.Atomic:
			out.b.AppendValue(v)
		case *xdm.ArrayItem:
			// §3.9.1.3 step 1 applies fn:data to the content sequence, and an
			// array does have a typed value: it is the concatenation of the
			// typed values of its members. So an array in element content
			// contributes its members, not XQTY0105 — "<a>{['a',['b','c'],
			// 'd']}</a>" is "<a>a b c d</a>", with the members flattened
			// through the nesting and separated as adjacent atomic values
			// always are. A map has no typed value and stays refused, which
			// is why this is not simply an atomization of the whole sequence.
			//
			// This is the complex-content rule and applies only where complex
			// content is being constructed. At the top of a sequence there is
			// none, and the array is an ordinary item there: a query whose
			// whole body is "[1, 2]" returns one array, not two integers.
			if out.b.Open() == nil {
				if err := out.b.AppendOpaque(it); err != nil {
					return err
				}
				continue
			}
			flat := xdm.Flatten(xdm.One(v))
			if len(flat) == 1 && flat[0] == it {
				// Flatten leaves an array it cannot reduce alone. Refusing it
				// here keeps the guarantee that this branch terminates.
				if err := out.b.AppendOpaque(it); err != nil {
					return err
				}
				continue
			}
			if err := appendSequence(out, flat, sc); err != nil {
				return err
			}
		default:
			if err := out.b.AppendOpaque(it); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyCopyNamespaces enforces the two halves of copy-namespaces on a subtree
// that has just been copied into the element being constructed (§3.9.1.3).
//
// Neither half is free, and the defaults are not the do-nothing case they
// look like. Re-parenting a copy loses the bindings its ancestors supplied and
// silently grants it the destination's, so preserve and inherit are as much
// work as their opposites — they are the work of putting back what the copy
// should have kept.
//
// preserve restores the in-scope namespaces the original had. They are read
// before the append, because afterwards the ancestors that supplied most of
// them are no longer reachable from the copy.
//
// no-preserve instead drops the namespace nodes the copied element carried,
// keeping the ones its own name and its attributes' names still need — a copy
// whose name became unresolvable would not be a node at all. copynamespace-16
// is the case: three nested constructors each declaring a prefix, and only the
// innermost element's own bindings survive to the innermost copy.
//
// The two are exclusive; inherit and no-inherit then apply on top of either.
//
// no-inherit stops the copy from picking up what is in scope at the
// destination, which re-parenting otherwise gives it for free. There is no way
// to *not* inherit in the data model, so the bindings are undeclared on the
// copy's root instead, which is what the namespace axis then reports.
// copynamespace-17 asks for both and expects the copy to be left with the xml
// binding alone.
func applyCopyNamespaces(n *xdm.Node, srcScope map[string]string, sc *staticContext) {
	if n == nil || n.Kind != xdm.KindElement {
		return
	}
	preserve := sc == nil || sc.preserveNS
	inherit := sc == nil || sc.inheritNS
	if preserve {
		preserveScope(n, srcScope)
	} else {
		stripNamespaces(n)
	}
	if inherit {
		return
	}
	// The bindings to undeclare are the ones the *parent* supplies; the
	// copy's own, whether original or just rebuilt above, are not inherited
	// and must survive. An undeclaration is written as an empty URI, which
	// InScopeNamespaces and LookupPrefix both read as "not in scope here".
	own := map[string]bool{}
	for _, ns := range n.Namespaces {
		own[ns.Name.Local] = true
	}
	if n.Parent != nil {
		for prefix := range n.Parent.InScopeNamespaces() {
			if prefix == "xml" || own[prefix] {
				continue
			}
			n.AddNamespace(prefix, "")
		}
	}
}

// preserveScope gives a copied element the namespace nodes it had at the
// source but that its new position no longer supplies.
//
// copy-namespaces preserve means the copy keeps "all the in-scope namespaces
// of the original element", and most of those are usually not on the element
// itself — they are on ancestors the copy has left behind. Re-parenting alone
// therefore loses them: Constr-inscope-7 copies <foo:child3/> out of a parent
// that declared foo, and the copy arrived with the right namespace URI on its
// name and no binding for it anywhere.
//
// Only the bindings the destination does not already agree with are written,
// so a copy landing where the same prefix means the same thing stays clean.
// A binding the copy declares itself wins over the source scope, which is
// what an undeclaration on the copied element means.
func preserveScope(n *xdm.Node, srcScope map[string]string) {
	if len(srcScope) == 0 {
		return
	}
	own := map[string]bool{}
	for _, ns := range n.Namespaces {
		own[ns.Name.Local] = true
	}
	for prefix, uri := range srcScope {
		if prefix == "xml" || own[prefix] {
			continue
		}
		if cur, ok := n.LookupPrefix(prefix); ok && cur == uri {
			continue
		}
		n.AddNamespace(prefix, uri)
	}
	// A default namespace the source did not have, but the destination
	// supplies, would silently move the copy into it. Constr-inscope-10 is
	// the case: <child2> in no namespace copied under a <new> that declares
	// one, and the expected result carries the xmlns="" that keeps it out.
	//
	// Only a copy whose own name is unprefixed is at that risk. The default
	// namespace applies to unprefixed element names and to nothing else, so
	// a copy named with a prefix -- <bar:b> under an <a xmlns="http://foo">
	// -- is not moved by the destination's default binding, and undeclaring
	// it would instead *take away* a binding copy-namespaces inherit says
	// the copy is entitled to. §4.8: with inherit, "the copied nodes retain
	// ... the in-scope namespaces of the construction". functx-change-
	// element-ns-all copies <bar:b> into an element in a default namespace
	// and expects <bar:b xmlns:bar="http://bar"> with no undeclaration.
	if n.Name.Prefix == "" && n.Name.URI == "" {
		if _, hadDefault := srcScope[""]; !hadDefault && !own[""] {
			if uri, ok := n.LookupPrefix(""); ok && uri != "" {
				n.AddNamespace("", "")
			}
		}
	}
}

// stripNamespaces rebuilds a subtree's namespace nodes as the smallest set its
// names require, which is what copy-namespaces no-preserve asks for.
//
// "Only those namespace bindings that are used in the names of the element and
// its attributes" survive. The set is recomputed per element rather than
// filtered down the tree, because a descendant may need a prefix its ancestor
// does not: dropping the ancestor's binding and stopping there would leave the
// descendant's name pointing at nothing.
func stripNamespaces(n *xdm.Node) {
	if n.Kind != xdm.KindElement {
		return
	}
	need := map[string]string{}
	if n.Name.URI != "" && n.Name.URI != xdm.NSXML {
		need[n.Name.Prefix] = n.Name.URI
	}
	for _, a := range n.Attrs {
		if a.Name.URI != "" && a.Name.URI != xdm.NSXML {
			need[a.Name.Prefix] = a.Name.URI
		}
	}
	kept := n.Namespaces[:0]
	for _, ns := range n.Namespaces {
		if uri, ok := need[ns.Name.Local]; ok && uri == ns.Value {
			kept = append(kept, ns)
			delete(need, ns.Name.Local)
		}
	}
	n.Namespaces = kept
	// A name whose binding was never on this element in the first place — it
	// came from an ancestor that the copy has left behind — still needs one,
	// or the copy would carry a prefix bound to nothing.
	for prefix, uri := range need {
		n.AddNamespace(prefix, uri)
	}
	for _, ch := range n.Children {
		stripNamespaces(ch)
	}
}

func (n *element) eval(out *builderRef, ctx *evalContext) error {
	name := n.name
	if n.nameExpr != nil {
		var err error
		name, err = evalNodeName(n.nameExpr, ctx, true)
		if err != nil {
			return err
		}
		if err := checkElementName(name); err != nil {
			return err
		}
	}
	sub := &builderRef{b: out.b.StartElement(name)}
	if el := sub.b.Open(); el != nil && n.baseURI != "" {
		el.BaseURI = n.baseURI
	}
	// §3.9.3.1: the in-scope namespaces of a constructed element include a
	// binding for its own name. A direct constructor writes that binding as
	// an xmlns attribute and it arrives below; a computed one has nowhere to
	// write it, so the name's own prefix is bound here. Without it the
	// element carried a namespace URI that nothing declared, and serializing
	// "declare namespace foo='...'; element foo:e {}" lost the xmlns
	// altogether. The default element namespace goes in the same way, under
	// the empty prefix.
	if el := sub.b.Open(); el != nil && name.URI != "" &&
		el.InScopeNamespaces()[name.Prefix] != name.URI {
		// The element's own name, so this binding must not be recorded as a
		// namespace node the content produced -- see AddOwnNameNamespace.
		if err := sub.b.AddOwnNameNamespace(name.Prefix, name.URI); err != nil {
			return err
		}
	}
	// Namespace declarations are applied before anything else, so that they
	// are in scope for the attributes and the content.
	for _, ns := range n.namespaces {
		if ns.prefix == "" && ns.uri == "" {
			// xmlns="" undeclares the default namespace rather than binding
			// one. There is no namespace node to add — the element is in no
			// namespace and its unprefixed children resolve to none either,
			// which is what having no default binding already means. Handing
			// it to AddNamespace would hit the rule against declaring a
			// default namespace on an element that is in none, and report as
			// an error the one case where writing it is exactly right.
			continue
		}
		if err := sub.b.AddNamespace(ns.prefix, ns.uri); err != nil {
			return err
		}
		sub.b.NoteDeclared(ns.prefix, ns.uri)
	}
	// The namespace declaration attributes of enclosing direct constructors
	// are in scope here too (§3.9.1.3), so a binding an ancestor does not
	// already supply is written on this element. A constructed element is
	// often asked about before it is attached to anything — count(in-scope-
	// prefixes(<e/>)) inside <e xmlns:p="..."> is exactly that — and a node
	// with no parent cannot inherit through a tree it is not yet in.
	//
	// Bindings an ancestor already carries are left alone, so an element that
	// *is* nested in the tree does not repeat its parent's declarations.
	if el := sub.b.Open(); el != nil {
		for _, prefix := range sortedPrefixes(n.inherited) {
			uri := n.inherited[prefix]
			if cur, ok := el.LookupPrefix(prefix); ok && cur == uri {
				continue
			}
			if err := sub.b.AddNamespace(prefix, uri); err != nil {
				return err
			}
		}
	}
	if err := declareOwnName(sub.b); err != nil {
		return err
	}
	for _, a := range n.attrs {
		if err := a.eval(sub, ctx); err != nil {
			return err
		}
	}
	// The attributes are on the node now, so their name fixups are too, and
	// what this element genuinely needs is finally known. Anything its parent
	// supplies beyond that and beyond the declaration attributes has to be
	// undeclared here -- see limitInherited.
	limitInherited(sub.b.Open(), n.inherited)
	// §3.9.1.3: the base URI of a constructed element is the static base URI
	// of the constructor *unless* the element carries an xml:base attribute,
	// in which case it is that attribute resolved against the base URI in
	// force at the parent. The attribute is only known once the attributes
	// have been evaluated — a computed one is not even a constant — so the
	// stamp above is a provisional answer and this is the final one. Without
	// it base-uri(<e xml:base="http://example.com/"/>) answered the query's
	// own base URI, or nothing, which is wrong in both directions.
	rebase := func() {
		el := sub.b.Open()
		if el == nil {
			return
		}
		parent := n.baseURI
		if el.Parent != nil {
			// A nested constructor resolves against the base URI its
			// enclosing element ended up with, not against the static one:
			// <e xml:base="http://a.example/x/"><b xml:base="y"/></e> makes
			// b's base http://a.example/x/y.
			if pb := inheritedBase(el.Parent); pb != "" {
				parent = pb
			}
		}
		xdmbuild.Rebase(el, parent)
	}
	// Before the content, so that a constructor nested in it resolves against
	// the base URI this element has actually settled on.
	rebase()
	for _, c := range n.content {
		if err := c.eval(sub, ctx); err != nil {
			return err
		}
	}
	// And again after it, because a computed constructor has no attribute
	// list of its own: "element e { attribute xml:base {...} }" writes the
	// attribute as content, so the first pass had nothing to see and the
	// element kept the static base URI. Constr-compelem-baseuri-1 is that
	// query, and answered the empty string.
	rebase()
	return nil
}

// limitInherited undeclares, on a freshly constructed element, every binding
// its constructed parent supplies that §3.9.1.3 does not pass down.
//
// The rule is narrow and easy to miss. Of everything in scope on a constructed
// element, only one kind reaches the elements constructed inside it:
//
//	"The in-scope namespaces property of the constructed element consists of
//	 ... namespace bindings for the namespace declaration attributes of this
//	 element and of its ancestors in the constructed tree ..."
//
// The other bindings a constructed element carries are *namespace fixup*
// (§3.9.1.1): the binding its own name needs, and one per prefixed attribute
// name. Those exist so the element is serialisable and its own names resolve.
// They are not declaration attributes and they do not descend.
//
// The engine's in-scope answer is a walk up the XDM tree, which cannot tell
// the two kinds apart -- a namespace node is a namespace node. So the
// separation has to be written into the tree, and the only way the data model
// offers is an undeclaration (an empty URI) on the child, which is exactly
// what the namespace axis and fn:in-scope-prefixes then report.
//
// K2-NameTest-30 is the case that pins it down: <e a:n1="c" b:n1="c"> binds
// both a and b, because its own attribute names require both, and yet the
// test requires that on the child <a:n1/> the prefix a is in scope and b is
// not. Nothing but the element's own needs distinguishes them, so a fixup
// binding cannot be inherited. cbcl-directconelem-001/002 assert the same
// split from the other side, and assert it identically under no-inherit and
// inherit -- copy-namespaces governs *copied* nodes, not the constructed
// parent-child relationship, so it has no say here.
//
// What the child keeps is therefore: the declaration attributes in scope
// (inherited, which the caller has already written), the bindings its own
// name and attribute names need (already on the node from fixup), and xml,
// which XML Names binds everywhere and which can never be undeclared.
func limitInherited(el *xdm.Node, inherited map[string]string) {
	if el == nil || el.Parent == nil || el.Parent.Kind != xdm.KindElement {
		return
	}
	// Bindings this element already carries -- fixup for its own name and its
	// attributes' names, plus the declaration attributes written above --
	// shadow the parent's and need no undeclaration.
	own := map[string]bool{}
	for _, ns := range el.Namespaces {
		own[ns.Name.Local] = true
	}
	// A prefix a name on this element needs, that the parent happens to
	// supply with the same URI, has no namespace node here: fixup skipped it
	// precisely because the parent already agreed. Undeclaring it would break
	// the name, so it counts as needed rather than inherited.
	need := map[string]string{}
	if el.Name.URI != "" {
		need[el.Name.Prefix] = el.Name.URI
	}
	for _, a := range el.Attrs {
		if a.Name.URI != "" && a.Name.Prefix != "" {
			need[a.Name.Prefix] = a.Name.URI
		}
	}
	for prefix, uri := range el.Parent.InScopeNamespaces() {
		switch {
		case prefix == "xml", own[prefix]:
			continue
		case inherited[prefix] == uri:
			// A declaration attribute in scope here. It descends, and the
			// parent already carries it, so nothing to write.
			continue
		case need[prefix] == uri:
			continue
		}
		el.AddNamespace(prefix, "")
	}
}

// declareOwnName gives the element under construction a namespace node for
// the binding its own name needs.
//
// §3.9.1.1's namespace fixup: "for each element node in the constructed
// content, a namespace binding must be present for the prefix and namespace
// URI of the element name". The element's name carries a prefix and a URI
// resolved from the static context at the constructor, but that context is
// not the constructed tree, so nothing has yet put the binding *on the node*.
// Without it in-scope-prefixes() answered one short and the element serialised
// as <foo:bar> with no xmlns:foo at all — not a document any parser reads back.
//
// Only the element's own name is fixed up here. The static context typically
// binds a good deal more — the eight predeclared prefixes, plus whatever the
// prolog declared — and §3.9.1.1 does not put those on the node:
// Constr-inscope-13 declares foo, never uses it, and expects a bare <new/>,
// while K2-DirectConElemNamespace-58 writes <xs:element/> and requires the
// xmlns:xs the *name* needs. A prefix reaches the result by being used, not by
// being in scope. Attributes are fixed up separately, as they are added.
//
// A binding an ancestor already supplies is left alone, so that a tree of
// elements in one namespace does not carry a redundant declaration on every
// node.
func declareOwnName(b *xdmbuild.Builder) error {
	el := b.Open()
	if el == nil {
		return nil
	}
	// The xml prefix is bound everywhere by XML Names itself and is never
	// declared; K2-DirectConElemNamespace-58 writes <xml:element/> and
	// expects no declaration on it.
	if el.Name.URI == xdm.NSXML {
		return nil
	}
	if el.Name.URI == "" {
		// An element in no namespace under an ancestor with a default
		// namespace has to undeclare it, or reading the result back puts the
		// element in the ancestor's namespace — a different element from the
		// one constructed. K2-DirectConElemNamespace-52 is exactly this:
		// a default namespace on <a>, <e xmlns=""/> inside it.
		//
		// The node is written straight onto the element rather than through
		// the builder's AddNamespace, whose rule against declaring a default
		// namespace on an element in no namespace (XQDY0102) is about
		// *binding* one and would reject the undeclaration that is precisely
		// right here.
		if el.Name.Prefix != "" {
			return nil
		}
		if uri, ok := el.LookupPrefix(""); ok && uri != "" {
			el.AddNamespace("", "")
		}
		return nil
	}
	if uri, ok := el.LookupPrefix(el.Name.Prefix); ok && uri == el.Name.URI {
		return nil
	}
	// The element's own name, so AddOwnNameNamespace rather than
	// AddNamespace: the binding is not a namespace node the content
	// produced, and recording it as one made a later "namespace p {...}" for
	// the same prefix look like two conflicting namespace nodes instead of
	// the rename §3.9.3.1 calls for.
	return b.AddOwnNameNamespace(el.Name.Prefix, el.Name.URI)
}

// inheritedBase returns the base URI in force at a node, which is the nearest
// one stamped on it or on an ancestor. A node the builder has not stamped —
// every node without an xml:base of its own — takes its parent's.
func inheritedBase(n *xdm.Node) string {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.BaseURI != "" {
			return cur.BaseURI
		}
	}
	return ""
}

// eval for an attribute joins its parts into one string.
//
// §3.9.1.1 is precise about the joining: each enclosed expression is atomised,
// its values are cast to strings and separated by single spaces, and the
// results of adjacent parts are concatenated with no separator at all. So
// id="a{1,2}b" is "a1 2b" rather than "a 1 2 b".
func (a *attribute) eval(out *builderRef, ctx *evalContext) error {
	name := a.name
	if a.nameExpr != nil {
		var err error
		name, err = evalNodeName(a.nameExpr, ctx, false)
		if err != nil {
			return err
		}
	}
	if err := checkAttributeName(name); err != nil {
		return err
	}
	var sb strings.Builder
	if a.computed {
		// A computed constructor's content is one enclosed expression, and
		// parseBracedContent hands it over already split at the top-level
		// commas. §3.9.3.3 atomises the whole of it as a single sequence and
		// separates the values with single spaces, so the items are gathered
		// and joined once — joining each on its own would drop the separator
		// between them, and drop an empty or node-valued item entirely.
		var seq xdm.Sequence
		for _, part := range a.value {
			switch v := part.(type) {
			case *literalText:
				seq = append(seq, xdm.NewString(v.text))
			case *enclosed:
				s, err := v.sequence(ctx)
				if err != nil {
					return err
				}
				seq = append(seq, s...)
			default:
				// A constructor among the items: "attribute a {1,<b/>,2}" is
				// three values, and the middle one is a node. It atomises to
				// the empty string but is still a value, so it takes a
				// separator on each side and must not be skipped — which is
				// what ignoring the unrecognised part did, giving "1 2" where
				// the answer is "1  2".
				inner := xdmbuild.New(policy{sc: ctx.sc})
				if err := v.eval(&builderRef{b: inner}, ctx); err != nil {
					return err
				}
				seq = append(seq, inner.Sequence()...)
			}
		}
		s, err := joinAtomized(seq)
		if err != nil {
			return err
		}
		sb.WriteString(s)
		return out.b.AddAttribute(name, sb.String())
	}
	// A direct constructor's value alternates literal runs and enclosed
	// expressions — id="a{$x}b" is three parts — and §3.9.1.1 concatenates
	// the parts with no separator while separating the values *within* each
	// enclosed expression. So each part is joined on its own here.
	for _, part := range a.value {
		switch v := part.(type) {
		case *literalText:
			sb.WriteString(v.text)
		case *enclosed:
			seq, err := v.sequence(ctx)
			if err != nil {
				return err
			}
			s, err := joinAtomized(seq)
			if err != nil {
				return err
			}
			sb.WriteString(s)
		}
	}
	return out.b.AddAttribute(name, sb.String())
}

// joinAtomized atomises a sequence and joins it with single spaces, which is
// what an attribute value and a computed text constructor both want.
func joinAtomized(seq xdm.Sequence) (string, error) {
	atoms, err := xdm.AtomizeChecked(seq)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(atoms))
	for _, it := range atoms {
		a, ok := it.(*xdm.Atomic)
		if !ok {
			return "", fmt.Errorf(
				"XPTY0004: %s cannot be used as a string", it.TypeName())
		}
		parts = append(parts, a.String())
	}
	return strings.Join(parts, " "), nil
}

func (n *comment) eval(out *builderRef, ctx *evalContext) error {
	text, _, err := contentString(n.content, ctx)
	if err != nil {
		return err
	}
	if strings.Contains(text, "--") || strings.HasSuffix(text, "-") {
		return fmt.Errorf("XQDY0072: a comment may not contain %q "+
			"or end with %q", "--", "-")
	}
	out.b.AppendNode(&xdm.Node{Kind: xdm.KindComment, Value: text})
	return nil
}

func (n *pi) eval(out *builderRef, ctx *evalContext) error {
	target := n.target
	if n.targetExpr != nil {
		t, err := evalPITarget(n.targetExpr, ctx)
		if err != nil {
			return err
		}
		target = t
	}
	// §3.9.3.5: the target may not be "xml" in any combination of case, which
	// XML itself reserves. XQDY0064 is a dynamic error, so it is raised here
	// for a target written as a name as much as for a computed one — a try
	// catches it either way. The *direct* constructor "<?xml ...?>" is
	// different: there the target is markup rather than an expression, and
	// XPST0003 refuses it while parsing.
	if strings.EqualFold(target, "xml") {
		return fmt.Errorf(
			"XQDY0064: %q is not a legal processing-instruction target",
			target)
	}
	text, _, err := contentString(n.content, ctx)
	if err != nil {
		return err
	}
	// §3.9.3.5: leading whitespace is stripped from the content. XML has no
	// way to write it — the target and the data are separated by whitespace,
	// so a leading space would be reread as part of that separator — and the
	// data model therefore does not admit it.
	text = strings.TrimLeft(text, " \t\r\n")
	if strings.Contains(text, "?>") {
		return fmt.Errorf(
			"XQDY0026: a processing instruction may not contain %q", "?>")
	}
	out.b.AppendNode(&xdm.Node{Kind: xdm.KindPI,
		Name: xdm.QName{Local: target}, Value: text})
	return nil
}

// evalPITarget computes the target of a processing-instruction constructor.
//
// §3.9.3.5 splits the failure in two, and the split is what the suite tests.
// The atomised name must be a single xs:string, xs:untypedAtomic or xs:NCName
// — anything else, including an xs:anyURI, an xs:integer, a duration or a
// sequence that is not of length one, is a *type* error, XPTY0004, because
// the value could never have been a target. Only once the value is of the
// right type does its spelling matter, and a string of the right type that
// is not an NCName is the dynamic error XQDY0041.
func evalPITarget(e *compiledExpr, ctx *evalContext) (string, error) {
	// bind, for the reason evalNodeName gives: a target expression holding a
	// lifted XQuery-only primary needs the context that installs the
	// "local:xq-stepN()" function standing in for it.
	xp, err := e.bind(ctx)
	if err != nil {
		return "", err
	}
	seq, err := e.compiled.Eval(xp)
	if err != nil {
		return "", err
	}
	atoms, err := xdm.AtomizeChecked(seq)
	if err != nil {
		return "", err
	}
	if len(atoms) != 1 {
		return "", fmt.Errorf("XPTY0004: the target of a processing " +
			"instruction must be a single string")
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return "", fmt.Errorf("XPTY0004: %s cannot be the target of a "+
			"processing instruction", atoms[0].TypeName())
	}
	switch a.Type {
	case xdm.TypeString, xdm.TypeUntypedAtomic:
		// xs:NCName is a subtype of xs:string and arrives as one, so the two
		// cases the grammar names are the two the model has.
	default:
		return "", fmt.Errorf("XPTY0004: %s cannot be the target of a "+
			"processing instruction", a.TypeName())
	}
	// The value came from an expression rather than from the query text, so
	// nothing has trimmed it: " name " names the target "name", the way the
	// name of a computed element does.
	target := strings.TrimSpace(a.String())
	if !xdm.IsNCName(target) {
		return "", fmt.Errorf(
			"XQDY0041: %q is not a valid processing-instruction target",
			target)
	}
	return target, nil
}

func (n *textNode) eval(out *builderRef, ctx *evalContext) error {
	text, empty, err := contentString(n.content, ctx)
	if err != nil {
		return err
	}
	// §3.9.3.3: a text constructor whose content expression is the empty
	// sequence produces no node at all. The test is on the sequence and not
	// on the string it joins to: "text {()}" yields nothing, while
	// "text {''}" yields a text node whose string value happens to be empty.
	if empty {
		return nil
	}
	// The node goes in as text rather than as a ready-made node so that the
	// builder applies §3.9.1.3's two rules about text in complex content:
	// adjacent text nodes are merged, and a zero-length one is dropped. Both
	// are conditional on there being a parent, which is what tells apart
	// "count(text {''})", one free-standing node, from
	// "count(element e {text {''}}/text())", which is none.
	out.b.AppendText(text)
	return nil
}

func (n *document) eval(out *builderRef, ctx *evalContext) error {
	inner := xdmbuild.New(policy{sc: ctx.sc})
	sub := &builderRef{b: inner}
	for _, c := range n.content {
		if err := c.eval(sub, ctx); err != nil {
			return err
		}
	}
	doc, err := inner.ToDocument()
	if err != nil {
		return err
	}
	// §3.9.3.1: a constructed document node takes the base URI of the
	// constructor. ToDocument may already have lifted a base URI off the
	// document element (builder.go), and that one wins, being the resolved
	// xml:base of the content rather than the constructor's own.
	if doc != nil && doc.BaseURI == "" && n.baseURI != "" {
		doc.BaseURI = n.baseURI
	}
	out.b.AppendNode(doc)
	return nil
}

// contentString evaluates content that becomes a string rather than nodes,
// which is what a comment, a processing instruction and a text constructor
// each hold.
func contentString(content []node, ctx *evalContext) (string, bool, error) {
	// The content is one sequence, and §3.9.3 joins it once: the values of
	// "text {1,2}" are separated by a single space, and so are the values of
	// "text {1}, {2}". Joining each item on its own would lose the separator
	// between them, so the whole content is flattened first.
	var seq xdm.Sequence
	for _, c := range content {
		switch v := c.(type) {
		case *literalText:
			seq = append(seq, xdm.NewString(v.text))
		case *enclosed:
			s, err := v.sequence(ctx)
			if err != nil {
				return "", false, err
			}
			seq = append(seq, s...)
		default:
			// A constructor written directly in the content — "text {1,<a/>}"
			// — is an item of the content sequence like any other. §3.9.3.1
			// atomises that sequence, and a constructed node atomises to its
			// string value, so there is nothing here to refuse: the item's
			// value is taken rather than its contribution to a tree.
			s, err := asEnclosed(c).sequence(ctx)
			if err != nil {
				return "", false, err
			}
			seq = append(seq, s...)
		}
	}
	str, err := joinAtomized(seq)
	return str, len(seq) == 0, err
}

// evalNodeName computes the name of a computed constructor.
//
// §3.9.3.1: the value is atomised and must be a QName, a string or an
// untypedAtomic. A string is resolved against the statically known
// namespaces, taking the default element namespace when it has no prefix and
// the constructor makes an element. Failure here is dynamic — XQDY0074 —
// where the same failure in a direct constructor is static, because a direct
// constructor's name is written in the query and a computed one's is not.
func evalNodeName(e *compiledExpr, ctx *evalContext, isElement bool) (xdm.QName, error) {
	// Through bind, not the raw context: a name expression holding an
	// XQuery-only primary was compiled by lifting that primary out into a
	// "local:xq-stepN()" call, and the function behind that call exists only
	// in the context bind builds. Evaluating against ctx.xp reached xpath
	// with the call still in the expression and nothing to answer it, which
	// surfaced as XPST0017 naming a function the query never wrote.
	xp, err := e.bind(ctx)
	if err != nil {
		return xdm.QName{}, err
	}
	seq, err := e.compiled.Eval(xp)
	if err != nil {
		return xdm.QName{}, err
	}
	atoms, err := xdm.AtomizeChecked(seq)
	if err != nil {
		return xdm.QName{}, err
	}
	if len(atoms) != 1 {
		return xdm.QName{}, fmt.Errorf(
			"XPTY0004: the name of a constructed node must be a single value")
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return xdm.QName{}, fmt.Errorf(
			"XPTY0004: the name of a constructed node must be a QName or a string")
	}
	if a.Type == xdm.TypeQName {
		if q := a.QName(); q != nil {
			return *q, nil
		}
	}
	switch a.Type {
	case xdm.TypeString, xdm.TypeUntypedAtomic:
	default:
		return xdm.QName{}, fmt.Errorf(
			"XPTY0004: the name of a constructed node must be a QName or a string")
	}
	// §3.9.3.1 admits either spelling, and either may be surrounded by
	// whitespace: the value came from an expression, not from the query text,
	// so nothing has trimmed it yet.
	lex := strings.TrimSpace(a.String())
	if uri, local, ok := splitBracedName(lex); ok {
		if !xdm.IsNCName(local) {
			return xdm.QName{}, fmt.Errorf(
				"XQDY0074: %q is not a lexical QName", lex)
		}
		return xdm.QName{URI: uri, Local: local}, nil
	}
	prefix, local := "", lex
	if i := strings.IndexByte(lex, ':'); i >= 0 {
		prefix, local = lex[:i], lex[i+1:]
	}
	if !xdm.IsNCName(local) || (prefix != "" && !xdm.IsNCName(prefix)) {
		return xdm.QName{}, fmt.Errorf("XQDY0074: %q is not a lexical QName", lex)
	}
	// The prefix resolves against the namespaces in scope *where the
	// constructor was written*, which is what §3.9.3.1's "statically known
	// namespaces" means. Inside a direct constructor those include that
	// element's own declarations — "<e xmlns:foo='...'>{element {'foo:x'} {}}
	// </e>" binds foo — and they are only on the expression, the evaluation
	// context having none but the module's.
	sc := e.sc
	if sc == nil {
		sc = ctx.sc
	}
	if isElement {
		q, err := sc.resolveElementName(prefix, local)
		if err != nil {
			return xdm.QName{}, fmt.Errorf("XQDY0074: %v", err)
		}
		return q, nil
	}
	q, err := sc.resolveAttributeName(prefix, local)
	if err != nil {
		return xdm.QName{}, fmt.Errorf("XQDY0074: %v", err)
	}
	return q, nil
}

// splitBracedName takes apart a name written as "Q{uri}local".
//
// The URI is normalised the way a braced URI literal in the query text is, so
// that a name computed as a string and one written in the source resolve
// alike: attribute { " Q{ }x " } and attribute Q{}x are the same attribute.
func splitBracedName(lex string) (uri, local string, ok bool) {
	if !strings.HasPrefix(lex, "Q{") {
		return "", "", false
	}
	end := strings.IndexByte(lex, '}')
	if end < 0 {
		return "", "", false
	}
	inner := lex[2:end]
	// A brace inside the URI is not something the literal form can spell, so
	// a second one means this is not a braced name after all.
	if strings.ContainsAny(inner, "{") {
		return "", "", false
	}
	return normalizeURILiteral(inner), lex[end+1:], true
}

// checkAttributeName refuses the names §3.9.3.3 reserves for namespace
// declarations, which an attribute node may not carry.
//
// XQDY0044 covers four cases, and they are four spellings of one rule: an
// attribute may not say anything about namespace bindings. "xmlns" and any
// name in the xmlns namespace are declarations rather than attributes. The
// xml prefix and the XML namespace are bound to each other permanently, so a
// name that pairs either with anything else would be asserting a binding that
// is not the processor's to change.
//
// The check is here rather than at parse time because a computed name is not
// known until the constructor runs, and a direct constructor cannot produce
// any of these anyway: scanAttributes routes xmlns and xmlns:p to the
// namespace-declaration path before a name is ever resolved.
func checkAttributeName(name xdm.QName) error {
	switch {
	case name.URI == "" && name.Local == "xmlns":
		return fmt.Errorf(
			"XQDY0044: an attribute may not be named %q", "xmlns")
	case name.URI == xdm.NSXMLNS:
		return fmt.Errorf(
			"XQDY0044: an attribute may not be in the namespace %q", xdm.NSXMLNS)
	case name.Prefix == "xmlns":
		// The prefix reserved for declarations, whatever URI it was paired
		// with. XQ.E19 added this case to §3.9.3.3 on its own, because the
		// namespace test above misses it: fn:QName() will pair "xmlns:foo"
		// with any URI at all, and comp-attr-bad-name-7 and cbcl-constr-
		// compattr-002 both do exactly that.
		return fmt.Errorf(
			"XQDY0044: an attribute name may not use the prefix %q", "xmlns")
	case name.Prefix == "xml" && name.URI != xdm.NSXML:
		return fmt.Errorf(
			"XQDY0044: the prefix %q is bound to %q and may not be rebound",
			"xml", xdm.NSXML)
	case name.Prefix != "" && name.Prefix != "xml" && name.URI == xdm.NSXML:
		return fmt.Errorf(
			"XQDY0044: the namespace %q may only be named by the prefix %q",
			xdm.NSXML, "xml")
	}
	return nil
}

// checkElementName is the same rule for an element name, which §3.9.3.1
// reports as XQDY0096.
//
// An element differs from an attribute in one place: it has no unprefixed
// "xmlns" case, because an element named xmlns in no namespace is a legal
// element. What is reserved is the namespace, not the local name.
func checkElementName(name xdm.QName) error {
	switch {
	case name.URI == xdm.NSXMLNS:
		return fmt.Errorf(
			"XQDY0096: an element may not be in the namespace %q", xdm.NSXMLNS)
	case name.Prefix == "xmlns":
		// The same addition XQ.E19 made for an attribute; see
		// checkAttributeName. comp-elem-bad-name-6 constructs the name with
		// fn:QName and expects XQDY0096.
		return fmt.Errorf(
			"XQDY0096: an element name may not use the prefix %q", "xmlns")
	case name.Prefix == "xml" && name.URI != xdm.NSXML:
		return fmt.Errorf(
			"XQDY0096: the prefix %q is bound to %q and may not be rebound",
			"xml", xdm.NSXML)
	case name.Prefix != "" && name.Prefix != "xml" && name.URI == xdm.NSXML:
		return fmt.Errorf(
			"XQDY0096: the namespace %q may only be named by the prefix %q",
			xdm.NSXML, "xml")
	}
	return nil
}

// eval for a namespace constructor adds a binding to the element being built.
//
// §3.9.3.7. The prefix may be empty, which binds the default namespace, and
// the URI is the constructor's content joined the way a text constructor's
// is. A namespace node with no element to attach to is a legal item, which is
// what AddNamespace does with it when nothing is open.
func (n *namespaceNode) eval(out *builderRef, ctx *evalContext) error {
	prefix := n.prefix
	if n.prefixExpr != nil {
		// bind, for the same reason evalNodeName uses it: the prefix
		// expression may hold an XQuery-only primary that was lifted into a
		// "local:xq-stepN()" call, and only bind installs the function that
		// call reaches. nscons-015 writes "namespace { <a/>/* } { ... }",
		// where the constructor is the lifted primary.
		xp, err := n.prefixExpr.bind(ctx)
		if err != nil {
			return err
		}
		seq, err := n.prefixExpr.compiled.Eval(xp)
		if err != nil {
			return err
		}
		atoms, err := xdm.AtomizeChecked(seq)
		if err != nil {
			return err
		}
		if len(atoms) > 1 {
			return fmt.Errorf(
				"XPTY0004: a namespace prefix must be a single value")
		}
		// The prefix is cast to xs:NCName, and §3.9.3.7 splits the two ways
		// that can fail. A value of a type the cast does not accept at all —
		// a number, an xs:anyURI, a duration — is XPTY0004, a static type
		// mismatch. A string or untypedAtomic that is simply not an NCName is
		// XQDY0074, the same code a computed node name gets for the same
		// reason: it is a name that will not parse.
		if len(atoms) == 1 {
			a, ok := atoms[0].(*xdm.Atomic)
			if !ok {
				return fmt.Errorf(
					"XPTY0004: a namespace prefix must be a string")
			}
			switch a.Type {
			case xdm.TypeString, xdm.TypeUntypedAtomic:
			default:
				return fmt.Errorf(
					"XPTY0004: a %s cannot be used as a namespace prefix",
					a.TypeName())
			}
			prefix = a.String()
			if prefix != "" && !xdm.IsNCName(prefix) {
				return fmt.Errorf(
					"XQDY0074: %q is not a valid namespace prefix", prefix)
			}
		}
	}
	uri, _, err := contentString(n.content, ctx)
	if err != nil {
		return err
	}
	if err := checkNamespaceBinding(prefix, uri); err != nil {
		return err
	}
	// A binding this element already has is not a conflict when it agrees, and
	// AddNamespace decides that; what it cannot see is a second constructor
	// binding the same prefix differently, which NoteDeclared records for it.
	if err := out.b.AddNamespace(prefix, uri); err != nil {
		return err
	}
	out.b.NoteDeclared(prefix, uri)
	return nil
}

// checkNamespaceBinding refuses the bindings §3.9.3.7 reserves, all XQDY0101.
//
// The xml prefix and the XML namespace are bound to each other permanently:
// binding either to anything else would assert a binding the processor does
// not get to change, and binding them to each other is a no-op the rule
// allows. The xmlns prefix and the xmlns namespace may not be bound at all,
// in either direction, because a namespace node is not how a declaration is
// spelled. And no prefix may be bound to the empty URI: that is an
// undeclaration, which a constructor has no way to mean.
func checkNamespaceBinding(prefix, uri string) error {
	switch {
	case prefix == "xmlns" || uri == xdm.NSXMLNS:
		return fmt.Errorf(
			"XQDY0101: neither the prefix %q nor the namespace %q may be "+
				"bound by a namespace constructor", "xmlns", xdm.NSXMLNS)
	case prefix == "xml" && uri != xdm.NSXML:
		return fmt.Errorf(
			"XQDY0101: the prefix %q is bound to %q and may not be rebound",
			"xml", xdm.NSXML)
	case prefix != "xml" && uri == xdm.NSXML:
		return fmt.Errorf(
			"XQDY0101: the namespace %q may only be bound to the prefix %q",
			xdm.NSXML, "xml")
	case uri == "":
		return fmt.Errorf(
			"XQDY0101: a namespace constructor may not bind %s to no namespace",
			describePrefix(prefix))
	}
	return nil
}

func describePrefix(prefix string) string {
	if prefix == "" {
		return "the default namespace"
	}
	return fmt.Sprintf("the prefix %q", prefix)
}

// sortedPrefixes returns a map's keys in a fixed order, so that the namespace
// nodes a constructor writes come out the same on every run rather than in
// Go's randomised map order.
func sortedPrefixes(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
