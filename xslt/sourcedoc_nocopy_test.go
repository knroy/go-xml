package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xsd"
)

// cachedDocs hands out one parsed tree for every request, as a caching
// resolver does.
type cachedDocs struct{ tree *xdm.Tree }

func (d cachedDocs) ResolveDocument(uri, base string) (*xdm.Tree, error) { return d.tree, nil }

// TestStrictSourceDocumentLeavesCachedTree: xsl:source-document and
// xsl:merge validate the retrieved document without first copying it when
// the validation copies anyway. The typed result must still be the copy the
// pre-copy gave -- no document URI, not the node fn:doc returns -- and the
// cached tree must come out untyped.
func TestStrictSourceDocumentLeavesCachedTree(t *testing.T) {
	const schema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="r"><xs:complexType><xs:sequence>
	    <xs:element name="amount" type="xs:decimal"/>
	  </xs:sequence></xs:complexType></xs:element>
	</xs:schema>`
	tree, err := xdm.ParseString(`<?xml version="1.1"?><r> <amount>12.5</amount> </r>`,
		xdm.ParseOptions{BaseURI: "http://ex/r.xml", DocumentURI: "http://ex/r.xml"})
	if err != nil {
		t.Fatal(err)
	}
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:import-schema schema-location="r.xsd"/>
	  <xsl:template name="main">
	    <xsl:source-document href="http://ex/r.xml" validation="strict">
	      <out uri="[{document-uri(/)}]" same="{. is doc('http://ex/r.xml')}"
	           typed="{data(/r/amount) instance of xs:decimal}" kids="{count(/r/node())}"/>
	    </xsl:source-document>
	    <xsl:for-each select="doc('http://ex/r.xml')">
	      <cached typed="{data(/r/amount) instance of xs:decimal}"/>
	    </xsl:for-each>
	  </xsl:template>
	</xsl:stylesheet>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{
		SchemaResolver: &xsd.MapResolver{ByLocation: map[string]string{"r.xsd": schema}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "main",
		Documents:       cachedDocs{tree},
	})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	got := res.String()
	for _, want := range []string{`uri="[]"`, `same="false"`, `typed="true"`, `kids="3"`,
		`typed="false"/>`} {
		if !strings.Contains(got, want) {
			t.Errorf("output %s lacks %s", got, want)
		}
	}
	if tree.XMLVersion != "1.1" || tree.Root.DocumentURI() != "http://ex/r.xml" {
		t.Errorf("cached tree changed: version %q, document URI %q", tree.XMLVersion, tree.Root.DocumentURI())
	}
}
