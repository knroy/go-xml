package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A copy is recorded against its original only when some package declares an
// accumulator that could read it back.
//
// §18.2.10 gives a node copied by fn:copy-of, fn:snapshot or xsl:copy-of with
// copy-accumulators="yes" its original's accumulator values, and the runtime
// keeps a copy-to-original table to answer them. The table holds both trees
// until the transform ends, so recording into it when no accumulator exists
// kept every copy-of(.) of a streamed record alive: 830 MB of peak heap for a
// 10 MB input. With an accumulator declared -- here only in a used package,
// which files it in the same table as the top-level package -- the copy must
// still answer as its original: x[2] counts 2 there, while its copy, walked
// as a tree of its own, would count 1.
func TestCopyOriginOnlyWithAccumulators(t *testing.T) {
	plain := compileString(t, `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template match="/"/></xsl:stylesheet>`)
	rt, err := newRuntime(plain, context.Background(), nil, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := xdm.ParseString(`<r><x/><x/><x/></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		noteCopy(rt, doc.Root, copyItem(doc.Root))
		noteCopy(rt, doc.Root, snapshotItem(doc.Root))
	}
	if n := len(rt.accumOrigin); n != 0 {
		t.Errorf("no accumulator is declared, yet %d copied nodes are held "+
			"against their originals", n)
	}

	const base = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema"
		xmlns:p="urn:base" name="urn:base" package-version="1.0.0" version="3.0">
		<xsl:accumulator name="c" initial-value="0" as="xs:integer">
			<xsl:accumulator-rule match="x" select="$value + 1"/>
		</xsl:accumulator>
		<xsl:function name="p:after" as="xs:integer" visibility="public">
			<xsl:param name="n" as="node()"/>
			<xsl:sequence select="$n ! accumulator-after('c')"/>
		</xsl:function>
	</xsl:package>`
	const top = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:p="urn:base" name="urn:top" package-version="1.0.0" expand-text="yes" version="3.0"
		exclude-result-prefixes="#all">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:use-package name="urn:base" package-version="1.0.0"/>
		<xsl:template name="xsl:initial-template" visibility="public">
			<xsl:variable name="d" select="parse-xml('&lt;r>&lt;x/>&lt;x/>&lt;x/>&lt;/r>')"/>
			<res>{p:after(copy-of($d//x[2]))}</res>
		</xsl:template>
	</xsl:package>`
	tree, err := xdm.ParseString(top, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{PackageResolver: fixedPackages{"urn:base": base}})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := s.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "initial-template", InitialTemplateURI: xdm.NSXSL})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := res.String(); !strings.Contains(got, "<res>2</res>") {
		t.Errorf("got %q, want <res>2</res>: a copy answers accumulator-after "+
			"with its original's value", got)
	}
}
