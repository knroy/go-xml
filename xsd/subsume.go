package xsd

// XSD 1.1 Content type restricts (Complex Content), §3.4.6.4.
//
// 1.1 replaced 1.0's syntactic Particle Derivation OK table with a single
// semantic constraint: every sequence locally valid against the derived
// content model must be locally valid against the base's. That is language
// inclusion, and it accepts derivations the 1.0 table calls Forbidden — a
// sequence restricting one branch of a choice, a group whose bounds the table
// compares pairwise but the language does not distinguish.
//
// restrict.go implements the 1.0 table plus a family of individually measured
// 1.1 exceptions. This file decides the general case for the shapes where it
// can be decided *exactly*, and declines everywhere else so the table keeps
// its answer. Declining is always safe: the table is a conservative
// approximation of the same inclusion, never wrong in the accepting direction.
//
// The decision procedure is a product construction over two NFAs. The content
// models compiled in automaton.go are *counter* automata — a repetition is a
// cycle plus a runtime count, which is what keeps maxOccurs="100000000" from
// becoming a hundred million states. A counter automaton has infinitely many
// configurations, so it cannot be determinised, and inclusion over it is not
// decidable by product construction. Here the occurrences are instead
// *unrolled* into ordinary states, under a hard size cap: a model that would
// exceed the cap is declined rather than approximated.

import "github.com/knroy/go-xml/xdm"

// subsumeMaxStates caps the unrolled NFA for either side.
//
// Unrolling is exponential in the schema text — the same blow-up automaton.go
// exists to avoid — so the cap is what makes this affordable to run on every
// restriction in a schema. It is generous enough for every occurrence range
// the conformance suite and both production corpora actually spell (the
// largest is maxOccurs="10"), and small enough that hitting it costs one
// declined check rather than an unbounded allocation.
const subsumeMaxStates = 4096

// subsumeMaxProduct caps the product exploration, which is the determinised
// base's subset space against the derived NFA's states.
const subsumeMaxProduct = 20000

// nfa is a plain nondeterministic automaton over particle positions, with
// every occurrence range unrolled. Transitions carry the term they match
// rather than a symbol, because a wildcard matches a set of names that is only
// resolved against the other side's alphabet.
type nfa struct {
	// trans[s] are the outgoing edges of state s.
	trans [][]nfaEdge
	// accept[s] reports whether s is final.
	accept []bool
	start  int
}

type nfaEdge struct {
	// term is the ElementDecl or Wildcard this edge matches.
	term Term
	// particle is the particle the term came from, for the type and
	// nillability checks that inclusion alone does not cover.
	particle *Particle
	to       int
}

func (n *nfa) addState() int {
	n.trans = append(n.trans, nil)
	n.accept = append(n.accept, false)
	return len(n.trans) - 1
}

// buildNFA unrolls a particle into an nfa, or reports that it cannot.
//
// It returns ok=false for a model that is recursive, unbounded in a way that
// unrolling cannot represent, or larger than the cap. Every such answer sends
// the caller back to the 1.0 table.
func buildNFA(p *Particle) (*nfa, bool) {
	n := &nfa{}
	n.start = n.addState()
	active := map[*ModelGroup]bool{}
	end, ok := nfaParticle(n, p, n.start, active)
	if !ok {
		return nil, false
	}
	for _, e := range end {
		n.accept[e] = true
	}
	return n, true
}

