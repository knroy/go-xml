package xslt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// ruleIndexSize sums every list the index retains, the shared #all index once.
func ruleIndexSize(s *Stylesheet) int {
	n := 0
	count := func(mr *modeRules) {
		for _, l := range mr.byKind {
			n += len(l)
		}
		for _, l := range mr.byName {
			n += len(l)
		}
	}
	var all *modeRules
	for _, mr := range s.rules {
		count(mr)
		all = mr.all
	}
	if all != nil {
		count(all)
	}
	return n
}

func compileSMB1(t *testing.T, body string) *Stylesheet {
	t.Helper()
	tree, err := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
		body+`</xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestSMB1RuleIndexLinear checks the rule index stays linear in the
// stylesheet: M modes, N named #all rules and W wildcard #all rules used to
// retain about M*N*W ints, and one rule naming many modes and many names
// retained their product.
func TestSMB1RuleIndexLinear(t *testing.T) {
	const k = 40
	var b strings.Builder
	modes := make([]string, k)
	names := make([]string, k)
	for i := range k {
		modes[i] = fmt.Sprintf("m%d", i)
		names[i] = fmt.Sprintf("e%d", i)
	}
	fmt.Fprintf(&b, `<xsl:template match="z" mode="%s"/>`, strings.Join(modes, " "))
	fmt.Fprintf(&b, `<xsl:template match="%s" mode="%s"/>`,
		strings.Join(names, "|"), strings.Join(modes, " "))
	for i := range k {
		fmt.Fprintf(&b, `<xsl:template match="*" mode="#all" priority="-1"><xsl:sequence select="%d"/></xsl:template>`, i)
		fmt.Fprintf(&b, `<xsl:template match="e%d" mode="#all"/>`, i)
	}
	s := compileSMB1(t, b.String())
	// z in k modes and 2k #all rules stored once; the fan-out rule's k
	// modes fall back to the linear scan. The old index held over k*k*k.
	if got := ruleIndexSize(s); got > 4*k {
		t.Fatalf("rule index retains %d entries for k=%d, want at most %d", got, k, 4*k)
	}
	// Dispatch still finds the right rule through the merged lists.
	doc, _ := xdm.ParseString(`<e7/>`, xdm.ParseOptions{})
	el := doc.Root.FirstChild()
	tm, err := s.findTemplate(el, "m3", nil)
	if err != nil || tm == nil || tm.Match.src != "e7" {
		t.Fatalf("m3 on e7: got %v, %v; want the e7 rule", tm, err)
	}
}

// TestSMB1RuleIndexDispatchAllocs checks candidate merging allocates nothing.
func TestSMB1RuleIndexDispatchAllocs(t *testing.T) {
	s := compileSMB1(t, `<xsl:template match="a | *" mode="m"/><xsl:template match="a" mode="#all"/>`+
		`<xsl:template match="node()" mode="#all"/>`)
	doc, _ := xdm.ParseString(`<a/>`, xdm.ParseOptions{})
	el := doc.Root.FirstChild()
	seen := 0
	allocs := testing.AllocsPerRun(100, func() {
		c, _ := s.candidates(el, "m")
		c.from(1)
		seen = 0
		for _, more := c.next(); more; _, more = c.next() {
			seen++
		}
	})
	if allocs != 0 {
		t.Errorf("candidate dispatch allocates %v times, want 0", allocs)
	}
	if seen != 3 {
		t.Errorf("got %d candidates from index 1, want 3", seen)
	}
}

func BenchmarkSMB1Candidates(b *testing.B) {
	tree, _ := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
		`<xsl:template match="a | *" mode="m"/><xsl:template match="a" mode="#all"/>`+
		`<xsl:template match="node()" mode="#all"/></xsl:stylesheet>`, xdm.ParseOptions{})
	s, _ := Compile(tree.Root, CompileOptions{})
	doc, _ := xdm.ParseString(`<a/>`, xdm.ParseOptions{})
	el := doc.Root.FirstChild()
	b.ReportAllocs()
	for b.Loop() {
		c, _ := s.candidates(el, "m")
		for _, more := c.next(); more; _, more = c.next() {
		}
	}
}
