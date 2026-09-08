package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// stripProbeDocs resolves one URI to one parsed document.
type stripProbeDocs struct{ uri, src string }

func (d stripProbeDocs) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	if !strings.HasSuffix(uri, d.uri) {
		return nil, xdm.Errorf("FODC0002", "no such document %q", uri)
	}
	return xdm.ParseString(d.src, xdm.ParseOptions{})
}

// TestSourceDocumentStripsInputTypeAnnotations covers the interaction of
// xsl:source-document validation="strict" with input-type-annotations="strip".
//
// Section 3.5 scopes the stripping to "the source trees ... affected by
// xsl:strip-space", and section 4.4 lists among those "any document read using
// xsl:stream" — the instruction now called xsl:source-document. So a document
// this instruction validates must still have its annotations stripped, and
// data() over it yields xs:untypedAtomic rather than the schema type.
//
// Stripping was applied only to the principal input, in Transform. A document
// loaded by xsl:source-document kept the annotations validation gave it, so
// "data(.) instance of xs:decimal" answered true where the suite's
// streamable-147 requires false.
func TestSourceDocumentStripsInputTypeAnnotations(t *testing.T) {
	const schema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	    elementFormDefault="qualified">
	  <xs:element name="r">
	    <xs:complexType><xs:sequence>
	      <xs:element name="amount" type="xs:decimal"/>
	    </xs:sequence></xs:complexType>
	  </xs:element>
	</xs:schema>`
	const doc = `<r><amount>12.5</amount></r>`

	// The same stylesheet twice, differing only in the attribute under test:
	// "strip" must hide the schema type, "preserve" must show it. Running both
	// is what proves the assertion reads the annotation rather than something
	// that was never there.
	run := func(t *testing.T, annotations string) string {
		t.Helper()
		src := `<xsl:stylesheet version="3.0"
		    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    xmlns:xs="http://www.w3.org/2001/XMLSchema"
		    input-type-annotations="` + annotations + `">
		  <xsl:import-schema schema-location="r.xsd"/>
		  <xsl:template name="main">
		    <xsl:source-document href="r.xml" validation="strict">
		      <out d="{data(/r/amount) instance of xs:decimal}"/>
		    </xsl:source-document>
		  </xsl:template>
		</xsl:stylesheet>`
		sheet, err := Compile(mustParse(t, src), CompileOptions{
			SchemaResolver: &xsd.MapResolver{
				ByLocation: map[string]string{"r.xsd": schema}},
		})
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		res, err := sheet.Transform(context.Background(), nil, TransformOptions{
			InitialTemplate: "main",
			Documents:       stripProbeDocs{uri: "r.xml", src: doc},
		})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		return res.String()
	}

	t.Run("strip", func(t *testing.T) {
		if got := run(t, "strip"); !strings.Contains(got, `d="false"`) {
			t.Errorf("output = %s, want d=\"false\": the annotations "+
				"validation applied were not stripped", got)
		}
	})
	// preserve is the control. Without it a fix that simply never annotated
	// the document would pass the strip case for the wrong reason.
	t.Run("preserve", func(t *testing.T) {
		if got := run(t, "preserve"); !strings.Contains(got, `d="true"`) {
			t.Errorf("output = %s, want d=\"true\": validation did not "+
				"annotate the document at all", got)
		}
	})
}
