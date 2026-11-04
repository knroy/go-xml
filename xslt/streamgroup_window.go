package xslt

// A window over a streamed grouping population (lane L5):
// select="subsequence(P, a, b)", a and b integer literals
// (si-group-A09/A10, B09/B10, C09/C10), or select="(P)[p]", p a test of
// the position alone ((//*)[position() = 1 to 6], si-group-A05/B05/C05).
//
// The population walk hands over P's matches in document order, which are
// the path's items in order; it counts them and passes over those outside
// the window, so the population is exactly the window's, read one item at a
// time as for the bare path. Every candidate is still tested against P's
// predicates, the window's members or not, as the tree's evaluation of P
// reads them all before the window takes its part: the select's errors are
// the bare path's.

import (
	"math"
	"strconv"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// groupWin is the window of a population: positions from to before to, or
// those pred holds at; on says there is one.
type groupWin struct {
	on       bool
	from, to int64
	pred     *xpath.Compiled
	seen     int64
}

// groupWindow is the window of s, a bare path but for its outer:
// subsequence($items, a) or subsequence($items, a, b) with integer
// literals, or $items[p] with p a boolean test of position() that cannot
// fail (positionPred); ok is false when s is not such a split.
func groupWindow(s *streamSplit) (w groupWin, ok bool) {
	if s.cond != nil || s.ctxItem || s.red != "" || s.tail != nil || s.tailText != "" ||
		s.steps == nil || s.skip != 0 || s.outer == nil {
		return w, false
	}
	if fe, isFilter := s.outer.Expr().(*xpath.FilterExpr); isFilter {
		if len(fe.Predicates) != 1 || !isVarRef(fe.Base, splitItems) {
			return w, false
		}
		p := fe.Predicates[0]
		b, isOp := p.(*xpath.BinaryOp)
		if !isOp || !positionPred(p) || !booleanOp[b.Op] {
			return w, false // a number would be a position, not a boolean
		}
		return groupWin{on: true, pred: xpathleaf.WithExpr(s.outer, p).(*xpath.Compiled)}, true
	}
	f, isCall := s.outer.Expr().(*xpath.FuncCall)
	if !isCall || !isFn(f, "subsequence", len(f.Args)) || len(f.Args) < 2 || len(f.Args) > 3 ||
		!isVarRef(f.Args[0], splitItems) {
		return w, false
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
	from, ok := lit(f.Args[1])
	if !ok {
		return w, false
	}
	w = groupWin{on: true, from: from, to: math.MaxInt64}
	if len(f.Args) == 3 {
		n, ok := lit(f.Args[2])
		if !ok {
			return w, false
		}
		w.to = from + n
	}
	return w, true
}

// booleanOp are the operators whose value is a boolean: a predicate of them
// is a test, not a position.
var booleanOp = map[string]bool{"=": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
	"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true, "and": true, "or": true}

// outside counts the population's next item and reports whether it is
// outside the window (none: every item is inside).
func (w *groupWin) outside(ctx *xpath.Context) (bool, error) {
	if !w.on {
		return false, nil
	}
	w.seen++
	if w.pred == nil {
		return w.seen < w.from || w.seen >= w.to, nil
	}
	seq, err := w.pred.Eval(ctx.WithFocus(xdm.NewInteger(w.seen), int(w.seen), 0))
	if err != nil {
		return false, err
	}
	in, err := xpath.EffectiveBooleanValue(seq)
	return !in, err
}
