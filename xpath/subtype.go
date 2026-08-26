package xpath

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// functionItemMatches decides a typed function test against a function item.
//
// A function type is a subtype of another when it accepts everything the other
// accepts and returns only what the other promises: parameters are
// contravariant and the return type covariant. So fn:name#1, whose type is
// function(node()?) as xs:string, is an instance of
// function(element(A)) as xs:string — every element(A) is a node()? — but not
// of function(item()) as xs:string, since an item() need not be a node.
//
// An item that records no signature is judged on arity alone. That is the
// answer for every function the library does not annotate, and it is the
// behaviour every function item had before signatures were recorded: a
// permissive answer rather than a wrong refusal.
func functionItemMatches(t SequenceType, fn *xdm.FunctionItem) bool {
	if !t.HasFunctionArity {
		return true // function(*)
	}
	if fn.Arity != t.FunctionArity {
		return false
	}
	if len(fn.Signature) != fn.Arity+1 {
		return true
	}
	// Covariance on the result: what the function returns must be within what
	// the test promises.
	if t.FunctionReturn != nil &&
		!spellingSubsumes(t.FunctionReturn.String(), fn.Signature[0]) {
		return false
	}
	// Contravariance on the parameters: the function must accept everything
	// the test's parameter type admits, not the other way round.
	for i, want := range t.FunctionParams {
		if !spellingSubsumes(fn.Signature[i+1], want.String()) {
			return false
		}
	}
	return true
}

// spellingSubsumes is the subtype relation over the type spellings a signature
// uses.
//
// It covers the item types the built-in signatures actually name, which is a
// small closed set: item(), node() and its kinds, and the atomic types. A
// spelling it does not recognise subsumes only itself, so an unknown type is
// never claimed to be wider than it is.
func spellingSubsumes(super, sub string) bool {
	superItem, superOcc := splitOccurrence(super)
	subItem, subOcc := splitOccurrence(sub)
	if !occurrenceSubsumes(superOcc, subOcc) {
		return false
	}
	return itemTypeSubsumes(superItem, subItem)
}

// splitOccurrence separates a type's occurrence indicator from its item type.
func splitOccurrence(s string) (item, occ string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	switch s[len(s)-1] {
	case '?', '*', '+':
		return s[:len(s)-1], s[len(s)-1:]
	}
	return s, ""
}

// occurrenceSubsumes reports whether the cardinality super permits every
// cardinality sub permits. "" is exactly one, "?" is zero or one, "+" is one
// or more, "*" is any number.
func occurrenceSubsumes(super, sub string) bool {
	superMin, superMax := occurrenceRange(super)
	subMin, subMax := occurrenceRange(sub)
	return superMin <= subMin && subMax <= superMax
}

func occurrenceRange(occ string) (min, max int) {
	switch occ {
	case "?":
		return 0, 1
	case "*":
		return 0, 1 << 30
	case "+":
		return 1, 1 << 30
	}
	return 1, 1
}

// itemTypeSubsumes reports whether every item of type sub is an item of type
// super, ignoring cardinality.
func itemTypeSubsumes(super, sub string) bool {
	if super == sub || super == "item()" {
		return true
	}
	if sub == "item()" {
		return false
	}
	// A node kind test: node() covers every kind, and a named kind covers
	// itself and its parameterised forms — element(A) is an element().
	if super == "node()" {
		return isNodeSpelling(sub)
	}
	if isNodeSpelling(super) || isNodeSpelling(sub) {
		return baseKind(super) == baseKind(sub) && baseKind(super) != ""
	}
	// Atomic types: the built-in hierarchy, as far as the signatures use it.
	return atomicSubsumes(super, sub)
}

// isNodeSpelling reports whether s names a node kind.
func isNodeSpelling(s string) bool { return baseKind(s) != "" }

// baseKind returns the kind test at the head of a spelling, dropping any
// parenthesised name: "element(A)" is "element".
func baseKind(s string) string {
	name := s
	if i := strings.IndexByte(s, '('); i >= 0 {
		name = s[:i]
	}
	switch name {
	case "node", "element", "attribute", "text", "comment",
		"processing-instruction", "document-node", "namespace-node":
		return name
	}
	return ""
}

