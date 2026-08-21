package xsd

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// ValidationError reports one reason a document is not valid.
//
// The spec gives each validation rule an error code — cvc-complex-type,
// cvc-datatype-valid and so on — and carrying it lets a caller distinguish the
// kinds of failure without matching on message text.
type ValidationError struct {
	// Code is the spec's error code, such as "cvc-complex-type.2.4".
	Code string
	// Message describes the failure.
	Message string
	// Path is the location in the instance, as an element path.
	Path string
	// Line and Column locate it when the node carried a position.
	Line, Column int
}

// Error implements error.
func (e *ValidationError) Error() string {
	var b strings.Builder
	if e.Line > 0 {
		fmt.Fprintf(&b, "%d:%d: ", e.Line, e.Column)
	}
	if e.Path != "" {
		b.WriteString(e.Path)
		b.WriteString(": ")
	}
	if e.Code != "" {
		b.WriteString(e.Code)
		b.WriteString(": ")
	}
	b.WriteString(e.Message)
	return b.String()
}

// ValidationErrors is the set of failures found in one document.
type ValidationErrors struct {
	Errors []*ValidationError
}

// Error implements error.
func (e *ValidationErrors) Error() string {
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d validation errors:", len(e.Errors))
	for _, err := range e.Errors {
		b.WriteString("\n  ")
		b.WriteString(err.Error())
	}
	return b.String()
}

// ValidateOptions configure a validation run.
type ValidateOptions struct {
	// MaxErrors stops validation once this many failures are found. Zero
	// means DefaultMaxErrors. A document that is wrong in every element
	// would otherwise produce an error for each, which helps nobody and
	// costs memory proportional to the document.
	MaxErrors int

	// Annotate writes the type of each validated node into its
	// TypeAnnotation, producing the part of the PSVI that the XPath and
	// XSLT layers consume. It is off by default because it mutates the
	// tree the caller passed in.
	Annotate bool
}

// DefaultMaxErrors bounds a run that does not set MaxErrors.
const DefaultMaxErrors = 100

// Validate checks a document against the schema.
//
// It returns nil when the document is valid. The error, when there is one, is a
// *ValidationErrors holding every failure found up to the limit.
func (s *Schema) Validate(root *xdm.Node, opts ValidateOptions) error {
	if opts.MaxErrors == 0 {
		opts.MaxErrors = DefaultMaxErrors
	}
	v := &validator{schema: s, opts: opts, ids: map[string]int{}}

	el := root
	if el.Kind == xdm.KindDocument {
		els := el.ChildElements()
		if len(els) == 0 {
			return &ValidationErrors{Errors: []*ValidationError{{
				Code: "cvc-elt.1", Message: "the document has no element",
			}}}
		}
		el = els[0]
	}

	decl, ok := s.Elements[xdm.QName{URI: el.Name.URI, Local: el.Name.Local}]
	if !ok {
		v.fail(el, "cvc-elt.1",
			"no element declaration for {%s}%s", el.Name.URI, el.Name.Local)
		return v.result()
	}
	v.validateElement(el, decl)
	v.checkIDs()
	return v.result()
}

// validator carries the state of one validation run.
type validator struct {
	schema *Schema
	opts   ValidateOptions
	errs   []*ValidationError

	// path is the element path to the node being validated, for messages.
	path []string

	// ids records every xs:ID value seen and every xs:IDREF, so that
	// Validation Root Valid (ID/IDREF) can be checked once at the end. A
	// count of zero means the value was referenced but never defined.
	ids map[string]int
	// idrefs are the referenced values, checked against ids at the end.
	idrefs []idref

	// idOwners maps each ID value to the element that defined it, so that
	// the same value on two attributes of one element counts once. XSD 1.1
	// permits an element to carry several ID attributes.
	idOwners map[string]*xdm.Node

	// skipped holds the elements matched by a processContents="skip"
	// wildcard. They and their descendants are outside the assessment, so
	// an identity constraint's selector must not reach into them.
	skipped map[*xdm.Node]bool

	// keyValues records the primitive of each node whose value an identity
	// constraint may have to compare by value rather than by spelling.
	keyValues map[*xdm.Node]keyValue

	// childTypes records the type each child name was matched with, per
	// parent, for the dynamic Element Declarations Consistent check.
	childTypes map[edtKey]Type

	// openContents caches the per-type copy of an open content whose
	// wildcard uses ##definedSibling, since the component itself may be
	// shared across types and must not be written to.
	openContents map[*ComplexType]*OpenContent

	// inherited holds the XSD 1.1 inheritable attributes in scope, innermost
	// last. Conditional type assignment on a descendant sees them, which is
	// how an ancestor's xml:lang can choose a nested element's type.
	inherited []*xdm.Node

	// stopped records that the error limit was reached.
	stopped bool
}

