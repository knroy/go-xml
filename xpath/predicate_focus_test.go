package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A predicate applied to many items moves one focus context along instead
// of copying the context per item, and does so only when nothing in it can
// keep that context past the item: a function item could, so an expression
// that can build or run one gets a fresh context per item as before.
func TestPredicateSharesOneFocus(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("<r>")
	for i := 0; i < 1000; i++ {
		sb.WriteString(`<p a="1"/>`)
	}
	sb.WriteString("</r>")
	doc, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := MustCompile("/r/p[@a]", nil)
	ctx := NewContext(doc.Root, Builtins())
	allocs := testing.AllocsPerRun(10, func() {
		if v, err := c.Eval(ctx); err != nil || len(v) != 1000 {
			t.Fatalf("got %d nodes, %v", len(v), err)
		}
	})
	t.Logf("/r/p[@a] over 1,000 p: %.0f allocations", allocs)
	if allocs > 1500 {
		t.Errorf("a predicate over 1,000 items allocated %.0f times, want at most 1500", allocs)
	}

	for src, want := range map[string]bool{
		"@id = 'x'":                                       false,
		"let $n := . return $n > 1":                       false,
		"some $x in * satisfies $x/@a":                    false,
		"map { 'k': . }?k":                                false,
		"for $i in 1 to 2 return [.]":                     false,
		"exists(function() { 1 })":                        true,
		"exists(name#0)":                                  true,
		"exists(function-lookup(xs:QName('fn:name'), 0))": true,
		"$f(.)":                   true,
		"exists(substring(?, 1))": true,
	} {
		e, err := ParseVersion("(1 to 3)["+src+"]", nil, XPath31)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		f, ok := e.(*FilterExpr)
		if !ok || len(f.Predicates) != 1 {
			t.Fatalf("%s: parsed as %T", src, e)
		}
		if got := mayCaptureFocus(f.Predicates[0]); got != want {
			t.Errorf("mayCaptureFocus(%s) = %v, want %v", src, got, want)
		}
	}
}
