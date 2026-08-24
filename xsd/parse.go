package xsd

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/knroy/go-xml/xdm"
)

// A schemaDoc is one schema document being read.
//
// The unit matters because a schema is a set of components assembled from
// possibly many documents, and several properties — the target namespace, the
// element and attribute form defaults, the block and final defaults — are
// document-scoped. A component built from one document keeps those settings
// even after it joins a schema that other documents also contributed to.
type schemaDoc struct {
	root *xdm.Node

	// targetNS is the target namespace of this document. The empty string
	// means the absent namespace, which XSD distinguishes from a namespace
	// whose name is empty — but since no namespace may be named "" in XML,
	// the two cannot collide and one field carries both meanings.
	targetNS string

	// chameleon records that this document had no target namespace of its
	// own and adopted the includer's. References written unprefixed in it
	// have to be converted along with the declarations they name.
	chameleon bool

	// hasTargetNS records whether targetNamespace was written at all. A
	// chameleon include needs this: a document with no target namespace
	// adopts the includer's, and that rewrite must not apply to a document
	// that declared one.
	hasTargetNS bool

	elementFormQualified   bool
	attributeFormQualified bool

	blockDefault DerivationSet
	finalDefault DerivationSet

	// baseURI locates this document, for resolving include and import.
	baseURI string

	// ids records the xs:ID-typed id= attributes seen in this document, so
	// that a repeat can be reported. ID uniqueness is scoped to one XML
	// document, not to the assembled schema: MS-Additional addA002 and
	// MS-Group groupA006 each write one id= in the including document and
	// the same id= in the document it pulls in, and both are valid.
	ids map[string]bool

	// defaultAttributes is the XSD 1.1 schema-level attribute group applied
	// to every complex type in this document, unless the type opts out with
	// defaultAttributesApply="false".
	defaultAttributes string

	// defaultOpenContent is the XSD 1.1 <xs:defaultOpenContent>, applied to
	// every complex type in the document that does not declare its own.
	defaultOpenContent *OpenContent

	// appliesToEmpty records defaultOpenContent's appliesToEmpty, which
	// decides whether a type with empty content is opened too. It defaults
	// to false, so a type declaring no content model stays closed.
	appliesToEmpty bool
}

// ParseError reports a fault in a schema document.
//
// The spec gives error codes for schema representation faults (the src-* and
// *-props-correct codes). Carrying the code rather than only a message lets a
// caller — and the conformance harness — distinguish "this schema is malformed"
// from "this schema is not the one you meant".
type ParseError struct {
	// Code is the spec's error code, such as "src-element.1", or empty if
	// the fault has no code in the spec.
	Code string
	// Message describes the fault.
	Message string
	// Line and Column locate it, when the node carried a position.
	Line, Column int
}

// Error implements error.
func (e *ParseError) Error() string {
	var b strings.Builder
	if e.Line > 0 {
		fmt.Fprintf(&b, "%d:%d: ", e.Line, e.Column)
	}
	if e.Code != "" {
		b.WriteString(e.Code)
		b.WriteString(": ")
	}
	b.WriteString(e.Message)
	return b.String()
}

// errorAt builds a ParseError located at a node.
func errorAt(n *xdm.Node, code, format string, args ...any) *ParseError {
	e := &ParseError{Code: code, Message: fmt.Sprintf(format, args...)}
	if n != nil {
		if line, col, ok := n.Position(); ok {
			e.Line, e.Column = line, col
		}
	}
	return e
}