// elementDefined answers ##defined for an element wildcard: whether the schema
// has a global element declaration of this name.
//
// It is a method rather than a closure literal at each call site so that the
// two kinds — element and attribute — cannot be confused. They are separate
// symbol spaces, and a wildcard that consulted the wrong one would exclude
// names it should admit.
func (v *validator) elementDefined(name xdm.QName) bool {
	_, ok := v.schema.Elements[name]
	return ok
}

// attributeDefined answers ##defined for an attribute wildcard.
func (v *validator) attributeDefined(name xdm.QName) bool {
	_, ok := v.schema.Attributes[name]
	return ok
}

// edtKey identifies one element name within one content model, which is the
// scope Element Declarations Consistent applies to.
type edtKey struct {
	parent *xdm.Node
	name   xdm.QName
}

// keyValue is a node's schema-normalized value and the primitive it belongs
// to, kept so that an identity constraint can canonicalise it.
type keyValue struct {
	normalized string
	primitive  string
}

type idref struct {
	value string
	node  *xdm.Node
}

func (v *validator) result() error {
	if len(v.errs) == 0 {
		return nil
	}
	return &ValidationErrors{Errors: v.errs}
}

// fail records a validation failure.
func (v *validator) fail(n *xdm.Node, code, format string, args ...any) {
	if len(v.errs) >= v.opts.MaxErrors {
		v.stopped = true
		return
	}
	e := &ValidationError{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
		Path:    "/" + strings.Join(v.path, "/"),
	}
	if n != nil {
		if line, col, ok := n.Position(); ok {
			e.Line, e.Column = line, col
		}
	}
	v.errs = append(v.errs, e)
}

// validateElement checks one element against a declaration.
func (v *validator) validateElement(el *xdm.Node, decl *ElementDecl) icTables {
	if v.stopped {
		return nil
	}
	v.path = append(v.path, el.Name.Local)
	defer func() { v.path = v.path[:len(v.path)-1] }()

	// An abstract declaration cannot itself validate an element; only a
	// member of its substitution group can.
	if decl.Abstract {
		v.fail(el, "cvc-elt.2",
			"element declaration {%s}%s is abstract",
			decl.Name.URI, decl.Name.Local)
		return nil
	}

	typ := decl.Type

	// XSD 1.1 conditional type assignment runs before xsi:type, because an
	// alternative selects the *declared* type that xsi:type must then be
	// derived from.
	if v.schema.Version == Version11 {
		typ = v.selectAlternativeType(el, decl)
		// The element's own inheritable attributes join the scope for
		// its descendants, and leave it again on the way out.
		if n := v.pushInherited(el, decl); n > 0 {
			defer func() { v.inherited = v.inherited[:len(v.inherited)-n] }()
		}
	}

	// xsi:type overrides the declared type, subject to the blocking rules.
	if xsiType := el.Attr(NSInstance, "type"); xsiType != nil {
		t, err := v.resolveXSIType(el, xsiType.Value)
		if err != nil {
			v.fail(el, "cvc-elt.4.2", "%v", err)
			return nil
		}
		if !v.derivedFrom(t, decl.Type) {
			v.fail(el, "cvc-elt.4.3",
				"xsi:type %q is not derived from the declared type",
				xsiType.Value)
			return nil
		}
		typ = t
	}

	// xsi:nil permits an empty element where the declaration allows it.
	if nilAttr := el.Attr(NSInstance, "nil"); nilAttr != nil {
		val := strings.TrimSpace(nilAttr.Value)
		if !decl.Nillable {
			v.fail(el, "cvc-elt.3.1",
				"xsi:nil is present but the declaration is not nillable")
			return nil
		}
		if val == "true" || val == "1" {
			// A nilled element must be empty and must not have a
			// fixed value constraint.
			if len(el.ChildElements()) > 0 || strings.TrimSpace(el.StringValue()) != "" {
				v.fail(el, "cvc-elt.3.2.1",
					"an element with xsi:nil=\"true\" must be empty")
			}
			if decl.Constraint != nil && decl.Constraint.Fixed {
				v.fail(el, "cvc-elt.3.2.2",
					"an element with xsi:nil=\"true\" may not have a "+
						"fixed value constraint")
			}
			return nil
		}
	}

	if typ == nil {
		// The type never resolved; the schema parse reported it.
		return nil
	}
	childTables := v.validateAgainstType(el, typ, decl)

	// The identity constraints run after the content, because a key is
	// defined over the subtree and the subtree has to have been walked for
	// its tables to exist.
	return v.checkIdentityConstraints(el, decl, childTables)
}

