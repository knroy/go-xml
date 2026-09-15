package xslt

import "github.com/knroy/go-xml/xdm"

// focusDependent lists the built-in functions F&O 3.1 and XSLT 3.0 declare
// ·focus-dependent·, by namespace, local name and arity. §19.8.8.15 turns on
// the property, and nothing else in the repository records it: the manifest
// (xpath/spec/function-signatures.json) carries signatures only.
//
// Each row is what the function's "Properties" paragraph says. F&O 3.1
// declares the property per arity -- "The zero-argument form of this
// function is ·deterministic·, ·context-dependent·, and ·focus-dependent·.
// The one-argument form ... ·focus-independent·" -- and the arity listed is
// the form so declared. Where a function has one form, the paragraph reads
// "This function is ... ·focus-dependent·". F&O §1.7.1 also lists the fn:
// rows in one sentence: "A number of functions including fn:base-uri#0,
// fn:data#0, fn:document-uri#0, fn:element-with-id#1, fn:id#1, fn:idref#1,
// fn:lang#1, fn:last#0, fn:local-name#0, fn:name#0, fn:namespace-uri#0,
// fn:normalize-space#0, fn:number#0, fn:path#0, fn:position#0, fn:root#0,
// fn:string#0, and fn:string-length#0 depend on the focus"; the per-function
// paragraphs add the five that sentence omits (node-name#0, nilled#0,
// has-children#0, generate-id#0, function-lookup#2).
//
// A function absent here and present in one of the standard namespaces is
// focus-independent. This is data: the rule that reads it is namedFunctionRef
// in streamexprs.go.
var focusDependent = map[funcKey]bool{
	// F&O 3.1 §2 accessors: "The zero-argument form of this function is
	// ... ·focus-dependent·."
	{xdm.NSFN, "node-name", 0}:    true, // §2.1
	{xdm.NSFN, "nilled", 0}:       true, // §2.2
	{xdm.NSFN, "string", 0}:       true, // §2.3
	{xdm.NSFN, "data", 0}:         true, // §2.4
	{xdm.NSFN, "base-uri", 0}:     true, // §2.5
	{xdm.NSFN, "document-uri", 0}: true, // §2.6
	// §4.4.1 fn:number, §5.4.4 fn:string-length, §5.4.5 fn:normalize-space:
	// "The zero-argument form of this function is ... ·focus-dependent·."
	{xdm.NSFN, "number", 0}:          true,
	{xdm.NSFN, "string-length", 0}:   true,
	{xdm.NSFN, "normalize-space", 0}: true,
	// §13 functions on nodes. name, local-name, namespace-uri, root,
	// has-children: "The zero-argument form ... ·focus-dependent·." lang:
	// "The one-argument form of this function is ... ·focus-dependent·."
	// path: the paragraph says "one-argument form" against a signature
	// list of path() and path($arg), and the Rules say "if the argument is
	// omitted ... the context item"; the form that depends on the focus is
	// path#0.
	{xdm.NSFN, "name", 0}:          true, // §13.1
	{xdm.NSFN, "local-name", 0}:    true, // §13.2
	{xdm.NSFN, "namespace-uri", 0}: true, // §13.3
	{xdm.NSFN, "lang", 1}:          true, // §13.4
	{xdm.NSFN, "root", 0}:          true, // §13.5
	{xdm.NSFN, "path", 0}:          true, // §13.6
	{xdm.NSFN, "has-children", 0}:  true, // §13.7
	// §14.5 id, element-with-id, idref: "The one-argument form of this
	// function is ... ·focus-dependent·." generate-id: "The zero-argument
	// form ... ·focus-dependent·."
	{xdm.NSFN, "id", 1}:              true, // §14.5.1
	{xdm.NSFN, "element-with-id", 1}: true, // §14.5.2
	{xdm.NSFN, "idref", 1}:           true, // §14.5.3
	{xdm.NSFN, "generate-id", 0}:     true, // §14.5.4
	// §15 context functions: "This function is ·deterministic·,
	// ·context-dependent·, and ·focus-dependent·."
	{xdm.NSFN, "position", 0}: true, // §15.1
	{xdm.NSFN, "last", 0}:     true, // §15.2
	// §16.1.1: "This function is ·deterministic·, ·context-dependent·,
	// ·focus-dependent·, and ·higher-order·."
	{xdm.NSFN, "function-lookup", 2}: true,

	// XSLT 3.0's own functions, in the same namespace.
	// §18.2.6, §18.2.7: "This function is deterministic, context-dependent,
	// and focus-dependent."
	{xdm.NSFN, "accumulator-before", 1}: true,
	{xdm.NSFN, "accumulator-after", 1}:  true,
	// §18.3 fn:copy-of, §18.4 fn:snapshot: "The zero-argument form of this
	// function is nondeterministic, focus-dependent, and
	// context-independent."
	{xdm.NSFN, "copy-of", 0}:  true,
	{xdm.NSFN, "snapshot", 0}: true,
	// §20.2.1 fn:key: "The two-argument form of this function is
	// deterministic, focus-dependent, and context-dependent."
	{xdm.NSFN, "key", 2}: true,
	// §20.4.1 fn:current: "This function is deterministic,
	// context-dependent, and focus-dependent."
	{xdm.NSFN, "current", 0}: true,
	// §20.4.2, §20.4.3: "This function is deterministic, focus-dependent,
	// and context-dependent" -- one Properties paragraph over both
	// signatures, so both arities are listed as the spec declares them,
	// though only the one-argument form's Rules reach for the context item.
	{xdm.NSFN, "unparsed-entity-uri", 1}:       true,
	{xdm.NSFN, "unparsed-entity-uri", 2}:       true,
	{xdm.NSFN, "unparsed-entity-public-id", 1}: true,
	{xdm.NSFN, "unparsed-entity-public-id", 2}: true,
}

// builtinFunctionNS reports whether uri is one of the namespaces whose
// functions are all specified -- and so all classified by focusDependent --
// rather than extension or stylesheet functions.
func builtinFunctionNS(uri string) bool {
	switch uri {
	case xdm.NSFN, xdm.NSXS, xdm.NSMap, xdm.NSArray, xdm.NSMath:
		return true
	}
	return false
}
