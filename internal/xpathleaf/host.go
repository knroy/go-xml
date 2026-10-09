package xpathleaf

import "github.com/knroy/go-xml/xdm"

// StableLibrary is implemented by a host's xpath.FunctionLibrary wrapper whose
// answers depend only on its own comparable fields, the library it wraps and
// Context.StaticHost. Package xpath caches a call site's resolution through
// such a wrapper, keyed on the wrapper's value; a wrapper that does not
// implement it is looked up on every call.
type StableLibrary interface {
	// WrappedLibrary returns the xpath.FunctionLibrary the wrapper consults.
	WrappedLibrary() any
}

// BindHost, set by the host, is consulted when package xpath turns the library
// function name into a function item (a named function reference,
// fn:function-lookup, or a partial application) at ctx, a *xpath.Context. A
// non-nil result maps the context of each later call of the item to the one
// the function runs under, putting back the host state it read at ctx: an XSLT
// function such as key() keeps answering for the transformation the item was
// made in, as it did when each transformation built its own library.
var BindHost func(ctx any, name xdm.QName) func(callCtx any) any
