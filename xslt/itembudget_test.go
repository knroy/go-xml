package xslt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// budgetRun compiles a stylesheet from its top-level declarations and runs it
// over <r/> with the given item bound (0 for the default), returning its text.
func budgetRun(t *testing.T, decls string, maxItems int) (string, error) {
	t.Helper()
	st, err := xdm.ParseString(`<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:f="urn:f">
		<xsl:output method="text"/>`+decls+`</xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compile(st.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src, _ := xdm.ParseString("<r/>", xdm.ParseOptions{})
	res, err := c.Transform(context.Background(), src.Root, TransformOptions{MaxItems: maxItems})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		t.Fatal(err)
	}
	return b.String(), nil
}

func wantItemsRefused(t *testing.T, what string, out string, err error) {
	t.Helper()
	if err == nil || !errors.Is(err, xdm.ErrResourceLimit) || !strings.Contains(err.Error(), "XPDY0130") {
		t.Fatalf("%s: out=%q err=%v, want XPDY0130 resource limit", what, out, err)
	}
}

// A nested evaluation -- a stylesheet function's body, an xsl:evaluate
// target -- must not clear the item count of the expression running around
// it. Each call reset the shared counter, so the outer loop never reached
// the bound that refuses the same loop without the call.
func TestNestedEvalKeepsEnclosingItemCount(t *testing.T) {
	const fn = `<xsl:function name="f:id"><xsl:param name="x"/><xsl:sequence select="$x"/></xsl:function>`
	for _, sel := range []string{
		`count(for $a in 1 to 40, $b in 1 to 40 return $b + 0)`,
		`count(for $a in 1 to 40, $b in 1 to 40 return f:id($b))`,
	} {
		out, err := budgetRun(t, fn+`<xsl:template match="/"><xsl:value-of select="`+sel+`"/></xsl:template>`, 1000)
		wantItemsRefused(t, sel, out, err)
	}
	out, err := budgetRun(t, `<xsl:template match="/"><xsl:value-of select="count(for $a in 1 to 40, $b in 1 to 40 return f:e($b))"/></xsl:template>
		<xsl:function name="f:e"><xsl:param name="x"/><xsl:evaluate xpath="'$x'"><xsl:with-param name="x" select="$x"/></xsl:evaluate></xsl:function>`, 1000)
	wantItemsRefused(t, "xsl:evaluate", out, err)
	// Under the default bound the same function call is an ordinary loop.
	out, err = budgetRun(t, fn+`<xsl:template match="/"><xsl:value-of select="count(for $a in 1 to 40, $b in 1 to 40 return f:id($b))"/></xsl:template>`, 0)
	if err != nil || out != "1600" {
		t.Fatalf("default limits: out=%q err=%v, want 1600", out, err)
	}
}

// A memoised function keeps every distinct call's result until the transform
// ends, so the results it holds count against the item bound together, not
// one call at a time.
func TestFunctionMemoCountsHeldItems(t *testing.T) {
	for _, attr := range []string{`new-each-time="no"`, `cache="yes"`} {
		decls := `<xsl:function name="f:r" ` + attr + `><xsl:param name="i"/><xsl:sequence select="(1 to 300) ! (. + $i)"/></xsl:function>
			<xsl:template match="/"><xsl:value-of select="sum(for $i in 1 to 10 return count(f:r($i)))"/></xsl:template>`
		out, err := budgetRun(t, decls, 1000)
		wantItemsRefused(t, attr, out, err)
		if out, err := budgetRun(t, decls, 0); err != nil || out != "3000" {
			t.Fatalf("%s default limits: out=%q err=%v, want 3000", attr, out, err)
		}
	}
}
