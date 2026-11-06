package xslt

import (
	"strings"
	"testing"
)

// The exclusion of the generated xsl:original namespace (see rewriteOverride)
// matched every URI under originalNS, so it also dropped a namespace the
// stylesheet itself declared there. 11.1.3 copies every in-scope namespace
// of a literal result element that is not excluded, and such a URI is not:
// only the URIs this compilation generated are.
func TestUserNamespaceUnderOriginalNSIsKept(t *testing.T) {
	const user = `xmlns:u="` + originalNS + `1"`
	plain, err := Compile(parseSheet(t, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	<xsl:template name="xsl:initial-template"><out `+user+`/></xsl:template>
</xsl:stylesheet>`), CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := runMain(plain, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, user) {
		t.Errorf("no package composition: got %s, want it to declare %s", got, user)
	}

	// With an override, the generated namespace is still excluded while a
	// user one beside it is kept.
	const user99 = `xmlns:u="` + originalNS + `99"`
	top := strings.Replace(overrideTop, "<over>", "<over "+user99+">", 1)
	s, err := Compile(parseSheet(t, top), CompileOptions{
		PackageResolver: fixedPackages{"urn:base": overrideBase}})
	if err != nil {
		t.Fatal(err)
	}
	got, err = runMain(s, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, user99) || strings.Contains(got, "xmlns:xsl") {
		t.Errorf("override: got %s, want %s and no generated xsl binding", got, user99)
	}
}
