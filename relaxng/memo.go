package relaxng

import "math"

// Static memo points: a cheap stand-in for hash-consing.
//
// The derivatives are taken over the compiled schema, and most of what they
// walk is the schema's own fixed structure -- DocBook's inline content is a
// choice of some three hundred elements, rebuilt or walked for every element
// the document opens. A *refPat is the one pattern with an identity, so after
// compiling, every compound subtree of four nodes or more is put behind one
// (a "static" refPat, already resolved). There the derivatives can stop or
// remember: attDeriv stops at attrFree, startTagCloseDeriv memoises its one
// answer, startTagOpenDeriv memoises by name, textDeriv memoises once for a
// subtree holding no data, value or list, and nullable reads a stored answer.
//
// A static wrapper is invisible to everything that measures or compares:
// patternSize reports the subtree's size and patEq and inChoice look through
// it, so the derivatives have the same shape, MaxPatternSize fires on the same
// inputs, and choice() merges exactly what it merged before.
//
// Subtree identity needs no address arithmetic: the compiler already shares a
// definition reached twice behind one *refPat (compileRefNamed), and inside a
// definition every subtree is used once, so walking each *refPat once visits
// every schema subtree once.

// minWrap is the smallest subtree worth a wrapper.
const minWrap = 4

// addMemoPoints wraps p's compound subtrees; see above.
func addMemoPoints(p pattern) pattern {
	return memoTr(p, map[*refPat]bool{})
}

func memoTr(p pattern, refs map[*refPat]bool) pattern {
	var q pattern
	switch t := p.(type) {
	case *refPat:
		if !refs[t] {
			refs[t] = true
			if t.done && t.err == nil && t.static == nil {
				t.cached = memoTr(t.cached, refs)
			}
		}
		return t
	case *elementPat:
		// An element is not wrapped itself: startTagOpenDeriv's answer for
		// it is one afterPat, and its content is wrapped instead.
		return &elementPat{t.Name, memoTr(t.Pattern, refs)}
	case *choicePat:
		q = &choicePat{memoTr(t.Left, refs), memoTr(t.Right, refs)}
	case *groupPat:
		q = &groupPat{memoTr(t.Left, refs), memoTr(t.Right, refs)}
	case *interleavePat:
		q = &interleavePat{memoTr(t.Left, refs), memoTr(t.Right, refs)}
	case *oneOrMorePat:
		q = &oneOrMorePat{memoTr(t.Pattern, refs)}
	default:
		return p
	}
	n := patternSize(q, math.MaxInt32)
	if n < minWrap {
		return q
	}
	s := &staticInfo{size: int32(n), null: q.nullable(),
		dataFree: staticFree(q, dataKinds, nil), selfEq: patEq(q, q)}
	r := &refPat{cached: q, done: true, name: "(static)", static: s}
	r.attrFree.Store(staticFree(q, attrKinds, nil))
	return r
}

// unstatic looks through static wrappers.
func unstatic(p pattern) pattern {
	for {
		r, ok := p.(*refPat)
		if !ok || r.static == nil {
			return p
		}
		p = r.cached
	}
}

type kinds int

const (
	attrKinds kinds = iota // attributePat
	dataKinds              // valuePat, dataPat, listPat
)

// staticFree reports whether p holds none of the kinds before an element
// boundary. A reference not yet resolved, or one being visited, answers false.
func staticFree(p pattern, k kinds, visiting map[*refPat]bool) bool {
	switch t := p.(type) {
	case *attributePat:
		return k != attrKinds
	case *valuePat, *dataPat, *listPat:
		return k != dataKinds
	case *elementPat, emptyPat, notAllowedPat, textPat:
		return true
	case *choicePat:
		return staticFree(t.Left, k, visiting) && staticFree(t.Right, k, visiting)
	case *groupPat:
		return staticFree(t.Left, k, visiting) && staticFree(t.Right, k, visiting)
	case *interleavePat:
		return staticFree(t.Left, k, visiting) && staticFree(t.Right, k, visiting)
	case *oneOrMorePat:
		return staticFree(t.Pattern, k, visiting)
	case *refPat:
		if t.static != nil {
			if k == attrKinds {
				return t.attrFree.Load()
			}
			return t.static.dataFree
		}
		if !t.done || t.err != nil || visiting[t] {
			return false
		}
		if visiting == nil {
			visiting = map[*refPat]bool{}
		}
		visiting[t] = true
		ok := staticFree(t.cached, k, visiting)
		delete(visiting, t)
		return ok
	}
	return false
}
