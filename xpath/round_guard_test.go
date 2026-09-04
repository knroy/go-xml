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
		want := exactRound(d, int(roundGuard+1), true)
		have := exactRound(d, p2, true)
		if want.Cmp(have) != 0 {
			t.Errorf("round(1/2^%d, %d) was answered at precision %d instead, "+
				"which is a different value", roundGuard+3, roundGuard+1, p2)
		}
	}
}

// withRoundGuard runs fn with roundGuard set to g, restoring it afterwards.
//
// roundGuard is a package-level var for this reason alone -- the same seam
// xsd/budget_soundness_test.go uses for maxPositions, branchLimit,
// subsumeMaxStates and subsumeMaxProduct. Injecting is the only way to reach
// the non-terminating branch in a test: at the shipped 1<<20 the branch is
// entered only by a precision above a million, and exactRound would then build
// a bignum with a million digits.
func withRoundGuard(g int64, fn func()) {
	old := roundGuard
	defer func() { roundGuard = old }()
	roundGuard = g
	fn()
}

// nonTerminating returns rationals with no finite decimal expansion. A big.Rat
// is kept in lowest terms, so a denominator with any prime factor other than 2
// or 5 never terminates; ratScale reports ok=false for exactly these.
func nonTerminating() []struct {
	name string
	val  *big.Rat
} {
	return []struct {
		name string
		val  *big.Rat
	}{
		{"1/3", big.NewRat(1, 3)},
		{"-1/3", big.NewRat(-1, 3)},
		{"1/7", big.NewRat(1, 7)},
		{"22/7", big.NewRat(22, 7)},
		{"-22/7", big.NewRat(-22, 7)},
		{"1/6", big.NewRat(1, 6)},
		{"2/11", big.NewRat(2, 11)},
		{"1000000/3", big.NewRat(1000000, 3)},
	}
}

// The guard branch exists and these values reach it.
//
// The audit that prompted this test observed that every case in
// TestRoundGuardReductionPreservesValue is a terminating value -- 1/2^n,
// 3*10^k -- and so takes the ratScale(r) == true path. Instrumentation
// confirmed it: across the whole xpath package test suite the
// non-terminating branch was entered zero times. A test that cannot say which
// branch it ran is the defect being fixed here, so this one asserts the branch
// directly, by the only observable that distinguishes it: with a small guard,
// a precision above the guard comes back REDUCED to exactly the guard. No
// terminating value can produce that, since the terminating path returns
// either the requested precision unchanged or identity/zero.
func TestRoundGuardNonTerminatingEntersGuardBranch(t *testing.T) {
	const g = 20
	withRoundGuard(g, func() {
		for _, c := range nonTerminating() {
			if _, ok := ratScale(c.val); ok {
				t.Fatalf("%s: test premise broken, ratScale reports it terminates", c.name)
			}
			// Above the guard: must be clamped to the guard.
			for _, p := range []int64{g + 1, g + 2, g + 1000, 1 << 40} {
				got, identity, zero := roundPlaces(c.val, p)
				if identity || zero {
					t.Errorf("%s at %d: identity=%v zero=%v; a non-terminating "+
						"value is never exactly representable at any precision, "+
						"so rounding always changes it and never discards it all",
						c.name, p, identity, zero)
					continue
				}
				if int64(got) != g {
					t.Errorf("%s at %d: roundPlaces returned %d, want the guard %d; "+
						"the guard branch was not entered", c.name, p, got, g)
				}
			}
			// At or below the guard: must pass through untouched, so the
			// clamp above is really the guard firing and not a blanket cap.
			for _, p := range []int64{0, 1, g - 1, g} {
				got, identity, zero := roundPlaces(c.val, p)
				if identity || zero {
					t.Errorf("%s at %d: identity=%v zero=%v, want a real rounding",
						c.name, p, identity, zero)
					continue
				}
				if int64(got) != p {
					t.Errorf("%s at %d: roundPlaces returned %d, want %d unchanged; "+
						"a precision within the guard must not be reduced",
						c.name, p, got, p)
				}
			}
		}
	})
}

