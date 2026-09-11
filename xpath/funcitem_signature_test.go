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
