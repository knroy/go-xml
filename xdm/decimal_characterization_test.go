package xdm

import (
	"math/big"
	"testing"
)

// rat parses a decimal literal into a big.Rat for the tables below.
func ratOf(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("SetString(%q) failed", s)
	}
	return r
}

// Characterization of formatDecimal, the only behaviour decimalScale exists to
// serve. These outputs are pinned so the shared-primitive refactor is proved
// not to move them.
func TestFormatDecimalCharacterization(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0"},
		{"1", "1"},
		{"12", "12"},
		{"1.2", "1.2"},
		{"12.34", "12.34"},
		{"0.001", "0.001"},
		{"0.000001", "0.000001"},
		{"100.001", "100.001"},
		{"1.00", "1"},
		{"10.0", "10"},
		{"-0.001", "-0.001"},
		{"-12.34", "-12.34"},
		{"1/3", "0.333333333333333333"},
		{"1/7", "0.142857142857142857"},
		{"-1/3", "-0.333333333333333333"},
		{"1/2", "0.5"},
		{"1/8", "0.125"},
		{"1/5", "0.2"},
	}
	for _, c := range cases {
		if got := formatDecimal(ratOf(t, c.in)); got != c.want {
			t.Errorf("formatDecimal(%s) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := formatDecimal(nil); got != "0" {
		t.Errorf("formatDecimal(nil) = %q, want \"0\"", got)
	}
}

// secondsScale is the other decimalScale caller: it wants a single number and
// accepts the fallbacks. Pin every fallback explicitly.
func TestSecondsScaleCharacterization(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 1},  // integer: the renderer's floor of one digit
		{"1", 1},  // integer
		{"12", 1}, // integer
		{"1.2", 1},
		{"12.34", 2},
		{"0.001", 3},
		{"0.000001", 6},
		{"100.001", 3},
		{"1.00", 1}, // value is an integer
		{"10.0", 1}, // value is an integer
		{"-0.001", 3},
		{"1/3", 18}, // non-terminating fallback
		{"1/7", 18}, // non-terminating fallback
		{"1/2", 1},
	}
	for _, c := range cases {
		if got := secondsScale(ratOf(t, c.in)); got != c.want {
			t.Errorf("secondsScale(%s) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Very large and very small magnitudes must be rendered exactly: capping the
// scale printed 10^-5000 as "0" while it compared unequal to zero.
func TestFormatDecimalExtremes(t *testing.T) {
	big5000 := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(5000), nil))
	if got := formatDecimal(big5000); len(got) != 5001 || got[0] != '1' {
		t.Errorf("10^5000 rendered %d chars starting %q", len(got), got[:1])
	}
	small := new(big.Rat).Inv(big5000)
	got := formatDecimal(small)
	if len(got) != 5002 || got[:3] != "0.0" || got[len(got)-1] != '1' {
		t.Errorf("10^-5000 rendered %d chars: %.10q…%q", len(got), got, got[len(got)-1:])
	}
}
