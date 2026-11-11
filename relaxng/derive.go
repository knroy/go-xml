package relaxng

import (
	"hash/maphash"

	"github.com/knroy/go-xml/v2/xdm"
)

// The derivative algorithm.
//
// Validation asks one question repeatedly: given a pattern and the next item
// of input, what pattern must the *rest* of the input match? That is the
// derivative. When the input runs out, the document is valid exactly when the
// remaining pattern is nullable.
//
// The constructors below are not plain struct literals. Each one simplifies as
// it builds — a choice with a notAllowedPat branch is the other branch, a group
// with an emptyPat branch is the other branch — and that is what keeps the
// pattern from growing without bound as the derivative is taken over a long
// document. Without it the algorithm is correct and unusable.

// patBuilder interns the patterns one validation builds and remembers the
// derivatives the validator takes of them, after Jing (T23 in
// docs/profiling.md). Interning makes equal patterns one pointer, so a
// document that returns to a state -- every row of a table -- finds the
// derivative it took last time instead of rebuilding it. It belongs to one
// validation, so a Schema shared between goroutines shares no mutable state.
//
// A nil patBuilder interns and remembers nothing, which is how the compiler
// uses the same code; so does one whose tables are not made yet (on), which
// is a validation until memoAfter elements.
//
// Interning changes no pattern's shape: choice() merges exactly what patEq
// merges, so patternSize and MaxPatternSize see the patterns they saw before.
// (Merging on pointer identity would merge more -- patEq never equates
// patterns holding data -- and which pointers are equal depends on what
// earlier validations left in the memo points, so the bound would fire by
// history.) Patterns that came from another validation through the schema's
// memo points (memo.go) are not in this table, which only costs a miss.
type patBuilder struct {
	intern map[internKey]pattern
	memo   map[memoKey]pattern
	// closeM and endM remember the two derivatives that depend on the
	// pattern alone, so their lookups hash one pointer, not a memoKey.
	closeM, endM map[pattern]pattern
	ctxUsed      bool // the derivative consulted the namespace context; see att
	// maxPattern is ValidateOptions.MaxPatternSize as resolved, checked
	// between the tokens of a list (listDeriv); a negative value means no
	// bound. tooBig records that it fired.
	maxPattern int
	tooBig     bool
}

// on reports whether pb interns and remembers.
func (pb *patBuilder) on() bool { return pb != nil && pb.intern != nil }

type internKey struct {
	k    uint8
	l, r pattern
}

const (
	kChoice uint8 = iota
	kGroup
	kInterleave
	kAfter
	kOneOrMore
)

type memoKey struct {
	op   uint8
	p    pattern
	name xdm.QName
	s    string
}

const (
	opOpen uint8 = iota
	opAtt
)

// maxMemo bounds each table, so that a document of unique attribute values
// cannot grow them without bound; past it nothing new is kept.
// ponytail: a cap, not an LRU; the states a document returns to are met early.
const maxMemo = 1 << 16

func newPatBuilder() *patBuilder {
	pb := &patBuilder{maxPattern: -1}
	pb.startMemo()
	return pb
}

// startMemo makes the tables, from which point pb interns and remembers.
func (pb *patBuilder) startMemo() {
	pb.intern, pb.memo = map[internKey]pattern{}, map[memoKey]pattern{}
	pb.closeM, pb.endM = map[pattern]pattern{}, map[pattern]pattern{}
}

// mk returns the interned node; pb is not nil.
func (pb *patBuilder) mk(k uint8, l, r pattern) pattern {
	key := internKey{k, l, r}
	if p, ok := pb.intern[key]; ok {
		return p
	}
	var p pattern
	switch k {
	case kChoice:
		p = newChoicePat(l, r)
	case kGroup:
		p = newGroupPat(l, r)
	case kInterleave:
		p = newInterleavePat(l, r)
	case kAfter:
		p = newAfterPat(l, r)
	default:
		p = newOneOrMorePat(l)
	}
	if len(pb.intern) < maxMemo {
		pb.intern[key] = p
	}
	return p
}

func (pb *patBuilder) remember(k memoKey, v pattern) {
	if len(pb.memo) < maxMemo {
		pb.memo[k] = v
	}
}

func (pb *patBuilder) usedCtx() {
	if pb != nil {
		pb.ctxUsed = true
	}
}

// open, att, closeTag and end are the derivatives the validator takes,
// remembered by the state they are taken of.
func (pb *patBuilder) open(p pattern, name xdm.QName) pattern {
	if !pb.on() {
		return pb.startTagOpenDeriv(p, name)
	}
	k := memoKey{op: opOpen, p: p, name: name}
	if v, ok := pb.memo[k]; ok {
		return v
	}
	d := pb.startTagOpenDeriv(p, name)
	pb.remember(k, d)
	return d
}

