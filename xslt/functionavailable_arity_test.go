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
// What is refused is what cannot be REPRESENTED. fn:name is fixed-arity, so
// every case below beyond 1 is false because no such function exists, not
// because of any ceiling: a 2^20 policy cap stood here and in xpath until the
// variadic descriptor removed the allocation that justified it. For a VARIADIC
// function the same arities now resolve, which is what
// TestFunctionAvailableVariadicArityHasNoCeiling pins.
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

// fn:concat has no maximum arity, and fn:function-available must agree with
// fn:function-lookup about that.
//
// A 2^20 ceiling stood in xslt/rtfuncs.go mirroring xpath's, so
// function-available('concat', 1048577) answered false for a function
// fn:function-lookup resolves -- the same inconsistency at the XSLT level.
// Both went when the variadic descriptor made the arity stop sizing an
// allocation; this is the half that has no xpath test to cover it.
func TestFunctionAvailableVariadicArityHasNoCeiling(t *testing.T) {
	for _, c := range []struct {
		arity string
		want  string
		why   string
	}{
		{"2", "true", "fn:concat's minimum arity"},
		{"1048576", "true", "what used to be exactly the ceiling"},
		{"1048577", "true", "one past it; F&O 3.1 states no maximum"},
		{"9223372036854775807", "true",
			"representable, so it names a function; calling it is a type " +
				"error, which is a different question from whether it exists"},
		{"1", "false", "below fn:concat's declared minimum of two"},
		{"100000000000000000000000", "false",
			"outside the host int range, so it names nothing"},
		{"-1", "false", "a negative arity names nothing"},
	} {
		sheet := wrap30(`<xsl:template match="/"><xsl:value-of ` +
			`select="function-available('concat', ` + c.arity + `)"/></xsl:template>`)
		got, _, err := runAssert(t, sheet, TransformOptions{})
		if err != nil {
			t.Errorf("function-available('concat', %s): %v", c.arity, err)
			continue
		}
		if got != c.want {
			t.Errorf("function-available('concat', %s) = %s, want %s (%s)",
				c.arity, got, c.want, c.why)
		}
	}
}

// fn:function-available and fn:function-lookup must agree at every arity.
//
// They did not, and the 2^20 ceiling hid it: both answered false above the
// cap, for different reasons. Under it, function-available('concat', 101) was
// false while fn:function-lookup at 101 was true, because xslt's libraries
// implement DynamicFunctionLibrary and xpath.LookupDynamic returned early on
// their "not found" -- past the synthesis. The boundary was concatMaxArity,
// not the ceiling, so this predates the cap removal by as long as the
// synthesis has existed.
func TestFunctionAvailableAgreesWithFunctionLookup(t *testing.T) {
	for _, arity := range []string{"2", "100", "101", "1048576", "1048577"} {
		sheet := wrap30(`<xsl:template match="/"><xsl:value-of select="` +
			`function-available('concat', ` + arity + `) eq ` +
			`exists(function-lookup(QName(` +
			`'http://www.w3.org/2005/xpath-functions','concat'), ` +
			arity + `))"/></xsl:template>`)
		got, _, err := runAssert(t, sheet, TransformOptions{})
		if err != nil {
			t.Errorf("arity %s: %v", arity, err)
			continue
		}
		if got != "true" {
			t.Errorf("at arity %s function-available and function-lookup "+
				"disagree about fn:concat; one synthesis governs both, so "+
				"they cannot", arity)
		}
	}
}
