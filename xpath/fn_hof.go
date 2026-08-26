package xpath

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// registerHOFuncs adds the higher-order functions of F&O 3.0 section 16: the
// ones that take or return a function item.
//
// They are the reason function items exist as a value kind at all, and they
// are all thin: the spec defines each by an equivalent XQuery implementation
// a few lines long, and the work here is applying the argument order and the
// empty-sequence rules exactly as written.
func registerHOFuncs(l *Library) {
	// fn:function-name($func as function(*)) as xs:QName?
	//
	// The empty sequence for an anonymous function, which is what
	// distinguishes an inline function from a named one at run time.
	l.registerFnSince(XPath30, "function-name", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		fn, err := argFunction(args, 0, "fn:function-name")
		if err != nil {
			return nil, err
		}
		if fn.IsAnonymous() {
			return xdm.Empty(), nil
		}
		// The returned QName carries a prefix, which fn:function-name's
		// examples show: function-name(fn:substring#2) is the QName whose
		// lexical form is "fn:substring". A name with no prefix would print
		// as the bare local part.
		name := fn.Name
		if name.Prefix == "" && name.URI == xdm.NSFN {
			name.Prefix = "fn"
		}
		return xdm.One(xdm.NewQNameValue(name)), nil
	})

	// fn:function-arity($func as function(*)) as xs:integer
	l.registerFnSince(XPath30, "function-arity", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		fn, err := argFunction(args, 0, "fn:function-arity")
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewInteger(int64(fn.Arity))), nil
	})

	// fn:function-lookup($name as xs:QName, $arity as xs:integer) as function(*)?
	//
	// The dynamic counterpart of a named function reference: the name is a
	// value rather than a literal. A name that is not in scope gives the
	// empty sequence rather than an error, which is what lets a stylesheet
	// test for a function's availability.
	l.registerFnSince(XPath30, "function-lookup", []int{2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		name, err := argQName(args, 0, "fn:function-lookup")
		if err != nil {
			return nil, err
		}
		arity, err := argNumber(args, 1)
		if err != nil {
			return nil, err
		}
		if arity == nil {
			return nil, xdm.ErrType("fn:function-lookup: arity is required")
		}
		n := int(arity.Float64())
		fn, ok := lookupFor(ctx, name, n)
		if !ok {
			return xdm.Empty(), nil
		}
		return xdm.One(functionItemFor(name, n, fn.Call)), nil
	})

	// fn:for-each($seq as item()*, $f as function(item()) as item()*) as item()*
	l.registerFnSince(XPath30, "for-each", []int{2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		fn, err := argFunction(args, 1, "fn:for-each")
		if err != nil {
			return nil, err
		}
		var out xdm.Sequence
		for _, it := range seqArg(args, 0) {
			v, err := callFunction(ctx, fn, xdm.One(it))
			if err != nil {
				return nil, err
			}
			out = append(out, v...)
		}
		return out, nil
	})

	// fn:filter($seq as item()*, $f as function(item()) as xs:boolean) as item()*
	//
	// The predicate's result is compared to true rather than run through the
	// effective boolean value rules: the declared return type is xs:boolean,
	// so anything else is a type error rather than something to coerce.
	l.registerFnSince(XPath30, "filter", []int{2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		fn, err := argFunction(args, 1, "fn:filter")
		if err != nil {
			return nil, err
		}
		var out xdm.Sequence
		for _, it := range seqArg(args, 0) {
			v, err := callFunction(ctx, fn, xdm.One(it))
			if err != nil {
				return nil, err
			}
			keep, err := singleBoolean(v, "fn:filter")
			if err != nil {
				return nil, err
			}
			if keep {
				out = append(out, it)
			}
		}
		return out, nil
	})

	// fn:fold-left($seq, $zero, $f as function(item()*, item()) as item()*)
	//
	// The accumulator is the *first* argument to $f here and the second in
	// fold-right. Getting that backwards is silent for a commutative
	// operation and wrong for every other, so the two are written out rather
	// than sharing a helper with a flag.
	l.registerFnSince(XPath30, "fold-left", []int{3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		fn, err := argFunction(args, 2, "fn:fold-left")
		if err != nil {
			return nil, err
		}
		acc := seqArg(args, 1)
		for _, it := range seqArg(args, 0) {
			acc, err = callFunction(ctx, fn, acc, xdm.One(it))
			if err != nil {
				return nil, err
			}
		}
		return acc, nil
	})

	// fn:fold-right($seq, $zero, $f as function(item(), item()*) as item()*)
	//
	// Right to left, and the item is the first argument.
	l.registerFnSince(XPath30, "fold-right", []int{3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		fn, err := argFunction(args, 2, "fn:fold-right")
		if err != nil {
			return nil, err
		}
		acc := seqArg(args, 1)
		seq := seqArg(args, 0)
		for i := len(seq) - 1; i >= 0; i-- {
			acc, err = callFunction(ctx, fn, xdm.One(seq[i]), acc)
			if err != nil {
				return nil, err
			}
		}
		return acc, nil
	})

	// fn:for-each-pair($seq1, $seq2, $f as function(item(), item()) as item()*)
	//
	// Stops at the shorter of the two: the spec's recursion ends as soon as
	// either is empty.
	l.registerFnSince(XPath30, "for-each-pair", []int{3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		fn, err := argFunction(args, 2, "fn:for-each-pair")
		if err != nil {
			return nil, err
		}
		a, b := seqArg(args, 0), seqArg(args, 1)
		n := len(a)
		if len(b) < n {
			n = len(b)
		}
		var out xdm.Sequence
		for i := 0; i < n; i++ {
			v, err := callFunction(ctx, fn, xdm.One(a[i]), xdm.One(b[i]))
			if err != nil {
				return nil, err
			}
			out = append(out, v...)
		}
		return out, nil
	})
}

