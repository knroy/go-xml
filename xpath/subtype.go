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
	// A map test in a signature. "map(*)" covers every map, and a typed one
	// covers a narrower typed one — the key types are atomic and run the same
	// way round, while the value types are sequence types compared whole.
	// MapTest-050 and 051 turn on exactly this.
	if strings.HasPrefix(super, "map(") || strings.HasPrefix(sub, "map(") {
		return mapSpellingSubsumes(super, sub)
	}
	// A node kind test: node() covers every kind, and a named kind covers
	// itself and its parameterised forms — element(A) is an element().
	if super == "node()" {
		return isNodeSpelling(sub)
	}
	if isNodeSpelling(super) || isNodeSpelling(sub) {
		if baseKind(super) != baseKind(sub) || baseKind(super) == "" {
			return false
		}
		if baseKind(super) == "element" {
			return elementSpellingSubsumes(super, sub)
		}
		return true
	}
	// Atomic types: the built-in hierarchy, as far as the signatures use it.
	return atomicSubsumes(super, sub)
}

// elementSpellingSubsumes is itemTypeSubsumes for two element tests.
//
// XPath 3.0's SequenceType subtype rules make element(N1, T1) a subtype of
// element(N2, T2) only when N2 is a wildcard or the same name as N1, T1 is T2
// or derived from it, AND either the SUPERTYPE admits a nilled element or the
// subtype does not. That last clause is what a one-argument element test turns
// on: element(e) is shorthand for element(e, xs:anyType?), which does admit a
// nilled element, so it is NOT a subtype of the two-argument element(e,
// xs:anyType), which does not.
//
// higher-order-functions-034 asks for exactly that distinction eight times
// over a function declared as="element(e)?": element(e)* subsumes it and
// element(e, xs:anyType)* does not.
//
// Only the type NAME is compared, not the schema derivation hierarchy, which
// is what keeps this to spellings: a signature naming a derived type subsumes
// only itself, the safe direction, as everywhere else in this file.
func elementSpellingSubsumes(super, sub string) bool {
	superName, superType, superNil := splitElementSpelling(super)
	subName, subType, subNil := splitElementSpelling(sub)
	if superName != "*" && superName != subName {
		return false
	}
	if superType != "" && superType != subType &&
		!derivesByRestriction(subType, superType) {
		// element() and element(N) place no constraint on the type, so an
		// untyped supertype subsumes any typed subtype; a typed one demands
		// the same type name, or one derived from it.
		return false
	}
	if subNil && !superNil {
		return false
	}
	return true
}

// splitElementSpelling breaks an element test into its name, its type name and
// whether that type admits a nilled element.
//
// element() and element(*) give a wildcard name and no type. A one-argument
// element(N) gives no type either -- it constrains only the name -- but it
// does admit a nilled element, which is the shorthand's whole difference from
// element(N, xs:anyType).
func splitElementSpelling(s string) (name, typ string, nillable bool) {
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return "*", "", true
	}
	body := strings.TrimSpace(s[open+1 : len(s)-1])
	if body == "" {
		return "*", "", true
	}
	if i := strings.IndexByte(body, ','); i >= 0 {
		name = strings.TrimSpace(body[:i])
		typ = strings.TrimSpace(body[i+1:])
		if strings.HasSuffix(typ, "?") {
			return name, strings.TrimSuffix(typ, "?"), true
		}
		return name, typ, false
	}
	return body, "", true
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

