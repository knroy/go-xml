package xslt

// A window over a streamed grouping population (lane L5):
// select="subsequence(P, a, b)", P a path of child steps to elements, a and
// b integer literals (si-group-A09/A10, B09/B10, C09/C10).
//
// popWalk hands over P's matches in document order, which are the path's
// items in order; it counts them and passes over (SkipTag) those outside
// positions a to a+b-1, so the population is exactly subsequence's, read
// one item at a time as for the bare path. Every candidate is still tested
// against P's predicates, the window's members or not, as the tree's
// evaluation of P reads them all before subsequence takes its part: the
// select's errors are the bare path's.

import (
	"math"
	"strconv"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// groupWindow reports whether s is a bare path but for its outer,
// subsequence($items, a) or subsequence($items, a, b) with integer literals,
// and the positions it keeps: from to before to.
func groupWindow(s *streamSplit) (from, to int64, ok bool) {
	if s.cond != nil || s.ctxItem || s.red != "" || s.tail != nil || s.tailText != "" ||
		s.steps == nil || s.skip != 0 || s.outer == nil {
		return 0, 0, false
	}
	f, isCall := s.outer.Expr().(*xpath.FuncCall)
	if !isCall || !isFn(f, "subsequence", len(f.Args)) || len(f.Args) < 2 || len(f.Args) > 3 ||
		!isVarRef(f.Args[0], splitItems) {
		return 0, 0, false
	}
	const most = 1 << 40 // far past any count, and no overflow below
	lit := func(e xpath.Expr) (int64, bool) {
		l, ok := e.(*xpath.Literal)
		if !ok || l.Val == nil || l.Val.Type != xdm.TypeInteger {
			return 0, false
		}
		n, err := strconv.ParseInt(l.Val.String(), 10, 64)
		return n, err == nil && n >= 0 && n <= most
	}
	if from, ok = lit(f.Args[1]); !ok {
		return 0, 0, false
	}
	to = math.MaxInt64
	if len(f.Args) == 3 {
		n, ok := lit(f.Args[2])
		if !ok {
			return 0, 0, false
		}
		to = from + n
	}
	return from, to, true
}

// outside reports whether the population's next item, counted, is outside
// the window (none: every item is inside).
func (p *popWalk) outside() bool {
	if !p.win {
		return false
	}
	p.seen++
	return p.seen < p.from || p.seen >= p.to
}