// att remembers by the attribute's name and value. The namespace context is
// not in the key, so a derivative that consulted it (a QName value) is not
// remembered.
func (pb *patBuilder) att(p pattern, a attr, ctx nsContext) pattern {
	if !pb.on() {
		return pb.attDeriv(p, a, ctx)
	}
	k := memoKey{op: opAtt, p: p, name: a.name, s: a.value}
	if v, ok := pb.memo[k]; ok {
		return v
	}
	pb.ctxUsed = false
	d := pb.attDeriv(p, a, ctx)
	if !pb.ctxUsed {
		pb.remember(k, d)
	}
	return d
}

func (pb *patBuilder) closeTag(p pattern) pattern {
	if !pb.on() {
		return pb.startTagCloseDeriv(p)
	}
	if v, ok := pb.closeM[p]; ok {
		return v
	}
	d := pb.startTagCloseDeriv(p)
	if len(pb.closeM) < maxMemo {
		pb.closeM[p] = d
	}
	return d
}

func (pb *patBuilder) end(p pattern) pattern {
	if !pb.on() {
		return pb.endTagDeriv(p)
	}
	if v, ok := pb.endM[p]; ok {
		return v
	}
	d := pb.endTagDeriv(p)
	if len(pb.endM) < maxMemo {
		pb.endM[p] = d
	}
	return d
}

// The constructors and derivatives without a builder, for the compiler and
// the tests.
func choice(a, b pattern) pattern     { return (*patBuilder)(nil).choice(a, b) }
func group(a, b pattern) pattern      { return (*patBuilder)(nil).group(a, b) }
func interleave(a, b pattern) pattern { return (*patBuilder)(nil).interleave(a, b) }
func after(a, b pattern) pattern      { return (*patBuilder)(nil).after(a, b) }
func oneOrMore(p pattern) pattern     { return (*patBuilder)(nil).oneOrMore(p) }
func startTagOpenDeriv(p pattern, name xdm.QName) pattern {
	return (*patBuilder)(nil).startTagOpenDeriv(p, name)
}
func attDeriv(p pattern, a attr, ctx nsContext) pattern {
	return (*patBuilder)(nil).attDeriv(p, a, ctx)
}
func textDeriv(p pattern, s string, ctx nsContext) pattern {
	return (*patBuilder)(nil).textDeriv(p, s, ctx)
}
func startTagCloseDeriv(p pattern) pattern { return (*patBuilder)(nil).startTagCloseDeriv(p) }

func (pb *patBuilder) choice(a, b pattern) pattern { return pb.choiceIn(a, b, nil) }

// choiceIn is choice, with ix describing a's alternatives when a is the
// chain a fold is building (see altIndex); ix may be nil.
func (pb *patBuilder) choiceIn(a, b pattern, ix *altIndex) pattern {
	if _, ok := a.(notAllowedPat); ok {
		return b
	}
	if _, ok := b.(notAllowedPat); ok {
		return a
	}
	// A choice between a pattern and itself is that pattern. Without this the
	// derivative of a oneOrMore nested in a oneOrMore carries two copies of
	// the same continuation per item, so the pattern doubles with every
	// child: DocBook's xref.001, valid, hit the 100,000-node bound on that
	// alone. Equality is structural and conservative (see patEq), which
	// catches the copies the derivative makes. It stays structural under
	// patBuilder's interning; see there for why.
	if patEq(a, b) || inChoice(a, b, ix) {
		return a
	}
	// A choice on the right is merged alternative by alternative. Taking the
	// derivative of a choice of choices yields chains that share most of their
	// alternatives, and testing the whole chain against a only finds a chain
	// that is identical: (a?){13} a{13} then grew past the pattern bound while
	// holding a few dozen distinct alternatives.
	//
	// The merge compares every pair of alternatives, so it is skipped when
	// the chains are long enough to make that dearer than the copies it
	// saves: alternatives that never compare equal (values, data) would
	// otherwise buy a quadratic cost per choice from a hostile document.
	// ponytail: a fixed pair budget; a hash of the alternatives if a schema
	// needs more.
	if c, ok := b.(*choicePat); ok && altCount(a)*altCount(b) <= maxMergePairs {
		return pb.choiceIn(pb.choiceIn(a, c.Left, ix), c.Right, ix)
	}
	var p pattern
	if pb.on() {
		p = pb.mk(kChoice, a, b)
	} else {
		p = newChoicePat(a, b)
	}
	ix.extend(a, b, p)
	return p
}

// choiceFold returns choice for folding a list of alternatives left to
// right, as one altIndex.
func choiceFold() func(a, b pattern) pattern {
	ix := &altIndex{}
	return func(a, b pattern) pattern { return (*patBuilder)(nil).choiceIn(a, b, ix) }
}

