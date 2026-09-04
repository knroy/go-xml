package xdm

import (
	"math/big"
	"testing"
)

func TestDecimalMagnitudeOf(t *testing.T) {
	cases := []struct {
		in    string
		coef  string
		scale int64
	}{
		{"0", "0", 0},
		{"1", "1", 0},
		{"12", "12", 0},
		{"1.2", "12", 1},
		{"12.34", "1234", 2},
		{"0.001", "1", 3},
		{"0.000001", "1", 6},
		{"100.001", "100001", 3},

		// The value, not the literal: 1.00 is the integer 1.
		{"1.00", "1", 0},
		{"10.0", "10", 0},

		// The coefficient is the absolute value; the sign is the
		// caller's business.
		{"-1.2", "12", 1},
		{"-0.001", "1", 3},
		{"-12", "12", 0},

		// Denominators of 2^a and 5^b alone, where the two exponents
		// differ and the larger one wins.
		{"1/2", "5", 1},
		{"1/8", "125", 3},
		{"1/5", "2", 1},
		{"1/50", "2", 2},
		{"3/40", "75", 3},
	}
	for _, c := range cases {
		r, ok := new(big.Rat).SetString(c.in)
		if !ok {
			t.Fatalf("bad input %q", c.in)
		}
		m, ok := DecimalMagnitudeOf(r)
		if !ok {
			t.Errorf("DecimalMagnitudeOf(%s) reported non-terminating", c.in)
			continue
		}
		if m.Coefficient.String() != c.coef || m.Scale != c.scale {
			t.Errorf("DecimalMagnitudeOf(%s) = %s/10^%d, want %s/10^%d",
				c.in, m.Coefficient, m.Scale, c.coef, c.scale)
		}
		if m.Scale < 0 {
			t.Errorf("DecimalMagnitudeOf(%s): negative scale %d", c.in, m.Scale)
		}
		if m.Coefficient.Sign() < 0 {
			t.Errorf("DecimalMagnitudeOf(%s): negative coefficient %s", c.in, m.Coefficient)
		}
		// The contract: r == ±Coefficient * 10^-Scale.
		back := new(big.Rat).SetFrac(m.Coefficient,
			new(big.Int).Exp(big.NewInt(10), big.NewInt(m.Scale), nil))
		if r.Sign() < 0 {
			back.Neg(back)
		}
		if back.Cmp(r) != 0 {
			t.Errorf("DecimalMagnitudeOf(%s) round-trips to %s", c.in, back)
		}
	}
}

// A value with no finite decimal expansion gets an explicit false, never an
// approximation: this primitive feeds a validity decision as well as a
// renderer, and an invented digit count would let 1/3 pass a fractionDigits
// facet it violates.
func TestDecimalMagnitudeOfNonTerminating(t *testing.T) {
	for _, in := range []string{"1/3", "1/7", "-1/3", "2/7", "1/6", "1/300"} {
		r, ok := new(big.Rat).SetString(in)
		if !ok {
			t.Fatalf("bad input %q", in)
		}
		if m, ok := DecimalMagnitudeOf(r); ok {
			t.Errorf("DecimalMagnitudeOf(%s) = %s/10^%d, want ok=false",
				in, m.Coefficient, m.Scale)
		}
	}
	// A huge power of two together with a factor of seven is still
	// non-terminating: the 2s must not be mistaken for termination.
	den := new(big.Int).Mul(
		new(big.Int).Exp(big.NewInt(2), big.NewInt(4000), nil), big.NewInt(7))
	if _, ok := DecimalMagnitudeOf(new(big.Rat).SetFrac(big.NewInt(1), den)); ok {
		t.Error("1/(2^4000*7) reported as terminating")
	}
}

// There is no ceiling on the scale: capping it made the lexical form disagree
// with the value, printing 10^-5000 as "0" while it compared unequal to zero.
func TestDecimalMagnitudeOfExtremes(t *testing.T) {
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(5000), nil)

	m, ok := DecimalMagnitudeOf(new(big.Rat).SetInt(pow))
	if !ok || m.Scale != 0 || m.Coefficient.Cmp(pow) != 0 {
		t.Errorf("10^5000: scale=%d ok=%v, want scale 0", m.Scale, ok)
	}

	m, ok = DecimalMagnitudeOf(new(big.Rat).SetFrac(big.NewInt(1), pow))
	if !ok || m.Scale != 5000 || m.Coefficient.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("10^-5000: %s/10^%d ok=%v, want 1/10^5000", m.Coefficient, m.Scale, ok)
	}

	// The two exponents differ and the larger one wins, with the
	// coefficient scaled up to match.
	mixed := new(big.Int).Mul(
		new(big.Int).Exp(big.NewInt(2), big.NewInt(300), nil),
		new(big.Int).Exp(big.NewInt(5), big.NewInt(700), nil))
	m, ok = DecimalMagnitudeOf(new(big.Rat).SetFrac(big.NewInt(1), mixed))
	if !ok || m.Scale != 700 {
		t.Errorf("1/(2^300*5^700): scale=%d ok=%v, want 700 true", m.Scale, ok)
	}
	// 1/(2^300*5^700) written with 700 digits is 2^400.
	if want := new(big.Int).Exp(big.NewInt(2), big.NewInt(400), nil); m.Coefficient.Cmp(want) != 0 {
		t.Errorf("1/(2^300*5^700): coefficient %s, want 2^400", m.Coefficient)
	}
}

func TestDecimalMagnitudeOfNil(t *testing.T) {
	m, ok := DecimalMagnitudeOf(nil)
	if !ok || m.Scale != 0 || m.Coefficient.Sign() != 0 {
		t.Errorf("DecimalMagnitudeOf(nil) = %v, %v; want zero, true", m, ok)
	}
}
