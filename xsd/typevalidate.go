package xsd

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// Validating a single element against a declaration or a type.
//
// Schema.Validate answers "is this document valid", which is what an instance
// validator needs. XSLT needs a narrower question: it constructs an element
// and then, if the stylesheet said so, asks whether *that element* is valid
// against a global declaration (validation="strict") or against a named type
// ([xsl:]type). The machinery is the same; only the entry point differs.

// ValidateElement checks one element against the global declaration for its
// name.
//
// This is xsl:element validation="strict": the element must have a global
// declaration and must be valid against it. An element with no declaration is
// an error rather than a pass, which is what distinguishes strict from lax.
func (s *Schema) ValidateElement(el *xdm.Node, opts ValidateOptions) error {
	if el == nil || el.Kind != xdm.KindElement {
		return fmt.Errorf("xsd: ValidateElement needs an element")
	}
	// Prefix-insensitive, for the reason given in ValidateAgainstType: the
	// element was built by a stylesheet using its own prefix, and the schema
	// stores the declaration under the one its document used.
	if _, ok := s.Elements[bareName(el.Name)]; !ok {
		return &ValidationErrors{Errors: []*ValidationError{{
			Code: "cvc-elt.1",
			Message: fmt.Sprintf("no global declaration for element %s",
				showName(el.Name)),
			Path: "/" + el.Name.Local,
		}}}
	}
	return s.Validate(el, opts)
}

// ValidateElementLax checks one element against its global declaration when
// there is one, and passes when there is not.
//
// That is what validation="lax" means: an element the schema does not
// describe is not thereby invalid. It is the mode that lets a stylesheet
// validate the parts of its output that are described without having to
// describe all of it.
func (s *Schema) ValidateElementLax(el *xdm.Node, opts ValidateOptions) error {
	if el == nil || el.Kind != xdm.KindElement {
		return fmt.Errorf("xsd: ValidateElementLax needs an element")
	}
	if _, ok := s.Elements[bareName(el.Name)]; !ok {
		return nil
	}
	return s.Validate(el, opts)
}

// HasElementDeclaration reports whether the schema declares a global element
// of that name, and HasAttributeDeclaration does the same for an attribute.
//
// A caller that must distinguish "not declared" from "declared and invalid" —
// XSLT's XTTE1512 against XTTE1510 — needs to ask before validating, because
// once validation has run both look like a failure.
func (s *Schema) HasElementDeclaration(name xdm.QName) bool {
	_, ok := s.Elements[bareName(name)]
	return ok
}

// CanAssessStrictly reports whether strict assessment has something to assess
// an element against.
//
// That is a weaker question than HasElementDeclaration, because §3.3.4 clause
// 1.2 also assesses an element carrying xsi:type against the type it names,
// declaration or no declaration. Validate already takes that path; without
// this the XSLT layer refused the element as undeclared before ever getting
// there, so <doc xsi:type="xs:anyType"> under validation="strict" reported no
// top-level declaration rather than validating.
func (s *Schema) CanAssessStrictly(el *xdm.Node) bool {
	if el == nil || el.Kind != xdm.KindElement {
		return false
	}
	if s.HasElementDeclaration(el.Name) {
		return true
	}
	return el.Attr(NSInstance, "type") != nil
}

// HasAttributeDeclaration reports whether the schema declares a global
// attribute of that name.
func (s *Schema) HasAttributeDeclaration(name xdm.QName) bool {
	_, ok := s.Attributes[bareName(name)]
	return ok
}

