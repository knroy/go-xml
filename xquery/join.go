package xquery

import (
	"sort"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// A joinClause is a "for" immediately followed by a "where" whose test is a
// general comparison between an operand that reads only the "for" variable
// and one that does not read it at all:
//
//	for $t in S where $t/buyer/@person = $p/@id
//
// Run as written, that is a nested loop: every incoming tuple evaluates S and
// then both operands once per item, which is quadratic when the FLWOR is
// itself evaluated once per outer tuple (XMark q8, q9, q11, q12). The join
// evaluates S and the inner operand once per item and keeps them while S's
// free variables stay bound to the same items, evaluates the outer operand
// once per incoming tuple, and answers "=" over strings from a hash index.
//
// Nothing about the result may differ from the nested loop, so the join only
// ever answers when it is sure. Any error, any value it cannot hash, anything
// the analysis did not recognise, and it runs the original two clauses over
// the whole input, which reproduces the nested loop's error and the point it
// is raised exactly: every expression involved is Hoistable, so evaluating
// one more or one fewer time has no other effect.
type joinClause struct {
	f *forClause
	w *whereClause

	op string
	// cmp is the where test itself, for Comparer.
	cmp *xpath.Compiled
	// inner reads only the for variable; outer does not read it.
	inner, outer *xpath.Compiled
	// innerLeft says which side of the comparison inner was written on, so
	// that each pair is compared in the order the operator would.
	innerLeft bool
	// deps are S's free variables: the cache key.
	deps []xdm.QName
}

// planJoins replaces each joinable for/where pair in a FLWOR's clauses.
func planJoins(cs []clause) []clause {
	var out []clause
	for i := 0; i < len(cs); i++ {
		if i+1 < len(cs) {
			if j := newJoin(cs[i], cs[i+1]); j != nil {
				out = append(out, j)
				i++
				continue
			}
		}
		out = append(out, cs[i])
	}
	return out
}

// plainXPath returns e's compiled form when evaluating it is exactly
// evaluating that form: no lifted operands, no declared type.
func plainXPath(e *compiledExpr) *xpath.Compiled {
	if e == nil || e.items != nil || len(e.ops) != 0 || e.typed || e.check != nil {
		return nil
	}
	return e.xpc
}

func newJoin(a, b clause) *joinClause {
	f, ok := a.(*forClause)
	if !ok || f.hasPos || f.allowingEmpty || f.emptyCheck != nil {
		return nil
	}
	w, ok := b.(*whereClause)
	if !ok {
		return nil
	}
	seq, test := plainXPath(f.seq), plainXPath(w.test)
	if seq == nil || test == nil || !seq.Hoistable() {
		return nil
	}
	op, l, r, ok := test.Comparison()
	if !ok || !l.Hoistable() || !r.Hoistable() {
		return nil
	}
	reads := func(c *xpath.Compiled) (v, others bool) {
		for _, n := range c.FreeVariables() {
			if n.URI == f.name.URI && n.Local == f.name.Local {
				v = true
			} else {
				others = true
			}
		}
		return v, others
	}
	lv, lo := reads(l)
	rv, ro := reads(r)
	j := &joinClause{f: f, w: w, op: op, cmp: test, deps: seq.FreeVariables()}
	switch {
	case lv && !lo && !rv:
		j.inner, j.outer, j.innerLeft = l, r, true
	case rv && !ro && !lv:
		j.inner, j.outer = r, l
	default:
		return nil
	}
	return j
}

// joinState is the join caches of one evaluation of a query. It lives on the
// evalContext rather than on the clause, so that concurrent evaluations of
// one compiled query share nothing, and it is released when the evaluation
// returns so that nothing it holds outlives it.
type joinState struct {
	m        map[*joinClause]*joinCache
	released bool
}

func (s *joinState) get(j *joinClause) *joinCache {
	if s == nil {
		return nil
	}
	return s.m[j]
}

func (s *joinState) put(j *joinClause, e *joinCache) {
	if s == nil || s.released {
		return
	}
	if s.m == nil {
		s.m = map[*joinClause]*joinCache{}
	}
	s.m[j] = e
}

func (s *joinState) release() {
	s.m, s.released = nil, true
}

// joinCache is S and its inner keys for one binding of S's free variables.
// It is never modified once built, so a re-entrant evaluation that replaces
// it leaves an outer user of the old one undisturbed.
type joinCache struct {
	deps  []xdm.Sequence
	items xdm.Sequence
	keys  []xdm.Sequence // the inner operand, atomized, per item
	// index maps a key's string value to the ascending positions of the items
	// having it. It is built only for "=" when every key is an xs:string or
	// xs:untypedAtomic, the only types whose general-comparison equality is
	// string equality (under the codepoint collation, checked at use).
	index map[string][]int
}

func (c *joinClause) apply(in []tuple, ctx *evalContext) ([]tuple, error) {
	if out, n, ok := c.join(in, ctx); ok {
		// What the for clause would have charged before the where clause ran.
		if err := ctx.xp.ChargeItems(n); err != nil {
			return nil, err
		}
		return out, nil
	}
	mid, err := c.f.apply(in, ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.xp.ChargeItems(len(mid)); err != nil {
		return nil, err
	}
	return c.w.apply(mid, ctx)
}

// join is the fast path. ok false means "run the clauses as written".
func (c *joinClause) join(in []tuple, ctx *evalContext) (out []tuple, n int, ok bool) {
	state := ctx.joins
	if state == nil || state.released {
		state = &joinState{} // this apply only
	}
	for _, t := range in {
		if ctx.xp.Err() != nil {
			return nil, 0, false
		}
		sub := t.sub(ctx)
		e := c.cache(sub, state)
		if e == nil {
			return nil, 0, false
		}
		n += len(e.items)
		o, err := c.outer.Eval(sub.xp)
		if err != nil {
			return nil, 0, false
		}
		oa, err := xdm.AtomizeChecked(o)
		if err != nil {
			return nil, 0, false
		}
		hits, ok := c.match(e, oa, sub.xp)
		if !ok {
			return nil, 0, false
		}
		for _, i := range hits {
			out = append(out, t.bind(c.f.name, xdm.One(e.items[i])))
		}
	}
	return out, n, true
}

// cache returns S and its keys for the bindings in sub, from the state when
// S's free variables are bound to the same items as when it was built.
func (c *joinClause) cache(sub *evalContext, state *joinState) *joinCache {
	deps := make([]xdm.Sequence, len(c.deps))
	for i, name := range c.deps {
		v, ok := sub.xp.LookupVar(name)
		if !ok {
			return nil
		}
		deps[i] = v
	}
	if e := state.get(c); e != nil && sameBindings(e.deps, deps) {
		return e
	}
	items, err := c.f.seq.eval(sub)
	if err != nil {
		return nil
	}
	e := &joinCache{deps: deps, items: items, keys: make([]xdm.Sequence, len(items))}
	hashable := c.op == "="
	for i, it := range items {
		k, err := c.inner.Eval(sub.xp.WithVar(c.f.name, xdm.One(it)))
		if err != nil {
			return nil
		}
		if e.keys[i], err = xdm.AtomizeChecked(k); err != nil {
			return nil
		}
		hashable = hashable && allStrings(e.keys[i])
	}
	if hashable {
		e.index = map[string][]int{}
		for i, ks := range e.keys {
			for _, k := range ks {
				s := k.(*xdm.Atomic).Str()
				if p := e.index[s]; len(p) == 0 || p[len(p)-1] != i {
					e.index[s] = append(p, i)
				}
			}
		}
	}
	state.put(c, e)
	return e
}

// match returns the ascending positions of the items whose key compares true
// against the outer key oa.
func (c *joinClause) match(e *joinCache, oa xdm.Sequence, xp *xpath.Context) ([]int, bool) {
	if e.index != nil && allStrings(oa) && c.cmp.CodepointEquality(xp) {
		if len(oa) == 1 {
			return e.index[oa[0].(*xdm.Atomic).Str()], true
		}
		var hits []int
		for _, k := range oa {
			hits = append(hits, e.index[k.(*xdm.Atomic).Str()]...)
		}
		sort.Ints(hits)
		return dedupSorted(hits), true
	}
	var hits []int
	compare := c.cmp.Comparer(xp)
	for i, ik := range e.keys {
		la, ra := oa, ik
		if c.innerLeft {
			la, ra = ik, oa
		}
		ok, err := compare(la, ra)
		if err != nil {
			return nil, false
		}
		if ok {
			hits = append(hits, i)
		}
	}
	return hits, true
}

func allStrings(s xdm.Sequence) bool {
	for _, it := range s {
		a, ok := it.(*xdm.Atomic)
		if !ok || (a.Type != xdm.TypeString && a.Type != xdm.TypeUntypedAtomic) {
			return false
		}
	}
	return true
}

func dedupSorted(s []int) []int {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// sameBindings reports whether two lists of variable values hold the same
// items, by identity.
func sameBindings(a, b []xdm.Sequence) bool {
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for k := range a[i] {
			if a[i][k] != b[i][k] {
				return false
			}
		}
	}
	return true
}
