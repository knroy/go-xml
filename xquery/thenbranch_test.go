package xquery

import "testing"

// A FLWOR written unparenthesised in a branch of a conditional belongs to that
// branch, not to whatever encloses the conditional.
//
// Two scans have to agree about that. scanExprSingleSource decides how much
// source parseIf probes, and stopped at the "let" after "then" -- the extent
// came back as "if (..) then", needsQueryParser saw no XQuery-only construct in
// it, and the whole conditional went to xpath, which has no typed let. That is
// the "XPST0003: a constructor or FLWOR expression cannot appear here" the
// app-Demos sudoku case reported at offset 0. scanToStop then decides where the
// branch itself ends, and ended it at the first comma or "else" it met, both of
// which may belong to the nested FLWOR or to a nested "if".
//
// The cases below are the shapes sudoku writes, reduced. Axes089 is here too:
// its conditional has literal branches and its "return" is the enclosing let's,
// which is what the branch flag must not swallow.
func TestConditionalBranchFLWOR(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"typed let in a then branch",
			`if (true()) then let $i as xs:integer := 1 return $i else ()`},
		{"several bindings in one clause",
			`declare function local:f() as xs:integer* {
				if (true()) then let $i := 1, $j := 2 return $i + $j else ()
			}; local:f()`},
		{"nested conditional inside the branch FLWOR",
			`if (true()) then let $i := 1 return if ($i) then 1 else 2 else 3`},
		{"conditional chain after the branch FLWOR",
			`declare function local:f($c as xs:integer) as xs:integer* {
				if ($c eq 0)
				then let $i as xs:integer := 1,
				         $p as xs:integer* := (1, 2)
				     return if (count($p) > 1)
				            then 9
				            else if (count($p) = 1)
				            then let $n as xs:integer+ := (1, 2) return local:f(0)
				            else ()
				else $c
			}; local:f(0)`},
		{"Axes089: the return belongs to the enclosing let",
			`let $c := if (true()) then 'a' else 'b' return <td bgcolor="{$c}"/>`},
	} {
		if _, err := Compile(tc.query, Options{}); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}