// choiceMap is choice(f(c.Left), f(c.Right)) applied down the whole chain of
// choices at c, as each derivative of a choice is. The recursion rebuilt the
// chain one alternative at a time and inChoice walked what was built so far,
// O(K²) for K alternatives; folding through one altIndex makes it O(K), and
// iterating keeps the stack flat.
//
// The spine is followed through static wrappers (memo.go), which addMemoPoints
// puts on every link of a long chain: stopping at each would make every link
// a fold of its own. Their memos still answer at the top of the chain, where
// the derivative enters through the wrapper.
//
// f also reports whether it changed its alternative; when none changed, c is
// returned unbuilt, which startTagCloseDerivCh relies on.
func (pb *patBuilder) choiceMap(c *choicePat, f func(pattern) (pattern, bool)) (pattern, bool) {
	if _, ok := unstatic(c.Left).(*choicePat); !ok {
		l, chl := f(c.Left)
		r, chr := f(c.Right)
		if !chl && !chr {
			return c, false
		}
		return pb.choice(l, r), true
	}
	var spine []*choicePat
	p := pattern(c)
	for t, ok := p.(*choicePat); ok; t, ok = unstatic(p).(*choicePat) {
		spine = append(spine, t)
		p = t.Left
	}
	acc, changed := f(p)
	ds := make([]pattern, len(spine))
	for i := len(spine) - 1; i >= 0; i-- {
		d, ch := f(spine[i].Right)
		ds[i], changed = d, changed || ch
	}
	if !changed {
		return c, false
	}
	var ix altIndex
	for i := len(spine) - 1; i >= 0; i-- {
		acc = pb.choiceIn(acc, ds[i], &ix)
	}
	return acc, true
}

// maxMergePairs bounds the pairs of alternatives choice compares when it
// merges one chain into another.
const maxMergePairs = 1 << 10

// altCount is the number of alternatives in a chain of choices, fixed when
// the chain was built.
func altCount(p pattern) int {
	if c, ok := unstatic(p).(*choicePat); ok {
		return c.alts
	}
	return 1
}

// inChoice reports whether b is already one of a's alternatives. A chain of
// choices is built leftwards, so the alternatives are the Right of each link
// and the Left of the last. ix, when not nil, is the index of the chain
// being folded; a long chain is looked up there instead of walked.
func inChoice(a, b pattern, ix *altIndex) bool {
	if ix.covers(a) {
		return ix.has(b)
	}
	// Equal patterns have equal sizes, so a size mismatch skips the walk.
	sb := patternSize(b)
	for {
		c, ok := unstatic(a).(*choicePat)
		if !ok {
			return patternSize(a) == sb && patEq(a, b)
		}
		if patternSize(c.Right) == sb && patEq(c.Right, b) {
			return true
		}
		a = c.Left
	}
}

// altIndex holds the alternatives of the choice chain a fold is building,
// by patHash, so each new alternative is tested against the chain in O(1)
// rather than by walking it. Without it a K-way choice, compiled or derived,
// cost O(K²): 2.7 s to take one start-tag derivative of an 8000-way choice of
// values.
//
// It belongs to one fold, never to a pattern: schema patterns are shared by
// concurrent validations and are not written to. A chain shorter than
// minIndexed is walked, which is cheaper than indexing it.
type altIndex struct {
	of pattern // the chain the index describes; nil until built
	m  map[uint64][]pattern
}

const minIndexed = 16

// covers reports whether ix can answer for chain a, indexing a first when it
// is long enough and ix does not already describe it.
func (ix *altIndex) covers(a pattern) bool {
	if ix == nil {
		return false
	}
	if ix.of != nil && ix.of == a {
		return true
	}
	n := 0
	for p := a; n < minIndexed; n++ {
		c, ok := unstatic(p).(*choicePat)
		if !ok {
			return false
		}
		p = c.Left
	}
	ix.of, ix.m = a, map[uint64][]pattern{}
	for p := a; ; {
		c, ok := unstatic(p).(*choicePat)
		if !ok {
			ix.add(p)
			return true
		}
		ix.add(c.Right)
		p = c.Left
	}
}

func (ix *altIndex) add(p pattern) {
	if h := patHash(p); h != 0 {
		ix.m[h] = append(ix.m[h], p)
	}
}

// has reports whether b is patEq to an indexed alternative.
func (ix *altIndex) has(b pattern) bool {
	h := patHash(b)
	if h == 0 {
		return false
	}
	sb := patternSize(b)
	for _, q := range ix.m[h] {
		if patternSize(q) == sb && patEq(q, b) {
			return true
		}
	}
	return false
}

// extend records that p is the chain a with b appended.
func (ix *altIndex) extend(a, b, p pattern) {
	if ix != nil && ix.of != nil && ix.of == a {
		ix.add(b)
		ix.of = p
	}
}

