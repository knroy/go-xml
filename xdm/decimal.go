package xdm

import "math/big"

// DecimalMagnitude is a terminating decimal split into an integer coefficient
// and a decimal scale: the value is Coefficient / 10^Scale, with Coefficient
// the absolute value and Scale the exact number of fraction digits.
type DecimalMagnitude struct {
	Coefficient *big.Int // absolute value; never negative
	Scale       int64    // fraction digits, exactly; never negative
}

// DecimalMagnitudeOf converts a rational to its exact decimal form, reporting
// false if the value has no terminating decimal expansion.
//
// The contract is strict on purpose. This primitive serves both a renderer and
// a validity decision, and a validity decision must never be handed a guess: a
// digit count invented for 1/3 would let it pass a fractionDigits facet it
// violates. So the only two answers are the exact one and an explicit false.
// Callers that want a fallback for a non-terminating value apply it themselves,
// visibly, at their own call site — see nonTerminatingScale in atomic.go.
//
// There is deliberately no ceiling on the scale either. Capping it makes the
// lexical form disagree with the value: a cap of 18 printed a literal with 360
// fraction digits as "0" while it compared unequal to zero, and raising the cap
// to 1024 moved the same contradiction to 10^-1025. The value is the thing that
// must not move, so the scale follows it.
//
// big.Rat is kept in lowest terms, so a value is an exact decimal precisely
// when its denominator is 2^a*5^b with nothing left over, and then the scale is
// max(a, b) — no more digits are needed and no fewer will do. The factor 2^a
// comes off in a single shift. The factor 5^b is stripped by repeated squaring
// rather than one division per power: dividing off 5 at a time is quadratic in
// b, which costs 907ms for a 50000-digit value against 415µs here.
func DecimalMagnitudeOf(r *big.Rat) (DecimalMagnitude, bool) {
	if r == nil {
		return DecimalMagnitude{Coefficient: new(big.Int)}, true
	}
	d := new(big.Int).Set(r.Denom())
	a := int64(d.TrailingZeroBits())
	d.Rsh(d, uint(a))

	// Powers 5^(2^i) up to the size of d, so the exponent is accumulated in
	// O(log b) divisions of a shrinking number instead of b divisions.
	pows := []*big.Int{big.NewInt(5)}
	for {
		next := new(big.Int).Mul(pows[len(pows)-1], pows[len(pows)-1])
		if next.BitLen() > d.BitLen() {
			break
		}
		pows = append(pows, next)
	}
	var b int64
	q, rem := new(big.Int), new(big.Int)
	for i := len(pows) - 1; i >= 0; i-- {
		for {
			q.QuoRem(d, pows[i], rem)
			if rem.Sign() != 0 {
				break
			}
			d.Set(q)
			b += 1 << uint(i)
		}
	}

	// Anything left after the 2s and 5s means a factor of 3, 7, … and no
	// finite decimal expansion.
	if d.CmpAbs(big.NewInt(1)) != 0 {
		return DecimalMagnitude{}, false
	}

	scale := a
	if b > scale {
		scale = b
	}
	// numerator * 2^(scale-a) * 5^(scale-b) is the value written with
	// exactly `scale` fraction digits, an exact integer by construction.
	coef := new(big.Int).Abs(r.Num())
	if k := scale - a; k > 0 {
		coef.Lsh(coef, uint(k))
	}
	if k := scale - b; k > 0 {
		coef.Mul(coef, new(big.Int).Exp(big.NewInt(5), big.NewInt(k), nil))
	}
	return DecimalMagnitude{Coefficient: coef, Scale: scale}, true
}
