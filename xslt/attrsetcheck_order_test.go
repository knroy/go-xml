package xslt

import (
	"fmt"
	"strings"
	"testing"
)

// The XTSE0020 and XTSE0730 attribute-set checks must report the first
// offending set in declaration order on every compile. Each sheet has twenty
// offending sets, so iterating the attribute-set map instead names a0/s0 on
// all twenty compiles only by chance.
func TestAttributeSetChecksReportInDeclarationOrder(t *testing.T) {
	for _, tc := range []struct {
		name, set, want string
	}{
		{
			name: "XTSE0020 visibility",
			set: `<xsl:attribute-set name="a%[1]d" visibility="public"/>` +
				`<xsl:attribute-set name="a%[1]d" visibility="private"/>`,
			want: `XTSE0020: the declarations of xsl:attribute-set "a0" give it`,
		},
		{
			name: "XTSE0730",
			set: `<xsl:attribute-set name="s%[1]d" streamable="yes" use-attribute-sets="n%[1]d"/>` +
				`<xsl:attribute-set name="n%[1]d"/>`,
			want: `XTSE0730: xsl:attribute-set "s0" specifies streamable="yes" but uses "n0"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			b.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`)
			for i := 0; i < 20; i++ {
				fmt.Fprintf(&b, tc.set, i)
			}
			b.WriteString(`<xsl:template name="xsl:initial-template"><out/></xsl:template></xsl:stylesheet>`)
			for i := 0; i < 20; i++ {
				err := compileAttrSetSheet(t, b.String())
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("compile %d: got %v, want %q", i, err, tc.want)
				}
			}
		})
	}
}
