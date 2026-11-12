package xsd

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

// Identity constraints on a stream: what checkIdentityConstraints does over
// a tree, done as a cursor reads the document.
//
// Selectors and fields are §3.11.6's restricted paths: child steps, an
// optional leading ".//", a final attribute step in a field. Whether an
// element is selected is therefore known at its start tag from the names of
// the elements open above it, and a key sequence is complete at its
// target's end tag, every field node being the target, an attribute of an
// element in its subtree, or such an element. So each scope keeps what the
// tree keeps -- the key tables -- and none of the document: a target is a
// token (idToken's), its key sequence a string.
//
// The tables are the tree's own (fillNodeTable, fillKeyref, mergeTables):
// a scope's targets are those its pruned walk would select
// (selectNodesPruned: a ".//" path does not start below a nested scope of
// the same constraint, whose table comes up merged instead), the tables of
// its children come up through the elements validateElement returns them
// from, and the check runs at the scope's end tag, after its content, as
// the tree's does. A table goes up only while an open element declares its
// constraint or a keyref to it: nothing else reads it.
//
// The typing a key reads is the one the run writes: keyValues and
// defaultedAttrs (recorded while v.icScopes counts an open scope) for the
// live records, read as soon as they are written and then forgotten (the
// parse reuses the records); complexTyped is the frame's type.

// svICFrame is an element's part in identity-constraint evaluation.
type svICFrame struct {
	// scope says the element's declaration has constraints and the tree
	// checks them (validateElement reaches checkIdentityConstraints).
	scope bool
	decl  *ElementDecl
	// own is, per constraint of a scope, the targets its own walk
	// selected, in the order their ends came, with their key sequences.
	own map[*IdentityConstraint][]svKeyed
	// tables are the children's tables, merged (mergeTables).
	tables icTables
	// targets are the constraints that selected the element, waiting for
	// its end tag.
	targets []*svTarget
	// fieldOf are the fields the element is the first node of.
	fieldOf []svFieldRef
	// text is the element's string value, for a field whose key is its
	// lexical form (keyString without a recorded value); stripped says
	// whitespace the stream stripped was part of it.
	text     *strings.Builder
	stripped bool
	// tok is the element's token in s.icToks, when it is a target.
	tok *xdm.Node
}

// svTarget is an element selected by a constraint, and its fields so far.
type svTarget struct {
	ic     *IdentityConstraint
	scopes []int // the frames of the scopes that selected it
	fields []svField
}

// svField is one field's nodes under a target: how many, and the first.
type svField struct {
	n       int
	key     string
	complex bool   // the node is an element with no simple value
	untyped string // the node is this attribute, unassessed (untypedAttrs)
}

type svFieldRef struct {
	t *svTarget
	i int
}

// svKeyed is a target's key sequence, as keySequence gives it.
type svKeyed struct {
	tok             *xdm.Node
	seq             []string
	complete, valid bool
}

// icFrame is f's part, made on first use.
func (f *svFrame) icFrame() *svICFrame {
	if f.ic == nil {
		f.ic = &svICFrame{}
	}
	return f.ic
}

// icEnter is validateElement's entry for decl: inside a scope, the
// keyValues and defaultedAttrs a key reads are recorded.
func (s *streamV) icEnter(f *svFrame, decl *ElementDecl) {
	if len(decl.IdentityConstraints) > 0 {
		s.v.icScopes++
		f.icFrame().decl = decl
	}
}

// declares reports whether f's element was validated against a declaration
// carrying ic (declaresConstraint).
func (f *svFrame) declares(ic *IdentityConstraint) bool {
	if f.decl == nil {
		return false
	}
	for _, d := range f.decl.IdentityConstraints {
		if d == ic {
			return true
		}
	}
	return false
}

// pathMatches reports whether the elements open at frames from+1 .. to
// match steps one to one.
func (s *streamV) pathMatches(steps []ICStep, from, to int) bool {
	if to-from != len(steps) {
		return false
	}
	for i, st := range steps {
		if !stepMatches(st, s.frames[from+1+i].el) {
			return false
		}
	}
	return true
}

// selects reports whether path, from the scope at frame si, selects the
// element at frame x as selectNodesPruned does for ic.
func (s *streamV) selects(path *ICPath, ic *IdentityConstraint, si, x int) bool {
	for _, alt := range path.Alternatives {
		start := x - len(alt.Steps)
		if !alt.DescendantOrSelf {
			if start == si && s.pathMatches(alt.Steps, si, x) {
				return true
			}
			continue
		}
		if start < si || !s.pathMatches(alt.Steps, start, x) {
			continue
		}
		pruned := false
		for j := si + 1; j <= start && !pruned; j++ {
			pruned = s.frames[j].declares(ic)
		}
		if !pruned {
			return true
		}
	}
	return false
}

