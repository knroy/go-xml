package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// evalSpec evaluates an XPath 3.1 expression and renders the result.
//
// It delegates to evalCardinality, the helper the empty-sequence tests of
// commit 7668773 already use, so that the structural check and the twelve
// hand fixes are exercised through exactly the same path — including its
// namespace resolver, which binds the math: prefix.
func evalSpec(t *testing.T, expr string) (string, error) {
	t.Helper()
	return evalCardinality(expr)
}

// These tests pin the call-binding cardinality check: that it fires where the
// declared type forbids a cardinality, that it does NOT fire where the
// declared type permits one, and that its error is the XPTY0004 F&O requires.
//
// The controls matter as much as the positives. The plan forbids "blanket
// eager checks that change existing error precedence", so a check that
// refused an empty sequence for a parameter declared "?" would be a
// regression, not a fix — and the whole point of the mechanism is that it
// answers from the declared type rather than from a guess.

// TestCallBindingRejectsEmptyForRequiredParameter is the structural form of
// the twelve-function defect of commit 7668773.
//
// fn:substring's $start and $length and fn:subsequence's $startingLoc and
// $length are declared xs:double, with no "?", so F&O 3.1 2.5.4 makes an
// empty sequence a type error. Before the manifest, each of these needed its
// own hand-written guard inside the function body; now the declared type in
// specSignatures is what produces the error, at call binding, before the
// callback runs.
func TestCallBindingRejectsEmptyForRequiredParameter(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{`fn:substring("abcdef", ())`, "second argument of fn:substring()"},
		{`fn:substring("abcdef", 2, ())`, "third argument of fn:substring()"},
		{`fn:subsequence((1, 2, 3), ())`, "second argument of fn:subsequence()"},
		{`fn:subsequence((1, 2, 3), 2, ())`, "third argument of fn:subsequence()"},
	} {
		got, err := evalSpec(t, tc.expr)
		if err == nil {
			t.Errorf("%s returned %q; F&O declares that parameter without "+
				"\"?\", so an empty sequence is XPTY0004", tc.expr, got)
			continue
		}
		if !strings.Contains(err.Error(), "XPTY0004") {
			t.Errorf("%s raised %v; F&O 2.5.4 requires XPTY0004", tc.expr, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s raised %v; the message should name the %s", tc.expr, err, tc.want)
		}
		if !strings.Contains(err.Error(), "declared xs:double") {
			t.Errorf("%s raised %v; the message should name the declared type, "+
				"which is what the manifest contributes over a hand guard",
				tc.expr, err)
		}
	}
}

// TestCallBindingRejectsManyForSingletonParameter pins the other arm.
//
// fn:string-length($arg as xs:string?) permits zero or one item, so two is a
// type error. This arm is what the seventeen seeded entries exercise: every
// one of them declares its parameter "?" or "*", so none of them can fire the
// empty arm and only this one proves they are wired at all.
func TestCallBindingRejectsManyForSingletonParameter(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{`fn:string-length(("a", "b"))`, "xs:string?"},
		{`fn:normalize-space(("a", "b"))`, "xs:string?"},
		{`fn:string((1, 2))`, "item()?"},
		{`fn:number((1, 2))`, "xs:anyAtomicType?"},
	} {
		got, err := evalSpec(t, tc.expr)
		if err == nil {
			t.Errorf("%s returned %q; the declared type permits at most one item",
				tc.expr, got)
			continue
		}
		if !strings.Contains(err.Error(), "XPTY0004") {
			t.Errorf("%s raised %v; want XPTY0004", tc.expr, err)
		}
		if !strings.Contains(err.Error(), "declared "+tc.want) {
			t.Errorf("%s raised %v; the message should name the declared type %s",
				tc.expr, err, tc.want)
		}
	}
}

// TestCallBindingAcceptsWhatTheDeclarationPermits is the control set, and it
// is the half that the plan's "do not change error precedence" constraint
// turns on.
//
// Every case here passes a cardinality the declared type really does permit.
// A check that fired on any of them would have broken working expressions to
// fix a different bug — which is exactly what "retain lazy evaluation and
// error behavior where the spec permits it" rules out.
func TestCallBindingAcceptsWhatTheDeclarationPermits(t *testing.T) {
	for _, tc := range []struct{ expr, want, why string }{
		{`fn:substring((), 2)`, "", "$sourceString is declared xs:string?"},
		{`fn:contains("a", ())`, "true", "$arg2 is declared xs:string?"},
		{`fn:string-length(())`, "0", "$arg is declared xs:string?"},
		{`fn:normalize-space(())`, "", "$arg is declared xs:string?"},
		{`fn:string(())`, "", "$arg is declared item()?"},
		{`fn:count(())`, "0", "$arg is declared item()*"},
		{`fn:count((1, 2, 3))`, "3", "$arg is declared item()*"},
		{`fn:subsequence((), 1)`, "", "$sourceSeq is declared item()*"},
		{`fn:substring("abcdef", 2)`, "bcdef", "an ordinary call still works"},
		{`fn:subsequence((1, 2, 3), 2)`, "2 3", "an ordinary call still works"},
	} {
		got, err := evalSpec(t, tc.expr)
		if err != nil {
			t.Errorf("%s raised %v; it must not, because %s", tc.expr, err, tc.why)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q (%s)", tc.expr, got, tc.want, tc.why)
		}
	}
}

