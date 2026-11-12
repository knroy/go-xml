package xpath

import (
	"strings"
	"testing"
)

// nearlySpentBytes returns an XPath 3.1 context whose byte budget has only
// headroom bytes left, held so that Eval does not reset it.
func nearlySpentBytes(t *testing.T, headroom int) *Context {
	t.Helper()
	ctx := NewContext(nil, Builtins()).WithVersion(XPath31).HoldByteBudget()
	if err := ctx.ChargeBytes(MaxBytes - headroom); err != nil {
		t.Fatalf("charging the budget: %v", err)
	}
	return ctx
}

// TestJSONFallbackReplacementIsCharged: each undeliverable escape is replaced
// by the fallback's answer, so K escapes build K x |rep| bytes from a
// replacement built once. Those bytes were never charged, and 1 KiB of
// headroom built 64 KB. Both fn:parse-json and fn:json-to-xml reach it.
func TestJSONFallbackReplacementIsCharged(t *testing.T) {
	doc := "'[\"" + strings.Repeat(`\u0000`, 1000) + "\"]'"
	fb := "map{'fallback': function($s) { '" + strings.Repeat("X", 64) + "' }}"
	for _, fn := range []string{"parse-json", "json-to-xml"} {
		t.Run(fn, func(t *testing.T) {
			_, err := Eval("count("+fn+"("+doc+", "+fb+"))", nearlySpentBytes(t, 1024), nil)
			wantByteRefusal(t, err)
		})
	}
}

// TestJSONToXMLChargesTheNodeBudget: fn:json-to-xml built its tree with direct
// Append calls, outside the budgeted builder, so neither MaxNodes nor
// MaxItems saw it. fn:parse-json over the same text was refused.
func TestJSONToXMLChargesTheNodeBudget(t *testing.T) {
	ctx := NewContext(nil, Builtins()).WithVersion(XPath31)
	if err := ctx.countNodes(MaxNodes - 16); err != nil {
		t.Fatalf("charging the node budget: %v", err)
	}
	text := "[" + strings.TrimSuffix(strings.Repeat("1,", 1000), ",") + "]"
	_, err := jsonToXML(ctx, text, jsonOptions{duplicates: "retain"})
	if err == nil || !strings.Contains(err.Error(), "XPDY0130") {
		t.Fatalf("json-to-xml of 1001 values under 16 nodes of headroom: err = %v, want XPDY0130", err)
	}
}

// TestXMLToJSONResultIsCharged: the indented output is members x 2 x depth
// bytes, written into a local builder that never charged the budget, and 16
// bytes of headroom returned a 25 KB string.
func TestXMLToJSONResultIsCharged(t *testing.T) {
	doc := "'" + strings.Repeat("[", 50) + strings.Repeat("1,", 100) + "1" + strings.Repeat("]", 50) + "'"
	// json-to-xml's own text and attribute values are not string content the
	// byte budget counts, so the headroom left is spent by xml-to-json alone.
	_, err := Eval("xml-to-json(json-to-xml("+doc+"), map{'indent': true()})", nearlySpentBytes(t, 1024), nil)
	wantByteRefusal(t, err)
}
