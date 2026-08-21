package xsd

import (
	"fmt"
	"strings"
	"sync"

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

	// stopped records that the error limit was reached.
	stopped bool
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
func (v *validator) validateElement(el *xdm.Node, decl *ElementDecl) {
	if v.stopped {
		return
	}
	v.path = append(v.path, el.Name.Local)
	defer func() { v.path = v.path[:len(v.path)-1] }()

	// An abstract declaration cannot itself validate an element; only a
	// member of its substitution group can.
	if decl.Abstract {
		v.fail(el, "cvc-elt.2",
			"element declaration {%s}%s is abstract",
			decl.Name.URI, decl.Name.Local)
		return
	}

	typ := decl.Type

	// xsi:type overrides the declared type, subject to the blocking rules.
	if xsiType := el.Attr(NSInstance, "type"); xsiType != nil {
		t, err := v.resolveXSIType(el, xsiType.Value)
		if err != nil {
			v.fail(el, "cvc-elt.4.2", "%v", err)
			return
		}
		if !v.derivedFrom(t, decl.Type) {
			v.fail(el, "cvc-elt.4.3",
				"xsi:type %q is not derived from the declared type",
				xsiType.Value)
			return
		}
		typ = t
	}

	// xsi:nil permits an empty element where the declaration allows it.
	if nilAttr := el.Attr(NSInstance, "nil"); nilAttr != nil {
		val := strings.TrimSpace(nilAttr.Value)
		if !decl.Nillable {
			v.fail(el, "cvc-elt.3.1",
				"xsi:nil is present but the declaration is not nillable")
			return
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
			return
		}
	}

	if typ == nil {
		// The type never resolved; the schema parse reported it.
		return
	}
	v.validateAgainstType(el, typ, decl)
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
func (v *validator) validateAgainstType(el *xdm.Node, typ Type, decl *ElementDecl) {
	switch t := typ.(type) {
	case *SimpleType:
		// An element with a simple type may have no element children and
		// no attributes other than the four xsi: ones.
		if kids := el.ChildElements(); len(kids) > 0 {
			v.fail(el, "cvc-type.3.1.2",
				"an element with a simple type may not have element children")
		}
		v.checkNoForeignAttributes(el, nil, nil)
		v.validateSimpleContent(el, el.StringValue(), t, decl)

	case *ComplexType:
		v.validateComplexType(el, t, decl)
	}

	if v.opts.Annotate {
		if n := typ.TypeName(); n.Local != "" {
			el.TypeAnnotation = n.Local
		}
	}
}

// validateComplexType checks an element against a complex type.
func (v *validator) validateComplexType(el *xdm.Node, t *ComplexType, decl *ElementDecl) {
	if t.Abstract {
		v.fail(el, "cvc-type.2",
			"type {%s}%s is abstract and cannot validate an element directly",
			t.Name.URI, t.Name.Local)
		return
	}

	v.validateAttributes(el, t)

	switch t.Content {
	case ContentEmpty:
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
			v.validateSimpleContent(el, el.StringValue(), t.SimpleContent, decl)
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
		v.validateChildren(el, t)

	case ContentMixed:
		v.validateChildren(el, t)
	}
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
func (v *validator) validateChildren(el *xdm.Node, t *ComplexType) {
	m, err := v.modelFor(t)
	if err != nil {
		v.fail(el, "", "compiling the content model: %v", err)
		return
	}
	kids := el.ChildElements()

	if isAllGroup(t.Particle) {
		v.matchAll(el, kids, t.Particle.Term.(*ModelGroup))
		return
	}
	v.matchSequence(el, kids, m)
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
// The cache is on the validator's schema rather than on the type so that a
// concurrently validating goroutine does not race to write the same field. A
// schema is documented as safe to share once loaded, and that has to remain
// true when it is used.
func (v *validator) modelFor(t *ComplexType) (*contentModel, error) {
	if m, ok := modelCache.Load(t); ok {
		if e, isErr := m.(error); isErr {
			return nil, e
		}
		return m.(*contentModel), nil
	}
	m, err := compileContentModel(t.Particle)
	if err != nil {
		modelCache.Store(t, err)
		return nil, err
	}
	modelCache.Store(t, m)
	return m, nil
}

// modelCache holds compiled content models, keyed by complex type.
//
// A sync.Map rather than a mutex-guarded map because the access pattern is
// write-once then read-many: every validation of a document using a given type
// reads the same entry, and after the first document there are no more writes.
var modelCache sync.Map

// matchSequence walks the automaton over an element's children.
func (v *validator) matchSequence(el *xdm.Node, kids []*xdm.Node, m *contentModel) {
	if len(m.positions) == 0 {
		if len(kids) > 0 {
			v.fail(kids[0], "cvc-complex-type.2.4.d",
				"element {%s}%s is not permitted here: the content model "+
					"is empty", kids[0].Name.URI, kids[0].Name.Local)
		}
		return
	}

	// counts tracks the repetitions of each counter scope.
	counts := make([]int, len(m.counters))
	current := m.first
	var prev *position
	prevIdx := -1

	for _, kid := range kids {
		name := xdm.QName{URI: kid.Name.URI, Local: kid.Name.Local}
		next := -1
		for _, idx := range current {
			p := m.positions[idx]
			if !p.matches(name) {
				continue
			}
			if !counterAllows(m, counts, prevIdx, idx) {
				continue
			}
			next = idx
			break
		}
		if next < 0 {
			v.fail(kid, "cvc-complex-type.2.4.a",
				"element {%s}%s is not permitted here%s",
				kid.Name.URI, kid.Name.Local, expected(m, current))
			return
		}

		p := m.positions[next]
		advanceCounters(m, counts, prevIdx, next)
		v.validateChild(kid, p)

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
		return
	}
	if !contains(m.last, prevIdx) || !countersSatisfied(m, counts, prevIdx) {
		v.fail(el, "cvc-complex-type.2.4.b",
			"element content is incomplete%s", expected(m, m.follow[prevIdx]))
	}
}

// counterAllows reports whether taking a transition to a position is permitted
// by the repetition bounds.
func counterAllows(m *contentModel, counts []int, from, to int) bool {
	p := m.positions[to]
	for _, c := range p.counters {
		// Entering the same scope again is what the count bounds; the
		// count is only consulted for a scope that is being re-entered
		// rather than entered for the first time.
		if from >= 0 && sharesScope(m.positions[from], c) {
			if m.counters[c].max != Unbounded && counts[c] >= m.counters[c].max {
				return false
			}
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
		// Re-entering from the scope's own last position means another
		// repetition; staying within it does not.
		if from >= 0 && contains(m.follow[from], to) && isScopeRestart(m, from, to, c) {
			counts[c]++
		}
	}
}

// isScopeRestart reports whether a transition goes back to the start of a
// repetition scope rather than forward within it.
func isScopeRestart(m *contentModel, from, to, scope int) bool {
	// A restart is a transition to a position that can begin the scope,
	// from one that can end it. Approximating this by "to is reachable
	// from itself" would count forward transitions inside the scope.
	return to <= from
}

// countersSatisfied reports whether every counter containing a position has met
// its minimum.
func countersSatisfied(m *contentModel, counts []int, at int) bool {
	for _, c := range m.positions[at].counters {
		if counts[c] < m.counters[c].min {
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
func (v *validator) matchAll(el *xdm.Node, kids []*xdm.Node, g *ModelGroup) {
	seen := make([]bool, len(g.Particles))
	for _, kid := range kids {
		name := xdm.QName{URI: kid.Name.URI, Local: kid.Name.Local}
		found := false
		for i, p := range g.Particles {
			d, ok := p.Term.(*ElementDecl)
			if !ok {
				continue
			}
			pos := &position{term: d, particle: p}
			if !pos.matches(name) {
				continue
			}
			if seen[i] {
				v.fail(kid, "cvc-complex-type.2.4.j",
					"element {%s}%s appears more than once in an all group",
					name.URI, name.Local)
				found = true
				break
			}
			seen[i] = true
			found = true
			v.validateChild(kid, pos)
			break
		}
		if !found {
			v.fail(kid, "cvc-complex-type.2.4.a",
				"element {%s}%s is not permitted in this all group",
				name.URI, name.Local)
		}
	}
	for i, p := range g.Particles {
		if seen[i] || p.MinOccurs == 0 {
			continue
		}
		if d, ok := p.Term.(*ElementDecl); ok {
			v.fail(el, "cvc-complex-type.2.4.b",
				"required element %s is missing from an all group", d.Name.Local)
		}
	}
}

// validateChild validates one matched child against the position that matched
// it.
func (v *validator) validateChild(kid *xdm.Node, p *position) {
	name := xdm.QName{URI: kid.Name.URI, Local: kid.Name.Local}

	if w, ok := p.term.(*Wildcard); ok {
		switch w.ProcessContents {
		case ProcessSkip:
			// Nothing is checked, by definition.
			return
		case ProcessLax:
			if d, ok := v.schema.Elements[name]; ok {
				v.validateElement(kid, d)
			}
			return
		case ProcessStrict:
			d, ok := v.schema.Elements[name]
			if !ok {
				v.fail(kid, "cvc-complex-type.2.4.c",
					"no declaration for {%s}%s, matched by a strict wildcard",
					name.URI, name.Local)
				return
			}
			v.validateElement(kid, d)
			return
		}
	}

	if d := p.resolveDecl(name); d != nil {
		v.validateElement(kid, d)
	}
}
