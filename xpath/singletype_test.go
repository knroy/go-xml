package xpath

import (
	"strings"
	"testing"
)

// TestCastTargetIsSingleType pins the grammar of a cast target.
//
//	[50] CastExpr   ::= ArrowExpr ( "cast" "as" SingleType )?
//	[77] SingleType ::= SimpleTypeName "?"?
//
// A SingleType carries at most "?", so the "*" and "+" that a SequenceType
// admits are not part of it. Parsing the target with parseSequenceType made
// the additive "+" of "15 cast as xs:integer + 15" an occurrence indicator,
// which swallowed the operator and reported XPST0003 for an expression the
// grammar allows.
func TestCastTargetIsSingleType(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want string // "" means the expression must parse
	}{
		// The "+" and "*" after a cast target are binary operators, because
		// the target ended at the type name.
		{"15 cast as xs:integer + 15", ""},
		{"15 cast as xs:integer * 15", ""},
		{"15 cast as xs:integer + 15 cast as xs:integer", ""},
		{"15 castable as xs:integer + 15", ""},
		// "?" is the one indicator SingleType admits, and it still binds to
		// the type rather than starting a lookup.
		{"15 cast as xs:integer? + 1", ""},
		{"15 cast as xs:integer ? + 1", ""},
		// K-SeqExprCast-1 and -2: only "?" is allowed as an occurrence
		// indicator in a cast. With nothing to the right of it the "*"/"+" is
		// an operator missing its operand, so the expression does not parse.
		// TestCastTargetMustBeSingleAtomicType covers the "castable" spelling
		// of this and the node-kind and abstract-type targets; what is new
		// here is an indicator with an operand after it, which is the shape
		// that used to be misread.
		{"'string' cast as xs:string*", "XPST0003"},
		{"'string' cast as xs:string+", "XPST0003"},
	} {
		_, err := Parse(tc.src, testNS{})
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("Parse(%q): unexpected error %v", tc.src, err)
		case tc.want != "" && err == nil:
			t.Errorf("Parse(%q): want %s, parsed cleanly", tc.src, tc.want)
		case tc.want != "" && !strings.Contains(err.Error(), tc.want):
			t.Errorf("Parse(%q): want %s, got %v", tc.src, tc.want, err)
		}
	}
}

// TestSequenceTypeKeepsAllOccurrenceIndicators guards the other half of the
// rule: only a *cast* target is a SingleType. Every other type position takes
// a SequenceType, where "*" and "+" are occurrence indicators as before.
func TestSequenceTypeKeepsAllOccurrenceIndicators(t *testing.T) {
	for _, src := range []string{
		"$x instance of xs:integer*",
		"$x instance of xs:integer+",
		"$x instance of xs:integer?",
		"$x treat as xs:integer*",
		"$x treat as xs:integer+",
	} {
		if _, err := Parse(src, testNS{}); err != nil {
			t.Errorf("Parse(%q): unexpected error %v", src, err)
		}
	}
}

// TestSingleTypeRestrictionDoesNotLeak checks that the SingleType restriction
// is confined to the cast target it was set for.
//
// It is a parser flag rather than a separate production, so the risk it
// carries is scope: a type parsed after a cast target — or nested inside the
// same expression — must still be a full SequenceType and still accept "*"
// and "+".
func TestSingleTypeRestrictionDoesNotLeak(t *testing.T) {
	for _, src := range []string{
		// A SequenceType parsed after a cast target.
		"(1 cast as xs:integer) instance of xs:integer*",
		"(1 cast as xs:integer?) instance of xs:integer+",
		// One parsed before it.
		"($x instance of xs:integer*) and (1 cast as xs:integer) = 1",
		// Two casts with a sequence type after both.
		"(1 cast as xs:integer) + (2 cast as xs:integer) instance of xs:integer*",
	} {
		if _, err := Parse(src, testNS{}); err != nil {
			t.Errorf("Parse(%q): unexpected error %v", src, err)
		}
	}
	// A function test's signature is the nested case, and it is 3.0-only.
	const nested = "(1 cast as xs:integer) instance of function(xs:integer*) as item()*"
	if _, err := ParseVersion(nested, testNS{}, XPath30); err != nil {
		t.Errorf("ParseVersion(%q, 3.0): unexpected error %v", nested, err)
	}
}
