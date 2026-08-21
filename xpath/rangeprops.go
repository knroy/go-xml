package xpath

import (
	"fmt"
	"math"
	"math/big"

	"github.com/knroy/go-xml/xdm"
)

// An integer range is the one sequence in XPath 2.0 whose contents are fully
// determined by its bounds, so aggregates over it can be answered from those
// bounds instead of from the items.
//
// The general form of this idea is a lazy sequence type that carries its own
// cardinality — "don't ask a sequence to enumerate itself if the operation can
// be answered from its structure". That would mean making xdm.Sequence an
// interface rather than a slice, which every operation in three packages
// indexes and ranges over directly. The narrower version below buys the same
// result for the cases that actually arise, because "lo to hi" is the only
// XPath 2.0 construct that can name a sequence too large to hold: a path
// expression is bounded by the document, and a literal sequence by the length
// of the stylesheet.
//
// XPath 2.0's "to" has no step — it is always +1 — which is why nothing here
// takes one. XQuery 3.1 and XSLT 3.0 add no step either; the stepped form
// people expect is fn:for-each over a computed range.

// rangeProps describes an integer range recognised in the AST.
type rangeProps struct {
	lo, hi int64
	// empty is true for a descending or empty-operand range, which is a
	// legitimate result rather than an error: "10 to 1" is the empty sequence.
	empty bool
}

// asRange reports whether e is a bare "lo to hi" with evaluable bounds.
//
// Only a literal range qualifies. Anything wrapping it — a predicate, a "for",
// a union — changes which items survive, so the cardinality is no longer the
// range length and the ordinary path must run.
func asRange(ctx *Context, e Expr) (rangeProps, bool, error) {
	op, ok := e.(*BinaryOp)
	if !ok || op.Op != "to" {
		return rangeProps{}, false, nil
	}
	lo, err := bigInteger(ctx, op.Left)
	if err != nil {
		return rangeProps{}, false, err
	}
	hi, err := bigInteger(ctx, op.Right)
	if err != nil {
		return rangeProps{}, false, err
	}
	if lo == nil || hi == nil || lo.Cmp(hi) > 0 {
		return rangeProps{empty: true}, true, nil
	}
	// The closed forms below work in int64, so a range whose endpoints do not
	// fit declines the shortcut rather than failing. That range is still
	// legal — xs:integer is arbitrary-precision — and evalRange materialises
	// it if it is short enough to hold; declining here just sends it there.
	if !lo.IsInt64() || !hi.IsInt64() {
		return rangeProps{}, false, nil
	}
	// The *span* needs one more bit than either endpoint, and the closed forms
	// compute it in int64. Checking the endpoints alone let
	// "-9223372036854775808 to 9223372036854775807" through, where the count
	// wrapped to zero and avg() then divided by it — a panic.
	//
	// The bound here is int64, not the item limit: answering a huge range
	// arithmetically is exactly what this shortcut is for, and
	// "sum(1 to 4000000000)" must still work without materialising four
	// billion integers. Only a span the closed forms cannot represent is
	// refused, and it is refused rather than declined because materialising
	// it is not an option either.
	span := new(big.Int).Sub(hi, lo)
	if !span.IsInt64() || span.Int64() == math.MaxInt64 {
		return rangeProps{}, false, fmt.Errorf(
			"range %s to %s is too large to count", lo, hi)
	}
	return rangeProps{lo: lo.Int64(), hi: hi.Int64()}, true, nil
}

// cardinality returns the number of items in the range, without building it.
func (r rangeProps) cardinality() int64 {
	if r.empty {
		return 0
	}
	return r.hi - r.lo + 1
}

// sum returns the total of the range as an exact integer.
//
// An arithmetic series sums to n(first+last)/2. The multiplication is done in
// big.Int rather than int64 because the result overflows int64 well before the
// range itself does: summing 1 to 10^10 exceeds int64 even though both bounds
// fit comfortably. xs:integer is arbitrary-precision in this engine, so the
// exact answer is representable and truncating to int64 would be silently
// wrong rather than an error.
func (r rangeProps) sum() *big.Int {
	if r.empty {
		return big.NewInt(0)
	}
	n := new(big.Int).SetInt64(r.cardinality())
	ends := new(big.Int).Add(big.NewInt(r.lo), big.NewInt(r.hi))
	prod := new(big.Int).Mul(n, ends)
	// n(first+last) is always even: either n or (first+last) is, since
	// consecutive integers alternate parity.
	return prod.Rsh(prod, 1)
}