// parser reads schema documents into components.
//
// Reading happens in two passes over each document. The first pass records
// every global declaration by name; the second resolves references and builds
// the component graph. Two passes are needed because a schema document may
// refer to a name declared later in the same document, or in a document not yet
// read — forward references are not merely permitted but idiomatic.
type parser struct {
	schema *Schema
	doc    *schemaDoc

	// errs accumulates faults rather than stopping at the first, because a
	// schema author fixing a file wants to see every fault in it.
	errs []error

	// fixups are deferred resolutions, run after every document has been
	// read. Each returns an error naming the unresolvable reference.
	fixups []func() error

	// icRefs records each <xs:key|keyref|unique ref="..."/> placeholder and
	// where it sits, so finish can swap in the component it names.
	icRefs []icRefSlot

	// unresolvedImports holds the namespaces an <xs:import> named but whose
	// schemaLocation could not be fetched. A reference into one of them
	// cannot be resolved and cannot be an assembly error either — see
	// absentNamespace.
	unresolvedImports map[string]bool

	// postFixups run once the fixups have drained. A fixup may queue
	// another, so a check that must see the *settled* component graph
	// cannot be a fixup itself — it would run somewhere in the middle.
	postFixups []func() error

	// pendingSplice holds each extension's content-model splice, keyed by
	// the type it belongs to, so that a type whose base has not been
	// spliced yet can pull the base's splice forward rather than reading a
	// half-built model. splicedNow records the ones already run.
	pendingSplice map[*ComplexType]func(map[*ComplexType]bool)
	splicedNow    map[*ComplexType]bool

	// attrsDone marks the complex types whose inherited attributes have
	// been resolved, so that a base shared by many derived types is walked
	// once rather than once per derivation.
	attrsDone map[*ComplexType]bool

	// simpleTypes records every simple type read, with the element it came
	// from, so that the Part 2 facet schema-component constraints can be
	// applied once the base chain is resolved. They cannot be checked while
	// reading: a restriction's base may be a forward reference, and a
	// facet's legality depends on the base's primitive and on the facets
	// the base already fixed.
	simpleTypes []simpleTypeSite

	// inOverride records that the components being read are the
	// replacements inside an <xs:override>. The document's
	// defaultAttributes and defaultOpenContent do not reach them — the
	// suite says so in as many words, "defaultAttributes does not apply to
	// types defined within xs:override" — because an override's job is to
	// say what a component in *another* document should be, and that
	// document's defaults are not this one's to supply.
	inOverride bool
}

// simpleTypeSite pairs a simple type with the schema element that defined it,
// so a facet constraint violated deep in a derivation can be reported at the
// line the author wrote.
type simpleTypeSite struct {
	typ *SimpleType
	el  *xdm.Node
}

// Schema is a set of schema components, assembled from one or more documents.
//
// The spec is explicit that a schema is a set of components rather than a
// document or a collection of documents (§4.2). Several documents may
// contribute to one namespace, and once assembled there is no way to ask which
// document a component came from — nor any need to.
type Schema struct {
	// Elements and the maps beside it are the global components, keyed by
	// expanded name. Local declarations are reachable only through the type
	// that contains them and are not indexed here.
	Elements        map[xdm.QName]*ElementDecl
	Attributes      map[xdm.QName]*AttributeDecl
	Types           map[xdm.QName]Type
	AttributeGroups map[xdm.QName]*AttributeGroupDef
	ModelGroups     map[xdm.QName]*ModelGroupDef
	Notations       map[xdm.QName]*NotationDecl

	// identityConstraints indexes key and unique definitions so that a
	// keyref can find the one it refers to. The spec scopes these names to
	// the whole schema, not to the element carrying the constraint.
	identityConstraints map[xdm.QName]*IdentityConstraint

	// Version selects XSD 1.0 or 1.1 behaviour. It governs whether the 1.1
	// features a document may use — assertions, conditional type
	// assignment, open content — are honoured; they are always *parsed*,
	// because a schema that uses them is not made valid by pretending they
	// are absent.
	Version Version

	// sourcePaths records the documents this schema was loaded from, so
	// that WithInstanceLocations can assemble them again alongside the ones
	// an instance names. A schema is not the union of its documents — a
	// type in one may extend a type in another — so extending it means
	// reassembling rather than merging.
	sourcePaths []string

	// allComplexTypes holds every complex type read, anonymous ones
	// included, in the order they were read. Types holds only the named
	// ones; the schema component constraints apply to both, and an
	// inline <xs:complexType> is a component like any other.
	//
	// The slice is append-only and never keyed, so the order is the
	// document order rather than a map walk — which keeps the errors a
	// schema reports stable between runs.
	allComplexTypes []*ComplexType

	// models caches compiled content models, keyed by complex type. It is
	// a sync.Map because the access pattern is write-once then read-many —
	// after the first document there are no more writes — and because a
	// Schema is documented as safe to share between goroutines once loaded.
	models sync.Map
}

// Version selects the XML Schema version a schema is interpreted under.
type Version uint8

// The supported versions.
const (
	// Version10 is XML Schema 1.0, the default.
	Version10 Version = iota
	// Version11 is XML Schema 1.1: assertions, conditional type
	// assignment, open content, and the relaxed rules that come with them.
	Version11
)

// String names the version.
func (v Version) String() string {
	if v == Version11 {
		return "1.1"
	}
	return "1.0"
}

// NewSchema returns an empty schema populated with the built-in types.
//
// The built-ins are present in every schema "by definition" (§4.1.2) without
// being declared, so they are added here rather than by reading a document.
func NewSchema() *Schema {
	s := &Schema{
		Elements:            map[xdm.QName]*ElementDecl{},
		Attributes:          map[xdm.QName]*AttributeDecl{},
		Types:               map[xdm.QName]Type{},
		AttributeGroups:     map[xdm.QName]*AttributeGroupDef{},
		ModelGroups:         map[xdm.QName]*ModelGroupDef{},
		Notations:           map[xdm.QName]*NotationDecl{},
		identityConstraints: map[xdm.QName]*IdentityConstraint{},
	}
	for name, t := range builtinTypes() {
		s.Types[name] = t
	}
	for name, d := range builtinAttributes() {
		s.Attributes[name] = d
	}
	return s
}