// The kinds patHash mixes in beyond the interned ones.
const (
	kList = kOneOrMore + 1 + iota
	kElement
	kAttribute
	kRef
	kEmpty
	kText
	kNotAllowed
)

var hashSeed = maphash.MakeSeed()

// kindHash keys each kind by the process seed, so no pattern's hash is
// predictable from the schema alone.
var kindHash = func() (h [kNotAllowed + 1]uint64) {
	for k := range h {
		h[k] = maphash.Comparable(hashSeed, k)
	}
	return
}()

// patHash is a structural hash consistent with patEq: patterns patEq equates
// hash alike. Zero marks a pattern patEq equates with nothing -- it holds a
// value or data outside a definition reference -- which no index holds.
func patHash(p pattern) uint64 {
	switch t := p.(type) {
	case *choicePat:
		return t.hash
	case *groupPat:
		return t.hash
	case *interleavePat:
		return t.hash
	case *afterPat:
		return t.hash
	case *oneOrMorePat:
		return t.hash
	case *listPat:
		return t.hash
	case *elementPat:
		return mixHash(kElement, maphash.Comparable(hashSeed, t.Name), patHash(t.Pattern))
	case *attributePat:
		return mixHash(kAttribute, maphash.Comparable(hashSeed, t.Name), patHash(t.Pattern))
	case *refPat:
		if t.static != nil {
			return patHash(t.cached)
		}
		return mixHash(kRef, maphash.Comparable(hashSeed, t), 1)
	case *valuePat, *dataPat:
		return 0
	case emptyPat:
		return kindHash[kEmpty] | 1
	case textPat:
		return kindHash[kText] | 1
	}
	return kindHash[kNotAllowed] | 1
}

func hash2(k uint8, l, r pattern) uint64 { return mixHash(k, patHash(l), patHash(r)) }

// mixHash combines a kind with two operand hashes, in order. An operand of
// hash zero makes the result zero, as patEq is false for the whole when it is
// for a part.
func mixHash(k uint8, a, b uint64) uint64 {
	if a == 0 || b == 0 {
		return 0
	}
	h := fmix64(kindHash[k] ^ a)
	h = fmix64(h ^ (b*0x9e3779b97f4a7c15 + 0x632be59bd9b4e019))
	return h | 1
}

// fmix64 is MurmurHash3's finalizer.
func fmix64(h uint64) uint64 {
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}

// patEq reports whether two patterns are structurally equal. It may answer
// false for equal patterns but never true for different ones: values and
// data, whose fields hold maps and slices, always compare unequal, and a
// refPat is equal only to itself.
func patEq(a, b pattern) bool {
	// patHash is consistent with patEq, and is zero exactly where patEq
	// equates nothing, so it settles most comparisons at once -- and the
	// same pattern on both sides, which a derivative sharing subpatterns
	// meets at every level, without walking it once per path.
	ha := patHash(a)
	if ha != patHash(b) || ha == 0 {
		return false
	}
	if a == b {
		return true
	}
	a, b = unstatic(a), unstatic(b)
	switch x := a.(type) {
	case *choicePat:
		y, ok := b.(*choicePat)
		return ok && patEq(x.Left, y.Left) && patEq(x.Right, y.Right)
	case *groupPat:
		y, ok := b.(*groupPat)
		return ok && patEq(x.Left, y.Left) && patEq(x.Right, y.Right)
	case *interleavePat:
		y, ok := b.(*interleavePat)
		return ok && patEq(x.Left, y.Left) && patEq(x.Right, y.Right)
	case *afterPat:
		y, ok := b.(*afterPat)
		return ok && patEq(x.Left, y.Left) && patEq(x.Right, y.Right)
	case *oneOrMorePat:
		y, ok := b.(*oneOrMorePat)
		return ok && patEq(x.Pattern, y.Pattern)
	case *listPat:
		y, ok := b.(*listPat)
		return ok && patEq(x.Pattern, y.Pattern)
	case *elementPat:
		y, ok := b.(*elementPat)
		return ok && x.Name == y.Name && patEq(x.Pattern, y.Pattern)
	case *attributePat:
		y, ok := b.(*attributePat)
		return ok && x.Name == y.Name && patEq(x.Pattern, y.Pattern)
	case *valuePat, *dataPat:
		return false
	}
	// notAllowedPat, emptyPat, textPat and *refPat are comparable.
	return a == b
}

func (pb *patBuilder) group(a, b pattern) pattern {
	if _, ok := a.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if _, ok := b.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if _, ok := a.(emptyPat); ok {
		return b
	}
	if _, ok := b.(emptyPat); ok {
		return a
	}
	if pb.on() {
		return pb.mk(kGroup, a, b)
	}
	return newGroupPat(a, b)
}

