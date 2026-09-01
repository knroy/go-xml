package xquery

import (
	"fmt"
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
// The empty URI means the clause said nothing, and the default collation is
// then whatever the evaluation context carries — nil, which every caller
// reads as codepoint.
func (ctx *evalContext) collation(uri string) (xpath.Collation, error) {
	if uri == "" {
		return nil, nil
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
	seq, err := n.expr.compiled.Eval(ctx.xp)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}

// sequence evaluates an enclosed expression to a flat sequence, for the
// callers that need its value rather than its contribution to a tree.
func (n *enclosed) sequence(ctx *evalContext) (xdm.Sequence, error) {
	if n.expr != nil {
		return n.expr.compiled.Eval(ctx.xp)
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
				if el := out.b.Open(); el != nil && len(el.Children) > 0 {
					return fmt.Errorf("XQTY0024: the attribute %s follows a "+
						"node that is not an attribute in the content of "+
						"element %s", v.Name.Lexical(), el.Name.Lexical())
				}
			}
			before := 0
			if el := out.b.Open(); el != nil {
				before = len(el.Children)
			}
			out.b.AppendNode(v)
			if el := out.b.Open(); el != nil && len(el.Children) > before {
				applyCopyNamespaces(el.Children[len(el.Children)-1], sc)
			}
		case *xdm.Atomic:
			out.b.AppendValue(v)
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
// Both halves default to the permissive answer — preserve and inherit — and
// under those defaults a re-parented copy is already right: it kept the
// namespace nodes it carried, and it sees the destination's through its new
// parent. So this only has work to do when the prolog asked for the other
// answer, and it is written to walk the subtree at all only then.
//
// no-preserve drops the namespace nodes the copied element carried, keeping
// the ones its own name and its attributes' names still need — a copy whose
// name became unresolvable would not be a node at all. copynamespace-16 is
// the case: three nested constructors each declaring a prefix, and only the
// innermost element's own bindings survive to the innermost copy.
//
// no-inherit stops the copy from picking up what is in scope at the
// destination, which re-parenting otherwise gives it for free. There is no way
// to *not* inherit in the data model, so the bindings are undeclared on the
// copy's root instead, which is what the namespace axis then reports.
// copynamespace-17 asks for both and expects the copy to be left with the xml
// binding alone.
func applyCopyNamespaces(n *xdm.Node, sc *staticContext) {
	if n == nil || n.Kind != xdm.KindElement {
		return
	}
	preserve := sc == nil || sc.preserveNS
	inherit := sc == nil || sc.inheritNS
	if preserve && inherit {
		return
	}
	if !preserve {
		stripNamespaces(n)
	}
	if !inherit {
		// The bindings to undeclare are the ones the *parent* supplies; the
		// copy's own, whether original or just rebuilt by stripNamespaces,
		// are not inherited and must survive. An undeclaration is written as
		// an empty URI, which InScopeNamespaces and LookupPrefix both read as
		// "not in scope here".
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
	if err := declareOwnName(sub.b); err != nil {
		return err
	}
	for _, a := range n.attrs {
		if err := a.eval(sub, ctx); err != nil {
			return err
		}
	}
	// §3.9.1.3: the base URI of a constructed element is the static base URI
	// of the constructor *unless* the element carries an xml:base attribute,
	// in which case it is that attribute resolved against the base URI in
	// force at the parent. The attribute is only known once the attributes
	// have been evaluated — a computed one is not even a constant — so the
	// stamp above is a provisional answer and this is the final one. Without
	// it base-uri(<e xml:base="http://example.com/"/>) answered the query's
	// own base URI, or nothing, which is wrong in both directions.
	if el := sub.b.Open(); el != nil {
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
	for _, c := range n.content {
		if err := c.eval(sub, ctx); err != nil {
			return err
		}
	}
	return nil
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
	return b.AddNamespace(el.Name.Prefix, el.Name.URI)
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
	text, err := contentString(n.content, ctx)
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
		seq, err := n.targetExpr.compiled.Eval(ctx.xp)
		if err != nil {
			return err
		}
		s, err := joinAtomized(seq)
		if err != nil {
			return err
		}
		target = s
		if !xdm.IsNCName(target) {
			return fmt.Errorf(
				"XQDY0041: %q is not a valid processing-instruction target",
				target)
		}
	}
	text, err := contentString(n.content, ctx)
	if err != nil {
		return err
	}
	if strings.Contains(text, "?>") {
		return fmt.Errorf(
			"XQDY0026: a processing instruction may not contain %q", "?>")
	}
	out.b.AppendNode(&xdm.Node{Kind: xdm.KindPI,
		Name: xdm.QName{Local: target}, Value: text})
	return nil
}

func (n *textNode) eval(out *builderRef, ctx *evalContext) error {
	text, err := contentString(n.content, ctx)
	if err != nil {
		return err
	}
	// A text constructor whose content is empty produces no node at all,
	// rather than an empty one the data model does not admit.
	if text == "" {
		return nil
	}
	out.b.AppendNode(&xdm.Node{Kind: xdm.KindText, Value: text})
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
func contentString(content []node, ctx *evalContext) (string, error) {
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
				return "", err
			}
			seq = append(seq, s...)
		default:
			return "", fmt.Errorf(
				"XPTY0004: this content may not contain a constructed node")
		}
	}
	return joinAtomized(seq)
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
	seq, err := e.compiled.Eval(ctx.xp)
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
	if isElement {
		q, err := ctx.sc.resolveElementName(prefix, local)
		if err != nil {
			return xdm.QName{}, fmt.Errorf("XQDY0074: %v", err)
		}
		return q, nil
	}
	q, err := ctx.sc.resolveAttributeName(prefix, local)
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
		seq, err := n.prefixExpr.compiled.Eval(ctx.xp)
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
	uri, err := contentString(n.content, ctx)
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
