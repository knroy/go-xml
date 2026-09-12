package xslt

import (
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// Tests for the data-flow environment behind §19.8.8.2.
//
// §19.8.8.1's note says the streamability rules deliberately exclude "data
// flow analysis (tracing from the binding of a variable to its usages)", and
// that exclusion is why §19.8.8.2 gives the binding sequence a NAVIGATION
// usage: navigation is §19.4's answer for "the analysis cannot tell what is
// done with the node". Transcribed literally that refuses every quantified
// expression over a streamed binding, valid or not.
//
// The tracing separates them, and the ordinary rules do the separating once it
// exists: a range variable bound to a striding sequence yields a striding
// reference, so "$t/@value" is an attribute step from a striding posture
// (striding, motionless) and "$t/preceding-sibling::*" is a reordering axis
// from one (roaming). No new judgement is added anywhere.
//
// The three accepting cases are streamable-100, -101 and -102, which the suite
// marks "_WRONG:streamability-rules-incorrect" and expects to RUN; the
// refusing one is streamable-129, which the suite expects to raise XTSE3430.

// analyze31 is not redefined here; it lives in streamforquant_test.go.

func quantProps(t *testing.T, src string) (props, bool) {
	t.Helper()
	e, err := xpath.Parse(src, nil)
	if err != nil {
		t.Fatalf("parsing %q: %v", src, err)
	}
	return analyzeExpr(e, postureStriding)
}

func TestQuantifiedOverAStreamedBindingUsedMotionlessly(t *testing.T) {
	// streamable-100, -101 and -102. The binding is a plain striding child
	// step and the only use of $t is "$t/@value" -- an attribute step, which
	// §19.8.8.8 makes striding and motionless from a striding posture. The
	// stream is read once, to walk the transactions, and nothing reaches
	// outside a transaction's own subtree.
	//
	// The result must be GROUNDED: the value of a quantified expression is
	// an xs:boolean, so no streamed node leaves it however the binding was
	// reached.
	for _, src := range []string{
		`some $t in transaction satisfies xs:decimal($t/@value) lt -11110.0`,
		`some $t in transaction satisfies xs:decimal($t/@value) lt -200.0`,
		`every $t in transaction satisfies xs:decimal($t/@value) ge 0.0`,
	} {
		p, known := quantProps(t, src)
		wantProps(t, p, known, postureGrounded, sweepConsuming, src)
	}
}

func TestQuantifiedOverAStreamedBindingNavigatedFrom(t *testing.T) {
	// streamable-129, whose own comment says what it does: "bind a variable
	// to a streamed node and then do illicit navigation starting from the
	// variable". "$t/preceding-sibling::*[1]" reaches a sibling the stream
	// has already delivered and discarded, and §19.8.8.8's table gives no
	// entry for a reordering axis from any posture -- roaming and
	// free-ranging.
	//
	// The verdict must come back MODELLED. Withheld, the caller reports
	// nothing, which is where this case stood before the environment
	// existed.
	src := `some $t in transaction satisfies ` +
		`xs:decimal($t/preceding-sibling::*[1]/@value) lt 0.0`
	p, known := quantProps(t, src)
	if !known {
		t.Fatal("navigation from a streamed range variable was reported " +
			"unmodelled; the data-flow environment gives it a definite answer")
	}
	if p.streamable() {
		t.Errorf("got %v and %v; preceding-sibling:: from a striding range "+
			"variable is roaming and free-ranging by §19.8.8.8", p.posture, p.sweep)
	}
}

func TestQuantifiedOverAGroundedBindingIsUnchanged(t *testing.T) {
	// A grounded binding never enters the environment: §19.8.8.12's plain
	// answer is already right for it, and this is the shape §19.8.8.2's own
	// note calls out as allowed -- "some $i in 1 to 3 satisfies @grade = $i".
	p, known := quantProps(t, `some $i in 1 to 3 satisfies $i gt 2`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		"a quantified expression over a grounded binding")
}

func TestARangeVariableDoesNotEscapeItsQuantifiedExpression(t *testing.T) {
	// The environment is copied per binding rather than mutated, so a range
	// variable is invisible outside the expression that introduced it. Were
	// it to leak, the second "$t" below -- which is a different, grounded
	// variable -- would be read as striding and the whole sequence would
	// come back roaming.
	//
	// Written as a sequence of two quantified expressions over the same
	// name: the second binding is grounded, so its $t must be grounded too.
	src := `(some $t in transaction satisfies $t/@value, ` +
		`some $t in 1 to 3 satisfies $t gt 2)`
	p, known := quantProps(t, src)
	if !known {
		t.Fatal("the two-expression sequence was reported unmodelled")
	}
	if !p.streamable() {
		t.Errorf("got %v and %v; the second binding is grounded, so its $t "+
			"must not inherit the first binding's striding posture",
			p.posture, p.sweep)
	}
}

func TestAStreamedRangeVariableAbsorbedIsStillRefused(t *testing.T) {
	// The environment does not soften anything. Absorbing the bound node --
	// "$t = 'x'" atomizes it -- reads its whole subtree from a striding
	// posture, which is consuming, and the satisfies clause is a
	// higher-order operand: §19.8.1 makes a construct whose one consuming
	// operand is higher-order roaming, because the stream cannot be rewound.
	src := `some $t in transaction satisfies $t = 'x'`
	p, known := quantProps(t, src)
	if !known {
		t.Fatal("an absorbed streamed range variable was reported unmodelled")
	}
	if p.streamable() {
		t.Errorf("got %v and %v; absorbing the bound node inside a "+
			"higher-order operand cannot be streamed (§19.8.1)",
			p.posture, p.sweep)
	}
}

func TestAVariableOutsideTheEnvironmentIsStillGrounded(t *testing.T) {
	// §19.8.8.12's rule is untouched for every variable the environment does
	// not hold. This is the acceptance direction that matters most: nothing
	// in a stylesheet's ordinary variable references changed.
	p, known := quantProps(t, `$anything`)
	wantProps(t, p, known, postureGrounded, sweepMotionless,
		"a variable reference outside any range binding")
}
