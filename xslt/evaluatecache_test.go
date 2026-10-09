package xslt

import (
	"fmt"
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// xsl:evaluate compiles a target expression once per instruction and source
// text, against the element's own resolver only: a @namespace-context or
// @schema-aware answer is a different resolver, and a different static
// context, so it is compiled afresh.
func TestEvaluateCompileCache(t *testing.T) {
	own := &nsResolver{bindings: map[string]string{}, xpathVersion: xpath.XPath31}
	i := &evaluateInstr{ns: own}
	a, err := i.compile("1 + 1", own)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := i.compile("1 + 1", own); b != a {
		t.Fatal("the same text against the same resolver compiled twice")
	}
	other := *own
	if c, _ := i.compile("1 + 1", &other); c == a {
		t.Fatal("another resolver was answered from the cache")
	}
	if _, err := i.compile("1 +", own); err == nil {
		t.Fatal("a malformed expression compiled")
	}
	if _, ok := i.compiled["1 +"]; ok {
		t.Fatal("a failed compile was cached")
	}
	for n := 0; n < 2*maxEvaluateCache; n++ {
		if _, err := i.compile(fmt.Sprint(n), own); err != nil {
			t.Fatal(err)
		}
	}
	if len(i.compiled) > maxEvaluateCache {
		t.Fatalf("cache holds %d entries, bound is %d", len(i.compiled), maxEvaluateCache)
	}
}
