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
	if _, ok := s.Elements[el.Name]; !ok {
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
	if _, ok := s.Elements[el.Name]; !ok {
		return nil
	}
	return s.Validate(el, opts)
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
