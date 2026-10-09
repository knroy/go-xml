package xslt

import (
	"fmt"
	"strings"
	"testing"
)

// XTSE0720 must name the same cycle on every compile. The sheet declares
// twenty independent two-set cycles; the walk follows declaration order, so
// the first cycle (a0 <-> b0) is always the one reported. Iterating the
// attribute-set map instead picks any of the forty sets, which twenty
// compiles cannot all get right by chance.
func TestAttributeSetCycleReportedInDeclarationOrder(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, `<xsl:attribute-set name="a%d" use-attribute-sets="b%d"/>`, i, i)
		fmt.Fprintf(&b, `<xsl:attribute-set name="b%d" use-attribute-sets="a%d"/>`, i, i)
	}
	b.WriteString(`<xsl:template name="xsl:initial-template"><out/></xsl:template></xsl:stylesheet>`)
	const want = "XTSE0720: xsl:attribute-set Q{}a0 is dependent on itself (Q{}a0 -> Q{}b0 -> Q{}a0)"
	for i := 0; i < 20; i++ {
		err := compileAttrSetSheet(t, b.String())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("compile %d: got %v, want %q", i, err, want)
		}
	}
}
