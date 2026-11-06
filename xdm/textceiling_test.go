package xdm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
	"github.com/knroy/go-xml/v2/xquery"
	"github.com/knroy/go-xml/v2/xslt"
)

// A tree whose text needs more blocks than its store addresses is refused
// with ErrResourceLimit, and no tree, by every entry point that builds one;
// it used to panic. The ceiling, 1<<17 blocks or about a gigabyte of text,
// is lowered to 2 here: each 64 KB value takes a block of its own.
func TestTextBlockCeilingIsAnError(t *testing.T) {
	long := strings.Repeat("abcdefgh", 8192)
	ss, err := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:template match="/"><out><xsl:for-each select="1 to 8">
<e><xsl:value-of select="string-join((1 to 8192) ! 'abcdefgh') || ."/></e>
</xsl:for-each></out></xsl:template></xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xslt.Compile(ss.Root, xslt.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	q, err := xquery.Compile(`<out>{for $i in 1 to 8 return <e>{string-join((1 to 8192) ! 'abcdefgh') || $i}</e>}</out>`, xquery.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx = ctx.WithVersion(xpath.XPath31).WithEnv(func(e *xpath.Env) { e.LibraryVersion = xpath.XPath31 })
	json, err := xpath.Compile(`json-to-xml('["'||$s||'1","'||$s||'2","'||$s||'3"]')`, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx = ctx.WithVar(xdm.QName{Local: "s"}, xdm.One(xdm.NewString(long)))
	defer xdm.SetTextBlocks(2)()

	check := func(name string, built bool, err error) {
		t.Helper()
		if !errors.Is(err, xdm.ErrResourceLimit) || built {
			t.Errorf("%s: got a result %v and error %v, want no result and ErrResourceLimit", name, built, err)
		}
	}
	tree, err := xdm.ParseString("<r><a>"+long+"1</a><a>"+long+"2</a><a>"+long+"3</a></r>", xdm.ParseOptions{})
	check("xdm.ParseString", tree != nil, err)
	res, err := sheet.Transform(context.Background(), src.Root, xslt.TransformOptions{})
	check("xslt Transform", res != nil, err)
	seq, err := q.Eval(nil)
	check("xquery Eval", seq != nil, err)
	seq, err = json.Eval(ctx)
	check("xpath Eval", seq != nil, err)
}
