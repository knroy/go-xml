package xquery_test

import (
	"testing"

	"github.com/knroy/go-xml/xquery"
)

// TestConstructorAsOperand covers the substitution in operand.go: a
// constructor or a FLWOR expression as the operand of an operator xpath owns.
//
// The cases that matter are the ones where precedence decides the answer, not
// merely whether the query parses. Substituting at primary position is what
// makes those come out right, so each side of an operator, both sides at
// once, and an operand with an operator of tighter binding beside it are all
// checked.
func TestConstructorAsOperand(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		// The general comparison in both directions, which is where the
		// suite exercises this hardest: the constructor is atomized to its
		// string value and compared as an untyped atomic.
		{`10000 = <a>10000</a>`, `true`},
		{`<a>10000</a> = 10000`, `true`},
		{`<a>10000</a> = 50000`, `false`},
		{`<a>10000</a> = <a>10000</a>`, `true`},
		{`(10000,50000) = <a>50000</a>`, `true`},
		{`() = <a>10000</a>`, `false`},
		{`<a>10000</a> = ()`, `false`},
		{`<a>10000</a> = (<a>10000</a>,<b>50000</b>)`, `true`},
		{`<a>10000</a> != <a>50000</a>`, `true`},
		{`<a>10</a> < <a>20</a>`, `true`},
		{`<a>10</a> >= <a>20</a>`, `false`},

		// A value comparison over an untyped operand, and a node comparison.
		// "eq" atomizes the constructed element to an xs:untypedAtomic and
		// §3.7.1 casts that to xs:string, so the operand on the right is
		// written as one: what is being checked is that the substitution
		// leaves the comparison's own typing rules alone.
		{`<a>10</a> eq "10"`, `true`},
		{`<a>x</a> is <a>x</a>`, `false`},

		// Precedence. "+" binds tighter than "=", so the answer differs from
		// what a left-to-right rewrite would give, and "*" tighter than "+".
		{`1 + <a>2</a> = 3`, `true`},
		{`<a>2</a> + 1 = 3`, `true`},
		{`1 + <a>2</a> * 3 = 7`, `true`},
		{`<a>2</a> * 3 + 1 = 7`, `true`},

		// The operand may be a FLWOR, and may carry a predicate of its own
		// before the operator applies.
		{`(for $i in 1 to 3 return $i)[2] = 2`, `true`},
		{`<a>1</a>/string() = "1"`, `true`},
		{`sum(for $i in 1 to 3 return $i) = 6`, `true`},

		// A computed constructor is an operand on the same terms.
		{`element a {10} = 10`, `true`},

		// Boolean operators, which bind more loosely than comparison: the
		// comparison on each side must group before the "and".
		{`<a>1</a> = 1 and <b>2</b> = 2`, `true`},
		{`<a>1</a> = 2 or <b>2</b> = 2`, `true`},

		// A "<" that is the operator rather than markup, with a constructor
		// only on the other side, is still read as a comparison.
		{`<a>1</a> < 2`, `true`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

// TestOperandSubstitutionIsBounded checks that the substitution does not
// reach past the expression it was asked about.
//
// An item of a comma-separated sequence, and an item inside a constructor's
// enclosed expression, both end before the source does; a substitution that
// swallowed the rest would silently change the result rather than fail.
func TestOperandSubstitutionIsBounded(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`<a>1</a> = 1, <b>2</b> = 2`, `truetrue`},
		{`(<a>1</a> = 1, 5)`, `true5`},
		{`<r>{<a>1</a> = 1}</r>`, `<r>true</r>`},
		{`concat(<a>1</a> = 1, "|", <b>2</b> = 3)`, `true|false`},
		{`for $x in 1 to 2 return <a>{$x}</a> = 1`, `truefalse`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}