func (pb *patBuilder) interleave(a, b pattern) pattern {
	if _, ok := a.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if _, ok := b.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if _, ok := a.(emptyPat); ok {
		return b
	}
	if _, ok := b.(emptyPat); ok {
		return a
	}
	if pb.on() {
		return pb.mk(kInterleave, a, b)
	}
	return newInterleavePat(a, b)
}

func (pb *patBuilder) after(a, b pattern) pattern {
	if _, ok := a.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if _, ok := b.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if pb.on() {
		return pb.mk(kAfter, a, b)
	}
	return newAfterPat(a, b)
}

func (pb *patBuilder) oneOrMore(p pattern) pattern {
	if _, ok := p.(notAllowedPat); ok {
		return notAllowedPat{}
	}
	if pb.on() {
		return pb.mk(kOneOrMore, p, nil)
	}
	return newOneOrMorePat(p)
}

// startTagOpenDeriv is the derivative with respect to an element's start tag.
//
// It descends into every branch that could admit the name, replacing the
// matching elementPat with an afterPat: the element's own content pattern, followed
// by whatever must come once that element closes. That pairing is what lets
// one recursion handle arbitrary nesting.
func (pb *patBuilder) startTagOpenDeriv(p pattern, name xdm.QName) pattern {
	if r, ok := p.(*refPat); ok {
		if d, ok := r.open.load(name); ok {
			return d
		}
		d := pb.startTagOpenDeriv(expand(r), name)
		r.open.store(name, d)
		return d
	}
	switch t := expand(p).(type) {
	case *choicePat:
		d, _ := pb.choiceMap(t, func(q pattern) (pattern, bool) { return pb.startTagOpenDeriv(q, name), true })
		return d
	case *elementPat:
		if !t.Name.contains(name) {
			return notAllowedPat{}
		}
		return pb.after(t.Pattern, emptyPat{})
	case *interleavePat:
		return pb.choice(
			pb.applyAfter(func(x pattern) pattern { return pb.interleave(x, t.Right) },
				pb.startTagOpenDeriv(t.Left, name)),
			pb.applyAfter(func(x pattern) pattern { return pb.interleave(t.Left, x) },
				pb.startTagOpenDeriv(t.Right, name)))
	case *oneOrMorePat:
		return pb.applyAfter(
			func(x pattern) pattern {
				return pb.group(x, pb.choice(pb.oneOrMore(t.Pattern), emptyPat{}))
			},
			pb.startTagOpenDeriv(t.Pattern, name))
	case *groupPat:
		d := pb.applyAfter(func(x pattern) pattern { return pb.group(x, t.Right) },
			pb.startTagOpenDeriv(t.Left, name))
		if t.Left.nullable() {
			return pb.choice(d, pb.startTagOpenDeriv(t.Right, name))
		}
		return d
	case *afterPat:
		return pb.applyAfter(func(x pattern) pattern { return pb.after(x, t.Right) },
			pb.startTagOpenDeriv(t.Left, name))
	}
	return notAllowedPat{}
}

// maxOpenMemo bounds each nameMemo.
// ponytail: per-definition cap, not a global LRU; enough for any real
// vocabulary, and past it the derivative is simply recomputed.
const maxOpenMemo = 1024

// expand resolves a refPat to the pattern it stands for.
//
// Every function that examines a pattern calls this first, so that a lazily
// compiled definition behaves exactly like the pattern it names. Expanding
// here rather than at compile time is what lets a definition refer to itself:
// the expansion happens once per level of nesting the document actually has,
// instead of unboundedly while compiling.
func expand(p pattern) pattern {
	for {
		r, ok := p.(*refPat)
		if !ok {
			return p
		}
		q, err := r.get()
		if err != nil {
			return notAllowedPat{}
		}
		p = q
	}
}

// applyAfter rewrites the continuation of every afterPat inside p.
//
// The derivative of a compound pattern has to remember what follows the
// element being opened, and that "what follows" lives in the right half of an
// afterPat. Rewriting it in place is what threads the context through without a
// separate stack.
func (pb *patBuilder) applyAfter(f func(pattern) pattern, p pattern) pattern {
	switch t := expand(p).(type) {
	case *afterPat:
		return pb.after(t.Left, f(t.Right))
	case *choicePat:
		d, _ := pb.choiceMap(t, func(q pattern) (pattern, bool) { return pb.applyAfter(f, q), true })
		return d
	case notAllowedPat:
		return notAllowedPat{}
	}
	return notAllowedPat{}
}

type attr struct {
	name  xdm.QName
	value string
}

func (pb *patBuilder) attDeriv(p pattern, a attr, ctx nsContext) pattern {
	var m refMemo
	return pb.attDerivM(p, a, ctx, &m)
}

