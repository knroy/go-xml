package xpath

import "testing"

// TestInt64ArithmeticBoundaries pins integer + - * at the int64 boundaries,
// where the int64 fast path must hand over to exact arithmetic. Each result
// is also typed: the fast path and the exact one both give xs:integer.
func TestInt64ArithmeticBoundaries(t *testing.T) {
	const minInt = "(-9223372036854775807 - 1)"
	for _, c := range []struct{ expr, want string }{
		{"9223372036854775807 + 1", "9223372036854775808"},
		{"1 + 9223372036854775807", "9223372036854775808"},
		{minInt + " - 1", "-9223372036854775809"},
		{minInt + " + -1", "-9223372036854775809"},
		{"9223372036854775807 - -1", "9223372036854775808"},
		{"0 - " + minInt, "9223372036854775808"},
		{minInt + " * -1", "9223372036854775808"},
		{"-1 * " + minInt, "9223372036854775808"},
		{"9223372036854775807 * 2", "18446744073709551614"},
		{"4294967296 * 4294967296", "18446744073709551616"},
		{"-4294967296 * 4294967296", "-18446744073709551616"},
		{"3037000499 * 3037000499", "9223372030926249001"},
		{"9223372036854775807 + 0", "9223372036854775807"},
		{minInt + " * 1", "-9223372036854775808"},
		{minInt + " - 0", "-9223372036854775808"},
		{"9223372036854775807 - 9223372036854775807", "0"},
		{"(9223372036854775807 + 1) - 1", "9223372036854775807"},
		{"7 * -6", "-42"},
		{"0 * " + minInt, "0"},
		{"(9223372036854775807 + 1) instance of xs:integer", "true"},
		{"(2 + 3) instance of xs:integer", "true"},
		{"(1, 2, 3)[2]", "2"},
		{"(1, 2, 3)[9223372036854775807 + 1]", ""},
		{"(1, 2, 3)[2.0]", "2"},
	} {
		if got := evalStr(t, "<r/>", c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}