// ParseSchema reads one schema document into a new schema.
//
// Include, import and redefine are not followed: this reads a single document.
// Assembling a schema from several documents is a separate concern, because it
// needs a resolver policy that a caller must supply — following a
// schemaLocation means fetching whatever the schema names, which is a decision
// about trust rather than about parsing.
func ParseSchema(root *xdm.Node) (*Schema, error) {
	// See Schema.Validate on why this is an error rather than a panic.
	if root == nil {
		return nil, fmt.Errorf("no schema document to parse")
	}
	s := NewSchema()
	p := &parser{schema: s, attrsDone: map[*ComplexType]bool{}}
	if err := p.readDocument(root, ""); err != nil {
		return nil, err
	}
	if err := p.finish(); err != nil {
		return nil, err
	}
	// The substitution-group closure is computed here as well as in the
	// assembler. Without it a single-document parse leaves every group
	// empty, so no member substitutes for its head — the feature silently
	// does nothing on the one entry point that reads a schema from a node
	// rather than from files.
	linkSubstitutionGroups(s)
	if err := p.checkParticleRestriction(); err != nil {
		return nil, err
	}
	// Unique Particle Attribution and Element Declarations Consistent are
	// checked after restriction for the same reason it runs last: both walk
	// compiled content models, which need every base resolved and every
	// group spliced.
	if err := checkContentModelConstraints(s, CheckOptions{Version: s.Version}); err != nil {
		return nil, err
	}
	if err := checkAllGroupLimited(s); err != nil {
		return nil, err
	}
	// Registered here as well as in the assembler, and for the same reason
	// linkSubstitutionGroups is: this entry point reads a schema from a node
	// rather than from files, and skipping the step left the data model with
	// no record of which built-in each user-defined type erases to. The
	// consequence is not confined to atomisation -- SetTypeAnnotation walks
	// this same derivation chain to decide is-id and is-idrefs, so an
	// element whose type extends xs:ID was not recognised as an ID at all,
	// and fn:id could not find it.
	registerDerivedTypes(s)
	return s, nil
}

// absentNamespace reports whether a QName that failed to resolve names a
// namespace that was imported but whose document could not be fetched.
//
// §5.3 Missing Sub-components makes this case explicitly not a schema error:
// an unresolvable QName leaves the property · absent ·, and the consequence is
// deferred to validation, where it acts as if clause 1 of Attribute Locally
// Valid or Element Locally Valid had failed. Assembly must therefore carry on.
//
// The suite's own metadata schema, common/xsts.xsd, is the case in point: it
// imports the XLink namespace from an http:// URL that this processor does not
// fetch, then writes <xsd:attribute ref="xlink:type"/>. Failing there rejected
// a schema every conforming processor loads.
//
// This is deliberately narrow. Only a namespace an import actually named is
// eligible, so a plain typo in a prefix bound to a local namespace still fails
// at the reference, which is what makes most missing-reference tests work.
func (p *parser) absentNamespace(ns string) bool {
	return p.unresolvedImports[ns]
}

// icRefSlot locates one ref= identity-constraint placeholder in the list of
// the element declaration that carries it.
type icRefSlot struct {
	decl *ElementDecl
	slot int
	ic   *IdentityConstraint
}

