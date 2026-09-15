package xpath

import (
	"context"

	"github.com/knroy/go-xml/xdm"
)

// Optimisation runs between parsing and evaluation, rewriting the tree into an
// equivalent one that is cheaper to evaluate.
//
// Two properties make this safe to do at compile time, and both are checked
// before any rewrite:
//
//   - The expression must be *closed*: no variable reference, no context item,
//     no function whose value depends on the dynamic context. "1 + 2" folds;
//     "$n + 2" and "position() + 2" do not.
//   - The rewrite must preserve errors as well as values. "1 div 0" is not
//     folded to anything, because the error it raises is part of its meaning
//     and must still be raised at the point the expression is evaluated.
//
// The pass is deliberately small. Each rule pays for itself on expressions
// that actually appear — a Schematron assertion evaluated once per node, a
// count over a range — rather than existing to be thorough.

// maxOptimizeDepth bounds how far the optimiser will descend.
//
// The parser refuses an over-long operator chain (maxChainLength), so an
// expression reaching here through Compile cannot have a spine deep enough to
// matter. This bound is the second half of the same guard, for the tree that
// did not come from this parser: optimize is an ordinary function on an Expr,
// and a caller building an AST directly, or a future construct the parser
// does not charge, would otherwise descend it without limit and take the
// process down — a Go stack overflow is fatal and recover() does not catch it.
//
// Exceeding it is not an error. Optimisation is a rewrite into an equivalent
// tree, so declining to descend further simply leaves that subtree as written:
// the result is always correct, only unfolded. That is why this half can be
// silent where the parser's half must speak, and why the bound can sit an
// order of magnitude above maxChainLength without weakening it.
const maxOptimizeDepth = 100000

// optimize rewrites e and returns the replacement.
func optimize(e Expr) Expr { return optimizeDepth(e, 0) }

// optimizeContext is optimize with cancellation.
//
// The check is deliberately OUTSIDE the folding decision: it runs as the walk
// descends, never on whether a subexpression is closed. isClosed decides
// whether folding is LEGAL, and an expression that folded differently under
// load would be a silent wrong answer rather than a slow one -- far worse
// than the cost this bounds.
//
// A nil ctx is the ordinary path and allocates no canceller at all, so a
// caller that wants no deadline pays nothing for this.
func optimizeContext(ctx context.Context, e Expr) (Expr, error) {
	if ctx == nil {
		return optimize(e), nil
	}
	f := newExprFacts()
	f.cancel = &optimizeCanceller{ctx: ctx}
	out := optimizeDepthFacts(e, 0, f)
	if f.cancel.err != nil {
		return nil, compileCancelled(f.cancel.err)
	}
	return out, nil
}

// optimizeCancelStride is how many nodes pass between cancellation checks.
//
// ctx.Err() takes a lock, and the ordinary expression compiles in a few
// microseconds, so checking at each node would be overhead for every caller
// in order to bound the one case that is already linear.
const optimizeCancelStride = 4096

type optimizeCanceller struct {
	ctx  context.Context
	seen int
	err  error
}

// tick reports whether the walk should stop.
func (c *optimizeCanceller) tick() bool {
	if c.err != nil {
		return true
	}
	c.seen++
	if c.seen%optimizeCancelStride != 0 {
		return false
	}
	if err := c.ctx.Err(); err != nil {
		c.err = err
		return true
	}
	return false
}

// exprFacts memoises the whole-subtree predicates the optimiser consults.
//
// Both isClosed and containsCompatSensitive answer a question about an entire
// subtree, and both are asked at every node of it. Walking the subtree afresh
// each time makes the pass O(n^2) in expression size: on a left-leaning
// operator chain the left child of each node is the whole chain so far, so the
// scan re-reads the entire prefix n times. That is reachable from document
// data — xsl:evaluate compiles its target expression at transform time — and
// the existing guards do not bound it, because maxChainLength bounds the
// length of ONE chain for stack safety rather than the total work, and a
// composition of many sub-limit chains multiplies freely beneath it.
// Measured before this cache: 160 kB of expression took 736 ms and 640 kB took
// 2.79 s, with isClosed at 86% of CPU samples.
//
// Memoising is exact rather than approximate. Each predicate is a pure
// function of the subtree, so the cached answer is the one the walk would
// have produced; nothing about which expressions fold changes, which is the
// property that matters, since isClosed decides whether folding is legal and
// a wrong answer there is a silent wrong result rather than a slow one.
//
// Keying on the Expr pointer is sound because an entry is only ever recorded
// after that node's children are final. Folding is bottom-up: optimizeWith
// rewrites the children, and only then does foldConstant ask these questions,
// so a node whose subtree could still change has not been cached yet. Nodes
// are not reused across compilations and the map lives for one pass, so a
// stale entry cannot outlive the tree it describes.
type exprFacts struct {
	closed         map[Expr]bool
	compatSensitiv map[Expr]bool

	// cancel, when non-nil, is consulted as the walk descends. It is held
	// here because exprFacts already reaches every node of the recursion, so
	// cancellation needs no second parameter threaded beside it -- and
	// crucially it stays OUT of foldConstant and isClosed, which decide
	// whether folding is legal.
	cancel *optimizeCanceller
}

