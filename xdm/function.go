package xdm

import "fmt"

// FunctionItem is the third kind of XDM item, introduced in XPath 3.0.
//
// XPath 2.0 has exactly two kinds of item, a node and an atomic value, and the
// Item interface is closed around them. 3.0 adds this one: a function is a
// value, so it can be bound to a variable, passed to another function, and
// returned from one.
//
// The body is deliberately not modelled here. Calling a function needs an
// evaluation context, a function library and the expression tree — all of
// which live in the xpath package, which imports this one. So this type
// carries only what the data model itself defines about a function item — its
// name, its arity, and an opaque payload — and the xpath package supplies the
// Invoke closure that knows how to run it.
//
// The consequence worth stating: a caller doing an exhaustive type switch over
// Item now has a third case to consider. That is unavoidable — a new kind of
// item is exactly what 3.0 adds — but it is contained, because an XPath 2.0
// expression can never produce one. Nothing in the 2.0 language constructs a
// function item, so a 2.0 caller's switch is never reached by one.
type FunctionItem struct {
	// Name is the function's name, or the zero QName for an anonymous
	// (inline) function. fn:function-name returns the empty sequence for the
	// latter, which is why this is a value rather than a pointer: the zero
	// QName is the "no name" case.
	Name QName

	// Arity is the number of parameters the function declares. It is part of
	// a function's identity: fn:concat#2 and fn:concat#3 are different
	// function items.
	Arity int

	// Signature is the function's declared parameter and return types, in
	// source spelling ("xs:string", "item()*", "node()?"), with Signature[0]
	// the return type and the rest the parameters in order.
	//
	// It is nil for a function whose signature was never recorded, which a
	// typed function test then cannot judge: such an item matches on arity
	// alone, as it did before signatures existed. Strings rather than a
	// parsed type because the parsed form lives in the xpath package, which
	// this one cannot import.
	Signature []string

	// Invoke calls the function with the given arguments.
	//
	// The context is passed as an any because the type that carries it lives
	// in the xpath package. The closure the xpath package installs here knows
	// the concrete type and asserts it; no other package calls this directly.
	Invoke func(ctx any, args []Sequence) (Sequence, error)
}

func (f *FunctionItem) isItem() {}

// TypeName implements Item.
//
// The data model calls this type "function(*)". A more precise signature is
// not available: this value records the arity but not the declared parameter
// and return types, which the static type system would need.
func (f *FunctionItem) TypeName() string { return "function(*)" }

// String renders the function item for an error message.
//
// A function item has no string value — fn:string of one is FOTY0014 — so this
// is not that, and is never the result of atomising one.
func (f *FunctionItem) String() string {
	if f.Name.Local == "" {
		return fmt.Sprintf("anonymous function with arity %d", f.Arity)
	}
	return fmt.Sprintf("%s#%d", f.Name.Clark(), f.Arity)
}

// IsAnonymous reports whether the function item came from an inline function
// expression rather than a named function.
func (f *FunctionItem) IsAnonymous() bool { return f.Name.Local == "" && f.Name.URI == "" }
