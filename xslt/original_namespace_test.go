package xslt

import (
	"strings"
	"testing"
)

// An override calling xsl:original is compiled with its xsl prefix rebound to
// a generated namespace (see rewriteOverride). That binding is an
// implementation artefact standing for the XSLT namespace, which 11.1.3 never
// copies to the result, so a literal result element in the override must not
// carry it: the result is the one the same templates give with no package
// composition at all.
func TestOriginalNamespaceStaysOutOfTheResult(t *testing.T) {
	s, err := Compile(parseSheet(t, overrideTop), CompileOptions{
		PackageResolver: fixedPackages{"urn:base": overrideBase}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := runMain(s, "")
	if err != nil {
		t.Fatal(err)
	}
	flat, err := Compile(parseSheet(t, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	<xsl:template name="xsl:initial-template"><out><over><base/></over></out></xsl:template>
</xsl:stylesheet>`), CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := runMain(flat, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "go-xml.invalid") || got != want {
		t.Errorf("override result\n%s\nwant\n%s", got, want)
	}
}
