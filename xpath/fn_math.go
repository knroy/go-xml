package xpath

import (
	"math"

	"github.com/knroy/go-xml/xdm"
)

// registerMathFuncs adds the math: functions of F&O 3.0 section 4.8.
//
// They live in their own namespace rather than fn:, which is why they need a
// registration helper of their own. The namespace is the only thing that
// gates them: an expression can only reach one by writing a prefix bound to
// the math URI, and a 2.0 stylesheet has no reason to bind it. Registering
// them unconditionally therefore costs a 2.0 caller nothing, and avoids
// making the builtin library depend on a version it is built once for.
//
// Every one of them is defined over xs:double and delegates to Go's math
// package, which is IEEE 754 as the spec requires. The interesting content is
// not the arithmetic but the edge cases — the empty sequence, NaN, the signed
// zeroes and infinities — and those are what the tests pin down.
func registerMathFuncs(l *Library) {
	// pi is the only nullary one, and the only one that cannot be empty.
	l.registerMath("pi", []int{0}, func(_ *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
		return numSeq(math.Pi), nil
	})

	// The one-argument functions share a shape: xs:double? in, xs:double?
	// out, with the empty sequence passing straight through. Go's math
	// functions already return NaN where the spec requires NaN, so no
	// domain checking is layered on top: math:sqrt(-1) is NaN, not an error.
	unary := []struct {
		name string
		fn   func(float64) float64
	}{
		{"exp", math.Exp},
		{"exp10", func(x float64) float64 { return math.Pow(10, x) }},
		{"log", math.Log},
		{"log10", math.Log10},
		{"sqrt", math.Sqrt},
		{"sin", math.Sin},
		{"cos", math.Cos},
		{"tan", math.Tan},
		{"asin", math.Asin},
		{"acos", math.Acos},
		{"atan", math.Atan},
	}
	for _, u := range unary {
		fn := u.fn
		l.registerMath(u.name, []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			x, err := argDoubleOpt(args, 0)
			if err != nil || x == nil {
				return xdm.Empty(), err
			}
			return numSeq(fn(*x)), nil
		})
	}

	// math:pow($x as xs:double?, $y as numeric) as xs:double?
	//
	// Only $x is nullable: an empty $x gives an empty result without $y being
	// looked at. Go's math.Pow implements the IEEE 754 pow, including the
	// special cases the spec spells out at length — pow(x, 0) is 1 for every
	// x including NaN, and the sign of a zero base is preserved for
	// odd-valued whole exponents.
	l.registerMath("pow", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		x, err := argDoubleOpt(args, 0)
		if err != nil || x == nil {
			return xdm.Empty(), err
		}
		y, err := argDoubleOpt(args, 1)
		if err != nil {
			return nil, err
		}
		if y == nil {
			return xdm.Empty(), nil
		}
		return numSeq(math.Pow(*x, *y)), nil
	})

	// math:atan2($y as xs:double, $x as xs:double) as xs:double
	//
	// Neither argument is nullable here, and the argument order is (y, x) —
	// the opposite of the reading order of the name, and the same order Go
	// uses.
	l.registerMath("atan2", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		y, err := argDoubleOpt(args, 0)
		if err != nil {
			return nil, err
		}
		x, err := argDoubleOpt(args, 1)
		if err != nil {
			return nil, err
		}
		if y == nil || x == nil {
			return xdm.Empty(), nil
		}
		return numSeq(math.Atan2(*y, *x)), nil
	})
}

// registerMath is registerFn for the math: namespace.
func (l *Library) registerMath(local string, arities []int,
	call func(*Context, []xdm.Sequence) (xdm.Sequence, error)) {
	for _, a := range arities {
		l.register(xdm.NSMath, local, a, call)
	}
}

// argDoubleOpt returns argument i as a float64, or nil for the empty sequence.
//
// A pointer rather than a (float64, bool) pair so that the "empty in, empty
// out" rule every math: function shares reads as one line at each call site.
func argDoubleOpt(args []xdm.Sequence, i int) (*float64, error) {
	n, err := argNumber(args, i)
	if err != nil || n == nil {
		return nil, err
	}
	d := n.Float64()
	return &d, nil
}