func newExprFacts() *exprFacts {
	return &exprFacts{
		closed:         make(map[Expr]bool),
		compatSensitiv: make(map[Expr]bool),
	}
}

func optimizeDepth(e Expr, depth int) Expr {
	return optimizeDepthFacts(e, depth, newExprFacts())
}

func optimizeDepthFacts(e Expr, depth int, f *exprFacts) Expr {
	if e == nil {
		return nil
	}
	if f.cancel != nil && f.cancel.tick() {
		// Cancelled: the subtree is returned unchanged, which is the same
		// legal outcome as exceeding maxOptimizeDepth. optimizeContext
		// discards the result and reports the cause.
		return e
	}
	if depth > maxOptimizeDepth {
		// Deeper than this processor will rewrite. The subtree is returned
		// unchanged, which is a legal outcome for a pass that only ever
		// replaces a tree with an equivalent one.
		return e
	}
	// Children are optimised first, so a rule sees folded operands: the "1+2"
	// in "count(1 to (1+2))" becomes a literal before the range rule looks at
	// the bounds.
	e = optimizeWith(e, func(c Expr) Expr { return optimizeDepthFacts(c, depth+1, f) })

	if lit, ok := foldConstant(e, f); ok {
		return lit
	}
	return e
}

// optimizeWith rewrites the sub-expressions of e in place using rec, which is
// the whole-expression pass to apply to each of them. The recursion is a
// parameter because there are two passes -- the ordinary one and the XPath 1.0
// compatibility one, which withholds a rule -- and each must descend through
// itself rather than through the other.
func optimizeWith(e Expr, optimize func(Expr) Expr) Expr {
	switch v := e.(type) {
	case *BinaryOp:
		v.Left, v.Right = optimize(v.Left), optimize(v.Right)
	case *UnaryOp:
		v.Operand = optimize(v.Operand)
	case *FuncCall:
		for i := range v.Args {
			v.Args[i] = optimize(v.Args[i])
		}
	case *SequenceExpr:
		for i := range v.Items {
			v.Items[i] = optimize(v.Items[i])
		}
	case *IfExpr:
		v.Cond, v.Then, v.Else = optimize(v.Cond), optimize(v.Then), optimize(v.Else)
	case *FilterExpr:
		v.Base = optimize(v.Base)
		for i := range v.Predicates {
			v.Predicates[i] = optimize(v.Predicates[i])
		}
	case *ForExpr:
		for i := range v.Bindings {
			v.Bindings[i].Seq = optimize(v.Bindings[i].Seq)
		}
		v.Return = optimize(v.Return)
	case *QuantifiedExpr:
		for i := range v.Bindings {
			v.Bindings[i].Seq = optimize(v.Bindings[i].Seq)
		}
		v.Test = optimize(v.Test)
	case *CastExpr:
		v.Operand = optimize(v.Operand)
	case *InstanceOfExpr:
		v.Operand = optimize(v.Operand)
	case *TreatExpr:
		v.Operand = optimize(v.Operand)
		// A path expression's steps are deliberately not descended into: a
		// step's meaning depends on the focus, so nothing inside one is
		// constant even when it looks like it.
	}
	return e
}

