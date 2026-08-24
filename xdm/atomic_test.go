package xdm

import (
	"math"
	"math/big"
	"testing"
)

func TestDoubleLexicalForm(t *testing.T) {
	// fn:string on a double is observable in output, and the boundaries of the
	// exponent/no-exponent switch are the part implementations get wrong.
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{math.Copysign(0, -1), "-0"},
		{1, "1"},
		{-1, "-1"},
		{1.5, "1.5"},
		{123456, "123456"},
		{999999, "999999"},
		{1e6, "1.0E6"},     // at 1e6 the canonical form switches to exponent
		{1e-6, "0.000001"}, // 1e-6 itself stays in decimal form
		{1e-7, "1.0E-7"},
		{math.Inf(1), "INF"},
		{math.Inf(-1), "-INF"},
		{math.NaN(), "NaN"},
	}
	for _, c := range cases {
		got := NewDouble(c.in).String()
		if got != c.want {
			t.Errorf("NewDouble(%v).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDecimalIsExact(t *testing.T) {
	// The reason xs:decimal is a big.Rat and not a float64: this must be exact.
	a := NewDecimal(big.NewRat(1, 10))
	b := NewDecimal(big.NewRat(2, 10))
	sum := new(big.Rat).Add(a.Rat(), b.Rat())
	if got := NewDecimal(sum).String(); got != "0.3" {
		t.Errorf("0.1 + 0.2 as xs:decimal = %q, want \"0.3\"", got)
	}
}

func TestDecimalLexicalForm(t *testing.T) {
	cases := []struct {
		num, den int64
		want     string
	}{
		{0, 1, "0"},
		{5, 1, "5"},
		{-5, 1, "-5"},
		{1, 2, "0.5"},
		{1, 4, "0.25"},
		{-3, 4, "-0.75"},
		{100, 4, "25"},
	}
	for _, c := range cases {
		got := NewDecimal(big.NewRat(c.num, c.den)).String()
		if got != c.want {
			t.Errorf("decimal %d/%d = %q, want %q", c.num, c.den, got, c.want)
		}
	}
}

func TestNumericPromotion(t *testing.T) {
	// integer -> decimal -> float -> double
	cases := []struct{ a, b, want TypeCode }{
		{TypeInteger, TypeInteger, TypeInteger},
		{TypeInteger, TypeDecimal, TypeDecimal},
		{TypeDecimal, TypeInteger, TypeDecimal},
		{TypeInteger, TypeDouble, TypeDouble},
		{TypeDecimal, TypeFloat, TypeFloat},
		{TypeFloat, TypeDouble, TypeDouble},
	}
	for _, c := range cases {
		if got := NumericPromote(c.a, c.b); got != c.want {
			t.Errorf("NumericPromote(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestFloatRoundsToFloat32(t *testing.T) {
	// xs:float operations must produce float32 results, so construction rounds.
	f := NewFloat(0.1)
	if f.Float64() == 0.1 {
		t.Error("NewFloat(0.1) kept float64 precision; want float32 rounding")
	}
	if got, want := f.Float64(), float64(float32(0.1)); got != want {
		t.Errorf("NewFloat(0.1) = %v, want %v", got, want)
	}
}

func TestIntegerLexicalForm(t *testing.T) {
	if got := NewInteger(-42).String(); got != "-42" {
		t.Errorf("NewInteger(-42) = %q", got)
	}
	// Integers must not render a fractional part even though they are rationals.
	if got := NewInteger(7).String(); got != "7" {
		t.Errorf("NewInteger(7) = %q, want \"7\"", got)
	}
}

func TestBooleanLexicalForm(t *testing.T) {
	if got := NewBoolean(true).String(); got != "true" {
		t.Errorf("true = %q", got)
	}
	if got := NewBoolean(false).String(); got != "false" {
		t.Errorf("false = %q", got)
	}
}

func TestSequenceSingle(t *testing.T) {
	if _, err := Empty().Single(); err == nil {
		t.Error("Single on empty sequence should error, not return nil silently")
	}
	if _, err := (Sequence{NewInteger(1), NewInteger(2)}).Single(); err == nil {
		t.Error("Single on 2-item sequence should error, not truncate")
	}
	it, err := One(NewInteger(1)).Single()
	if err != nil || it == nil {
		t.Errorf("Single on 1-item sequence: %v", err)
	}
}