// finish runs the deferred reference resolutions and returns the accumulated
// faults, if any.
func (p *parser) finish() error {
	// Fixups are run in the order they were queued, and one may need what
	// another writes — a type whose simple content comes from a base whose
	// own is filled in later, for instance. Neither can arrange the order
	// for itself, so a fixup that finds its input missing queues a second
	// pass, and the loop keeps going until nothing new is queued.
	for i := 0; i < len(p.fixups); i++ {
		if err := p.fixups[i](); err != nil {
			p.errs = append(p.errs, err)
		}
	}

	// Substitute the referenced component for every ref= placeholder, now
	// that the fixups above have resolved them. This runs after the whole
	// fixup drain because a placeholder may name a keyref whose own refer=
	// is bound by a fixup queued later than the one that found it.
	for _, r := range p.icRefs {
		if r.ic.resolved != nil {
			r.decl.IdentityConstraints[r.slot] = r.ic.resolved
		}
	}

	// A final sweep over every complex type. A redefine's replacements are
	// read after the main pass, so the fixup their inheritAttributes queued
	// has already been drained by the time they exist — and the type is
	// left without the attributes its base supplies. The attrsDone guard
	// makes this idempotent, so a type already resolved is not touched.
	for _, t := range p.schema.Types {
		if ct, ok := t.(*ComplexType); ok {
			p.resolveAttributes(ct, nil)
		}
	}

	// Checks that need every fixup to have run, including the fixups other
	// fixups queue: a value constraint is validated against a type whose
	// own members and base are themselves filled in by fixups.
	for i := 0; i < len(p.postFixups); i++ {
		if err := p.postFixups[i](); err != nil {
			p.errs = append(p.errs, err)
		}
	}

	// Circular attribute group references are diagnosed once every ref edge
	// has been resolved, which is only true after the fixups have drained.
	p.checkAttributeGroupCycles()

	// Circular group references, once every group ref has been bound to the
	// group it names. This has to follow the fixups: a <group ref> becomes a
	// term only when its fixup runs, so before that point the cycle the
	// check is looking for does not exist in the component graph yet.
	p.checkGroupCycles()

	// Circular base type definitions, once every base reference has been
	// bound. Same ordering reason as the two cycle checks above: before the
	// fixups drain, {base type definition} is nil everywhere.
	p.checkTypeBaseCycles()
	p.checkUnionMemberCycles()

	// The Part 2 facet constraints run last, once every base reference has
	// been bound: they compare a facet against the base's primitive and
	// against the facets the base itself set, neither of which is known
	// while the restriction is being read.
	p.checkFacetConstraints()

	if len(p.errs) == 0 {
		return nil
	}
	return &SchemaErrors{Errors: p.errs}
}

// SchemaErrors collects every fault found in a schema.
type SchemaErrors struct {
	Errors []error
}

// Error implements error, listing every fault.
func (e *SchemaErrors) Error() string {
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d schema errors:", len(e.Errors))
	for _, err := range e.Errors {
		b.WriteString("\n  ")
		b.WriteString(err.Error())
	}
	return b.String()
}

// Unwrap exposes the faults to errors.As and errors.Is.
func (e *SchemaErrors) Unwrap() []error { return e.Errors }

// readDocument reads one <xs:schema> element.
func (p *parser) readDocument(root *xdm.Node, baseURI string) error {
	if root.Kind == xdm.KindDocument {
		els := root.ChildElements()
		if len(els) == 0 {
			return errorAt(root, "", "schema document is empty")
		}
		root = els[0]
	}
	if !root.IsElement(NSSchema, "schema") {
		return errorAt(root, "",
			"schema document root is %s, want {%s}schema", root.Name.Local, NSSchema)
	}

	doc := &schemaDoc{root: root, baseURI: baseURI}
	if a := root.Attr("", "targetNamespace"); a != nil {
		doc.targetNS = a.Value
		doc.hasTargetNS = true
	}
	doc.elementFormQualified = p.formDefault(root, "elementFormDefault")
	doc.attributeFormQualified = p.formDefault(root, "attributeFormDefault")
	doc.defaultAttributes = root.AttrValue("defaultAttributes")

	var err error
	if doc.blockDefault, err = p.derivationSet(root, "blockDefault"); err != nil {
		p.errs = append(p.errs, err)
	}
	if doc.finalDefault, err = p.derivationSet(root, "finalDefault"); err != nil {
		p.errs = append(p.errs, err)
	}
	p.doc = doc

	// The versioning attributes may sit on <xs:schema> itself, and there
	// they make the whole document invisible to a processor the conditions
	// exclude. It is how a schema says "this file is for some other
	// version" without the reader having to understand its contents.
	if !includeElement(root, p.schema.Version) {
		return nil
	}

	// checkIDs walks the whole document, <xs:schema> included, so it is the
	// single place the id attribute is checked. An earlier arrangement also
	// checked it per element on the source-model walk, which reported every
	// duplicate twice in two different wordings.
	p.checkIDs(root)

	for _, el := range root.ChildElements() {
		p.readTopLevel(el)
	}
	return nil
}

