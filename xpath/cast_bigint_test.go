package xpath

import (
	"strings"
	"testing"
)

// bigLit returns the literal "1" followed by n zeros.
func bigLit(n int) string { return "1" + strings.Repeat("0", n) }

// shorten keeps a failure message readable when the value is thousands of
// digits long.
func shorten(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[:30] + "..." + s[len(s)-25:]
}

func evalBig30(t *testing.T, expr string) (string, error) {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath30
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		return "", err
	}
	if len(seq) != 1 {
		return "", nil
	}
	return seq[0].(interface{ String() string }).String(), nil
}

// TestCastNumericArbitraryPrecision pins that casting between the two exact
// numeric types answers from the exact value and never from a float64
// projection of it.
//
// castToNumeric's NaN/infinite guard used to ask math.IsInf(a.Float64(), 0).
// Float64() on an arbitrary-precision xs:integer or xs:decimal overflows to
// +Inf above the float64 range, so a finite exact value of 10^309 was
// reported as infinite and raised FOCA0002 -- a cast that has an exact answer
// refused to produce it. The transition sat exactly on the float64 exponent
// limit (10^308 fine, 10^309 not), which is not a boundary the XPath data
// model has.
//
// Every case asserts the VALUE, not merely the absence of an error: a guard
// that admitted the value but then narrowed it would raise nothing.
func TestCastNumericArbitraryPrecision(t *testing.T) {
	cases := []struct{ name, expr, want string }{
		// Around the int64 boundary, where nothing exotic happens yet.
		{"decimal 10^17 to integer", "xs:integer(xs:decimal('" + bigLit(17) + "'))", bigLit(17)},
		{"decimal 10^18 to integer", "xs:integer(xs:decimal('" + bigLit(18) + "'))", bigLit(18)},
		{"decimal 10^19 to integer", "xs:integer(xs:decimal('" + bigLit(19) + "'))", bigLit(19)},

		// 2^53 is where a double stops holding consecutive integers. An exact
		// type must still tell 2^53 and 2^53+1 apart.
		{"2^53 to integer", "xs:integer(xs:decimal('9007199254740992'))", "9007199254740992"},
		{"2^53+1 to integer", "xs:integer(xs:decimal('9007199254740993'))", "9007199254740993"},
		{"2^53+1 to decimal", "xs:decimal(xs:integer('9007199254740993'))", "9007199254740993"},

		// The float64 exponent limit. 10^308 always worked; 10^309 is the
		// first value the old guard called infinite.
		{"decimal 10^308 to integer", "xs:integer(xs:decimal('" + bigLit(308) + "'))", bigLit(308)},
		{"decimal 10^309 to integer", "xs:integer(xs:decimal('" + bigLit(309) + "'))", bigLit(309)},
		{"decimal 10^400 to integer", "xs:integer(xs:decimal('" + bigLit(400) + "'))", bigLit(400)},
		{"decimal 10^4096 to integer", "xs:integer(xs:decimal('" + bigLit(4096) + "'))", bigLit(4096)},

		{"integer 10^308 to decimal", "xs:decimal(xs:integer('" + bigLit(308) + "'))", bigLit(308)},
		{"integer 10^309 to decimal", "xs:decimal(xs:integer('" + bigLit(309) + "'))", bigLit(309)},
		{"integer 10^400 to decimal", "xs:decimal(xs:integer('" + bigLit(400) + "'))", bigLit(400)},
		{"integer 10^4096 to decimal", "xs:decimal(xs:integer('" + bigLit(4096) + "'))", bigLit(4096)},

		// A huge decimal with a fractional part. The cast to integer
		// truncates toward zero, so the fraction must vanish and every one of
		// the 401 integral digits must survive.
		{"huge decimal .5 to integer", "xs:integer(xs:decimal('" + bigLit(400) + ".5'))", bigLit(400)},
		{"huge decimal .9 to integer", "xs:integer(xs:decimal('" + bigLit(309) + ".9'))", bigLit(309)},
		{"huge negative decimal .9 to integer",
			"xs:integer(xs:decimal('-" + bigLit(400) + ".9'))", "-" + bigLit(400)},

		// Negatives, which the old guard rejected symmetrically because
		// Float64() underflows to -Inf.
		{"negative decimal 10^400 to integer",
			"xs:integer(xs:decimal('-" + bigLit(400) + "'))", "-" + bigLit(400)},
		{"negative integer 10^400 to decimal",
			"xs:decimal(xs:integer('-" + bigLit(400) + "'))", "-" + bigLit(400)},

		// The fractional part must be kept when the target keeps fractions.
		{"huge decimal to decimal is identity",
			"string(xs:decimal('" + bigLit(400) + ".25') + xs:decimal('0'))", bigLit(400) + ".25"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := evalString(t, c.expr)
			if err != nil {
				t.Fatalf("%s: unexpected error %v", c.expr, err)
			}
			if got != c.want {
				t.Errorf("%s\n got %s\nwant %s", c.name, shorten(got), shorten(c.want))
			}
		})
	}
}

