package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A call that hits the new-each-time="no" memo builds its key in a reused
// buffer and looks it up without allocating: building the key used to cost a
// Clark name, a growing builder and an fmt boxing per argument on every call.
func TestFunctionMemoHitAllocs(t *testing.T) {
	run := func(calls string) float64 {
		src := strings.ReplaceAll(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
  xmlns:f="urn:f" xmlns:xs="http://www.w3.org/2001/XMLSchema">
<xsl:function name="f:id" new-each-time="no" as="xs:integer"><xsl:param name="a" as="xs:integer"/><xsl:param name="b" as="xs:string"/><xsl:sequence select="$a"/></xsl:function>
<xsl:template name="main"><xsl:variable name="v" select="CALLS"/><xsl:sequence select="count($v)"/></xsl:template>
</xsl:stylesheet>`, "CALLS", calls)
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
	got := (run("for $i in 1 to 201 return f:id(7, 'seven')") - run("for $i in 1 to 1 return f:id(7, 'seven')")) / 200
	// 13 at the time of writing, most of it evaluating the call's
	// arguments; the key itself cost 6 more.
	if got > 16 {
		t.Errorf("a memo hit costs %.1f allocations, want at most 16", got)
	}
}
