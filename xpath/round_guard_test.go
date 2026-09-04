package xpath

import (
	"fmt"
	"math/big"
	"testing"
)

// The claim under test.
//
// roundPlaces reduces a requested precision to one that is cheaper to carry
// out. Its comment asserts that the reduction never changes an answer. That
// was asserted rather than proven, and an earlier version of the same claim in
// this file was false: the +-4096 clamp it replaced silently changed values in
// both directions.
//
// The property: whenever roundPlaces(x, p) returns a precision p' without
// reporting identity or zero, exactRound(x, p) == exactRound(x, p'). Anything
// else is a silent wrong answer, since no error is raised on this path.
//
// These cases straddle roundGuard on both sides, in both signs, because that
// is the only place a reduction can happen at all.
func TestRoundGuardReductionPreservesValue(t *testing.T) {
	pow2 := func(n int64) *big.Int { return new(big.Int).Exp(big.NewInt(2), big.NewInt(n), nil) }
	pow10 := func(n int64) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil) }

	type tc struct {
		name string
		val  *big.Rat
		prec []int64
	}

	var cases []tc

	// xs:decimal values whose own scale sits far below, near, and far above
	// the guard. 1/2^n has scale exactly n.
	for _, n := range []int64{1, 64, roundGuard - 2, roundGuard, roundGuard + 3, roundGuard + 64} {
		v := new(big.Rat).SetFrac(big.NewInt(1), pow2(n))
		cases = append(cases, tc{
			name: fmt.Sprintf("decimal 1/2^%d (scale %d)", n, n),
			// Precisions below, at, and above the guard and the scale.
			prec: []int64{0, 1, n - 1, n, n + 1,
				roundGuard - 1, roundGuard, roundGuard + 1, roundGuard + 5,
				-1, -2, -(roundGuard - 1), -roundGuard, -(roundGuard + 1)},
			val: v,
		})
	}

	// xs:integer values with magnitudes far below and far above the guard.
	// 3*10^k has k+1 integer digits and k trailing zeros, so any negative
	// precision down to -k is the identity — the case the removed negative
	// guard answered with zero.
	for _, k := range []int64{0, 5, roundGuard - 2, roundGuard + 10} {
		v := new(big.Rat).SetInt(new(big.Int).Mul(big.NewInt(3), pow10(k)))
		cases = append(cases, tc{
			name: fmt.Sprintf("integer 3*10^%d", k),
			prec: []int64{0, 1, 5,
				-1, -(k / 2), -(k - 1), -k, -(k + 1), -(k + 2),
				-(roundGuard - 1), -roundGuard, -(roundGuard + 1), -(roundGuard + 5),
				roundGuard, roundGuard + 1},
			val: v,
		})
	}

	// A negative value, to catch a reduction that is sign-dependent.
	cases = append(cases, tc{
		name: "negative integer -7*10^(guard+2)",
		val:  new(big.Rat).SetInt(new(big.Int).Neg(new(big.Int).Mul(big.NewInt(7), pow10(roundGuard+2)))),
		prec: []int64{-(roundGuard - 1), -roundGuard, -(roundGuard + 1), -(roundGuard + 2), -(roundGuard + 4)},
	})

	for _, c := range cases {
		for _, p := range c.prec {
			for _, halfToEven := range []bool{false, true} {
				got, identity, zero := roundPlaces(c.val, p)
				if identity || zero {
					// These shortcuts are checked by
					// TestRoundGuardShortcutsAreCorrect below.
					continue
				}
				if int64(got) == p {
					continue // no reduction: nothing to prove
				}
				want := exactRound(c.val, int(p), halfToEven)
				have := exactRound(c.val, got, halfToEven)
				if want.Cmp(have) != 0 {
					t.Errorf("%s: roundPlaces reduced precision %d to %d, "+
						"but that changes the answer (halfToEven=%v): "+
						"round at %d gives a value with sign %d, round at %d gives sign %d, equal=%v",
						c.name, p, got, halfToEven, p, want.Sign(), got, have.Sign(), false)
				}
			}
		}
	}
}

