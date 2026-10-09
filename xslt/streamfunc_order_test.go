package xslt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// XTSE3430 for streamable functions names the least offending function by
// name on every compile. Twenty filter functions each declare a streaming
// parameter permitting several nodes; ranging the function map named any.
func TestStreamableFunctionErrorInNameOrder(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f">`)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, `<xsl:function name="f:f%d" streamability="filter"><xsl:param name="p" as="node()*"/><xsl:sequence select="$p"/></xsl:function>`, i)
	}
	b.WriteString(`<xsl:template name="xsl:initial-template"><out/></xsl:template></xsl:stylesheet>`)
	const want = "streamable stylesheet function f:f0 is declared"
	for i := 0; i < 20; i++ {
		tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = Compile(tree.Root, CompileOptions{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("compile %d: got %v, want %q", i, err, want)
		}
	}
}
