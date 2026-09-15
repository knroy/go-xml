package xslts

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// The raw-result delivery rule, which decides whether the assertions see
// 2.3.5's raw result sequence or the document node wrapping it.
//
// This is not a judgement call the harness gets to make: run-tests.xsl states
// it outright, and the condition below is that line transcribed.
//
//	<xsl:if test="c:test/c:output/(@tree='no' and not(@serialize='yes'))">
//	  <xsl:map-entry key="'delivery-format'" select="'raw'"/>
//
// The @result-var the harness once required appears nowhere in the reference
// driver -- grep the whole runner directory for it and there is no hit -- so
// requiring it withheld the raw binding from the 34 cases that write
// <output tree="no" serialize="no"/> and nothing else. Those cases then had
// every assertion judged against a serialisation: initial-function-100b asks
// whether its result is an xs:integer and a text node holding "986572"
// answers no, however right the value behind it is.
func TestRawResultVar(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  *OutputRef
		want string
	}{{
		// The shape all ten initial-function cases in this fix use, and the
		// one the old rule turned away for want of a result-var.
		name: "tree=no serialize=no binds the ordinary name",
		out:  &OutputRef{Tree: "no", Serialize: "no"},
		want: "result",
	}, {
		// tree="no" alone is the same delivery format: run-tests.xsl tests
		// not(@serialize='yes'), which an absent attribute satisfies.
		name: "tree=no with serialize absent still binds",
		out:  &OutputRef{Tree: "no"},
		want: "result",
	}, {
		// The suite spells result-var exactly once, on initial-template-004.
		// It is honoured where written so that case keeps the name its
		// assertions use.
		name: "an explicit result-var wins",
		out:  &OutputRef{Tree: "no", ResultVar: "v"},
		want: "v",
	}, {
		// The exclusion that protects the result-document-14xx and
		// output-07xx families. They ask for the serialized delivery format
		// and assert about the text; a raw binding would answer a question
		// they never ask.
		name: "serialize=yes takes the serialized format instead",
		out:  &OutputRef{Tree: "no", Serialize: "yes"},
		want: "",
	}, {
		name: "an ordinary case keeps the document-node binding",
		out:  &OutputRef{},
		want: "",
	}, {
		// Most cases have no <output> at all, and a nil must not be a raw
		// one -- that would rebind $result for the whole suite.
		name: "no output element at all",
		out:  nil,
		want: "",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.out.rawResultVar(); got != tc.want {
				t.Errorf("rawResultVar() = %q, want %q", got, tc.want)
			}
		})
	}
}

// assert-count and assert-deep-eq, judged through the real path.
//
// Both were missing from judge.go entirely -- initial-function-002 failed
// with "unsupported assertion assert-count" rather than with a wrong answer,
// which is the harness refusing to guess rather than guessing badly. They are
// translated to XPath over $result, exactly as assert.xsl states them:
//
//	<xsl:sequence select="count($result) = number(.)"/>
//
// so the engine's own count and deep-equal decide, and a matcher written here
// cannot agree with the engine merely by construction.
//
// The result is built as a raw sequence of two integers, which is the shape
// initial-function-002 produces and the shape a document node cannot carry:
// wrapped in a tree these two items become one text node "12345678", whose
// count is 1 and which is deep-equal to neither operand.
func TestJudgeCountAndDeepEq(t *testing.T) {
	raw := &xslt.Result{Nodes: xdm.Sequence{
		xdm.NewInteger(1234), xdm.NewInteger(5678),
	}}
	var r Runner
	for _, tc := range []struct {
		name string
		a    Assertion
		want bool
	}{
		{"count matches", Assertion{Kind: "assert-count", Value: "2"}, true},
		{"count differs", Assertion{Kind: "assert-count", Value: "3"}, false},
		// The comma makes this a two-item sequence, so it must be
		// parenthesised into deep-equal's second argument rather than pasted
		// in bare -- which would be a three-argument call and an error.
		{"deep-eq matches", Assertion{Kind: "assert-deep-eq", Value: "1234,5678"}, true},
		{"deep-eq differs", Assertion{Kind: "assert-deep-eq", Value: "1234,9999"}, false},
		// The count is of the sequence, not of the tree wrapping it. A
		// serialised result would answer 1 here.
		{"count is not the tree's", Assertion{Kind: "assert-count", Value: "1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := r.judge(tc.a, raw, nil, &TestSet{}, nil, "result")
			if got != tc.want {
				t.Errorf("judge(%s %q) = %v (%s), want %v",
					tc.a.Kind, tc.a.Value, got, why, tc.want)
			}
		})
	}
}
