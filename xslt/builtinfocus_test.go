package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A mode with no template rules reads no focus, so the built-in shallow-copy
// rule must not copy the runtime and its XPath context for every node it
// visits. Before, 1,000 <a x="1">t</a> cost about 10,000 allocations (10 per
// element); the budget below is 6 per element.
func TestBuiltInShallowCopyWithoutRulesDoesNotCopyFocus(t *testing.T) {
	const n = 1000
	s := compileString(t, `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:mode on-no-match="shallow-copy"/></xsl:stylesheet>`)
	src := "<d>" + strings.Repeat(`<a x="1">t</a>`, n) + "</d>"
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var res *Result
	allocs := testing.AllocsPerRun(3, func() {
		res, err = s.Transform(context.Background(), doc.Root, TransformOptions{})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res.Nodes); got != 1 {
		t.Fatalf("result has %d items, want 1", got)
	}
	if per := allocs / n; per > 6 {
		t.Errorf("%.1f allocations per element, want <= 6", per)
	}
}
