package xslt

import "testing"

// fn:function-available's $arity is xs:integer, which is unbounded.
//
// The arity was read with big.Int.Int64, whose result is UNDEFINED out of
// range rather than saturating, so a huge arity arrived as its low 64 bits and
// the lookup was performed at that wrapped value: function-available('name',
// 2^64+1) wrapped to 1, found fn:name, and answered TRUE. No error was raised
// -- the boolean was simply wrong -- which is why this test asserts the value
// rather than the absence of an error.
//
// Bounding as well as converting exactly is the point. Nothing is registered
// above a modest arity, so an arity outside that range names no function and
// XSLT 3.0 makes the answer false rather than an error.
func TestFunctionAvailableArityIsNotNarrowed(t *testing.T) {
	cases := []struct {
		arity string
		want  string
	}{
		// The truthful baselines the wrapped values must not be confused with.
		{"0", "true"},  // fn:name() is context-dependent, arity 0
		{"1", "true"},  // fn:name($node)
		{"2", "false"}, // no fn:name#2

		// 2^64+1: low 64 bits are 1, which is a real fn:name arity.
		{"18446744073709551617", "false"},
		// 2^64: low 64 bits are 0, also a real fn:name arity.
		{"18446744073709551616", "false"},
		// 2^63: low 64 bits are MinInt64, a negative arity.
		{"9223372036854775808", "false"},
		// Well past any machine type, in both signs.
		{"100000000000000000000000", "false"},
		{"-100000000000000000000000", "false"},
		// Exactly representable but far above anything registered.
		{"4611686018427387904", "false"},
		// Negative arities name nothing.
		{"-1", "false"},
		{"-9223372036854775808", "false"},
	}

	for _, c := range cases {
		sheet := wrap30(`<xsl:template match="/"><xsl:value-of ` +
			`select="function-available('name', ` + c.arity + `)"/></xsl:template>`)
		got, _, err := runAssert(t, sheet, TransformOptions{})
		if err != nil {
			t.Errorf("function-available('name', %s): unexpected error %v", c.arity, err)
			continue
		}
		if got != c.want {
			t.Errorf("function-available('name', %s) = %s, want %s",
				c.arity, got, c.want)
		}
	}
}
