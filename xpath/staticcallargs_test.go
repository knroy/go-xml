package xpath

import "testing"

// callNamed returns the single StaticCall of the given local name in expr,
// which every case below writes exactly once.
func callNamed(t *testing.T, expr, local string) StaticCall {
	t.Helper()
	comp, err := CompileVersion(expr, nil, XPath31)
	if err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
	var found []StaticCall
	for _, c := range comp.StaticCalls() {
		if c.Name.Local == local {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("Compile(%q): found %d calls to %s, want 1", expr, len(found), local)
	}
	return found[0]
}

// TestStaticCallStringArgsReportsLiterals covers what StringArgs is for: a
// host that has to decide an error about the *value* of a literal argument
// needs that value without evaluating the call. Each position is reported
// independently, so a literal beside a computed argument is still available.
//
// fn:lang is used rather than a function of constants because a call whose
// arguments are all literals is constant-folded to its value, and a folded
// call is rightly no longer a call to report. Its second argument keeps every
// case here unfoldable -- which is also the shape the field exists for, since
// XSLT's current-merge-group() is context-dependent for the same reason.
func TestStaticCallStringArgsReportsLiterals(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want []string // "" means the position must be reported as nil
	}{
		{`lang('en', .)`, []string{"en", ""}},
		{`lang($v, .)`, []string{"", ""}},
		// A number is not a string literal: the field is about string values,
		// and reporting "1" here would let a host compare a name against a
		// value that was never written as one.
		{`lang(1, .)`, []string{"", ""}},
		// An expression that merely *evaluates* to a string is not a literal.
		{`lang(concat('a', $v), .)`, []string{"", ""}},
	} {
		got := callNamed(t, tc.expr, "lang")
		if got.Arity != len(tc.want) {
			t.Errorf("%s: arity = %d, want %d", tc.expr, got.Arity, len(tc.want))
			continue
		}
		for i, want := range tc.want {
			var have *string
			if i < len(got.StringArgs) {
				have = got.StringArgs[i]
			}
			switch {
			case want == "" && have != nil:
				t.Errorf("%s: arg %d reported %q, want it reported as not a "+
					"literal", tc.expr, i, *have)
			case want != "" && have == nil:
				t.Errorf("%s: arg %d reported as not a literal, want %q",
					tc.expr, i, want)
			case want != "" && have != nil && *have != want:
				t.Errorf("%s: arg %d = %q, want %q", tc.expr, i, *have, want)
			}
		}
	}
}

// TestStaticCallStringArgsAbsentWithoutLiterals pins the allocation contract
// stated on the field: a call with no string literal among its arguments
// reports no StringArgs at all, so the common case costs nothing. A host must
// therefore cope with a short or empty slice, which is what the length checks
// in checkMergeGroupCalls rely on.
func TestStaticCallStringArgsAbsentWithoutLiterals(t *testing.T) {
	got := callNamed(t, `lang($a, .)`, "lang")
	if got.StringArgs != nil {
		t.Errorf("StringArgs = %v, want nil for a call with no string literal",
			got.StringArgs)
	}
}

// TestStaticCallStringArgsEmptyForRef guards the field's meaning for a named
// function reference. "concat#3" has an arity but no arguments, so there is no
// argument whose literal value could be reported, and a host that reads
// StringArgs[0] on a Ref must find nothing rather than a stale value.
func TestStaticCallStringArgsEmptyForRef(t *testing.T) {
	comp, err := CompileVersion(`concat#3`, nil, XPath31)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	calls := comp.StaticCalls()
	if len(calls) != 1 || !calls[0].Ref {
		t.Fatalf("StaticCalls() = %+v, want one Ref", calls)
	}
	if len(calls[0].StringArgs) != 0 {
		t.Errorf("StringArgs = %v, want empty for a named function reference",
			calls[0].StringArgs)
	}
}

// TestStaticCallStringArgsAtDepth checks that the field survives the walk
// reaching a nested call. StaticCalls reports calls "at every depth", and a
// host deciding an error about a literal must get the same answer wherever the
// call is written -- inside a predicate, here, rather than at the top level.
func TestStaticCallStringArgsAtDepth(t *testing.T) {
	got := callNamed(t, `/a[lang('en', .)]`, "lang")
	if len(got.StringArgs) != 2 ||
		got.StringArgs[0] == nil || *got.StringArgs[0] != "en" {
		t.Errorf("StringArgs for a nested call = %v, want arg 0 = en",
			got.StringArgs)
	}
}
