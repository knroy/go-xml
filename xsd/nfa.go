package xsd

import "errors"

// The content-model runtime: a subset construction over the counter state.
//
// The automaton compiled in automaton.go is a Glushkov position automaton with
// runtime counters standing in for occurrence bounds, because unrolling the
// bounds is exponential in the size of the schema text. Counters buy that back
// at a price: a Glushkov automaton is deterministic on the positions, but the
// counter update for a transition is *not* determined by the positions alone.
//
// The reason is that occurrence scopes nest. A step from position p to position
// q leaves every scope of p that q is not in, enters every scope of q that p is
// not in, and — this is the ambiguous part — either continues or restarts the
// scopes the two share. Which shared scope repeats is a genuine choice: when a
// repeated group holds a single repeating particle, the group's FIRST and LAST
// positions coincide, so one edge is at once the particle's own repeat edge and
// the group's wraparound. Both readings are legal attributions; only the rest of
// the input says which one the document meant.
//
// The previous runtime chose one path and arbitrated the counters with
// heuristics, tracking a low and a high reading of each count independently.
// That admits a document when *different* readings satisfy different bounds
// though no single reading satisfies all of them, and refuses one when a
// consistent reading exists that no single heuristic path finds.
// <sequence 5..5> over <element c 2..2/> is the smallest witness: ten c is the
// only valid document, and it was refused, while five c — which no reading
// admits — was accepted, so a minOccurs floor went silently unenforced.
//
// The fix is to stop choosing. A state here is a position together with a whole
// vector of counts, and the runtime carries the *set* of states the input can
// have reached. Counts in one vector belong to one execution by construction, so
// no bound can be met by a reading that another bound is not measured against.
// The branching is bounded and small: a transition's only freedom is how deep
// into the shared scope nesting the repetition happens, which is at most the
// nesting depth of the model, and states that agree on position and counts are
// merged. Ordinary schemas hold one or two states per step.
//
// Every counter is bounded in the vector by its own maxOccurs — an unbounded
// counter is capped at its minimum plus one, since past the minimum no further
// count is distinguishable — so the state space is finite. It is not
// *small* for a hostile schema, which is why the walk carries a budget.

// DefaultMaxMatchStates bounds the number of simultaneous content-model states
// the validator will carry while matching one element's children.
//
// The set is the subset construction over the counter vectors, and its size is
// what makes the matcher exact rather than heuristic. On real schemas it stays
// at one or two: UBL 2.1, DocBook and both W3C suites never exceed a handful.
// A schema deep in nested repetitions with coinciding boundary positions can in
// principle make it grow, and a validator that grows without a ceiling is a way
// to be handed an unbounded allocation by a document being validated. Exceeding
// it is reported as an error rather than guessed at: a matcher that silently
// degraded to an approximation would do so precisely on the inputs where the
// answer was hardest to get.
const DefaultMaxMatchStates = 4096

// errMatchStates reports that a content model needed more simultaneous states
// than the budget allows. It is wrapped with the element's location by the
// caller that has one.
var errMatchStates = errors.New(
	"content model needs more simultaneous states than the limit allows; " +
		"the model nests occurrence bounds too deeply to decide")

// A matchState is one coherent execution of the automaton: the position last
// matched, and the repetition count of every scope as that execution reads them.
//
// counts is a fixed-width vector over m.counters, so a state is comparable and
// can be used as a map key directly. A scope not currently entered holds zero;
// leaving a scope resets it, which keeps the vector canonical so that two
// executions that have genuinely converged are merged rather than tracked twice.
type matchState struct {
	// pos is the position last matched, or -1 before anything has matched.
	pos int
	// counts is the repetition count of each scope, keyed by counter index.
	counts string
}

// stateSet is the set of executions consistent with the input so far.
//
// It is a map to deduplicate, and a slice to keep iteration order stable so
// that the position chosen for error reporting and for type annotation does not
// depend on map ordering.
type stateSet struct {
	seen  map[matchState]bool
	order []matchState
}

func newStateSet() *stateSet {
	return &stateSet{seen: map[matchState]bool{}}
}

func (s *stateSet) add(st matchState) {
	if s.seen[st] {
		return
	}
	s.seen[st] = true
	s.order = append(s.order, st)
}

func (s *stateSet) len() int { return len(s.order) }

// encodeCounts packs a count vector into a comparable string.
//
// A [] int cannot be a map key and an array cannot be sized at runtime, so the
// vector is carried as a string of bytes. Counts are bounded by capCount below,
// which keeps every one inside a byte for any model a schema can express: an
// occurrence bound larger than 254 is capped, because past a scope's maximum
// the exact count no longer changes any decision.
func encodeCounts(counts []int) string {
	if len(counts) == 0 {
		return ""
	}
	b := make([]byte, len(counts))
	for i, c := range counts {
		if c > 254 {
			c = 254
		}
		b[i] = byte(c)
	}
	return string(b)
}