// attDerivM is attDeriv with the references derived so far in this call.
func (pb *patBuilder) attDerivM(p pattern, a attr, ctx nsContext, m *refMemo) pattern {
	// A definition known to hold no attribute pattern (learnt by
	// startTagCloseDerivCh, which stops at the same element boundary)
	// derives to notAllowed for every attribute: every leaf does.
	if r, ok := p.(*refPat); ok {
		if r.attrFree.Load() {
			return notAllowedPat{}
		}
		if s := r.static; s != nil {
			if d, ok := s.att.load(a.name); ok && d != nil {
				return d
			} else if ok {
				return pb.attDerivM(r.cached, a, ctx, m)
			}
			d := pb.attDerivM(r.cached, a, ctx, m)
			keep := d
			if !anyValueFor(r.cached, a.name, map[*refPat]bool{}) {
				keep = nil
			}
			s.att.store(a.name, keep)
			return d
		}
		return m.derive(r, func(q pattern) pattern { return pb.attDerivM(q, a, ctx, m) })
	}
	switch t := expand(p).(type) {
	case *afterPat:
		return pb.after(pb.attDerivM(t.Left, a, ctx, m), t.Right)
	case *choicePat:
		d, _ := pb.choiceMap(t, func(q pattern) (pattern, bool) { return pb.attDerivM(q, a, ctx, m), true })
		return d
	case *groupPat:
		return pb.choice(
			pb.group(pb.attDerivM(t.Left, a, ctx, m), t.Right),
			pb.group(t.Left, pb.attDerivM(t.Right, a, ctx, m)))
	case *interleavePat:
		return pb.choice(
			pb.interleave(pb.attDerivM(t.Left, a, ctx, m), t.Right),
			pb.interleave(t.Left, pb.attDerivM(t.Right, a, ctx, m)))
	case *oneOrMorePat:
		return pb.group(pb.attDerivM(t.Pattern, a, ctx, m),
			pb.choice(pb.oneOrMore(t.Pattern), emptyPat{}))
	case *attributePat:
		if !t.Name.contains(a.name) || !pb.valueMatch(t.Pattern, a.value, ctx) {
			return notAllowedPat{}
		}
		return emptyPat{}
	}
	return notAllowedPat{}
}

// anyValueFor reports whether every attribute pattern in p that admits name,
// up to an element boundary, takes any value (its content is text), so that
// attDeriv of p for that name does not depend on the value or its context.
// A reference not yet resolved, or one being visited, answers false. seen,
// which must not be nil, holds each reference's answer for this walk, so a
// definition shared along many paths is walked once.
func anyValueFor(p pattern, name xdm.QName, seen map[*refPat]bool) bool {
	switch t := p.(type) {
	case *attributePat:
		if !t.Name.contains(name) {
			return true
		}
		_, ok := t.Pattern.(textPat)
		return ok
	case *choicePat:
		return anyValueFor(t.Left, name, seen) && anyValueFor(t.Right, name, seen)
	case *groupPat:
		return anyValueFor(t.Left, name, seen) && anyValueFor(t.Right, name, seen)
	case *interleavePat:
		return anyValueFor(t.Left, name, seen) && anyValueFor(t.Right, name, seen)
	case *oneOrMorePat:
		return anyValueFor(t.Pattern, name, seen)
	case *refPat:
		if t.attrFree.Load() {
			return true
		}
		if !t.done || t.err != nil {
			return false
		}
		if ok, done := seen[t]; done {
			return ok
		}
		seen[t] = false // being visited
		ok := anyValueFor(t.cached, name, seen)
		seen[t] = ok
		return ok
	}
	// Elements, values, data, lists, text, empty and notAllowed hold no
	// attribute pattern before an element boundary.
	return true
}

// valueMatch reports whether a string satisfies a pattern.
//
// An attribute value and a text node are both just a string, so the same
// question is asked of both. The empty string is special: it matches a
// nullable pattern, which is how <empty/> admits an absent value.
func (pb *patBuilder) valueMatch(p pattern, s string, ctx nsContext) bool {
	if p.nullable() && whitespaceOnly(s) {
		return true
	}
	return !isNotAllowed(pb.textDeriv(p, s, ctx))
}

func isNotAllowed(p pattern) bool {
	_, ok := p.(notAllowedPat)
	return ok
}

// textDeriv is the derivative with respect to a string of character data.
func (pb *patBuilder) textDeriv(p pattern, s string, ctx nsContext) pattern {
	var m refMemo
	return pb.textDerivM(p, s, ctx, &m)
}

// refMemo holds, for one derivative call, the derivative of each definition
// reference met: the input is the same throughout the call, so a definition
// the compiler shares along many paths is derived once. Without it a ladder
// of definitions d_i = d_{i-1} d_{i-1} was derived once per path, 2^N times.
type refMemo struct{ m map[*refPat]pattern }