// The guard's CONTRACT, stated precisely and tested.
//
// A non-terminating rational x has no finite decimal expansion. Therefore for
// every precision p, x is STRICTLY between the two adjacent multiples of
// 10^-p, and in particular is never equal to either and never equal to the
// halfway point between them. Two consequences, and they are what make
// reducing the precision safe rather than merely cheap:
//
//  1. Rounding x at any precision is decided by a strict inequality, so no
//     tie-break is ever consulted: round and round-half-to-even agree, and
//     the answer does not depend on how many further digits are examined.
//  2. Rounding is never the identity and never yields zero for a p >= 0, so
//     roundPlaces must report a real precision on this path, never a
//     shortcut.
//
// That is the whole justification for the reduction. It does NOT claim
// round(x, p) == round(x, guard) -- those are different numbers, and the
// policy comment on roundGuard says so. It claims the reduced answer is the
// correctly rounded value at the reduced precision, computed without a
// fabricated tie.
func TestRoundGuardNonTerminatingContract(t *testing.T) {
	for _, c := range nonTerminating() {
		for _, p := range []int64{0, 1, 2, 5, 17, 40} {
			half := exactRound(c.val, int(p), true)
			up := exactRound(c.val, int(p), false)
			// (1) No tie is reachable, so the two tie-break rules agree.
			if half.Cmp(up) != 0 {
				t.Errorf("%s at %d: round-half-to-even gave %v but round gave %v; "+
					"a non-terminating value cannot sit on a halfway point, so "+
					"the tie-break must never be consulted",
					c.name, p, half, up)
			}
			// (1) restated as the fact underneath it: x is strictly between
			// its floor and ceiling at 10^-p, and not at the midpoint.
			scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(p), nil))
			scaled := new(big.Rat).Mul(c.val, scale)
			if scaled.IsInt() {
				t.Errorf("%s at %d: value is exactly representable, so it terminates; "+
					"test premise broken", c.name, p)
			}
			twice := new(big.Rat).Mul(scaled, big.NewRat(2, 1))
			if twice.IsInt() {
				t.Errorf("%s at %d: value sits exactly on a halfway point, which a "+
					"non-terminating rational cannot do", c.name, p)
			}
			// (2) The guard branch never reports identity or zero.
			withRoundGuard(3, func() {
				_, identity, zero := roundPlaces(c.val, p)
				if identity || zero {
					t.Errorf("%s at %d: roundPlaces reported identity=%v zero=%v on a "+
						"non-terminating value", c.name, p, identity, zero)
				}
			})
		}
	}
}

// The reduced answer is the correctly rounded value AT THE REDUCED PRECISION.
//
// This is the operational half of the contract: whatever roundPlaces hands
// back on the non-terminating path, exactRound at that precision must agree
// with rounding the original value there directly. If the guard ever returned
// a precision that was not simply "the requested one, capped", this fails.
func TestRoundGuardNonTerminatingReducedAnswerIsCorrect(t *testing.T) {
	for _, g := range []int64{1, 3, 12, 25} {
		withRoundGuard(g, func() {
			for _, c := range nonTerminating() {
				for _, p := range []int64{0, g - 1, g, g + 1, g + 7, 1 << 30} {
					if p < 0 {
						continue
					}
					got, identity, zero := roundPlaces(c.val, p)
					if identity || zero {
						t.Errorf("%s at %d (guard %d): unexpected shortcut", c.name, p, g)
						continue
					}
					wantPrec := p
					if p > g {
						wantPrec = g
					}
					if int64(got) != wantPrec {
						t.Errorf("%s at %d (guard %d): got precision %d, want %d",
							c.name, p, g, got, wantPrec)
					}
					// And the value at that precision is the honest rounding.
					if exactRound(c.val, got, true).Cmp(exactRound(c.val, int(wantPrec), true)) != 0 {
						t.Errorf("%s at %d (guard %d): answer at precision %d is not "+
							"round(x, %d)", c.name, p, g, got, wantPrec)
					}
				}
			}
		})
	}
}

// A terminating value must NEVER be clamped, whatever the guard is.
//
// This is the property commit 1140493 shipped, restated with the guard forced
// pathologically low so that a reintroduced clamp cannot hide behind 1<<20
// being larger than any precision a test would otherwise use. With guard 2,
// any code path that consults roundGuard for a terminating value is visible
// immediately.
func TestRoundGuardTerminatingNeverClamped(t *testing.T) {
	withRoundGuard(2, func() {
		pow2 := func(n int64) *big.Int { return new(big.Int).Exp(big.NewInt(2), big.NewInt(n), nil) }
		pow10 := func(n int64) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil) }

		// 1/2^40 has scale 40. Every precision strictly below 40 is a real
		// rounding and must be answered at exactly that precision.
		v := new(big.Rat).SetFrac(big.NewInt(1), pow2(40))
		for _, p := range []int64{0, 1, 3, 10, 39} {
			got, identity, zero := roundPlaces(v, p)
			if identity || zero {
				t.Errorf("1/2^40 at %d: identity=%v zero=%v, want a real rounding",
					p, identity, zero)
				continue
			}
			if int64(got) != p {
				t.Errorf("1/2^40 at %d: precision reduced to %d with guard=2; a "+
					"terminating value is bounded by its own scale and must never "+
					"consult the guard", p, got)
			}
		}
		// 3*10^30: negative precisions down to -30 discard only zeros.
		w := new(big.Rat).SetInt(new(big.Int).Mul(big.NewInt(3), pow10(30)))
		for _, p := range []int64{-1, -15, -29, -30} {
			got, identity, zero := roundPlaces(w, p)
			if zero {
				t.Errorf("3e30 at %d: reported zero with guard=2; those digits are "+
					"all zeros and the answer is the value unchanged", p)
				continue
			}
			if !identity && exactRound(w, got, true).Cmp(w) != 0 {
				t.Errorf("3e30 at %d: value changed with guard=2", p)
			}
		}
	})
}
