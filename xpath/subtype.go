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
// An item that records no signature is judged on arity alone. Since every
// standard function now carries its manifest signature, that path is reached
// only by a function with no F&O declaration to read: an inline function whose
// parameters were written untyped, a partial application, an EXSLT or host
// extension. Refusing those would be the wrong answer rather than the strict
// one -- their declared type is whatever declared them, not "nothing" -- so
// arity remains the judgement there.
func functionItemMatches(t SequenceType, fn *xdm.FunctionItem) bool {
	if !t.HasFunctionArity {
		return true // function(*)
	}
	if fn.Arity != t.FunctionArity {
		return false
	}
	// A variadic function declares one parameter type for every arity, so it
	// carries that type rather than a slice repeating it. This branch is
	// taken before the Signature path and leaves that path untouched.
	if sig := fn.VariadicSignature; sig != nil {
		// MinArity is part of the declared type, not a fact about how the
		// item was built: fn:concat is declared for two arguments or more,
		// so an item claiming concat#1 matches no function test.
		if fn.Arity < sig.MinArity {
			return false
		}
		if t.FunctionReturn != nil &&
			!spellingSubsumes(t.FunctionReturn.String(), sig.Result) {
			return false
		}
		// Every parameter has the same declared type, so the arity has
		// already been checked above and only the spelling remains.
		for _, want := range t.FunctionParams {
			if !spellingSubsumes(sig.Parameter, want.String()) {
				return false
			}
		}
		return true
	}
	if len(fn.Signature) != fn.Arity+1 {
		return true // see the note above: no declared type to be strict about
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
//
// A typed function test is the exception: its spelling ends in its RETURN
// type, so the trailing indicator of "function(item()) as xs:boolean*"
// belongs to the xs:boolean, not to the function test. Stripping it here left
// "function(item()) as xs:boolean" paired with an occurrence of "*", which
// made the return type compare as exactly-one and answered false for
// MapTest-054, whose declared parameter is "function(xs:anyAtomicType) as
// item()*". A function test's own occurrence indicator would have to come
// after the return type's, and this package never writes one.
func splitOccurrence(s string) (item, occ string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if strings.HasPrefix(s, "function(") && strings.Contains(s, ") as ") {
		return s, ""
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
	// A function test in a signature. XPath 3.1 2.5.6.2 makes a map and an
	// array function items too, so a function test can be wider than either;
	// the reverse never holds. That asymmetry is why this is asked before the
	// map and array arms rather than after them: MapTest-052 asks whether
	// "function(map(*)) as xs:integer" is satisfied by a function declared
	// "function(function(*)) as xs:integer", which needs function(*) to
	// subsume map(*).
	if strings.HasPrefix(super, "function(") {
		return functionSpellingSubsumes(super, sub)
	}
	if strings.HasPrefix(sub, "function(") {
		return false // only item(), settled above, is wider than a function
	}
	// A map test in a signature. "map(*)" covers every map, and a typed one
	// covers a narrower typed one — the key types are atomic and run the same
	// way round, while the value types are sequence types compared whole.
	// MapTest-050 and 051 turn on exactly this.
	if strings.HasPrefix(super, "map(") || strings.HasPrefix(sub, "map(") {
		return mapSpellingSubsumes(super, sub)
	}
	// An array test. "array(*)" covers every array; a typed one covers a
	// narrower typed one, comparing member types as whole sequence types.
	if strings.HasPrefix(super, "array(") || strings.HasPrefix(sub, "array(") {
		return arraySpellingSubsumes(super, sub)
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

// The four process-global type-environment reads in this file are the ONLY
// ones left in xpath, and they are deliberate rather than overlooked.
//
// Everything here relates two type SPELLINGS written in the query text -- a
// declared function signature against a sequence type, one element test's type
// argument against another's. There is no node and no atomic value to take an
// environment from, so xdm.TypeEnvOf and xdm.TypeEnvOfAtomic have nothing to
// be called on, and the static context carries no environment either: xpath
// cannot import xsd (xsd imports xpath, because assertions and selectors
// contain XPath expressions -- see the comment on SchemaTypes in
// schema_types.go), so no *xsd.Schema reaches here.
//
// Answering one of these wrongly needs two schemas defining the same lexical
// type name differently AND a signature or type test naming it, which is a
// narrower shape than the node case this migration closed, and which neither
// suite exercises. Closing it properly means giving the static context a type
// environment of its own, which is remaining work rather than something to
// guess at here. Every consumer that HAS a node or a value -- "instance of",
// "castable as", the element and attribute tests, fn:id and fn:idref,
// xsl:copy's namespace-sensitivity check, xsl:validate -- goes through
// xdm.TypeEnvOf or xdm.TypeEnvOfAtomic instead.
//
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

// isManifestNamespace reports whether a function name belongs to one of the
// four namespaces xpath/spec/function-signatures.json describes.
//
// Everything else -- an EXSLT extension, a stylesheet's own function, a host
// application's registration -- has no F&O signature to look up, and refusing
// a typed function test against one on that ground would be wrong: XPath 3.1
// 2.5.5.6 judges a function item against its OWN declared type, and an
// extension's declared type is whatever its host says it is, not "nothing".
// Those keep the arity-only judgement.
func isManifestNamespace(uri string) bool {
	switch uri {
	case xdm.NSFN, xdm.NSMath, xdm.NSMap, xdm.NSArray:
		return true
	}
	return false
}

// applyBuiltinSignatures annotates the library's entries from the manifest.
//
// specSignatures in funcspec_table.go is the single source of declared types
// for the whole package. It has TWO consumers, and this is the second one:
//
//   - CALL BINDING reads it through lookupSpecParams to decide XPTY0004 on an
//     argument of the wrong cardinality.
//   - FUNCTION ITEMS read it here, so that a typed function test --
//     "fn:concat#2 instance of function(xs:date, xs:date) as xs:integer" --
//     is judged against fn:concat's ACTUAL type.
//
// Before this, function items read a separate seventeen-entry table while
// call binding read all 272, so migrating a family for call binding left
// function items unannotated and judging on arity alone. fn:concat#2 then
// answered TRUE for the test above, because both take two arguments. Those
// seventeen entries are gone: specSignatures was seeded from them and still
// carries them verbatim, so nothing they said was lost, and there is now one
// table to keep right instead of two that could disagree.
//
// It runs after registration rather than at each call site so that a
// function's declared type lives with every other function's, where it can be
// read against the Recommendation's function summary in one pass.
func applyBuiltinSignatures(l *Library) {
	applyVariadicSignatures(l)
	applyConstructorSignatures(l)
	for key, sig := range specSignatures {
		name, arity, ok := splitSpecEntryKey(key)
		if !ok || len(sig) != arity+1 {
			continue
		}
		fn, ok := l.Lookup(name, arity)
		if !ok {
			continue // in the Recommendation, not implemented here
		}
		fn.Signature = sig
		l.Add(fn)
	}
}

// applyVariadicSignatures annotates the functions whose proforma declares no
// fixed arity, so no manifest row can describe them.
//
// fn:concat is the only one in F&O 3.1. Its proforma -- 5.4.1 --
// is "fn:concat($arg1 as xs:anyAtomicType?, $arg2 as xs:anyAtomicType?, ...)
// as xs:string", so while it has no single ARITY it does have a fully
// determined TYPE at each arity: every parameter is xs:anyAtomicType? and the
// result is xs:string. cmd/genfunctions excludes it because a row would have
// to claim one arity and thereby deny the others; nothing about that stops
// the type being written out per arity here, which is what a typed function
// test needs.
//
// Leaving it to the arity-only fallback was the observable defect: fn:concat#2
// answered TRUE for "function(xs:date, xs:date) as xs:integer", since the only
// question asked was whether both take two arguments.
func applyVariadicSignatures(l *Library) {
	for arity := 2; ; arity++ {
		name := xdm.QName{URI: xdm.NSFN, Local: "concat"}
		fn, ok := l.Lookup(name, arity)
		if !ok {
			return // past the highest arity the library registers
		}
		sig := make([]string, 0, arity+1)
		sig = append(sig, "xs:string")
		for i := 0; i < arity; i++ {
			sig = append(sig, "xs:anyAtomicType?")
		}
		fn.Signature = sig
		l.Add(fn)
	}
}

// applyConstructorSignatures annotates the xs: constructor functions.
//
// They have no manifest row and cannot have one: F&O 18.1 states their
// signature ONCE for every built-in atomic type -- "eg:TYPE($arg as
// xs:anyAtomicType?) as eg:TYPE?" -- rather than writing a proforma per type,
// so cmd/genfunctions has nothing per-type to extract and
// TestRegisteredFunctionsHaveManifestMetadata excuses the whole namespace.
// That excuse was read as "these have no declared type", which is the
// opposite of what 18.1 says: the type is declared uniformly, not absent.
//
// The consequence was the permissive branch in functionItemMatches. An item
// carrying no signature is judged on ARITY ALONE, which is right for an inline
// function nobody declared and wrong here, so every one of the 49 registered
// constructors answered TRUE to every one-argument function test.
//
// The discriminating cases are the RETURN type and a WIDENED parameter, not a
// narrowed one. "xs:date#1 instance of function(xs:anyAtomicType?) as
// xs:integer?" is nonsense -- the date constructor does not return an integer
// -- and is now false; so is "xs:integer#1 instance of function(item()) as
// xs:integer?", because item() is not a subtype of the declared
// xs:anyAtomicType? and 2.5.6.2 tests subtype(Ba_I, Aa_I) on arguments.
//
// Note that "xs:integer#1 instance of function(xs:date) as xs:integer?" is
// TRUE and must stay true: parameters are CONTRAVARIANT, so a test naming a
// NARROWER parameter than the function declares is satisfied. A function that
// accepts any atomic type does accept an xs:date. This reading is easy to
// invert -- an audit, a fix and a review each got it backwards -- so the pair
// is pinned in the tests rather than left to the reader.
//
// Deriving the row instead of listing it is deliberate: 49 hand-written rows
// saying the same thing is 49 chances to mistype one, and a constructor
// registered later would silently get none. The parameter is xs:anyAtomicType?
// for every constructor without exception. Only the RESULT varies, and only in
// the two cases the switch below names: the three built-in list types of 18.3,
// and xs:error. Both are stated in the spec and both are pinned by the suite.
//
// This does NOT touch the schema constructors of an imported type
// (lookupSchemaConstructor). Those keep the permissive treatment, and rightly:
// F&O 18.5 gives a user-defined type's constructor the same shape, but the
// result spelling is a type name that spellingSubsumes has never heard of --
// it is in the schema, not in the closed set of built-in spellings -- and an
// unknown spelling subsumes only itself. Annotating them would make
// "myType#1 instance of function(xs:anyAtomicType?) as item()*" answer FALSE,
// turning a permissive wrong answer into a strict wrong one. The 49 here are
// exactly the ones whose result type this package can actually reason about.
func applyConstructorSignatures(l *Library) {
	for _, fn := range l.fns {
		if fn.Name.URI != xdm.NSXS || fn.Arity != 1 {
			continue
		}
		result := "xs:" + fn.Name.Local + "?"
		switch {
		case fn.Name.Local == "error":
			// xs:error is the one constructor whose result is NOT its own
			// type optional. F&O 18.4 gives the union type xs:error no member
			// types at all, so its value space is empty and the only value it
			// can ever return is the empty sequence; xs-error-007 asserts
			// exactly "xs:error#1 instance of function(xs:anyAtomicType?) as
			// empty-sequence()". Deriving "xs:error?" like the other 48 made
			// that case fail -- the first real evidence that these items are
			// now judged on their type at all, since it passed vacuously
			// while every constructor was matched on arity alone.
			result = "empty-sequence()"
		case listItemFacet[fn.Name.Local] != "":
			// F&O 18.3: the three built-in list types return the ITEM type,
			// repeated -- "xs:IDREFS($arg) as xs:IDREF*". The minLength=1
			// facet is the list's, not the return type's, which is why the
			// declared result is "*" and admits the empty sequence.
			result = "xs:" + listItemFacet[fn.Name.Local] + "*"
		}
		fn.Signature = []string{result, "xs:anyAtomicType?"}
		l.Add(fn)
	}
}

// functionSpellingSubsumes is itemTypeSubsumes for a function test on the
// supertype side.
//
// "function(*)" covers every function item, which by 2.5.6.2 includes every
// map and every array. A TYPED function test covers another function type by
// the ordinary rule -- parameters contravariant, return type covariant -- and
// covers a map or an array by the function type that map or array has: a
// map(K, V) is a function(xs:anyAtomicType) as V?, and an array(M) is a
// function(xs:integer) as M.
//
// MapTest-052 and -054 are exactly these two arms, and they are why this
// exists rather than being left to string equality: both were answered
// correctly before only because a function test rendered as "item()", which
// subsumed everything including the things it should not have.
func functionSpellingSubsumes(super, sub string) bool {
	superParams, superRet, superTyped, ok := splitFunctionSpelling(super)
	if !ok {
		return false
	}
	if !superTyped {
		// function(*): every function item, map and array qualifies.
		return strings.HasPrefix(sub, "function(") ||
			strings.HasPrefix(sub, "map(") || strings.HasPrefix(sub, "array(")
	}
	subParams, subRet, subTyped, ok := functionViewOf(sub)
	if !ok || !subTyped {
		// function(*) on the subtype side is wider than any typed test, so
		// no typed test subsumes it.
		return false
	}
	if len(superParams) != len(subParams) {
		return false
	}
	if !spellingSubsumes(superRet, subRet) {
		return false
	}
	for i := range superParams {
		// Contravariance: the SUBTYPE must accept everything the supertype's
		// parameter admits.
		if !spellingSubsumes(subParams[i], superParams[i]) {
			return false
		}
	}
	return true
}

// functionViewOf gives the function type of a spelling, so that a map or an
// array can be compared against a typed function test.
//
// 2.5.6.2: a map(K, V) behaves as function(xs:anyAtomicType) as V?, and an
// array(M) as function(xs:integer) as M. map(*) and array(*) have no declared
// member type, so their view is the untyped one.
func functionViewOf(s string) (params []string, ret string, typed bool, ok bool) {
	switch {
	case strings.HasPrefix(s, "function("):
		p, r, ty, o := splitFunctionSpelling(s)
		return p, r, ty, o
	case strings.HasPrefix(s, "map("):
		k, v, o := splitMapSpelling(s)
		if !o {
			return nil, "", false, false
		}
		if k == "" {
			return []string{"xs:anyAtomicType"}, "item()*", true, true
		}
		return []string{"xs:anyAtomicType"}, v + "?", true, true
	case strings.HasPrefix(s, "array("):
		m, o := splitArraySpelling(s)
		if !o {
			return nil, "", false, false
		}
		if m == "" {
			return []string{"xs:integer"}, "item()*", true, true
		}
		return []string{"xs:integer"}, m, true, true
	}
	return nil, "", false, false
}

// splitFunctionSpelling breaks "function(P1, P2) as R" into its parts.
//
// typed is false for the "function(*)" form, which fixes neither arity nor
// types. The parameter split is on TOP-LEVEL commas, since a parameter may
// itself be a function or map test carrying commas of its own --
// "function(item()*, function(item()) as xs:boolean) as item()*" is precisely
// the shape instanceof132 and instanceof133 are written in.
func splitFunctionSpelling(s string) (params []string, ret string, typed bool, ok bool) {
	if !strings.HasPrefix(s, "function(") {
		return nil, "", false, false
	}
	close := matchingParen(s, len("function(")-1)
	if close < 0 {
		return nil, "", false, false
	}
	body := strings.TrimSpace(s[len("function("):close])
	rest := strings.TrimSpace(s[close+1:])
	if body == "*" {
		return nil, "", false, rest == ""
	}
	if !strings.HasPrefix(rest, "as ") {
		return nil, "", false, false
	}
	ret = strings.TrimSpace(rest[len("as "):])
	if body != "" {
		params = splitTopLevel(body)
	}
	return params, ret, true, true
}

// splitArraySpelling breaks "array(M)" into its member type, answering "" for
// the "array(*)" form.
func splitArraySpelling(s string) (member string, ok bool) {
	if !strings.HasPrefix(s, "array(") || !strings.HasSuffix(s, ")") {
		return "", false
	}
	body := strings.TrimSpace(s[len("array(") : len(s)-1])
	if body == "*" {
		return "", true
	}
	return body, true
}

// arraySpellingSubsumes is itemTypeSubsumes for two array tests. Only an
// array is a subtype of an array test; array(*) covers every one, and a typed
// one covers a narrower typed one by its member type.
func arraySpellingSubsumes(super, sub string) bool {
	superMember, superOK := splitArraySpelling(super)
	subMember, subOK := splitArraySpelling(sub)
	if !superOK || !subOK {
		return false
	}
	if superMember == "" {
		return true // array(*) covers every array
	}
	if subMember == "" {
		return false
	}
	return spellingSubsumes(superMember, subMember)
}

// matchingParen returns the index of the ")" closing the "(" at open, or -1.
func matchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevel splits a parameter list on commas that are not nested inside
// parentheses.
func splitTopLevel(body string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(body[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(body[start:]))
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