// ValidateAttribute checks one attribute against the global declaration for
// its name.
//
// It is the attribute counterpart of ValidateElement, for a stylesheet that
// copies an attribute under validation="strict": the declaration selects the
// type, and the value has to satisfy it. lax passes an attribute the schema
// does not declare; strict rejects it.
func (s *Schema) ValidateAttribute(at *xdm.Node, lax bool, opts ValidateOptions) error {
	if at == nil || at.Kind != xdm.KindAttribute {
		return fmt.Errorf("xsd: ValidateAttribute needs an attribute")
	}
	decl, ok := s.Attributes[bareName(at.Name)]
	if !ok || decl == nil || decl.Type == nil {
		if lax {
			return nil
		}
		return &ValidationErrors{Errors: []*ValidationError{{
			Code: "cvc-attribute.1",
			Message: fmt.Sprintf("no global declaration for attribute %s",
				showName(at.Name)),
			Path: "/@" + at.Name.Local,
		}}}
	}
	// The declaration's type is passed as a component rather than by name.
	// An attribute may be declared with an inline <xs:simpleType>, which has
	// no name to look up — the built-in xml:lang is one, being a union of
	// xs:language with the empty string — and routing through the name turned
	// every such declaration into "no type named {…}… in the schema".
	return s.validateNodeAgainstType(at, decl.Type, decl.Type.TypeName(), opts)
}

// ValidateAgainstType checks one element or attribute against a named type.
//
// This is the [xsl:]type attribute, which names a type directly rather than
// letting the element's own name select a declaration — so an element called
// anything at all may be asked to match xs:integer.
func (s *Schema) ValidateAgainstType(n *xdm.Node, typeName xdm.QName,
	opts ValidateOptions) error {

	if n == nil {
		return fmt.Errorf("xsd: ValidateAgainstType needs a node")
	}
	// A QName is compared as a whole struct, prefix included, and a schema
	// stores a type under the prefix its own document used. Looking up the
	// name as the *caller* spelled it therefore misses whenever the two
	// differ, which is nearly always. Only the URI and local name identify a
	// type.
	typeName = bareName(typeName)
	typ, ok := s.Types[typeName]
	if !ok {
		if bt := BuiltinType(typeName.Local); bt != nil &&
			typeName.URI == xdm.NSXS {
			typ = bt
		} else {
			return &ValidationErrors{Errors: []*ValidationError{{
				Code: "cvc-type.1",
				Message: fmt.Sprintf("no type named %s in the schema",
					showName(typeName)),
			}}}
		}
	}
	return s.validateNodeAgainstType(n, typ, typeName, opts)
}

