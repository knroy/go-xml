package xslt

import (
	"context"
	"errors"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

func iterateSheet(t *testing.T, body string) *Stylesheet {
	t.Helper()
	st, err := xdm.ParseString(`<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output method="text"/>
		<xsl:template match="/">`+body+`</xsl:template></xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compile(st.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A value handed on by xsl:next-iteration is held for every later turn, so
// one larger than the item bound is refused rather than carried.
func TestIterateNextIterationParamIsCharged(t *testing.T) {
	c := iterateSheet(t, `<xsl:iterate select="1 to 20">
		<xsl:param name="s" select="(1, 2)"/>
		<xsl:on-completion><xsl:value-of select="count($s)"/></xsl:on-completion>
		<xsl:next-iteration><xsl:with-param name="s" select="($s, $s)"/></xsl:next-iteration>
		</xsl:iterate>`)
	src, _ := xdm.ParseString("<r/>", xdm.ParseOptions{})
	_, err := c.Transform(context.Background(), src.Root, TransformOptions{MaxItems: 1000})
	if err == nil || !errors.Is(err, xdm.ErrResourceLimit) ||
		!strings.Contains(err.Error(), "xsl:next-iteration") {
		t.Fatalf("err = %v, want a resource limit naming xsl:next-iteration", err)
	}
}

// The parameters are carried in slices indexed by declaration order, whatever
// order xsl:with-param names them in, and a turn costs no map.
func TestIterateParamsCarryInDeclarationOrder(t *testing.T) {
	c := iterateSheet(t, `<xsl:iterate select="1 to 100000">
		<xsl:param name="a" select="0"/><xsl:param name="b" select="0"/>
		<xsl:on-completion><xsl:value-of select="$a, $b"/></xsl:on-completion>
		<xsl:next-iteration>
		  <xsl:with-param name="b" select="$b + 2"/>
		  <xsl:with-param name="a" select="$a + 1"/>
		</xsl:next-iteration></xsl:iterate>`)
	src, _ := xdm.ParseString("<r/>", xdm.ParseOptions{})
	var m0, m1 goruntime.MemStats
	goruntime.ReadMemStats(&m0)
	res, err := c.Transform(context.Background(), src.Root, TransformOptions{})
	goruntime.ReadMemStats(&m1)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		t.Fatal(err)
	}
	if b.String() != "100000 200000" {
		t.Fatalf("got %q", b.String())
	}
	// 800 bytes a turn with slices; 1119 when each turn built a map.
	if per := (m1.TotalAlloc - m0.TotalAlloc) / 100000; per > 950 {
		t.Fatalf("a turn allocated %d bytes", per)
	}
}

// A parameter passed back unchanged is not converted again: its value was
// converted when it was carried in, and converting 20 000 strings on each of
// 20 000 turns took 16 s. A changed value still is, and still fails.
func TestIterateUnchangedParamIsNotReconverted(t *testing.T) {
	loop := func(with string) *Stylesheet {
		return iterateSheet(t, `<xsl:iterate select="1 to 1000">
		<xsl:param name="p" as="xs:string*" select="(1 to 20000) ! string(.)" xmlns:xs="http://www.w3.org/2001/XMLSchema"/>
		<xsl:on-completion><xsl:value-of select="count($p)"/></xsl:on-completion>
		<xsl:next-iteration><xsl:with-param name="p" select="`+with+`"/></xsl:next-iteration></xsl:iterate>`)
	}
	src, _ := xdm.ParseString("<r/>", xdm.ParseOptions{})
	var m0, m1 goruntime.MemStats
	goruntime.ReadMemStats(&m0)
	res, err := loop("$p").Transform(context.Background(), src.Root, TransformOptions{})
	goruntime.ReadMemStats(&m1)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.String(); got != "20000" {
		t.Fatalf("got %q", got)
	}
	// About 320 KB a turn when each turn converted the sequence again.
	if per := (m1.TotalAlloc - m0.TotalAlloc) / 1000; per > 32<<10 {
		t.Fatalf("a turn allocated %d bytes", per)
	}
	_, err = loop("$p, 1").Transform(context.Background(), src.Root, TransformOptions{})
	if err == nil || !strings.Contains(err.Error(), "XTTE0590") {
		t.Fatalf("err = %v, want XTTE0590", err)
	}
}
