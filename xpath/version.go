package xpath

import "github.com/knroy/go-xml/xdm"

// Version selects the version of the XPath language an expression is written
// in.
//
// The two versions are not a superset relationship in every detail. XPath 3.0
// adds constructs — non-capturing groups and reluctant quantifiers in regular
// expressions, the "q" flag, "let", function items — but a 2.0 processor is
// required to *reject* those, not to accept them quietly. A stylesheet that
// relies on one and is run as 2.0 must fail here exactly as it would on any
// other conforming processor, rather than working here and failing there.
//
// The zero value is XPath20, so every existing caller keeps the behaviour it
// had before this type existed. Parse compiles 2.0; ParseVersion opts in.
type Version int

const (
	// XPath20 is XPath 2.0, as defined by the 2010 Recommendation.
	XPath20 Version = iota
	// XPath30 is XPath 3.0, as defined by the 2014 Recommendation. It admits
	// everything in 2.0 with the same meaning, plus the 3.0 additions.
	XPath30
	// XPath31 is XPath 3.1, as defined by the 2017 Recommendation. Its
	// additions are maps and arrays — two new kinds of item rather than new
	// syntax over the existing ones — together with the lookup operator that
	// reaches into them and the function libraries that build them.
	XPath31
)

// atLeast30 reports whether v admits the XPath 3.0 additions.
//
// Written as a method rather than a bare ">= XPath30" comparison at each use
// so that adding 3.1 later does not mean auditing every comparison for whether
// it meant "exactly 3.0" or "3.0 and later".
func (v Version) atLeast30() bool { return v >= XPath30 }

// atLeast31 reports whether v admits the XPath 3.1 additions, on the same
// reasoning as atLeast30.
func (v Version) atLeast31() bool { return v >= XPath31 }

// LookupVisible resolves a function the way a call does, hiding one the
// context's version does not have.
//
// Exported because fn:function-available has to give the same answer a call
// would: asking the library directly reported every function the engine can
// implement, so a 2.0 stylesheet was told that map:get and fn:parse-json were
// available to it and then refused when it called them.
func LookupVisible(ctx *Context, name xdm.QName, arity int) (Function, bool) {
	return lookupFor(ctx, name, arity)
}

// LookupDynamic resolves a DYNAMIC function reference -- one whose name is a
// value rather than a literal, as in fn:function-lookup and
// fn:function-available.
//
// It is LookupVisible except that a library implementing
// DynamicFunctionLibrary gets to answer for itself; see there for why a host
// language would scope the two differently.
func LookupDynamic(ctx *Context, name xdm.QName, arity int) (Function, bool) {
	if ctx != nil {
		if dl, ok := ctx.Funcs.(DynamicFunctionLibrary); ok {
			fn, found := dl.LookupDynamic(ctx, name, arity)
			if !found {
				return Function{}, false
			}
			if fn.Since > ctx.libraryVersion() {
				return Function{}, false
			}
			return fn, true
		}
	}
	return lookupFor(ctx, name, arity)
}

// lookupFor resolves a function call, hiding functions the context's version
// does not have.
//
// A function introduced in 3.0 must be invisible to a 2.0 expression rather
// than merely refusing to run: the error for calling it is XPST0017, the same
// "unknown function" every other processor raises, and reporting anything else
// would tell a stylesheet author their processor is unusual when it is the
// stylesheet that is wrong.
//
// Filtering here rather than inside FunctionLibrary keeps that interface as it
// was, which matters because the XSLT layer implements it: a library supplying
// xsl:function declarations neither knows nor needs to know about versions.
func lookupFor(ctx *Context, name xdm.QName, arity int) (Function, bool) {
	// A context may legitimately carry no library at all — NewContext takes a
	// nil one. FuncCall.Eval tests for that before it gets here, but a named
	// function reference reaches this directly, so the test has to be here
	// too: "not found" turns into the XPST0017 every caller already raises,
	// where the dereference would be a panic in a library call.
	if ctx == nil || ctx.Funcs == nil {
		return Function{}, false
	}
	fn, ok := ctx.Funcs.Lookup(name, arity)
	if !ok {
		// fn:concat is the one genuinely variadic function in the library.
		// Lookup is keyed by (name, arity), so it is registered at each of a
		// fixed range of arities — but the spec puts no bound on it, and
		// "concat#123456" is a legal reference the suite makes. Rather than
		// registering a hundred thousand entries, an arity past the
		// registered range is answered by synthesising the entry.
		if syn, made := synthesizeVariadic(ctx, name, arity); made {
			return syn, true
		}
		return Function{}, false
	}
	if fn.Since > ctx.libraryVersion() {
		return Function{}, false
	}
	return fn, true
}

// synthesizeVariadic builds the entry for a variadic function at an arity that
// was not pre-registered.
func synthesizeVariadic(ctx *Context, name xdm.QName, arity int) (Function, bool) {
	if name.URI != xdm.NSFN || name.Local != "concat" || arity < 2 {
		return Function{}, false
	}
	// The registered entries carry the real implementation; borrowing one
	// keeps a single definition of what fn:concat does. Its own arity is
	// irrelevant, since the call passes whatever arguments it was given.
	base, ok := ctx.Funcs.Lookup(name, 2)
	if !ok || base.Since > ctx.Version {
		return Function{}, false
	}
	base.Arity = arity
	return base, true
}

// String implements fmt.Stringer.
func (v Version) String() string {
	switch v {
	case XPath31:
		return "XPath 3.1"
	case XPath30:
		return "XPath 3.0"
	default:
		return "XPath 2.0"
	}
}

// AtLeast30 and AtLeast31 are the exported spellings of atLeast30 and
// atLeast31, for a host that has to make the same distinction the engine does
// — the XSLT layer decides whether a stylesheet gets the predeclared map: and
// array: prefixes on exactly this question.
func (v Version) AtLeast30() bool { return v.atLeast30() }

// AtLeast31 reports whether v admits the XPath 3.1 additions.
func (v Version) AtLeast31() bool { return v.atLeast31() }
