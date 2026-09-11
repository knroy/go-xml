package xslt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xslt"
)

// The limit a runaway stylesheet actually reaches must be classifiable.
//
// A caller has only errors.Is to decide whether a failure means "your input
// is bad" or "I declined to spend more", and for a while the commonest
// refusal in this package was the one that answered neither: template
// recursion returned a bare string while fn:transform's nesting refusal, in
// the same package and for the same kind of bound, wrapped the sentinel.
func TestTemplateRecursionCarriesTheResourceSentinel(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:template name="go"><xsl:call-template name="go"/></xsl:template>` +
		`</xsl:stylesheet>`
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := xslt.Compile(doc.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := xdm.ParseString("<d/>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, terr := st.Transform(context.Background(), src.Root,
		xslt.TransformOptions{InitialTemplate: "go", MaxDepth: 20})
	if terr == nil {
		t.Fatal("unbounded template recursion returned no error")
	}
	if !errors.Is(terr, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell a refusal to spend from a fault in its input", terr)
	}
}
