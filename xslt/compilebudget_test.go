package xslt

import (
	"errors"
	"fmt"
	"math"
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
	return compileWith(t, m, CompileOptions{budget: b})
}

// compileWith compiles the module m names main.xsl through m under opts.
func compileWith(t *testing.T, m memModules, opts CompileOptions) error {
	t.Helper()
	tree, err := xdm.ParseString(m["main.xsl"], xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opts.Resolver = memSheets{m}
	_, err = Compile(tree.Root, opts)
	return err
}

// includeChain is main.xsl including m1.xsl including m2.xsl ... to depth n.
// Each level's static phase and compile both walk what lies below it, so the
// chain costs about n*n/2 module visits.
func includeChain(n int) memModules {
	m := memModules{}
	for i := range n {
		m[fmt.Sprintf("m%d.xsl", i+1)] = fmt.Sprintf(
			`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0"><xsl:include href="m%d.xsl"/></xsl:stylesheet>`, i+2)
	}
	m[fmt.Sprintf("m%d.xsl", n+1)] = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0"/>`
	m["main.xsl"] = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">` +
		`<xsl:include href="m1.xsl"/><xsl:template name="xsl:initial-template"/></xsl:stylesheet>`
	return m
}

// CompileOptions.MaxModuleVisits: zero keeps the 10,000 default, a larger
// value is honoured, and a negative or over-ceiling value is read as the
// 50,000 ceiling rather than as no limit.
func TestMaxModuleVisitsOption(t *testing.T) {
	chain := includeChain(150) // about 11,000 visits
	refused := func(err error, n int) bool {
		return errors.Is(err, xdm.ErrResourceLimit) &&
			strings.Contains(err.Error(), fmt.Sprintf("more than %d xsl:include/xsl:import/xsl:use-package module visits", n))
	}
	if err := compileWith(t, chain, CompileOptions{}); !refused(err, maxModuleVisits) {
		t.Fatalf("default: got %v, want the 10000-visit limit", err)
	}
	if err := compileWith(t, chain, CompileOptions{MaxModuleVisits: 20_000}); err != nil {
		t.Fatalf("raised to 20000: %v", err)
	}
	if err := compileWith(t, includeChain(10), CompileOptions{MaxModuleVisits: 20}); !refused(err, 20) {
		t.Fatalf("lowered to 20: got %v", err)
	}
	deep := includeChain(400) // about 80,000 visits
	for _, n := range []int{-1, math.MaxInt} {
		if err := compileWith(t, deep, CompileOptions{MaxModuleVisits: n}); !refused(err, maxModuleVisitsCeiling) {
			t.Fatalf("MaxModuleVisits %d: got %v, want the %d ceiling", n, err, maxModuleVisitsCeiling)
		}
	}
}

// CompileOptions.MaxModuleNodes: zero keeps the 500,000 default, a lower value
// is honoured, and a negative one is no limit. The module is about 4,000
// nodes; 150 imports of it are 600,000, over the default.
func TestMaxModuleNodesOption(t *testing.T) {
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
	m := memModules{"L.xsl": big.String(), "main.xsl": main(150)}
	if err := compileWith(t, m, CompileOptions{}); !errors.Is(err, xdm.ErrResourceLimit) ||
		!strings.Contains(err.Error(), "more than 500000 module nodes") {
		t.Fatalf("default: got %v, want the 500000-node limit", err)
	}
	if err := compileWith(t, m, CompileOptions{MaxModuleNodes: -1}); err != nil {
		t.Fatalf("no limit: %v", err)
	}
	m["main.xsl"] = main(3)
	if err := compileWith(t, m, CompileOptions{MaxModuleNodes: 10_000}); !errors.Is(err, xdm.ErrResourceLimit) ||
		!strings.Contains(err.Error(), "more than 10000 module nodes") {
		t.Fatalf("lowered to 10000: got %v", err)
	}
}

// The compilations a static-phase fn:transform starts spend the outer
// compilation's budget under its bound: a fan-out of 20 nested compiles that
// runs at the default is refused under a MaxModuleVisits of 10.
func TestStaticTransformKeepsCompileBound(t *testing.T) {
	shallow := memModules{"main.xsl": strings.Replace(fanoutSheet, "$d lt 7", "$d lt 2", 1)}
	err := compileWith(t, shallow, CompileOptions{MaxModuleVisits: 10})
	if !errors.Is(err, xdm.ErrResourceLimit) || !strings.Contains(err.Error(), "more than 10 xsl:include") {
		t.Fatalf("got %v, want the 10-visit limit", err)
	}
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
