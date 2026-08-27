package xslt

import (
	"errors"
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
	// constructsElement records that the instruction builds a NEW element
	// rather than copying one wholesale. Section 19.2 makes validation=
	// "preserve" mean two different things depending on that: xsl:copy-of
	// copies nodes and "all the nodes that are copied will retain their type
	// annotations unchanged", but xsl:element, a literal result element and
	// xsl:copy over an element all give the new element the annotation
	// xs:anyType — for xsl:copy explicitly "because this instruction does not
	// copy the content of the element, it would be wrong to assume that the
	// type is unchanged". The distinction is not visible from the node handed
	// to assess, so it is recorded when the instruction is compiled.
	constructsElement bool
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
		// Section 19.2.1.2: "If the QName has no prefix, it is expanded using
		// the default namespace established using the effective
		// [xsl:]xpath-default-namespace attribute if there is one; otherwise,
		// it is taken as being a name in no namespace." Section 5.2 lists
		// both the type attribute of an XSLT element and the xsl:type
		// attribute of a literal result element among the places this
		// applies. Resolving an unprefixed type name in no namespace made a
		// schema-declared type unfindable under a default namespace.
		if qn.Prefix == "" && qn.URI == "" {
			qn.URI = xpathDefaultNamespaceAt(n)
		}
		spec.typeName = &qn
		return spec, nil
	}
	if v == "" {
		// §3.6: with neither attribute present "the effect is the same as
		// specifying the validation attribute with the value specified in
		// the default-validation attribute of the containing xsl:stylesheet
		// element". The default is read from the module the instruction is
		// written in, because the specification says in as many words that
		// it "does not extend to included or imported stylesheet modules" —
		// so it is found by walking up from this node rather than held once
		// on the compiler.
		v = moduleDefaultValidation(n)
	}
	mode, err := parseValidationMode(v)
	if err != nil {
		return spec, err
	}
	spec.mode = mode
	spec.constructsElement = constructsElement(n)
	return spec, nil
}

// constructsElement reports whether an instruction builds a new element node,
// as opposed to copying existing nodes wholesale.
//
// Only three constructs do: xsl:element, xsl:copy (when the context node is an
// element — which assess re-checks, since the same compiled spec serves a
// context node of any kind), and a literal result element. xsl:copy-of and
// xsl:document copy or wrap, and validation="preserve" leaves their
// annotations alone.
func constructsElement(n *xdm.Node) bool {
	if n == nil {
		return false
	}
	if n.Name.URI != xdm.NSXSL {
		// A literal result element: anything not in the XSLT namespace that
		// reached compileValidation is one.
		return true
	}
	switch n.Name.Local {
	case "element", "copy":
		return true
	}
	return false
}

// moduleDefaultValidation returns the default-validation in force at n, or ""
// when none is declared.
//
// Section 24.4 settles which one applies: "the [xsl:]default-validation
// attribute of the innermost containing element having such an attribute".
// It is a standard attribute, so any element in the ancestor chain may carry
// it -- an XSLT element unprefixed, a literal result element in the XSLT
// namespace -- and the nearest one wins. Reading it only from the module
// element ignored import-schema-195..197, which set it on an inner LRE and on
// xsl:template.
//
// The walk stops at the module element, which is what keeps the spec's other
// half true: the default "does not extend to included or imported stylesheet
// modules or used packages".
func moduleDefaultValidation(n *xdm.Node) string {
	for a := n; a != nil; a = a.Parent {
		if a.Kind != xdm.KindElement {
			continue
		}
		if a.Name.URI == xdm.NSXSL {
			if v := a.AttrValue("default-validation"); v != "" {
				return v
			}
			switch a.Name.Local {
			case "stylesheet", "transform", "package":
				return ""
			}
			continue
		}
		// A literal result element spells the standard attribute with the
		// xsl: prefix, to keep it apart from a user-defined attribute.
		for _, at := range a.Attrs {
			if at.Name.URI == xdm.NSXSL && at.Name.Local == "default-validation" {
				return at.Value
			}
		}
	}
	return ""
}