// checkIDs validates the id attribute of every element in a schema document.
//
// Nearly every element in the schema for schemas carries `id = ID`, so the
// check is one walk of the tree rather than a line in each of the twenty-odd
// readers that would otherwise need it. Being xs:ID gives it two properties:
// the value is an NCName, and it is unique within the document.
//
// The uniqueness map is per document, not per schema: xs:ID uniqueness is a
// property of one XML document, and two included documents may each use the
// same id without conflict. attgA005 (two attribute groups sharing id="abc" in
// one file) is expected invalid, while attgA006 and attgA009 put the duplicate
// across a redefine boundary and are expected invalid on the same grounds only
// because the redefined group is pulled into this document.
func (p *parser) checkIDs(root *xdm.Node) {
	seen := map[string]bool{}
	var walk func(*xdm.Node)
	walk = func(n *xdm.Node) {
		if n.Name.URI == NSSchema {
			if a := n.Attr("", "id"); a != nil {
				switch {
				case !isNCName(a.Value):
					p.errs = append(p.errs, errorAt(n, "",
						"id %q is not a valid xs:ID: an "+
							"xs:ID must be an NCName",
						a.Value))
				case seen[a.Value]:
					p.errs = append(p.errs, errorAt(n, "",
						"id %q appears more than once; an "+
							"xs:ID must be unique within "+
							"the document", a.Value))
				default:
					seen[a.Value] = true
				}
			}
		}
		for _, c := range n.ChildElements() {
			walk(c)
		}
	}
	walk(root)
}

// readTopLevel dispatches one child of <xs:schema>.
//
// Conditional inclusion is applied here rather than in the callers' loops,
// because there is more than one loop: the assembler has its own, and a filter
// in only one of them let a schema document carrying a 1.0 and a 1.1 spelling
// of the same global declaration be read twice — reporting each as a duplicate
// of the other, which is exactly what the feature exists to prevent.
func (p *parser) readTopLevel(el *xdm.Node) {
	if !includeElement(el, p.schema.Version) {
		return
	}
	if el.Name.URI != NSSchema {
		// Foreign elements at the top level are permitted only inside
		// <xs:annotation>; elsewhere they are a representation fault.
		p.errs = append(p.errs, errorAt(el, "src-schema.1",
			"unexpected element {%s}%s at the top level of a schema",
			el.Name.URI, el.Name.Local))
		return
	}

	// The readers below pick out the children they need by name and ignore
	// whatever else they find, which is what lets one reader serve several
	// element shapes — but it also means a document that breaks the schema
	// for schemas loads without complaint. Checking the subtree's shape
	// first turns a second <simpleContent>, or an <annotation> after the
	// content model, into the fault it is rather than a silently discarded
	// element. This is the one place every path into the readers passes
	// through: the assembler reaches the top level here too, for included
	// documents and for the replacements inside <redefine> and <override>.
	p.checkSourceModel(el)

	switch el.Name.Local {
	case "annotation":
		// Annotations carry documentation and application information.
		// Neither affects validation.

	case "element":
		if d := p.readElementDecl(el, ScopeGlobal); d != nil {
			p.declareElement(el, d)
		}
	case "attribute":
		if d := p.readAttributeDecl(el, ScopeGlobal); d != nil {
			p.declareAttribute(el, d)
		}
	case "simpleType":
		if t := p.readSimpleType(el); t != nil {
			p.declareType(el, t)
		}
	case "complexType":
		if t := p.readComplexType(el); t != nil {
			p.declareType(el, t)
		}
	case "group":
		if g := p.readModelGroupDef(el); g != nil {
			p.declareModelGroup(el, g)
		}
	case "attributeGroup":
		if g := p.readAttributeGroupDef(el); g != nil {
			p.declareAttributeGroup(el, g)
		}
	case "notation":
		if n := p.readNotation(el); n != nil {
			p.declareNotation(el, n)
		}

	case "defaultOpenContent":
		// XSD 1.1: an open content that applies to every complex type
		// in the document that does not declare its own.
		p.doc.defaultOpenContent = p.readOpenContent(el)
		p.doc.appliesToEmpty = p.boolAttr(el, "appliesToEmpty", false)

	case "override", "include", "import", "redefine":
		// Assembling several documents is the caller's concern; see the
		// note on ParseSchema. A single-document parse records nothing
		// for these, and a reference into the un-read document will be
		// reported as unresolvable, which is the honest outcome.

	default:
		p.errs = append(p.errs, errorAt(el, "src-schema.1",
			"unexpected element xs:%s at the top level of a schema", el.Name.Local))
	}
}

// declareType records a global type, rejecting a duplicate name.
//
// Two global components of the same kind may not share a name (§4.2's
// uniqueness rules). The check is here rather than at the end because reporting
// the second declaration's position is more useful than reporting that a
// conflict exists somewhere.
func (p *parser) declareType(el *xdm.Node, t Type) {
	name := t.TypeName()
	if name.Local == "" {
		p.errs = append(p.errs, errorAt(el, "src-element.1",
			"a top-level type must have a name"))
		return
	}
	if prev, ok := p.schema.Types[name]; ok {
		if st, isBuiltin := prev.(*SimpleType); isBuiltin && st.builtin {
			p.errs = append(p.errs, errorAt(el, "sch-props-correct.2",
				"%s redefines the built-in type xs:%s", name.Local, name.Local))
			return
		}
		p.errs = append(p.errs, errorAt(el, "sch-props-correct.2",
			"duplicate type definition %s", name.Local))
		return
	}
	p.schema.Types[name] = t
}