// validateNodeAgainstType is ValidateAgainstType with the type already
// resolved, so that a caller holding an anonymous type component can use it.
// typeName is carried alongside only for error messages and the annotation.
func (s *Schema) validateNodeAgainstType(n *xdm.Node, typ Type,
	typeName xdm.QName, opts ValidateOptions) error {

	if opts.MaxErrors == 0 {
		opts.MaxErrors = DefaultMaxErrors
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	v := &validator{schema: s, opts: opts, ids: map[string]int{}}

	switch n.Kind {
	case xdm.KindElement:
		v.validateAgainstType(n, typ, nil)
	case xdm.KindAttribute:
		// An attribute has only a value, so only a simple type can describe
		// it. A complex type here is a mistake in the stylesheet rather than
		// a failure of the value.
		st, ok := typ.(*SimpleType)
		if !ok {
			return fmt.Errorf(
				"xsd: attribute %s cannot be validated against complex type %s",
				n.Name.Local, showName(typeName))
		}
		v.validateSimpleContent(n, n.Value, st, nil)
		if opts.Annotate && len(v.errs) == 0 && typeName.Local != "" {
			// The element branch stamps the annotation inside the validator;
			// this one has to do it here, because validateSimpleContent works
			// on a value rather than on a declared node. Without it an
			// attribute validated against a named type came out untyped and
			// "instance of attribute(a, my:t)" answered false for it.
			n.SetTypeAnnotation(typeName.Local)
		}
	default:
		return fmt.Errorf("xsd: ValidateAgainstType needs an element or attribute")
	}

	if len(v.errs) > 0 {
		return &ValidationErrors{Errors: v.errs}
	}
	return nil
}

func showName(n xdm.QName) string {
	if n.URI == "" {
		return n.Local
	}
	return "{" + n.URI + "}" + n.Local
}

// bareName drops the prefix from a QName so that it can be used as a lookup
// key.
//
// xdm.QName carries the prefix and is compared as a whole struct, but a
// prefix is a spelling rather than part of a name's identity. Every global
// component here is keyed by URI and local name, and a caller who built a
// name from a stylesheet carries whatever prefix that stylesheet used.
func bareName(q xdm.QName) xdm.QName {
	return xdm.QName{URI: q.URI, Local: q.Local}
}

// ValidateValue checks a lexical value against a named simple type, without a
// node to hang it on.
//
// "castable as my:hatsize" asks exactly this question: whether a value is in
// the value space of a schema type, facets and all. The XPath engine holds
// only the type's name, so it has to be able to ask without constructing an
// element or attribute first — and without that, a cast to a user-defined
// type checked the built-in it derives from and ignored every facet the
// schema author wrote.
//
// A name that is not a simple type in this schema is an error rather than a
// pass: a caller asking about a complex type has asked a question with no
// answer, and reporting success would make the cast succeed.
func (s *Schema) ValidateValue(value string, typeName xdm.QName) error {
	name := bareName(typeName)
	t, ok := s.Types[name]
	if !ok {
		if bt := BuiltinType(name.Local); bt != nil && name.URI == xdm.NSXS {
			t = bt
		} else {
			return fmt.Errorf("xsd: no type named %s in the schema",
				showName(name))
		}
	}
	st, ok := t.(*SimpleType)
	if !ok {
		return fmt.Errorf("xsd: %s is a complex type", showName(name))
	}
	// A QName-valued type may be asked about in the expanded "{uri}local"
	// spelling, by a caller that resolved the prefix while it still had a
	// namespace context and has none now. Its lexical checks are the caller's
	// to have made — a QName literal has no lexical space beyond being a
	// QName — so only the facets are left to apply. See
	// ValidateExpandedQNameValue, which is the same check reached by name.
	if q, expanded := ParseExpandedName(value); expanded {
		if known, err := s.ValidateExpandedQNameValue(name, q); known {
			return err
		}
	}
	_, err := validateSimpleValueVersion(value, st, s.Version)
	return err
}

// HasSimpleType reports whether a name is a simple type in this schema, which
// is the precondition ValidateValue needs a caller to have checked.
func (s *Schema) HasSimpleType(typeName xdm.QName) bool {
	t, ok := s.Types[bareName(typeName)]
	if !ok {
		return false
	}
	_, ok = t.(*SimpleType)
	return ok
}

// ValidateExpandedQNameValue checks an already-expanded QName against a named
// simple type whose value space is the QName one.
//
// The constructor of a type derived from xs:NOTATION or xs:QName has to
// resolve its argument's prefix while the static context of the expression
// still exists, so by the time the facets can be checked there is no node and
// no in-scope namespaces left — and comparing the raw lexical form against the
// schema's enumeration matched prefixes rather than namespaces, which made
// one:mp3 fail an enumeration written smokey:mp3 for the same URI. The
// expanded name is passed instead, in the "{uri}local" spelling that no
// lexical QName can have, and the lexical checks are skipped because the
// caller has already done the only one that applies to a QName literal.
//
// known is false when the name is not a QName-valued simple type in this
// schema, in which case the caller keeps whatever answer it already had.
func (s *Schema) ValidateExpandedQNameValue(typeName, value xdm.QName) (known bool, err error) {
	t, ok := s.Types[bareName(typeName)]
	if !ok {
		return false, nil
	}
	st, ok := t.(*SimpleType)
	if !ok {
		return false, nil
	}
	if p := primitiveOf(st); p == nil ||
		(p.Name.Local != "QName" && p.Name.Local != "NOTATION") {
		return false, nil
	}
	clark := "{" + value.URI + "}" + value.Local
	steps := facetChain(st)
	if perr := checkEnumerationIn(steps, clark, st, nil); perr != nil {
		return true, perr
	}
	return true, nil
}