// icStart is the element at frame x's start tag, validated: the
// constraints that select it, and the fields it is a node of.
func (s *streamV) icStart(f *svFrame, x int) {
	v := s.v
	if x > 0 {
		f.skipped = f.skipped || s.frames[x-1].skipped
	}
	if v.icScopes == 0 {
		return
	}
	if ic := f.ic; ic != nil && ic.decl != nil && (f.typ != nil || f.kind == svNil) {
		ic.scope = true
		ic.own = map[*IdentityConstraint][]svKeyed{}
		for _, c := range ic.decl.IdentityConstraints {
			s.need(c, 1)
		}
	}
	// The constraints that select it.
	if !f.skipped {
		for si := 0; si <= x; si++ {
			sc := s.frames[si].ic
			if sc == nil || !sc.scope {
				continue
			}
			for _, c := range sc.decl.IdentityConstraints {
				if !s.selects(c.Selector, c, si, x) {
					continue
				}
				fi := f.icFrame()
				var t *svTarget
				for _, have := range fi.targets {
					if have.ic == c {
						t = have
					}
				}
				if t == nil {
					t = &svTarget{ic: c, fields: make([]svField, len(c.Fields))}
					fi.targets = append(fi.targets, t)
					s.token(f)
				}
				t.scopes = append(t.scopes, si)
			}
		}
	}
	// The fields it is a node of: as the element, or by its attributes.
	for ti := 0; ti <= x; ti++ {
		tf := s.frames[ti].ic
		if tf == nil {
			continue
		}
		for _, t := range tf.targets {
			for i, field := range t.ic.Fields {
				s.fieldNodes(f, x, ti, t, i, field)
			}
		}
	}
	clear(v.keyValues)
	clear(v.defaultedAttrs)
	clear(v.untypedAttrs)
}

// fieldNodes counts the nodes field, of the target t at frame ti, selects
// at the element at frame x (selectNodes, walkSteps).
func (s *streamV) fieldNodes(f *svFrame, x, ti int, t *svTarget, i int, field *ICPath) {
	v := s.v
	el, elem := f.el, false
	var attrs []*xdm.Node
	for _, alt := range field.Alternatives {
		start := x - len(alt.Steps)
		if alt.DescendantOrSelf && start < ti || !alt.DescendantOrSelf && start != ti ||
			!s.pathMatches(alt.Steps, start, x) {
			continue
		}
		if alt.Attribute == nil {
			elem = true
			continue
		}
	next:
		for _, a := range v.selectAttrs([]*xdm.Node{el}, alt) {
			for _, b := range attrs {
				if a == b {
					continue next
				}
			}
			attrs = append(attrs, a)
		}
	}
	fd := &t.fields[i]
	if elem {
		if fd.n++; fd.n == 1 {
			fi := f.icFrame()
			fi.fieldOf = append(fi.fieldOf, svFieldRef{t, i})
			// A complex field has no key to read (keySequence refuses
			// it), so its text is not kept: every open complex field
			// keeping all the text below it was depth times the text.
			if fi.text == nil && !f.complexField() {
				fi.text = &strings.Builder{}
			}
		}
	}
	for _, a := range attrs {
		if fd.n++; fd.n == 1 {
			fd.key = v.keyString(a)
			if _, ok := v.untypedAttrs[a]; ok {
				fd.untyped = a.Name().Local
			}
		}
		if a.Parent() == nil {
			delete(v.attrOwner, a) // selectAttrs' stand-in for a defaulted value
		}
	}
}

// complexField reports whether f's element, as a field node, has no simple
// value (complexTyped). A nilled element's content was not assessed.
func (f *svFrame) complexField() bool {
	return f.ct != nil && f.kind != svNil && f.ct.Content != ContentSimple
}

// icText is text the element at frame x has directly; stripped says it was
// whitespace the stream stripped, whose characters it did not see.
func (s *streamV) icText(x int, value string, stripped bool) {
	for i := 0; i <= x && i < len(s.frames); i++ {
		if fi := s.frames[i].ic; fi != nil && fi.text != nil {
			fi.text.WriteString(value)
			fi.stripped = fi.stripped || stripped
		}
	}
}