// The identity and zero shortcuts are the other half of roundPlaces: a wrong
// shortcut is just as silent as a wrong reduction, and the +-4096 defect
// showed up as exactly that. Check them against the unreduced operation.
func TestRoundGuardShortcutsAreCorrect(t *testing.T) {
	pow2 := func(n int64) *big.Int { return new(big.Int).Exp(big.NewInt(2), big.NewInt(n), nil) }
	pow10 := func(n int64) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil) }

	type tc struct {
		name string
		val  *big.Rat
		prec []int64
	}
	cases := []tc{
		{"1/2^10", new(big.Rat).SetFrac(big.NewInt(1), pow2(10)), []int64{9, 10, 11, 50, -1, -2}},
		{"3*10^7", new(big.Rat).SetInt(new(big.Int).Mul(big.NewInt(3), pow10(7))), []int64{0, 3, -6, -7, -8, -9, -12}},
		{"-3*10^7", new(big.Rat).SetInt(new(big.Int).Neg(new(big.Int).Mul(big.NewInt(3), pow10(7)))), []int64{-6, -7, -8, -9}},
		{"9*10^3 (carry at the boundary)", new(big.Rat).SetInt(new(big.Int).Mul(big.NewInt(9), pow10(3))), []int64{-3, -4, -5, -6}},
		{"1/2 (tie)", big.NewRat(1, 2), []int64{0, 1, -1}},
	}

	for _, c := range cases {
		for _, p := range c.prec {
			for _, halfToEven := range []bool{false, true} {
				got, identity, zero := roundPlaces(c.val, p)
				want := exactRound(c.val, int(p), halfToEven)
				switch {
				case identity:
					if want.Cmp(c.val) != 0 {
						t.Errorf("%s at %d (halfToEven=%v): roundPlaces said identity, "+
							"but rounding does change the value", c.name, p, halfToEven)
					}
				case zero:
					if want.Sign() != 0 {
						t.Errorf("%s at %d (halfToEven=%v): roundPlaces said zero, "+
							"but the true answer is nonzero", c.name, p, halfToEven)
					}
				default:
					have := exactRound(c.val, got, halfToEven)
					if want.Cmp(have) != 0 {
						t.Errorf("%s at %d (halfToEven=%v): reduction to %d changed the answer",
							c.name, p, halfToEven, got)
					}
				}
			}
		}
	}
}

// The two regressions this file was written for, stated as the values they
// produce, so a reintroduced clamp fails here by name.
func TestRoundGuardKnownRegressions(t *testing.T) {
	// Negative side: 3e1048586 has its lowest nonzero digit at position
	// 10^1048586, so zeroing everything below 10^1048581 discards only zeros.
	// The removed guard answered zero.
	v := new(big.Rat).SetInt(new(big.Int).Mul(big.NewInt(3),
		new(big.Int).Exp(big.NewInt(10), big.NewInt(roundGuard+10), nil)))
	p, identity, zero := roundPlaces(v, -(roundGuard + 5))
	if zero {
		t.Errorf("round(3e%d, %d) reported zero; rounding below a value's "+
			"lowest significant digit discards only zeros and is the identity",
			roundGuard+10, -(roundGuard + 5))
	} else if !identity {
		if exactRound(v, p, true).Cmp(v) != 0 {
			t.Errorf("round(3e%d, %d) changed the value", roundGuard+10, -(roundGuard + 5))
		}
	}

	// Positive side: scale roundGuard+3, precision roundGuard+1. Both below
	// the scale, so the rounding is real; the removed clamp answered it at
	// roundGuard instead, a different number.
	d := new(big.Rat).SetFrac(big.NewInt(1),
		new(big.Int).Exp(big.NewInt(2), big.NewInt(roundGuard+3), nil))
	p2, id2, z2 := roundPlaces(d, roundGuard+1)
	if !id2 && !z2 && int64(p2) != roundGuard+1 {
		want := exactRound(d, roundGuard+1, true)
		have := exactRound(d, p2, true)
		if want.Cmp(have) != 0 {
			t.Errorf("round(1/2^%d, %d) was answered at precision %d instead, "+
				"which is a different value", roundGuard+3, roundGuard+1, p2)
		}
	}
}
