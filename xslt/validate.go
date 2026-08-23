package xslt

import (
	"fmt"
	"strings"

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
	// The unprefixed spellings are read only on an XSLT instruction. On a
	// literal result element they are ordinary attributes of the *output*,
	// and reading them as XSLT's own turned <script type="text/javascript">
	// into a request to validate against a type named "text/javascript" —
	// which then failed XTSE1660 for want of a schema, on a stylesheet that
	// asked for no validation at all. output-0154 is exactly that stylesheet.
	v, t := "", ""
	if n.Name.URI == xdm.NSXSL {
		v = n.AttrValue(vAttr)
		t = n.AttrValue(tAttr)
	}
	// On a literal result element the attributes are spelled xsl:validation
	// and xsl:type. They live in the XSLT namespace, which AttrValue does
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
			// XTSE1520 is the code for a type attribute whose value is not a
			// valid QName or whose prefix is unbound. The generic XTSE0280
			// names the condition but not the place it arose.
			return spec, fmt.Errorf(
				"XTSE1520: in %s/@%s: %w", n.Name.Lexical(), tAttr, err)
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
	if spec.typeName == nil && spec.mode == validatePreserve {
		// preserve keeps whatever the source carried, which the copy already
		// has: there is nothing to assess and nothing to remove.
		return nil
	}
	if spec.typeName == nil && spec.mode == validateStrip {
		// strip is the other half of the same rule: the copy is untyped
		// however the source was annotated. This is where it happens, because
		// the copy carries the annotation forward by default so that preserve
		// has something to preserve.
		stripAnnotations(n)
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

	if spec.typeName != nil && spec.typeName.URI == xdm.NSXS {
		// xs:untyped and xs:untypedAtomic are the annotations a node that was
		// never validated carries, so naming one as the type is a request for
		// exactly that: the node comes out untyped rather than being checked
		// against a schema component, which is what "no type named
		// {…XMLSchema}untyped in the schema" was complaining about. xs:anyType
		// is the other end of the same idea — every element is valid against
		// it — and validating against it likewise constrains nothing.
		switch spec.typeName.Local {
		case "untyped", "untypedAtomic", "anyType":
			stripAnnotations(n)
			return nil
		}
	}

	if spec.typeName != nil && n.Kind == xdm.KindDocument {
		// A [xsl:]type on a document node applies to the element the document
		// contains: a schema describes elements, not documents. The same
		// XTTE1550 shape applies as for validation=, so the document must have
		// exactly one element child and no significant text. Passing the
		// document node straight to the validator produced "needs an element
		// or attribute", which is a complaint about the caller rather than
		// about the stylesheet.
		elem, err := soleElementChild(n)
		if err != nil {
			return err
		}
		n = elem
	}

	if spec.typeName != nil {
		// XTTE1545: an attribute may not be validated against a type derived
		// from, or built by list or union from, xs:ID, xs:IDREF, xs:IDREFS,
		// xs:ENTITY or xs:ENTITIES. Those types carry document-level
		// identity, which a constructed attribute has no document to have.
		if n.Kind == xdm.KindAttribute {
			if bad, why := namespaceSensitiveType(schema, *spec.typeName); bad {
				return fmt.Errorf(
					"XTTE1545: attribute %s cannot be validated against %s, "+
						"which is %s", n.Name.Local, spec.typeName.Lexical(), why)
			}
		}
		// Annotate: the whole point of validating a constructed node is that
		// the result carries the type it was validated against, so that
		// "instance of element(x, my:t)" and a match pattern naming a type
		// answer true for it. Without the annotation the node came out of a
		// successful validation still untyped.
		if err := schema.ValidateAgainstType(n, *spec.typeName,
			xsd.ValidateOptions{Annotate: true}); err != nil {
			return fmt.Errorf("XTTE1540: %s is not valid against %s: %w",
				describeNode(n), spec.typeName.Lexical(), err)
		}
		return nil
	}

	if n.Kind == xdm.KindDocument {
		elem, err := soleElementChild(n)
		if err != nil {
			return err
		}
		n = elem
	}

	if n.Kind == xdm.KindAttribute {
		// An attribute is assessed against the *global attribute*
		// declaration for its name, which is the attribute counterpart of
		// what strict and lax do for an element. Passing it over left an
		// attribute copied under validation="strict" untyped, so a template
		// declaring as="attribute(a, my:t)" rejected its own result.
		if spec.mode == validateStrict && !schema.HasAttributeDeclaration(n.Name) {
			return fmt.Errorf(
				"XTTE1512: no top-level declaration for %s", describeNode(n))
		}
		if err := schema.ValidateAttribute(n, spec.mode != validateStrict,
			xsd.ValidateOptions{Annotate: true}); err != nil {
			return fmt.Errorf("XTTE1510: %s is not valid: %w",
				describeNode(n), err)
		}
		return nil
	}
	if n.Kind != xdm.KindElement {
		// Nothing else carries a type annotation, so there is nothing to
		// assess.
		return nil
	}
	var err error
	if spec.mode == validateStrict {
		if !schema.HasElementDeclaration(n.Name) {
			// XTTE1512 is the specific code for strict validation finding no
			// top-level declaration to assess against, as distinct from
			// XTTE1510, which says the node was assessed and found invalid.
			return fmt.Errorf(
				"XTTE1512: no top-level declaration for %s", describeNode(n))
		}
		err = schema.ValidateElement(n, xsd.ValidateOptions{Annotate: true})
	} else {
		err = schema.ValidateElementLax(n, xsd.ValidateOptions{Annotate: true})
	}
	if err != nil {
		return fmt.Errorf("XTTE1510: %s is not valid: %w", describeNode(n), err)
	}
	return nil
}

// soleElementChild returns the one element child a validated document node is
// required to have.
//
// XTTE1550: validating a document node requires its children to be exactly one
// element, no significant text, and any number of comments and processing
// instructions. It is that element child that is then validated, since a
// schema describes elements rather than documents.
func soleElementChild(n *xdm.Node) (*xdm.Node, error) {
	var elem *xdm.Node
	for _, c := range n.Children {
		switch c.Kind {
		case xdm.KindElement:
			if elem != nil {
				return nil, fmt.Errorf(
					"XTTE1550: a validated document node must have exactly " +
						"one element child")
			}
			elem = c
		case xdm.KindText:
			if strings.TrimSpace(c.Value) != "" {
				return nil, fmt.Errorf(
					"XTTE1550: a validated document node must have no text " +
						"node children")
			}
		}
	}
	if elem == nil {
		return nil, fmt.Errorf(
			"XTTE1550: a validated document node must have exactly one " +
				"element child")
	}
	return elem, nil
}

// stripAnnotations removes every type annotation from a subtree, which is what
// validation="strip" means.
func stripAnnotations(n *xdm.Node) {
	if n == nil {
		return
	}
	n.TypeAnnotation = ""
	for _, a := range n.Attrs {
		a.TypeAnnotation = ""
	}
	for _, c := range n.Children {
		stripAnnotations(c)
	}
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

// namespaceSensitiveType reports whether a named type is, or derives from,
// xs:QName or xs:NOTATION.
//
// Both carry a namespace binding rather than only characters, so their value
// depends on the namespace context of the element the lexical form was written
// on. A constructed attribute's value is a string with no such context, which
// is why section 19.2 forbids validating one against these types instead of
// letting the prefix resolve against nothing.
func namespaceSensitiveType(schema *xsd.Schema, name xdm.QName) (bool, string) {
	local := name.Local
	for i := 0; i < 32 && local != ""; i++ {
		switch local {
		case "QName", "NOTATION":
			return true, "derived from xs:" + local
		}
		next := xdm.DerivedBase(local)
		if next == local {
			break
		}
		local = next
	}
	return false, ""
}