// foldConstant evaluates a closed expression to a literal.
func foldConstant(e Expr, f *exprFacts) (Expr, bool) {
	switch v := e.(type) {
	case *Literal:
		return nil, false // already folded

	case *BinaryOp:
		// An aggregate over a constant range is answered arithmetically at
		// run time already; folding the bounds here means the arithmetic
		// happens once per compilation rather than once per evaluation.
		if !isClosed(v.Left, f) || !isClosed(v.Right, f) {
			return nil, false
		}
		// A comparison is not closed over its operands alone: comparing two
		// strings consults the default collation, which [xsl:]default-collation
		// sets *after* the expression is compiled. Folding it here evaluated
		// it under codepoint order, so "'Adele' eq 'ADELE'" was decided
		// false before the stylesheet's case-blind collation could be
		// applied. Leaving comparisons unfolded costs one evaluation and is
		// the only way the answer can depend on the collation in force.
		if isComparisonOp(v.Op) {
			return nil, false
		}
		return evalToLiteral(e)

	case *UnaryOp:
		if !isClosed(v.Operand, f) {
			return nil, false
		}
		return evalToLiteral(e)

	case *FuncCall:
		if !foldableFunction(v.Name, len(v.Args)) {
			return nil, false
		}
		for _, a := range v.Args {
			if !isClosed(a, f) {
				return nil, false
			}
		}
		return evalToLiteral(e)
	}
	return nil, false
}

// evalToLiteral evaluates e in an empty context and returns it as a literal.
//
// A failure is not an error here: it means the expression raises at run time,
// and the unfolded tree is returned so that it raises then, at the point the
// stylesheet actually evaluates it. Folding "1 div 0" into an error at compile
// time would refuse a stylesheet whose branch is never taken.
func evalToLiteral(e Expr) (Expr, bool) {
	ctx := NewContext(nil, Builtins())
	seq, err := e.Eval(ctx)
	if err != nil || len(seq) != 1 {
		return nil, false
	}
	a, ok := seq[0].(*xdm.Atomic)
	if !ok {
		return nil, false
	}
	// A folded double must keep its type: xs:double(1) and the integer 1 are
	// different values, and a literal carries its type with it.
	return &Literal{Val: a}, true
}

// isClosed reports whether e can be evaluated without a dynamic context.
//
// The answer is memoised in f. It is asked once per node of a subtree that is
// itself walked per node, which is the O(n^2) the cache removes; see exprFacts
// for why caching on the node is exact rather than an approximation.
func isClosed(e Expr, f *exprFacts) bool {
	if v, ok := f.closed[e]; ok {
		return v
	}
	v := isClosedUncached(e, f)
	f.closed[e] = v
	return v
}

func isClosedUncached(e Expr, f *exprFacts) bool {
	switch v := e.(type) {
	case *Literal:
		return true
	case *BinaryOp:
		return isClosed(v.Left, f) && isClosed(v.Right, f)
	case *UnaryOp:
		return isClosed(v.Operand, f)
	case *SequenceExpr:
		for _, it := range v.Items {
			if !isClosed(it, f) {
				return false
			}
		}
		return true
	case *FuncCall:
		if !foldableFunction(v.Name, len(v.Args)) {
			return false
		}
		for _, a := range v.Args {
			if !isClosed(a, f) {
				return false
			}
		}
		return true
	case *CastExpr:
		return isClosed(v.Operand, f)
	}
	// Everything else — a variable, the context item, a path, a for — depends
	// on something only the dynamic context supplies.
	return false
}

// foldableFunction reports whether calling fn at arity during compilation is
// safe.
//
// The list is an allowlist rather than a denylist of the obviously unsafe
// ones. A function is foldable only if it is pure, deterministic, and reads
// nothing from the dynamic context — which rules out fn:position, fn:last,
// fn:current-dateTime, fn:doc, and anything collation- or timezone-sensitive,
// and would rule out a user-defined function even if it happened to be pure.
//
// Arity is part of the question, not a detail of it. XPath overloads on arity,
// and F&O gives the two forms of a name different properties: fn:string#1,
// fn:number#1 and fn:string-length#1 are focus-independent, while fn:string#0,
// fn:number#0 and fn:string-length#0 are all declared ·focus-dependent· —
// they read the context item. Keying the allowlist on the name alone said
// "foldable" for all six.
//
// Nothing was ever mis-folded by that: evalToLiteral evaluates against an
// empty focus, so the zero-arity forms raised XPDY0002 and the unfolded tree
// came back. But that is the wrong thing to be relying on. It makes the
// optimiser's correctness a property of what happens to fail rather than of
// what it declines to attempt, and it fails silently in the direction of
// mis-compiling if evalToLiteral is ever given a focus. The arity check states
// the invariant where the decision is made.
func foldableFunction(name xdm.QName, arity int) bool {
	switch name.URI {
	case xdm.NSXS:
		// The xs: constructors are pure conversions of their argument. Every
		// one of them is registered at arity 1 only — there is no zero-arity
		// constructor to admit a focus dependency — so the whole namespace
		// stays foldable.
		return true
	case xdm.NSFN:
	default:
		return false
	}
	switch name.Local {
	case "string", "number", "string-length":
		// Only the explicit-argument form. The zero-arity form is the context
		// item's, and the context item is not known at compile time.
		return arity == 1
	case "abs", "ceiling", "floor", "round", "round-half-to-even",
		"count", "sum", "avg",
		"concat", "upper-case", "lower-case",
		"substring", "translate",
		"not", "true", "false", "boolean",
		"empty", "exists", "reverse":
		return true
	}
	// fn:contains, starts-with, ends-with, substring-before, substring-after,
	// min, max, distinct-values and compare all read the default collation
	// from the dynamic context, so folding a call on literal arguments here
	// answers it under the codepoint collation and freezes that answer.
	// collations-1006 sets default-collation to a UCA collation at
	// strength=secondary and asks starts-with('abc', 'AB'), which is true
	// under that collation and false under codepoint; folding gave the
	// codepoint answer for an expression that never evaluates under it.
	return false
}

