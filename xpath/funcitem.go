package xpath

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// This file implements the function-item half of XPath 3.0: a function is a
// value, so it can be bound to a variable, passed to another function and
// returned from one.
//
// The pieces are a named function reference (fn:concat#3), an inline function
// expression (function($x) { $x + 1 }), and a dynamic call ($f(1)) that
// invokes whatever function item an expression produced.
//
// The item itself lives in the xdm package, because it is a kind of XDM item
// and sequences are defined there. What lives here is everything that needs an
// evaluation context: xdm.FunctionItem carries an Invoke closure, and the
// closures below are the ones installed into it.

// invokeContext is the concrete type xdm.FunctionItem.Invoke is handed.
//
// The field is typed any there because xdm cannot import this package. Every
// closure installed on a function item asserts this type back out, and nothing
// outside this package ever calls Invoke, so the assertion cannot be reached
// with anything else.
type invokeContext = *Context

// Eval implements Expr: it resolves the named function and yields it as a
// value.
//
// Resolution is static in the sense that matters — the name and arity are
// fixed by the expression — but it happens here rather than at parse time
// because the function library lives on the context, exactly as it does for an
// ordinary call.
func (e *NamedFunctionRef) Eval(ctx *Context) (xdm.Sequence, error) {
	fn, ok := lookupFor(ctx, e.Name, e.Arity)
	if !ok {
		return nil, fmt.Errorf("XPST0017: unknown function %s with %d argument(s)",
			e.Name.Clark(), e.Arity)
	}
	item := functionItemFor(e.Name, e.Arity, fn.Call)
	item.Signature = fn.Signature
	return xdm.One(item), nil
}

// functionItemFor wraps a library function as a function item.
func functionItemFor(name xdm.QName, arity int,
	call func(*Context, []xdm.Sequence) (xdm.Sequence, error)) *xdm.FunctionItem {
	return &xdm.FunctionItem{
		Name:  name,
		Arity: arity,
		Invoke: func(ctx any, args []xdm.Sequence) (xdm.Sequence, error) {
			c, ok := ctx.(invokeContext)
			if !ok {
				return nil, fmt.Errorf("XPTY0004: function item invoked without an evaluation context")
			}
			return call(c, args)
		},
	}
}

// Eval implements Expr: it captures the inline function as a value.
//
// The captured context is what makes this a closure rather than a function
// definition. "let $n := 2 return function($x) { $x * $n }" returns a function
// that still knows $n, so the context in scope where the expression was
// written is the one the body is evaluated in — not the one in scope wherever
// the function is eventually called.
func (e *InlineFunctionExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	// The focus is *not* captured. Section 3.1.7: an inline function's body
	// has no context item, position or size, so "." inside one is XPDY0002
	// even where the expression that wrote it had a focus. Variables are
	// captured; the focus is deliberately dropped.
	noFocus := *ctx
	noFocus.Item, noFocus.Position, noFocus.Size = nil, 0, 0
	captured := &noFocus
	item := &xdm.FunctionItem{
		// No name: fn:function-name returns the empty sequence for an inline
		// function, which is what the zero QName means here.
		Arity: len(e.Params),
	}
	item.Invoke = func(callCtx any, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args) != len(e.Params) {
			return nil, fmt.Errorf("XPTY0004: inline function expects %d argument(s), got %d",
				len(e.Params), len(args))
		}
		// The body runs in the captured scope, not the caller's. The caller's
		// context supplies only the resource limits, which are per-evaluation
		// rather than per-closure.
		sub := captured
		if c, ok := callCtx.(invokeContext); ok && c != nil {
			s := *captured
			s.Ctx = c.Ctx
			s.items = c.items
			sub = &s
		}
		for i, p := range e.Params {
			v := args[i]
			// A declared parameter type is checked on the way in, which is
			// where a mismatch is XPTY0004 rather than something the body
			// discovers as a stranger error later.
			if p.Type != nil {
				conv, err := convertForParam(v, *p.Type)
				if err != nil {
					return nil, err
				}
				v = conv
			}
			sub = sub.WithVar(p.Name, v)
		}
		out, err := e.Body.Eval(sub)
		if err != nil {
			return nil, err
		}
		if e.Result != nil {
			return convertForParam(out, *e.Result)
		}
		return out, nil
	}
	return xdm.One(item), nil
}

