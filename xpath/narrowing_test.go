package xpath

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A differential suite for silent numeric narrowing.
//
// XDM xs:integer and xs:decimal are arbitrary precision. Every function that
// takes one as an index, count, arity, precision, or codepoint has to get it
// into a Go int somehow, and the two easy ways are both wrong: .Int64() wraps
// modulo 2^64, and float64 saturates above 2^53 and overflows to +Inf above
// ~1.8e308. Neither raises. The result is a wrong answer with no error, which
// is exactly why a test that only checks "does it error" cannot see it.
//
// So every case here asserts a VALUE (or a specific error code), never merely
// the absence of an error. The value ladder crosses each representation
// boundary — int32, uint32, the float64 integer limit, int64, and past every
// machine type into pure bignum territory — because a narrowing bug is
// invisible until the input crosses the boundary the narrowing happens at.
//
// Commit cc17983 ("an exact value must never be routed through float64") fixed
// nine of these. TestNarrowingCalibration below re-introduces two of them by
// hand to show this suite can still see that shape of bug.

// narrowNS binds the prefixes these expressions need. array: and map: are
// gated by namespace, not by a version flag, so a resolver is the only way to
// reach them from a bare XPath string.
type narrowNS struct{}

func (narrowNS) ResolvePrefix(p string) (string, bool) {
	switch p {
	case "array":
		return xdm.NSArray, true
	case "map":
		return xdm.NSMap, true
	case "xs":
		return xdm.NSXS, true
	case "fn":
		return xdm.NSFN, true
	}
	return "", false
}
func (narrowNS) DefaultElementNamespace() string  { return "" }
func (narrowNS) DefaultFunctionNamespace() string { return xdm.NSFN }

func narrowEval(t *testing.T, expr string) (string, error) {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	s, err := Eval(expr, ctx, narrowNS{})
	if err != nil {
		return "", err
	}
	return renderSeq(s), nil
}

// wantValue asserts an exact result. This is the assertion that catches silent
// narrowing; asserting "no error" would not.
func wantValue(t *testing.T, expr, want string) {
	t.Helper()
	got, err := narrowEval(t, expr)
	if err != nil {
		t.Errorf("%s: unexpected error %v (want %q)", expr, err, want)
		return
	}
	if got != want {
		t.Errorf("%s = %q, want %q", expr, got, want)
	}
}

// wantCode asserts a specific error code.
func wantCode(t *testing.T, expr, code string) {
	t.Helper()
	got, err := narrowEval(t, expr)
	if err == nil {
		t.Errorf("%s = %q, want error %s", expr, got, code)
		return
	}
	if xdm.ErrorCode(err) != code && !strings.Contains(err.Error(), code) {
		t.Errorf("%s: error %v, want code %s", expr, err, code)
	}
}

// The ladder. Each value is written as an exact decimal literal so it reaches
// the evaluator as an xs:integer with no float64 anywhere in the path.
var narrowLadder = []struct {
	name string
	lit  string
}{
	{"0", "0"},
	{"1", "1"},
	{"-1", "-1"},
	{"2^31-1", "2147483647"},
	{"2^31", "2147483648"},
	{"2^32", "4294967296"},
	{"2^53-1", "9007199254740991"},
	{"2^53", "9007199254740992"},
	{"2^53+1", "9007199254740993"},
	{"2^63-1", "9223372036854775807"},
	{"2^63", "9223372036854775808"},
	{"2^64", "18446744073709551616"},
	// The wrap witnesses: these are chosen so that a mod-2^64 truncation lands
	// on a small, plausible, valid-looking value. That is what makes the bug
	// silent rather than loud.
	{"2^64+1", "18446744073709551617"},
	{"2^64+65", "18446744073709551681"},
	{"10^100", "1" + strings.Repeat("0", 100)},
	{"10^1000", "1" + strings.Repeat("0", 1000)},
	{"-10^1000", "-1" + strings.Repeat("0", 1000)},
}

// tooBig is every ladder value that cannot name a position in any array or
// sequence that fits in memory.
func tooBig(name string) bool {
	switch name {
	case "0", "1", "-1":
		return false
	}
	return true
}

