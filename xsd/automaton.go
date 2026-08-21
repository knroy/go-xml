package xsd

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// A contentModel is a compiled particle tree: the finite automaton that decides
// whether a sequence of element children matches.
//
// The construction is Glushkov's — a position automaton, where each *occurrence*
// of a term in the particle tree is its own state. Two properties make it the
// right choice here rather than a Thompson NFA with epsilon transitions:
//
// First, the states are the positions, so a transition names the particle it
// came from. Unique Particle Attribution asks whether the particle to validate
// an item against can be determined without lookahead, and that question is only
// answerable if the automaton remembers which particle each transition belongs
// to. Labelling transitions by element name instead — the obvious shortcut —
// discards exactly the information UPA is about, and determinising such an
// automaton silently resolves the ambiguity the check exists to find.
//
// Second, a Glushkov automaton is deterministic precisely when the expression is
// one-unambiguous, which is what UPA requires. So the UPA check is not a
// separate analysis: it is the observation that two transitions out of one state
// can match the same item.
//
// Occurrence bounds are *not* unrolled. Unrolling makes the automaton linear in
// the numeric value of maxOccurs, which is exponential in the size of the schema
// text — maxOccurs="100000000" is nine characters and a hundred million states.
// Both Xerces and Saxon hit this (XERCESJ-1227 is still open) and both moved to
// runtime counters. Counters are what this uses.
type contentModel struct {
	// positions are the leaf terms, one per occurrence in the tree.
	positions []*position

	// first are the positions that may begin a match.
	first []int
	// follow[i] are the positions that may follow position i.
	follow [][]int
	// last are the positions at which a match may end.
	last []int

	// nullable records whether the model matches the empty sequence.
	nullable bool

	// counters are the repetition scopes, innermost last. A position
	// inside a bounded repetition belongs to one, and the runtime tracks a
	// count for each rather than duplicating states.
	counters []*counter
}

// A position is one occurrence of an element declaration or wildcard in a
// content model.
//
// It is not the same thing as the particle: a particle with maxOccurs="3" is one
// particle and one position, matched three times. But two references to the same
// named group produce two positions, because erratum E1-29 makes them distinct
// particles for UPA.
type position struct {
	// term is the element declaration or wildcard this position matches.
	term Term
	// particle is the particle the term came from, which UPA compares.
	particle *Particle
	// counters are the repetition scopes containing this position,
	// outermost first.
	counters []int
}

// A counter bounds how many times a repeated particle may match.
type counter struct {
	min, max int
	// parent is the enclosing counter, or -1.
	parent int
}

// compileContentModel builds the automaton for a complex type's particle.
func compileContentModel(p *Particle) (*contentModel, error) {
	m := &contentModel{}
	if p == nil {
		m.nullable = true
		return m, nil
	}
	f, err := m.build(p, -1)
	if err != nil {
		return nil, err
	}
	m.first = f.first
	m.last = f.last
	m.nullable = f.nullable
	return m, nil
}

// frag is the Glushkov data for one subtree: the positions that may start it,
// the positions that may end it, and whether it matches nothing.
type frag struct {
	first    []int
	last     []int
	nullable bool
}

// maxPositions bounds the automaton size.
//
// A content model large enough to exceed this is either generated or hostile.
// The bound exists because the follow relation is quadratic in the number of
// positions, so an unbounded model is a way to be handed an unbounded
// allocation — the failure Xerces and Saxon both hit.
const maxPositions = 8192

// build walks a particle, adding positions and follow edges, and returns the
// fragment data for it.
func (m *contentModel) build(p *Particle, enclosing int) (frag, error) {
	if p == nil {
		return frag{nullable: true}, nil
	}

	// A repetition scope is introduced only where it is needed: a particle
	// occurring exactly once needs no counter, and most do.
	scope := enclosing
	if p.MinOccurs != 1 || p.MaxOccurs != 1 {
		m.counters = append(m.counters, &counter{
			min: p.MinOccurs, max: p.MaxOccurs, parent: enclosing,
		})
		scope = len(m.counters) - 1
	}

	var inner frag
	var err error
	switch t := p.Term.(type) {
	case *ElementDecl, *Wildcard:
		if len(m.positions) >= maxPositions {
			return frag{}, fmt.Errorf(
				"content model has more than %d positions", maxPositions)
		}
		idx := len(m.positions)
		m.positions = append(m.positions, &position{
			term:     t,
			particle: p,
			counters: counterChain(m.counters, scope),
		})
		m.follow = append(m.follow, nil)
		inner = frag{first: []int{idx}, last: []int{idx}}

	case *ModelGroup:
		inner, err = m.buildGroup(t, scope)
		if err != nil {
			return frag{}, err
		}

	case nil:
		// An unresolved reference; the parser has already reported it.
		return frag{nullable: true}, nil

	default:
		return frag{}, fmt.Errorf("unexpected particle term %T", t)
	}

	// A repeatable particle may follow itself, which is what replaces
	// unrolling: the cycle is in the automaton and the count is at runtime.
	if p.MaxOccurs == Unbounded || p.MaxOccurs > 1 {
		for _, l := range inner.last {
			m.addFollow(l, inner.first)
		}
	}
	if p.MinOccurs == 0 {
		inner.nullable = true
	}
	return inner, nil
}

