package xslt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// runtimeFanoutSheet is fanoutSheet at run time: its initial template calls
// fn:transform on its own text four times, one level deeper each time, while
// $d is below DEPTH. The static parameter $p numbers the call's path from the
// top, so every nested stylesheet is a distinct cache key and nothing is a
// cache hit; the padding
// template makes each compile charge about 1,000 module nodes.
const runtimeFanoutSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:param name="d" static="yes" as="xs:integer" select="0"/>
  <xsl:param name="p" static="yes" as="xs:integer" select="0"/>
  <xsl:template name="xsl:initial-template">
    <out>
      <xsl:if test="$d lt DEPTH">
        <xsl:for-each select="1 to 4">
          <xsl:sequence select="transform(map{
            'stylesheet-location': 'main.xsl',
            'initial-template': QName('http://www.w3.org/1999/XSL/Transform', 'initial-template'),
            'static-params': map{QName('', 'd'): $d + 1, QName('', 'p'): $p * 4 + .}})?output"/>
        </xsl:for-each>
      </xsl:if>
    </out>
  </xsl:template>
  <xsl:template name="pad">PAD</xsl:template>
</xsl:stylesheet>`

// runFanout compiles and runs runtimeFanoutSheet with the given depth.
func runFanout(t *testing.T, depth string) error {
	t.Helper()
	src := strings.Replace(runtimeFanoutSheet, "DEPTH", depth, 1)
	src = strings.Replace(src, "PAD", strings.Repeat("<p/>", 1000), 1)
	m := &memSheets{memModules{"main.xsl": src}}
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := Compile(tree.Root, CompileOptions{Resolver: m, BaseURI: "main.xsl"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sheet.Transform(context.Background(), nil, TransformOptions{Documents: m})
	return err
}

// The stylesheets fn:transform compiles at run time spend one module budget
// per transform, shared with the transforms they start, as the static phase's
// do. Each run-time call used to mint a fresh budget, so this fan-out of 1,364
// distinct compiles (about 1.4 million module nodes) was bounded by time
// alone. A shallow fan-out still runs.
func TestRuntimeTransformFanoutIsBounded(t *testing.T) {
	err := runFanout(t, "5")
	if !errors.Is(err, xdm.ErrResourceLimit) || !strings.Contains(err.Error(), "module nodes") {
		t.Fatalf("got %v, want the module-node resource limit", err)
	}
	if err := runFanout(t, "2"); err != nil {
		t.Fatalf("20 nested transforms: %v", err)
	}
}

// A cache hit compiles nothing, so it spends nothing: 600 calls on one
// stylesheet of about 1,000 nodes run, where 600 compiles would exceed
// maxModuleNodes.
func TestRuntimeTransformCacheHitIsNotCharged(t *testing.T) {
	inner := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:template name="xsl:initial-template"><in/></xsl:template>` +
		`<xsl:template name="pad">` + strings.Repeat("<p/>", 1000) + `</xsl:template></xsl:stylesheet>`
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template name="xsl:initial-template">
    <out><xsl:sequence select="count(for $k in 1 to 600 return transform(map{
      'stylesheet-location': 'inner.xsl',
      'initial-template': QName('http://www.w3.org/1999/XSL/Transform', 'initial-template')}))"/></out>
  </xsl:template>
</xsl:stylesheet>`
	m := &memSheets{memModules{"inner.xsl": inner}}
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := Compile(tree.Root, CompileOptions{Resolver: m, BaseURI: "main.xsl"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := sheet.Transform(context.Background(), nil, TransformOptions{Documents: m})
	if err != nil {
		t.Fatalf("600 cached nested transforms: %v", err)
	}
	if got := res.String(); !strings.Contains(got, "<out>600</out>") {
		t.Fatalf("got %q", got)
	}
	if n := sheet.nested.compiles; n != 1 {
		t.Fatalf("compiled %d times, want 1", n)
	}
}
