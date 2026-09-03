package xpath

import "testing"

// A hyphen is a NameChar, so a name takes it greedily — and that is correct in
// every case, including the ones that look like arithmetic.
//
// docs/known-gaps.md used to record `$e-1` as a defect, on the grounds that
// Saxon and BaseX read it as subtraction. The QT3 suite settles it the other
// way, twice over.
//
// It writes such names itself and depends on them: fo-spec-examples.xml binds
// `let $tz-10 := xs:dayTimeDuration("-PT10H")` and then uses `$tz-10` as one
// variable, and `$in-xml-1` and `$in-xml-2` account for 106 further uses.
// Reading a hyphen before a digit as subtraction would fail all of them.
//
// And it states the trailing case directly. prod/NameTest.xml's K-NameTest-3
// is `foo- foo` with the description "'foo-' is an invalid nametest.
// Whitespace is wrong", expecting XPST0003 — so a name absorbs a final hyphen
// even when whitespace follows, and the result is a syntax error rather than
// subtraction. An attempt to make `$e- 1` mean subtraction broke exactly this
// case, in all four suites at once.
//
// The rule is therefore plain longest-match with no lookahead, and the
// workaround for anyone wanting arithmetic is a space before the hyphen.
func TestHyphenIsANameChar(t *testing.T) {
	for _, tc := range []struct {
		expr  string
		valid bool
		why   string
	}{
		{`$a-b`, true, "one variable"},
		{`$tz-10`, true, "one variable; the QT3 suite writes this"},
		{`$in-xml-1`, true, "one variable; 106 uses in the suite"},
		{`$e-1`, true, "one variable named e-1, per longest match"},
		{`$e - 1`, true, "subtraction: the space ends the name"},
		{`$e -1`, true, "subtraction: the space ends the name"},
		{`$e- 1`, false, "K-NameTest-3: the name takes the hyphen, then '1' is a syntax error"},
		{`foo- foo`, false, "K-NameTest-3 itself"},
		{`foo-bar`, true, "one name test"},
		{`foo - 1`, true, "subtraction"},
	} {
		_, err := Compile(tc.expr, nil)
		switch {
		case tc.valid && err != nil:
			t.Errorf("%q should compile (%s), got %v", tc.expr, tc.why, err)
		case !tc.valid && err == nil:
			t.Errorf("%q should not compile (%s)", tc.expr, tc.why)
		}
	}
}
