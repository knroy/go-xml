package xpath

import "github.com/knroy/go-xml/xdm"

// functionItemView presents a map or an array as the function item the data
// model says it already is.
//
// XPath 3.1 does not merely allow "$a(1)" as sugar for a lookup: it defines a
// map and an array to *be* function items of arity one, so every place that
// takes a function — a dynamic call, fn:for-each, array:filter, the $key
// argument of fn:sort — must accept one. Representing them as distinct item
// kinds rather than as xdm.FunctionItem is what keeps map:size and array:size
// able to tell them apart, so the function view is synthesised here on demand
// instead of being stored on the value.
//
// A plain function item is returned unchanged, which makes this a safe filter
// to run at any point where a function is expected.
func functionItemView(it xdm.Item) *xdm.FunctionItem {
	switch v := it.(type) {
	case *xdm.FunctionItem:
		return v
	case *xdm.ArrayItem:
		return &xdm.FunctionItem{
			Arity: 1,
			// The signature is the one the specification gives an array:
			// function(xs:integer) as item()*. prod-ArrayTest asserts that an
			// array is an instance of exactly that type.
			Signature: []string{"item()*", "xs:integer"},
			Invoke: func(_ any, args []xdm.Sequence) (xdm.Sequence, error) {
				if len(args) != 1 {
					return nil, xdm.ErrType(
						"an array takes one argument, got %d", len(args))
				}
				n, err := arrayCallIndex(args[0])
				if err != nil {
					return nil, err
				}
				return v.Member(n)
			},
		}
	case *xdm.MapItem:
		return &xdm.FunctionItem{
			Arity:     1,
			Signature: []string{"item()*", "xs:anyAtomicType"},
			Invoke: func(_ any, args []xdm.Sequence) (xdm.Sequence, error) {
				if len(args) != 1 {
					return nil, xdm.ErrType(
						"a map takes one argument, got %d", len(args))
				}
				atoms, err := xdm.AtomizeChecked(args[0])
				if err != nil {
					return nil, err
				}
				k, err := atoms.Single()
				if err != nil {
					return nil, xdm.ErrType("a map key must be a single atomic value")
				}
				key, ok := k.(*xdm.Atomic)
				if !ok {
					return nil, xdm.ErrType("a map key must be an atomic value")
				}
				val, _, err := v.Get(key)
				return val, err
			},
		}
	}
	return nil
}

// arrayCallIndex reads the position an array is called with.
//
// Calling an array is array:get, so it takes the same xs:integer and raises
// the same errors: a non-integer is XPTY0004 and an integer outside the array
// is FOAY0001.
func arrayCallIndex(seq xdm.Sequence) (int, error) {
	atoms, err := xdm.AtomizeChecked(seq)
	if err != nil {
		return 0, err
	}
	it, err := atoms.Single()
	if err != nil {
		return 0, xdm.ErrType("an array is called with a single xs:integer")
	}
	a, ok := it.(*xdm.Atomic)
	if !ok {
		return 0, xdm.ErrType("an array is called with a single xs:integer")
	}
	return integerPosition(a, "array call")
}

// isFunctionLike reports whether an item is one of the three kinds that behave
// as a function item: a function, a map or an array.
//
// It exists so that the "nothing else matches" clauses of the type tests do
// not have to name all three, and so that adding a fourth would be one edit.
func isFunctionLike(it xdm.Item) bool { return functionItemView(it) != nil }
