package xpath

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// evalInstanceOf answers a single "instance of" probe as a Go bool.
//
// The probes below are all of the form "<fn>#<n> instance of function(...)",
// which yields exactly one xs:boolean, so anything else is a defect in the
// probe rather than a false answer.
func evalInstanceOf(t *testing.T, expr string) bool {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	seq, err := Eval(expr, ctx, cardinalityNS{})
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	if len(seq) != 1 {
		t.Fatalf("%s: got %d items, want 1", expr, len(seq))
	}
	a, ok := seq[0].(*xdm.Atomic)
	if !ok {
		t.Fatalf("%s: got %T, want xs:boolean", expr, seq[0])
	}
	return a.String() == "true"
}

// TestTypedFunctionTestReadsActualSignature is the failing-first test for the
// arity-only fallback.
//
// XPath 3.1 2.5.5.6 makes a typed function test succeed only when the
// function's OWN type is a subtype of the test's: parameters contravariant,
// return type covariant. Before function items were annotated from the
// manifest, builtinSignatures listed seventeen fn: functions and
// functionItemMatches judged every other function on ARITY ALONE -- so
// fn:concat#2, whose type is function(xs:anyAtomicType?, xs:anyAtomicType?)
// as xs:string, answered true for function(xs:date, xs:date) as xs:integer
// merely because both take two arguments.
//
// That is an observable wrong answer, not a missing diagnostic: the
// expression returns true where the specification requires false.
func TestTypedFunctionTestReadsActualSignature(t *testing.T) {
	cases := []struct {
		expr string
		want bool
		why  string
	}{
		// The headline defect. fn:concat returns xs:string, which is not a
		// subtype of xs:integer, so the return type alone settles it false.
		{"fn:concat#2 instance of function(xs:date, xs:date) as xs:integer", false,
			"fn:concat returns xs:string, which is no subtype of xs:integer"},
		// The same function against a test it genuinely satisfies: every
		// xs:date is an xs:anyAtomicType? and xs:string is xs:string.
		{"fn:concat#2 instance of function(xs:date, xs:date) as xs:string", true,
			"contravariance holds: xs:date is within xs:anyAtomicType?"},
		// A second family, so the fix is not concat-shaped. fn:upper-case
		// takes xs:string? and returns xs:string.
		{"fn:upper-case#1 instance of function(xs:integer) as xs:string", false,
			"xs:integer is not within fn:upper-case's xs:string? parameter"},
		{"fn:upper-case#1 instance of function(xs:string) as xs:string", true,
			"xs:string is within xs:string?, and the returns agree"},
		// Return-type covariance in the accepting direction: fn:count
		// returns xs:integer, which IS within xs:decimal.
		{"fn:count#1 instance of function(item()*) as xs:decimal", true,
			"xs:integer derives from xs:decimal"},
		{"fn:count#1 instance of function(item()*) as xs:string", false,
			"xs:integer is no subtype of xs:string"},
		// A math: function, to show the non-fn: namespaces are reached.
		{"math:pow#2 instance of function(xs:double?, xs:numeric) as xs:string", false,
			"math:pow returns xs:double?, which is no subtype of xs:string"},
	}
	for _, c := range cases {
		if got := evalInstanceOf(t, c.expr); got != c.want {
			t.Errorf("%s = %v, want %v (%s)", c.expr, got, c.want, c.why)
		}
	}
}

// TestEveryStandardBuiltinHasAManifestSignature fails if a function in one of
// the four namespaces the manifest covers is registered with no signature.
//
// The arity-only fallback makes such a hole invisible: an unannotated
// function silently answers true for every typed function test of its arity,
// which is exactly how fn:concat#2 came to claim it was a
// function(xs:date, xs:date) as xs:integer. A hole cannot be seen from the
// outside, because the wrong answer is a plausible one. This test is what
// makes it loud.
//
// extensionAllowlist, not a list of its own, is what may excuse a function:
// the same allowlist TestRegisteredFunctionsHaveManifestMetadata reads, so a
// function excused here must be excused there too, with a written reason.
// fn:concat needs no entry -- applyVariadicSignatures gives it its actual
// per-arity type, which is determined even though its arity is not.
func TestEveryStandardBuiltinHasAManifestSignature(t *testing.T) {
	l := Builtins().(*Library)
	checked := 0
	for _, fn := range l.fns {
		if !isManifestNamespace(fn.Name.URI) {
			continue
		}
		if _, ok := extensionAllowlist[specKey(fn.Name, fn.Arity)]; ok {
			continue
		}
		checked++
		if len(fn.Signature) != fn.Arity+1 {
			t.Errorf("%s#%d: registered in a manifest namespace with no "+
				"signature and no entry in extensionAllowlist", fn.Name.Local, fn.Arity)
		}
	}
	if checked == 0 {
		t.Fatal("checked no functions at all; the lookup is not running")
	}
	t.Logf("checked %d standard builtins", checked)
}