// seqArg returns argument i, or the empty sequence when it is absent.
func seqArg(args []xdm.Sequence, i int) xdm.Sequence {
	if i >= len(args) {
		return nil
	}
	return args[i]
}

// argFunction returns argument i as the single function item it must be.
func argFunction(args []xdm.Sequence, i int, fn string) (*xdm.FunctionItem, error) {
	if i >= len(args) || len(args[i]) != 1 {
		return nil, xdm.ErrType("%s: expected a single function item", fn)
	}
	f, ok := args[i][0].(*xdm.FunctionItem)
	if !ok {
		return nil, xdm.ErrType("%s: argument is %s, not a function",
			fn, args[i][0].TypeName())
	}
	return f, nil
}

// argQName returns argument i as the single xs:QName it must be.
func argQName(args []xdm.Sequence, i int, fn string) (xdm.QName, error) {
	atoms := xdm.Atomize(seqArg(args, i))
	if len(atoms) != 1 {
		return xdm.QName{}, xdm.ErrType("%s: expected a single xs:QName", fn)
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok || a.Type != xdm.TypeQName || a.QName() == nil {
		return xdm.QName{}, xdm.ErrType("%s: argument is not an xs:QName", fn)
	}
	return *a.QName(), nil
}

// callFunction invokes a function item, checking its arity first.
//
// The arity check is here rather than inside each caller because every
// higher-order function passes a fixed number of arguments and the error is
// the same: the function it was given does not take them.
func callFunction(ctx *Context, fn *xdm.FunctionItem, args ...xdm.Sequence) (xdm.Sequence, error) {
	if fn.Arity != len(args) {
		return nil, fmt.Errorf("XPTY0004: %s takes %d argument(s), but was applied to %d",
			fn.String(), fn.Arity, len(args))
	}
	return fn.Invoke(ctx, args)
}

// singleBoolean reads the xs:boolean a predicate function must return.
func singleBoolean(seq xdm.Sequence, fn string) (bool, error) {
	if len(seq) != 1 {
		return false, xdm.ErrType("%s: the function must return a single xs:boolean", fn)
	}
	a, ok := seq[0].(*xdm.Atomic)
	if !ok || a.Type != xdm.TypeBoolean {
		return false, xdm.ErrType("%s: the function must return xs:boolean", fn)
	}
	return a.Bool(), nil
}
