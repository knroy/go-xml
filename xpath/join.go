package xpath

import (
	"unsafe"

	"github.com/knroy/go-xml/xdm"
)

// This file is the xpath half of XQuery's FLWOR join (xquery/join.go): the
// questions about an expression that a host must have answered before it may
// evaluate a "where" comparison's operands once each instead of once per
// tuple. Every answer is conservative: "no" only costs the optimisation.

// Comparison splits c, when it is a general comparison "A op B", into the
// operator and the two operands, each a copy of c carrying c's static
// properties so that it evaluates exactly as it would inside the comparison.
//
// ok is false for anything else, under XPath 1.0 compatibility mode (whose
// operand conversions comparePairs does not apply), and when B is a range:
// evalGeneralComparison answers "x = 1 to N" from the bounds, which is a
// different path a host must not bypass.
func (c *Compiled) Comparison() (op string, left, right *Compiled, ok bool) {
	b, isBin := c.expr.(*BinaryOp)
	if !isBin || c.compat {
		return "", nil, nil, false
	}
	if generalValueOp(b.Op) == "" {
		return "", nil, nil, false
	}
	if r, isRange := b.Right.(*BinaryOp); isRange && r.Op == "to" {
		return "", nil, nil, false
	}
	l, r := *c, *c
	l.expr, r.expr = b.Left, b.Right
	return b.Op, &l, &r, true
}

// Comparer returns the general comparison c (one Comparison accepted) as a
// function of operands the caller has already evaluated and atomized: the
// same pair order, untypedAtomic conversions, collation and errors as
// evaluating c in ctx, without evaluating either operand again. The context
// is derived once, here, rather than once per call.
func (c *Compiled) Comparer(ctx *Context) func(la, ra xdm.Sequence) (bool, error) {
	b, sub := c.expr.(*BinaryOp), c.scope(ctx)
	return func(la, ra xdm.Sequence) (bool, error) {
		return b.comparePairs(sub, la, ra)
	}
}

// CodepointEquality reports whether c compares strings under the codepoint
// collation in ctx, so that two xs:string or xs:untypedAtomic values are
// equal exactly when their Go strings are.
func (c *Compiled) CodepointEquality(ctx *Context) bool {
	switch c.scope(ctx).collation.(type) {
	case nil, codepointCollation:
		return true
	}
	return false
}

// CallsBuiltins reports whether every function c calls resolves in ctx,
// exactly as evaluation resolves it, to the built-in implementation. Hoistable
// judges calls by name, and a name is not an implementation: a host may pass
// a function library that binds fn:exactly-one or xs:integer to its own Go
// function, which need be neither pure nor deterministic. A host that merely
// re-adds a built-in's Function value still resolves to the built-in.
func (c *Compiled) CallsBuiltins(ctx *Context) bool {
	return c != nil && callsBuiltins(c.expr, c.scope(ctx))
}

// callsBuiltins walks the node types hoistable admits; c has passed it.
func callsBuiltins(e Expr, ctx *Context) bool {
	all := func(es []Expr) bool {
		for _, x := range es {
			if !callsBuiltins(x, ctx) {
				return false
			}
		}
		return true
	}
	switch v := e.(type) {
	case *FuncCall:
		got, found := lookupFor(ctx, v.Name, len(v.Args))
		want, builtin := Builtins().Lookup(v.Name, len(v.Args))
		return found && builtin && sameFunc(got.Call, want.Call) && all(v.Args)
	case *Step:
		return all(v.Predicates)
	case *FilterExpr:
		return callsBuiltins(v.Base, ctx) && all(v.Predicates)
	case *PathExpr:
		return all(v.Steps)
	case *BinaryOp:
		return callsBuiltins(v.Left, ctx) && callsBuiltins(v.Right, ctx)
	case *UnaryOp:
		return callsBuiltins(v.Operand, ctx)
	case *CastExpr:
		return callsBuiltins(v.Operand, ctx)
	case *SequenceExpr:
		return all(v.Items)
	case *IfExpr:
		return callsBuiltins(v.Cond, ctx) && callsBuiltins(v.Then, ctx) &&
			callsBuiltins(v.Else, ctx)
	}
	return true // a literal, a variable or the context item calls nothing
}

// sameFunc reports whether two func values are the same closure. Go does not
// compare funcs, and reflect's Pointer is the code address, which every
// closure from one literal shares (the xs: constructors are one literal over
// many types); the func value's own word is the closure's identity.
func sameFunc(a, b func(*Context, []xdm.Sequence) (xdm.Sequence, error)) bool {
	if a == nil || b == nil {
		return false
	}
	return *(*unsafe.Pointer)(unsafe.Pointer(&a)) == *(*unsafe.Pointer)(unsafe.Pointer(&b))
}

// Hoistable reports whether c's value is a function of its free variables
// alone: it reads no focus, and calls only built-ins that are deterministic
// and read nothing from the dynamic context. Two evaluations with every free
// variable bound to the same items then return the same items, which is what
// lets a host evaluate it once and reuse the value.
func (c *Compiled) Hoistable() bool {
	return c != nil && hoistable(c.expr, false)
}

// hoistable walks e. focus says whether a focus is supplied from inside the
// expression -- a later path step or a predicate -- rather than read from the
// caller's context.
func hoistable(e Expr, focus bool) bool {
	switch v := e.(type) {
	case *Literal, *VarRef:
		return true
	case *ContextItem:
		return focus
	case *Step:
		return focus && allHoistable(v.Predicates, true)
	case *FilterExpr:
		return hoistable(v.Base, focus) && allHoistable(v.Predicates, true)
	case *PathExpr:
		if v.Root && !focus {
			return false
		}
		for i, s := range v.Steps {
			// The first step is evaluated against the caller's focus (or the
			// root, already refused above); every later one against the items
			// the step before produced.
			if !hoistable(s, focus || i > 0 || v.Root) {
				return false
			}
		}
		return true
	case *BinaryOp:
		return hoistable(v.Left, focus) && hoistable(v.Right, focus)
	case *UnaryOp:
		return hoistable(v.Operand, focus)
	case *CastExpr:
		return hoistable(v.Operand, focus)
	case *SequenceExpr:
		return allHoistable(v.Items, focus)
	case *IfExpr:
		return hoistable(v.Cond, focus) && hoistable(v.Then, focus) &&
			hoistable(v.Else, focus)
	case *FuncCall:
		return hoistableFunction(v.Name, len(v.Args)) && allHoistable(v.Args, focus)
	}
	// A binding form, an inline function, a dynamic call or anything the
	// optimiser rewrote into a node of its own: not worth reasoning about.
	return false
}

func allHoistable(es []Expr, focus bool) bool {
	for _, e := range es {
		if !hoistable(e, focus) {
			return false
		}
	}
	return true
}

// hoistableFunction is foldableFunction plus the functions that are just as
// pure but only ever applied to non-literals, so the optimiser never needed
// them: the cardinality checks and fn:data#1.
func hoistableFunction(name xdm.QName, arity int) bool {
	if name.URI == xdm.NSFN && arity == 1 {
		switch name.Local {
		case "exactly-one", "zero-or-one", "one-or-more", "data":
			return true
		}
	}
	return foldableFunction(name, arity)
}