// resolveXSIType expands an xsi:type value against the namespaces in scope.
func (v *validator) resolveXSIType(el *xdm.Node, value string) (Type, error) {
	value = strings.TrimSpace(value)
	prefix, local := "", value
	if i := strings.IndexByte(value, ':'); i >= 0 {
		prefix, local = value[:i], value[i+1:]
	}
	uri, ok := el.LookupPrefix(prefix)
	if !ok && prefix != "" {
		return nil, fmt.Errorf("xsi:type %q uses undeclared prefix %q", value, prefix)
	}
	t, ok := v.schema.Types[xdm.QName{URI: uri, Local: local}]
	if !ok {
		return nil, fmt.Errorf("xsi:type %q names no type definition", value)
	}
	if ct, ok := t.(*ComplexType); ok && ct.Abstract {
		return nil, fmt.Errorf("xsi:type %q names an abstract type", value)
	}
	return t, nil
}

// derivedFrom reports whether t is or derives from want.
//
// The walk stops on self as well as on nil, because xs:anyType is its own base
// type definition and a chain that tested only for nil would not terminate.
func (v *validator) derivedFrom(t, want Type) bool {
	if want == nil {
		return true
	}
	// A member of a union is validly derived from it (§3.14.6 clause 2.2.3),
	// which is what lets xsi:type name one. The base chain alone does not
	// reach it: a member's base is whatever it restricts, not the union.
	if u, ok := want.(*SimpleType); ok && u.Variety == VarietyUnion {
		for _, m := range u.MemberTypes {
			if m != nil && v.derivedFrom(t, m) {
				return true
			}
		}
	}

	seen := 0
	for cur := t; cur != nil; {
		if cur == want {
			return true
		}
		// Comparing by name as well catches the case where a built-in
		// was reached through two different schema loads.
		if n := cur.TypeName(); n.Local != "" && n == want.TypeName() {
			return true
		}
		base := cur.BaseType()
		if base == cur || base == nil {
			return false
		}
		cur = base
		// A malformed schema can build a cycle that is not a self-loop.
		if seen++; seen > 256 {
			return false
		}
	}
	return false
}

// validateAgainstType dispatches on the kind of type.
func (v *validator) validateAgainstType(el *xdm.Node, typ Type, decl *ElementDecl) []icTables {
	var tables []icTables
	switch t := typ.(type) {
	case *SimpleType:
		// An element with a simple type may have no element children and
		// no attributes other than the four xsi: ones.
		if kids := el.ChildElements(); len(kids) > 0 {
			v.fail(el, "cvc-type.3.1.2",
				"an element with a simple type may not have element children")
		}
		v.checkNoForeignAttributes(el, nil, nil)
		v.validateSimpleContent(el, effectiveValue(el, decl), t, decl)

	case *ComplexType:
		tables = v.validateComplexType(el, t, decl)
	}

	if v.opts.Annotate {
		if n := typ.TypeName(); n.Local != "" {
			el.TypeAnnotation = n.Local
		}
	}
	return tables
}

// validateComplexType checks an element against a complex type.
func (v *validator) validateComplexType(el *xdm.Node, t *ComplexType, decl *ElementDecl) []icTables {
	if t.Abstract {
		v.fail(el, "cvc-type.2",
			"type {%s}%s is abstract and cannot validate an element directly",
			t.Name.URI, t.Name.Local)
		return nil
	}

	v.validateAttributes(el, t)

	// XSD 1.1 assertions are checked after the content, because an
	// assertion is a co-constraint over content that has to exist first.
	if v.schema.Version == Version11 {
		defer v.checkAssertions(el, t)
	}

	switch t.Content {
	case ContentEmpty:
		// An empty type with open content is not empty: XSD 1.1's
		// appliesToEmpty="true" exists precisely to open one, and
		// refusing children before consulting the wildcard makes the
		// attribute do nothing.
		if oc := v.openContentFor(t); oc != nil {
			return v.matchOpenOnly(el, el.ChildElements(), oc)
		}
		if len(el.ChildElements()) > 0 {
			v.fail(el, "cvc-complex-type.2.1",
				"element must be empty but has element children")
		}
		if s := strings.TrimSpace(el.StringValue()); s != "" {
			v.fail(el, "cvc-complex-type.2.1",
				"element must be empty but has character content %q",
				truncate(s))
		}

	case ContentSimple:
		if len(el.ChildElements()) > 0 {
			v.fail(el, "cvc-complex-type.2.2",
				"element has simple content but has element children")
		}
		if t.SimpleContent != nil {
			v.validateSimpleContent(el, effectiveValue(el, decl), t.SimpleContent, decl)
		}

	case ContentElementOnly:
		// Character data other than whitespace is not permitted. The
		// whitespace exception is what lets a schema-valid document be
		// indented.
		if s := nonSpaceText(el); s != "" {
			v.fail(el, "cvc-complex-type.2.3",
				"element-only content may not contain character data %q",
				truncate(s))
		}
		return v.validateChildren(el, t)

	case ContentMixed:
		return v.validateChildren(el, t)
	}
	return nil
}

