package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// Schema validity assessment of constructed nodes.
//
// xsl:element, xsl:attribute, xsl:copy, xsl:copy-of and xsl:result-document
// may each carry a validation attribute, and a literal result element may
// carry xsl:validation. A [xsl:]type attribute names a type directly instead.
// Both ask the same question of a node the stylesheet has just built: is it
// valid, and against what.
//
// The four modes are section 19.2's. strict and lax assess against the
// element's own global declaration, differing only in what happens when there
// is none; preserve and strip do no assessment. strip is the default, which
// is why a stylesheet that says nothing about validation gets none.

// validationMode is the value of a validation attribute.
type validationMode int

const (
	validateStrip validationMode = iota // the default: no assessment
	validatePreserve
	validateStrict
	validateLax
)

func parseValidationMode(s string) (validationMode, error) {
	switch s {
	case "", "strip":
		return validateStrip, nil
	case "preserve":
		return validatePreserve, nil
	case "strict":
		return validateStrict, nil
	case "lax":
		return validateLax, nil
	}
	return validateStrip, fmt.Errorf(
		"XTSE0020: validation=%q is not strict, lax, preserve or strip", s)
}

// validationSpec is what an instruction carries from its validation and type
// attributes.
type validationSpec struct {
	mode validationMode
	// typeName is the [xsl:]type attribute, which names a type to assess
	// against instead of using the element's declaration.
	typeName *xdm.QName
}

// compileValidation reads the validation and type attributes of an
// instruction.
//
// Section 19.2 makes the two mutually exclusive: type names what to assess
// against, validation names how to find it, and giving both leaves no rule
// for reconciling them.
func compileValidation(n *xdm.Node, attrPrefix string) (validationSpec, error) {
	var spec validationSpec
	vAttr, tAttr := "validation", "type"
	if attrPrefix != "" {
		vAttr, tAttr = attrPrefix+"validation", attrPrefix+"type"
	}
	v := n.AttrValue(vAttr)
	t := n.AttrValue(tAttr)
	// On a literal result element the attributes are spelled xsl:validation
	// and xsl:type — an unprefixed one there would be an ordinary attribute
	// of the output. They live in the XSLT namespace, which AttrValue does
	// not search, so a literal result element asking to be validated was
	// silently not being validated at all.
	if v == "" {
		if a := n.Attr(xdm.NSXSL, "validation"); a != nil {
			v = a.Value
		}
	}
	if t == "" {
		if a := n.Attr(xdm.NSXSL, "type"); a != nil {
			t = a.Value
		}
	}
	if v != "" && t != "" {
		return spec, fmt.Errorf(
			"XTSE1505: %s and %s cannot both be present", vAttr, tAttr)
	}
	if t != "" {
		qn, err := resolveQNameAttr(n, t)
		if err != nil {
			return spec, err
		}
		spec.typeName = &qn
		return spec, nil
	}
	mode, err := parseValidationMode(v)
	if err != nil {
		return spec, err
	}
	spec.mode = mode
	return spec, nil
}

// assess validates a constructed node, if the instruction asked for it.
//
// A failure is a dynamic error rather than a recorded one: the stylesheet
// declared what it was building and built something else, and continuing
// would write output the author said was wrong.
func (spec validationSpec) assess(rt *runtime, n *xdm.Node) error {
	if spec.typeName == nil && (spec.mode == validateStrip ||
		spec.mode == validatePreserve) {
		return nil
	}
	schema := rt.sheet.schema
	if schema == nil {
		// A [xsl:]type naming a built-in needs no imported schema: the XSD
		// built-ins are always available, and requiring an import for
		// type="xs:integer" would refuse stylesheets that import nothing and
		// need nothing.
		if spec.typeName != nil && spec.typeName.URI == xdm.NSXS {
			schema = xsd.NewSchema()
		} else {
			return fmt.Errorf(
				"XTSE1660: validation requires a schema; none was imported")
		}
	}

	if spec.typeName != nil {
		if err := schema.ValidateAgainstType(n, *spec.typeName,
			xsd.ValidateOptions{}); err != nil {
			return fmt.Errorf("XTTE1540: %s is not valid against %s: %w",
				describeNode(n), spec.typeName.Lexical(), err)
		}
		return nil
	}

	if n.Kind != xdm.KindElement {
		// validation= assesses against an element declaration, so there is
		// nothing for it to do to an attribute.
		return nil
	}
	var err error
	if spec.mode == validateStrict {
		err = schema.ValidateElement(n, xsd.ValidateOptions{})
	} else {
		err = schema.ValidateElementLax(n, xsd.ValidateOptions{})
	}
	if err != nil {
		return fmt.Errorf("XTTE1510: %s is not valid: %w", describeNode(n), err)
	}
	return nil
}

func describeNode(n *xdm.Node) string {
	switch n.Kind {
	case xdm.KindAttribute:
		return "attribute " + n.Name.Local
	case xdm.KindElement:
		return "element " + n.Name.Local
	}
	return n.Kind.String()
}
