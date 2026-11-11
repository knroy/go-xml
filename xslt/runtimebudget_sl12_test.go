package xslt

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// TestCopyChargesInheritedBindings: a copied element carries every binding
// it inherited, so xsl:copy-of, xsl:sequence and fn:copy-of of each of 300
// children of a root declaring 300 prefixes write 90,000 bindings, which the
// byte budget is charged for: refused under 1 MiB, and passed with
// copy-namespaces="no" or a bound that holds them.
func TestCopyChargesInheritedBindings(t *testing.T) {
	var doc strings.Builder
	doc.WriteString(`<r`)
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&doc, ` xmlns:p%d="u%d"`, i, i)
	}
	doc.WriteString(`>` + strings.Repeat(`<e/>`, 300) + `</r>`)
	src, err := xdm.ParseString(doc.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		body     string
		maxBytes int64
		refuse   bool
	}{
		{`<out><xsl:copy-of select="/*/*"/></out>`, 1 << 20, true},
		{`<out><xsl:sequence select="/*/*"/></out>`, 1 << 20, true},
		{`<out><xsl:sequence select="copy-of(/*/*)"/></out>`, 1 << 20, true},
		{`<out><xsl:copy-of select="/*/*" copy-namespaces="no"/></out>`, 1 << 20, false},
		{`<out><xsl:copy-of select="/*/*"/></out>`, 64 << 20, false},
	} {
		sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template match="/">` + c.body + `</xsl:template></xsl:stylesheet>`
		tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(tree.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Transform(context.Background(), src.Root, TransformOptions{MaxBytes: c.maxBytes})
		if got := errors.Is(err, xdm.ErrResourceLimit); got != c.refuse {
			t.Errorf("%s under %d: err = %v, want refused %v", c.body, c.maxBytes, err, c.refuse)
		}
	}
}

// TestScopeBindingsShadowing: scopeBindings keeps the innermost binding of
// each prefix, the last of one element's declarations for it, drops an
// undeclaration and xml, and sorts by prefix.
func TestScopeBindingsShadowing(t *testing.T) {
	tree, err := xdm.ParseString(`<a xmlns:z="z1" xmlns:b="b1" xmlns:c="c1" xmlns="d1"><m xmlns:b="b2" xmlns=""><n xmlns:c="c3"/></m></a>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	n := deepCopy(tree.Root.FirstChild()).FirstChild().FirstChild()
	n.AddNamespace("c", "c4") // a constructed duplicate: the last one wins
	got := fmt.Sprint(scopeBindings(n, nil))
	if want := "[{b b2} {c c4} {z z1}]"; got != want {
		t.Errorf("scopeBindings = %s, want %s", got, want)
	}
}
