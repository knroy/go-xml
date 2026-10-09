package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// An element missing two required attributes names the same one, the least
// by name, on every compile. xsl:namespace-alias requires both prefixes;
// ranging the attribute table's map named either of them.
func TestMissingRequiredAttributeNamedStably(t *testing.T) {
	const want = "xsl:namespace-alias requires a result-prefix attribute (XTSE0010)"
	for i := 0; i < 100; i++ {
		tree, err := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			<xsl:namespace-alias/>
		</xsl:stylesheet>`, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = Compile(tree.Root, CompileOptions{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("compile %d: got %v, want %q", i, err, want)
		}
	}
}
