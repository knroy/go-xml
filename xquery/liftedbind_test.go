package xquery

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// TestLiftedOperandReachesEveryEvaluator covers the class of bug the eval,
// evalBool and evalIn methods on compiledExpr exist to prevent.
//
// compileExpr may lift an XQuery-only primary out of an expression's source,
// leaving a "local:xq-stepN()" call in its place and recording the primary in
// ops. The function behind that call is installed by bind and by nothing
// else, so an evaluator that reaches for the compiled expression and hands it
// the bare context asks xpath for a function the query never wrote and gets
// XPST0017. That is not a wrong answer a caller would notice as a wrong
// answer; it is an error naming an invented name, and it is what a computed
// constructor's name expression and a processing instruction's target both
// did before they were routed through bind.
//
// The test is written against the type rather than against a query on
// purpose. Which queries actually reach which evaluator with a lifted operand
// is decided by two independent lexical scanners — needsXQueryParser, which
// sends a clause expression to this package's parser, and substituteOperands,
// which lifts. They agree today, which is why evalBool's old bypass never
// misbehaved; nothing states that they must, and the agreement is not
// something a future edit to either would be reminded of. So the operand is
// lifted by hand and driven through every evaluator on the type: each must
// answer the operand's value, and an evaluator added later that forgets to
// bind fails here whether or not a query can reach it yet.
func TestLiftedOperandReachesEveryEvaluator(t *testing.T) {
	sc := newStaticContext()

	// The lifted primary is the constructor <a>7</a> — exactly the shape
	// substituteOperands pulls out, because xpath cannot read it. Parsing it
	// rather than building the node by hand keeps the test honest about what
	// a real lift produces.
	cp := &parser{src: "<a>7</a>", sc: sc, version: 31}
	prim, err := cp.parseConstructorHere()
	if err != nil {
		t.Fatalf("parsing the operand: %v", err)
	}
	ops := []liftedOperand{{n: prim}}

	// call is the source substituteOperands leaves where the primary stood.
	call := callArgPrefix + ":" + stepFn(0) + "()"

	compile := func(expr string) *compiledExpr {
		t.Helper()
		c, err := xpath.CompileXQuery(expr, sc, 31)
		if err != nil {
			t.Fatalf("compiling %q: %v", expr, err)
		}
		return &compiledExpr{src: expr, xpc: c, sc: sc, ops: ops}
	}

	ctx := &evalContext{xp: xpath.NewContext(nil, xpath.Builtins()), sc: sc}

	// eval, which every constructor's enclosed expression, a function body, a
	// computed name and a PI target all now go through.
	seq, err := compile("number(" + call + ")").eval(ctx)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := seqString(t, seq); got != "7" {
		t.Errorf("eval = %q, want %q", got, "7")
	}

	// evalBool, the path a "where" clause, a "satisfies" clause and a window
	// clause's conditions take. This is the one that used to call EvalBool on
	// the bare context, and it is the previously latent case: with the bypass
	// restored, this call reports XPST0017 for local:xq-step0.
	ok, err := compile("number(" + call + ") = 7").evalBool(ctx)
	if err != nil {
		t.Fatalf("evalBool: %v", err)
	}
	if !ok {
		t.Error("evalBool = false, want true")
	}

	// evalIn, which a nested call's arguments, a parenthesised path's left
	// half and a standalone type check use. The decoration must compose on
	// top of bind's library rather than replace it, so an expression that
	// needs both the lifted operand and a caller-bound variable must see
	// both — replacing the library instead loses the operand silently.
	extra := xdm.QName{URI: nsLocal, Local: "extra"}
	seq, err = compile("number("+call+") + $"+callArgPrefix+":extra").
		evalIn(ctx, func(xp *xpath.Context) *xpath.Context {
			return xp.WithVar(extra, xdm.One(xdm.NewInteger(1)))
		})
	if err != nil {
		t.Fatalf("evalIn: %v", err)
	}
	if got := seqString(t, seq); got != "8" {
		t.Errorf("evalIn = %q, want %q", got, "8")
	}
}

// TestInspectDoesNotEvaluate pins the other half of the split: inspect hands
// back the compiled form for static analysis, and answers nil for an
// expression xpath never compiled, so a caller that walks it has to say what
// it does with the absent case rather than dereferencing a nil.
func TestInspectDoesNotEvaluate(t *testing.T) {
	sc := newStaticContext()
	c, err := xpath.CompileXQuery("fn:count((1,2))", sc, 31)
	if err != nil {
		t.Fatalf("compiling: %v", err)
	}
	if got := (&compiledExpr{xpc: c, sc: sc}).inspect(); got == nil {
		t.Error("inspect on a compiled expression = nil, want the compiled form")
	}
	// A constructor is held in items and never reaches xpath.
	if got := (&compiledExpr{items: []node{}}).inspect(); got != nil {
		t.Error("inspect on a parsed expression, want nil")
	}
	// The nil receiver is the shape checkStaticCalls relies on: a
	// declaration with no expression at all.
	if got := (*compiledExpr)(nil).inspect(); got != nil {
		t.Error("inspect on a nil expression, want nil")
	}
}

// seqString renders a sequence through the same atomization a caller would
// see, which is enough for the single numbers these expressions produce.
func seqString(t *testing.T, seq xdm.Sequence) string {
	t.Helper()
	atoms, err := xdm.AtomizeChecked(seq)
	if err != nil {
		t.Fatalf("atomizing the result: %v", err)
	}
	out := ""
	for _, a := range atoms {
		out += a.(*xdm.Atomic).String()
	}
	return out
}
