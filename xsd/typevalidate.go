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
	return s.ValidateAgainstType(at, decl.Type.Name, opts)
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
			n.TypeAnnotation = typeName.Local
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