func decodeCounts(s string, into []int) {
	for i := range into {
		into[i] = int(s[i])
	}
}

// reachable narrows every counter's maximum to what a document of n children
// can actually reach.
//
// A scope cannot repeat more often than there are children to fill it, so a
// maximum above n can never be broken and behaves exactly as
// maxOccurs="unbounded" does. Schemas write enormous bounds to mean "no
// practical limit" — the suite's particlesZ036 is a choice of 100,000 over a
// sequence of 100,000,000 over an unbounded element — and treating those as
// literal is what makes the vector set grow: every count below the bound stays
// distinguishable, so three nested scopes give every step three readings that
// never merge.
//
// The result is per-walk state and never written back into the compiled model,
// which is shared across goroutines and must stay immutable.
func reachable(m *contentModel, n int) []int {
	reach := make([]int, len(m.counters))
	for i, c := range m.counters {
		if c.max != Unbounded && c.max <= n {
			reach[i] = c.max
		} else {
			reach[i] = Unbounded
		}
	}
	return reach
}

// capCount clamps a count so that the state space stays finite and small.
//
// Two executions that differ only in a count no later decision can turn on are
// the same execution, and merging them is what keeps the vector set from
// growing combinatorially. A count matters for exactly two things: reaching the
// scope's minimum, and staying inside the maximum it can actually reach. Past
// the minimum with the maximum out of reach, the exact value cannot change any
// answer, so it stops being counted.
func capCount(c *counter, reach, n int) int {
	if reach == Unbounded {
		if n > c.min {
			return c.min + 1
		}
		return n
	}
	if n > reach {
		return reach
	}
	return n
}

// initialStates returns the states reachable before any child has matched.
func initialStates(m *contentModel) *stateSet {
	s := newStateSet()
	s.add(matchState{pos: -1, counts: encodeCounts(make([]int, len(m.counters)))})
	return s
}

// enterStates returns the states reached by matching the first child at
// position to.
func enterCounts(m *contentModel, reach []int, to int, buf []int) {
	for i := range buf {
		buf[i] = 0
	}
	for _, c := range m.positions[to].counters {
		buf[c] = capCount(m.counters[c], reach[c], 1)
	}
}

// stepCounts computes the count vectors reachable by taking the transition from
// position from to position to, appending each to out.
//
// The scopes of a position are its enclosing counters outermost first, and
// because occurrence scopes nest properly the scopes two positions share are a
// prefix of both chains. The step's only freedom is where in that shared prefix
// the repetition falls, so the alternatives are enumerated by split point:
//
//	split k < len(shared): shared[k] restarts. Everything outside it is
//	    continued untouched; everything inside it — the rest of the shared
//	    prefix and all of from's remaining scopes — is left, and to's
//	    remaining scopes are entered fresh.
//	split k = len(shared): no shared scope repeats. from's scopes past the
//	    prefix are left and to's are entered, which is an ordinary step
//	    forward across a scope boundary.
//
// Leaving a scope requires it to have met its minimum; restarting one requires
// it to have a repetition left, and requires from to be a position the scope can
// end at and to one it can begin at. The last split is available only when the
// step actually crosses a boundary, or when the compiler laid the edge down as
// one *within* a single repetition — otherwise a self-loop could be taken
// without charging any counter at all, which is how a repeated group's minimum
// went unenforced.
// The refusal reason is reported so the validator can name the right clause:
// a scope left before it met its minimum means the content so far is
// incomplete, which is a different complaint from a scope with no repetition
// left, and the spec spells them as different rules.
func stepCounts(m *contentModel, reach []int, from, to int, cur []int, out *[][]int) {
	stepCountsWhy(m, reach, from, to, cur, out, nil)
}

// stepCountsWhy is stepCounts with the diagnosis. When why is non-nil it is set
// to true if some alternative was refused because a scope had not yet met its
// minimum — that is, because the content so far is incomplete rather than
// over-full.
// satisfied reports whether a scope with count n may be left or ended at.
//
// The plain reading is n >= min. An emptiable scope has one more way to reach
// its minimum: a repetition of it may match nothing, and an iteration that
// matches nothing is still an iteration. XSD satisfies a particle by
// partitioning the content into between minOccurs and maxOccurs consecutive
// parts each matching the term, and when the term is nullable an empty part
// satisfies it, so the shortfall min-n can always be made up with empty parts.
//
// The maximum needs no corresponding relaxation: empty iterations are only ever
// added to reach a floor, and a reading that would exceed the ceiling can
// simply not add them.
func satisfied(c *counter, n int) bool {
	return n >= c.min || c.emptiable
}