// TestUnmigratedFunctionsAreUnconstrained records that the migration is a
// narrowing rather than a switch.
//
// A function with no entry in specSignatures binds exactly as it did before
// the mechanism existed. That is what lets a family land on its own without a
// coordinated change to every other family, and it is why adding an entry can
// only turn a wrongly-accepted call into the required error, never the
// reverse.
func TestUnmigratedFunctionsAreUnconstrained(t *testing.T) {
	name, ok := parseSpecName("fn:upper-case")
	if !ok {
		t.Fatal("fn:upper-case did not parse as a manifest name")
	}
	if _, migrated := lookupSpecParams(name, 1); migrated {
		t.Skip("fn:upper-case has since been migrated; this control needs " +
			"an unmigrated function to be asserting anything")
	}
	// Declared xs:string?, so this is legal either way and proves only that
	// the unmigrated path still runs.
	if got, err := evalSpec(t, `fn:upper-case(())`); err != nil || got != "" {
		t.Errorf(`fn:upper-case(()) = %q, %v; want "", nil`, got, err)
	}
}

// TestAllowsEmptyAndAllowsMany pins the two predicates the check is built
// from, including the empty-sequence() case, which no occurrence indicator
// covers.
func TestAllowsEmptyAndAllowsMany(t *testing.T) {
	for _, tc := range []struct {
		spelling    string
		empty, many bool
	}{
		{"xs:string", false, false},
		{"xs:string?", true, false},
		{"xs:string*", true, true},
		{"xs:string+", false, true},
		{"item()", false, false},
		{"item()*", true, true},
		{"node()?", true, false},
		{"map(*)", false, false},
		{"array(*)*", true, true},
		{"empty-sequence()", true, false},
	} {
		st, err := ParseSequenceType(tc.spelling, nil)
		if err != nil {
			t.Errorf("parsing %s: %v", tc.spelling, err)
			continue
		}
		if got := st.AllowsEmpty(); got != tc.empty {
			t.Errorf("%s.AllowsEmpty() = %v, want %v", tc.spelling, got, tc.empty)
		}
		if got := st.AllowsMany(); got != tc.many {
			t.Errorf("%s.AllowsMany() = %v, want %v", tc.spelling, got, tc.many)
		}
	}
}

// TestPrefixedSpecKeysNameOtherNamespaces pins the widened key format.
//
// Before it, buildFunctionSpecs expanded every specSignatures key into the
// fn: namespace, so the manifest's math:, map: and array: rows were
// structurally unreachable: a "pi/0" key constrained a non-existent fn:pi,
// left math:pi untouched, and was rejected by
// TestMigratedSignaturesMatchManifest as a name the manifest does not
// describe. This test is what keeps the prefixed path from decaying back into
// dead code, by asserting the two properties separately: an unprefixed key
// still means fn:, and a prefixed one resolves to the namespace it names.
func TestPrefixedSpecKeysNameOtherNamespaces(t *testing.T) {
	for _, tc := range []struct {
		key   string
		want  xdm.QName
		arity int
	}{
		{"substring/2", xdm.QName{URI: xdm.NSFN, Local: "substring"}, 2},
		{"math:pi/0", xdm.QName{URI: xdm.NSMath, Local: "pi"}, 0},
		{"math:pow/2", xdm.QName{URI: xdm.NSMath, Local: "pow"}, 2},
		{"map:get/2", xdm.QName{URI: xdm.NSMap, Local: "get"}, 2},
		{"array:size/1", xdm.QName{URI: xdm.NSArray, Local: "size"}, 1},
		// Arity is read as a number, not as one digit: a ten-argument
		// proforma would otherwise be read as arity 1 and silently skipped.
		{"fake/10", xdm.QName{URI: xdm.NSFN, Local: "fake"}, 10},
	} {
		got, arity, ok := splitSpecEntryKey(tc.key)
		if !ok {
			t.Errorf("splitSpecEntryKey(%q) declined the key", tc.key)
			continue
		}
		if got != tc.want || arity != tc.arity {
			t.Errorf("splitSpecEntryKey(%q) = %v#%d, want %v#%d",
				tc.key, got, arity, tc.want, tc.arity)
		}
	}

	for _, bad := range []string{"nope", "bogus:pi/0", "pi/", "pi/x"} {
		if _, _, ok := splitSpecEntryKey(bad); ok {
			t.Errorf("splitSpecEntryKey(%q) accepted a key it cannot map", bad)
		}
	}

	// The end-to-end property: math:pi is reachable through lookupSpecParams,
	// which is what the fn:-only expansion made impossible.
	if _, ok := lookupSpecParams(xdm.QName{URI: xdm.NSMath, Local: "pi"}, 0); !ok {
		t.Error("math:pi#0 has a specSignatures entry but lookupSpecParams " +
			"does not find it; the prefixed key path is not live")
	}
	if _, ok := lookupSpecParams(xdm.QName{URI: xdm.NSFN, Local: "pi"}, 0); ok {
		t.Error("a prefixed key leaked into the fn: namespace as fn:pi")
	}
}