// TestCastStillRejectsNaNAndInfinity is the regression guard on the fix
// above: the false reject must not have been cured by dropping the check.
//
// Only xs:double and xs:float can be NaN or infinite, and casting one of
// those to an exact type has no answer, so it must still raise FOCA0002.
// Without this, "fix" and "delete the guard" are indistinguishable.
func TestCastStillRejectsNaNAndInfinity(t *testing.T) {
	for _, expr := range []string{
		"xs:integer(xs:double('INF'))",
		"xs:integer(xs:double('-INF'))",
		"xs:integer(xs:double('NaN'))",
		"xs:integer(xs:float('INF'))",
		"xs:integer(xs:float('-INF'))",
		"xs:integer(xs:float('NaN'))",
		"xs:decimal(xs:double('INF'))",
		"xs:decimal(xs:double('-INF'))",
		"xs:decimal(xs:double('NaN'))",
		"xs:decimal(xs:float('INF'))",
		"xs:decimal(xs:float('-INF'))",
		"xs:decimal(xs:float('NaN'))",
	} {
		if _, err := evalString(t, expr); err == nil {
			t.Errorf("%s: expected FOCA0002, got success", expr)
		} else if !strings.Contains(err.Error(), "FOCA0002") {
			t.Errorf("%s: expected FOCA0002, got %v", expr, err)
		}
	}

	// "castable as" asks the same question and must answer false, not raise.
	for _, c := range []struct{ expr, want string }{
		{"xs:double('INF') castable as xs:integer", "false"},
		{"xs:double('NaN') castable as xs:decimal", "false"},
		// A finite huge value IS castable, which is the other half of the fix.
		{"xs:decimal('" + bigLit(400) + "') castable as xs:integer", "true"},
		{"xs:integer('" + bigLit(400) + "') castable as xs:decimal", "true"},
	} {
		got, err := evalString(t, c.expr)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.expr, err)
		} else if got != c.want {
			t.Errorf("%s: got %s want %s", c.expr, got, c.want)
		}
	}

	// A finite double, however large in magnitude, casts fine. This pins that
	// the guard tests the value's own infinity and not its size.
	if got, err := evalString(t, "xs:integer(xs:double('1e300'))"); err != nil {
		t.Errorf("xs:integer(1e300): unexpected error %v", err)
	} else if len(got) != 301 || !strings.HasPrefix(got, "1") {
		t.Errorf("xs:integer(1e300): got %s, want a 301-digit value", shorten(got))
	}
}

