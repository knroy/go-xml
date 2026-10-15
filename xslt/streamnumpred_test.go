package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xpath"
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
			"with no declaration in reach, $i is U{*}: not numeric"},
		{"(child::x)[1]", postureStriding, striding,
			"a base that is not crawling is left to the motionless rule"},

		// count(current()/foo) is numeric but not focus-free: XSLT §20.4.1
		// says fn:current "is deterministic, context-dependent, and
		// focus-dependent". The rule fails, P is consuming since
		// current()/foo strides, so the motionless rule fails too, and
		// "Otherwise, roaming and free-ranging" applies -- as it does below
		// with a base that is not crawling.
		{"(//x)[count(current()/foo)]", postureStriding, roamingFreeRanging,
			"fn:current is focus-dependent, so the numeric rule does not narrow"},
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
			"with no declaration in reach, $i is U{*}: not numeric"},

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
		{"descendant::section[count(current()/foo)]", postureStriding, roamingFreeRanging,
			"fn:current is focus-dependent (§20.4.1), so the fourth rule fails and the fifth applies"},
		{"child::section[count(current()/foo)]", postureStriding, roamingFreeRanging,
			"the same P on the child axis is outside the fourth rule, and the fifth makes it roaming"},
	})
}

// numericPredicateSheet declares a global xsl:param $i with the given "as"
// (none when empty) and a key, and iterates sel from a template rule in the
// streamable mode "s".
func numericPredicateSheet(as, sel string) string {
	if as != "" {
		as = ` as="` + as + `"`
	}
	return modeSheet(`<xsl:param name="i"` + as + ` select="1"/>
		<xsl:key name="k" match="x" use="@id"/>
		<xsl:template match="/" mode="s">
			<xsl:for-each select="` + sel + `"><xsl:value-of select="."/></xsl:for-each>
		</xsl:template>`)
}

// TestStreamableNumericPredicateFocusDependentCall pins the second condition
// of §19.8.8.9's fourth rule against the whole focusDependent table: key#2,
// current#0 and the rest are focus-dependent by their Properties paragraphs,
// so a numeric predicate calling one leaves the step crawling, and iterating
// it in a streamable template is XTSE3430 exactly as it is for name#0.
func TestStreamableNumericPredicateFocusDependentCall(t *testing.T) {
	for _, pred := range []string{"count(name())", "count(current())", "count(copy-of())"} {
		err := compileModeSheet(t, numericPredicateSheet("", "descendant::x["+pred+"]"))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Errorf("descendant::x[%s] calls a focus-dependent function; want XTSE3430, got: %v", pred, err)
		}
	}
	// key#2 and unparsed-entity-uri#1 are not modelled as operands, so the
	// verdict is no opinion rather than XTSE3430; what matters is that the
	// numeric rule no longer answers striding for them without looking.
	for _, pred := range []string{"count(key('k', '1'))", "count(unparsed-entity-uri('e'))"} {
		src := "descendant::x[" + pred + "]"
		if got, known := analyzeIn(t, src, postureStriding); known && got.posture == postureStriding {
			t.Errorf("%s calls a focus-dependent function; the numeric rule must not make it striding", src)
		}
	}
	// The control: a focus-free numeric predicate is still narrowed.
	if err := compileModeSheet(t, numericPredicateSheet("", "descendant::x[1 + 1]")); err != nil {
		t.Errorf("descendant::x[1 + 1] is numeric and focus-free; got: %v", err)
	}
}

// TestStreamableNumericPredicateDeclaredVariable checks the spec's own
// "descendant::section[$i+1]": the static type of $i is its declared type
// (§19.1, VarRef), so a numeric "as" makes the predicate numeric whatever
// its occurrence indicator. A variable without one is U{*}, a string is not
// numeric, and a range variable shadowing $i is not the declared $i.
func TestStreamableNumericPredicateDeclaredVariable(t *testing.T) {
	for _, as := range []string{"xs:integer", "xs:double?", "xs:positiveInteger", "xs:numeric"} {
		if err := compileModeSheet(t, numericPredicateSheet(as, "descendant::x[$i + 1]")); err != nil {
			t.Errorf("descendant::x[$i + 1] with $i as %s is numeric; got: %v", as, err)
		}
	}
	for _, c := range []struct{ as, sel string }{
		{"", "descendant::x[$i + 1]"},
		{"xs:string", "descendant::x[$i]"},
		{"xs:integer", "let $i := 'a' return descendant::x[$i]"},
		{"xs:integer", "for $i in 'a' return descendant::x[$i]"},
	} {
		err := compileModeSheet(t, numericPredicateSheet(c.as, c.sel))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Errorf("%s with $i as %q is not numeric; want XTSE3430, got: %v", c.sel, c.as, err)
		}
	}
}
