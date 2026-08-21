package xsd

import (
	"time"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// XSD 1.1 assertions and conditional type assignment.
//
// These are the two features that make XSD 1.1 unavailable to most
// implementations: both evaluate XPath 2.0 expressions during validation.
// <xs:assert test="..."> is a co-constraint on an element's own content —
// "the end date must not precede the start date" — which XSD 1.0 cannot state
// at all, and <xs:alternative> chooses a type from an XPath test on the
// element's attributes.
//
// The XPath subset is restricted in one way that matters: the expression is
// evaluated with the element being validated as the context item, and it may
// not look outside that element's subtree. That confinement is what keeps
// assertion evaluation local, so that validating a large document does not
// become quadratic.

// Assertion is an <xs:assert> (XSD 1.1 §3.13).
type Assertion struct {
	// Test is the compiled XPath 2.0 expression.
	Test *xpath.Compiled
	// Source is the expression as written, for diagnostics.
	Source string
	// XPathDefaultNamespace is the namespace unprefixed element names in
	// the test resolve to.
	XPathDefaultNamespace string
}

// TypeAlternative is an <xs:alternative> (XSD 1.1 §3.3).
//
// A declaration may carry several; the first whose test matches selects the
// type. An alternative with no test is the default, and may appear only last.
type TypeAlternative struct {
	// Test is the compiled condition, or nil for the default alternative.
	Test *xpath.Compiled
	// Source is the condition as written.
	Source string
	// Type is the type to use when the test matches.
	Type Type
}

// readAssert reads an <xs:assert> or <xs:assertion>.
//
// Both spellings exist: <xs:assert> inside a complex type, and <xs:assertion>
// inside a simple type restriction, where it is a facet rather than a
// component. They compile identically.
func (p *parser) readAssert(el *xdm.Node) *Assertion {
	test := el.AttrValue("test")
	if test == "" {
		p.errs = append(p.errs, errorAt(el, "src-assert",
			"an assertion must have a test"))
		return nil
	}

	// xpathDefaultNamespace may be set on the assertion, on an ancestor, or
	// on <xs:schema> for the whole document, and takes three keywords
	// besides a literal URI. Reading only the local attribute treated
	// "##defaultNamespace" as a namespace URI of that spelling, so every
	// unprefixed name in the test was resolved into a namespace no element
	// is ever in.
	def, _ := p.xpathDefaultNamespace(el)

	compiled, err := xpath.Compile(test, assertResolver{el: el, defaultNS: def})
	if err != nil {
		p.errs = append(p.errs, errorAt(el, "src-assert",
			"assertion test %q: %v", test, err))
		return nil
	}
	return &Assertion{Test: compiled, Source: test, XPathDefaultNamespace: def}
}

// assertResolver resolves namespace prefixes in an assertion or alternative,
// against the bindings in scope on the schema element that carries it.
//
// The default namespace is not the one in scope for elements in the schema
// document — that would be the XML Schema namespace — but the one
// xpathDefaultNamespace names, which defaults to none.
type assertResolver struct {
	el        *xdm.Node
	defaultNS string
}

// ResolvePrefix implements xpath.NamespaceResolver.
func (r assertResolver) ResolvePrefix(prefix string) (string, bool) {
	if prefix == "" {
		if r.defaultNS == "" {
			return "", false
		}
		return r.defaultNS, true
	}
	return r.el.LookupPrefix(prefix)
}

// DefaultElementNamespace implements xpath.NamespaceResolver.
func (r assertResolver) DefaultElementNamespace() string { return r.defaultNS }

// DefaultFunctionNamespace implements xpath.NamespaceResolver.
//
// An assertion may call the fn: functions unprefixed, as every other XPath
// context does.
func (r assertResolver) DefaultFunctionNamespace() string {
	return "http://www.w3.org/2005/xpath-functions"
}

// readAlternative reads an <xs:alternative>.
func (p *parser) readAlternative(el *xdm.Node) *TypeAlternative {
	alt := &TypeAlternative{Source: el.AttrValue("test")}

	if alt.Source != "" {
		// As for an assertion: the attribute may be inherited and takes
		// keywords besides a URI.
		def, _ := p.xpathDefaultNamespace(el)
		compiled, err := xpath.Compile(alt.Source,
			assertResolver{el: el, defaultNS: def})
		if err != nil {
			p.errs = append(p.errs, errorAt(el, "src-type-alternative",
				"alternative test %q: %v", alt.Source, err))
			return nil
		}
		alt.Test = compiled
	}

	if ref := el.AttrValue("type"); ref != "" {
		p.resolveTypeRef(el, ref, func(t Type) { alt.Type = t })
	} else if inline := p.childElement(el, "simpleType", "complexType"); inline != nil {
		if inline.Name.Local == "simpleType" {
			alt.Type = p.readSimpleType(inline)
		} else {
			alt.Type = p.readComplexType(inline)
		}
	} else {
		p.errs = append(p.errs, errorAt(el, "src-type-alternative",
			"an alternative must name a type or contain one"))
		return nil
	}
	return alt
}

// checkAssertions evaluates an element's assertions (cvc-assertion).
//
// The context item is the element itself, and — this is the part that keeps
// evaluation local — the element is presented as the root of its own tree, so
// an expression cannot navigate to an ancestor or a sibling. XSD 1.1 requires
// exactly that confinement, and without it an assertion on a deeply nested
// element could walk the whole document, making validation quadratic.
func (v *validator) checkAssertions(el *xdm.Node, t *ComplexType) {
	if len(t.Assertions) == 0 {
		return
	}
	scoped := scopeForAssertion(el)
	annotateForAssertion(scoped, t)

	// $value is in scope in every assertion, not only those on a simple
	// type (§3.13.4). On a complex type it is the element's simple content
	// where there is one, and the empty sequence otherwise — which is what
	// makes empty($value) the way a schema asserts "this element has element
	// content, not a value". Leaving it unbound raises XPST0008 and turns a
	// legitimate assertion into an evaluation failure.
	value := v.assertionValue(el, t)

	for _, a := range t.Assertions {
		ctx := newAssertContext(scoped)
		ctx.Vars = map[string]xdm.Sequence{"value": value}
		ok, err := a.Test.EvalBool(ctx)
		if err != nil {
			v.fail(el, "cvc-assertion.2",
				"assertion %q could not be evaluated: %v", a.Source, err)
			continue
		}
		if !ok {
			v.fail(el, "cvc-assertion.3",
				"assertion %q is not satisfied", a.Source)
		}
	}
}

// assertionValue returns the sequence bound to $value in a complex type's
// assertions.
//
// A type with simple content has a value; one with element content or empty
// content does not, and the empty sequence is what the spec binds there rather
// than a zero-length string. The distinction is visible: empty($value) is true
// for the second and false for a simple content whose value happens to be "".
func (v *validator) assertionValue(el *xdm.Node, t *ComplexType) xdm.Sequence {
	if t.Content != ContentSimple || t.SimpleContent == nil {
		return nil
	}
	normalized, err := validateSimpleValue(el.StringValue(), t.SimpleContent)
	if err != nil {
		// The content is invalid and has already been reported; there
		// is no typed value to bind, so the assertion sees nothing
		// rather than a value the type does not admit.
		return nil
	}
	return typedSequenceFor(normalized, t.SimpleContent)
}

// scopeForAssertion returns a copy of an element rooted in its own tree.
//
// The copy is what enforces the confinement: the original keeps its parent, so
// evaluating against it directly would let "../x" or "/root" reach outside the
// element being validated, which XSD 1.1 forbids. Copying also means an
// assertion cannot mutate the document it is checking.
func scopeForAssertion(el *xdm.Node) *xdm.Node {
	tree := xdm.NewTree()
	clone := deepCopyNode(el)
	tree.Root.AppendChild(clone)
	tree.Finalize()
	return clone
}

// deepCopyNode copies a node and its subtree, detached from any parent.
//
// Comments and processing instructions are dropped. XSD 1.1 builds the tree an
// assertion sees from the element's [children] with comments and PIs excluded
// unless a processor offers an option to include them, and this one does not:
// an assertion writing empty(.//comment()) is asking whether the schema-visible
// content holds any, and the answer the spec defines is yes-by-default only for
// a processor that has been told to expose them.
func deepCopyNode(n *xdm.Node) *xdm.Node {
	out := &xdm.Node{
		Kind:           n.Kind,
		Name:           n.Name,
		Value:          n.Value,
		BaseURI:        n.BaseURI,
		TypeAnnotation: n.TypeAnnotation,
	}
	for _, a := range n.Attrs {
		out.AddAttr(&xdm.Node{Kind: a.Kind, Name: a.Name, Value: a.Value,
			TypeAnnotation: a.TypeAnnotation})
	}
	for _, ns := range n.Namespaces {
		out.AddNamespace(ns.Name.Local, ns.Value)
	}
	for _, c := range n.Children {
		if c.Kind == xdm.KindComment || c.Kind == xdm.KindPI {
			continue
		}
		out.AppendChild(deepCopyNode(c))
	}
	return out
}

// selectAlternativeType applies conditional type assignment (cvc-type-alt).
//
// The alternatives are tried in order and the first whose test holds selects
// the type; one with no test is the default. The tests may look only at the
// element's attributes and its name, not at its content — the spec confines
// them so that the type can be chosen before the content is examined, which is
// what makes the feature implementable at all.
func (v *validator) selectAlternativeType(el *xdm.Node, decl *ElementDecl) Type {
	if len(decl.Alternatives) == 0 {
		return decl.Type
	}
	scoped := scopeForAssertion(el)

	// An inheritable attribute from an ancestor is visible to the test, but
	// only where the element does not carry one of the same name: the
	// nearest declaration wins.
	for _, a := range v.inherited {
		if scoped.Attr(a.Name.URI, a.Name.Local) != nil {
			continue
		}
		scoped.AddAttr(&xdm.Node{Kind: a.Kind, Name: a.Name, Value: a.Value})
	}

	for _, alt := range decl.Alternatives {
		if alt.Test == nil {
			// The default alternative, which matches unconditionally.
			if alt.Type != nil {
				return alt.Type
			}
			continue
		}
		ctx := newAssertContext(scoped)
		ok, err := alt.Test.EvalBool(ctx)
		if err != nil || !ok {
			continue
		}
		if alt.Type != nil {
			return alt.Type
		}
	}
	return decl.Type
}

// annotateForAssertion labels the copy an assertion evaluates against with the
// types the schema assigns.
//
// XSD 1.1 assertions run on the PSVI, so "@length eq count(entry)" compares an
// integer with an integer. Without the annotations the attribute atomises as
// xs:untypedAtomic, which promotes to a string against a numeric operand and
// raises XPTY0004 — the assertion then fails to evaluate rather than being
// true or false, which is a different and much less useful answer.
//
// The whole subtree is labelled, not just the immediate children. An assertion
// reaches as far as any XPath does — "data(event/d) instance of xs:date*" is a
// grandchild — and a descendant left untyped atomises as xs:untypedAtomic,
// which makes the instance-of false and the comparison a type error. The type
// of a descendant is reachable because each child's declaration names its type,
// and that type carries its own content model.
//
// The depth bound is what makes a recursive schema safe: a type whose content
// model reaches itself is legal, and an instance of it is finite, but the walk
// follows declarations rather than nodes and would otherwise not terminate.
func annotateForAssertion(el *xdm.Node, t *ComplexType) {
	annotateSubtree(el, t, 0)
}

// maxAnnotateDepth bounds the descent through declared types.
const maxAnnotateDepth = 32

func annotateSubtree(el *xdm.Node, t *ComplexType, depth int) {
	if el == nil || t == nil || depth > maxAnnotateDepth {
		return
	}
	for _, use := range t.AttributeUses {
		if use.Decl == nil || use.Decl.Type == nil {
			continue
		}
		a := el.Attr(use.Decl.Name.URI, use.Decl.Name.Local)
		if a != nil && a.TypeAnnotation == "" {
			a.TypeAnnotation = annotationName(use.Decl.Type)
		}
	}

	if t.Content == ContentSimple && t.SimpleContent != nil {
		if el.TypeAnnotation == "" {
			el.TypeAnnotation = annotationName(t.SimpleContent)
		}
		return
	}
	if t.Particle == nil {
		return
	}
	byName := map[xdm.QName]*ElementDecl{}
	collectElementDecls(t.Particle, byName, 0)
	for _, c := range el.ChildElements() {
		d, ok := byName[xdm.QName{URI: c.Name.URI, Local: c.Name.Local}]
		if !ok || d.Type == nil {
			continue
		}
		switch dt := d.Type.(type) {
		case *SimpleType:
			if c.TypeAnnotation == "" {
				c.TypeAnnotation = annotationName(dt)
			}
		case *ComplexType:
			annotateSubtree(c, dt, depth+1)
		}
	}
}

// collectElementDecls gathers the element declarations a particle can match.
//
// The depth bound is what makes a recursive model group safe here: a group that
// reaches itself is legal, and the content-model compiler refuses it, but this
// runs before that and would otherwise recurse without end.
func collectElementDecls(p *Particle, out map[xdm.QName]*ElementDecl, depth int) {
	if p == nil || depth > 32 {
		return
	}
	switch term := p.Term.(type) {
	case *ElementDecl:
		if _, seen := out[term.Name]; !seen {
			out[term.Name] = term
		}
		for _, sub := range term.substitutable {
			if _, seen := out[sub.Name]; !seen {
				out[sub.Name] = sub
			}
		}
	case *ModelGroup:
		for _, child := range term.Particles {
			collectElementDecls(child, out, depth+1)
		}
	}
}

// newAssertContext builds the evaluation context for an assertion.
//
// The clock is set because XSD 1.1 permits fn:current-date and its siblings in
// an assertion, and this package has no transform to inherit one from. It is
// read once per context so that two calls inside one assertion cannot disagree
// with each other.
func newAssertContext(item xdm.Item) *xpath.Context {
	ctx := xpath.NewContext(item, xpath.Builtins())
	ctx.Now = time.Now()
	ctx.HasNow = true
	return ctx
}

// checkSimpleAssertions evaluates the <xs:assertion> facets of a simple type.
//
// On a simple type an assertion is a facet rather than a component, and the
// value under test is bound to $value — there is no element to be the context
// item, so an expression has nothing else to refer to.
func checkSimpleAssertions(steps []facetStep, normalized string, t *SimpleType) error {
	for _, st := range steps {
		for _, a := range st.facets.Assertions {
			ctx := newAssertContext(nil)
			ctx.Vars = map[string]xdm.Sequence{
				"value": typedSequenceFor(normalized, t),
			}
			ok, err := a.Test.EvalBool(ctx)
			if err != nil {
				return facetError(st.typ, FacetKind(0),
					"assertion %q could not be evaluated: %v", a.Source, err)
			}
			if !ok {
				return facetError(st.typ, FacetKind(0),
					"assertion %q is not satisfied", a.Source)
			}
		}
	}
	return nil
}

// typedValueFor builds the value bound to $value in a simple-type assertion.
//
// It carries the type the schema assigns, so that "$value gt 5" compares
// numbers rather than raising on a string against an integer.
func typedValueFor(normalized string, t *SimpleType) xdm.Item {
	prim := ""
	if p := primitiveOf(t); p != nil {
		prim = p.Name.Local
	}
	n := &xdm.Node{Kind: xdm.KindText, Value: normalized, TypeAnnotation: prim}
	return n.Atomize()
}

// typedSequenceFor builds the sequence bound to $value.
//
// A list type's value is a sequence with one item per list item, not one item
// holding the whole literal. "every $x in data($value) satisfies $x mod 2 = 0"
// over a list of integers depends on it: given the literal as a single item,
// data() yields "2 4 6 8 10" and the arithmetic raises FORG0001 rather than
// running over five numbers.
func typedSequenceFor(normalized string, t *SimpleType) xdm.Sequence {
	if item := listItemTypeOf(t); item != nil {
		items := splitFields(normalized)
		out := make(xdm.Sequence, 0, len(items))
		for _, item := range items {
			out = append(out, typedValueFor(item, listItemTypeOf(t)))
		}
		return out
	}
	return xdm.One(typedValueFor(normalized, t))
}

// annotationName returns the built-in name a type atomises as.
//
// An anonymous type has no name of its own, and using it leaves the node
// untyped — so "even-number lt 500" against a restriction of xs:int compares a
// string with an integer and raises XPTY0004, when the schema plainly gave the
// element a numeric type. What decides atomisation is the primitive, and every
// simple type has one whether or not it was given a name.
//
// A named built-in still returns its own name, since a restriction of xs:int
// atomises as xs:int rather than as xs:decimal.
func annotationName(t Type) string {
	st, ok := t.(*SimpleType)
	if !ok || st == nil {
		return ""
	}
	if st.Name.Local != "" {
		return st.Name.Local
	}
	// Walk to the nearest named ancestor: a restriction of xs:int atomises
	// as xs:int, which the primitive alone (xs:decimal) would lose.
	for cur := st; cur != nil; {
		if cur.Name.Local != "" && cur.Name.URI == NSSchema {
			return cur.Name.Local
		}
		base, isSimple := cur.Base.(*SimpleType)
		if !isSimple || base == cur {
			break
		}
		cur = base
	}
	if p := st.Primitive; p != nil {
		return p.Name.Local
	}
	return ""
}

// listItemTypeOf returns the item type of a list, seeing through restrictions.
//
// A restriction of a list type is itself of the list variety but carries no
// item type of its own: the item type is the one it inherits. Reading only the
// type's own field left an assertion on such a restriction with $value bound to
// the whole literal as one item, so count($value) was always 1.
func listItemTypeOf(t *SimpleType) *SimpleType {
	seen := 0
	for cur := t; cur != nil; {
		if cur.Variety != VarietyList {
			return nil
		}
		if cur.ItemType != nil {
			return cur.ItemType
		}
		base, ok := cur.Base.(*SimpleType)
		if !ok || base == cur {
			return nil
		}
		cur = base
		if seen++; seen > 64 {
			return nil
		}
	}
	return nil
}