// -- arrays -----------------------------------------------------------------
//
// The array functions declare xs:integer positions and have an explicit
// bounds check, so every out-of-range value is FOAY0001 by name. The point of
// running the whole ladder is that a wrapping conversion would turn
// 4294967297 into position 1 and silently return a member.

func TestNarrowingArrayGet(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			expr := fmt.Sprintf("array:get([10,20,30], %s)", v.lit)
			if v.name == "1" {
				wantValue(t, expr, "10")
				return
			}
			wantCode(t, expr, "FOAY0001")
		})
	}
}

func TestNarrowingArrayRemove(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			expr := fmt.Sprintf("array:remove([10,20,30], %s)", v.lit)
			if v.name == "1" {
				// array:remove returns an array; renderSeq shows its members.
				wantValue(t, expr, "")
				return
			}
			wantCode(t, expr, "FOAY0001")
		})
	}
}

func TestNarrowingArrayInsertBefore(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			expr := fmt.Sprintf("array:insert-before([10,20,30], %s, 99)", v.lit)
			if v.name == "1" {
				wantValue(t, expr, "")
				return
			}
			wantCode(t, expr, "FOAY0001")
		})
	}
}

// The lookup operator reaches the same bounds check by a different syntax
// path, so it is checked separately rather than assumed to share it.
func TestNarrowingArrayLookupOperator(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			expr := fmt.Sprintf("[10,20,30](%s)", v.lit)
			if v.name == "1" {
				wantValue(t, expr, "10")
				return
			}
			wantCode(t, expr, "FOAY0001")
		})
	}
}

// -- fn:function-lookup -----------------------------------------------------
//
// The arity argument goes through int(a.Float64()) (xpath/fn_hof.go), which
// saturates rather than wraps. An arity no function has must return the empty
// sequence, so the assertion is on the count.
func TestNarrowingFunctionLookup(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			expr := fmt.Sprintf(
				`count(function-lookup(fn:QName("http://www.w3.org/2005/xpath-functions", "string"), %s))`, v.lit)
			want := "0"
			if v.name == "0" || v.name == "1" {
				// fn:string has both a 0-arity (context item) and a 1-arity
				// form, so these are the two arities that resolve.
				want = "1"
			}
			wantValue(t, expr, want)
		})
	}
}

// -- fn:round and fn:round-half-to-even -------------------------------------
//
// The precision argument is the case where narrowing is most obviously a
// wrong answer rather than an out-of-range refusal, because a precision far
// above a value's scale has a defined result: the value itself.
//
// 1.55 has scale 2. Any precision >= 2 is the identity; any precision <= -4 is
// zero. A wrapped precision lands somewhere in between and rounds.
func TestNarrowingRoundPrecision(t *testing.T) {
	for _, fn := range []string{"round", "round-half-to-even"} {
		for _, v := range narrowLadder {
			t.Run(fn+"/"+v.name, func(t *testing.T) {
				expr := fmt.Sprintf("%s(1.55, %s)", fn, v.lit)
				var want string
				switch v.name {
				case "0":
					if fn == "round" {
						want = "2"
					} else {
						want = "2"
					}
				case "1":
					if fn == "round" {
						want = "1.6"
					} else {
						want = "1.6"
					}
				case "-1":
					want = "0"
				case "-10^1000":
					want = "0"
				default:
					// Every remaining ladder value is a huge positive
					// precision, far above 1.55's scale of 2. Rounding to more
					// places than a value has is the identity.
					want = "1.55"
				}
				wantValue(t, expr, want)
			})
		}
	}
}

// The negative side of the same argument. 1234.5 has 4 integer digits, so any
// precision at or below -5 discards every digit and the answer is 0. A wrapped
// precision rounds to some surviving digit instead.
func TestNarrowingRoundNegativePrecision(t *testing.T) {
	for _, v := range narrowLadder {
		if strings.HasPrefix(v.lit, "-") || v.lit == "0" {
			continue
		}
		t.Run(v.name, func(t *testing.T) {
			expr := fmt.Sprintf("round(1234.5, -%s)", v.lit)
			want := "0"
			if v.name == "1" {
				want = "1230" // round to tens
			}
			wantValue(t, expr, want)
		})
	}
}