// TestFunctionItemSignaturesMatchSpecSignatures asserts the two consumers read
// one table.
//
// specSignatures is the single source for both call binding and function
// items. This walks every entry in it and checks the registered function item
// carries exactly those spellings, so a family migrated for call binding can
// never again reach function items unannotated -- which is precisely the
// defect this file exists for.
func TestFunctionItemSignaturesMatchSpecSignatures(t *testing.T) {
	l := Builtins()
	matched := 0
	for key, sig := range specSignatures {
		name, arity, ok := splitSpecEntryKey(key)
		if !ok {
			t.Errorf("%s: unreadable key", key)
			continue
		}
		fn, found := l.Lookup(name, arity)
		if !found {
			continue // in the manifest, not implemented here
		}
		if strings.Join(fn.Signature, "|") != strings.Join(sig, "|") {
			t.Errorf("%s: registered signature is %v, specSignatures has %v", key, fn.Signature, sig)
			continue
		}
		matched++
	}
	if matched == 0 {
		t.Fatal("matched no entries; the wiring is not running")
	}
	t.Logf("matched %d function items against specSignatures", matched)
}

// TestSequenceTypeSpellingIsLosslessForSubtyping pins the renderings that
// functionItemMatches compares.
//
// A signature is carried as a SPELLING and a typed function test is compared
// against it through SequenceType.String(), so a type that renders wider than
// it is makes the comparison answer about the wrong type. Three item types
// rendered as a bare "item()" before function items read the manifest:
// xs:numeric, which has no type code of its own; an array test; and a typed
// function test. That cost nothing while every function was judged on arity,
// and became four wrong QT3 answers the moment signatures were consulted --
// ArrayTest-063, ArrayTest-083, instanceof132 and instanceof133, all of which
// expect true and got false because an identical type failed to match itself.
//
// The QT3 suite is a measurement rather than a gate, so it cannot fail a
// build on a regression here. This can.
func TestSequenceTypeSpellingIsLosslessForSubtyping(t *testing.T) {
	cases := []struct{ src, want string }{
		{"xs:numeric?", "xs:numeric?"},
		{"array(*)", "array(*)"},
		{"array(xs:integer)", "array(xs:integer)"},
		{"array(function(xs:numeric?) as xs:numeric?)", "array(function(xs:numeric?) as xs:numeric?)"},
		{"function(*)", "function(*)"},
		{"function(item()) as xs:boolean", "function(item()) as xs:boolean"},
		{"function(item()*, function(item()) as xs:boolean) as item()*",
			"function(item()*, function(item()) as xs:boolean) as item()*"},
		{"map(xs:integer, xs:string)", "map(xs:integer, xs:string)"},
	}
	for _, c := range cases {
		st, err := ParseSequenceType(c.src, cardinalityNS{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got := st.String(); got != c.want {
			t.Errorf("ParseSequenceType(%q).String() = %q, want %q", c.src, got, c.want)
		}
	}
}

// TestFunctionSubtypingAcrossItemKinds pins the relations XPath 3.1 2.5.6.2
// gives between function, map and array tests.
//
// A map and an array ARE function items, so a function test can be wider than
// either; the reverse never holds. MapTest-052 and MapTest-054 turn on the
// first half and would otherwise be answered only by accident -- they passed
// before merely because a function test rendered as "item()".
func TestFunctionSubtypingAcrossItemKinds(t *testing.T) {
	cases := []struct {
		super, sub string
		want       bool
		why        string
	}{
		{"function(*)", "map(xs:integer, xs:string)", true, "a map is a function item"},
		{"function(*)", "array(xs:string)", true, "an array is a function item"},
		{"map(*)", "function(xs:anyAtomicType) as item()*", false,
			"a function is not a map"},
		{"array(*)", "function(xs:integer) as item()*", false,
			"a function is not an array"},
		// MapTest-054: map(K, V) viewed as function(xs:anyAtomicType) as V?.
		{"function(xs:anyAtomicType) as item()*", "map(xs:integer, xs:string)", true,
			"map(xs:integer, xs:string) is a function(xs:anyAtomicType) as xs:string?"},
		// Contravariance on a nested function parameter: instanceof133.
		{"function(item()) as xs:boolean", "function(item()*) as xs:boolean", true,
			"a function accepting item()* accepts every item()"},
		{"function(item()*) as xs:boolean", "function(item()) as xs:boolean", false,
			"a function accepting only item() does not accept item()*"},
		// Array member types.
		{"array(*)", "array(xs:string)", true, "array(*) covers every array"},
		{"array(xs:string)", "array(*)", false, "array(*) is wider than a typed one"},
	}
	for _, c := range cases {
		if got := spellingSubsumes(c.super, c.sub); got != c.want {
			t.Errorf("spellingSubsumes(%q, %q) = %v, want %v (%s)",
				c.super, c.sub, got, c.want, c.why)
		}
	}
}

// TestFunctionLookupCarriesTheSameSignatureAsANamedReference pins the two ways
// of obtaining one standard function item against each other.
//
// F&O 16.4.3 makes fn:function-lookup return the same function item a named
// function reference would, so "fn:abs#1" and "function-lookup(xs:QName('fn:abs'),1)"
// must answer every typed function test identically. They did not:
// NamedFunctionRef.Eval assigns Signature (funcitem.go) and the
// fn:function-lookup callback built the item without it, so the looked-up item
// reached functionItemMatches carrying no signature and was judged on ARITY
// ALONE -- the permissive branch written for inline functions that were never
// declared. fn:abs#1 correctly refused function(xs:date) as xs:integer while
// the looked-up fn:abs accepted it: one function item, two opposite answers.
//
// The negative cases are the ones that catch the regression. A test that only
// asserted the two agree on a type they both match would pass with the
// signature dropped, because arity-only matching says "true" to everything.
func TestFunctionLookupCarriesTheSameSignatureAsANamedReference(t *testing.T) {
	doc, err := xdm.ParseString(`<p>x</p>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// fn:function-lookup is context-dependent, so it needs a focus; the named
	// reference does not care, and both are evaluated under the same context
	// so that the focus cannot be what makes them differ.
	eval := func(expr string) bool {
		t.Helper()
		ctx := NewContext(doc.Root, Builtins())
		ctx.Version = XPath31
		ctx.LibraryVersion = XPath31
		seq, err := Eval(expr, ctx, cardinalityNS{})
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if len(seq) != 1 {
			t.Fatalf("%s: got %d items, want 1", expr, len(seq))
		}
		a, ok := seq[0].(*xdm.Atomic)
		if !ok {
			t.Fatalf("%s: got %T, want xs:boolean", expr, seq[0])
		}
		return a.String() == "true"
	}

	tests := []struct {
		test string
		want bool
		why  string
	}{
		{"function(xs:date) as xs:integer", false,
			"fn:abs is declared xs:numeric? -> xs:numeric?, which is neither"},
		{"function(xs:double) as xs:double", false,
			"xs:numeric? does not subsume the required xs:double return"},
		{"function(*)", true, "every function item is a function(*)"},
		{"function(xs:numeric?) as xs:numeric?", true,
			"fn:abs's own declared signature"},
	}
	for _, tc := range tests {
		named := eval("fn:abs#1 instance of " + tc.test)
		looked := eval(`function-lookup(xs:QName("fn:abs"),1) instance of ` + tc.test)
		if named != looked {
			t.Errorf("fn:abs#1 instance of %s = %v, but the same function "+
				"item from fn:function-lookup = %v; F&O 16.4.3 makes them the "+
				"same item, so a typed function test cannot tell them apart",
				tc.test, named, looked)
		}
		if named != tc.want {
			t.Errorf("fn:abs#1 instance of %s = %v, want %v (%s)",
				tc.test, named, tc.want, tc.why)
		}
	}
}

// TestFunctionLookupItemCarriesTheManifestSignature reads the field directly,
// so a regression is reported as the missing signature it is rather than as a
// downstream "instance of" answer.
func TestFunctionLookupItemCarriesTheManifestSignature(t *testing.T) {
	doc, err := xdm.ParseString(`<p>x</p>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := NewContext(doc.Root, Builtins())
	ctx.Version = XPath31
	ctx.LibraryVersion = XPath31
	seq, err := Eval(`function-lookup(xs:QName("fn:abs"),1)`, ctx, cardinalityNS{})
	if err != nil {
		t.Fatal(err)
	}
	if len(seq) != 1 {
		t.Fatalf("got %d items, want 1", len(seq))
	}
	item, ok := seq[0].(*xdm.FunctionItem)
	if !ok {
		t.Fatalf("got %T, want *xdm.FunctionItem", seq[0])
	}
	// Arity+1: the result type followed by one entry per parameter.
	if len(item.Signature) != item.Arity+1 {
		t.Fatalf("fn:function-lookup returned fn:abs#1 with Signature %v "+
			"(%d entries); want %d, the manifest signature a named function "+
			"reference carries", item.Signature, len(item.Signature), item.Arity+1)
	}
	fn, ok := Builtins().Lookup(xdm.QName{URI: xdm.NSFN, Local: "abs"}, 1)
	if !ok {
		t.Fatal("fn:abs#1 is not registered")
	}
	for i, want := range fn.Signature {
		if item.Signature[i] != want {
			t.Errorf("Signature[%d] = %q, want %q (the registered signature)",
				i, item.Signature[i], want)
		}
	}
}

// TestVariadicArityBoundIsTheSameByEveryRoute pins the two ways of naming a
// variadic function against each other.
//
// fn:concat is registered over a fixed arity range and SYNTHESISED above it,
// so both "concat#N" and fn:function-lookup can answer at an arity no entry
// exists for. The bound on that synthesis used to live at fn:function-lookup
// only, which was wrong twice over: the two routes disagreed above 2^20
// (concat#5000000 resolved while the lookup was empty), and the saturation
// value the bound exists to refuse stayed reachable through the named
// reference -- concat#9223372036854775807 built a function item claiming an
// arity of 2^63-1, which no argument slice can hold.
//
// The bound now sits in synthesizeVariadic, where both routes meet.
//
// The two routes report the refusal differently, and that is correct rather
// than a leftover: a named function reference to something unknown is a
// static error (XPST0017), while F&O 3.0 16.1.1 makes fn:function-lookup
// return the empty sequence. So this asserts "neither yields a function item",
// not "both raise the same thing".
func TestVariadicArityBoundIsTheSameByEveryRoute(t *testing.T) {
	doc, err := xdm.ParseString(`<p>x</p>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	answers := func(expr string) bool {
		t.Helper()
		ctx := NewContext(doc.Root, Builtins())
		ctx.Version = XPath31
		ctx.LibraryVersion = XPath31
		// The expression is an exists(...), so the answer is the boolean it
		// yields -- NOT whether one item came back. exists() returns false as
		// a perfectly good single item, which read as "answered" and made this
		// helper report the opposite of the truth for every refusal.
		seq, err := Eval(expr, ctx, cardinalityNS{})
		if err != nil {
			return false // XPST0017 from the named route
		}
		if len(seq) != 1 {
			t.Fatalf("%s: got %d items, want 1", expr, len(seq))
		}
		a, ok := seq[0].(*xdm.Atomic)
		if !ok {
			t.Fatalf("%s: got %T, want xs:boolean", expr, seq[0])
		}
		return a.String() == "true"
	}

	for _, tc := range []struct {
		arity string
		want  bool
		why   string
	}{
		{"123456", true, "the QT3 and XSLT 3.0 suites both reference concat#123456"},
		{"1048576", true, "what used to be exactly the ceiling"},
		{"1048577", true,
			"one past the OLD ceiling of 2^20. F&O 3.1 declares fn:concat " +
				"for two arguments or more and names no maximum, so this " +
				"case asserted a non-conformance until the variadic " +
				"descriptor made the arity stop sizing an allocation"},
		{"9223372036854775807", true,
			"an arity int can represent is an arity this can name. Calling " +
				"such an item is refused by the ordinary arity check " +
				"(XPTY0004) before anything is allocated, so it is inert " +
				"rather than dangerous -- see TestHugeArityItemIsInert"},
	} {
		named := answers("exists(concat#" + tc.arity + ")")
		looked := answers(`exists(function-lookup(xs:QName("fn:concat"),` + tc.arity + `))`)
		if named != looked {
			t.Errorf("concat#%s yields a function item = %v, but "+
				"function-lookup(fn:concat, %s) = %v; one bound governs both "+
				"routes, so they cannot disagree", tc.arity, named, tc.arity, looked)
		}
		if named != tc.want {
			t.Errorf("concat#%s yields a function item = %v, want %v (%s)",
				tc.arity, named, tc.want, tc.why)
		}
	}
}

// A synthesized fn:concat arity must carry the signature its arity implies.
//
// fn:concat is registered over a fixed range and synthesized above it, and
// synthesizeVariadic builds the entry by borrowing concat#2 and changing only
// the arity. The borrowed THREE-entry signature came with it, so an item whose
// arity was 101 carried a signature for 2 -- and functionItemMatches reads a
// signature whose length disagrees with the arity as "no declared type" and
// matches on arity alone.
//
// That is the fn:function-lookup defect arriving from the other side: one more
// way for a standard function to answer a typed function test as though it had
// never been declared. The registered and synthesized arities straddle
// concatMaxArity, so testing across the boundary is what catches it.
func TestSynthesizedConcatArityCarriesItsSignature(t *testing.T) {
	ctx := func() *Context {
		c := NewContext(nil, Builtins())
		c.Version = XPath31
		c.LibraryVersion = XPath31
		return c
	}

	// The structural half. A SYNTHESIZED arity describes its signature rather
	// than materialising it: the slice used to be arity+1 strings of one
	// repeated spelling, 16 bytes per argument at an arity the caller picks,
	// which is what made the ceiling in synthesizeVariadic load-bearing.
	// Asserting the descriptor rather than the slice is the point of the
	// change; the observable half below is what did not move.
	for _, n := range []int{2, 100, 101, 150} {
		fn, ok := lookupFor(ctx(), xdm.QName{URI: xdm.NSFN, Local: "concat"}, n)
		if !ok {
			t.Fatalf("fn:concat#%d does not resolve", n)
		}
		switch {
		case fn.VariadicSignature != nil:
			if len(fn.Signature) != 0 {
				t.Errorf("fn:concat#%d carries BOTH a variadic descriptor "+
					"and %d signature entries; the descriptor exists so the "+
					"slice need not, and two sources of one type will drift",
					n, len(fn.Signature))
			}
			v := fn.VariadicSignature
			if v.MinArity != 2 {
				t.Errorf("fn:concat#%d declares MinArity %d, want 2; F&O 3.1 "+
					"declares fn:concat for two arguments or more", n, v.MinArity)
			}
			if v.Result != "xs:string" || v.Parameter != "xs:anyAtomicType?" {
				t.Errorf("fn:concat#%d describes %s -> %s, want "+
					"xs:anyAtomicType? -> xs:string", n, v.Parameter, v.Result)
			}
		case len(fn.Signature) != n+1:
			// A REGISTERED arity keeps the ordinary slice, and there the
			// length must still follow the arity: a length that disagrees is
			// read as no declaration at all.
			t.Errorf("fn:concat#%d carries %d signature entries and no "+
				"variadic descriptor, want %d (one result plus one per "+
				"argument)", n, len(fn.Signature), n+1)
		}
	}

	// The observable half: a typed function test must consult those types.
	// 101 is past concatMaxArity, so this item is synthesized.
	params := func(typ string, n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = typ
		}
		return strings.Join(parts, ",")
	}
	mismatch := "concat#101 instance of function(" +
		params("xs:date", 101) + ") as xs:integer"
	seq, err := Eval(mismatch, ctx(), cardinalityNS{})
	if err != nil {
		t.Fatalf("%s: %v", mismatch, err)
	}
	if got := seq[0].(*xdm.Atomic).String(); got != "false" {
		t.Errorf("a synthesized fn:concat#101 matched "+
			"function(xs:date x101) as xs:integer (= %s); fn:concat is "+
			"declared xs:anyAtomicType? -> xs:string, so the test must "+
			"consult those types rather than the arity alone", got)
	}

	// The control: the item's own declared shape must still match, so the
	// signature is right and not merely long enough to defeat the fallback.
	own := "concat#101 instance of function(" +
		params("xs:anyAtomicType?", 101) + ") as xs:string"
	seq, err = Eval(own, ctx(), cardinalityNS{})
	if err != nil {
		t.Fatalf("%s: %v", own, err)
	}
	if got := seq[0].(*xdm.Atomic).String(); got != "true" {
		t.Errorf("a synthesized fn:concat#101 did not match its own declared "+
			"signature (= %s)", got)
	}
}

// TestConstructorFunctionItemsCarryTheirDeclaredType pins the xs: constructors
// against a typed function test.
//
// F&O 18.1 declares every built-in constructor uniformly --
// "xs:TYPE($arg as xs:anyAtomicType?) as xs:TYPE?" -- and 18.3 declares the
// three list types with the ITEM type repeated, "xs:IDREFS(...) as xs:IDREF*".
// None of that reached the function item: cmd/genfunctions extracts per-type
// proformas that section 18 never writes, so all 49 registered constructors
// carried an empty Signature and functionItemMatches fell to its arity-only
// branch. Every one-argument function test then answered TRUE, however absurd:
// xs:integer#1 was an instance of function(node()) as xs:integer? and
// xs:date#1 of function(xs:anyAtomicType?) as xs:integer?.
//
// The negatives are what catch a regression. Dropping the annotation restores
// arity-only matching, which says TRUE to everything, so a test made only of
// types the constructors really do match would stay green with the fix gone.
//
// Both acquisition paths are asserted together for the reason
// TestFunctionLookupCarriesTheSameSignatureAsANamedReference states: F&O 16.4.3
// makes fn:function-lookup yield the same function item a named reference
// does, so no typed function test may tell them apart.
func TestConstructorFunctionItemsCarryTheirDeclaredType(t *testing.T) {
	doc, err := xdm.ParseString(`<p>x</p>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	eval := func(expr string) bool {
		t.Helper()
		ctx := NewContext(doc.Root, Builtins())
		ctx.Version = XPath31
		ctx.LibraryVersion = XPath31
		seq, err := Eval(expr, ctx, cardinalityNS{})
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		a, ok := seq[0].(*xdm.Atomic)
		if len(seq) != 1 || !ok {
			t.Fatalf("%s: want one xs:boolean", expr)
		}
		return a.String() == "true"
	}

	tests := []struct {
		ctor string
		test string
		want bool
		why  string
	}{
		{"xs:integer", "function(xs:anyAtomicType?) as xs:integer?", true,
			"the signature F&O 18.1 declares for it"},
		{"xs:integer", "function(xs:anyAtomicType?) as xs:date?", false,
			"the integer constructor does not return an xs:date"},
		{"xs:integer", "function(node()) as xs:integer?", false,
			"it takes xs:anyAtomicType?, which does not admit a node"},
		{"xs:integer", "function(item()*) as xs:integer?", false,
			"an item()* is neither a single argument's type nor atomic"},
		// Parameters are CONTRAVARIANT, and this pair is the proof. XPath 3.1
		// 2.5.6.2 tests subtype(Ba_I, Aa_I) on arguments -- the TEST's
		// parameter against the FUNCTION's, not the reverse -- and notes
		// "Function arguments are contravariant". So a test naming a NARROWER
		// parameter than the constructor declares is SATISFIED: a function
		// accepting any atomic type does accept an xs:date. The widened case
		// above (node(), item()*) is what must fail.
		//
		// Pinned because the direction is easy to invert: an audit, the fix
		// for it, and the review of that fix each read it backwards and each
		// called this true case a bug.
		{"xs:date", "function(xs:date) as xs:date?", true,
			"a narrower parameter than declared is contravariantly satisfied"},
		{"xs:integer", "function(xs:date) as xs:integer?", true,
			"same rule: xs:date is a subtype of the declared xs:anyAtomicType?"},
		{"xs:date", "function(xs:anyAtomicType?) as xs:integer?", false,
			"the date constructor does not return an xs:integer"},
		{"xs:date", "function(xs:anyAtomicType?) as xs:date?", true,
			"its own declared signature"},
		// F&O 18.3: the result is the ITEM type, repeated. The minLength=1
		// facet belongs to the list, not to the return type, which is why the
		// declared result is "*" and not "+".
		{"xs:IDREFS", "function(xs:anyAtomicType?) as xs:IDREF*", true,
			"F&O 18.3 declares xs:IDREFS as returning xs:IDREF*"},
		{"xs:IDREFS", "function(xs:anyAtomicType?) as xs:IDREF?", false,
			"xs:IDREF? does not subsume the declared xs:IDREF*"},
		{"xs:numeric", "function(xs:anyAtomicType?) as xs:numeric?", true,
			"F&O 18.4 declares the xs:numeric union constructor the same way"},
		// The one constructor whose result is not its own type optional: the
		// union type xs:error has no member types, so its value space is
		// empty and the empty sequence is all it can return. QT3 xs-error-007
		// asserts this spelling exactly.
		{"xs:error", "function(xs:anyAtomicType?) as empty-sequence()", true,
			"F&O 18.4: xs:error has an empty value space"},
		// No negative is asserted for xs:error. "as xs:error?" is TRUE and
		// correctly so -- empty-sequence() is within it -- and "as xs:error" is
		// also true, because ParseSequenceType reads a bare xs:error in TYPE
		// position as item(), which is a separate and pre-existing question
		// about the type parser rather than about the signature this fixes.
		// The positive above is what xs-error-007 asserts, and the derivation
		// is guarded against regression by that suite case and by
		// TestEveryConstructorIsAnnotated.
		{"xs:string", "function(*)", true,
			"every function item is a function(*)"},
	}
	for _, tc := range tests {
		named := eval(tc.ctor + "#1 instance of " + tc.test)
		looked := eval(`function-lookup(xs:QName("` + tc.ctor +
			`"),1) instance of ` + tc.test)
		if named != looked {
			t.Errorf("%s#1 instance of %s = %v, but the same item from "+
				"fn:function-lookup = %v; F&O 16.4.3 makes them one item",
				tc.ctor, tc.test, named, looked)
		}
		if named != tc.want {
			t.Errorf("%s#1 instance of %s = %v, want %v (%s)",
				tc.ctor, tc.test, named, tc.want, tc.why)
		}
	}
}

// TestEveryConstructorIsAnnotated is the coverage half of the test above,
// which names ten constructors out of 49.
//
// The defect was uniform across the whole namespace, so a per-name test cannot
// show it is uniformly fixed. This asserts the invariant instead: every xs:
// entry carries a two-element signature whose parameter is the one F&O 18.1
// states for all of them, so a constructor registered later cannot quietly
// arrive unannotated and fall back to arity-only matching.
func TestEveryConstructorIsAnnotated(t *testing.T) {
	lib := Builtins().(*Library)
	n := 0
	for _, fn := range lib.fns {
		if fn.Name.URI != xdm.NSXS {
			continue
		}
		n++
		if len(fn.Signature) != fn.Arity+1 {
			t.Errorf("%s carries no declared type, so a typed function test "+
				"of it is decided on arity alone", displayName(fn.Name))
			continue
		}
		if fn.Signature[1] != "xs:anyAtomicType?" {
			t.Errorf("%s declares its parameter %q; F&O 18.1 gives every "+
				"constructor xs:anyAtomicType?", displayName(fn.Name),
				fn.Signature[1])
		}
	}
	if n == 0 {
		t.Fatal("no xs: constructors are registered, so this asserts nothing")
	}
}

// The variadic descriptor must cost the same at every arity, which is the
// whole reason it exists.
//
// Before it, synthesizeVariadic wrote arity+1 strings of one repeated
// spelling. Measured, that was 16 bytes per argument: 16,806,800 bytes at
// arity 2^20 against 2,280 after. The arity is supplied by the CALLER --
// fn:function-lookup takes it as an argument -- so the slice was an
// allocation an untrusted expression could size, and the ceiling in
// synthesizeVariadic was the only thing bounding it.
//
// That matters beyond tidiness. An audit twice proposed raising the ceiling
// to the host int limit as a conformance fix; with the slice in place that
// turned one function-lookup call into a request for roughly 10^11 GB. This
// test is what keeps the allocation from creeping back and making that
// proposal dangerous again.
func TestVariadicSignatureDoesNotGrowWithArity(t *testing.T) {
	ctx := func() *Context {
		c := NewContext(nil, Builtins())
		c.Version = XPath31
		c.LibraryVersion = XPath31
		return c
	}
	name := xdm.QName{URI: xdm.NSFN, Local: "concat"}

	// Allocation is measured rather than inferred, but the assertion is on
	// the DESCRIPTOR rather than on a byte count: a heap delta is noisy
	// enough that pinning one would make this test flap. What must hold is
	// that nothing proportional to the arity is retained.
	for _, n := range []int{2, 1000, 1 << 20} {
		fn, ok := lookupFor(ctx(), name, n)
		if !ok {
			t.Fatalf("fn:concat#%d does not resolve", n)
		}
		if fn.Arity != n {
			t.Errorf("fn:concat#%d reports arity %d", n, fn.Arity)
		}
		if n > concatMaxArity {
			if fn.VariadicSignature == nil {
				t.Errorf("fn:concat#%d was synthesized without a variadic "+
					"descriptor; the only other way to carry its type is a "+
					"slice whose length follows the arity", n)
			}
			if len(fn.Signature) != 0 {
				t.Errorf("fn:concat#%d materialised %d signature entries; "+
					"that is %d bytes of one repeated spelling, at an arity "+
					"the caller chooses",
					n, len(fn.Signature), len(fn.Signature)*16)
			}
		}
	}
}

// Both acquisition routes must describe the same function.
//
// This is the invariant a previous fix established for fn:function-lookup and
// named function references: F&O 16.4.3 makes the two yield the same function
// item, so no typed function test may tell them apart. The descriptor is a
// second thing that has to be copied on both paths, and copying it on only
// one would reintroduce exactly the divergence that fix closed.
func TestVariadicDescriptorAgreesOnBothAcquisitionRoutes(t *testing.T) {
	ctx := func() *Context {
		c := NewContext(nil, Builtins())
		c.Version = XPath31
		c.LibraryVersion = XPath31
		return c
	}
	const lookup = `function-lookup(QName(` +
		`"http://www.w3.org/2005/xpath-functions","concat"),101)`

	// Two things have to line up or this test says nothing, and the first two
	// versions of it each missed one. The function test must name the SAME
	// arity as the item, or the arity check settles it before the descriptor
	// is read; and the arity must be ABOVE concatMaxArity, or the item is a
	// registered one carrying an ordinary Signature and the descriptor is not
	// involved at all. 101 satisfies both: it is synthesized, and every test
	// below is written at that arity.
	params := func(typ string, n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = typ
		}
		return strings.Join(parts, ",")
	}
	for _, tc := range []struct {
		test string
		want string
		why  string
	}{
		{"function(" + params("xs:anyAtomicType?", 101) + ") as xs:string", "true",
			"this is fn:concat's own declared type, at its own arity"},
		{"function(" + params("xs:anyAtomicType?", 101) + ") as xs:date", "false",
			"fn:concat returns xs:string, so the result type must be read"},
		{"function(" + params("item()", 101) + ") as xs:string", "false",
			"item() is wider than the declared xs:anyAtomicType?, and " +
				"parameters are contravariant"},
	} {
		for _, expr := range []string{
			"concat#101 instance of " + tc.test,
			lookup + " instance of " + tc.test,
		} {
			seq, err := Eval(expr, ctx(), cardinalityNS{})
			if err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			if got := seq[0].(*xdm.Atomic).String(); got != tc.want {
				t.Errorf("%s = %s, want %s; %s\n  the two acquisition routes "+
					"must describe one function (F&O 16.4.3), so a descriptor "+
					"copied on only one of them is a divergence",
					expr, got, tc.want, tc.why)
			}
		}
	}

	// The arity a lookup reports must survive the descriptor, since
	// fn:function-arity reads it from the item rather than from a signature.
	seq, err := Eval("function-arity("+lookup+")", ctx(), cardinalityNS{})
	if err != nil {
		t.Fatal(err)
	}
	if got := seq[0].(*xdm.Atomic).String(); got != "101" {
		t.Errorf("function-arity of a looked-up concat#101 = %s, want 101", got)
	}
}

// MinArity is part of the declared type, not a fact about construction.
//
// F&O 3.1 declares fn:concat for two arguments or more. Carrying that on the
// descriptor rather than enforcing it only at synthesis is what lets the
// MATCHER refuse an item claiming concat#1: a function test of arity 1 must
// not match fn:concat however such an item was obtained.
func TestVariadicMinArityIsCarriedOnTheDescriptor(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	ctx.LibraryVersion = XPath31

	item := &xdm.FunctionItem{
		Name:  xdm.QName{URI: xdm.NSFN, Local: "concat"},
		Arity: 1,
		VariadicSignature: &xdm.VariadicSignature{
			MinArity: 2, Result: "xs:string", Parameter: "xs:anyAtomicType?",
		},
	}
	// The parameter type must be one the descriptor WOULD accept, or the
	// parameter check rejects the item and the test says nothing about
	// MinArity. An empty SequenceType{} spells "item()", which
	// xs:anyAtomicType? does not subsume -- the first version of this test
	// used it, passed with MinArity deleted, and was therefore vacuous.
	param, err := ParseSequenceType("xs:anyAtomicType?", cardinalityNS{})
	if err != nil {
		t.Fatal(err)
	}
	typ := SequenceType{
		HasFunctionArity: true,
		FunctionArity:    1,
		FunctionParams:   []SequenceType{param},
	}
	// The control: at arity 2 the same item and the same parameter type must
	// MATCH, which is what proves the refusal below is MinArity's doing.
	ok := *item
	ok.Arity = 2
	okTyp := typ
	okTyp.FunctionArity = 2
	okTyp.FunctionParams = []SequenceType{param, param}
	if !functionItemMatches(okTyp, &ok) {
		t.Fatal("fn:concat#2 did not match function(xs:anyAtomicType?, " +
			"xs:anyAtomicType?); the control must pass or the refusal below " +
			"proves nothing")
	}
	if functionItemMatches(typ, item) {
		t.Error("an item claiming fn:concat#1 matched a function test of " +
			"arity 1; fn:concat is declared for two arguments or more, so " +
			"MinArity must be consulted rather than assumed at construction")
	}
}

// The two representations must never disagree at the boundary between them.
//
// fn:concat is REGISTERED up to concatMaxArity, where applyVariadicSignatures
// materialises an ordinary Signature, and SYNTHESIZED above it, where
// synthesizeVariadic describes one instead. That is deliberate -- the
// registered slices are built once at library construction and bounded by a
// constant, not by anything a caller supplies -- but it means one function has
// two type representations, and a typed function test must not be able to tell
// which side of the boundary it is on.
//
// This is the check the earlier vacuous version of the acquisition-route test
// was missing: it probed concat#3, which is registered, and so never exercised
// the descriptor at all.
func TestConcatTypeIsTheSameEitherSideOfTheRegistrationBoundary(t *testing.T) {
	ctx := func() *Context {
		c := NewContext(nil, Builtins())
		c.Version = XPath31
		c.LibraryVersion = XPath31
		return c
	}
	params := func(typ string, n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = typ
		}
		return strings.Join(parts, ",")
	}

	// concatMaxArity is registered; one past it is synthesized. Asking the
	// same three questions on both sides is what proves they agree.
	for _, n := range []int{concatMaxArity, concatMaxArity + 1} {
		for _, tc := range []struct {
			ret  string
			par  string
			want string
		}{
			{"xs:string", "xs:anyAtomicType?", "true"},
			{"xs:date", "xs:anyAtomicType?", "false"},
			{"xs:string", "item()", "false"},
		} {
			expr := fmt.Sprintf("concat#%d instance of function(%s) as %s",
				n, params(tc.par, n), tc.ret)
			seq, err := Eval(expr, ctx(), cardinalityNS{})
			if err != nil {
				t.Fatalf("concat#%d: %v", n, err)
			}
			if got := seq[0].(*xdm.Atomic).String(); got != tc.want {
				kind := "registered"
				if n > concatMaxArity {
					kind = "synthesized"
				}
				t.Errorf("a %s concat#%d instance of function(%s x%d) as %s "+
					"= %s, want %s; the registered slice and the variadic "+
					"descriptor are two spellings of one declared type and "+
					"must answer alike",
					kind, n, tc.par, n, tc.ret, got, tc.want)
			}
		}
	}
}
