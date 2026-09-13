package xquery

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

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
//
// Each case asserts the VALUE the query produces, not merely that Compile
// returned no error. A boundary placed one clause too early or too late does
// not always fail to parse -- it can parse into a different, still-legal
// expression that answers something else -- so a compile-only assertion holds
// whether or not the scanners agree. It also cannot distinguish "the branch
// was read correctly" from "the branch was read wrongly and happened to stay
// well-formed", which is exactly the failure mode the branch flag exists to
// prevent.
func TestConditionalBranchFLWOR(t *testing.T) {
	for _, tc := range []struct{ name, query, want string }{
		{"typed let in a then branch",
			`if (true()) then let $i as xs:integer := 1 return $i else ()`, "1"},
		{"several bindings in one clause",
			`declare function local:f() as xs:integer* {
				if (true()) then let $i := 1, $j := 2 return $i + $j else ()
			}; local:f()`, "3"},
		{"nested conditional inside the branch FLWOR",
			`if (true()) then let $i := 1 return if ($i) then 1 else 2 else 3`, "1"},
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
			}; local:f(0)`, "9"},
		{"Axes089: the return belongs to the enclosing let",
			`let $c := if (true()) then 'a' else 'b' return <td bgcolor="{$c}"/>`, ""},

		// The shapes above are all bounded before the branch flag is
		// consulted: a single binding clause is folded onto the branch FLWOR
		// by openBinding, so the scan never meets a bare clause keyword with
		// nothing open. It takes a SECOND clause in the branch for the flag
		// to decide anything, which is why the cases below are here -- they
		// are the ones that fail when branchHead is pinned false.
		{"two let clauses in a then branch",
			`if (1) then let $r := 1 let $e := 2 return $r + $e else 9`, "3"},
		{"two let clauses in an else branch",
			`if (0) then 9 else let $r := 1 let $e := 2 return $r + $e`, "3"},
		{"the enclosing return is not the branch FLWOR's",
			`let $q := 1 return if ($q) then let $r := 1 let $e := 2 return $r + $e else 9`, "3"},
		// Two conditionals deep, the inner "else" is met with the stack
		// already emptied by the outer one, so only the flag says the FLWOR
		// after it opens the branch rather than continuing an enclosing one.
		{"nested else branches each opening a FLWOR",
			`declare function local:f($a as xs:integer, $e as xs:integer) as xs:integer {
				if ($e) then $a
				else
					let $arg := $a idiv 128
					let $act := $a mod 8
					return
						if ($act eq 6) then $a
						else
							let $srs := if ($act eq 1) then (1, -1, -1) else (-1, -1, -1)
							let $sh := $srs[1]
							return $sh
			}; local:f(1, 0)`, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seq, err := evalQ(t, tc.query)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if got := branchText(t, seq); got != tc.want {
				t.Errorf("= %q, want %q; the branch boundary was placed "+
					"somewhere other than where [42] ExprSingle puts it",
					got, tc.want)
			}
		})
	}
}

// branchText is the string value of a one-item result. The Axes089 case
// returns an element, the rest return integers.
func branchText(t *testing.T, seq xdm.Sequence) string {
	t.Helper()
	if len(seq) != 1 {
		t.Fatalf("want 1 item, got %d", len(seq))
	}
	switch v := seq[0].(type) {
	case *xdm.Atomic:
		return v.String()
	case *xdm.Node:
		return v.StringValue()
	}
	t.Fatalf("unexpected item type %T", seq[0])
	return ""
}
