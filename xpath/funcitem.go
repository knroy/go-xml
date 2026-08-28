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
	if e.Cast != nil {
		return xdm.One(schemaConstructorItem(e.Name, e.Cast)), nil
	}
	fn, ok := lookupFor(ctx, e.Name, e.Arity)
	if !ok {
		return nil, fmt.Errorf("XPST0017: unknown function %s with %d argument(s)",
			e.Name.Clark(), e.Arity)
	}
	item := functionItemFor(e.Name, e.Arity, fn.Call)
	item.Signature = fn.Signature
	// A named function reference to a context-dependent function retains the
	// focus in force where the reference was *written*, not where the item is
	// eventually called (3.1.6: the function item's dynamic context is the one
	// at the point of the reference). fn-lang-31/32 pin this exactly:
	// "/langs/para[4]!fn:lang#1" is applied to /langs/para[1], and the answer
	// must be about para[4]'s xml:lang, not para[1]'s. Passing the caller's
	// context straight through made it about para[1] and inverted both.
	//
	// Only a reference written *with* a focus retains one. Where there is
	// none there is nothing to retain, and falling through to the caller's
	// context is what keeps a bare "fn:name#0" usable in the places that
	// supply a focus at call time.
	if ctx.Item != nil {
		item.Invoke = withRetainedFocus(ctx, item.Invoke)
	}
	return xdm.One(item), nil
}

// ConstructorArgVar is the name the argument of a schema constructor function
// is bound to while the cast that defines the constructor is evaluated. It is
// in a namespace no query or stylesheet can write, so nothing can collide with
// it or observe it.
var ConstructorArgVar = xdm.QName{
	URI: "urn:go-xml:xpath:internal", Local: "constructor-arg",
}

// schemaConstructorItem builds the function item a reference to an imported
// schema type's constructor function denotes.
//
// The constructor is defined as a cast, so the item binds its one argument and
// evaluates the cast the parser built. See NamedFunctionRef.Cast.
func schemaConstructorItem(name xdm.QName, cast Expr) *xdm.FunctionItem {
	return &xdm.FunctionItem{
		Name:  name,
		Arity: 1,
		Invoke: func(ctx any, args []xdm.Sequence) (xdm.Sequence, error) {
			c, ok := ctx.(invokeContext)
			if !ok || c == nil {
				return nil, fmt.Errorf(
					"XPTY0004: function item invoked without an evaluation context")
			}
			var arg xdm.Sequence
			if len(args) > 0 {
				arg = args[0]
			}
			return cast.Eval(c.WithVar(ConstructorArgVar, arg))
		},
	}
}

// withRetainedFocus wraps an invoke closure so the call runs under the focus
// captured at reference time, while still taking the caller's cancellation and
// resource counters — those are per-evaluation, not per-closure.
func withRetainedFocus(ref *Context, inner func(any, []xdm.Sequence) (xdm.Sequence, error)) func(any, []xdm.Sequence) (xdm.Sequence, error) {
	captured := *ref
	return func(callCtx any, args []xdm.Sequence) (xdm.Sequence, error) {
		sub := captured
		p := &sub
		if c, ok := callCtx.(invokeContext); ok && c != nil {
			sub.Ctx = c.Ctx
			sub.items = c.items
			// The retained focus does not retain the host's dynamic-call
			// markers. XSLT 3.0 24.3 says the XSLT extensions to the dynamic
			// context are not part of a function item's closure, so a marker
			// the caller set for the duration of the call must survive being
			// evaluated under the reference's focus -- XTDE1360's
			// current#0() is exactly that case.
			for _, name := range MarkedOnDynamicCall {
				if v, ok := c.LookupVar(name); ok {
					p = p.WithVar(name, v)
				}
			}
			// The clearing goes the same way, and for the same reason: the
			// captured context still holds whatever grouping or captured
			// substrings were in scope where the reference was written, and
			// restoring it wholesale would hand them to the call. 5.3.4
			// names regex-group#1(2) as the example -- the components are
			// cleared, so regex-090 gets the zero-length string rather than
			// the enclosing match.
			for _, name := range ClearedOnDynamicCall {
				p = p.WithVar(name, nil)
			}
		}
		return inner(p, args)
	}
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
		Arity:     len(e.Params),
		Signature: inlineSignature(e),
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
	// Function coercion, the last clause of 3.1.5: a function item supplied
	// where a typed function test is declared is not rejected for having a
	// narrower signature — it is wrapped, so that the arguments are converted
	// to what it actually accepts and its result to what the test promises.
	// for-each-011 depends on this: fn:upper-case#1 declares (xs:string?) as
	// xs:string, which is not a subtype of "function(item()) as item()", yet
	// passing it there is legal and must work.
	if st.IsFunctionTest && st.HasFunctionArity && len(v) == 1 {
		// functionItemView rather than a type assertion, so that a map or an
		// array supplied where a typed function test is declared is coerced
		// like any other function item: MapTest-058 binds a map to a variable
		// declared "function(xs:anyAtomicType) as xs:string?".
		if fn := functionItemView(v[0]); fn != nil && fn.Arity == st.FunctionArity {
			return xdm.One(coerceFunctionItem(fn, st)), nil
		}
	}
	return nil, xdm.ErrType("XPTY0004: value does not match the declared type %s", st.String())
}

