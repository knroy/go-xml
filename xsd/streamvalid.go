package xsd

import (
	"fmt"
	"math"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
	"github.com/knroy/go-xml/v2/internal/xsdstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// Validation on a stream (internal/xsdstream): the run ValidateCopy,
// ValidateElementLaxCopy and ValidateAgainstTypeCopy make over a tree, made
// over a cursor's events instead.
//
// It is the same validator. validateElement, validateAgainstType,
// validateComplexType, matchSequence, matchAll and validateChild walk a
// subtree they are given whole; here each is split at the points where it
// reads content: what it does before (the declaration, xsi:type and xsi:nil,
// the attributes and their defaults, the content model's start) runs at the
// start tag, each child element is one step of the content model, text is
// gathered where the type reads it, and what it does after (the content
// model's end, simple content, assertions, the annotation) runs at the end
// tag. Every check is one of the tree's, called with what it would read.
//
// The typing goes onto the parse's own records, which every copy the cursor
// makes of them copies (xdm copyProps, cloneMoving): an element's annotation
// at its start tag, for its views, and at its end again after its simple
// content, which is the order the tree writes them in; attributes at the
// start tag. Default attributes are appended to the start tag
// (xdmburst.AppendAttr), after the element's own, where the typed copy has
// them.
//
// A failure is recorded and the run stops validating: the caller has a tree
// to ask for the error the tree gives (its code, message and order), and
// only needs to know that there is one. So what this run must be is exact on
// a valid document, and exact about whether a document is valid.
//
// What it cannot do as the tree does is refused for the whole schema
// (streamRefusal): a content model whose count readings may outgrow the
// budget on a stream but not on the tree. Identity constraints are
// streamvalid_ic.go's.

func init() {
	xsdstream.Open = func(schema any, m xsdstream.Mode) xsdstream.Validator {
		return openStream(schema.(*Schema), m)
	}
	xsdstream.Refusal = func(schema any) string { return schema.(*Schema).streamRefusal() }
}

// svKind is what a frame's element is assessed as.
type svKind uint8

const (
	svSkip       svKind = iota // nothing is assessed (a skip wildcard's, or the tree does not walk it)
	svNil                      // nilled: must be empty
	svSimple                   // a simple type, or simple content: its text is the value
	svEmpty                    // empty content
	svEmptyElems               // element-only content whose model admits only the empty sequence
	svElems                    // element-only content
	svMixed                    // mixed content
	svOpen                     // empty content its open content opens (matchOpenOnly)
)

// svFrame is one open element.
type svFrame struct {
	el   *xdm.Node // the parse's record, valid while open
	kind svKind
	decl *ElementDecl
	typ  Type
	ct   *ComplexType
	st   *SimpleType // svSimple: the value's type (nil: none checked)
	// lax says the element is laxly assessed against xs:anyType: its
	// annotation is restored to prev at the end (validateChild).
	lax  bool
	prev xdm.Typing
	// path and inherited are what the element pushed on v.path and
	// v.inherited; assert says its type has assertions (v.assertScopes).
	path      bool
	inherited int
	assert    bool
	emptiable bool // element-only: the model admits the empty sequence
	seq       *svSeq
	all       *svAll
	kids      int    // child elements
	text      []byte // svSimple: the value; svMixed with a fixed value: its text
	hasText   bool   // any text at all
	nonSpace  string // the first text that is not all XML whitespace
	// child says the element has a child node of any kind (FirstChild),
	// which decides whether its value constraint is its value
	// (effectiveValue) and whether a fixed one applies to its content
	// (checkFixedValueConstraint); stripped says some of its text was
	// whitespace the stream stripped before Observe could see it.
	child, stripped bool
	// held is the element's copy in the tree an enclosing (or its own)
	// assertion reads (fillScopeCopy's shape); pre is the element's typing
	// before it was annotated, which is what that copy's root carries.
	held *xdm.Node
	pre  xdm.Typing
	// edt says v.childTypes holds keys of this element (noteChildType),
	// taken out at its end; tok is the element's ID owner (idToken).
	edt bool
	tok *xdm.Node
	// oc is openContentFor the element's type, or nil.
	oc *OpenContent
	// skipped says the element is in content a skip wildcard matched
	// (inSkippedContent); ic is its part in identity constraints, or nil
	// (streamvalid_ic.go).
	skipped bool
	ic      *svICFrame
	// preserve says the element has xml:space="preserve", which keeps
	// its ignorable whitespace (stripIgnorableWhitespace).
	preserve bool
}

// streamV is a validation run on a stream.
type streamV struct {
	v      *validator
	mode   xsdstream.Mode
	frames []svFrame
	err    error
	done   bool
	// toks holds the ID owners' tokens (idToken), icToks the identity
	// constraints' targets'; needs counts the open elements reading a
	// constraint's tables (streamvalid_ic.go).
	toks, icToks *xdm.Node
	needs        map[*IdentityConstraint]int
}

func openStream(s *Schema, m xsdstream.Mode) *streamV {
	v := s.newValidator(nil, ValidateOptions{})
	v.typed = true
	sv := &streamV{v: v, mode: m}
	v.idToken = sv.idToken
	return sv
}

// idToken stands in for owner, a record of the parse, as the owner of an ID
// binding: the parse reuses a record once its element is dropped, and two
// elements must not be one owner because they had one slot.
func (s *streamV) idToken(owner *xdm.Node) *xdm.Node {
	for i := len(s.frames) - 1; i >= 0; i-- {
		f := &s.frames[i]
		if f.el == owner {
			if f.tok == nil {
				// One record each, in a tree of tokens: a node a failure
				// can be reported at (it has no position).
				if s.toks == nil {
					s.toks = xdm.NewNode(xdm.KindElement, xdm.QName{Local: "ids"}, "")
				}
				f.tok = s.toks.AppendElement(owner.Name())
			}
			return f.tok
		}
	}
	return owner
}

func (s *streamV) Err() error {
	if s.err == nil && len(s.v.errs) > 0 {
		s.err = &ValidationErrors{Errors: s.v.errs}
	}
	return s.err
}

// failed reports whether the run has met a failure, and stops it if so.
func (s *streamV) failed() bool {
	if s.err != nil {
		return true
	}
	if len(s.v.errs) > 0 || s.v.stopped {
		if len(s.v.errs) == 0 {
			s.v.fail(nil, "", "validation stopped")
		}
		s.Err()
		return true
	}
	return false
}

func (s *streamV) Drops(el *xdm.Node) bool {
	if !s.mode.Doc || s.err != nil || len(s.frames) == 0 {
		return false
	}
	f := &s.frames[len(s.frames)-1]
	// stripIgnorableWhitespace: element-only content the model walks (not
	// the empty-only branch, which reads all its text).
	return f.el == el && f.kind == svElems && !f.preserve
}

func (s *streamV) Stripped(el *xdm.Node) {
	if s.err != nil || len(s.frames) == 0 {
		return
	}
	if f := &s.frames[len(s.frames)-1]; f.el == el {
		f.child, f.stripped = true, true
		s.text(f, " ", nil) // whitespace: only that there was some matters
		if s.v.icScopes > 0 {
			s.icText(len(s.frames)-1, "", true)
		}
	}
}

// Observe is the cursor's observer.
func (s *streamV) Observe(n *xdm.Node, depth int, end bool) {
	if s.failed() || s.done {
		return
	}
	switch {
	case n == nil:
		s.finish()
	case n.Kind() == xdm.KindElement && !end:
		s.start(n, depth)
	case n.Kind() == xdm.KindElement:
		s.end(depth)
	case depth >= 2 && depth-2 < len(s.frames):
		f := &s.frames[depth-2]
		f.child = true
		switch n.Kind() {
		case xdm.KindText:
			s.text(f, n.Value(), n)
			if s.v.icScopes > 0 {
				s.icText(depth-2, n.Value(), false)
			}
		default: // a comment or processing instruction: no content to assess
		}
	}
	s.failed()
}

func (s *streamV) start(el *xdm.Node, depth int) {
	for len(s.frames) < depth {
		s.frames = append(s.frames, svFrame{})
	}
	s.frames = s.frames[:depth]
	f := &s.frames[depth-1]
	*f = svFrame{el: el}
	if depth == 1 {
		s.startRoot(f, el)
	} else {
		s.startChild(depth-2, f, el)
	}
	s.hold(f, depth)
	s.icStart(f, depth-1)
}

// startRoot assesses the document element as the mode's entry point does.
func (s *streamV) startRoot(f *svFrame, el *xdm.Node) {
	v := s.v
	name := xdm.QName{URI: el.Name().URI, Local: el.Name().Local}
	switch s.mode.Root {
	case xsdstream.Type:
		// validateAgainstNamedType, then checkAgainstType.
		tn := bareName(s.mode.Type)
		typ, ok := v.schema.Types[tn]
		if !ok {
			if bt := BuiltinType(tn.Local); bt != nil && tn.URI == xdm.NSXS {
				typ = bt
			} else {
				v.fail(nil, "cvc-type.1", "no type named %s in the schema", showName(tn))
				return
			}
		}
		s.startAgainstType(f, el, typ, nil)
		return
	case xsdstream.Lax:
		if _, ok := v.schema.Elements[name]; !ok && el.Attr(NSInstance, "type") == nil {
			s.done = true // nothing to assess (laxAssesses)
			return
		}
	}
	// run.
	decl, ok := v.schema.Elements[name]
	if !ok {
		if xsiType := el.Attr(NSInstance, "type"); xsiType != nil {
			t, err := v.resolveXSIType(el, xsiType.Value())
			if err != nil {
				v.fail(el, "cvc-elt.4.2", "%v", err)
				return
			}
			s.startAgainstType(f, el, t, nil)
			return
		}
		v.fail(el, "cvc-elt.1", "no element declaration for {%s}%s", name.URI, name.Local)
		return
	}
	s.startElement(f, el, decl)
}

// startChild is validateChild for el, a child of parent's element: the
// content model's step, then the assessment of the position it took.
func (s *streamV) startChild(pi int, f *svFrame, el *xdm.Node) {
	v := s.v
	parent := &s.frames[pi]
	if parent.held != nil {
		f.held = parent.held.AppendShallowCopy(el)
	}
	parent.kids++
	parent.child = true
	switch parent.kind {
	case svSkip:
		return // f stays svSkip
	case svNil:
		v.fail(parent.el, "cvc-elt.3.2.1", "an element with xsi:nil=\"true\" must be empty")
		return
	case svSimple:
		if parent.ct == nil {
			v.fail(parent.el, "cvc-type.3.1.2",
				"an element with a simple type may not have element children")
		} else {
			v.fail(parent.el, "cvc-complex-type.2.2",
				"element has simple content but has element children")
		}
		return
	case svEmpty:
		v.fail(parent.el, "cvc-complex-type.2.1",
			"element must be empty but has element children")
		return
	}
	name := xdm.QName{URI: el.Name().URI, Local: el.Name().Local}
	var p *position
	walk := parent.ct
	switch {
	case parent.kind == svOpen:
		// matchOpenOnly: no content model is walked, so the type the
		// dynamic EDC check reads is the enclosing walk's.
		if !parent.oc.Wildcard.AllowsName(name, v.elementDefined) {
			v.fail(el, "cvc-complex-type.2.4.a",
				"element {%s}%s is not permitted by the open content wildcard", name.URI, name.Local)
			return
		}
		p = &position{term: parent.oc.Wildcard}
		walk = nil
		for i := pi - 1; i >= 0; i-- {
			if k := s.frames[i].kind; k == svElems || k == svEmptyElems || k == svMixed {
				walk = s.frames[i].ct
				break
			}
		}
	case parent.all != nil:
		p = s.allStep(parent.all, parent.oc, parent.el, el, name)
	default:
		p = s.seqStep(parent.seq, parent.oc, parent.el, el, name)
	}
	if p == nil {
		return
	}
	saved := v.currentType
	v.currentType = walk
	if v.schema.Version == Version11 {
		parent.edt = true
	}
	v.noteChildType(el, name, p, v.currentType)
	v.currentType = saved
	if w, ok := p.term.(*Wildcard); ok {
		switch w.ProcessContents {
		case ProcessSkip:
			f.skipped = true
			return
		case ProcessLax:
			if d, ok := v.schema.Elements[name]; ok {
				s.startElement(f, el, d)
				return
			}
			if xsiType := el.Attr(NSInstance, "type"); xsiType != nil {
				t, err := v.resolveXSIType(el, xsiType.Value())
				if err != nil {
					v.fail(el, "cvc-elt.4.2", "%v", err)
					return
				}
				s.startAgainstType(f, el, t, nil)
				return
			}
			f.lax, f.prev = true, v.typingOf(el)
			s.startAgainstType(f, el, v.schema.anyType(), nil)
			return
		case ProcessStrict:
			d, ok := v.schema.Elements[name]
			if !ok {
				if xsiType := el.Attr(NSInstance, "type"); xsiType != nil {
					t, err := v.resolveXSIType(el, xsiType.Value())
					if err != nil {
						v.fail(el, "cvc-elt.4.2", "%v", err)
						return
					}
					s.startAgainstType(f, el, t, nil)
					return
				}
				v.fail(el, "cvc-complex-type.2.4.c",
					"no declaration for {%s}%s, matched by a strict wildcard", name.URI, name.Local)
				return
			}
			s.startElement(f, el, d)
			return
		}
	}
	if d := p.resolveDecl(name); d != nil {
		s.startElement(f, el, d)
	}
}

// startElement is validateElement up to the content.
func (s *streamV) startElement(f *svFrame, el *xdm.Node, decl *ElementDecl) {
	v := s.v
	if maxDepth := v.opts.MaxDepth; maxDepth > 0 && len(v.path) >= maxDepth {
		v.failLimit(el, "cvc-elt.1", "element nesting exceeds %d levels", maxDepth)
		v.stopped = true
		return
	}
	s.icEnter(f, decl)
	v.path = append(v.path, el.Name().Local)
	f.path, f.decl = true, decl
	if decl.unresolved != "" {
		v.fail(el, "src-resolve",
			"element declaration {%s}%s refers to %q, which no definition matches",
			decl.Name.URI, decl.Name.Local, decl.unresolved)
		return
	}
	if decl.Abstract {
		v.fail(el, "cvc-elt.2", "element declaration {%s}%s is abstract",
			decl.Name.URI, decl.Name.Local)
		return
	}
	typ := decl.Type
	if v.schema.Version == Version11 {
		typ = v.selectAlternativeType(el, decl)
		f.inherited = v.pushInherited(el, decl)
		// What a descendant's type alternative reads of them is their name
		// and value (scopeForAlternative): detached copies, so that no
		// record of the parse is held past its start tag.
		for i := len(v.inherited) - f.inherited; i < len(v.inherited); i++ {
			a := v.inherited[i]
			v.inherited[i] = xdm.NewNode(xdm.KindAttribute, a.Name(), a.Value())
		}
	}
	if xsiType := el.Attr(NSInstance, "type"); xsiType != nil {
		t, err := v.resolveXSIType(el, xsiType.Value())
		if err != nil {
			v.fail(el, "cvc-elt.4.2", "%v", err)
			return
		}
		if !v.derivedFrom(t, typ) {
			v.fail(el, "cvc-elt.4.3", "xsi:type %q is not derived from the declared type",
				xsiType.Value())
			return
		}
		if m, blocked := v.substitutionBlocked(t, decl, typ); blocked {
			v.fail(el, "cvc-elt.4.3", "xsi:type %q substitutes by %v, which is blocked",
				xsiType.Value(), m)
			return
		}
		typ = t
	}
	if nilAttr := el.Attr(NSInstance, "nil"); nilAttr != nil {
		val := trimXMLSpace(nilAttr.Value())
		if !decl.Nillable {
			v.fail(el, "cvc-elt.3.1", "xsi:nil is present but the declaration is not nillable")
			return
		}
		if val == "true" || val == "1" {
			f.kind = svNil
			if decl.Constraint != nil && decl.Constraint.Fixed {
				v.fail(el, "cvc-elt.3.2.2",
					"an element with xsi:nil=\"true\" may not have a fixed value constraint")
			}
			switch t := typ.(type) {
			case *SimpleType:
				v.checkNoForeignAttributes(el, nil, nil)
			case *ComplexType:
				// nilledComplex: the assertions run at the end tag.
				if t.Abstract {
					v.fail(el, "cvc-type.2", "type {%s}%s is abstract and cannot validate an element directly",
						t.Name.URI, t.Name.Local)
					break
				}
				if v.schema.Version == Version11 && len(t.Assertions) > 0 {
					f.ct, f.assert = t, true
					v.assertScopes++
				}
				v.validateAttributes(el, t)
				s.applyDefaults(el)
			}
			f.pre = xdm.TypingOf(el)
			v.annotate(el, typ)
			t := v.typingOf(el)
			t.IsNilled = true
			v.setTyping(el, t)
			return
		}
	}
	if typ == nil {
		return // svSkip: the type never resolved, and the tree walks no further
	}
	s.startAgainstType(f, el, typ, decl)
}

// startAgainstType is validateAgainstType, and validateComplexType, up to
// the content.
func (s *streamV) startAgainstType(f *svFrame, el *xdm.Node, typ Type, decl *ElementDecl) {
	v := s.v
	f.typ, f.decl = typ, decl
	switch t := typ.(type) {
	case *SimpleType:
		f.kind, f.st = svSimple, t
		v.checkNoForeignAttributes(el, nil, nil)
	case *ComplexType:
		f.ct = t
		if t.Abstract {
			v.fail(el, "cvc-type.2", "type {%s}%s is abstract and cannot validate an element directly",
				t.Name.URI, t.Name.Local)
			return
		}
		if v.schema.Version == Version11 && len(t.Assertions) > 0 {
			f.assert = true
			v.assertScopes++
		}
		v.validateAttributes(el, t)
		s.applyDefaults(el)
		f.oc = v.openContentFor(t)
		switch t.Content {
		case ContentEmpty:
			f.kind = svEmpty
			if f.oc != nil {
				f.kind = svOpen
			}
		case ContentSimple:
			f.kind, f.st = svSimple, t.SimpleContent
		case ContentElementOnly:
			if a := el.Attr(xdm.NSXML, "space"); a != nil && a.Value() == "preserve" {
				f.preserve = true
			}
			f.emptiable = f.oc != nil || particleAcceptsEmpty(t.Particle, map[*Particle]bool{})
			f.kind = svElems
			if isEmptyContent(t) && f.oc == nil {
				f.kind = svEmptyElems
			}
			s.startModel(f, el, t)
		case ContentMixed:
			f.kind = svMixed
			s.startModel(f, el, t)
		}
	}
	if !f.lax {
		f.pre = xdm.TypingOf(el)
		v.annotate(el, typ)
	}
}

// startModel is validateChildren's start.
func (s *streamV) startModel(f *svFrame, el *xdm.Node, t *ComplexType) {
	v := s.v
	m, err := v.modelFor(t)
	if err != nil {
		v.fail(el, "", "compiling the content model: %v", err)
		return
	}
	if isAllGroup(t.Particle) {
		g := t.Particle.Term.(*ModelGroup)
		ps := flattenAll(g)
		f.all = &svAll{g: g, t: t, particles: ps, counts: make([]int, len(ps))}
		return
	}
	w := &svSeq{m: m, t: t, nested: len(m.counters) > 0, current: m.first, prevIdx: -1}
	if w.nested {
		nc := len(m.counters)
		ints := make([]int, 2*nc)
		w.counts = ints[:nc]
		// The child count is not known yet: a bounded maximum is taken as
		// reachable. The verdict is the same (capCount); the readings kept
		// may be more than the tree's.
		w.reach = reachableInto(m, math.MaxInt, ints[nc:])
	}
	f.seq = w
}

// applyDefaults appends to el, open on the stream, the attributes its type
// supplied by default (applyAttributeDefault), as the typed copy adds them.
func (s *streamV) applyDefaults(el *xdm.Node) {
	v := s.v
	as := v.defaults[el]
	if len(as) == 0 {
		return
	}
	// XSD 1.1 namespace fixup (declareFixupPrefix): the binding a default
	// in a namespace needed goes on the start tag, after the element's own,
	// where the typed copy declares it; lookupPrefix then finds it there.
	for _, ns := range v.fixups[el] {
		if !xdmburst.AddNamespace(el, ns.prefix, ns.uri) {
			v.fail(el, "", "streaming: a namespace cannot be declared here")
			return
		}
	}
	delete(v.fixups, el)
	for _, a := range as {
		q := a.Name()
		live, _ := xdmburst.AppendAttr(el, q.Prefix, q.URI, q.Local, a.Value()).(*xdm.Node)
		if live == nil {
			v.fail(el, "", "streaming: a default attribute cannot be added here")
			return
		}
		live.ApplyTyping(xdm.TypingOf(a))
		live.SetTypeEnv(a.TypeEnv())
		delete(v.attrOwner, a)
	}
	delete(v.defaults, el)
}

// text is a text child of f's element; n is the parse's node (nil for
// whitespace the stream stripped).
func (s *streamV) text(f *svFrame, value string, n *xdm.Node) {
	if f.held != nil && n != nil {
		f.held.AppendShallowCopy(n)
	}
	if value == "" {
		return
	}
	f.hasText = true
	if f.nonSpace == "" && trimXMLSpace(value) != "" {
		f.nonSpace = value
	}
	v := s.v
	switch f.kind {
	case svNil:
		v.fail(f.el, "cvc-elt.3.2.1", "an element with xsi:nil=\"true\" must be empty")
	case svSimple:
		f.text = append(f.text, value...)
	case svMixed:
		if f.decl != nil && f.decl.Constraint != nil && f.decl.Constraint.Fixed {
			f.text = append(f.text, value...)
		}
	case svEmpty, svEmptyElems:
		if f.nonSpace != "" {
			v.fail(f.el, "cvc-complex-type.2.1",
				"element must be empty but has character content %q", truncate(trimXMLSpace(f.nonSpace)))
		} else if f.kind == svEmptyElems {
			v.fail(f.el, "cvc-complex-type.2.1", "element must be empty but has character content")
		}
	case svElems:
		if f.nonSpace != "" {
			v.fail(f.el, "cvc-complex-type.2.3",
				"element-only content may not contain character data %q", truncate(f.nonSpace))
		}
	}
}

// end is the element at depth closing: the tree's checks after the content.
func (s *streamV) end(depth int) {
	if depth > len(s.frames) {
		return
	}
	f := &s.frames[depth-1]
	v, el := s.v, f.el
	switch f.kind {
	case svSimple:
		if f.st != nil {
			v.validateSimpleContent(el, s.value(f), f.st, f.decl)
		}
	case svElems:
		if !f.emptiable && f.kids == 0 {
			v.fail(el, "cvc-complex-type.2.4.b",
				"element has no children, and the content model admits no sequence at all")
			break
		}
		s.endModel(f)
	case svEmptyElems:
		if !f.emptiable && f.kids == 0 {
			v.fail(el, "cvc-complex-type.2.4.b",
				"element has no children, and the content model admits no sequence at all")
			break
		}
		s.endModel(f)
	case svMixed:
		s.endModel(f)
	}
	if f.held != nil && f.kind != svSkip && (f.typ != nil || f.kind == svNil) {
		if f.assert && len(v.errs) == 0 {
			f.held.ApplyTyping(f.pre)
			if f.kind == svSimple && f.st != nil {
				_, _ = validateSimpleValueIn(s.value(f), f.st, v.schema.Version, &instance{f.held, v})
			}
			v.checkAssertions(f.held, f.ct)
		}
	}
	if f.assert {
		v.assertScopes--
	}
	if f.typ != nil && f.kind != svNil {
		v.annotate(el, f.typ)
		if f.lax {
			v.setTypeAnnotationResolved(el, f.prev.TypeAnnotation, f.prev.DerivedPrimitive, f.prev.ListItem)
		}
	}
	if f.held != nil {
		f.held.ApplyTyping(xdm.TypingOf(el))
	}
	if f.typ != nil && f.kind != svNil {
		s.checkFixed(f)
	}
	s.icEnd(f, depth-1)
	if f.path {
		v.path = v.path[:len(v.path)-1]
	}
	if f.inherited > 0 {
		v.inherited = v.inherited[:len(v.inherited)-f.inherited]
	}
	if f.edt {
		for k := range v.childTypes {
			if k.parent == el {
				delete(v.childTypes, k)
			}
		}
	}
	s.frames = s.frames[:depth-1]
}

// value is effectiveValue for f's element, svSimple: its text, or its
// declaration's value constraint when it has no child at all. The typed copy
// adds no text for a defaulted value (an empty element stays empty, and
// atomizes as its annotation reads ""), so neither does the stream.
func (s *streamV) value(f *svFrame) string {
	if !f.child && f.decl != nil && f.decl.Constraint != nil {
		return f.decl.Constraint.Lexical
	}
	return string(f.text)
}

// checkFixed is checkFixedValueConstraint for f's element at its end.
func (s *streamV) checkFixed(f *svFrame) {
	v, decl := s.v, f.decl
	if decl == nil || decl.Constraint == nil || !decl.Constraint.Fixed || !f.child {
		return
	}
	if f.kids > 0 {
		v.fail(f.el, "cvc-elt.5.2.2.1",
			"an element with a fixed value constraint may not have element children")
		return
	}
	if f.kind != svMixed {
		return
	}
	got, want := string(f.text), decl.Constraint.Lexical
	if f.stripped && strings.ContainsAny(want, " \t\r\n") {
		// The whitespace the stream stripped is part of the value, and
		// the stream saw only that there was some.
		v.fail(f.el, "", "streaming: whitespace stripped before validation decides a fixed value")
		return
	}
	if f.stripped || got != want {
		v.fail(f.el, "cvc-elt.5.2.2.2.1",
			"value %q does not equal the fixed value %q", truncate(got), truncate(want))
	}
}

func (s *streamV) endModel(f *svFrame) {
	if f.all != nil {
		s.allEnd(f.all, f.el)
	} else if f.seq != nil {
		s.seqEnd(f.seq, f.el)
	}
}

// finish is the document's end: the ID/IDREF constraints run applies.
func (s *streamV) finish() {
	s.done = true
	if s.mode.Root != xsdstream.Type {
		s.v.checkIDs()
	}
}

// hold starts the copy an assertion reads, at an element whose type has
// assertions and no enclosing one's copy (startChild makes the others).
func (s *streamV) hold(f *svFrame, depth int) {
	v := s.v
	if f.held == nil && f.assert {
		el := f.el
		h := xdm.NewNode(xdm.KindElement, el.Name(), "")
		h.SetBaseURI(el.BaseURI())
		// The frame is built in one pass and set once, as scopeForAssertion
		// does: one AddNamespace per binding copies the frame built so far.
		var pairs []string
		declared := map[string]bool{}
		for prefix, uri := range el.DeclaredNamespaces() {
			declared[prefix] = true
			pairs = append(pairs, prefix, uri)
		}
		for prefix, uri := range v.inScopeNamespaces(el) {
			if !declared[prefix] {
				pairs = append(pairs, prefix, uri)
			}
		}
		h.AddNamespaces(pairs...)
		f.held = h
	} else if f.held != nil {
		var pairs []string
		for prefix, uri := range f.el.DeclaredNamespaces() {
			pairs = append(pairs, prefix, uri)
		}
		f.held.AddNamespaces(pairs...)
	}
	if f.held != nil {
		for a := range f.el.Attrs() {
			f.held.AppendAttr(a.Name(), a.Value()).ApplyTyping(xdm.TypingOf(a))
		}
	}
	_ = depth
}

// svSeq is matchSequence as a step machine: its locals, kept between the
// children.
type svSeq struct {
	m                             *contentModel
	t                             *ComplexType
	nested                        bool
	counts, reach                 []int
	vectors, stepped, wildStepped [][]int
	current                       []int
	prevIdx                       int
	inSuffix                      bool
}

// seqStep is matchSequence's loop body for the child kid of el: the
// position it takes, or nil when kid is refused (v.fail has said why).
func (s *streamV) seqStep(w *svSeq, oc *OpenContent, el, kid *xdm.Node, name xdm.QName) *position {
	v, m := s.v, w.m
	if len(m.positions) == 0 {
		if oc != nil && oc.Wildcard.AllowsName(kid.Name(), v.elementDefined) {
			return &position{term: oc.Wildcard}
		}
		v.fail(kid, "cvc-complex-type.2.4.d",
			"element {%s}%s is not permitted here: the content model is empty",
			kid.Name().URI, kid.Name().Local)
		return nil
	}
	incomplete := false
	admits := func(idx int) bool {
		if !w.nested {
			return true
		}
		w.stepped = w.stepped[:0]
		if w.prevIdx < 0 {
			enterCounts(m, w.reach, idx, w.counts)
			w.stepped = append(w.stepped, scratchVec(&w.stepped, w.counts))
			return true
		}
		for _, vec := range w.vectors {
			stepCountsWhy(m, w.reach, w.prevIdx, idx, vec, &w.stepped, &incomplete)
		}
		return len(w.stepped) > 0
	}
	keep := func() bool {
		w.vectors, w.stepped = w.stepped, w.vectors[:0]
		return v.withinMatchStates(len(w.vectors))
	}
	next := -1
	boundRefused := false
	for _, idx := range w.current {
		p := m.positions[idx]
		if w.inSuffix {
			break
		}
		if !p.matches(name, v.elementDefined) {
			continue
		}
		if !admits(idx) {
			boundRefused = true
			continue
		}
		if _, isWildcard := p.term.(*Wildcard); isWildcard {
			if next < 0 {
				next = idx
				if w.nested {
					w.stepped, w.wildStepped = w.wildStepped, w.stepped
				}
			}
			continue
		}
		next = idx
		if w.nested && !keep() {
			v.fail(el, "", "%v", errMatchStates)
			return nil
		}
		break
	}
	if next >= 0 && w.nested {
		if _, isWildcard := m.positions[next].term.(*Wildcard); isWildcard {
			w.stepped, w.wildStepped = w.wildStepped, w.stepped[:0]
			if !keep() {
				v.fail(el, "", "%v", errMatchStates)
				return nil
			}
		}
	}
	if next < 0 {
		// XSD 1.1 open content: interleaved anywhere, a suffix once the
		// model is satisfied.
		satisfied := w.prevIdx < 0 && m.nullable || w.prevIdx >= 0 && contains(m.last, w.prevIdx)
		if oc != nil && oc.Wildcard.AllowsName(name, v.elementDefined) &&
			(oc.Mode == OpenInterleave || satisfied) {
			if oc.Mode == OpenSuffix {
				w.inSuffix = true
			}
			return &position{term: oc.Wildcard}
		}
		if boundRefused && incomplete {
			v.fail(kid, "cvc-complex-type.2.4.b",
				"element content is incomplete: {%s}%s cannot follow until an earlier particle has met its minimum",
				kid.Name().URI, kid.Name().Local)
			return nil
		}
		v.fail(kid, "cvc-complex-type.2.4.a", "element {%s}%s is not permitted here%s",
			kid.Name().URI, kid.Name().Local, expected(m, w.current))
		return nil
	}
	w.prevIdx = next
	w.current = m.follow[next]
	return m.positions[next]
}

// seqEnd is matchSequence's end: the sequence must be able to end here.
func (s *streamV) seqEnd(w *svSeq, el *xdm.Node) {
	v, m := s.v, w.m
	if w.prevIdx < 0 {
		if !m.nullable {
			v.fail(el, "cvc-complex-type.2.4.b", "element content is incomplete%s", expected(m, m.first))
		}
		return
	}
	if !contains(m.last, w.prevIdx) || w.nested && !someReadingSatisfies(m, w.vectors, w.prevIdx) {
		v.fail(el, "cvc-complex-type.2.4.b", "element content is incomplete%s", expected(m, m.follow[w.prevIdx]))
	}
}

// svAll is matchAll's locals.
type svAll struct {
	g         *ModelGroup
	t         *ComplexType
	particles []*Particle
	counts    []int
	inSuffix  bool
}

func (s *streamV) allStep(w *svAll, oc *OpenContent, el, kid *xdm.Node, name xdm.QName) *position {
	v := s.v
	exhausted := -1
	for i, p := range w.particles {
		if w.inSuffix {
			break
		}
		pos := &position{term: p.Term, particle: p}
		if !pos.matches(name, v.elementDefined) {
			continue
		}
		if p.MaxOccurs != Unbounded && w.counts[i] >= p.MaxOccurs {
			if exhausted < 0 {
				exhausted = i
			}
			continue
		}
		w.counts[i]++
		return pos
	}
	if exhausted >= 0 {
		p := w.particles[exhausted]
		v.fail(kid, "cvc-complex-type.2.4.j", "element {%s}%s appears more than %d times in an all group",
			name.URI, name.Local, p.MaxOccurs)
		return nil
	}
	if oc != nil && oc.Wildcard.AllowsName(name, v.elementDefined) {
		if oc.Mode == OpenSuffix {
			w.inSuffix = true
		}
		return &position{term: oc.Wildcard}
	}
	v.fail(kid, "cvc-complex-type.2.4.a", "element {%s}%s is not permitted in this all group",
		name.URI, name.Local)
	return nil
}

func (s *streamV) allEnd(w *svAll, el *xdm.Node) {
	v, t, counts, particles := s.v, w.t, w.counts, w.particles
	if t != nil && t.Particle != nil && t.Particle.MinOccurs == 0 {
		anyPresent := false
		for _, n := range counts {
			if n > 0 {
				anyPresent = true
				break
			}
		}
		if !anyPresent {
			return
		}
	}
	optional := optionalAllMembers(w.g, particles)
	if len(optional) > 0 {
		present, missing := 0, 0
		for _, idx := range optional {
			if counts[idx] > 0 {
				present++
			} else if particles[idx].MinOccurs > 0 {
				missing++
			}
		}
		if present > 0 && missing > 0 {
			v.fail(el, "cvc-complex-type.2.4.b",
				"an optional all group is present, so every element it requires must be present")
		}
	}
	for i, p := range particles {
		if counts[i] >= p.MinOccurs || len(optional) > 0 && contains(optional, i) {
			continue
		}
		switch term := p.Term.(type) {
		case *ElementDecl:
			v.fail(el, "cvc-complex-type.2.4.b", "required element %s is missing from an all group", term.Name.Local)
		case *Wildcard:
			v.fail(el, "cvc-complex-type.2.4.b", "an all group requires %d more element(s) matching a wildcard",
				p.MinOccurs-counts[i])
		}
	}
}

// streamReadingsBounded reports whether matchSequence over m cannot reach
// budget readings (ValidateOptions.MaxMatchStates, negative for none) on a
// stream unless it reaches them on the tree too. A tree walk knows its child count and takes a bounded maximum
// above it as unbounded (reachableInto), which merges the counts past the
// minimum; a stream cannot, and keeps them apart. The readings at a position
// differ only in the counters of its chain (the scopes left are reset), so
// the number of them is at most the product of what each counter can take:
// 0..max, or 0..min+1 once merged. A chain with no bounded counter walks
// the same on both; one with a bounded counter is bounded when even the
// larger of the two products is within the budget, and then neither walk
// gives up.
func streamReadingsBounded(m *contentModel, budget int) bool {
	if budget < 0 {
		return true // neither walk gives up
	}
	over := budget // budget+1, which MaxInt cannot hold
	if over < math.MaxInt {
		over++
	}
	for _, p := range m.positions {
		bounded, n := false, 1
		for _, i := range p.counters {
			c := m.counters[i]
			r := c.min + 2
			if c.max != Unbounded {
				bounded, r = true, max(r, c.max+1)
			}
			// Both at most the budget + 1 before the product: no overflow.
			n = min(n*min(r, over), over)
		}
		if bounded && n > budget {
			return false
		}
	}
	return true
}

// streamRefusal says why s cannot be validated on a stream as a tree is,
// or "". It looks at every element declaration and complex type the schema
// has, global and local.
//
// ponytail: walked on every call, which is per streamed document; cache it
// per schema if a profile shows it (a schema may still be extended after
// compiling, as the conformance harness's mergeInto does).
func (s *Schema) streamRefusal() string {
	mv := s.newValidator(nil, ValidateOptions{})
	seenT := map[Type]bool{}
	seenP := map[*Particle]bool{}
	seenD := map[*ElementDecl]bool{}
	var decl func(d *ElementDecl) string
	var typ func(t Type) string
	var part func(p *Particle) string
	decl = func(d *ElementDecl) string {
		if d == nil || seenD[d] {
			return ""
		}
		seenD[d] = true
		for _, alt := range d.Alternatives {
			if w := typ(alt.Type); w != "" {
				return w
			}
		}
		for _, sub := range d.substitutable {
			if w := decl(sub); w != "" {
				return w
			}
		}
		return typ(d.Type)
	}
	typ = func(t Type) string {
		ct, ok := t.(*ComplexType)
		if !ok || ct == nil || seenT[t] {
			return ""
		}
		seenT[t] = true
		if !isAllGroup(ct.Particle) {
			if m, err := mv.modelFor(ct); err == nil && !streamReadingsBounded(m, mv.matchStates()) {
				return fmt.Sprintf("the content model of type %s may hold more than %d count readings on a stream",
					showName(ct.Name), mv.matchStates())
			}
		}
		if w := part(ct.Particle); w != "" {
			return w
		}
		if b, ok := ct.Base.(*ComplexType); ok && b != ct {
			return typ(b)
		}
		return ""
	}
	part = func(p *Particle) string {
		if p == nil || seenP[p] {
			return ""
		}
		seenP[p] = true
		switch t := p.Term.(type) {
		case *ElementDecl:
			return decl(t)
		case *ModelGroup:
			for _, q := range t.Particles {
				if w := part(q); w != "" {
					return w
				}
			}
		}
		return ""
	}
	for _, d := range s.Elements {
		if w := decl(d); w != "" {
			return w
		}
	}
	for _, t := range s.Types {
		if w := typ(t); w != "" {
			return w
		}
	}
	return ""
}