// convertForParam applies a declared parameter or return type to a value.
//
// The function conversion rules of section 3.1.5 are wider than a bare
// instance-of test: an untypedAtomic argument is cast to the declared type,
// and a node is atomised when the declared type is atomic. Only the parts that
// arise for a function item are applied here — the sequence type is checked,
// and atomisation happens when the target is an atomic type.
func convertForParam(v xdm.Sequence, st SequenceType) (xdm.Sequence, error) {
	// An atomic target atomises its argument first: "function($x as xs:string)"
	// called with an element is given the element's string value.
	if st.HasAtomicType {
		atoms, err := xdm.AtomizeChecked(v)
		if err != nil {
			return nil, err
		}
		v = atoms
	}
	if st.Matches(v) {
		return v, nil
	}
	// The function conversion rules of section 3.1.5 are wider than an
	// instance-of test. Numeric promotion applies — an xs:integer argument
	// satisfies an xs:double parameter, and xs:float promotes to xs:double —
	// and xs:untypedAtomic is cast to the declared type. Both convert the
	// value rather than merely accepting it, so they happen here and not in
	// the type test.
	if st.HasAtomicType {
		conv := make(xdm.Sequence, 0, len(v))
		for _, it := range v {
			a, ok := it.(*xdm.Atomic)
			if !ok {
				return nil, xdm.ErrType(
					"XPTY0004: value does not match the declared type %s", st.String())
			}
			if !convertibleToParam(a.Type, st.AtomicType) {
				return nil, xdm.ErrType(
					"XPTY0004: value does not match the declared type %s", st.String())
			}
			c, err := CastAtomic(a, st.AtomicType)
			if err != nil {
				return nil, xdm.ErrType(
					"XPTY0004: value does not match the declared type %s", st.String())
			}
			conv = append(conv, c)
		}
		if st.Matches(conv) {
			return conv, nil
		}
	}
	return nil, xdm.ErrType("XPTY0004: value does not match the declared type %s", st.String())
}

// Eval implements Expr. A placeholder is never evaluated on its own: the call
// that contains it detects it first and builds a partial application instead.
func (e *ArgumentPlaceholder) Eval(_ *Context) (xdm.Sequence, error) {
	return nil, fmt.Errorf(
		"XPST0003: an argument placeholder may only appear in an argument list")
}

// partialApply builds the function item a partially applied call produces.
//
// "concat('a', ?)" is a function of one argument that concatenates 'a' with
// whatever it is given. The supplied arguments are evaluated once, where the
// partial application is written, and the placeholders become the parameters
// of the new function in the order they appear.
func partialApply(name xdm.QName, arity int,
	call func(*Context, []xdm.Sequence) (xdm.Sequence, error),
	args []Expr, ctx *Context) (xdm.Sequence, error) {
	// A partial application still supplies one argument position per
	// parameter — the placeholders mark which of them stay open, they do not
	// excuse the count. "concat#4('one', ?, 'three')" names three positions
	// for a function of four, which is a type error rather than a function of
	// arity one.
	if len(args) != arity {
		return nil, xdm.Errorf("XPTY0004",
			"%s takes %d argument(s), but %d were supplied",
			name.Clark(), arity, len(args))
	}
	fixed := make([]xdm.Sequence, len(args))
	var holes []int
	for i, a := range args {
		if _, ok := a.(*ArgumentPlaceholder); ok {
			holes = append(holes, i)
			continue
		}
		v, err := a.Eval(ctx)
		if err != nil {
			return nil, err
		}
		fixed[i] = v
	}
	captured := ctx
	item := &xdm.FunctionItem{
		Name:  name,
		Arity: len(holes),
		Invoke: func(callCtx any, supplied []xdm.Sequence) (xdm.Sequence, error) {
			if len(supplied) != len(holes) {
				return nil, fmt.Errorf(
					"XPTY0004: partially applied %s takes %d argument(s), got %d",
					name.Clark(), len(holes), len(supplied))
			}
			full := make([]xdm.Sequence, len(fixed))
			copy(full, fixed)
			for i, h := range holes {
				full[h] = supplied[i]
			}
			c := captured
			if cc, ok := callCtx.(invokeContext); ok && cc != nil {
				c = cc
			}
			return call(c, full)
		},
	}
	return xdm.One(item), nil
}

