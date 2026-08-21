package xpath

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// Library is a mutable function library keyed by expanded name and arity.
//
// Arity is part of the key because XPath overloads on it: fn:string() and
// fn:string($arg) are different functions, and fn:substring has both a
// two- and a three-argument form with different behaviour.
type Library struct {
	fns map[string]Function
	// Parent is consulted when a name is not found locally, so a stylesheet's
	// own functions can shadow and extend the builtins without copying them.
	Parent FunctionLibrary
}

// NewLibrary returns an empty library chained to parent.
func NewLibrary(parent FunctionLibrary) *Library {
	return &Library{fns: map[string]Function{}, Parent: parent}
}

func libKey(name xdm.QName, arity int) string {
	return fmt.Sprintf("%s#%d", name.Clark(), arity)
}

// Lookup implements FunctionLibrary.
func (l *Library) Lookup(name xdm.QName, arity int) (Function, bool) {
	if f, ok := l.fns[libKey(name, arity)]; ok {
		return f, true
	}
	if l.Parent != nil {
		return l.Parent.Lookup(name, arity)
	}
	return Function{}, false
}

// Add registers a function.
func (l *Library) Add(f Function) {
	l.fns[libKey(f.Name, f.Arity)] = f
}

// register is the terse form used to build the builtin library.
func (l *Library) register(uri, local string, arity int,
	call func(*Context, []xdm.Sequence) (xdm.Sequence, error)) {
	name := xdm.QName{URI: uri, Local: local}
	l.Add(Function{Name: name, Arity: arity, Call: call})
}

// registerFn registers a function in the standard fn: namespace, optionally at
// several arities. Most builtins have more than one, and repeating the QName
// for each is noise.
func (l *Library) registerFn(local string, arities []int,
	call func(*Context, []xdm.Sequence) (xdm.Sequence, error)) {
	for _, a := range arities {
		l.register(xdm.NSFN, local, a, call)
	}
}

// --- Argument helpers -------------------------------------------------------
//
// These centralise the conversion rules the spec states per-signature. Doing
// them inline in each function is where subtle divergence creeps in: one
// function atomising and another not, one treating an empty argument as "" and
// another as an error.

// argString returns the string value of an argument, applying fn:string
// semantics: the empty sequence becomes "".
func argString(args []xdm.Sequence, i int) (string, error) {
	if i >= len(args) {
		return "", nil
	}
	atoms := xdm.Atomize(args[i])
	if len(atoms) == 0 {
		return "", nil
	}
	if len(atoms) > 1 {
		return "", xdm.ErrType("argument %d: expected at most one item, got %d", i+1, len(atoms))
	}
	return stringArgValue(atoms[0].(*xdm.Atomic), i)
}

// stringArgValue applies the conversion rules for a parameter declared
// xs:string.
//
// Only the string-like types and xs:untypedAtomic convert; everything else is
// a type error. Calling String() on any atomic instead — which is what this
// did — made "encode-for-uri(12)" quietly encode "12" where the spec requires
// XPTY0004, and the same for every other function with a declared xs:string
// parameter.
//
// xs:anyURI is included because the spec's function conversion rules promote
// it to xs:string, which is what makes fn:resolve-uri and friends composable.
func stringArgValue(a *xdm.Atomic, i int) (string, error) {
	switch a.Type {
	case xdm.TypeString, xdm.TypeUntypedAtomic, xdm.TypeAnyURI:
		return a.String(), nil
	}
	return "", xdm.ErrType(
		"argument %d: expected xs:string, got %s", i+1, a.TypeName())
}

// argAnyAtomicString returns the string value of an argument of *any* atomic
// type.
//
// fn:concat is the one function declared to take xs:anyAtomicType rather than
// xs:string — "concat('a', 1)" is legal and gives "a1" — so it must not be
// type-checked the way every other string parameter is.
func argAnyAtomicString(args []xdm.Sequence, i int) (string, error) {
	if i >= len(args) {
		return "", nil
	}
	atoms := xdm.Atomize(args[i])
	if len(atoms) == 0 {
		return "", nil
	}
	if len(atoms) > 1 {
		return "", xdm.ErrType("argument %d: expected at most one item, got %d", i+1, len(atoms))
	}
	return atoms[0].(*xdm.Atomic).String(), nil
}

// argStringRequired is argString for parameters where an empty sequence is an
// error rather than "".
func argStringRequired(args []xdm.Sequence, i int) (string, error) {
	atoms := xdm.Atomize(args[i])
	it, err := atoms.Single()
	if err != nil {
		return "", err
	}
	return stringArgValue(it.(*xdm.Atomic), i)
}

// argNumber returns a numeric argument, or nil for the empty sequence.
func argNumber(args []xdm.Sequence, i int) (*xdm.Atomic, error) {
	if i >= len(args) {
		return nil, nil
	}
	atoms := xdm.Atomize(args[i])
	if len(atoms) == 0 {
		return nil, nil
	}
	it, err := atoms.Single()
	if err != nil {
		return nil, err
	}
	return toNumeric(it.(*xdm.Atomic))
}

// contextString returns the string value of the context item, for the
// zero-argument forms of string(), name(), normalize-space() and friends.
func contextString(ctx *Context) (string, error) {
	if ctx.Item == nil {
		return "", fmt.Errorf("XPDY0002: no context item")
	}
	switch v := ctx.Item.(type) {
	case *xdm.Node:
		return v.StringValue(), nil
	case *xdm.Atomic:
		return v.String(), nil
	}
	return "", fmt.Errorf("XPDY0002: context item has no string value")
}

// argOrContextString returns argument i when present, else the context item's
// string value. This is the shape of every "…($arg)?" builtin.
func argOrContextString(ctx *Context, args []xdm.Sequence, i int) (string, error) {
	if i < len(args) {
		return argString(args, i)
	}
	return contextString(ctx)
}

// argNode returns a single node argument, or the context node when absent.
func argNodeOrContext(ctx *Context, args []xdm.Sequence, i int) (*xdm.Node, error) {
	if i >= len(args) {
		return contextNodeArg(ctx)
	}
	if len(args[i]) == 0 {
		return nil, nil
	}
	it, err := args[i].Single()
	if err != nil {
		return nil, err
	}
	n, ok := it.(*xdm.Node)
	if !ok {
		return nil, xdm.ErrType("argument %d: expected a node, got %s", i+1, it.TypeName())
	}
	return n, nil
}

func boolSeq(v bool) xdm.Sequence   { return xdm.One(xdm.NewBoolean(v)) }
func strSeq(s string) xdm.Sequence  { return xdm.One(xdm.NewString(s)) }
func intSeq(n int64) xdm.Sequence   { return xdm.One(xdm.NewInteger(n)) }
func numSeq(f float64) xdm.Sequence { return xdm.One(xdm.NewDouble(f)) }

// contextNodeArg returns the context item as a node for a function argument
// that defaults to it.
//
// It differs from Context.ContextNode only in the error code. XPTY0020 is for
// an axis step applied to a non-node; a function whose argument defaults to
// the context item is not a step, so the argument is simply of the wrong type,
// which is XPTY0004. The distinction shows up in "(1 to 100)[fn:local-name()]",
// where the focus is an integer.
func contextNodeArg(ctx *Context) (*xdm.Node, error) {
	n, err := ctx.ContextNode()
	if xdm.ErrorCode(err) == "XPTY0020" {
		return nil, xdm.ErrType(
			"the context item is %s, not a node", ctx.Item.TypeName())
	}
	return n, err
}