// matchOpenOnly validates the children of a type whose only content model is
// its open content wildcard.
//
// An empty type opened by appliesToEmpty="true" has no particle at all, so
// there is no automaton to walk: every child is either admitted by the wildcard
// or refused. Mode does not enter into it — with nothing in the model, every
// position is both after it and interleaved with it.
func (v *validator) matchOpenOnly(el *xdm.Node, kids []*xdm.Node, oc *OpenContent) []icTables {
	var tables []icTables
	for _, kid := range kids {
		name := xdm.QName{URI: kid.Name.URI, Local: kid.Name.Local}
		if !oc.Wildcard.AllowsName(name, v.elementDefined) {
			v.fail(kid, "cvc-complex-type.2.4.a",
				"element {%s}%s is not permitted by the open content wildcard",
				name.URI, name.Local)
			continue
		}
		if tbl := v.validateChild(kid, &position{term: oc.Wildcard}); tbl != nil {
			tables = append(tables, tbl)
		}
	}
	return tables
}

// nonSpaceText returns the first non-whitespace text directly inside el.
func nonSpaceText(el *xdm.Node) string {
	for _, c := range el.Children {
		if c.Kind != xdm.KindText {
			continue
		}
		if s := strings.Trim(c.Value, " \t\n\r"); s != "" {
			return s
		}
	}
	return ""
}

