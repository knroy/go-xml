package xpath

import (
	"strings"
	"testing"
)

// The parser is recursive descent with no depth bound, so a deeply nested
// expression exhausted the goroutine stack. Go reports that as "fatal error:
// stack overflow", which recover() cannot catch — the process dies rather
// than the request failing. Measured before the bound existed: 130,000 nested
// parentheses crashed at Go's default 1 GB stack, and 7,000 crashed under a
// 32 MB one.
//
// Reachable through a stylesheet's @select, which is not bounded by the XML
// parser's own nesting limit: that limit counts ELEMENTS, and the expression
// lives inside a single attribute value.
func TestDeepExpressionIsRefusedNotFatal(t *testing.T) {
	for _, depth := range []int{maxParseDepth + 1, 200000} {
		expr := strings.Repeat("(", depth) + "1" + strings.Repeat(")", depth)
		_, err := Compile(expr, nil)
		if err == nil {
			t.Fatalf("depth %d was accepted; the bound did not apply", depth)
		}
		if !strings.Contains(err.Error(), "nesting exceeds") {
			t.Fatalf("depth %d refused for the wrong reason: %v", depth, err)
		}
	}
}

// A type nests on its own path. parseSequenceType recurses into itself for a
// parenthesised item type, for a function test's argument and return types,
// and for the member types of map() and array() — none of which passes
// through parseExprSingle, so the bound above did not apply to any of them
// and each crashed the process exactly as an unbounded expression did. 400 KB
// of "1 instance of ((((…item()…))))" was enough at Go's default 1 GB stack.
func TestDeepTypeIsRefusedNotFatal(t *testing.T) {
	nest := func(open, close string, depth int, inner string) string {
		return "1 instance of " + strings.Repeat(open, depth) + inner +
			strings.Repeat(close, depth)
	}
	cases := map[string]string{
		"parenthesized": nest("(", ")", maxParseDepth+1, "item()"),
		"huge":          nest("(", ")", 200000, "item()"),
		"function":      nest("function(", ") as item()", 5000, "item()"),
		"array":         nest("array(", ")", 5000, "item()"),
		"map":           nest("map(xs:string, ", ")", 5000, "item()"),
	}
	for name, expr := range cases {
		_, err := CompileVersion(expr, nil, XPath31)
		if err == nil {
			t.Errorf("%s: accepted; the bound did not apply", name)
			continue
		}
		if !strings.Contains(err.Error(), "nesting exceeds") {
			t.Errorf("%s: refused for the wrong reason: %v", name, err)
		}
	}
}

// The type bound must leave the types people really write alone.
func TestOrdinaryTypeNestingStillCompiles(t *testing.T) {
	for _, expr := range []string{
		"1 instance of item()",
		"1 instance of (item())",
		"1 instance of xs:integer+",
		"() instance of empty-sequence()",
		"$f instance of function(item()) as item()",
		"$m instance of map(xs:string, array(xs:integer))",
		"$f instance of (function(item()) as item())",
	} {
		if _, err := CompileVersion(expr, nil, XPath31); err != nil {
			t.Errorf("%s: %v", expr, err)
		}
	}
}

// The bound must not refuse expressions anyone actually writes. Every nesting
// construct funnels through parseExprSingle or parseSequenceType, so each is
// checked.
func TestOrdinaryNestingStillCompiles(t *testing.T) {
	cases := map[string]string{
		"parens":     strings.Repeat("(", 100) + "1" + strings.Repeat(")", 100),
		"predicates": "a" + strings.Repeat("[b", 100) + strings.Repeat("]", 100),
		"calls":      strings.Repeat("not(", 100) + "true()" + strings.Repeat(")", 100),
		"ifs": strings.Repeat("if (1) then ", 100) + "2" +
			strings.Repeat(" else 3", 100),
		"for": strings.Repeat("for $x in 1 return ", 100) + "$x",
	}
	for name, expr := range cases {
		if _, err := Compile(expr, nil); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
