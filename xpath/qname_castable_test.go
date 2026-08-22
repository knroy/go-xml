package xpath

import (
	"strings"
	"testing"
)

// Casting to xs:QName is defined only from a literal string: the namespace
// comes from the static context, and only a literal is folded where the prefix
// bindings are in scope.
func TestQNameCastableRequiresLiteral(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		// A literal is castable — this is the case the rule exists to allow.
		{`"ABC" castable as xs:QName`, "true"},
		// Parentheses do not make it computed.
		{`("ABC") castable as xs:QName`, "true"},
		// A variable is not a literal, however well-formed its value looks.
		// Answering true here let a caller believe a prefix had resolved.
		{`for $v in "ABC" return $v castable as xs:QName`, "false"},
		// A value that is already a QName carries its own binding and needs
		// nothing from the static context, so the restriction does not apply.
		{`QName("", "lname") castable as xs:QName`, "true"},
		// A lexically invalid literal is still not castable.
		{`"not a qname" castable as xs:QName`, "false"},
	}
	for _, c := range cases {
		got := evalStr(t, testDoc, c.expr)
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// "cast as" reports the same restriction as an error rather than as false.
func TestQNameCastNonLiteralIsError(t *testing.T) {
	err := evalErr(t, testDoc, `for $v in "ABC" return $v cast as xs:QName`)
	if !strings.Contains(err.Error(), "literal") {
		t.Errorf("error = %v, want it to name the literal restriction", err)
	}
}

// fn:in-scope-prefixes takes element(), so a document node is a type error
// rather than something to answer about the root element instead.
func TestInScopePrefixesRequiresElement(t *testing.T) {
	err := evalErr(t, testDoc, `in-scope-prefixes(/)`)
	if !strings.Contains(err.Error(), "XPTY0004") {
		t.Errorf("error = %v, want XPTY0004", err)
	}
	// An element still works.
	if got := evalStr(t, testDoc, `count(in-scope-prefixes(/*))`); got == "" {
		t.Error("in-scope-prefixes on an element should succeed")
	}
}