func stepCountsWhy(m *contentModel, reach []int, from, to int, cur []int, out *[][]int, why *bool) {
	a := m.positions[from].counters
	b := m.positions[to].counters

	shared := 0
	for shared < len(a) && shared < len(b) && a[shared] == b[shared] {
		shared++
	}

	// leave applies the exit checks and resets for from's scopes at depth
	// >= d, reporting whether every one had met its minimum.
	leave := func(v []int, d int) bool {
		for _, c := range a[d:] {
			if !satisfied(m.counters[c], v[c]) {
				if why != nil {
					*why = true
				}
				return false
			}
			v[c] = 0
		}
		return true
	}
	enter := func(v []int, d int) {
		for _, c := range b[d:] {
			v[c] = capCount(m.counters[c], reach[c], 1)
		}
	}

	for k := 0; k <= shared; k++ {
		if k == shared {
			// No shared scope repeats: the step is a move forward
			// within the innermost scope the two positions share.
			// That reading is available only if the compiler
			// actually laid this edge down inside one repetition of
			// that scope. Without the check, the wraparound edge
			// the compiler added to make the scope repeatable could
			// be walked for free, and a repeated group's own
			// minOccurs and maxOccurs would never be charged at all.
			if shared > 0 && !m.scopeInner[a[shared-1]][[2]int{from, to}] {
				continue
			}
			next := append([]int(nil), cur...)
			if !leave(next, shared) {
				continue
			}
			enter(next, shared)
			*out = append(*out, next)
			continue
		}

		c := a[k]
		if !m.scopeLast[c][from] || !m.scopeFirst[c][to] {
			continue
		}
		if reach[c] != Unbounded && cur[c] >= reach[c] {
			continue
		}
		next := append([]int(nil), cur...)
		// Everything inside c is left behind, on both sides of the
		// transition: the scopes shared with to below depth k, and
		// from's own remaining ones.
		if !leave(next, k+1) {
			continue
		}
		for _, inner := range b[k+1:] {
			next[inner] = 0
		}
		next[c] = capCount(m.counters[c], reach[c], cur[c]+1)
		enter(next, k+1)
		*out = append(*out, next)
	}
}

// accepting reports whether an execution may end at its current position.
func accepting(m *contentModel, st matchState, buf []int) bool {
	if st.pos < 0 {
		return m.nullable
	}
	if !contains(m.last, st.pos) {
		return false
	}
	decodeCounts(st.counts, buf)
	for _, c := range m.positions[st.pos].counters {
		if !satisfied(m.counters[c], buf[c]) {
			return false
		}
	}
	// Scopes left behind were checked against their minimum on the way out
	// and reset to zero, so nothing outside the current chain can be short.
	return true
}

// candidates returns the positions reachable from a state, which is the
// automaton's follow set — the counters are what filters them, and that happens
// in stepCounts.
func candidates(m *contentModel, st matchState) []int {
	if st.pos < 0 {
		return m.first
	}
	return m.follow[st.pos]
}

// matchNames is the counter-exact walk, used by SequenceMatcher and by the
// UPA-independent callers that want only a yes-or-no.
//
// The validator does not go through here: it has to report which position
// matched each child, and it interleaves open content and child validation into
// the same walk, so it carries its own copy of this loop over in validate.go.
func matchNames(m *contentModel, match func(i, pos int) bool, n, budget int) (ok bool, at int, err error) {
	if len(m.positions) == 0 {
		return n == 0, 0, nil
	}
	reach := reachable(m, n)
	states := initialStates(m)
	cur := make([]int, len(m.counters))
	enter := make([]int, len(m.counters))
	var vecs [][]int

	for i := 0; i < n; i++ {
		next := newStateSet()
		for _, st := range states.order {
			for _, pos := range candidates(m, st) {
				if !match(i, pos) {
					continue
				}
				if st.pos < 0 {
					enterCounts(m, reach, pos, enter)
					next.add(matchState{pos: pos, counts: encodeCounts(enter)})
					continue
				}
				decodeCounts(st.counts, cur)
				vecs = vecs[:0]
				stepCounts(m, reach, st.pos, pos, cur, &vecs)
				for _, v := range vecs {
					next.add(matchState{pos: pos, counts: encodeCounts(v)})
				}
			}
		}
		if next.len() == 0 {
			return false, i, nil
		}
		if next.len() > budget {
			return false, i, errMatchStates
		}
		states = next
	}
	for _, st := range states.order {
		if accepting(m, st, cur) {
			return true, 0, nil
		}
	}
	return false, n, nil
}

// intsEqual compares two count vectors, which are always the same length.
func intsEqual(a, b []int) bool {
	for i, x := range a {
		if b[i] != x {
			return false
		}
	}
	return true
}
