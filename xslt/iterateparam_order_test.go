package xslt

import (
	"fmt"
	"strings"
	"testing"
)

// When xsl:next-iteration supplies several values that fail conversion to
// their xsl:param types, XTTE0590 names the first declared parameter on
// every run. Twenty xs:integer parameters all get a string; ranging the
// supplied map named any of them.
func TestIterateParamConversionErrorInDeclarationOrder(t *testing.T) {
	var params, with strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&params, `<xsl:param name="p%d" as="xs:integer" select="0"/>`, i)
		fmt.Fprintf(&with, `<xsl:with-param name="p%d" select="'x'"/>`, i)
	}
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema">
		<xsl:template name="xsl:initial-template">
			<xsl:iterate select="1 to 2">` + params.String() + `
				<xsl:next-iteration>` + with.String() + `</xsl:next-iteration>
			</xsl:iterate>
		</xsl:template>
	</xsl:stylesheet>`
	const want = "parameter $p0 of xsl:iterate"
	for i := 0; i < 20; i++ {
		err := compileAttrSetSheet(t, src)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "XTTE0590") {
			t.Fatalf("run %d: got %v, want XTTE0590 naming %q", i, err, want)
		}
	}
}