// isComparisonOp reports whether an operator's result can depend on the
// default collation, which is what makes it unsafe to fold at compile time.
func isComparisonOp(op string) bool {
	switch op {
	case "eq", "ne", "lt", "le", "gt", "ge",
		"=", "!=", "<", "<=", ">", ">=":
		return true
	}
	return false
}

// optimizeCompat is optimize for an expression that will evaluate under XPath
// 1.0 compatibility mode.
//
// It runs every rule except constant folding of arithmetic and comparison.
// Those two are the operators B.1 redefines, and folding one means evaluating
// it here against a context that is not in compatibility mode -- which gives
// the 2.0 answer and the 2.0 type, permanently, for an expression that will
// never be evaluated under 2.0 rules. Everything else the optimiser does is
// mode-independent.
func optimizeCompat(e Expr) Expr { return optimizeCompatFacts(e, newExprFacts()) }

func optimizeCompatFacts(e Expr, f *exprFacts) Expr {
	if e == nil {
		return nil
	}
	e = optimizeWith(e, func(c Expr) Expr { return optimizeCompatFacts(c, f) })
	// The check is on the whole subtree, not just the node in hand. Folding
	// happens bottom-up, so "string(-0)" reaches this as a FuncCall whose
	// argument is still a UnaryOp; folding the call evaluates that unary in a
	// context that is not in compatibility mode and bakes in the 2.0 answer,
	// exactly as folding the operator directly would.
	if containsCompatSensitive(e, f) {
		return e
	}
	if lit, ok := foldConstant(e, f); ok {
		return lit
	}
	return e
}

// containsCompatSensitive reports whether e contains an operator whose meaning
// B.1 redefines, anywhere in its subtree.
//
// Unary + and - count alongside the binary operators: B.1 rule 2 converts an
// arithmetic operand with fn:number, which makes it xs:double. That is
// observable in the value and not only in which expressions raise, because
// xs:double has a signed zero and xs:integer does not -- "-0" is the double
// -0.0 under 1.0 and the integer 0 under 2.0, so string(xs:float(-0)) is "-0"
// there and "0" here.
// The answer is memoised in f for the same reason isClosed is: it is a
// whole-subtree question asked at every node of that subtree.
func containsCompatSensitive(e Expr, f *exprFacts) bool {
	if v, ok := f.compatSensitiv[e]; ok {
		return v
	}
	v := containsCompatSensitiveUncached(e, f)
	f.compatSensitiv[e] = v
	return v
}

func containsCompatSensitiveUncached(e Expr, f *exprFacts) bool {
	switch v := e.(type) {
	case *UnaryOp:
		return true
	case *BinaryOp:
		if compatSensitiveOp(v.Op) {
			return true
		}
		return containsCompatSensitive(v.Left, f) || containsCompatSensitive(v.Right, f)
	case *FuncCall:
		for _, a := range v.Args {
			if containsCompatSensitive(a, f) {
				return true
			}
		}
	case *SequenceExpr:
		for _, it := range v.Items {
			if containsCompatSensitive(it, f) {
				return true
			}
		}
	}
	return false
}

// compatSensitiveOp reports whether B.1 redefines the operator, and therefore
// whether folding it at compile time would bake in the wrong answer.
func compatSensitiveOp(op string) bool {
	switch op {
	case "+", "-", "*", "div", "idiv", "mod",
		"=", "!=", "<", "<=", ">", ">=", "to":
		return true
	}
	return false
}