// hasPlaceholder reports whether an argument list contains a "?".
func hasPlaceholder(args []Expr) bool {
	for _, a := range args {
		if _, ok := a.(*ArgumentPlaceholder); ok {
			return true
		}
	}
	return false
}

// convertibleToParam reports whether the function conversion rules turn a
// value of type from into one of type to.
//
// They are narrow, and deliberately so: only numeric *promotion* — which
// widens and never loses information — and the casting of xs:untypedAtomic.
// A general cast would let an xs:decimal satisfy an xs:integer parameter by
// truncating, which is XPTY0004: "$add(3, 4.2)" on a function declared over
// integers is an error, not a call with 4.
func convertibleToParam(from, to xdm.TypeCode) bool {
	if from == to || from == xdm.TypeUntypedAtomic {
		return true
	}
	switch to {
	case xdm.TypeDouble:
		// Every other numeric type promotes to double.
		switch from {
		case xdm.TypeFloat, xdm.TypeDecimal, xdm.TypeInteger:
			return true
		}
	case xdm.TypeFloat:
		switch from {
		case xdm.TypeDecimal, xdm.TypeInteger:
			return true
		}
	case xdm.TypeDecimal:
		// An integer is a decimal by derivation, not by promotion, but the
		// effect is the same and it loses nothing.
		return from == xdm.TypeInteger
	case xdm.TypeString:
		// xs:anyURI promotes to xs:string.
		return from == xdm.TypeAnyURI
	}
	return false
}

// Eval implements Expr: it calls the function item the target produces.
func (e *DynamicCall) Eval(ctx *Context) (xdm.Sequence, error) {
	target, err := e.Target.Eval(ctx)
	if err != nil {
		return nil, err
	}
	fn, err := singleFunctionItem(target)
	if err != nil {
		return nil, err
	}
	// A placeholder makes this a partial application rather than a call.
	if hasPlaceholder(e.Args) {
		return partialApply(fn.Name, fn.Arity,
			func(c *Context, a []xdm.Sequence) (xdm.Sequence, error) {
				return fn.Invoke(c, a)
			}, e.Args, ctx)
	}
	args := make([]xdm.Sequence, 0, len(e.Args))
	for _, a := range e.Args {
		v, err := a.Eval(ctx)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	if len(args) != fn.Arity {
		return nil, fmt.Errorf("XPTY0004: %s takes %d argument(s), got %d",
			fn.String(), fn.Arity, len(args))
	}
	return fn.Invoke(ctx, args)
}

// singleFunctionItem extracts the one function item a sequence must hold.
//
// Calling something that is not a function is XPTY0004, and so is calling a
// sequence of them: the target of a dynamic call is a single item.
func singleFunctionItem(seq xdm.Sequence) (*xdm.FunctionItem, error) {
	if len(seq) != 1 {
		return nil, xdm.ErrType(
			"XPTY0004: the target of a dynamic call must be a single function item, got %d items",
			len(seq))
	}
	fn, ok := seq[0].(*xdm.FunctionItem)
	if !ok {
		return nil, xdm.ErrType(
			"XPTY0004: the target of a dynamic call is %s, not a function", seq[0].TypeName())
	}
	return fn, nil
}