// TestFormatIntegerArbitraryPrecision pins that fn:format-integer formats the
// exact xs:integer it is given.
//
// integerValueOf used to return int64(conv.Float64()). That failed SILENTLY:
// Float64() on an arbitrary-precision xs:integer overflows the float64 range
// and the int64 conversion then saturates, so format-integer(10^400, '1')
// answered "9223372036854775807"; and below the overflow the double simply
// lost precision, so 123456789012345678 came back as 123456789012345680. No
// error was raised in either case, which is why every assertion here is on
// the VALUE. An error-only test cannot see this defect at all.
//
// fn:format-integer takes $value as xs:integer?, which is unbounded, and the
// spec gives no error code for a value that is merely large -- so the correct
// behaviour is to format it, not to refuse it.
func TestFormatIntegerArbitraryPrecision(t *testing.T) {
	cases := []struct{ name, expr, want string }{
		// Exact within int64, but past the 2^53 mark where a double stops
		// holding consecutive integers.
		{"10^17", "format-integer(" + bigLit(17) + ", '1')", bigLit(17)},
		{"10^18", "format-integer(" + bigLit(18) + ", '1')", bigLit(18)},
		{"2^53", "format-integer(9007199254740992, '1')", "9007199254740992"},
		{"2^53+1", "format-integer(9007199254740993, '1')", "9007199254740993"},
		{"17 significant digits", "format-integer(123456789012345678, '1')", "123456789012345678"},
		{"maxint64", "format-integer(9223372036854775807, '1')", "9223372036854775807"},

		// Past int64 entirely, where the old code saturated to MaxInt64.
		{"10^19", "format-integer(xs:integer('" + bigLit(19) + "'), '1')", bigLit(19)},
		{"10^308", "format-integer(xs:integer('" + bigLit(308) + "'), '1')", bigLit(308)},
		{"10^309", "format-integer(xs:integer('" + bigLit(309) + "'), '1')", bigLit(309)},
		{"10^400", "format-integer(xs:integer('" + bigLit(400) + "'), '1')", bigLit(400)},
		{"10^4096", "format-integer(xs:integer('" + bigLit(4096) + "'), '1')", bigLit(4096)},

		// Negatives: the sign is prepended to the formatted absolute value.
		{"-10^400", "format-integer(xs:integer('-" + bigLit(400) + "'), '1')", "-" + bigLit(400)},
		{"-17 digits", "format-integer(-123456789012345678, '1')", "-123456789012345678"},
		{"minint64", "format-integer(-9223372036854775808, '1')", "-9223372036854775808"},

		// Mandatory-digit padding and grouping must work on the exact digits.
		{"padding beyond int64",
			"format-integer(xs:integer('" + bigLit(19) + "'), '0000000000000000000000000')",
			"00000" + bigLit(19)},
		{"grouping at 10^19",
			"format-integer(xs:integer('" + bigLit(19) + "'), '#,##0')",
			"10,000,000,000,000,000,000"},

		// A named sequence has no representation this far out, and the spec's
		// answer for an unsupported value is the fallback decimal string --
		// which must still be the EXACT digits.
		{"roman beyond range", "format-integer(xs:integer('" + bigLit(400) + "'), 'I')", bigLit(400)},
		{"words beyond range", "format-integer(xs:integer('" + bigLit(400) + "'), 'w')", bigLit(400)},
		{"alpha beyond range", "format-integer(xs:integer('" + bigLit(400) + "'), 'a')", bigLit(400)},
		{"unrecognised token", "format-integer(xs:integer('" + bigLit(400) + "'), '#')", bigLit(400)},

		// The named sequences must still work inside their own range.
		{"roman in range", "format-integer(1984, 'I')", "MCMLXXXIV"},
		{"words in range", "format-integer(21, 'w')", "twenty-one"},
		{"alpha in range", "format-integer(27, 'a')", "aa"},

		// An ordinal suffix depends only on the last two digits, so it must
		// be right for a value no int64 can hold.
		{"ordinal at 10^400", "format-integer(xs:integer('" + bigLit(400) + "'), '1;o')", bigLit(400) + "th"},
		{"ordinal 21 beyond int64",
			"format-integer(xs:integer('" + bigLit(400) + "21'), '1;o')", bigLit(400) + "21st"},
		{"ordinal 12 beyond int64",
			"format-integer(xs:integer('" + bigLit(400) + "12'), '1;o')", bigLit(400) + "12th"},

		// Zero and the small values, which must not have regressed.
		{"zero", "format-integer(0, '1')", "0"},
		{"one", "format-integer(1, '1')", "1"},
		{"empty", "format-integer((), '1')", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := evalBig30(t, c.expr)
			if err != nil {
				t.Fatalf("%s: unexpected error %v", c.expr, err)
			}
			if got != c.want {
				t.Errorf("%s\n got %s\nwant %s", c.name, shorten(got), shorten(c.want))
			}
		})
	}
}