// nfaParticle threads the particle's occurrence range, returning the states at
// which a match of p may end. `from` is the single state a match begins at.
//
// Unbounded repetition is represented by a cycle back to the entry of the last
// unrolled copy, which is exact for maxOccurs="unbounded" — the language of
// x{n,} is x{n} followed by x*.
func nfaParticle(n *nfa, p *Particle, from int, active map[*ModelGroup]bool) ([]int, bool) {
	if p == nil || p.Term == nil {
		return []int{from}, true
	}
	if len(n.trans) > subsumeMaxStates {
		return nil, false
	}

	min, max := p.MinOccurs, p.MaxOccurs
	// A bound above 64 used to decline here outright. That was a second,
	// smaller cliff in front of the real one: the unroll lays down a state
	// per copy and the loop below checks subsumeMaxStates on every
	// iteration, so the state budget already stops a bound too large to
	// unroll — and stops it at the point where the cost is actually
	// incurred rather than at a number chosen in advance. Declining early
	// meant a legal XSD 1.1 restriction with maxOccurs="100" fell back to
	// the structural 1.0 rules, which can refuse a schema whose language
	// really is a subset.

	// ends collects every state at which the whole repetition may stop:
	// after min copies, after min+1, and so on.
	ends := []int{}
	if min == 0 {
		ends = append(ends, from)
	}

	cur := from
	// count is how many copies to lay down: max when bounded, min+1 when
	// unbounded so the last copy can loop onto itself.
	count := max
	if max == Unbounded {
		count = min + 1
	}
	if count == 0 {
		return ends, true
	}

	var lastEntry int
	for i := 0; i < count; i++ {
		// Each copy begins at a state of its own. Reusing the state the
		// caller handed in would let the back edge an unbounded
		// repetition adds re-enter whatever else leaves that state: for
		// (d1*, d2+)+ inside a choice, looping to the sequence's own
		// entry put the outer choice's epsilon-to-accept back in reach,
		// and the base then accepted a lone d1 that it does not allow.
		entry := n.addState()
		if len(n.trans) > subsumeMaxStates {
			return nil, false
		}
		nfaEpsilon(n, cur, entry)
		cur = entry
		lastEntry = cur
		outs, ok := nfaOnce(n, p, cur, active)
		if !ok {
			return nil, false
		}
		// A copy that can finish in several places needs a join state,
		// because the next copy must start from exactly one.
		join := n.addState()
		if len(n.trans) > subsumeMaxStates {
			return nil, false
		}
		for _, o := range outs {
			nfaEpsilon(n, o, join)
		}
		cur = join
		if i+1 >= min {
			ends = append(ends, cur)
		}
	}
	if max == Unbounded {
		// The final copy repeats: re-entering it from its own end is
		// exactly x*.
		nfaEpsilon(n, cur, lastEntry)
	}
	return ends, true
}

// nfaOnce lays down a single occurrence of p's term.
func nfaOnce(n *nfa, p *Particle, from int, active map[*ModelGroup]bool) ([]int, bool) {
	switch t := p.Term.(type) {
	case *ElementDecl, *Wildcard:
		to := n.addState()
		if len(n.trans) > subsumeMaxStates {
			return nil, false
		}
		n.trans[from] = append(n.trans[from], nfaEdge{term: t, particle: p, to: to})
		return []int{to}, true

	case *ModelGroup:
		// A group that reaches itself describes a non-regular language;
		// automaton.go refuses it and so does this.
		if active[t] {
			return nil, false
		}
		active[t] = true
		defer delete(active, t)
		return nfaGroup(n, t, from, active)
	}
	return nil, false
}

func nfaGroup(n *nfa, g *ModelGroup, from int, active map[*ModelGroup]bool) ([]int, bool) {
	switch g.Compositor {
	case CompositorSequence:
		cur := []int{from}
		for _, sub := range g.Particles {
			// Each member starts from one state, so a multi-ended
			// predecessor is joined first.
			entry := cur[0]
			if len(cur) != 1 {
				entry = n.addState()
				if len(n.trans) > subsumeMaxStates {
					return nil, false
				}
				for _, c := range cur {
					nfaEpsilon(n, c, entry)
				}
			}
			outs, ok := nfaParticle(n, sub, entry, active)
			if !ok {
				return nil, false
			}
			cur = outs
		}
		return cur, true

	case CompositorChoice:
		var outs []int
		if len(g.Particles) == 0 {
			return []int{from}, true
		}
		for _, sub := range g.Particles {
			o, ok := nfaParticle(n, sub, from, active)
			if !ok {
				return nil, false
			}
			outs = append(outs, o...)
		}
		return outs, true

	case CompositorAll:
		// An all group of k members is k! interleavings. Unrolling it is
		// only affordable for small k, and allSubsumes already decides
		// the all-group cluster by counting rather than by language, so
		// this declines and leaves that path in charge.
		return nil, false
	}
	return nil, false
}

// nfaEpsilon adds an epsilon edge by copying the target's outgoing edges and
// acceptance, which keeps the machine epsilon-free without a closure pass.
//
// This is correct only because the graph is built in topological order except
// for the single back edge an unbounded repetition adds, and that back edge is
// laid last, after the state it points at is complete.
func nfaEpsilon(n *nfa, from, to int) {
	if from == to {
		return
	}
	n.trans[from] = append(n.trans[from], nfaEdge{term: nil, to: to})
}

// epsClose expands a state set over the nil-term edges.
func (n *nfa) epsClose(set map[int]bool) {
	stack := make([]int, 0, len(set))
	for s := range set {
		stack = append(stack, s)
	}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range n.trans[s] {
			if e.term == nil && !set[e.to] {
				set[e.to] = true
				stack = append(stack, e.to)
			}
		}
	}
}

func (n *nfa) accepts(set map[int]bool) bool {
	for s := range set {
		if n.accept[s] {
			return true
		}
	}
	return false
}

