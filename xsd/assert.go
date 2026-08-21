package xsd

import (
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

	def := el.AttrValue("xpathDefaultNamespace")
	if def == "##targetNamespace" {
		def = p.doc.targetNS
	} else if def == "##local" {
		def = ""
	}

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
		def := el.AttrValue("xpathDefaultNamespace")
		if def == "##targetNamespace" {
			def = p.doc.targetNS
		}
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
	} else if inline := childElement(el, "simpleType", "complexType"); inline != nil {
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

	for _, a := range t.Assertions {
		ctx := xpath.NewContext(scoped, xpath.Builtins())
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
		ctx := xpath.NewContext(scoped, xpath.Builtins())
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
