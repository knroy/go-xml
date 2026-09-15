package xpath

import "testing"

// TestDefaultCollationReportsStaticDefault covers fn:default-collation.
//
// F&O 3.0 15.7 (functions-and-operators-rec30.xml:26642): "Returns the value
// of the default collation property from the static context."
//
// The handler ignored its context and returned a hardcoded codepoint URI, so
// it contradicted the very context it sat in: on the same compiled expression
// with an ASCII-case-insensitive default, fn:contains and fn:compare honoured
// that default while fn:default-collation still said codepoint.
//
// The suite cannot see this. The only two cases naming the function are
// collations-1004 and -1005, and neither sets a non-codepoint default, so the
// default must be set explicitly here.
func TestDefaultCollationReportsStaticDefault(t *testing.T) {
	const ciURI = HTMLASCIICaseInsensitive

	coll, err := ResolveCollation(ciURI)
	if err != nil {
		t.Fatalf("ResolveCollation(%q): %v", ciURI, err)
	}

	eval := func(t *testing.T, expr string, c *Compiled) string {
		t.Helper()
		seq, err := c.Eval(NewContext(nil, Builtins()))
		if err != nil {
			t.Fatalf("eval %q: %v", expr, err)
		}
		if len(seq) != 1 {
			t.Fatalf("eval %q: %d items, want 1", expr, len(seq))
		}
		return seq[0].(interface{ String() string }).String()
	}

	compile := func(t *testing.T, expr string) *Compiled {
		t.Helper()
		c, err := Compile(expr, nil)
		if err != nil {
			t.Fatalf("compile %q: %v", expr, err)
		}
		return c
	}

	// With a non-codepoint static default, the function must report THAT URI.
	{
		expr := `default-collation()`
		c := compile(t, expr).WithDefaultCollationURI(coll, ciURI)
		if got := eval(t, expr, c); got != ciURI {
			t.Errorf("default-collation() = %q, want %q", got, ciURI)
		}
	}

	// The default is not merely reported, it is in force: these two agree
	// with the URI above rather than contradicting it.
	for _, tc := range []struct{ expr, want string }{
		{`contains("ABC","b")`, "true"},
		{`compare("a","A")`, "0"},
	} {
		c := compile(t, tc.expr).WithDefaultCollationURI(coll, ciURI)
		if got := eval(t, tc.expr, c); got != tc.want {
			t.Errorf("%s under the case-insensitive default = %q, want %q",
				tc.expr, got, tc.want)
		}
	}

	// Control: with no default set, the spec's stated default still holds.
	{
		expr := `default-collation()`
		if got := eval(t, expr, compile(t, expr)); got != CodepointCollation {
			t.Errorf("implicit default-collation() = %q, want %q",
				got, CodepointCollation)
		}
	}
}
