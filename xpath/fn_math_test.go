package xpath

import (
	"math"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// mathResolver binds the "math" prefix, which is how an expression reaches
// these functions at all: they are gated by their namespace, not by a version
// flag.
type mathResolver struct{}

func (mathResolver) ResolvePrefix(p string) (string, bool) {
	switch p {
	case "math":
		return xdm.NSMath, true
	case "xs":
		return xdm.NSXS, true
	}
	return "", false
}
func (mathResolver) DefaultElementNamespace() string  { return "" }
func (mathResolver) DefaultFunctionNamespace() string { return xdm.NSFN }

func evalMath(t *testing.T, expr string) xdm.Sequence {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath30
	got, err := Eval(expr, ctx, mathResolver{})
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return got
}

// evalMathDouble evaluates and returns the single double result.
func evalMathDouble(t *testing.T, expr string) float64 {
	t.Helper()
	got := evalMath(t, expr)
	if len(got) != 1 {
		t.Fatalf("%s returned %d items, want 1", expr, len(got))
	}
	return got[0].(*xdm.Atomic).Float64()
}

func TestMathBasics(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{"math:pi()", math.Pi},
		{"math:sqrt(16)", 4},
		{"math:exp(0)", 1},
		{"math:exp10(3)", 1000},
		{"math:log(math:exp(1))", 1},
		{"math:log10(1000)", 3},
		{"math:sin(0)", 0},
		{"math:cos(0)", 1},
		{"math:tan(0)", 0},
		{"math:asin(0)", 0},
		{"math:acos(1)", 0},
		{"math:atan(0)", 0},
		{"math:pow(2, 3)", 8},
		{"math:pow(2, -3)", 0.125},
		{"math:atan2(0, 1)", 0},
	}
	for _, tc := range cases {
		if got := evalMathDouble(t, tc.expr); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

// The empty sequence passes straight through every one-argument function, and
// through math:pow's first argument. This is the rule that distinguishes these
// from a plain wrapper over Go's math package.
func TestMathEmptySequence(t *testing.T) {
	for _, expr := range []string{
		"math:sqrt(())", "math:exp(())", "math:log(())", "math:sin(())",
		"math:cos(())", "math:tan(())", "math:asin(())", "math:acos(())",
		"math:atan(())", "math:exp10(())", "math:log10(())",
		"math:pow((), 93.7)",
	} {
		if got := evalMath(t, expr); len(got) != 0 {
			t.Errorf("%s returned %d items, want the empty sequence", expr, len(got))
		}
	}
}

// The spec spells these out at length because they are where an implementation
// that merely calls a library function tends to diverge.
func TestMathEdgeCases(t *testing.T) {
	// pow(x, 0) is 1 for every x, NaN and INF included.
	for _, expr := range []string{
		"math:pow(2, 0)", "math:pow(0, 0)",
		`math:pow(xs:double('INF'), 0)`,
		`math:pow(xs:double('NaN'), 0)`,
		"math:pow(-math:pi(), 0)",
		`math:pow(1, xs:double('NaN'))`,
		`math:pow(-1, xs:double('INF'))`,
	} {
		if got := evalMathDouble(t, expr); got != 1 {
			t.Errorf("%s = %v, want 1", expr, got)
		}
	}

	// The sign of a zero base survives an odd-valued whole exponent.
	if got := evalMathDouble(t, "math:pow(-0e0, 3)"); math.Signbit(got) == false || got != 0 {
		t.Errorf("math:pow(-0e0, 3) = %v, want -0", got)
	}
	if got := evalMathDouble(t, "math:pow(-0e0, -3)"); !math.IsInf(got, -1) {
		t.Errorf("math:pow(-0e0, -3) = %v, want -INF", got)
	}
	if got := evalMathDouble(t, "math:pow(0e0, -3)"); !math.IsInf(got, 1) {
		t.Errorf("math:pow(0e0, -3) = %v, want INF", got)
	}
	// An even-valued exponent loses the sign.
	if got := evalMathDouble(t, "math:pow(-0e0, -3.1e0)"); !math.IsInf(got, 1) {
		t.Errorf("math:pow(-0e0, -3.1e0) = %v, want INF", got)
	}

	// Out-of-domain arguments are NaN rather than an error: the spec defines
	// these over IEEE 754, which has no exceptions to raise here.
	for _, expr := range []string{
		"math:sqrt(-1)", "math:log(-1)", "math:asin(2)", "math:acos(2)",
	} {
		if got := evalMathDouble(t, expr); !math.IsNaN(got) {
			t.Errorf("%s = %v, want NaN", expr, got)
		}
	}

	if got := evalMathDouble(t, "math:log(0)"); !math.IsInf(got, -1) {
		t.Errorf("math:log(0) = %v, want -INF", got)
	}
}

// atan2 takes (y, x), which is the opposite of the reading order of the name.
// Getting it backwards is silent — both orders typecheck — so it is pinned.
func TestMathAtan2ArgumentOrder(t *testing.T) {
	// atan2(1, 0) is +pi/2; atan2(0, 1) is 0. Swapping them swaps the answers.
	if got := evalMathDouble(t, "math:atan2(1, 0)"); math.Abs(got-math.Pi/2) > 1e-12 {
		t.Errorf("math:atan2(1, 0) = %v, want pi/2", got)
	}
	if got := evalMathDouble(t, "math:atan2(0, 1)"); got != 0 {
		t.Errorf("math:atan2(0, 1) = %v, want 0", got)
	}
	if got := evalMathDouble(t, "math:atan2(-0e0, -1)"); math.Abs(got+math.Pi) > 1e-12 {
		t.Errorf("math:atan2(-0e0, -1) = %v, want -pi", got)
	}
}