func truncate(s string) string {
	const max = 40
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// validateChildren matches an element's children against the content model.
func (v *validator) validateChildren(el *xdm.Node, t *ComplexType) []icTables {
	m, err := v.modelFor(t)
	if err != nil {
		v.fail(el, "", "compiling the content model: %v", err)
		return nil
	}
	kids := el.ChildElements()

	if isAllGroup(t.Particle) {
		return v.matchAll(el, kids, t.Particle.Term.(*ModelGroup), t)
	}
	return v.matchSequence(el, kids, m, t)
}

// noteChildType applies the XSD 1.1 dynamic Element Declarations Consistent
// check (cvc-complex-type.2.4.k).
//
// Element Declarations Consistent forbids two element declarations with the
// same name and different types in one content model. A wildcard can only break
// it at validation time, because the schema cannot know which global
// declaration a lax wildcard will pick up — so the rule has a dynamic half, and
// this is it: two <e> in one content model, one matched by a local declaration
// of type xs:date and the other by the global declaration of type xs:time, is
// the inconsistency the static rule exists to prevent.
//
// The type is taken from the position that actually matched, which is the whole
// point: a scan of the model's positions finds the local declaration for both
// children and sees no conflict. A 1.0 processor accepts this, so the check is
// version-gated — the suite's wild061 is annotated "valid in 1.0, invalid in
// 1.1".
func (v *validator) noteChildType(kid *xdm.Node, name xdm.QName, p *position) {
	if v.schema.Version != Version11 {
		return
	}
	var got Type
	switch term := p.term.(type) {
	case *ElementDecl:
		d := p.resolveDecl(name)
		if d == nil {
			return
		}
		got = d.Type
	case *Wildcard:
		if term.ProcessContents == ProcessSkip {
			// Skipped content is not assessed, so it asserts no
			// type and cannot conflict with one.
			return
		}
		d, ok := v.schema.Elements[name]
		if !ok {
			return
		}
		got = d.Type
	default:
		return
	}
	if got == nil {
		return
	}

	// The scope is one content model, which is one parent element.
	parent := kid.Parent
	if parent == nil {
		return
	}
	if v.childTypes == nil {
		v.childTypes = map[edtKey]Type{}
	}
	k := edtKey{parent: parent, name: name}
	prev, seen := v.childTypes[k]
	if !seen {
		v.childTypes[k] = got
		return
	}
	if edtConsistent(v, prev, got) {
		return
	}
	v.fail(kid, "cvc-complex-type.2.4.k",
		"element {%s}%s is matched with two different types in one "+
			"content model", name.URI, name.Local)
}

// edtConsistent reports whether two types one name was matched with agree.
//
// Identity is too strict: the rule is that the declarations are consistent, and
// a type validly derived from the other is consistent with it. A global
// <e type="xs:positiveInteger"/> reached through a wildcard does not conflict
// with a local <e type="xs:integer"/>, because everything the global admits the
// local admits too. Union membership counts for the same reason — a local type
// that is a union of xs:date and xs:time is consistent with a global xs:date.
//
// The test runs both ways, since which of the two the wildcard supplied is an
// accident of document order rather than something the rule distinguishes.
func edtConsistent(v *validator, a, b Type) bool {
	return a == b || v.derivedFrom(a, b) || v.derivedFrom(b, a)
}

// isAllGroup reports whether a particle is an xs:all at the top of a content
// model, which is the only place XSD 1.0 permits one.
func isAllGroup(p *Particle) bool {
	if p == nil {
		return false
	}
	g, ok := p.Term.(*ModelGroup)
	return ok && g.Compositor == CompositorAll
}

// modelFor returns the compiled content model for a type, building it once.
//
// The cache lives on the Schema rather than in a package-level map. A global
// keyed by *ComplexType is unsound: Go reuses freed addresses, so a type from a
// discarded schema can collide with a live one and hand back the wrong
// automaton. That is exactly the shape of bug that only appears once two
// schemas exist in one process, which makes it a poor thing to leave for a
// user to find.
func (v *validator) modelFor(t *ComplexType) (*contentModel, error) {
	if m, ok := v.schema.models.Load(t); ok {
		if e, isErr := m.(error); isErr {
			return nil, e
		}
		return m.(*contentModel), nil
	}
	m, err := compileContentModel(t.Particle)
	if err != nil {
		v.schema.models.Store(t, err)
		return nil, err
	}
	v.schema.models.Store(t, m)
	return m, nil
}

// matchSequence walks the automaton over an element's children.
func (v *validator) matchSequence(el *xdm.Node, kids []*xdm.Node, m *contentModel, t *ComplexType) []icTables {
	if len(m.positions) == 0 {
		// An empty content model still admits whatever open content
		// permits, which is the case that makes a type declaring only
		// <xs:openContent> useful at all.
		var tables []icTables
		oc := v.openContentFor(t)
		for _, kid := range kids {
			if oc != nil && oc.Wildcard.AllowsName(kid.Name, v.elementDefined) {
				if tbl := v.validateChild(kid, &position{term: oc.Wildcard}); tbl != nil {
					tables = append(tables, tbl)
				}
				continue
			}
			v.fail(kid, "cvc-complex-type.2.4.d",
				"element {%s}%s is not permitted here: the content model "+
					"is empty", kid.Name.URI, kid.Name.Local)
			return tables
		}
		return tables
	}

	var tables []icTables

	// counts tracks the repetitions of each counter scope.
	counts := make([]int, len(m.counters))
	current := m.first
	var prev *position
	prevIdx := -1

	// inSuffix latches once a suffix-mode wildcard has matched. From then
	// on the content model is over: an element it names appearing after the
	// suffix has begun is not a suffix at all, and admitting it would make
	// suffix mean interleave with extra steps.
	inSuffix := false

	for _, kid := range kids {
		name := xdm.QName{URI: kid.Name.URI, Local: kid.Name.Local}
		next := -1
		for _, idx := range current {
			p := m.positions[idx]
			if inSuffix {
				break
			}
			if !p.matches(name, v.elementDefined) {
				continue
			}
			if !counterAllows(m, counts, prevIdx, idx) {
				continue
			}
			next = idx
			break
		}
		if next < 0 {
			// XSD 1.1 open content: an element the model does not
			// name may still be permitted by the type's wildcard.
			// In interleave mode it may appear anywhere; in suffix
			// mode only once the model has been satisfied, which
			// prevLast records.
			// Suffix mode admits the wildcard only once the content
			// model has been satisfied: either nothing has matched
			// and the model accepts the empty sequence, or the last
			// position reached is one the model may end at. Letting
			// it match at the start would make suffix mean
			// interleave.
			satisfied := prevIdx < 0 && m.nullable ||
				prevIdx >= 0 && contains(m.last, prevIdx)
			if oc := v.openContentFor(t); oc != nil && oc.Wildcard.AllowsName(name, v.elementDefined) &&
				(oc.Mode == OpenInterleave || satisfied) {
				if oc.Mode == OpenSuffix {
					inSuffix = true
				}
				if tbl := v.validateChild(kid, &position{term: oc.Wildcard}); tbl != nil {
					tables = append(tables, tbl)
				}
				continue
			}
			v.fail(kid, "cvc-complex-type.2.4.a",
				"element {%s}%s is not permitted here%s",
				kid.Name.URI, kid.Name.Local, expected(m, current))
			return tables
		}

		p := m.positions[next]
		advanceCounters(m, counts, prevIdx, next)
		if t := v.validateChild(kid, p); t != nil {
			tables = append(tables, t)
		}

		prev, prevIdx = p, next
		current = m.follow[next]
	}
	_ = prev

	// The sequence must be able to end here: either nothing was required,
	// or the last position reached is a valid ending point and every
	// counter has met its minimum.
	if prevIdx < 0 {
		if !m.nullable {
			v.fail(el, "cvc-complex-type.2.4.b",
				"element content is incomplete%s", expected(m, m.first))
		}
		return tables
	}
	if !contains(m.last, prevIdx) || !countersSatisfied(m, counts, prevIdx) {
		v.fail(el, "cvc-complex-type.2.4.b",
			"element content is incomplete%s", expected(m, m.follow[prevIdx]))
	}
	return tables
}

// counterAllows reports whether taking a transition to a position is permitted
// by the repetition bounds.
func counterAllows(m *contentModel, counts []int, from, to int) bool {
	p := m.positions[to]
	for _, c := range p.counters {
		if from < 0 || !sharesScope(m.positions[from], c) {
			// Entering the scope for the first time; nothing to bound.
			continue
		}
		// The bound applies to a *restart*, not to every transition
		// inside the scope. Consulting the count on a continuation —
		// x1 to x2 within one repetition of a group — refused a legal
		// step the moment the count reached its maximum, which is the
		// same confusion the restart test exists to resolve.
		if !isScopeRestart(m, c, from, to) {
			continue
		}
		if m.counters[c].max != Unbounded && counts[c] >= m.counters[c].max {
			return false
		}
	}
	return true
}

func sharesScope(p *position, c int) bool {
	for _, x := range p.counters {
		if x == c {
			return true
		}
	}
	return false
}

// advanceCounters updates the repetition counts for a transition.
func advanceCounters(m *contentModel, counts []int, from, to int) {
	p := m.positions[to]
	for _, c := range p.counters {
		if from < 0 || !sharesScope(m.positions[from], c) {
			// Entering the scope: this is the first repetition.
			counts[c] = 1
			continue
		}
		if isScopeRestart(m, c, from, to) {
			counts[c]++
		}
	}
}

// isScopeRestart reports whether a transition begins another repetition of a
// scope rather than continuing the current one.
//
// A repetition restarts when the transition lands on a position that can *begin*
// the scope, from one that can *end* it — which is exactly the edge the compiler
// added to make the scope repeatable.
//
// The first version compared position indices, on the reasoning that a restart
// goes backwards. That holds only while positions are numbered in the order
// they appear, and a group reference breaks it: a choice of two groups numbers
// the second group's positions after the first's, so moving from the first
// group's end into the second group's start is a restart that runs *forwards*.
// A repeated choice over two groups was then read as one long repetition and
// rejected once it passed maxOccurs.
func isScopeRestart(m *contentModel, scope, from, to int) bool {
	return scopeCanEndAt(m, scope, from) && scopeCanBeginAt(m, scope, to)
}

// scopeCanBeginAt reports whether a position can start a repetition of a scope.
func scopeCanBeginAt(m *contentModel, scope, at int) bool {
	return m.scopeFirst[scope][at]
}

// scopeCanEndAt reports whether a position can finish a repetition of a scope.
func scopeCanEndAt(m *contentModel, scope, at int) bool {
	return m.scopeLast[scope][at]
}

// countersSatisfied reports whether every counter containing a position has met
// its minimum.
func countersSatisfied(m *contentModel, counts []int, at int) bool {
	for _, c := range m.positions[at].counters {
		if counts[c] < m.counters[c].min {
			return false
		}
	}
	// Every counter that was entered has to have met its minimum, not only
	// those enclosing the final position. In <a minOccurs="5"/><b
	// minOccurs="0"/> the last position is b, which is in none of a's
	// scopes — so checking only the final position's counters never looked
	// at a's minimum at all, and four <a/> passed.
	//
	// A counter still at zero is one whose particle was never entered, and
	// that is not a shortfall: an optional repetition that did not occur is
	// satisfied by the surrounding model accepting its absence, which the
	// automaton has already decided by reaching an accepting position.
	for c, n := range counts {
		if n > 0 && n < m.counters[c].min {
			return false
		}
	}
	return true
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// expected renders what could have appeared, for a diagnostic.
func expected(m *contentModel, positions []int) string {
	if len(positions) == 0 {
		return ""
	}
	var names []string
	seen := map[string]bool{}
	for _, idx := range positions {
		var s string
		switch t := m.positions[idx].term.(type) {
		case *ElementDecl:
			s = t.Name.Local
		case *Wildcard:
			s = "any element"
		}
		if s != "" && !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) > 6 {
		names = append(names[:6], "...")
	}
	return "; expected one of " + strings.Join(names, ", ")
}

// matchAll checks an xs:all group.
//
// All Group Limited confines an all group to the whole content model, with
// element particles occurring at most once, which is what makes a seen-set
// check sound rather than needing every interleaving.
func (v *validator) matchAll(el *xdm.Node, kids []*xdm.Node, g *ModelGroup, t *ComplexType) []icTables {
	// counts rather than a seen set: XSD 1.0 confines every particle in an
	// all group to maxOccurs 1, but 1.1 lifts that, so what is checked is
	// the particle's own bound.
	particles := flattenAll(g)
	counts := make([]int, len(particles))
	var tables []icTables

	// inSuffix latches once a suffix-mode wildcard has matched, exactly as
	// in matchSequence: an all group's members may come in any order, but a
	// suffix still has to be at the end.
	inSuffix := false

	for _, kid := range kids {
		name := xdm.QName{URI: kid.Name.URI, Local: kid.Name.Local}
		found := false
		// A particle whose bound is used up does not fail the element:
		// another particle may still admit it. XSD 1.1 permits a
		// wildcard alongside named particles in an all group, and it is
		// there precisely to take what the named ones cannot — so
		// stopping at the first particle that matches by name reports a
		// bound violation for content the group accepts.
		exhausted := -1
		for i, p := range particles {
			if inSuffix {
				break
			}
			pos := &position{term: p.Term, particle: p}
			if !pos.matches(name, v.elementDefined) {
				continue
			}
			if p.MaxOccurs != Unbounded && counts[i] >= p.MaxOccurs {
				if exhausted < 0 {
					exhausted = i
				}
				continue
			}
			counts[i]++
			found = true
			if tbl := v.validateChild(kid, pos); tbl != nil {
				tables = append(tables, tbl)
			}
			break
		}
		if !found && exhausted >= 0 {
			// Every particle that names this element is used up, and
			// nothing else admits it: that is the bound violation.
			p := particles[exhausted]
			v.fail(kid, "cvc-complex-type.2.4.j",
				"element {%s}%s appears more than %d times in an all group",
				name.URI, name.Local, p.MaxOccurs)
			continue
		}
		if found {
			continue
		}
		// XSD 1.1 open content applies to an all group too: the
		// wildcard admits what the group does not name.
		if oc := v.openContentFor(t); oc != nil && oc.Wildcard.AllowsName(name, v.elementDefined) {
			if oc.Mode == OpenSuffix {
				inSuffix = true
			}
			if tbl := v.validateChild(kid, &position{term: oc.Wildcard}); tbl != nil {
				tables = append(tables, tbl)
			}
			continue
		}
		v.fail(kid, "cvc-complex-type.2.4.a",
			"element {%s}%s is not permitted in this all group",
			name.URI, name.Local)
	}

	for i, p := range particles {
		if counts[i] >= p.MinOccurs {
			continue
		}
		switch term := p.Term.(type) {
		case *ElementDecl:
			v.fail(el, "cvc-complex-type.2.4.b",
				"required element %s is missing from an all group", term.Name.Local)
		case *Wildcard:
			// A wildcard particle in an all group carries its own
			// minOccurs in XSD 1.1, and falling short of it is the
			// same failure as a missing element — there is just no
			// name to report.
			v.fail(el, "cvc-complex-type.2.4.b",
				"an all group requires %d more element(s) matching a wildcard",
				p.MinOccurs-counts[i])
		}
	}
	return tables
}

// validateChild validates one matched child against the position that matched
// it.
func (v *validator) validateChild(kid *xdm.Node, p *position) icTables {
	name := xdm.QName{URI: kid.Name.URI, Local: kid.Name.Local}
	v.noteChildType(kid, name, p)

	if w, ok := p.term.(*Wildcard); ok {
		switch w.ProcessContents {
		case ProcessSkip:
			// Nothing is checked, by definition. The element is
			// recorded so that identity constraints skip it too:
			// a selector picks nodes out of the PSVI, and skipped
			// content was never assessed, so it contributes no
			// nodes to select. Without this a key sees ids in
			// content the schema said not to look at, and reports
			// a duplicate or a missing field for an element that
			// was never validated at all.
			if v.skipped == nil {
				v.skipped = map[*xdm.Node]bool{}
			}
			v.skipped[kid] = true
			return nil
		case ProcessLax:
			if d, ok := v.schema.Elements[name]; ok {
				return v.validateElement(kid, d)
			}
			return nil
		case ProcessStrict:
			d, ok := v.schema.Elements[name]
			if !ok {
				v.fail(kid, "cvc-complex-type.2.4.c",
					"no declaration for {%s}%s, matched by a strict wildcard",
					name.URI, name.Local)
				return nil
			}
			return v.validateElement(kid, d)
		}
	}

	if d := p.resolveDecl(name); d != nil {
		return v.validateElement(kid, d)
	}
	return nil
}

// openContentFor returns the open content in force for a type, or nil.
//
// It is only consulted under XSD 1.1: a 1.0 schema has no open content, and a
// 1.0 validator honouring one would accept documents the version it claims
// rejects.
func (v *validator) openContentFor(t *ComplexType) *OpenContent {
	if v.schema.Version != Version11 || t == nil || t.OpenContent == nil {
		return nil
	}
	if t.OpenContent.Wildcard == nil {
		return nil
	}
	if t.OpenContent.Wildcard.DisallowDefinedSibling {
		// The wildcard may be the document's shared defaultOpenContent,
		// so its sibling set cannot be written onto the component: a
		// Schema is safe to share between goroutines, and two of them
		// validating against different types would race on it. The
		// resolved copy is returned instead.
		return v.openContentWithSiblings(t)
	}
	return t.OpenContent
}

// bindOpenSiblings resolves ##definedSibling on an open content wildcard.
//
// The wildcard is not a particle in the content model, so compiling the model
// never reaches it — but its siblings are exactly that model's element names:
// open content is written to admit what the model does not already name, and
// ##definedSibling is how a schema says so without listing them.
//
// It is done here rather than at parse time because the same defaultOpenContent
// component is shared by every type in the document that does not declare its
// own, and each has different siblings. Binding one set onto the shared
// component would give every type the last one's.
func (v *validator) openContentWithSiblings(t *ComplexType) *OpenContent {
	if v.openContents == nil {
		v.openContents = map[*ComplexType]*OpenContent{}
	}
	if oc, done := v.openContents[t]; done {
		return oc
	}

	names := map[xdm.QName]bool{}
	if m, err := v.modelFor(t); err == nil {
		for _, pos := range m.positions {
			d, ok := pos.term.(*ElementDecl)
			if !ok {
				continue
			}
			names[d.Name] = true
			for _, sub := range d.substitutable {
				names[sub.Name] = true
			}
		}
	}

	w := *t.OpenContent.Wildcard
	w.siblingNames = names
	oc := &OpenContent{Mode: t.OpenContent.Mode, Wildcard: &w}
	v.openContents[t] = oc
	return oc
}

// pushInherited adds an element's inheritable attributes to the scope, and
// returns how many were added.
//
// The attributes are found from the type the element is being validated
// against, which is why this runs after conditional type assignment has chosen
// it: an alternative may select a type that declares different inheritable
// attributes from the default one.
func (v *validator) pushInherited(el *xdm.Node, decl *ElementDecl) int {
	ct, ok := decl.Type.(*ComplexType)
	if !ok {
		return 0
	}
	n := 0
	for _, use := range ct.AttributeUses {
		if !use.Inheritable || use.Decl == nil {
			continue
		}
		a := el.Attr(use.Decl.Name.URI, use.Decl.Name.Local)
		if a == nil {
			continue
		}
		v.inherited = append(v.inherited, a)
		n++
	}
	return n
}

// effectiveValue returns the value an element is validated against.
//
// An element with no content and a value constraint takes that value — §3.3.4
// clause 5.2. Without this an empty <price/> declared with default="0" was
// validated as the empty string, which is not a valid xs:decimal, so a document
// the schema explicitly provides for was rejected.
//
// A fixed constraint supplies its value the same way: fixed means "this value
// or nothing", not "this value must be written out".
func effectiveValue(el *xdm.Node, decl *ElementDecl) string {
	raw := el.StringValue()
	if raw != "" || decl == nil || decl.Constraint == nil {
		return raw
	}
	// Only a genuinely empty element defaults. One containing whitespace has
	// content, which whiteSpace normalisation may later collapse to nothing
	// — that is a different value from absent, and the spec treats it so.
	if len(el.Children) > 0 {
		return raw
	}
	return decl.Constraint.Lexical
}

// flattenAll returns an all group's member particles, seeing through nested all
// groups reached by a group reference.
//
// <xs:group ref="..."/> inside an <xs:all> is how XSD 1.1 lets a schema share
// an all group, and the reference leaves a model group where matchAll expects
// a term it can match a name against. The members of the referenced group are
// members of the enclosing one — an all group nested in an all group adds no
// ordering constraint of its own — so flattening is the meaning, not an
// approximation.
//
// A reference carrying its own occurrence bounds is not flattened: those bounds
// apply to the group as a unit, which is a different thing from applying them
// to each member, and the members' own bounds could not express it.
func flattenAll(g *ModelGroup) []*Particle {
	return flattenAllSeen(g, map[*ModelGroup]bool{})
}

// flattenAllSeen carries the set of groups already entered.
//
// A group definition whose content references itself is a cycle, and following
// it would recurse until the stack is gone. Stopping at a repeat drops the
// self-reference rather than looping; the schema is ill-formed either way, and
// the content-model compiler reports it.
func flattenAllSeen(g *ModelGroup, seen map[*ModelGroup]bool) []*Particle {
	if seen[g] {
		return nil
	}
	seen[g] = true
	defer delete(seen, g)

	nested := false
	for _, p := range g.Particles {
		if inner := allGroupOf(p); inner != nil {
			nested = true
			break
		}
	}
	if !nested {
		return g.Particles
	}
	var out []*Particle
	for _, p := range g.Particles {
		if inner := allGroupOf(p); inner != nil {
			out = append(out, flattenAllSeen(inner, seen)...)
			continue
		}
		out = append(out, p)
	}
	return out
}
