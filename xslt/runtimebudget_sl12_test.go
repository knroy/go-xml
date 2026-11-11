package xslt

import (
	"context"
	"errors"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestSequenceConstructorItemsChargedPerInstruction: the item bound on an
// as-typed variable holds whichever instruction delivered the items, not only
// xsl:sequence, xsl:copy-of and xsl:copy. A for-each of 200 x 1000 items,
// each turn wrapped in xsl:try, a named template declared as="item()*",
// xsl:perform-sort or xsl:evaluate, is refused under MaxItems 100000; the
// same at 20 turns passes.
func TestSequenceConstructorItemsChargedPerInstruction(t *testing.T) {
	for _, wrap := range []struct{ name, open, close, decl string }{
		{"try", `<xsl:try>`, `<xsl:catch/></xsl:try>`, ``},
		{"as-template", `<xsl:call-template name="t">`, `</xsl:call-template>`,
			`<xsl:template name="t" as="item()*"><xsl:sequence select="1 to 1000"/></xsl:template>`},
		{"perform-sort", `<xsl:perform-sort><xsl:sort select="."/>`, `</xsl:perform-sort>`, ``},
		{"evaluate", `<xsl:evaluate xpath="'1 to 1000'">`, `</xsl:evaluate>`, ``},
	} {
		inner := `<xsl:sequence select="1 to 1000"/>`
		switch wrap.name {
		case "as-template", "evaluate":
			inner = ``
		}
		for _, c := range []struct {
			turns  string
			refuse bool
		}{{"200", true}, {"20", false}} {
			sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` + wrap.decl +
				`<xsl:template name="main"><xsl:variable name="v" as="item()*"><xsl:for-each select="1 to ` + c.turns + `">` +
				wrap.open + inner + wrap.close +
				`</xsl:for-each></xsl:variable><out><xsl:value-of select="count($v)"/></out></xsl:template></xsl:stylesheet>`
			tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			s, err := Compile(tree.Root, CompileOptions{})
			if err != nil {
				t.Fatalf("%s: %v", wrap.name, err)
			}
			_, err = s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", MaxItems: 100000})
			if got := errors.Is(err, xdm.ErrResourceLimit); got != c.refuse {
				t.Errorf("%s x %s: err = %v, want refused %v", wrap.name, c.turns, err, c.refuse)
			}
		}
	}
}