// TestScaleDurationArbitraryPrecision covers a FOURTH instance of the same
// pattern, found while fixing the two casts above and reported alongside them.
//
// scaleDuration's infinity check asked math.IsInf(n.Float64(), 0) of the
// scaling factor. An xs:integer or xs:decimal factor beyond the float64 range
// therefore took the infinity path: multiplying reported FODT0002 without ever
// looking at whether the result actually overflowed, and -- worse, because it
// is silent -- dividing by a huge finite factor took the "dividing by an
// infinity shrinks the duration to nothing" branch and returned PT0S for what
// is an ordinary, representable division.
//
// A genuine overflow is still caught: ratFitsInt and the math.MaxInt check
// below the branch test the scaled value itself, which is the right place for
// it.
func TestScaleDurationArbitraryPrecision(t *testing.T) {
	for _, c := range []struct{ name, expr, want string }{
		// Dividing one second by 10^309 is a tiny but perfectly ordinary
		// duration. The old code silently answered PT0S.
		{"div by 10^309 is not zero",
			"xs:dayTimeDuration('PT1S') div xs:integer('" + bigLit(309) + "') eq xs:dayTimeDuration('PT0S')",
			"false"},
		{"div by 10^400 is not zero",
			"xs:dayTimeDuration('PT1S') div xs:integer('" + bigLit(400) + "') eq xs:dayTimeDuration('PT0S')",
			"false"},
		// Dividing by a huge factor and multiplying back is the identity, so
		// the intermediate kept its value rather than collapsing.
		{"div then mul round-trips",
			"(xs:dayTimeDuration('PT1S') div xs:integer('" + bigLit(400) + "')) * xs:integer('" + bigLit(400) + "') eq xs:dayTimeDuration('PT1S')",
			"true"},
		// A finite factor just below and just above the float64 limit must
		// behave the same way as each other.
		{"div by 10^308 is not zero",
			"xs:dayTimeDuration('PT1S') div xs:integer('" + bigLit(308) + "') eq xs:dayTimeDuration('PT0S')",
			"false"},

		// Dividing by an actual infinity still shrinks to nothing.
		{"div by INF is zero",
			"xs:dayTimeDuration('PT1S') div xs:double('INF') eq xs:dayTimeDuration('PT0S')",
			"true"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := evalString(t, c.expr)
			if err != nil {
				t.Fatalf("%s: unexpected error %v", c.expr, err)
			}
			if got != c.want {
				t.Errorf("%s: got %s want %s", c.name, got, c.want)
			}
		})
	}

	// Multiplying by an actual infinity still overflows, and so does
	// multiplying by a finite factor whose RESULT genuinely does not fit.
	// The second is what proves the fix moved the check rather than removed
	// it: the huge factor now reaches the real range test below.
	for _, expr := range []string{
		"xs:dayTimeDuration('PT1S') * xs:double('INF')",
		"xs:dayTimeDuration('PT1S') * xs:double('-INF')",
		"xs:yearMonthDuration('P1M') * xs:integer('" + bigLit(400) + "')",
	} {
		if _, err := evalString(t, expr); err == nil {
			t.Errorf("%s: expected FODT0002, got success", expr)
		} else if !strings.Contains(err.Error(), "FODT0002") {
			t.Errorf("%s: expected FODT0002, got %v", expr, err)
		}
	}

	// Scaling by NaN is still FOCA0005.
	if _, err := evalString(t, "xs:dayTimeDuration('PT1S') * xs:double('NaN')"); err == nil ||
		!strings.Contains(err.Error(), "FOCA0005") {
		t.Errorf("PT1S * NaN: expected FOCA0005, got %v", err)
	}
}
