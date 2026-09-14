package xslt

import (
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// Tests for the numeric-predicate rules: the fourth rule of §19.8.8.9 (axis
// steps) and the first rule of §19.8.8.10 (filter expressions). Both say that
// a predicate whose static type is numeric, and which is independent of the
// focus, selects at most one node, so a crawling posture can be narrowed to
// striding. Each case cites the sentence it checks; the controls pin what the
// ordinary predicate rule of each section gives when the narrowing does not
// apply, so that a change to the detection is visible from both sides.

// analyzeIn assesses an XPath 3.0 expression with the given context posture.
func analyzeIn(t *testing.T, src string, ctx posture) (props, bool) {
	t.Helper()
	expr, err := xpath.Parse(src, nil)
	if err != nil {
		t.Fatalf("parsing %q: %v", src, err)
	}
	return analyzeExpr(expr, ctx)
}

func runNumericPredicateCases(t *testing.T, section string, cases []struct {
	expr    string
	ctx     posture
	want    props
	comment string
}) {
	t.Helper()
	for _, c := range cases {
		got, known := analyzeIn(t, c.expr, c.ctx)
		if !known {
			t.Errorf("%s: %q was not modelled, but %s gives it a rule",
				c.comment, c.expr, section)
			continue
		}
		if got != c.want {
			t.Errorf("%s: %q is %v and %v, want %v and %v",
				c.comment, c.expr, got.posture, got.sweep, c.want.posture, c.want.sweep)
		}
	}
}

// TestAnalyzeFilterNumericPredicate checks §19.8.8.10's first rule: "If all
// the following conditions are satisfied: B is crawling; The static type of P
// is a subtype of U{xs:decimal, xs:double, xs:float}, and Neither P, nor any
// operand of P, at any depth provided it has F as its focus-setting
// container, is a context item expression, an axis expression, or a call on
// a focus-dependent function then the posture is striding and the sweep is
// the sweep of B."
func TestAnalyzeFilterNumericPredicate(t *testing.T) {
	striding := props{postureStriding, sweepConsuming}
	crawling := props{postureCrawling, sweepConsuming}
	runNumericPredicateCases(t, "§19.8.8.10", []struct {
		expr    string
		ctx     posture
		want    props
		comment string
	}{
		// The rule's own examples: "(//x)[3], (//x)[$i+1],
		// (//x)[index-of($a, $b)[last()]], and (//x)[1 to 5]".
		{"(//x)[3]", postureStriding, striding,
			"a numeric literal narrows a crawling base to striding"},
		{"(//x)[1 to 5]", postureStriding, striding,
			"the spec's range example: xs:integer* is numeric"},
		{"(//x)[index-of($a, $b)[last()]]", postureStriding, striding,
			"last() inside a nested filter has that filter as its focus-setting container"},
		{"(//x)[count($x)]", postureStriding, striding,
			"§19.8.8.9's count($x) example, as a filter"},
		{"(//x)[2 + 1]", postureStriding, striding,
			"arithmetic on numeric operands is numeric"},
		{"(//x)[-1]", postureStriding, striding,
			"unary minus on a numeric operand is numeric"},

		// "the sweep is the sweep of B", and the rule reads B[P] with B
		// itself possibly a filter expression: each predicate is a filter.
		{"(//x)[@id = 'a'][3]", postureStriding, striding,
			"a later numeric predicate narrows the filter built by an earlier one"},
		{"(//x)[3][@id = 'a']", postureStriding, striding,
			"and a motionless predicate after the narrowing keeps striding"},

		// Controls: the ordinary motionless-predicate rule, "If P is
		// motionless, then the posture and sweep of B".
		{"(//x)[@id = 'a']", postureStriding, crawling,
			"a boolean predicate leaves the base crawling"},
		{"(//x)[position() = 1]", postureStriding, crawling,
			"position() = 1 is boolean, not numeric: no narrowing"},
		{"(//x)[last()]", postureStriding, roamingFreeRanging,
			"last() is numeric but focus-dependent, and §19.8.9.14 makes it roaming from a crawling context"},
		{"(//x)[count(@*)]", postureStriding, crawling,
			"count(@*) is numeric but contains an axis expression"},
		{"(//x)[number(.)]", postureStriding, roamingFreeRanging,
			"number(.) is numeric but contains the context item expression, which it absorbs"},
		{"(//x)[$i + 1]", postureStriding, crawling,
			"the spec's $i+1 example is not decided: variable types are not carried"},
		{"(child::x)[1]", postureStriding, striding,
			"a base that is not crawling is left to the motionless rule"},

		// "the first of the following that applies": the numeric rule is
		// the first, ahead of "If P is motionless", so P's own sweep plays
		// no part. count(current()/foo) is numeric, and focus-free in the
		// rule's terms -- fn:current is neither a context item expression
		// nor a focus-dependent function (§19.8.9.3 gives it the outermost
		// context posture) -- yet it is consuming, since current()/foo
		// strides. The control below shows that sweep: with a base that is
		// not crawling the rule fails, the motionless rule fails too, and
		// "Otherwise, roaming and free-ranging" applies.
		{"(//x)[count(current()/foo)]", postureStriding, striding,
			"a numeric focus-free P narrows a crawling base even when not motionless"},
		{"(child::x)[count(current()/foo)]", postureStriding, roamingFreeRanging,
			"the same P on a striding base is not motionless, so the filter roams"},
	})
}

// TestAnalyzeAxisStepNumericPredicate checks §19.8.8.9's fourth rule: "If all
// the following conditions are satisfied: The context posture is striding;
// The axis is descendant or descendant-or-self; There is a predicate P in the
// PredicateList that satisfies all the following conditions: The static type
// of P is a subtype of U{xs:decimal, xs:double, xs:float}; Neither P, nor any
// operand of P, at any depth provided it has the AxisStep S as its
// focus-setting container, is a context item expression, an axis expression,
// or a call on a focus-dependent function; then striding and consuming".
func TestAnalyzeAxisStepNumericPredicate(t *testing.T) {
	striding := props{postureStriding, sweepConsuming}
	crawling := props{postureCrawling, sweepConsuming}
	runNumericPredicateCases(t, "§19.8.8.9", []struct {
		expr    string
		ctx     posture
		want    props
		comment string
	}{
		// The rule's own examples: "descendant::section[1],
		// descendant::section[$i+1], descendant::section[count($x)]".
		{"descendant::section[1]", postureStriding, striding,
			"a numeric literal makes the descendant step striding"},
		{"descendant::section[count($x)]", postureStriding, striding,
			"count of a variable is numeric and focus-independent"},
		{"descendant-or-self::section[2]", postureStriding, striding,
			"descendant-or-self is named alongside descendant"},
		{"descendant::section[@id = 'a'][1]", postureStriding, striding,
			"\"There is a predicate P in the PredicateList\": any one suffices"},

		// Controls: the table's row "Striding, descendant, Yes: Crawling,
		// Consuming", reached when no predicate qualifies.
		{"descendant::section[@id = 'a']", postureStriding, crawling,
			"a boolean predicate leaves the table's crawling"},
		{"descendant::section[position() = 1]", postureStriding, crawling,
			"position() = 1 is boolean, not numeric: no narrowing"},
		{"descendant::section[last()]", postureStriding, roamingFreeRanging,
			"last() is numeric but focus-dependent, and §19.8.9.14 makes it roaming from a crawling context"},
		{"descendant::section[$i + 1]", postureStriding, crawling,
			"the spec's $i+1 example is not decided: variable types are not carried"},

		// The rule's other conditions: the axis and the context posture.
		{"child::section[1]", postureStriding, striding,
			"the child axis is striding by the table already"},
		{"self::section[1]", postureCrawling, props{postureCrawling, sweepMotionless},
			"the self axis from a crawling context is not narrowed"},
		{"descendant::section[1]", postureCrawling, roamingFreeRanging,
			"a crawling context posture is outside the rule"},

		// "the first of the following rules that applies": this is the
		// fourth rule, and "If the PredicateList contains a Predicate that
		// is not motionless, then ... roaming" is the fifth, so a step with
		// one qualifying P is striding whatever its other predicates do.
		// [title] is child::title from a crawling posture, which the table
		// makes roaming; on its own it makes the step roaming.
		{"descendant::section[1][title]", postureStriding, striding,
			"the fourth rule is taken before the fifth sees the non-motionless [title]"},
		{"descendant::section[title]", postureStriding, roamingFreeRanging,
			"and the fifth rule alone makes that step roaming"},
		{"descendant::section[count(current()/foo)]", postureStriding, striding,
			"a numeric focus-free P that is itself consuming still satisfies the fourth rule"},
		{"child::section[count(current()/foo)]", postureStriding, roamingFreeRanging,
			"the same P on the child axis is outside the fourth rule, and the fifth makes it roaming"},
	})
}
