package xslt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// TestTransformFromPlainXPathKeepsCallerBytes: a bare XPath caller's byte
// budget is unheld (per expression), and the nested transform's runtime holds
// its own; arming that hold must not erase what the calling expression has
// already charged. 700,000 bytes per step under a 1 MiB bound is refused at
// the second step, with or without the fn:transform beside it.
func TestTransformFromPlainXPathKeepsCallerBytes(t *testing.T) {
	const tr = `transform(map{'stylesheet-text': '<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0"><xsl:template name="xsl:initial-template"><r/></xsl:template></xsl:stylesheet>'})`
	for _, step := range []string{`'y'`, tr} {
		ctx := plainContext(nil)
		xpathleaf.SetLimits(ctx, xpathleaf.Limits{Bytes: 1 << 20})
		_, err := xpath.Eval(`count((1 to 3) ! (string-join((1 to 70000) ! '0123456789'), `+step+`))`, ctx, nil)
		if !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("step %.20s: err = %v, want the byte budget's refusal", step, err)
		}
	}
}

// TestSequenceConstructorItemsCharged: the items a sequence constructor
// gathers into an as-typed variable are held against MaxItems, however few
// each xsl:sequence select produced: a chain of sibling variables doubling by
// two xsl:sequence of the one before, and a for-each of xsl:sequence, are
// refused where the comma form of the same chain already was.
func TestSequenceConstructorItemsCharged(t *testing.T) {
	var chain strings.Builder
	chain.WriteString(`<xsl:variable name="v0" select="1"/>`)
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&chain, `<xsl:variable name="v%d" as="item()*"><xsl:sequence select="$v%d"/><xsl:sequence select="$v%d"/></xsl:variable>`, i, i-1, i-1)
	}
	for _, c := range []struct {
		body   string
		refuse bool
	}{
		{chain.String() + `<out><xsl:value-of select="count($v20)"/></out>`, true},
		{`<xsl:variable name="v" as="item()*"><xsl:for-each select="1 to 200"><xsl:sequence select="1 to 1000"/></xsl:for-each></xsl:variable><out><xsl:value-of select="count($v)"/></out>`, true},
		{`<xsl:variable name="v" as="item()*"><xsl:for-each select="1 to 20"><xsl:sequence select="1 to 1000"/></xsl:for-each></xsl:variable><out><xsl:value-of select="count($v)"/></out>`, false},
	} {
		sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template name="main">` + c.body + `</xsl:template></xsl:stylesheet>`
		tree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(tree.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", MaxItems: 100000})
		if got := errors.Is(err, xdm.ErrResourceLimit); got != c.refuse {
			t.Errorf("%.60s: err = %v, want refused %v", c.body, err, c.refuse)
		}
	}
}

// TestStreamXMLToJSONChargesBytes: the streamed xml-to-json charges the text
// it writes to MaxBytes, as the tree's does: indentation writes 2 x depth
// bytes per member, so nested arrays 300 deep, about 5 KB of input, write
// some 180 KB indented. Under a 64 KiB bound that is refused (written out,
// held whole, and for a path), and the unindented text passes.
func TestStreamXMLToJSONChargesBytes(t *testing.T) {
	dir := t.TempDir()
	doc := strings.Repeat(`<array xmlns="http://www.w3.org/2005/xpath-functions">`, 300) + strings.Repeat(`</array>`, 300)
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		body   string
		refuse bool
	}{
		{`<j><xsl:value-of select="xml-to-json(., map{'indent': true()})"/></j>`, true},
		{`<j><xsl:value-of select="string-length(xml-to-json(., map{'indent': true()}))"/></j>`, true},
		{`<j><xsl:value-of select="string-length(xml-to-json(/*, map{'indent': true()}))"/></j>`, true},
		{`<j><xsl:value-of select="string-length(xml-to-json(.))"/></j>`, false},
	} {
		so, se, st := coreRun(coreSheetWith("", c.body), dir, true, TransformOptions{MaxBytes: 64 << 10})
		if st.Runs[0] == 0 {
			t.Errorf("%s: not streamed", c.body)
		}
		// A refusal on the core is re-run on the tree, which raises its own
		// (xpath's xml-to-json); either way the error is the budget's.
		if got := strings.Contains(se, "XPDY0130"); got != c.refuse {
			t.Errorf("%s: %q %q; want refused %v", c.body, so, se, c.refuse)
		}
	}
}
