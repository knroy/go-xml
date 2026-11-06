package xslt

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// memSheets is memModules that also answers fn:transform's
// stylesheet-location, which reads through a DocumentResolver.
type memSheets struct{ memModules }

func (m memSheets) ResolveDocument(href, base string) (*xdm.Tree, error) {
	return xdm.ParseString(m.memModules[href], xdm.ParseOptions{})
}

// compileFrom compiles the module m names main.xsl through m, spending b, or
// a fresh budget when b is nil.
func compileFrom(t *testing.T, m memModules, b *compileBudget) error {
	t.Helper()
	tree, err := xdm.ParseString(m["main.xsl"], xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(tree.Root, CompileOptions{Resolver: memSheets{m}, budget: b})
	return err
}

// A module imported many times is compiled in full each time, since each
// occurrence has its own precedence. maxModuleVisits counted occurrences
// only, so 300 imports of one module of about 4,000 nodes (some 1.2 million
// nodes of compile work, well under the 10,000-visit bound) compiled. The
// work is now charged by size, against maxModuleNodes.
func TestRepeatedImportIsChargedBySize(t *testing.T) {
	var big strings.Builder
	big.WriteString(`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">`)
	for i := range 1000 {
		fmt.Fprintf(&big, `<xsl:template name="e%d"><e a="x"/></xsl:template>`, i)
	}
	big.WriteString(`</xsl:stylesheet>`)
	main := func(n int) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">` +
			strings.Repeat(`<xsl:import href="L.xsl"/>`, n) +
			`<xsl:template name="xsl:initial-template"/></xsl:stylesheet>`
	}
	m := memModules{"L.xsl": big.String(), "main.xsl": main(300)}
	err := compileFrom(t, m, nil)
	if !errors.Is(err, xdm.ErrResourceLimit) || !strings.Contains(err.Error(), "module nodes") {
		t.Fatalf("got %v, want the module-node resource limit", err)
	}
	// The same module imported a few times still compiles.
	m["main.xsl"] = main(3)
	if err := compileFrom(t, m, nil); err != nil {
		t.Fatalf("three imports: %v", err)
	}
}

// fanoutSheet calls fn:transform on itself four times from a static
// variable, one level deeper each time, while $d is below 7: some 21,844
// nested compilations from one small stylesheet. Each nested compilation used
// to count its module visits afresh, so nothing bounded the breadth.
const fanoutSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:param name="d" static="yes" as="xs:integer" select="0"/>
  <xsl:variable name="r" static="yes" use-when="$d lt 7" select="for $i in 1 to 4 return transform(map{
    'stylesheet-location': 'main.xsl',
    'initial-template': QName('http://www.w3.org/1999/XSL/Transform', 'initial-template'),
    'static-params': map{QName('', 'd'): $d + 1}})"/>
  <xsl:template name="xsl:initial-template"><out/></xsl:template>
</xsl:stylesheet>`

// The nested compilations a static-phase fn:transform starts spend the outer
// compilation's module budget, so the fan-out is refused once the outer
// compilation's maxModuleVisits is spent, and a shallow one still compiles.
func TestStaticTransformFanoutIsBounded(t *testing.T) {
	err := compileFrom(t, memModules{"main.xsl": fanoutSheet}, nil)
	if !errors.Is(err, xdm.ErrResourceLimit) || !strings.Contains(err.Error(), "module visits") {
		t.Fatalf("got %v, want the module-visit resource limit", err)
	}
	shallow := strings.Replace(fanoutSheet, "$d lt 7", "$d lt 2", 1)
	if err := compileFrom(t, memModules{"main.xsl": shallow}, nil); err != nil {
		t.Fatalf("20 nested compilations: %v", err)
	}
}