// counterChain returns the counter indices containing a scope, outermost first.
func counterChain(counters []*counter, scope int) []int {
	if scope < 0 {
		return nil
	}
	var out []int
	for i := scope; i >= 0; i = counters[i].parent {
		out = append(out, i)
		if counters[i].parent < 0 {
			break
		}
	}
	// Reverse into outermost-first order, which is how the runtime resets
	// them: entering an outer repetition resets the inner counts.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// buildGroup handles sequence, choice and all.
func (m *contentModel) buildGroup(g *ModelGroup, scope int) (frag, error) {
	switch g.Compositor {
	case CompositorSequence:
		return m.buildSequence(g, scope)
	case CompositorChoice:
		return m.buildChoice(g, scope)
	case CompositorAll:
		// An all group is not compiled to an automaton. XSD 1.0 confines
		// it to the whole content model, unrepeated, containing only
		// element particles with maxOccurs at most 1 (All Group
		// Limited), and those restrictions are exactly what make a
		// seen-set check sound. Compiling it to a Glushkov automaton
		// would need every interleaving, which is factorial.
		return m.buildAll(g, scope)
	}
	return frag{}, fmt.Errorf("unknown compositor %v", g.Compositor)
}

func (m *contentModel) buildSequence(g *ModelGroup, scope int) (frag, error) {
	out := frag{nullable: true}
	for _, p := range g.Particles {
		f, err := m.build(p, scope)
		if err != nil {
			return frag{}, err
		}
		// Everything that could end the sequence so far may be followed
		// by anything that starts this particle.
		for _, l := range out.last {
			m.addFollow(l, f.first)
		}
		if out.nullable {
			out.first = append(out.first, f.first...)
		}
		if f.nullable {
			out.last = append(out.last, f.last...)
		} else {
			out.last = append([]int(nil), f.last...)
		}
		out.nullable = out.nullable && f.nullable
	}
	return out, nil
}

func (m *contentModel) buildChoice(g *ModelGroup, scope int) (frag, error) {
	var out frag
	if len(g.Particles) == 0 {
		// An empty choice matches nothing at all — not even the empty
		// sequence. It is legal to write and makes the content model
		// unsatisfiable, which is the author's business.
		return frag{}, nil
	}
	for _, p := range g.Particles {
		f, err := m.build(p, scope)
		if err != nil {
			return frag{}, err
		}
		out.first = append(out.first, f.first...)
		out.last = append(out.last, f.last...)
		out.nullable = out.nullable || f.nullable
	}
	return out, nil
}

// buildAll records an all group's particles as positions without follow edges.
//
// The absence of follow edges is what marks it: matchAll uses the positions
// directly, and the automaton walk is never entered for a type whose content
// model is an all group.
func (m *contentModel) buildAll(g *ModelGroup, scope int) (frag, error) {
	var out frag
	out.nullable = true
	for _, p := range g.Particles {
		if len(m.positions) >= maxPositions {
			return frag{}, fmt.Errorf(
				"content model has more than %d positions", maxPositions)
		}
		idx := len(m.positions)
		m.positions = append(m.positions, &position{
			term:     p.Term,
			particle: p,
			counters: counterChain(m.counters, scope),
		})
		m.follow = append(m.follow, nil)
		out.first = append(out.first, idx)
		out.last = append(out.last, idx)
		if p.MinOccurs > 0 {
			out.nullable = false
		}
	}
	return out, nil
}

// addFollow records that every position in to may follow from.
func (m *contentModel) addFollow(from int, to []int) {
	existing := m.follow[from]
	for _, t := range to {
		dup := false
		for _, e := range existing {
			if e == t {
				dup = true
				break
			}
		}
		if !dup {
			existing = append(existing, t)
		}
	}
	m.follow[from] = existing
}

// matches reports whether a position's term accepts an element name.
func (p *position) matches(name xdm.QName) bool {
	switch t := p.term.(type) {
	case *ElementDecl:
		if t.Name == name {
			return true
		}
		// A declaration is matched by every member of its substitution
		// group, which is why the closure has to be computed before any
		// document is validated.
		for _, sub := range t.substitutable {
			if sub.Name == name && !sub.Abstract {
				return true
			}
		}
		return false
	case *Wildcard:
		return t.Allows(name.URI)
	}
	return false
}

// resolveDecl returns the element declaration a position matches a name with,
// which for a substitution group is the substituting declaration rather than
// the head.
func (p *position) resolveDecl(name xdm.QName) *ElementDecl {
	t, ok := p.term.(*ElementDecl)
	if !ok {
		return nil
	}
	if t.Name == name {
		return t
	}
	for _, sub := range t.substitutable {
		if sub.Name == name {
			return sub
		}
	}
	return nil
}