// min and max are the bounds themselves, since the range is ascending.
func (r rangeProps) min() int64 { return r.lo }
func (r rangeProps) max() int64 { return r.hi }

// avg returns the mean, which for an arithmetic series is the midpoint of the
// bounds. It is a decimal rather than an integer because the midpoint of two
// consecutive integers is not one: avg(1 to 2) is 1.5.
func (r rangeProps) avg() *big.Rat {
	if r.empty {
		return nil
	}
	sum := new(big.Rat).SetInt(r.sum())
	return sum.Quo(sum, new(big.Rat).SetInt64(r.cardinality()))
}

// rangeAggregate answers an aggregate function over a bare range from its
// bounds. It reports ok=false when the call is not that shape.
func rangeAggregate(ctx *Context, e *FuncCall) (xdm.Sequence, bool, error) {
	if e.Name.URI != xdm.NSFN || len(e.Args) != 1 {
		return nil, false, nil
	}
	switch e.Name.Local {
	case "count", "sum", "min", "max", "avg":
	default:
		return nil, false, nil
	}

	r, ok, err := asRange(ctx, e.Args[0])
	if err != nil || !ok {
		return nil, false, err
	}

	switch e.Name.Local {
	case "count":
		return xdm.One(xdm.NewInteger(r.cardinality())), true, nil
	case "sum":
		// fn:sum of the empty sequence is 0, not the empty sequence.
		return xdm.One(xdm.NewIntegerFromRat(new(big.Rat).SetInt(r.sum()))), true, nil
	case "min", "max", "avg":
		// These return the empty sequence for an empty input, unlike sum.
		if r.empty {
			return xdm.Empty, true, nil
		}
		switch e.Name.Local {
		case "min":
			return xdm.One(xdm.NewInteger(r.min())), true, nil
		case "max":
			return xdm.One(xdm.NewInteger(r.max())), true, nil
		}
		return xdm.One(xdm.NewDecimal(r.avg())), true, nil
	}
	return nil, false, nil
}

// rangeContains answers a general comparison of one integer against a bare
// range without building it.
//
// "$n = 1000000000000000000000 to 1000000000000010000003" asks whether one
// value falls inside ten million integers; materialising them to find out is
// both slow and, past the item limit, impossible. The bounds settle every
// operator, and they settle it in big.Int so the endpoints need not fit an
// int64.
//
// It reports ok=false for anything it does not recognise, which sends the
// expression to the ordinary path.
func rangeContains(ctx *Context, op *BinaryOp, valueOp string) (bool, bool, error) {
	rng, ok := op.Right.(*BinaryOp)
	if !ok || rng.Op != "to" {
		return false, false, nil
	}
	lo, err := bigInteger(ctx, rng.Left)
	if err != nil {
		return false, false, err
	}
	hi, err := bigInteger(ctx, rng.Right)
	if err != nil {
		return false, false, err
	}
	if lo == nil || hi == nil {
		return false, false, nil
	}
	// A descending range is empty, and a general comparison against an empty
	// sequence is false whatever the operator.
	if lo.Cmp(hi) > 0 {
		return false, true, nil
	}

	lv, err := op.Left.Eval(ctx)
	if err != nil {
		return false, false, err
	}
	atoms := xdm.Atomize(lv)
	if len(atoms) != 1 {
		return false, false, nil
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok || a.Type != xdm.TypeInteger {
		return false, false, nil
	}
	r := a.Rat()
	if r == nil || !r.IsInt() {
		return false, false, nil
	}
	n := new(big.Int).Set(r.Num())

	// Each operator holds for *some* member of [lo, hi], which the bounds
	// decide: "n < hi" is enough for "<", and a range with more than one
	// member always has something both equal to and different from n.
	switch valueOp {
	case "eq":
		return n.Cmp(lo) >= 0 && n.Cmp(hi) <= 0, true, nil
	case "ne":
		return lo.Cmp(hi) != 0 || n.Cmp(lo) != 0, true, nil
	case "lt":
		return n.Cmp(hi) < 0, true, nil
	case "le":
		return n.Cmp(hi) <= 0, true, nil
	case "gt":
		return n.Cmp(lo) > 0, true, nil
	case "ge":
		return n.Cmp(lo) >= 0, true, nil
	}
	return false, false, nil
}
