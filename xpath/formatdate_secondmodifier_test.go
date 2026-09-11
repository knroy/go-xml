package xpath

import "testing"

// F&O 3.0 section 9.8.4.1, functions-and-operators-rec30.xml:18686-18706 and
// :18709: a first presentation modifier "may optionally be followed by a
// second presentation modifier", and the grammar admits four of them in two
// independent pairs -- "either a or t" for alphabetic or traditional
// numbering, and "either c or o" for cardinal or ordinal.
//
// Only "t" and "o" were recognised. "a" and "c" were left in the modifier
// string, which failed in two different ways: where the first modifier was a
// digit pattern the stray letter reached parseDigitPattern and raised
// FOFD1340 on a picture the grammar admits, and where it was not, the letter
// was swallowed into the sequence name, which then matched nothing and fell
// back to the default numbering -- "[MNna]" lost the month name entirely.
func TestFormatDateSecondPresentationModifierAccepted(t *testing.T) {
	cases := []struct{ expr, want, why string }{
		// A digit pattern followed by a second modifier: FOFD1340 before.
		{`format-date(xs:date("2003-09-07"), "[M1a]")`, "9", "digit pattern + alphabetic"},
		{`format-date(xs:date("2003-09-07"), "[M01a]")`, "09", "padded digit pattern + alphabetic"},
		{`format-date(xs:date("2003-09-07"), "[D1a]")`, "7", "day, digit pattern + alphabetic"},
		{`format-date(xs:date("2003-09-07"), "[M1c]")`, "9", "digit pattern + cardinal"},
		{`format-date(xs:date("2003-09-07"), "[M01c]")`, "09", "padded digit pattern + cardinal"},
		// A named sequence followed by a second modifier: silently degraded
		// to the default numbering before, rather than raising.
		{`format-date(xs:date("2003-09-07"), "[Mia]")`, "ix", "roman + alphabetic"},
		{`format-date(xs:date("2003-09-07"), "[Mic]")`, "ix", "roman + cardinal"},
		{`format-date(xs:date("2003-09-07"), "[Mwa]")`, "nine", "spelled + alphabetic"},
		{`format-date(xs:date("2003-09-07"), "[Mwc]")`, "nine", "spelled + cardinal"},
		{`format-date(xs:date("2003-09-07"), "[MNna]")`, "September", "by-name + alphabetic"},

		// Controls. A lone "a" is the alphabetic FIRST modifier and a lone
		// "i" roman, NOT an empty first modifier with a second appended;
		// "o" and "t" are not first modifiers, so a lone one of those is a
		// second modifier against the default presentation. These were
		// correct before and must stay correct.
		{`format-date(xs:date("2003-09-07"), "[Ma]")`, "i", "lone a is the first modifier"},
		{`format-date(xs:date("2003-09-07"), "[Mt]")`, "9", "lone t is a second modifier"},
		{`format-date(xs:date("2003-09-07"), "[Mo]")`, "9th", "lone o is a second modifier"},
		{`format-date(xs:date("2003-09-07"), "[Mi]")`, "ix", "roman alone"},
		{`format-date(xs:date("2003-09-07"), "[Mw]")`, "nine", "spelled alone"},
		{`format-date(xs:date("2003-09-07"), "[MNn]")`, "September", "by-name alone"},
		{`format-date(xs:date("2003-09-07"), "[M1o]")`, "9th", "ordinal still applies"},
		// The width modifier still splits on the last comma, second
		// modifier or not.
		{`format-date(xs:date("2003-09-07"), "[MNn,*-3]")`, "Sep", "width with by-name"},
		{`format-date(xs:date("2003-09-07"), "[M01a,*-1]")`, "9", "width after a second modifier"},
	}
	for _, c := range cases {
		got := evalStrXSLT(t, `<r/>`, c.expr)
		if got != c.want {
			t.Errorf("%s (%s)\n  got  %q\n  want %q", c.expr, c.why, got, c.want)
		}
	}
}