// derive returns f of r's expansion, computing it once per call. It steps
// through r alone, so the static wrapper on the definition's body (memo.go)
// answers from its own memo.
func (rm *refMemo) derive(r *refPat, f func(pattern) pattern) pattern {
	if d, ok := rm.m[r]; ok {
		return d
	}
	q, err := r.get()
	if err != nil {
		q = notAllowedPat{}
	}
	d := f(q)
	if rm.m == nil {
		rm.m = map[*refPat]pattern{}
	}
	rm.m[r] = d
	return d
}

// textDerivM is textDeriv with the references derived so far in this call.
func (pb *patBuilder) textDerivM(p pattern, s string, ctx nsContext, m *refMemo) pattern {
	if r, ok := p.(*refPat); ok && r.static != nil && r.static.dataFree {
		if b := r.static.text.Load(); b != nil {
			return b.p
		}
		d := pb.textDerivM(r.cached, s, ctx, m)
		r.static.text.Store(&patBox{p: d})
		return d
	}
	if r, ok := p.(*refPat); ok && r.static == nil {
		return m.derive(r, func(q pattern) pattern { return pb.textDerivM(q, s, ctx, m) })
	}
	switch t := expand(p).(type) {
	case *choicePat:
		d, _ := pb.choiceMap(t, func(q pattern) (pattern, bool) { return pb.textDerivM(q, s, ctx, m), true })
		return d
	case *interleavePat:
		return pb.choice(
			pb.interleave(pb.textDerivM(t.Left, s, ctx, m), t.Right),
			pb.interleave(t.Left, pb.textDerivM(t.Right, s, ctx, m)))
	case *groupPat:
		d := pb.group(pb.textDerivM(t.Left, s, ctx, m), t.Right)
		if t.Left.nullable() {
			return pb.choice(d, pb.textDerivM(t.Right, s, ctx, m))
		}
		return d
	case *afterPat:
		return pb.after(pb.textDerivM(t.Left, s, ctx, m), t.Right)
	case *oneOrMorePat:
		return pb.group(pb.textDerivM(t.Pattern, s, ctx, m),
			pb.choice(pb.oneOrMore(t.Pattern), emptyPat{}))
	case textPat:
		// textPat consumes any amount of character data and remains itself,
		// which is what makes it match a run of text nodes.
		return t
	case *valuePat:
		// A qnamePat value is compared by what its prefix means on each side,
		// so the datatype is asked with both contexts when it has an opinion.
		if ct, ok := t.Type.(contextualType); ok {
			pb.usedCtx()
			if ct.equalIn(t.Value, nsContext{prefixes: t.Prefixes, dflt: t.Ns},
				s, ctx) {
				return emptyPat{}
			}
			return notAllowedPat{}
		}
		if t.Type.equal(t.Value, s) {
			return emptyPat{}
		}
		return notAllowedPat{}
	case *dataPat:
		var err error
		if ct, ok := t.Type.(contextualType); ok {
			pb.usedCtx()
			err = ct.checkIn(s, t.Params, ctx)
		} else {
			err = t.Type.check(s, t.Params)
		}
		if err != nil {
			return notAllowedPat{}
		}
		if t.Except != nil && pb.valueMatch(t.Except, s, ctx) {
			return notAllowedPat{}
		}
		return emptyPat{}
	case *listPat:
		return pb.listDeriv(t.Pattern, splitTokens(s), ctx)
	}
	return notAllowedPat{}
}

// listDeriv applies a pattern to the tokens of a whitespace-separated list.
//
// The pattern bound is checked before each token, for the reason
// validator.attsDeriv checks it before each attribute: a oneOrMore nested
// in a oneOrMore duplicates its operand on every token, and the result
// collapses to empty or notAllowed before any check outside this loop can
// see it. Measured, a 46-byte instance of twenty tokens cost 25 s and
// 224 MB with MaxPatternSize set to 1.
func (pb *patBuilder) listDeriv(p pattern, tokens []string, ctx nsContext) pattern {
	for _, tok := range tokens {
		if pb != nil && pb.maxPattern >= 0 && (pb.tooBig || patternSize(p) > pb.maxPattern) {
			pb.tooBig = true
			return notAllowedPat{}
		}
		p = pb.textDeriv(p, tok, ctx)
	}
	if p.nullable() {
		return emptyPat{}
	}
	return notAllowedPat{}
}

// startTagCloseDeriv discards the attribute patterns that were never matched.
//
// An attribute is optional in the sense that the pattern may offer one the
// document did not carry; reaching the end of the start tag with such a
// pattern still live means it went unused, and an unused attributePat cannot be
// satisfied later.
func (pb *patBuilder) startTagCloseDeriv(p pattern) pattern {
	q, _ := pb.startTagCloseDerivCh(p)
	return q
}

