package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xsd"
)

// TestAnnotatedSourceAtomisesToTypedValues pins that a source validated with
// xsd.ValidateOptions{Annotate: true} reaches the stylesheet typed: an element
// declared xs:decimal atomises to an xs:decimal and arithmetic on an
// xs:integer stays xs:integer, with no cast in the stylesheet. Without
// Annotate the schema only checks the document, and the same stylesheet sees
// xs:untypedAtomic. docs/validation.md once said typed values did not exist at
// all; this is the behaviour that statement contradicted.
func TestAnnotatedSourceAtomisesToTypedValues(t *testing.T) {
	const schema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="order"><xs:complexType><xs:sequence>
    <xs:element name="price" type="xs:decimal"/>
    <xs:element name="qty" type="xs:integer"/>
  </xs:sequence></xs:complexType></xs:element>
</xs:schema>`
	sh, err := Compile(mustParse(t, `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
    xmlns:xs="http://www.w3.org/2001/XMLSchema" version="3.0">
  <xsl:import-schema schema-location="s.xsd"/>
  <xsl:output method="text"/>
  <xsl:template match="/">
    <xsl:value-of select="data(order/price) instance of xs:decimal,
                          (order/qty * 2) instance of xs:integer"/>
  </xsl:template>
</xsl:stylesheet>`), CompileOptions{
		SchemaResolver: &xsd.MapResolver{ByLocation: map[string]string{"s.xsd": schema}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		annotate bool
		want     string
	}{{false, "false false"}, {true, "true true"}} {
		src := mustParse(t, `<order><price>10.50</price><qty>3</qty></order>`)
		if err := sh.Schema().Validate(src, xsd.ValidateOptions{Annotate: c.annotate}); err != nil {
			t.Fatal(err)
		}
		res, err := sh.Transform(context.Background(), src, TransformOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(res.String()); got != c.want {
			t.Errorf("Annotate=%v: typed = %q, want %q", c.annotate, got, c.want)
		}
	}
}