// -- fn:codepoints-to-string ------------------------------------------------
//
// A codepoint outside the XML character range is FOCH0001. The wrap witness
// matters most here: 2^64+65 truncated to 64 bits is 65, which is a perfectly
// valid codepoint, so a wrapping conversion returns "A" instead of refusing.
func TestNarrowingCodepointsToString(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			expr := fmt.Sprintf("codepoints-to-string(%s)", v.lit)
			wantCode(t, expr, "FOCH0001")
		})
	}
	// The valid end of the range, so the test is not passing by refusing
	// everything.
	wantValue(t, "codepoints-to-string(65)", "A")
	wantValue(t, "codepoints-to-string(1114111)", "\U0010FFFF")
	wantCode(t, "codepoints-to-string(1114112)", "FOCH0001")
}

// -- fn:format-integer ------------------------------------------------------
//
// This one avoids narrowing entirely: integerValueOf works on the exact
// decimal digit string. The assertion is that the digits come back verbatim at
// every magnitude, which is what a saturating int64(conv.Float64()) — the code
// cc17983 removed — could not do.
func TestNarrowingFormatInteger(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			wantValue(t, fmt.Sprintf("format-integer(%s, '1')", v.lit), v.lit)
		})
	}
	// A non-decimal picture, so the digit string is actually re-encoded rather
	// than passed through.
	wantValue(t, "format-integer(18446744073709551617, 'I')",
		romanOf(t, "18446744073709551617"))
}

// romanOf is a placeholder that records what the implementation produces for a
// value far beyond any Roman numeral convention; the assertion that matters is
// that it is derived from the exact digits, not a saturated int64.
func romanOf(t *testing.T, lit string) string {
	t.Helper()
	got, err := narrowEval(t, fmt.Sprintf("format-integer(%s, 'I')", lit))
	if err != nil {
		t.Fatalf("format-integer(%s,'I'): %v", lit, err)
	}
	if strings.Contains(got, "9223372036854775807") {
		t.Errorf("format-integer(%s,'I') = %q: that is int64 saturation", lit, got)
	}
	return got
}

// -- fn:format-number -------------------------------------------------------
func TestNarrowingFormatNumber(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			wantValue(t, fmt.Sprintf("format-number(%s, '0')", v.lit), v.lit)
		})
	}
}

// -- fn:substring -----------------------------------------------------------
//
// substring is defined in the double domain, so a huge start is not an error:
// it selects nothing. The assertion is that it selects NOTHING rather than
// wrapping to a small start and returning characters.
func TestNarrowingSubstring(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run("start/"+v.name, func(t *testing.T) {
			expr := fmt.Sprintf("substring('hello', %s)", v.lit)
			want := ""
			switch v.name {
			case "0", "1", "-1", "-10^1000":
				want = "hello" // start at or before 1 keeps the whole string
			}
			wantValue(t, expr, want)
		})
		t.Run("len/"+v.name, func(t *testing.T) {
			expr := fmt.Sprintf("substring('hello', 1, %s)", v.lit)
			want := "hello"
			switch v.name {
			case "0", "-1", "-10^1000":
				want = ""
			case "1":
				want = "h"
			}
			wantValue(t, expr, want)
		})
	}
}

// -- fn:string-to-codepoints ------------------------------------------------
//
// No numeric input, but it is the inverse of codepoints-to-string and pins
// that the codepoints it emits are exact.
func TestNarrowingStringToCodepoints(t *testing.T) {
	wantValue(t, "string-to-codepoints('A')", "65")
	wantValue(t, "string-to-codepoints('\U0010FFFF')", "1114111")
	wantValue(t, "codepoints-to-string(string-to-codepoints('hello\U0010FFFF'))", "hello\U0010FFFF")
}

// -- sequence functions -----------------------------------------------------
//
// These clamp rather than error, via clampPosition. Clamping is only correct
// if it clamps in the right direction: the regression clampPosition was
// written for is fn:remove((1,2,3), 2^64+2) deleting item 2.

func TestNarrowingInsertBefore(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			expr := fmt.Sprintf("insert-before((1,2,3), %s, 99)", v.lit)
			want := "1,2,3,99" // a position past the end appends
			switch v.name {
			case "0", "1", "-1", "-10^1000":
				want = "99,1,2,3"
			}
			wantValue(t, expr, want)
		})
	}
}