// assess validates a constructed node, if the instruction asked for it.
//
// A failure is a dynamic error rather than a recorded one: the stylesheet
// declared what it was building and built something else, and continuing
// would write output the author said was wrong.
func (spec validationSpec) assess(rt *runtime, n *xdm.Node) error {
	// XSLT 2.0 §11.9.1 and §11.9.2, in identical words for xsl:copy and
	// xsl:copy-of: "These attributes are ignored when copying an item that is
	// not an element, attribute or document node." Only those three kinds
	// carry a type annotation, so there is nothing for a validation or type
	// attribute to say about a comment, a text node or a processing
	// instruction. Passing one through reached xsd.ValidateAgainstType, which
	// answered with a complaint about its caller ("needs an element or
	// attribute") rather than about the stylesheet.
	if n != nil {
		switch n.Kind {
		case xdm.KindElement, xdm.KindAttribute, xdm.KindDocument:
		default:
			return nil
		}
	}
	if spec.typeName == nil && spec.mode == validatePreserve {
		// preserve keeps whatever the source carried, which the copy already
		// has: there is nothing to assess and nothing to remove — except on
		// an instruction that CONSTRUCTS an element, where §19.2 requires the
		// new element itself to be annotated xs:anyType while its contained
		// nodes keep whatever they carried. Leaving it unannotated made
		// "instance of element(*, xs:anyType)" indistinguishable from
		// xs:untyped, which is the whole distinction import-schema-076 draws.
		if spec.constructsElement && n != nil && n.Kind == xdm.KindElement {
			n.SetTypeAnnotation("anyType")
		}
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
		case "untyped", "anyType":
			stripAnnotations(n)
			return nil
		case "untypedAtomic":
			// xs:untypedAtomic is the *simple* end of the untyped pair: it is
			// what an unvalidated attribute's typed value is. Naming it as the
			// type therefore asks for a node whose content is a single
			// untypedAtomic value, and an element with element children has no
			// such value at all — there is nothing for the atomic to be. That
			// makes it a type error under XTTE1540, exactly as validating
			// against any other simple type with element content would be.
			//
			// The three cases were previously folded together and all three
			// simply stripped. Stripping is right for xs:untyped and
			// xs:anyType, whose annotations are what an absent annotation
			// already means. It is wrong here: an element with an empty
			// annotation reads back as xs:untyped, so
			// "instance of element(*, xs:untypedAtomic)" answered false for a
			// node the stylesheet had just validated as untypedAtomic. The
			// annotation is written instead, which is what the general
			// xsl:type path does for every named type.
			if n.Kind == xdm.KindElement {
				for _, c := range n.Children {
					if c.Kind == xdm.KindElement {
						return fmt.Errorf(
							"XTTE1540: %s is not valid against %s: an element "+
								"with element children has no atomic value",
							describeNode(n), spec.typeName.Lexical())
					}
				}
			}
			stripAnnotations(n)
			n.TypeAnnotation = "untypedAtomic"
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
			// XTTE1535: "It is a type error if the value of the type
			// attribute of an xsl:copy or xsl:copy-of instruction refers to a
			// complex type definition and one or more of the items being
			// copied is an attribute node." The instruction is not recorded
			// on the spec, but it does not need to be: xsl:attribute naming a
			// complex type is already the static error XTSE1530, and no other
			// instruction carrying a type attribute can produce an attribute
			// node at all. So an attribute reaching here against a complex
			// type came from xsl:copy or xsl:copy-of, and 1535 is the code
			// the spec gives it rather than the general 1540.
			if _, complex := schema.Types[xdm.QName{URI: spec.typeName.URI, Local: spec.typeName.Local}].(*xsd.ComplexType); complex {
				return fmt.Errorf(
					"XTTE1535: attribute %s cannot be copied against %s, "+
						"which is a complex type",
					n.Name.Local, spec.typeName.Lexical())
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

	// XTTE1555: validating a *document* node applies the document-level
	// constraints, ID/IDREF among them, over the whole tree. They are checked
	// after the element has been assessed, because it is that assessment
	// which annotates the tree and so says which nodes carry IDs at all.
	docNode := false
	if n.Kind == xdm.KindDocument {
		elem, err := soleElementChild(n)
		if err != nil {
			return err
		}
		n = elem
		docNode = true
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
			return fmt.Errorf("%s: %s is not valid: %w",
				invalidCode(spec.mode), describeNode(n), err)
		}
		return nil
	}
	if n.Kind != xdm.KindElement {
		// Nothing else carries a type annotation, so there is nothing to
		// assess.
		return nil
	}
	// XSLT 2.0 §19.2.1.3: validating a constructed *element* takes into
	// account only constraints on its own content. "The validation rule
	// 'Validation Root Valid (ID/IDREF)' is not applied. This means that
	// validation will not fail if there are non-unique ID values or dangling
	// IDREF values in the subtree being validated." §19.2.2 applies the same
	// rule when the validation root is a document node, and XTTE1555 is the
	// code for failing it there. Applying it to a bare element reported five
	// duplicate-ID failures as XTTE1510 for element constructions the spec
	// says are valid.
	vopts := xsd.ValidateOptions{Annotate: true, SkipIDConstraints: !docNode}
	var err error
	if spec.mode == validateStrict {
		// CanAssessStrictly rather than HasElementDeclaration: an element
		// carrying xsi:type is assessed against the type it names even with
		// no declaration of its own, so refusing it here would report a
		// missing declaration for a document the schema can in fact assess.
		if !schema.CanAssessStrictly(n) {
			// XTTE1512 is the specific code for strict validation finding no
			// top-level declaration to assess against, as distinct from
			// XTTE1510, which says the node was assessed and found invalid.
			return fmt.Errorf(
				"XTTE1512: no top-level declaration for %s", describeNode(n))
		}
		err = schema.Validate(n, vopts)
	} else {
		err = schema.ValidateElementLax(n, vopts)
	}
	if err != nil {
		// XTTE1555 rather than XTTE1510/XTTE1515 when the only thing the
		// assessment found wrong was an ID/IDREF constraint, and the node
		// being validated was a document node. See idConstraintFailure.
		if docNode && idConstraintFailure(err) {
			return fmt.Errorf("XTTE1555: %s is not valid: %w",
				describeNode(n), err)
		}
		code := invalidCode(spec.mode)
		if unresolvableXSIType(err) {
			// An xsi:type naming a type the schema cannot resolve is not
			// "lax assessment found the element invalid": nothing was
			// assessed, because the type to assess against does not exist.
			// XTTE1515 is defined for the case where lax validity assessment
			// ran and reported invalid, so this falls to XTTE1510 in both
			// modes. validation-1701 and validation-1702 run the same
			// stylesheet under strict and under lax and expect XTTE1510
			// from both.
			code = "XTTE1510"
		}
		return fmt.Errorf("%s: %s is not valid: %w",
			code, describeNode(n), err)
	}
	if docNode {
		return checkDocumentIDs(n)
	}
	return nil
}

// idConstraintFailure reports whether every failure in a validation error is
// an ID/IDREF constraint (cvc-id.1, a dangling IDREF, or cvc-id.2, an ID
// bound twice).
//
// XTTE1555 covers "document-level constraints are not satisfied" when a
// document node is validated, and the ID/IDREF constraints are exactly the
// document-level ones. The identity constraints named alongside them in the
// text of XTTE1555 are NOT: test error-1555c carries a note from the suite's
// editor saying in as many words that "xs:unique/key/keyref are not
// document-level constraints, and they do not result in the 1555 error code",
// and that test expects XTTE1510. So the discrimination is on the cvc-id
// codes specifically and not on "an identity-shaped rule failed".
//
// Every failure must be an ID failure, not merely one of them. A document
// that is also wrong in its content model was found invalid for a reason that
// has nothing to do with document-level constraints, and reporting XTTE1555
// for it would tell the stylesheet the wrong thing about why it failed.
func idConstraintFailure(err error) bool {
	var errs *xsd.ValidationErrors
	if !errors.As(err, &errs) || len(errs.Errors) == 0 {
		return false
	}
	for _, e := range errs.Errors {
		if !strings.HasPrefix(e.Code, "cvc-id.") {
			return false
		}
	}
	return true
}

// unresolvableXSIType reports whether the assessment failed because an
// xsi:type attribute names a type the schema has no definition for.
//
// cvc-elt.4.2 is the Schema Part 1 constraint that the local type definition
// an xsi:type names must be resolvable, so its presence is the evidence that
// no assessment against a type ever happened. Every error must be that one:
// an element whose content is also wrong was genuinely assessed against
// something, and the mode's own code is right for it.
func unresolvableXSIType(err error) bool {
	var errs *xsd.ValidationErrors
	if !errors.As(err, &errs) || len(errs.Errors) == 0 {
		return false
	}
	for _, e := range errs.Errors {
		if !strings.HasPrefix(e.Code, "cvc-elt.4.") {
			return false
		}
	}
	return true
}

// invalidCode is the error code for a node that was assessed and found
// invalid, which differs by mode.
//
// XTTE1510 is the strict code and XTTE1515 the lax one. They are distinct
// because lax has two outcomes strict does not — "no declaration, so not
// assessed" is a pass under lax — so a stylesheet catching one is not asking
// about the other.
func invalidCode(mode validationMode) string {
	if mode == validateLax {
		return "XTTE1515"
	}
	return "XTTE1510"
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

// checkDocumentIDs applies the ID/IDREF half of the document-level constraints
// to an already-annotated tree.
//
// Only nodes carrying an annotation count: an unvalidated attribute named "id"
// is not an xs:ID, and treating it as one would invent constraints the
// stylesheet never asked for.
func checkDocumentIDs(doc *xdm.Node) error {
	owners := map[string]*xdm.Node{}
	var refs []string
	var walk func(n *xdm.Node)
	noteRefs := func(ann, value string) {
		switch ann {
		case "IDREF":
			refs = append(refs, value)
		case "IDREFS":
			refs = append(refs, strings.Fields(value)...)
		}
	}
	// A value bound to two *different* elements is the clash. The same value
	// twice on one element is one binding, which is why the owner is
	// compared rather than the value counted.
	var dupes []string
	noteID := func(owner *xdm.Node, value string) {
		prev, seen := owners[value]
		switch {
		case !seen:
			owners[value] = owner
		case prev != owner:
			dupes = append(dupes, value)
		}
	}
	walk = func(n *xdm.Node) {
		if n == nil {
			return
		}
		for _, a := range n.Attrs {
			if a.TypeAnnotation == "ID" {
				noteID(n, a.Value)
			} else {
				noteRefs(a.TypeAnnotation, a.Value)
			}
		}
		if n.Kind == xdm.KindElement {
			if n.TypeAnnotation == "ID" {
				noteID(n, n.StringValue())
			} else {
				noteRefs(n.TypeAnnotation, n.StringValue())
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(doc)
	if len(dupes) > 0 {
		return fmt.Errorf(
			"XTTE1555: ID value %q is defined more than once in the "+
				"document", dupes[0])
	}
	for _, r := range refs {
		if _, ok := owners[r]; !ok {
			return fmt.Errorf(
				"XTTE1555: IDREF %q does not match any ID in the document", r)
		}
	}
	return nil
}