func (p *parser) declareElement(el *xdm.Node, d *ElementDecl) {
	if d.Name.Local == "" {
		p.errs = append(p.errs, errorAt(el, "src-element.1",
			"a top-level element declaration must have a name"))
		return
	}
	if _, ok := p.schema.Elements[d.Name]; ok {
		p.errs = append(p.errs, errorAt(el, "sch-props-correct.2",
			"duplicate element declaration %s", d.Name.Local))
		return
	}
	p.schema.Elements[d.Name] = d
}

func (p *parser) declareAttribute(el *xdm.Node, d *AttributeDecl) {
	if d.Name.Local == "" {
		p.errs = append(p.errs, errorAt(el, "src-attribute.3.1",
			"a top-level attribute declaration must have a name"))
		return
	}
	if prev, ok := p.schema.Attributes[d.Name]; ok {
		// The XML namespace's own attributes are supplied so that an
		// import without a schemaLocation resolves, but a schema may
		// also declare them itself — the schema document for that
		// namespace in Part 1 §F.1 does exactly that, and it is in the
		// suite. An explicit declaration replaces the supplied one
		// rather than colliding with it.
		if !prev.builtin {
			p.errs = append(p.errs, errorAt(el, "sch-props-correct.2",
				"duplicate attribute declaration %s", d.Name.Local))
			return
		}
	}
	p.schema.Attributes[d.Name] = d
}

func (p *parser) declareModelGroup(el *xdm.Node, g *ModelGroupDef) {
	if _, ok := p.schema.ModelGroups[g.Name]; ok {
		p.errs = append(p.errs, errorAt(el, "sch-props-correct.2",
			"duplicate group definition %s", g.Name.Local))
		return
	}
	p.schema.ModelGroups[g.Name] = g
}

func (p *parser) declareAttributeGroup(el *xdm.Node, g *AttributeGroupDef) {
	if _, ok := p.schema.AttributeGroups[g.Name]; ok {
		p.errs = append(p.errs, errorAt(el, "sch-props-correct.2",
			"duplicate attributeGroup definition %s", g.Name.Local))
		return
	}
	p.schema.AttributeGroups[g.Name] = g
}

func (p *parser) declareNotation(el *xdm.Node, n *NotationDecl) {
	if _, ok := p.schema.Notations[n.Name]; ok {
		p.errs = append(p.errs, errorAt(el, "sch-props-correct.2",
			"duplicate notation declaration %s", n.Name.Local))
		return
	}
	p.schema.Notations[n.Name] = n
}

// qnameFor builds the expanded name of a global declaration in this document.
func (p *parser) qnameFor(local string) xdm.QName {
	return xdm.QName{URI: p.doc.targetNS, Local: local}
}

// resolveQName expands a QName written in an attribute value, using the
// namespace bindings in scope at the element that carries it.
//
// An unprefixed name resolves to the default namespace, not to the target
// namespace. That distinction is a common source of confusion: a schema whose
// target namespace is bound to a prefix but not made the default must write
// type="tns:foo", never type="foo".
func (p *parser) resolveQName(el *xdm.Node, attr, value string) (xdm.QName, error) {
	value = strings.TrimSpace(value)
	prefix, local := "", value
	if i := strings.IndexByte(value, ':'); i >= 0 {
		prefix, local = value[:i], value[i+1:]
	}
	if local == "" {
		return xdm.QName{}, errorAt(el, "src-resolve",
			"%s=%q is not a valid QName", attr, value)
	}
	uri, ok := el.LookupPrefix(prefix)
	if !ok {
		if prefix == "" {
			// No default namespace is in scope: the name is in the
			// absent namespace.
			return p.chameleonQName(local), nil
		}
		return xdm.QName{}, errorAt(el, "src-resolve",
			"%s=%q uses undeclared prefix %q", attr, value, prefix)
	}
	// The prefix is deliberately dropped. xdm.QName carries it so that
	// serialisation can reproduce the source spelling, but it is a lexical
	// detail: xs:string and foo:string name the same component whenever both
	// prefixes are bound to the schema namespace. Leaving it in would make
	// the prefix part of every map key, so a type would be findable only
	// under the spelling its reference happened to use.
	if uri == "" {
		// A bound-but-empty default namespace is the same case as an
		// unbound one: the name is in no namespace.
		return p.chameleonQName(local), nil
	}
	return xdm.QName{URI: uri, Local: local}, nil
}

