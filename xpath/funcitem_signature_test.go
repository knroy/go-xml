package xpath

import (
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
		{"1048576", true, "exactly the bound, which is inclusive"},
		{"1048577", false, "one past the bound"},
		{"9223372036854775807", false,
			"the saturation value; no argument slice can hold it"},
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
