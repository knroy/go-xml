package xsd

import (
	"cmp"
	"fmt"
	"slices"
	"sort"

	"github.com/knroy/go-xml/v2/xdm"
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
	// maxPositions bounds len(positions) while this model is being built.
	// It is per-model rather than global so that a Schema loaded with
	// Options.MaxContentModelPositions compiles every model — at load and
	// at validation alike — under the budget it was loaded with.
	maxPositions int
	// maxFollow, when positive, bounds every follow set while the model is
	// being built. Zero means unbounded; see compileForChecks.
	maxFollow int
	// maxEdges, when positive, bounds the follow representation the build
	// stores — pieces, plus the members each one holds that are not shared
	// with another — and size is how much it has stored so far.
	maxEdges, size int
	// shared leaves follow unmaterialized: the matcher reads pieces only.
	// It is the NewSequenceMatcher build, where a follow set may be as wide
	// as the model and materializing it is what makes a repeated choice
	// quadratic.
	shared bool

	// positions are the leaf terms, one per occurrence in the tree.
	positions []*position

	// first are the positions that may begin a match.
	first []int
	// follow[i] are the positions that may follow position i, in the order
	// their edges were laid down. It is the union of pieces[i], and is nil
	// for a shared model.
	follow [][]int
	// pieces[i] is follow[i] as disjoint runs, sorted by first member. A
	// run is often a whole first set shared with every other position
	// that loops back to it, which is what keeps (a1|...|an)* at O(n)
	// rather than n follow sets of n.
	pieces [][]piece
	// last are the positions at which a match may end.
	last []int

	// nullable records whether the model matches the empty sequence.
	nullable bool

	// counters are the repetition scopes, innermost last. A position
	// inside a bounded repetition belongs to one, and the runtime tracks a
	// count for each rather than duplicating states.
	counters []*counter

	// scopeFirst[c] and scopeLast[c] are the positions at which one
	// repetition of counter c may begin and end. They are what tells a
	// restart from a continuation, which cannot be read off the position
	// numbering once a group reference makes it non-monotonic.
	scopeFirst []map[int]bool
	scopeLast  []map[int]bool

	// active is the set of model groups on the path currently being built,
	// used to detect a group that reaches itself.
	active map[*ModelGroup]bool
}

// A piece is one run of a position's follow set: positions that may follow it,
// ascending, all laid down by one edge-laying step and so inside one
// repetition of the same scope.
//
// The scope is the counter the edges lie *inside* one repetition of: the
// innermost scope whose subtree was being built when they were laid down. A
// loop-back laid as c's own wraparound belongs to c's parent, not to c, and an
// edge outside every scope has -1.
//
// Only the innermost scope is stored. An edge joins two positions that both lie
// in that scope, so the innermost scope the two positions share — the only one
// the runtime ever asks about — is that scope exactly when the edge is inside
// it. Storing the edge once per enclosing scope instead held d copies of the
// same edges for a chain of d nested repeated group references: 6.5M entries
// and 216 MB for a 14.6 KB schema.
//
// scopeFirst and scopeLast alone cannot tell a restart from a continuation once
// every member of a repeated group is optional. A <sequence maxOccurs="3"> of
// three optional particles has all three positions in both sets, so the plain
// forward step a -> any inside one iteration reads as a restart and charges the
// outer counter. The edge that carries a step forward through the sequence is a
// different edge from the one that wraps around, even though the two can join
// the same pair of positions, so the edges are what has to be recorded.
type piece struct {
	to    []int
	scope int
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
	// emptiable records whether one repetition of this scope can match the
	// empty sequence — that is, whether the scope's *body*, ignoring the
	// scope's own occurrence bounds, is nullable.
	//
	// It is what lets the runtime credit an iteration that consumed
	// nothing. A particle is satisfied by partitioning the content into
	// between minOccurs and maxOccurs consecutive parts each matching the
	// term, and nothing in that rule requires a part to be non-empty: when
	// the term is emptiable an empty part is a legitimate one. Without
	// this, a count could only ever advance on a transition between two
	// matched positions, so a scope had no way to reach its minimum except
	// by consuming an element per iteration — and
	// <sequence 2..2> over <element c 0..2/> refused a single c while
	// accepting both zero and two of them.
	emptiable bool
}

// compileContentModel builds the automaton for a complex type's particle.
//
// limit bounds the number of positions; zero means
// DefaultMaxContentModelPositions. Callers holding a Schema pass its retained
// budget, so a model validates under the same limit its constraints were
// checked under — see Schema.maxPositions.
//
// Every follow set is bounded by maxUPAStateWidth, the width checkUPA already
// refuses at load, so a schema that loaded never reaches it.
func compileContentModel(p *Particle, limit int) (*contentModel, error) {
	return compileForChecks(p, limit, maxUPAStateWidth, 0)
}