// chameleonQName places a name that resolved to no namespace.
//
// §4.2.1 converts a document with no target namespace to the namespace of the
// document that included it, and the conversion is of the whole document, not
// only its declarations: a reference written unprefixed named a component in
// the same document, and it has to go on naming it after the move. Leaving
// such a reference in the absent namespace pointed it at nothing, since the
// component it names has been converted. sunData's xsd024 is built to catch
// exactly this — a module whose components all refer to each other unprefixed.
func (p *parser) chameleonQName(local string) xdm.QName {
	if p.doc != nil && p.doc.chameleon {
		return xdm.QName{URI: p.doc.targetNS, Local: local}
	}
	return xdm.QName{Local: local}
}

// occursValue parses one xs:nonNegativeInteger occurrence bound.
//
// The type has no upper bound in the spec, and msData's particlesZ033_a writes
// minOccurs="79228162514244337593543950335" in a schema the suite expects to
// load. Rejecting that as "not a non-negative integer" is simply wrong: it is
// one. Values past what an int can hold are saturated to occursHuge rather than
// refused, since no instance document can ever supply that many children and
// any bound at or above the saturation point behaves identically during
// validation. Leading "+" and leading zeros are permitted by the lexical space
// of xs:nonNegativeInteger, so they are accepted here too.
func occursValue(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if v[0] == '+' {
		v = v[1:]
	}
	if v == "" {
		return 0, false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return occursHuge, true
	}
	return n, true
}

// occursHuge stands in for an occurrence bound too large to hold in an int.
// It is far beyond any attainable child count, so treating a larger bound as
// equal to it cannot change the outcome of validating a real document.
const occursHuge = int(^uint(0) >> 2)

// occurs reads minOccurs and maxOccurs from a particle-bearing element.
//
// Both default to 1. maxOccurs additionally accepts "unbounded".
//
// The attribute is looked up with Attr rather than AttrValue because the two
// cases AttrValue conflates — absent, and present but empty — have opposite
// answers here. An absent minOccurs means 1; minOccurs="" is a
// xs:nonNegativeInteger with no digits, which is a fault. Reading it through
// AttrValue silently applied the default to both, which is how wildB014
// (maxOccurs="") and wildB022 (minOccurs="") loaded clean.
func (p *parser) occurs(el *xdm.Node) (min, max int, err error) {
	min, max = 1, 1
	if a := el.Attr("", "minOccurs"); a != nil {
		n, ok := occursValue(a.Value)
		if !ok {
			return 0, 0, errorAt(el, "p-props-correct.1",
				"minOccurs=%q is not a non-negative integer", a.Value)
		}
		min = n
	}
	if a := el.Attr("", "maxOccurs"); a != nil {
		v := strings.TrimSpace(a.Value)
		if v == "unbounded" {
			max = Unbounded
		} else {
			n, ok := occursValue(v)
			if !ok {
				return 0, 0, errorAt(el, "p-props-correct.1",
					"maxOccurs=%q is not a non-negative integer or \"unbounded\"", a.Value)
			}
			max = n
		}
	}
	if max != Unbounded && min > max {
		return 0, 0, errorAt(el, "p-props-correct.2.1",
			"minOccurs %d is greater than maxOccurs %d", min, max)
	}
	return min, max, nil
}

// boolAttr reads an attribute whose type is xs:boolean.
//
// XSD spells booleans as "true"/"false" or "1"/"0"; anything else is a fault
// rather than a silent false, since "True" or "yes" in a schema is a mistake
// the author wants to hear about.
func (p *parser) boolAttr(el *xdm.Node, name string, def bool) bool {
	// Fetched as a node so that an absent attribute, which takes the
	// default, is distinguished from one written with an empty value, which
	// is not in the lexical space of xs:boolean and so is a fault.
	// elemB005 (abstract="") and elemK003 (nillable="") are exactly this.
	a := el.Attr("", name)
	if a == nil {
		return def
	}
	v := strings.TrimSpace(a.Value)
	switch v {
	case "true", "1":
		return true
	case "false", "0":
		return false
	}
	p.errs = append(p.errs, errorAt(el, "",
		"%s=%q is not a boolean", name, v))
	return def
}

