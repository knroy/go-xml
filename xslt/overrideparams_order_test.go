package xslt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// An overriding template that drops several required parameters is told
// about the first one the original declares, on every compile. Ranging a
// map of the original's parameters named any of the twenty.
func TestOverrideMissingRequiredParamInDeclarationOrder(t *testing.T) {
	var params strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&params, `<xsl:param name="p%d" required="yes"/>`, i)
	}
	base := `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		name="urn:base" package-version="1.0.0" version="3.0">
		<xsl:template name="t" visibility="public">` + params.String() + `<x/></xsl:template>
	</xsl:package>`
	const top = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		name="urn:top" package-version="1.0.0" version="3.0">
		<xsl:use-package name="urn:base" package-version="1.0.0">
			<xsl:override><xsl:template name="t" visibility="public"><y/></xsl:template></xsl:override>
		</xsl:use-package>
		<xsl:template name="xsl:initial-template" visibility="public"><out/></xsl:template>
	</xsl:package>`
	const want = "does not declare the required parameter $p0 "
	for i := 0; i < 20; i++ {
		tree, err := xdm.ParseString(top, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = Compile(tree.Root, CompileOptions{PackageResolver: fixedPackages{"urn:base": base}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("compile %d: got %v, want %q", i, err, want)
		}
	}
}
