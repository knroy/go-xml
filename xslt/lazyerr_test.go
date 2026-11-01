package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

const lazyErrSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
  xmlns:f="urn:f" xmlns:xs="http://www.w3.org/2001/XMLSchema">
<xsl:function name="f:g" as="xs:integer"><xsl:param name="a" as="xs:integer"/><xsl:variable name="v" as="xs:integer" select="$a"/><xsl:sequence select="$v"/></xsl:function>
<xsl:template name="main"><xsl:sequence select="CALLS"/></xsl:template>
</xsl:stylesheet>`

func lazyErrRun(t *testing.T, calls string) (*Stylesheet, func() error) {
	t.Helper()
	d, err := xdm.ParseString(strings.ReplaceAll(lazyErrSheet, "CALLS", calls), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := Compile(d.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return st, func() error {
		_, err := st.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main"})
		return err
	}
}

// The description a failed conversion reports ("parameter $a of f:g") is
// built only when the conversion fails; building it on every call cost
// three strings per call of a typed function with a typed variable.
func TestTypeConversionDescribesLazily(t *testing.T) {
	allocs := func(calls string) float64 {
		_, run := lazyErrRun(t, calls)
		return testing.AllocsPerRun(5, func() {
			if err := run(); err != nil {
				t.Fatal(err)
			}
		})
	}
	got := (allocs("sum(for $i in 1 to 201 return f:g($i))") - allocs("sum(for $i in 1 to 1 return f:g($i))")) / 200
	// 26.8 at the time of writing; building the three descriptions eagerly
	// made it 29.8.
	if got > 28.3 {
		t.Errorf("a typed function call costs %.1f allocations, want at most 28.3", got)
	}
	// The message itself is unchanged.
	_, run := lazyErrRun(t, "f:g('x')")
	if err := run(); err == nil || !strings.Contains(err.Error(), "parameter $a of f:g") {
		t.Errorf("err = %v, want it to name parameter $a of f:g", err)
	}
}
