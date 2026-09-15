package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A parameter the spec declares without "?" must reject the empty sequence
// with XPTY0004; one declared with "?" must accept it. Nothing in the call
// path enforces declared argument types -- registerFn stores only name and
// arity, and builtinSignatures is read only by "instance of function(...)" --
// so the cardinality rule holds only where the body checks it. These cases
// pin the five parameters where it did not.
//
// fn:subsequence is the reason this is a bug rather than a choice: its
// $startingLoc already refused an empty sequence while the $length beside it,
// declared identically, returned (). A function disagreeing with itself
// cannot be reading the same signature for both.
//
// Each fixed function is paired with a control -- an optional-parameter
// sibling of the same function, or the same parameter given a real value --
// so a regression that made the check unconditional is caught too.
func TestEmptySequenceRejectedByRequiredParameters(t *testing.T) {
	// F&O 3.1 signatures, quoted because they are the whole justification:
	//
	//   §5.4.3  fn:substring($sourceString as xs:string?,
	//                        $start as xs:double [, $length as xs:double])
	//   §14.1.6 fn:subsequence($sourceSeq as item()*,
	//                          $startingLoc as xs:double
	//                          [, $length as xs:double])
	//   §4.8.6  math:pow($x as xs:double?, $y as xs:numeric) as xs:double?
	//   §4.8.14 math:atan2($y as xs:double, $x as xs:double) as xs:double
	bad := []struct {
		expr     string
		declared string
	}{
		{`fn:substring("12345",(),2)`, "$start as xs:double"},
		{`fn:substring("12345",2,())`, "$length as xs:double"},
		{`fn:subsequence((1,2,3),())`, "$startingLoc as xs:double"},
		{`fn:subsequence((1,2,3),2,())`, "$length as xs:double"},
		{`math:pow(2,())`, "$y as xs:numeric"},
		{`math:atan2((),1)`, "$y as xs:double"},
		{`math:atan2(1,())`, "$x as xs:double"},
	}
	for _, c := range bad {
		t.Run(c.expr, func(t *testing.T) {
			got, err := evalCardinality(c.expr)
			if err == nil {
				t.Fatalf("%s (%s declared non-optional): got %v, want XPTY0004",
					c.expr, c.declared, got)
			}
			// The code is asserted, not merely that an error occurred: a
			// case failing for some unrelated reason would otherwise pass.
			if !strings.Contains(err.Error(), "XPTY0004") {
				t.Fatalf("%s: got %v, want XPTY0004", c.expr, err)
			}
		})
	}
}

// The controls. Every one of these is declared with "?" (or is simply a
// non-empty argument), so empty-in/empty-out is correct and the fixes above
// must not have disturbed it.
func TestEmptySequenceAcceptedByOptionalParameters(t *testing.T) {
	good := []struct {
		expr     string
		declared string
		want     string
	}{
		// The optional sibling of the parameter fixed in math:pow.
		{`math:pow((),2)`, "$x as xs:double?", ""},
		{`fn:contains("a",())`, "$arg2 as xs:string?", "true"},
		{`fn:substring((),1)`, "$sourceString as xs:string?", ""},
		{`math:log(())`, "$arg as xs:double?", ""},
		{`fn:string-to-codepoints(())`, "$arg as xs:string?", ""},
		// The fixed parameters still work when actually supplied.
		{`fn:substring("12345",2,2)`, "$start/$length supplied", "23"},
		{`fn:subsequence((1,2,3),2,1)`, "$startingLoc/$length supplied", "2"},
		{`math:atan2(0,1)`, "both supplied", "0"},
	}
	for _, c := range good {
		t.Run(c.expr, func(t *testing.T) {
			got, err := evalCardinality(c.expr)
			if err != nil {
				t.Fatalf("%s (%s): unexpected error %v", c.expr, c.declared, err)
			}
			if got != c.want {
				t.Fatalf("%s: got %q, want %q", c.expr, got, c.want)
			}
		})
	}
}

// evalCardinality evaluates src at XPath 3.1 and renders the result as a
// space-separated string; the empty sequence renders as "".
//
// Both Version and LibraryVersion are set: the math: functions carry
// Since: XPath30, and an unbound math: prefix raises XPST0081, which would
// look like a cardinality error without being one.
func evalCardinality(src string) (string, error) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	ctx.LibraryVersion = XPath31
	seq, err := Eval(src, ctx, cardinalityNS{})
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(seq))
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			parts = append(parts, v.StringValue())
		case *xdm.Atomic:
			parts = append(parts, v.String())
		}
	}
	return strings.Join(parts, " "), nil
}

type cardinalityNS struct{}

func (cardinalityNS) ResolvePrefix(p string) (string, bool) {
	switch p {
	case "fn":
		return "http://www.w3.org/2005/xpath-functions", true
	case "math":
		return "http://www.w3.org/2005/xpath-functions/math", true
	case "xs":
		return "http://www.w3.org/2001/XMLSchema", true
	}
	return "", false
}
func (cardinalityNS) DefaultElementNamespace() string { return "" }
func (cardinalityNS) DefaultFunctionNamespace() string {
	return "http://www.w3.org/2005/xpath-functions"
}