// schemaSubsumes is atomicSubsumes for the types an imported schema defines.
//
// The built-in table above cannot reach them: a schema type is whatever the
// author wrote, and the relations that matter -- what it restricts, what a
// union admits -- are recorded only in the schema. The xsd package registers
// both as it loads, keyed by annotation name, and a signature reaching here
// has been rendered into that same alphabet, so the two registries are enough
// to answer without carrying a schema into the evaluator.
//
// Three clauses, all from XPath 3.1 §2.5.6.2's Judgement 1 (subtype-itemtype)
// over the derives-from relation of §2.5.5:
//
//   - RESTRICTION. sub derives from super by walking sub's base chain. That is
//     the ordinary derivation clause, and it is what makes a restriction of
//     xs:date a subtype of xs:date (instanceof138).
//
//   - MEMBERSHIP. super is a union and sub is one of its members, transitively.
//     §2.5.5 makes union membership a clause of derives-from in its own right,
//     so every member type is a subtype of the union (instanceof136, 137).
//
//   - SUBSET. both are unions and every member of sub is subsumed by super.
//     A union is the union of its members' value spaces, so one union is a
//     subtype of another exactly when its members all fit -- which is why the
//     suite notes on instanceof139 that "there is a subtype relationship
//     between union(A,B,C) and union(A,B)", the direction being that the
//     SMALLER union is the subtype. instanceof140 and 141 are the two mixed
//     shapes: a union whose members are all integers under xs:integer, and
//     xs:integer under a union that has xs:decimal among its members.
//
// The walk is depth-bounded rather than cycle-tracked because a schema's
// derivation chain and union membership are both acyclic by construction --
// xsd rejects a cycle at load -- and a bound keeps a malformed registry from
// hanging the evaluator instead of merely answering wrongly.
func schemaSubsumes(super, sub string, depth int) bool {
	if depth <= 0 {
		return false
	}
	super, sub = annotationKeyOfSpelling(super), annotationKeyOfSpelling(sub)
	// Restriction: walk sub's base chain up toward super.
	for base := xdm.DerivedBase(sub); base != ""; base = xdm.DerivedBase(base) {
		if base == super {
			return true
		}
		if depth--; depth <= 0 {
			return false
		}
	}
	subParts := xdm.UnionMembersOf(sub)
	superParts := xdm.UnionMembersOf(super)
	if len(subParts) == 0 && len(superParts) == 0 {
		// Neither is a union and the derivation walk above already failed, so
		// there is no relation left to find. Returning here is also what stops
		// the recursion: the loop below would otherwise ask the same question
		// of the same pair one depth lower and answer true on the way out.
		return false
	}
	// Every value of sub has to be a value of super. When sub is a union that
	// is its members, one at a time; otherwise it is sub itself.
	if len(subParts) == 0 {
		subParts = []string{sub}
	}
	for _, sp := range subParts {
		part := spellingOfAnnotationKey(sp)
		if len(superParts) == 0 {
			// super is not a union: the part has to reach it directly, which
			// for a member that is itself a schema type means its own
			// derivation chain. instanceof140 is this shape -- both members of
			// s:integer-union derive from xs:integer.
			if !atomicSubsumesDepth(spellingOfAnnotationKey(super), part, depth-1) {
				return false
			}
			continue
		}
		// super is a union: the part has to land in one of its members.
		ok := false
		for _, mp := range superParts {
			if atomicSubsumesDepth(spellingOfAnnotationKey(mp), part, depth-1) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// annotationKeyOfSpelling converts a signature spelling into the key the type
// registries are indexed by.
//
// They differ for the built-ins alone. An annotation key in the XSD namespace
// is the BARE local name -- AnnotationName drops the namespace for xs: --
// while a sequence type renders the same type as "xs:date". A schema type in
// any other namespace is already Clark-notated on both sides and passes
// through untouched.
func annotationKeyOfSpelling(s string) string {
	if rest, ok := strings.CutPrefix(s, "xs:"); ok {
		return rest
	}
	return s
}

// spellingOfAnnotationKey is the inverse: it puts a registry name back into
// the alphabet the rest of this file compares in, so that a union member
// recorded as "date" is asked about as "xs:date" and reaches atomicAncestors.
func spellingOfAnnotationKey(s string) string {
	if strings.HasPrefix(s, "{") {
		return s
	}
	return "xs:" + s
}

// maxSchemaDerivationDepth bounds the registry walks in schemaSubsumes. A
// derivation chain or union nesting deeper than this is not something a real
// schema produces; the bound exists so a malformed one cannot hang the walk.
const maxSchemaDerivationDepth = 64

// atomicSubsumes reports whether super is sub or one of its ancestors.
func atomicSubsumes(super, sub string) bool {
	return atomicSubsumesDepth(super, sub, maxSchemaDerivationDepth)
}

// atomicSubsumesDepth is atomicSubsumes carrying the schema walk's budget.
func atomicSubsumesDepth(super, sub string, depth int) bool {
	if super == sub {
		return true
	}
	for _, a := range atomicAncestors[sub] {
		if a == super {
			return true
		}
	}
	// Neither is a built-in relation, so ask the schema. A name no schema
	// registered answers false there too, which is the unchanged behaviour for
	// every query that imports no schema.
	return schemaSubsumes(super, sub, depth)
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

// mapSpellingSubsumes is itemTypeSubsumes for the map tests a signature can
// name.
//
// Both sides have to be maps: nothing else is a subtype of a map test, and a
// map is a subtype of no other item type except item() and the function tests,
// which itemTypeSubsumes settles before reaching here.
func mapSpellingSubsumes(super, sub string) bool {
	superKey, superVal, superOK := splitMapSpelling(super)
	subKey, subVal, subOK := splitMapSpelling(sub)
	if !superOK || !subOK {
		return false
	}
	if superKey == "" {
		return true // map(*) covers every map
	}
	if subKey == "" {
		// map(*) is wider than any typed map test, so it is subsumed by none.
		return false
	}
	return atomicSubsumes(superKey, subKey) && spellingSubsumes(superVal, subVal)
}

// splitMapSpelling breaks "map(K, V)" into its two halves, answering "" for
// both on the "map(*)" form.
//
// The split is on the *top-level* comma, since a value type may itself be a
// map test with a comma of its own: "map(xs:integer, map(xs:integer, xs:string))".
func splitMapSpelling(s string) (key, val string, ok bool) {
	if !strings.HasPrefix(s, "map(") || !strings.HasSuffix(s, ")") {
		return "", "", false
	}
	body := strings.TrimSpace(s[len("map(") : len(s)-1])
	if body == "*" {
		return "", "", true
	}
	depth := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				return strings.TrimSpace(body[:i]), strings.TrimSpace(body[i+1:]), true
			}
		}
	}
	return "", "", false
}

// derivesByRestriction reports whether sub's schema derivation chain reaches
// super.
//
// XPath 3.1 2.5.6.2's subtype-itemtype judgement for two element tests asks
// that the SUBTYPE's type annotation be DERIVED FROM the supertype's, not
// equal to it. elementSpellingSubsumes compared the two names outright, so
// "element(*, s:restrictedUnion)" was not a subtype of
// "element(*, s:approximateDate)" even though the schema declares the first
// as a restriction of the second. FunctionCall-051 is that pair, reached
// through the contravariant parameter rule for function types.
//
// It is deliberately ONLY the restriction walk -- the first clause of
// schemaSubsumes -- and not schemaSubsumes itself. That function also relates
// two types whose union member sets stand in a subset relation, which is
// right for atomic union subtyping and wrong here: a restriction of a union
// keeps its members, so schemaSubsumes answers true in BOTH directions for
// this very pair, and routing element tests through it would turn
// FunctionCall-052 -- which asserts the reverse is false -- from a pass into a
// failure.
func derivesByRestriction(sub, super string) bool {
	sub, super = annotationKeyOfSpelling(sub), annotationKeyOfSpelling(super)
	depth := maxSchemaDerivationDepth
	for base := xdm.DerivedBase(sub); base != ""; base = xdm.DerivedBase(base) {
		if base == super {
			return true
		}
		if depth--; depth <= 0 {
			return false
		}
	}
	return false
}