// icEnd is the element at frame x's end tag, after its content and its
// value were validated: the fields it is a node of take its key, its
// targets are keyed, a scope checks its constraints, and its tables go to
// its parent.
func (s *streamV) icEnd(f *svFrame, x int) {
	v, fi := s.v, f.ic
	defer clear(v.keyValues)
	if fi == nil {
		return
	}
	if fi.decl != nil {
		if v.icScopes--; v.icScopes == 0 {
			defer func() { s.icToks = nil }()
		}
	}
	for _, r := range fi.fieldOf {
		fd := &r.t.fields[r.i]
		if fd.complex = f.complexField(); fd.complex {
			continue
		}
		if _, ok := v.keyValues[f.el]; ok {
			fd.key = v.keyString(f.el)
		} else if fi.stripped {
			v.fail(f.el, "", "streaming: whitespace stripped before validation is part of a key")
			return
		} else {
			fd.key = strings.TrimSpace(fi.text.String())
		}
	}
	for _, t := range fi.targets {
		k := s.keySequence(f, t)
		for _, si := range t.scopes {
			sc := s.frames[si].ic
			sc.own[t.ic] = append(sc.own[t.ic], k)
		}
	}
	tables := fi.tables
	if fi.scope {
		if tables == nil {
			tables = icTables{}
		}
		seqs := map[*xdm.Node]svKeyed{}
		selected := func(c *IdentityConstraint) []*xdm.Node {
			var out []*xdm.Node
			for _, k := range fi.own[c] {
				seqs[k.tok] = k
				out = append(out, k.tok)
			}
			return out
		}
		for _, c := range fi.decl.IdentityConstraints {
			if c.Kind != ICKeyref {
				tables[c] = v.fillNodeTable(c, tables[c], selected(c),
					func(n *xdm.Node, _ *IdentityConstraint) ([]string, bool, bool) {
						k := seqs[n]
						return k.seq, k.complete, k.valid
					})
			}
		}
		for _, c := range fi.decl.IdentityConstraints {
			if c.Kind == ICKeyref {
				tables[c] = v.fillKeyref(c, tables, selected(c),
					func(n *xdm.Node, _ *IdentityConstraint) (string, bool, bool) {
						k := seqs[n]
						if k.valid && k.complete {
							return joinKeySequence(k.seq), true, true
						}
						return "", k.complete, k.valid
					})
			}
		}
		for _, c := range fi.decl.IdentityConstraints {
			s.need(c, -1)
		}
	}
	// validateElement returns the tables to a parent walking its content,
	// and validateChild those of a child it assessed with no declaration.
	if x == 0 || (f.typ == nil && f.kind != svNil) || len(tables) == 0 {
		return
	}
	switch p := &s.frames[x-1]; p.kind {
	case svElems, svEmptyElems, svMixed, svOpen:
		up := icTables{}
		for c, t := range tables {
			if s.needs[c] > 0 {
				up[c] = t
			}
		}
		if len(up) > 0 {
			pi := p.icFrame()
			pi.tables = mergeTables([]icTables{pi.tables, up})
		}
	}
}

// keySequence is keySequence for the target f's element is of t.
func (s *streamV) keySequence(f *svFrame, t *svTarget) svKeyed {
	v, ic := s.v, t.ic
	k := svKeyed{tok: s.token(f), valid: true}
	for _, fd := range t.fields {
		switch {
		case fd.n == 0:
			return svKeyed{tok: k.tok, valid: true}
		case fd.n > 1:
			v.fail(k.tok, "cvc-identity-constraint.3",
				"%s %q: a field selects %d nodes, want at most one", ic.Kind, ic.Name.Local, fd.n)
			return svKeyed{tok: k.tok}
		case fd.complex:
			v.fail(k.tok, "cvc-identity-constraint.3",
				"%s %q: a field selects an element with a complex type, which has no simple value",
				ic.Kind, ic.Name.Local)
			return svKeyed{tok: k.tok}
		case fd.untyped != "":
			v.fail(k.tok, "cvc-identity-constraint.3",
				"%s %q: a field selects attribute %s, which a wildcard "+
					"matched without assessing, so it has no simple type",
				ic.Kind, ic.Name.Local, fd.untyped)
			return svKeyed{tok: k.tok}
		}
		k.seq = append(k.seq, fd.key)
	}
	k.complete = true
	return k
}

// need counts the open elements that read c's tables: those declaring it,
// and those declaring a keyref to it.
func (s *streamV) need(c *IdentityConstraint, d int) {
	if s.needs == nil {
		s.needs = map[*IdentityConstraint]int{}
	}
	s.needs[c] += d
	if c.Kind == ICKeyref && c.Refer != nil {
		s.needs[c.Refer] += d
	}
}

// token is the node standing for f's element in tables: one record each,
// in document order, in a tree of tokens dropped whenever no scope is open
// (no table is then left to hold one).
func (s *streamV) token(f *svFrame) *xdm.Node {
	fi := f.icFrame()
	if fi.tok == nil {
		if s.icToks == nil {
			s.icToks = xdm.NewNode(xdm.KindElement, xdm.QName{Local: "keys"}, "")
		}
		fi.tok = s.icToks.AppendElement(f.el.Name())
	}
	return fi.tok
}
