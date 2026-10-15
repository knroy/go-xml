package xslt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// Of several xsl:override children that clash with the using package's own
// declarations, the first written is reported, with its own code, on every
// compile. Template t0 comes first and is XTSE3055; the ten functions after
// it would be XTSE0770, so ranging the override map flipped the code.
func TestOverrideClashReportedInDocumentOrder(t *testing.T) {
	var baseDecls, overrides, own strings.Builder
	decl := func(b *strings.Builder, kind string, i int) {
		if kind == "t" {
			fmt.Fprintf(b, `<xsl:template name="t%d" visibility="public"><x/></xsl:template>`, i)
		} else {
			fmt.Fprintf(b, `<xsl:function name="f:f%d" visibility="public"><x/></xsl:function>`, i)
		}
	}
	for i := 0; i < 10; i++ {
		decl(&baseDecls, "t", i)
		decl(&baseDecls, "f", i)
	}
	decl(&overrides, "t", 0)
	for i := 0; i < 10; i++ {
		decl(&overrides, "f", i)
	}
	for i := 1; i < 10; i++ {
		decl(&overrides, "t", i)
	}
	for i := 0; i < 10; i++ {
		decl(&own, "t", i)
		decl(&own, "f", i)
	}
	base := `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f"
		name="urn:base" package-version="1.0.0" version="3.0">` + baseDecls.String() + `</xsl:package>`
	top := `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f"
		name="urn:top" package-version="1.0.0" version="3.0">
		<xsl:use-package name="urn:base" package-version="1.0.0">
			<xsl:override>` + overrides.String() + `</xsl:override>
		</xsl:use-package>` + own.String() + `
		<xsl:template name="xsl:initial-template" visibility="public"><out/></xsl:template>
	</xsl:package>`
	for i := 0; i < 20; i++ {
		tree, err := xdm.ParseString(top, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = Compile(tree.Root, CompileOptions{PackageResolver: fixedPackages{"urn:base": base}})
		if err == nil || !strings.HasPrefix(err.Error(), "XTSE3055: ") || !strings.Contains(err.Error(), "t0 ") {
			t.Fatalf("compile %d: got %v, want XTSE3055 for t0", i, err)
		}
	}
}