// coerceFunctionItem wraps fn so that it presents the signature st declares.
//
// The wrapper's own signature is the declared one, so a further coercion or an
// instance-of test sees what the parameter promised rather than what the
// original function happened to declare. Conversion of the actual values is
// deferred to call time, where the arguments exist; the wrapper itself is
// built without knowing them.
func coerceFunctionItem(fn *xdm.FunctionItem, st SequenceType) *xdm.FunctionItem {
	sig := make([]string, 0, st.FunctionArity+1)
	if st.FunctionReturn != nil {
		sig = append(sig, st.FunctionReturn.String())
	} else {
		sig = append(sig, "item()*")
	}
	for _, p := range st.FunctionParams {
		sig = append(sig, p.String())
	}
	params := st.FunctionParams
	ret := st.FunctionReturn
	inner := fn.Invoke
	return &xdm.FunctionItem{
		Name:      fn.Name,
		Arity:     fn.Arity,
		Signature: sig,
		Invoke: func(callCtx any, args []xdm.Sequence) (xdm.Sequence, error) {
			// The declared parameter types are applied on the way in and the
			// declared return type on the way out. The wrapped function then
			// applies its own, which is what makes a chain of coercions
			// compose rather than the outermost one deciding everything.
			for i := range args {
				if i < len(params) {
					conv, err := convertForParam(args[i], params[i])
					if err != nil {
						return nil, err
					}
					args[i] = conv
				}
			}
			out, err := inner(callCtx, args)
			if err != nil {
				return nil, err
			}
			if ret != nil {
				return convertForParam(out, *ret)
			}
			return out, nil
		},
	}
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
		// No name. A partial application produces a *new* function, not the
		// one it was written from: fn:function-name(fn:substring(?, 1)) is
		// the empty sequence, the same answer an inline function gives.
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

// ClearedOnDynamicCall names variables a host language wants unbound for the
// duration of a dynamic function call.
//
// XSLT 3.0 section 24.3 clears the current output URI across a dynamic call,
// and the value is carried as an ordinary variable binding so that it follows
// the same scoping an expression does. XPath itself has no such notion, so
// the host registers the name rather than this package knowing it.
var ClearedOnDynamicCall []xdm.QName

// MarkedOnDynamicCall names variables a host language wants BOUND, to a single
// true, for the duration of a dynamic function call.
//
// It is the counterpart of ClearedOnDynamicCall, for a condition the host
// cannot express by unbinding: XSLT's fn:current() falls back to the context
// item when nothing bound the current node, which is right for a bare XPath
// evaluation and wrong across a dynamic call, where XTDE1360 says the
// function behaves "as if the context item is absent". Clearing cannot say
// that; a positive marker can.
var MarkedOnDynamicCall []xdm.QName

// clearHostVars applies both host registries for one call.
func clearHostVars(ctx *Context) *Context {
	for _, name := range ClearedOnDynamicCall {
		ctx = ctx.WithVar(name, nil)
	}
	for _, name := range MarkedOnDynamicCall {
		ctx = ctx.WithVar(name, xdm.One(xdm.NewBoolean(true)))
	}
	return ctx
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
	return fn.Invoke(clearHostVars(ctx), args)
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
	// A map and an array are function items in the data model, so "$a(1)" and
	// "$m('k')" are dynamic calls rather than a separate construct. They are
	// represented as their own item kinds here, which is why the function
	// view has to be asked for rather than being found by a type assertion.
	fn := functionItemView(seq[0])
	if fn == nil {
		return nil, xdm.ErrType(
			"XPTY0004: the target of a dynamic call is %s, not a function", seq[0].TypeName())
	}
	return fn, nil
}

// inlineSignature records an inline function's declared types, in the order
// xdm.FunctionItem.Signature uses: the return type first, then the parameters.
//
// An omitted type is item()*, which is what the specification defaults both
// the parameters and the result to.
func inlineSignature(e *InlineFunctionExpr) []string {
	const anyType = "item()*"
	sig := make([]string, 0, len(e.Params)+1)
	if e.Result != nil {
		sig = append(sig, e.Result.String())
	} else {
		sig = append(sig, anyType)
	}
	for _, p := range e.Params {
		if p.Type != nil {
			sig = append(sig, p.Type.String())
			continue
		}
		sig = append(sig, anyType)
	}
	return sig
}