// startTagCloseDerivCh is startTagCloseDeriv reporting whether anything
// changed. A subtree holding no attributePat comes back as it went in, so it
// is not rebuilt -- and its choices not deduplicated again by choice() --
// once per element when there is nothing to discard.
func (pb *patBuilder) startTagCloseDerivCh(p pattern) (pattern, bool) {
	if r, ok := p.(*refPat); ok {
		if r.attrFree.Load() {
			return p, false
		}
		if r.static != nil {
			if b := r.static.close.Load(); b != nil {
				return b.p, b.ch
			}
			q, ch := pb.startTagCloseDerivCh(r.cached)
			if !ch {
				q = p
			}
			r.static.close.Store(&patBox{p: q, ch: ch})
			return q, ch
		}
		// One step, not expand: the definition's body is a static wrapper
		// (memo.go) whose memo answers for every path that shares it.
		q, err := r.get()
		if err != nil {
			q = notAllowedPat{}
		}
		q, ch := pb.startTagCloseDerivCh(q)
		if !ch {
			r.attrFree.Store(true)
			return p, false
		}
		return q, true
	}
	switch t := expand(p).(type) {
	case *afterPat:
		l, ch := pb.startTagCloseDerivCh(t.Left)
		if !ch {
			return p, false
		}
		return pb.after(l, t.Right), true
	case *choicePat:
		d, ch := pb.choiceMap(t, pb.startTagCloseDerivCh)
		if !ch {
			return p, false
		}
		return d, true
	case *groupPat:
		l, chl := pb.startTagCloseDerivCh(t.Left)
		r, chr := pb.startTagCloseDerivCh(t.Right)
		if !chl && !chr {
			return p, false
		}
		return pb.group(l, r), true
	case *interleavePat:
		l, chl := pb.startTagCloseDerivCh(t.Left)
		r, chr := pb.startTagCloseDerivCh(t.Right)
		if !chl && !chr {
			return p, false
		}
		return pb.interleave(l, r), true
	case *oneOrMorePat:
		q, ch := pb.startTagCloseDerivCh(t.Pattern)
		if !ch {
			return p, false
		}
		return pb.oneOrMore(q), true
	case *attributePat:
		return notAllowedPat{}, true
	}
	return p, false
}

// endTagDeriv is the derivative with respect to an element's end tag.
//
// The element's content is complete, so what remains is the continuation
// stored in the afterPat — but only if the content pattern is nullable, meaning
// everything it required was supplied.
func (pb *patBuilder) endTagDeriv(p pattern) pattern {
	switch t := expand(p).(type) {
	case *choicePat:
		d, _ := pb.choiceMap(t, func(q pattern) (pattern, bool) { return pb.endTagDeriv(q), true })
		return d
	case *afterPat:
		if t.Left.nullable() {
			return t.Right
		}
		return notAllowedPat{}
	}
	return notAllowedPat{}
}

func whitespaceOnly(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

func splitTokens(s string) []string {
	var out []string
	start := -1
	for i, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// patternSize is a pattern's node count. The compound constructors store it
// (pattern.go), so reading it is O(1); a static wrapper reports its subtree's.
// An elementPat's and attributePat's content is not counted: it is the
// schema's own structure, which is fixed, and only the derivative's
// accumulation is what grows with the document.
//
// It exists to bound the derivative's growth. The algorithm's cost is the size
// of the pattern it is carrying, and the constructors' simplifications keep
// that bounded for ordinary schemas — but not for all of them. A oneOrMore
// nested inside a oneOrMore duplicates its operand on every child, so the
// pattern grows multiplicatively in the number of children: measured, a
// 189-byte schema and a 63-byte instance of fourteen children reached 1.2 GB
// and 1.35 seconds, growing about ninefold for every two children added.
// Nothing else bounded it — MaxDepth does not, because the document is two
// levels deep whatever its width.
//
// choice() merging equal alternatives keeps ordinary schemas small, and
// patBuilder interns patterns, but neither bounds a hostile grammar: interning
// shares equal nodes without merging the alternatives patEq cannot equate, so
// the tree this counts still grows. So the bound stays: the size is checked as
// the derivative is taken, and a pattern past the limit ends validation with
// an error that says so rather than with a verdict that cost a gigabyte to
// reach.
func patternSize(p pattern) int {
	switch t := p.(type) {
	case *choicePat:
		return t.size
	case *groupPat:
		return t.size
	case *interleavePat:
		return t.size
	case *afterPat:
		return t.size
	case *oneOrMorePat:
		return t.size
	case *listPat:
		return t.size
	case *refPat:
		if t.static != nil {
			return int(t.static.size)
		}
	}
	return 1
}