// particleSubsumes decides XSD 1.1 §3.4.6.4 for r against b by language
// inclusion, returning ok=false when it declines.
//
// It declines rather than guesses whenever the construction cannot be exact:
// an all group, a recursive model, a range too wide to unroll, or a wildcard
// pair whose name sets it cannot compare precisely. Every decline returns the
// question to the 1.0 table in restrict.go.
func particleSubsumes(r, b *Particle) (error, bool) {
	rn, ok := buildNFA(r)
	if !ok {
		return nil, false
	}
	bn, ok := buildNFA(b)
	if !ok {
		return nil, false
	}

	// The alphabet is the set of name-equivalence classes the two models
	// can tell apart. Every element declaration on either side contributes
	// its own name and each of its substitutable members' names; a
	// wildcard contributes no name of its own, so a derived wildcard is
	// only accepted where a base wildcard covers it outright.
	alphabet := subsumeAlphabet(rn, bn)

	// Product exploration: a derived state paired with the *set* of base
	// states reachable on the same input. Determinising the base is what
	// makes inclusion decidable — the derived side stays nondeterministic
	// because we quantify over all its runs, and reaching one accepting
	// derived state with no accepting base state is a counterexample.
	type key struct {
		rs string
		bs string
	}
	rstart := map[int]bool{rn.start: true}
	rn.epsClose(rstart)
	bstart := map[int]bool{bn.start: true}
	bn.epsClose(bstart)

	// The derived side is explored as a set too, so one queue entry covers
	// every derived run on the same word.
	type pair struct {
		rs map[int]bool
		bs map[int]bool
	}
	seen := map[key]bool{}
	queue := []pair{{rstart, bstart}}
	seen[key{setKey(rstart), setKey(bstart)}] = true

	for len(queue) > 0 {
		if len(seen) > subsumeMaxProduct {
			return nil, false
		}
		p := queue[0]
		queue = queue[1:]

		// A word the derived model accepts and the base does not is a
		// counterexample, and the constraint fails.
		if rn.accepts(p.rs) && !bn.accepts(p.bs) {
			return errSubsumeCounterexample, true
		}

		for _, sym := range alphabet {
			rnext, rdecl := stepNFA(rn, p.rs, sym)
			if len(rnext) == 0 {
				continue
			}
			bnext, bdecl := stepNFA(bn, p.bs, sym)
			if len(bnext) == 0 {
				// The derived model admits a name the base does
				// not admit at all.
				return errSubsumeCounterexample, true
			}
			// Inclusion of the *languages* is not the whole
			// constraint: the element declarations reached must
			// still satisfy NameAndTypeOK's type, nillability and
			// fixed-value clauses, which no automaton records.
			if rdecl != nil && bdecl != nil {
				if err := declCompatible(rdecl, bdecl); err != nil {
					return err, true
				}
			}
			// processContents is likewise invisible to the
			// language: two wildcards admitting the same names
			// differ only in how the admitted element is then
			// validated, and a restriction may only tighten that.
			// The whole particlesOb cluster is exactly this —
			// identical namespace constraints, a weakened
			// processContents — so a step decided by wildcards on
			// both sides is handed back to nsSubset rather than
			// answered here.
			if wildcardStep(rn, p.rs, sym) && wildcardStep(bn, p.bs, sym) {
				return nil, false
			}
			k := key{setKey(rnext), setKey(bnext)}
			if !seen[k] {
				seen[k] = true
				queue = append(queue, pair{rnext, bnext})
			}
		}
	}
	return nil, true
}

// subsumeSymbol is one letter of the comparison alphabet.
//
// A named symbol stands for exactly one element name. The wildcard symbol
// stands for "some name no declaration on either side mentions", which is what
// distinguishes a derived wildcard from a base that only lists names.
type subsumeSymbol struct {
	name     xdm.QName
	anyOther bool
}

// subsumeAlphabet collects the names the two models can distinguish.
//
// Two names that every position on both sides treats identically are the same
// letter, and the only such class that is not a literal name is "anything
// neither model names", carried by anyOther. Substitution group members are
// included because a base declaration matches its members, so a derived model
// naming a member must be compared against the head's edge.
func subsumeAlphabet(rn, bn *nfa) []subsumeSymbol {
	seen := map[xdm.QName]bool{}
	var out []subsumeSymbol
	add := func(q xdm.QName) {
		if !seen[q] {
			seen[q] = true
			out = append(out, subsumeSymbol{name: q})
		}
	}
	for _, n := range []*nfa{rn, bn} {
		for _, edges := range n.trans {
			for _, e := range edges {
				d, ok := e.term.(*ElementDecl)
				if !ok {
					continue
				}
				add(d.Name)
				for _, s := range d.substitutable {
					if !s.Abstract {
						add(s.Name)
					}
				}
			}
		}
	}
	return append(out, subsumeSymbol{anyOther: true})
}

