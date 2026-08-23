package xpath

import "github.com/knroy/go-xml/xdm"

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

// optimize rewrites e and returns the replacement.
func optimize(e Expr) Expr {
	if e == nil {
		return nil
	}
	// Children are optimised first, so a rule sees folded operands: the "1+2"
	// in "count(1 to (1+2))" becomes a literal before the range rule looks at
	// the bounds.
	e = optimizeChildren(e)

	if lit, ok := foldConstant(e); ok {
		return lit
	}
	return e
}

// optimizeChildren rewrites the sub-expressions of e in place.
func optimizeChildren(e Expr) Expr {
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
func foldConstant(e Expr) (Expr, bool) {
	switch v := e.(type) {
	case *Literal:
		return nil, false // already folded

	case *BinaryOp:
		// An aggregate over a constant range is answered arithmetically at
		// run time already; folding the bounds here means the arithmetic
		// happens once per compilation rather than once per evaluation.
		if !isClosed(v.Left) || !isClosed(v.Right) {
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
		if !isClosed(v.Operand) {
			return nil, false
		}
		return evalToLiteral(e)

	case *FuncCall:
		if !foldableFunction(v.Name) {
			return nil, false
		}
		for _, a := range v.Args {
			if !isClosed(a) {
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
func isClosed(e Expr) bool {
	switch v := e.(type) {
	case *Literal:
		return true
	case *BinaryOp:
		return isClosed(v.Left) && isClosed(v.Right)
	case *UnaryOp:
		return isClosed(v.Operand)
	case *SequenceExpr:
		for _, it := range v.Items {
			if !isClosed(it) {
				return false
			}
		}
		return true
	case *FuncCall:
		if !foldableFunction(v.Name) {
			return false
		}
		for _, a := range v.Args {
			if !isClosed(a) {
				return false
			}
		}
		return true
	case *CastExpr:
		return isClosed(v.Operand)
	}
	// Everything else — a variable, the context item, a path, a for — depends
	// on something only the dynamic context supplies.
	return false
}

// foldableFunction reports whether calling fn during compilation is safe.
//
// The list is an allowlist rather than a denylist of the obviously unsafe
// ones. A function is foldable only if it is pure, deterministic, and reads
// nothing from the dynamic context — which rules out fn:position, fn:last,
// fn:current-dateTime, fn:doc, and anything collation- or timezone-sensitive,
// and would rule out a user-defined function even if it happened to be pure.
func foldableFunction(name xdm.QName) bool {
	switch name.URI {
	case xdm.NSXS:
		// The xs: constructors are pure conversions of their argument.
		return true
	case xdm.NSFN:
	default:
		return false
	}
	switch name.Local {
	case "abs", "ceiling", "floor", "round", "round-half-to-even",
		"count", "sum", "min", "max", "avg",
		"concat", "string-length", "upper-case", "lower-case",
		"substring", "substring-before", "substring-after",
		"contains", "starts-with", "ends-with", "translate",
		"not", "true", "false", "boolean", "number", "string",
		"empty", "exists", "reverse", "distinct-values":
		return true
	}
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