// maxSequenceMatcherEdges bounds the follow representation a
// NewSequenceMatcher build stores: one per piece, plus each member a piece
// holds, counted once for a first set the pieces share.
//
// The DTD validator compiles through NewSequenceMatcher, and a DTD has no UPA
// gate, so the width bound XSD models live under would refuse legitimate DTDs:
// (a1|a2|...|a300)* has a follow width of 300, and TEI and DocBook have choices
// that wide. What has to be bounded is the total. <!ELEMENT r (a?,a?,...)> of
// 8192 members otherwise built ~n²/2 edges — 36 s and 4 GB for a 24 KB
// internal subset — before the position budget refused it, and again for
// every <r/>. Its follow sets are distinct suffixes, one piece per edge, so at
// 1<<20 it is still refused at about 1450 positions. A repeated choice is not:
// every alternative shares one piece, so (a1|...|an)* stores about 2n and
// compiles at any width the position budget admits.
//
// Every piece or copied member is one the old explicit build also stored as an
// edge, so the bound refuses nothing that build admitted.
const maxSequenceMatcherEdges = 1 << 20

// compileForChecks is compileContentModel with every follow set bounded by
// maxFollow and the follow representation by maxEdges (zero: unbounded; both
// unbounded only in tests). A positive maxEdges also builds the model shared,
// as NewSequenceMatcher wants it: follow is not materialized, and the matcher
// walks pieces. maxFollow applies only to a materialized model.
//
// checkUPA refuses any state wider than maxUPAStateWidth, and a dense model —
// a sequence of n optional elements — has follow sets that wide long before
// its positions run out: building it in full costs quadratic edges and memory
// (229 MB for n=2048) only for the width gate to refuse it in microseconds.
// Bounding the build at the same width stops at the first follow set the gate
// would refuse, so the refusal costs what the gate allows rather than what
// the model would.
func compileForChecks(p *Particle, limit, maxFollow, maxEdges int) (*contentModel, error) {
	if limit <= 0 {
		limit = maxPositions
	}
	m := &contentModel{active: map[*ModelGroup]bool{}, maxPositions: limit, maxFollow: maxFollow,
		maxEdges: maxEdges, shared: maxEdges > 0}
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
	// Pieces are disjoint runs, so ordering them by first member is what
	// lets innerEdge find the one run that can hold a target.
	for _, ps := range m.pieces {
		slices.SortFunc(ps, func(a, b piece) int { return cmp.Compare(a.to[0], b.to[0]) })
	}
	m.bindSiblings()
	return m, nil
}

// bindSiblings resolves ##definedSibling against the finished model.
//
// The keyword means "a name some other particle in this content model
// declares", so it cannot be answered while the tree is still being walked —
// a wildcard may precede the declarations it must exclude. Binding it here,
// once, also keeps it off the validation path: the alternative is re-walking
// the particle tree for every element checked against the wildcard.
//
// Substitution group members count as declared names. A wildcard that admitted
// a substitute while excluding its head would let the same element in through
// the back door, which is not what the schema author wrote.
func (m *contentModel) bindSiblings() {
	var wildcards []*Wildcard
	for _, pos := range m.positions {
		if w, ok := pos.term.(*Wildcard); ok && w.DisallowDefinedSibling {
			wildcards = append(wildcards, w)
		}
	}
	if len(wildcards) == 0 {
		return
	}
	names := map[xdm.QName]bool{}
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
	for _, w := range wildcards {
		// The map is shared: it is never written after this point, and
		// the same wildcard component may appear in more than one
		// model only when the models agree on its siblings.
		w.siblingNames = names
	}
}

// frag is the Glushkov data for one subtree: the positions that may start it,
// the positions that may end it, and whether it matches nothing.
//
// first is ascending: positions are numbered in the order the walk creates
// them, and each compositor lists its children's first sets in that order.
type frag struct {
	first    []int
	last     []int
	nullable bool
	// looped records that every last -> first edge is already laid down,
	// so a repetition wrapped directly around this subtree adds nothing.
	looped bool
}