// derivationSet reads a block, final, blockDefault or finalDefault attribute.
func (p *parser) derivationSet(el *xdm.Node, name string) (DerivationSet, error) {
	v := strings.TrimSpace(el.AttrValue(name))
	if v == "" {
		return 0, nil
	}
	if v == "#all" {
		return AllDerivations, nil
	}
	// Which tokens are legal depends on the attribute, and the schema for
	// schemas gives each its own type rather than one shared list.
	//
	// blockSet — block and blockDefault — admits substitution, because
	// blocking substitution is a thing an element can do. derivationSet —
	// final and finalDefault on an element — does not: "#all or (possibly
	// empty) subset of {extension, restriction}". A simple type's final is
	// different again, admitting list and union, since those are the ways a
	// simple type is derived.
	//
	// Accepting every token everywhere let elemF004 write
	// final="substitution" on an <element>, which reads as though it
	// prevented substitution and does not — that is what block says.
	// blockSet — which admits substitution — is the type of blockDefault on
	// <xs:schema> and of block on <xs:element>. block on a *type* is
	// xs:derivationSet, "#all or (possibly empty) subset of {extension,
	// restriction}": substitution is something an element does, not
	// something a type definition can prohibit (ctA016).
	blocking := name == "blockDefault" ||
		(name == "block" && el.Name.Local == "element")
	simple := el.Name.Local == "simpleType" ||
		(name == "finalDefault" && !blocking)

	var out DerivationSet
	for _, word := range strings.Fields(v) {
		switch word {
		case "extension":
			out = out.With(DerivationExtension)
		case "restriction":
			out = out.With(DerivationRestriction)
		case "list", "union":
			if !simple {
				return 0, errorAt(el, "",
					"%s=%q: %q is not permitted here", name, v, word)
			}
			if word == "list" {
				out = out.With(DerivationList)
			} else {
				out = out.With(DerivationUnion)
			}
		case "substitution":
			if !blocking {
				return 0, errorAt(el, "",
					"%s=%q: %q is only permitted in block", name, v, word)
			}
			out = out.With(DerivationSubstitution)
		default:
			return 0, errorAt(el, "",
				"%s=%q contains unknown derivation %q", name, v, word)
		}
	}
	return out, nil
}

// valueConstraint reads default and fixed from a declaration.
//
// The two are mutually exclusive: a declaration that supplies both is a
// representation fault, since fixed already implies the value.
func (p *parser) valueConstraint(el *xdm.Node) *ValueConstraint {
	def := el.Attr("", "default")
	fix := el.Attr("", "fixed")
	switch {
	case def != nil && fix != nil:
		p.errs = append(p.errs, errorAt(el, "src-element.1",
			"a declaration may not have both default and fixed"))
		return nil
	case fix != nil:
		return &ValueConstraint{Fixed: true, Lexical: fix.Value}
	case def != nil:
		return &ValueConstraint{Lexical: def.Value}
	}
	return nil
}

// childElement returns the first child of el in the schema namespace with one
// of the given names, skipping annotations.
func (p *parser) childElement(el *xdm.Node, names ...string) *xdm.Node {
	for _, c := range el.ChildElements() {
		if c.Name.URI != NSSchema {
			continue
		}
		if !includeElement(c, p.schema.Version) {
			// Excluded by conditional inclusion: the element is not
			// there as far as this processor is concerned.
			continue
		}
		for _, n := range names {
			if c.Name.Local == n {
				return c
			}
		}
	}
	return nil
}

// contentChildren returns el's children in the schema namespace, with
// annotations removed.
//
// Annotations may appear as the first child of nearly every schema element, so
// almost every caller would otherwise have to skip them by hand.
func (p *parser) contentChildren(el *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	for _, c := range el.ChildElements() {
		if c.Name.URI == NSSchema && c.Name.Local == "annotation" {
			continue
		}
		// XSD 1.1 conditional inclusion (§4.2.1): an element the
		// versioning attributes exclude is treated as though it were
		// not written, so it has to vanish before anything reads it.
		// A reader that noticed it and skipped it would still report
		// its errors, which is the opposite of what the feature is for.
		if !includeElement(c, p.schema.Version) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// formDefault reads elementFormDefault or attributeFormDefault, whose value is
// the two-token enumeration xs:formChoice.
//
// The value used to be compared against "qualified" and anything else taken as
// unqualified, so a misspelling silently reversed the meaning of every local
// declaration in the document: elemH004 writes "Qualified" with a capital Q
// and elemH005 "Unqualified", and both loaded as though they said the
// opposite of what their author intended. An empty value (elemH003) and a
// two-token list (elemH006) are equally not members of the enumeration.
func (p *parser) formDefault(root *xdm.Node, name string) bool {
	a := root.Attr("", name)
	if a == nil {
		return false
	}
	switch a.Value {
	case "qualified":
		return true
	case "unqualified":
		return false
	}
	p.errs = append(p.errs, errorAt(root, "",
		"%s=%q is not one of qualified or unqualified", name, a.Value))
	return false
}
