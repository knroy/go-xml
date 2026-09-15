package xquery

import "testing"

// The two ExprSingle scanners bound an expression by walking the source and
// stopping at the first bare clause keyword that is not part of something
// nested inside it. Which construct a keyword closes depends on the ORDER the
// open constructs were entered in, so the scanners keep a stack; see
// nesting.go.
//
// Every case below is a shape whose boundaries the flat counters that stack
// replaced could not place. The expectation for each is read off XQuery 3.1
// [42] ExprSingle and the productions it names, not off what the parser
// happens to do: a branch of a conditional [77] is an ExprSingle, so a FLWOR
// [41] opening there is nested inside the branch and its "return" is its own;
// the clauses of one FLWOR share one "return"; and a keyword with nothing
// nested open belongs to the enclosing clause.
func TestNestedExprSingleBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		// Several clauses of one FLWOR in a branch. The counter this
		// replaced opened a level per clause and the branch ran past its
		// end; the flag that patched that went false at the first word after
		// "then", so the SECOND clause read as an enclosing clause and cut
		// the branch. RexParser's p:transition writes this.
		{"two let clauses in a then branch",
			`if (1) then let $r := 1 let $e := 2 return $r else 9`},
		{"two let clauses in an else branch",
			`if (0) then 9 else let $r := 1 let $e := 2 return $r`},
		{"four let clauses in an else branch",
			`if (0) then 9 else let $a := 1 let $b := 2 let $c := 3 let $d := 4 return $a`},
		// A "for" opens a FLWOR the same way a "let" does.
		{"for then let in one branch FLWOR",
			`if (1) then for $i in (1, 2) let $j := $i return $j else ()`},

		// The branch FLWOR's return is its own; the one after the whole
		// conditional is the enclosing FLWOR's.
		{"FLWOR inside if-then, enclosing return outside",
			`let $q := 1 return if ($q) then let $r := 1 let $e := 2 return $r else 9`},
		{"conditional as a let value, return belongs to the let",
			`let $c := if (1) then let $x := 1 let $y := 2 return $x else 0 return $c`},

		// The last branch has no keyword of the construct after it, so a
		// clause of the enclosing FLWOR ends it. Leaving the binding keywords
		// out of that stop set is what let "else 0" run on through the "let"
		// after it -- RexParser's p:transition again.
		{"else branch ends at the enclosing let",
			`declare function local:f($c0 as xs:integer) as xs:integer {
				let $c1 := if ($c0 < 128) then 1
				           else if ($c0 < 55296) then let $x := 1 let $y := 2 return $x + $y
				           else 0
				let $n := $c1 + 1
				return $n
			}; local:f(0)`},
		{"else branch ends at the enclosing for",
			`declare function local:f($a as xs:integer) as xs:integer* {
				let $v := if ($a) then 1 else 0
				for $i in (1, 2)
				return $i + $v
			}; local:f(1)`},

		// A FLWOR may open a branch, in which case the keyword is not the
		// enclosing clause's. Stopping there returned an empty ExprSingle.
		{"else branch opens with a FLWOR",
			`declare function local:f($a as xs:integer) as xs:integer {
				if ($a eq 0) then 9
				else let $c := 1 let $d := 2 return $c + $d
			}; local:f(0)`},

		// An "else" opens a branch just as a "then" does, so a FLWOR written
		// there is the branch's. The scan reaches this only two conditionals
		// deep, where the inner "else" is met with the stack already emptied
		// by the outer one: the FLWOR after it then read as a clause of the
		// enclosing FLWOR and the branch was cut at the "else". RexParser's
		// p:parse is this shape.
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
			}; local:f(1, 0)`},

		// An "else" with nothing open belongs to an enclosing conditional and
		// ends the expression. Running past it swallowed the caller's own
		// else branch -- RexParser's p:parse.
		{"branch FLWOR followed by the enclosing else",
			`declare function local:f($a as xs:integer, $b as xs:integer) as xs:integer {
				if ($a) then
					if ($b) then let $s := 1 return $s
					else let $t := 2 return $t
				else 0
			}; local:f(1, 1)`},

		// typeswitch inside an else, and its default branch bounded the same
		// way a conditional's is.
		{"typeswitch in an else branch",
			`declare function local:f($a as item()) as xs:integer {
				let $v := if (false()) then 0
				          else typeswitch ($a)
				               case $i as xs:integer return let $x := 1 let $y := 2 return $x
				               default return 0
				let $w := $v + 1
				return $w
			}; local:f(1)`},
		{"typeswitch default opens with a FLWOR and ends at the enclosing let",
			`let $v := typeswitch (1)
			           case $s as xs:string return 0
			           default return let $x := 1 let $y := 2 return $x + $y
			 let $w := $v
			 return $w`},

		// switch, whose default branch is bounded like typeswitch's.
		{"switch default in a branch FLWOR",
			`let $v := switch (1)
			           case 1 return let $a := 1 let $b := 2 return $a
			           default return 0
			 let $w := $v
			 return $w`},

		// try/catch nested in a branch, and nested in itself.
		{"try/catch in a then branch",
			`if (1) then try { let $x := 1 let $y := 2 return $x } catch * { 0 } else 9`},
		{"nested try/catch inside a branch FLWOR",
			`declare function local:f() as xs:integer {
				if (1) then
					let $a := try { try { 1 } catch * { 2 } } catch * { 3 }
					let $b := 4
					return $a + $b
				else 0
			}; local:f()`},

		// A keyword inside a string literal or a comment is text, and must
		// not close anything. The stack is only reached for bare words.
		{"clause keywords inside a string literal",
			`if (1) then let $r := "return let else then" let $e := 2 return $r else "x"`},
		{"clause keywords inside a comment",
			`if (1) then (: return let else :) let $r := 1 (: let :) let $e := 2 return $r else 9`},
		{"an else spelled inside a string is not a boundary",
			`let $v := if (1) then "else" else "then" let $w := $v return $w`},

		// None of the stop words is reserved. A bare one followed by "(" is a
		// function call, which scanToStop did not test for: "empty($stack)"
		// in RexParser's p:parse cut the clause after "let $accept :=".
		{"fn:empty called in a branch clause",
			`declare function local:f($s as item()*) as xs:boolean {
				if (1) then let $a := empty($s) let $b := $a return $b
				else false()
			}; local:f(())`},
		{"fn:count called in an else branch clause",
			`let $v := if (0) then 0 else let $n := count((1, 2)) let $m := $n return $m
			 let $w := $v
			 return $w`},

		// The whole of RexParser's p:transition, reduced to its shape: an
		// if/then/else whose branches each hold a FLWOR, one of them with a
		// nested else-if chain that itself holds a FLWOR.
		{"RexParser p:transition shape",
			`declare function local:t($input as xs:string, $begin as xs:integer,
			                         $current as xs:integer, $end as xs:integer,
			                         $result as xs:integer, $state as xs:integer,
			                         $prev as xs:integer) as xs:integer* {
				if ($state eq 0) then
					let $result := $result idiv 4
					let $end := if ($end gt string-length($input)) then string-length($input) + 1 else $end
					return
						if ($result ne 0) then ($result - 1, $begin, $end)
						else (- $prev, $begin, $current - 1)
				else
					let $c0 := 1
					let $c1 :=
						if ($c0 < 128) then 1
						else if ($c0 < 55296) then
							let $a := $c0 idiv 32
							let $b := $a idiv 32
							return $a + $b
						else 0
					let $current := $current + 1
					return
						if ($c1 > 3) then ($current, $c1)
						else ($end, $result)
			}; local:t("a", 1, 1, 1, 0, 0, 0)`},
	} {
		if _, err := Compile(tc.query, Options{}); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

// Shapes where a keyword must NOT be taken as nested: the enclosing clause
// keeps it. These are the direction the stack could break in without any of
// the cases above noticing, since over-nesting swallows rather than truncates.
func TestNestedExprSingleKeepsEnclosingClause(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		want        string
	}{
		// Axes089: the branches open nothing, so the "return" after the
		// conditional is the enclosing let's.
		{"return after a conditional with simple branches",
			`let $c := if (true()) then "a" else "b" return $c`, "a"},
		// The branch FLWOR's own return is consumed by it, and the second
		// return is the outer let's.
		{"two returns, inner one the branch FLWOR's",
			`let $c := if (true()) then let $x := "a" return $x else "b" return $c`, "a"},
		// The conditional is the whole binding, and the "for" after it is the
		// enclosing FLWOR's next clause rather than part of the branch.
		{"for after a conditional binding",
			`let $v := if (true()) then 1 else 2 for $i in (10) return $i + $v`, "11"},
		// A comma at the top of an ExprSingle separates the enclosing Expr's
		// items; inside a branch FLWOR's clause it separates bindings. The
		// conditional is written inside a function body because the query
		// body itself is split at every depth-zero comma by a third scan, in
		// xquery.go, which has no nesting of its own -- a separate gap, and
		// one the stack here does not reach.
		{"several bindings in one branch clause",
			`declare function local:f() as xs:integer {
				if (true()) then let $i := 1, $j := 2 return $i + $j else 0
			}; local:f()`, "3"},
		// Exactly one branch is evaluated, XQuery 3.1 §3.8, which is why the
		// conditional is read here rather than lifted into variables.
		{"the branch not taken is not evaluated",
			`if (false()) then let $x := 1 idiv 0 return $x else "ok"`, "ok"},
	} {
		seq, err := Eval(tc.query, nil, Options{})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := seqText(t, seq); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