// atomicAncestors maps an atomic type to the types it derives from, nearest
// first. Only the relations the built-in signatures need are listed; a type
// absent from the table subsumes only itself.
var atomicAncestors = map[string][]string{
	"xs:string":           {"xs:anyAtomicType"},
	"xs:NCName":           {"xs:Name", "xs:token", "xs:normalizedString", "xs:string", "xs:anyAtomicType"},
	"xs:Name":             {"xs:token", "xs:normalizedString", "xs:string", "xs:anyAtomicType"},
	"xs:token":            {"xs:normalizedString", "xs:string", "xs:anyAtomicType"},
	"xs:normalizedString": {"xs:string", "xs:anyAtomicType"},
	"xs:anyURI":           {"xs:anyAtomicType"},
	"xs:QName":            {"xs:anyAtomicType"},
	"xs:boolean":          {"xs:anyAtomicType"},
	"xs:double":           {"xs:numeric", "xs:anyAtomicType"},
	"xs:float":            {"xs:numeric", "xs:anyAtomicType"},
	"xs:decimal":          {"xs:numeric", "xs:anyAtomicType"},
	"xs:integer":          {"xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	// The integer family. A typed function test written against one of these
	// is not exotic — inline-fn-033 asserts that a function taking xs:integer
	// is an instance of "function(xs:long, xs:long) as xs:integer+", which is
	// contravariance over exactly this chain. With the derived names absent
	// each subsumed only itself and the assertion failed.
	"xs:long":               {"xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:int":                {"xs:long", "xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:short":              {"xs:int", "xs:long", "xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:byte":               {"xs:short", "xs:int", "xs:long", "xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:nonPositiveInteger": {"xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:negativeInteger":    {"xs:nonPositiveInteger", "xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:nonNegativeInteger": {"xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:positiveInteger":    {"xs:nonNegativeInteger", "xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:unsignedLong":       {"xs:nonNegativeInteger", "xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:unsignedInt":        {"xs:unsignedLong", "xs:nonNegativeInteger", "xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:unsignedShort":      {"xs:unsignedInt", "xs:unsignedLong", "xs:nonNegativeInteger", "xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:unsignedByte":       {"xs:unsignedShort", "xs:unsignedInt", "xs:unsignedLong", "xs:nonNegativeInteger", "xs:integer", "xs:decimal", "xs:numeric", "xs:anyAtomicType"},
	"xs:untypedAtomic":      {"xs:anyAtomicType"},
	"xs:date":               {"xs:anyAtomicType"},
	"xs:time":               {"xs:anyAtomicType"},
	"xs:dateTime":           {"xs:anyAtomicType"},
	"xs:numeric":            {"xs:anyAtomicType"},
}

// atomicSubsumes reports whether super is sub or one of its ancestors.
func atomicSubsumes(super, sub string) bool {
	if super == sub {
		return true
	}
	for _, a := range atomicAncestors[sub] {
		if a == super {
			return true
		}
	}
	return false
}

// builtinSignatures records the declared types of library functions, keyed by
// "local/arity" in the fn: namespace.
//
// Only the functions a typed function test is realistically written against
// are listed. An unlisted function is matched on arity alone, which is the
// permissive answer rather than a wrong refusal — so this table can grow
// without any entry already here changing meaning.
//
// Each entry is the return type followed by the parameter types, which is the
// order xdm.FunctionItem.Signature uses.
var builtinSignatures = map[string][]string{
	"name/1":            {"xs:string", "node()?"},
	"local-name/1":      {"xs:string", "node()?"},
	"namespace-uri/1":   {"xs:anyURI", "node()?"},
	"string/1":          {"xs:string", "item()?"},
	"number/1":          {"xs:double", "xs:anyAtomicType?"},
	"boolean/1":         {"xs:boolean", "item()*"},
	"not/1":             {"xs:boolean", "item()*"},
	"count/1":           {"xs:integer", "item()*"},
	"string-length/1":   {"xs:integer", "xs:string?"},
	"normalize-space/1": {"xs:string", "xs:string?"},
	"data/1":            {"xs:anyAtomicType*", "item()*"},
	"root/1":            {"node()?", "node()?"},
	"reverse/1":         {"item()*", "item()*"},
	"empty/1":           {"xs:boolean", "item()*"},
	"exists/1":          {"xs:boolean", "item()*"},
	"head/1":            {"item()?", "item()*"},
	"tail/1":            {"item()*", "item()*"},
}

// applyBuiltinSignatures annotates the library's entries from the table above.
//
// It runs after registration rather than at each call site so that the
// signatures sit together, where they can be read against the specification's
// function summary in one pass.
func applyBuiltinSignatures(l *Library) {
	for key, sig := range builtinSignatures {
		slash := strings.IndexByte(key, '/')
		local, arity := key[:slash], int(key[slash+1]-'0')
		name := xdm.QName{URI: xdm.NSFN, Local: local}
		fn, ok := l.Lookup(name, arity)
		if !ok {
			continue
		}
		fn.Signature = sig
		l.Add(fn)
	}
}