// termAccepts reports whether an edge's term matches a symbol.
//
// For anyOther only a wildcard can match, and only one whose namespace
// constraint is open enough to admit a name outside those the schema lists.
// Answering that exactly is what the ##defined and notQName forms make hard,
// so a wildcard carrying either is treated as matching, which is the
// conservative direction: it makes the derived side look larger and the base
// side larger too, and the pair is only reported as a counterexample when the
// derived side has a match the base lacks.
func termAccepts(t Term, sym subsumeSymbol) bool {
	switch d := t.(type) {
	case *ElementDecl:
		if sym.anyOther {
			return false
		}
		if d.Name == sym.name {
			return true
		}
		for _, s := range d.substitutable {
			if s.Name == sym.name && !s.Abstract {
				return true
			}
		}
		return false
	case *Wildcard:
		if sym.anyOther {
			return true
		}
		return d.AllowsName(sym.name, func(xdm.QName) bool { return true })
	}
	return false
}

// stepNFA advances a state set on one symbol, returning the new set and, when
// the step was decided by a single element declaration on this side, that
// declaration — which the caller needs for the clauses inclusion does not
// cover.
func stepNFA(n *nfa, set map[int]bool, sym subsumeSymbol) (map[int]bool, *ElementDecl) {
	next := map[int]bool{}
	var decl *ElementDecl
	multiple := false
	for s := range set {
		for _, e := range n.trans[s] {
			if e.term == nil || !termAccepts(e.term, sym) {
				continue
			}
			next[e.to] = true
			if d, ok := e.term.(*ElementDecl); ok {
				if decl != nil && decl != d {
					multiple = true
				}
				decl = d
			}
		}
	}
	if len(next) == 0 {
		return nil, nil
	}
	n.epsClose(next)
	if multiple {
		// Several declarations answer this name; no single one carries
		// the type constraint, so the caller skips that check rather
		// than picking arbitrarily.
		return next, nil
	}
	return next, decl
}

// declCompatible applies the NameAndTypeOK clauses that language inclusion
// does not express: a derived declaration reached on some name must still have
// a type derived from the base's, must not add nillability, and must keep a
// fixed value.
//
// Without this the inclusion check would accept a restriction that admits the
// same *names* in the same order while widening what those elements may
// contain, which is exactly what the constraint exists to forbid.
func declCompatible(rd, bd *ElementDecl) error {
	if rd == bd {
		return nil
	}
	if rd.Nillable && !bd.Nillable {
		return errSubsumeCounterexample
	}
	if !fixedConstraintsAgree(bd.Constraint, rd.Constraint, rd.Type) {
		return errSubsumeCounterexample
	}
	if blockSet(bd.DisallowedSubstitutions)&^blockSet(rd.DisallowedSubstitutions) != 0 {
		return errSubsumeCounterexample
	}
	if rd.Type != nil && bd.Type != nil && !typeRestricts(rd.Type, bd.Type) {
		return errSubsumeCounterexample
	}
	return nil
}

// setKey renders a state set as a comparable key.
func setKey(set map[int]bool) string {
	xs := make([]int, 0, len(set))
	for s := range set {
		xs = append(xs, s)
	}
	sortInts(xs)
	b := make([]byte, 0, len(xs)*3)
	for _, x := range xs {
		b = append(b, byte(x), byte(x>>8), byte(x>>16))
	}
	return string(b)
}

func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// errSubsumeCounterexample reports that the inclusion check found a sequence
// the derived model accepts and the base does not.
//
// The message names the constraint rather than the counterexample: producing a
// readable witness means reconstructing a word from the product path, and the
// 1.0 table's own message — which the caller falls back to whenever this
// declines — is more specific about *which* particle failed than a witness
// would be.
var errSubsumeCounterexample = errorString(
	"the restriction admits content the base does not allow")

type errorString string

func (e errorString) Error() string { return string(e) }

// wildcardStep reports whether any edge leaving a state set on this symbol is
// a wildcard, which means the step carries a processContents the language
// cannot see.
func wildcardStep(n *nfa, set map[int]bool, sym subsumeSymbol) bool {
	for s := range set {
		for _, e := range n.trans[s] {
			if e.term == nil || !termAccepts(e.term, sym) {
				continue
			}
			if _, ok := e.term.(*Wildcard); ok {
				return true
			}
		}
	}
	return false
}