// maxPositions is the default bound on the automaton size, used when no
// Schema-retained budget applies. Options.MaxContentModelPositions overrides
// it per load; DefaultMaxContentModelPositions documents the measurements.
//
// It is a MEMORY bound. The original justification recorded here — that the
// follow relation is quadratic in the number of positions — is no longer the
// binding one: that cost is a property of DENSE models, and maxUPAStateWidth
// (256) refuses those long before 8192 positions is reached. A dense sequence
// of 1024 optional elements is refused by the width gate in 68ms, never
// reaching this limit at all.
//
// What this bound still owns, and nothing else does, is the SPARSE model: wide,
// cheap to scan, and enormous. A group DAG of n groups each referencing the
// next twice is valid, acyclic, and a couple of kilobytes, yet expands to
// 2^(n-1) positions at a flat ~400 bytes each — 3.4 GB at n=24. Time alone
// makes that model look affordable (2.5s); memory is what makes it an attack.
// "An unbounded model is a way to be handed an unbounded allocation" was the
// right half of the original reasoning, and it is still exactly right.
//
// The gate is INCREMENTAL: it fires having already allocated in proportion to
// the limit, so the limit is what a hostile schema can actually reserve. That
// is why raising it is a grant of memory, not merely a relaxed refusal.
//
// The invariant on exceeding it: build returns an error, which means the
// procedure DECLINED to decide — explicitly NOT "no violation was found".
// Every caller must therefore take a conservative path. See
// TestContentModelBudgetSoundness in budget_soundness_test.go, which enforces this
// differentially by forcing the budget pathologically low.
//
// It is a var only so tests may lower it; it is never assigned in production.
var maxPositions = DefaultMaxContentModelPositions

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
		m.scopeFirst = append(m.scopeFirst, map[int]bool{})
		m.scopeLast = append(m.scopeLast, map[int]bool{})
		scope = len(m.counters) - 1
	}

	var inner frag
	var err error
	switch t := p.Term.(type) {
	case *ElementDecl, *Wildcard:
		if len(m.positions) >= m.maxPositions {
			return frag{}, fmt.Errorf(
				"content model has more than %d positions", m.maxPositions)
		}
		idx := len(m.positions)
		m.positions = append(m.positions, &position{
			term:     t,
			particle: p,
			counters: counterChain(m.counters, scope),
		})
		m.newPosition()
		inner = frag{first: []int{idx}, last: []int{idx}}

	case *ModelGroup:
		// A model group may reach itself: <xs:group name="expr"> whose
		// content references expr is how a schema describes a nested
		// structure, and it is legal. The particle tree is then a cyclic
		// graph, and Glushkov construction assumes a tree — following
		// the cycle recurses until the stack is gone, which is what real
		// schemas in the W3C suite actually do.
		//
		// A recursive content model is not a regular language, so no
		// finite automaton describes it. Refusing is the honest answer;
		// the alternative is to unroll to some arbitrary depth and
		// accept or reject documents based on where the unrolling
		// stopped.
		if m.active[t] {
			return frag{}, fmt.Errorf(
				"content model is recursive: a model group reaches itself, " +
					"which no finite automaton can describe")
		}
		m.active[t] = true
		inner, err = m.buildGroup(t, scope)
		delete(m.active, t)
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
	// The positions that begin and end one repetition are recorded so that
	// the runtime can tell a restart from a continuation.
	if scope != enclosing && scope >= 0 {
		// inner.nullable here is the body's own nullability: this
		// particle's minOccurs is not folded in until below, so an
		// emptiable body is recorded without being confused with a
		// scope that is merely optional.
		m.counters[scope].emptiable = inner.nullable
		for _, f := range inner.first {
			m.scopeFirst[scope][f] = true
		}
		for _, l := range inner.last {
			m.scopeLast[scope][l] = true
		}
	}
	if (p.MaxOccurs == Unbounded || p.MaxOccurs > 1) && !inner.looped {
		// The loop-back edges wrap around scope, so they lie inside
		// one repetition of the enclosing scope, not of this one.
		//
		// They are skipped when the body already loops: a chain of d
		// repeated group references each wrapping the last re-lays the
		// same |last| x |first| edges at every level, a dedup scan per
		// edge, for nothing.
		inner.looped = true
		if err := m.addFollow(inner.last, inner.first, enclosing, true); err != nil {
			return frag{}, err
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
		// by anything that starts this particle. No dedup: f's
		// positions were created by this particle, so no edge into them
		// from an earlier one exists yet.
		if err := m.addFollow(out.last, f.first, scope, false); err != nil {
			return frag{}, err
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
		out.looped = len(g.Particles) == 1 && f.looped
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
		out.looped = len(g.Particles) == 1 && f.looped
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
	// A group reference to an all group inside an all group contributes
	// its members directly; see flattenAll.
	for _, p := range flattenAll(g) {
		if len(m.positions) >= m.maxPositions {
			return frag{}, fmt.Errorf(
				"content model has more than %d positions", m.maxPositions)
		}
		idx := len(m.positions)
		m.positions = append(m.positions, &position{
			term:     p.Term,
			particle: p,
			counters: counterChain(m.counters, scope),
		})
		m.newPosition()
		out.first = append(out.first, idx)
		out.last = append(out.last, idx)
		if p.MinOccurs > 0 {
			out.nullable = false
		}
	}
	return out, nil
}

// newPosition makes room for the follow data of the position just appended.
func (m *contentModel) newPosition() {
	m.pieces = append(m.pieces, nil)
	if !m.shared {
		m.follow = append(m.follow, nil)
	}
}

// addFollow records that every position in to may follow each position in
// from, inside one repetition of scope. It refuses a follow set wider than
// m.maxFollow, or a representation past m.maxEdges, when those are set.
//
// A position whose follow set holds nothing of to yet gets to itself as a
// piece, shared with every other such position and charged once.
// That is the whole saving: every last position of a repeated choice of n
// loops back to the same first set, and copying it into each of them is n².
//
// dedup is needed only for loop-back edges, the one place an edge can be laid
// twice, and it costs no scan of the existing set. Glushkov first sets nest:
// to, a loop-back's first set, meets the positions of any subtree inside it in
// either that subtree's whole first set or nothing. Every piece a position
// already holds is a run of the first set of a subtree inside to, and spans
// only that subtree's positions, so the members of to that fall within an
// existing piece's span are exactly the duplicates, and the rest are new. They
// are split into runs at every existing span, not just the ones that remove a
// member, so a run never straddles another piece and innerEdge can find a
// target's run by its first member. A run short of the whole set is copied:
// sharing it would keep the whole set alive for a few members, and copying
// costs what the explicit build spent on the same edges.
//
// A duplicate keeps the scope it was first laid down in: a later copy can only
// come from an enclosing scope's loop-back, which is further out.
func (m *contentModel) addFollow(from []int, to []int, scope int, dedup bool) error {
	set := to[:len(to):len(to)]
	if len(set) == 0 {
		return nil
	}
	charged := false
	for _, l := range from {
		var runs [][]int
		if dedup {
			runs = splitRuns(set, m.pieces[l])
		} else {
			runs = [][]int{set}
		}
		for _, r := range runs {
			cost := 1
			if len(r) < len(set) {
				r = append([]int(nil), r...)
				cost += len(r)
			} else if !charged {
				charged = true
				cost += len(r)
			}
			if m.size += cost; m.maxEdges > 0 && m.size > m.maxEdges {
				return fmt.Errorf(
					"content model has more than %d follow entries", m.maxEdges)
			}
			m.pieces[l] = append(m.pieces[l], piece{to: r, scope: scope})
			if m.shared {
				continue
			}
			m.follow[l] = append(m.follow[l], r...)
			if m.maxFollow > 0 && len(m.follow[l]) > m.maxFollow {
				return fmt.Errorf(
					"a state of the content model has more than %d competing positions",
					m.maxFollow)
			}
		}
	}
	return nil
}

// splitRuns returns the runs of set, ascending, that lie outside the span of
// every existing piece, split at each one.
func splitRuns(set []int, existing []piece) [][]int {
	if len(existing) == 0 {
		return [][]int{set}
	}
	cuts := make([][2]int, len(existing))
	for i, p := range existing {
		cuts[i] = [2]int{sort.SearchInts(set, p.to[0]), sort.SearchInts(set, p.to[len(p.to)-1]+1)}
	}
	slices.SortFunc(cuts, func(a, b [2]int) int { return cmp.Compare(a[0], b[0]) })
	var out [][]int
	at := 0
	for _, c := range cuts {
		if c[0] > at {
			out = append(out, set[at:c[0]])
		}
		at = max(at, c[1])
	}
	if at < len(set) {
		out = append(out, set[at:])
	}
	return out
}

// innerEdge reports whether the edge from -> to was laid down inside one
// repetition of counter c. c must be the innermost counter the two positions
// share; see piece.
func (m *contentModel) innerEdge(c, from, to int) bool {
	ps := m.pieces[from]
	i := sort.Search(len(ps), func(i int) bool { return ps[i].to[0] > to }) - 1
	if i < 0 || ps[i].scope != c {
		return false
	}
	_, ok := slices.BinarySearch(ps[i].to, to)
	return ok
}

// matches reports whether a position's term accepts an element name.
//
// defined answers ##defined for a wildcard term, and may be nil when no
// wildcard in the model uses it.
func (p *position) matches(name xdm.QName, defined func(xdm.QName) bool) bool {
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
		return t.AllowsName(name, defined)
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
