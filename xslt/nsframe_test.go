package xslt

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A literal result element sets its namespace bindings in one step. Adding
// them one at a time copied and re-interned the frame per binding, so an
// element declaring sixteen namespaces cost sixteen frame allocations.
func TestLiteralElementNamespaceAllocs(t *testing.T) {
	var decls strings.Builder
	for i := range 16 {
		fmt.Fprintf(&decls, ` xmlns:p%d="urn:p%d"`, i, i)
	}
	run := func(n int) float64 {
		src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:template name="main"><wrap><xsl:for-each select="1 to ` + fmt.Sprint(n) + `"><x` + decls.String() + `/></xsl:for-each></wrap></xsl:template>
</xsl:stylesheet>`
		d, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		st, err := Compile(d.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return testing.AllocsPerRun(5, func() {
			if _, err := st.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main"}); err != nil {
				t.Fatal(err)
			}
		})
	}
	got := (run(201) - run(1)) / 200
	// 11 at the time of writing; one AddNamespace per binding cost 25.
	if got > 16 {
		t.Errorf("an element declaring 16 namespaces costs %.1f allocations, want at most 16", got)
	}
}
