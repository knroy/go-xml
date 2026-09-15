package xquery_test

import (
	"strings"
	"testing"
)

// TestTypeswitchCaseVariableScope pins XPST0008 for a clause naming another
// clause's variable.
//
// XQuery 3.1 §3.14.2: "The scope of the variable bound in a CaseClause is the
// return expression of that CaseClause." So in
//
//	typeswitch (...) case node() return $i case $i as xs:integer return 1
//
// the $i of the FIRST clause is out of scope where it is written, even though
// the second clause binds that very name. It is a STATIC error, so it must be
// reported whether or not the clause that names it is ever entered — and none
// of these three is entered, which is precisely why each answered from another
// branch instead of failing. K2-sequenceExprTypeswitch-5, -6 and -7.
func TestTypeswitchCaseVariableScope(t *testing.T) {
	for _, src := range []string{
		// The reference precedes the binding clause.
		`typeswitch (1, 2, 3) case node() return $i ` +
			`case $i as xs:integer return 1 default return true()`,
		// The reference is in the default clause, which binds nothing.
		`typeswitch (1, 2, 3) case node() return 5 ` +
			`case $i as xs:integer return 1 default return $i`,
		// The default clause binds $i; a case clause may still not see it.
		`typeswitch (1, 2, 3) case node() return 5 ` +
			`case xs:integer* return $i default $i return 1`,
	} {
		_, err := evalStrings(t, src)
		if err == nil {
			t.Errorf("%s: want XPST0008, got no error", src)
			continue
		}
		if !strings.Contains(err.Error(), "XPST0008") {
			t.Errorf("%s: want XPST0008, got %v", src, err)
		}
	}
}

// TestTypeswitchCaseVariableInScope is the other side of §3.14.2, and is what
// keeps the check from refusing valid queries.
//
// A clause may name its OWN variable, and it may name any variable an
// enclosing scope binds — including one whose name a sibling clause also
// binds, which the sibling merely shadows within its own branch. A check that
// judged names lexically rather than against the live scope would refuse every
// one of these.
func TestTypeswitchCaseVariableInScope(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// Its own variable, in the clause that binds it.
		{`typeswitch (7) case $i as xs:integer return $i default return 0`, "7"},
		// The default clause's own variable.
		{`typeswitch (<a/>) case xs:integer return 0 default $i return name($i)`, "a"},
		// An outer let, shadowed by a case clause that is not taken.
		{`let $i := 99 return typeswitch (<a/>) ` +
			`case $i as xs:integer return $i default return $i`, "99"},
		// The same outer let, in the clause that shadows it.
		{`let $i := 99 return typeswitch (7) ` +
			`case $i as xs:integer return $i default return $i`, "7"},
		// A global the prolog declares.
		{`declare variable $g := 7; typeswitch (1) ` +
			`case xs:string return $g default return $g`, "7"},
		// An enclosing for's range variable.
		{`for $x in (1, 2) return typeswitch ($x) ` +
			`case node() return $x default return $x`, "1 2"},
		// A default clause whose own variable SHADOWS the enclosing for's,
		// and a case clause that names the outer one. This is the shape an
		// earlier, lexical attempt at this check refused: it saw $d free in
		// the case clause, saw the default clause bind that name, and
		// concluded the reference was to the sibling. K2-ForExprWithout-8 is
		// this query, and it is why the check is made against the live scope.
		{`for $d in (1, 2) return typeswitch ($d) ` +
			`case $n as xs:integer return $d default $d return 0`, "1 2"},
	} {
		got, err := evalStrings(t, tc.src)
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.src, got, tc.want)
		}
	}
}