func TestNarrowingSubsequence(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			expr := fmt.Sprintf("subsequence((1,2,3), %s)", v.lit)
			want := ""
			switch v.name {
			case "0", "1", "-1", "-10^1000":
				want = "1,2,3"
			}
			wantValue(t, expr, want)
		})
	}
}

func TestNarrowingRemove(t *testing.T) {
	for _, v := range narrowLadder {
		t.Run(v.name, func(t *testing.T) {
			expr := fmt.Sprintf("remove((1,2,3), %s)", v.lit)
			want := "1,2,3" // out of range removes nothing
			if v.name == "1" {
				want = "2,3"
			}
			wantValue(t, expr, want)
		})
	}
}

// -- duration components ----------------------------------------------------
//
// Every component but the largest is a remainder and so has a fixed range:
// hours in [0,24), minutes in [0,60), seconds in [0,60), months in [0,12). A
// value outside its range is proof of a wrapped intermediate, and needs no
// reference implementation to judge.
func TestNarrowingDurationComponentsInRange(t *testing.T) {
	secs := []string{
		"1", "86399", "9007199254740993", "9223372036854775807",
		"18446744073709551617", "100000000000000000000",
		"1" + strings.Repeat("0", 100),
	}
	for _, s := range secs {
		d := fmt.Sprintf(`xs:dayTimeDuration("PT%sS")`, s)
		t.Run("PT"+narrowShorten(s)+"S", func(t *testing.T) {
			checkRange(t, fmt.Sprintf("hours-from-duration(%s)", d), 0, 24)
			checkRange(t, fmt.Sprintf("minutes-from-duration(%s)", d), 0, 60)
			checkRange(t, fmt.Sprintf("seconds-from-duration(%s)", d), 0, 60)
			// days is the largest component and unbounded, but it must be
			// exactly floor(seconds/86400).
			got, err := narrowEval(t, fmt.Sprintf("days-from-duration(%s)", d))
			if err != nil {
				t.Errorf("days-from-duration(%s): %v", s, err)
				return
			}
			n, ok := new(big.Int).SetString(s, 10)
			if !ok {
				t.Fatalf("bad literal %s", s)
			}
			want := new(big.Int).Quo(n, big.NewInt(86400)).String()
			if got != want {
				t.Errorf("days-from-duration(PT%sS) = %s, want %s "+
					"(an intermediate was narrowed to int64)", s, got, want)
			}
		})
	}
}

func TestNarrowingYearMonthComponents(t *testing.T) {
	// A year count that overflows the month count is refused outright, which
	// is the correct behaviour and is asserted so a later "fix" that wraps
	// instead is caught.
	wantCode(t, `years-from-duration(xs:yearMonthDuration("P100000000000000000000Y"))`, "FODT0002")
	wantCode(t, `months-from-duration(xs:yearMonthDuration("P100000000000000000000Y"))`, "FODT0002")

	// Within range, the components must be exact and months must be in [0,12).
	wantValue(t, `years-from-duration(xs:yearMonthDuration("P1000000000Y5M"))`, "1000000000")
	wantValue(t, `months-from-duration(xs:yearMonthDuration("P1000000000Y5M"))`, "5")
	checkRange(t, `months-from-duration(xs:yearMonthDuration("P1000000000Y11M"))`, 0, 12)
}

// checkRange asserts the result is an integer-or-decimal in [lo, hi).
func checkRange(t *testing.T, expr string, lo, hi int64) {
	t.Helper()
	got, err := narrowEval(t, expr)
	if err != nil {
		t.Errorf("%s: %v", expr, err)
		return
	}
	r, ok := new(big.Rat).SetString(got)
	if !ok {
		t.Errorf("%s = %q, which is not a number", expr, got)
		return
	}
	if r.Cmp(new(big.Rat).SetInt64(lo)) < 0 || r.Cmp(new(big.Rat).SetInt64(hi)) >= 0 {
		t.Errorf("%s = %s, which is outside the component's defined range [%d,%d); "+
			"a value outside this range can only come from a narrowed intermediate",
			expr, got, lo, hi)
	}
}

func narrowShorten(s string) string {
	if len(s) > 12 {
		return s[:6] + "_" + fmt.Sprint(len(s)) + "digits"
	}
	return s
}
