package xslt

import (
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// dtSchema declares urn:dt's element behind an internal-subset DOCTYPE whose
// entity supplies its type, so a load that succeeds has also expanded it.
const dtSchema = `<!DOCTYPE xs:schema [<!ENTITY t "xs:integer">]>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:dt">
  <xs:element name="e" type="&t;"/>
</xs:schema>`

// dtIncluding reaches dtSchema only through xs:include, which is xsd.Load's
// own parse rather than importschema.go's.
const dtIncluding = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:dt">
  <xs:include schemaLocation="dt.xsd"/>
</xs:schema>`

type dtResolver map[string]string

func (r dtResolver) Resolve(ns, loc, base string) (io.ReadCloser, string, error) {
	if loc == "" {
		loc = ns
	}
	if src, ok := r[loc]; ok {
		return io.NopCloser(strings.NewReader(src)), loc, nil
	}
	return nil, "", nil
}

// TestSchemaParseOptionsReachEveryImportSchemaParse: a schema carrying a
// DOCTYPE is refused by default and loads with SchemaParseOptions.AllowDOCTYPE
// on every path xsl:import-schema reads a document by -- a schema-location,
// an xs:include inside it, and the namespace alone (with and without one). The last one's failure is
// silent by §3.15, so it is observed as the declaration being absent.
func TestSchemaParseOptionsReachEveryImportSchemaParse(t *testing.T) {
	res := dtResolver{"dt.xsd": dtSchema, "top.xsd": dtIncluding, "urn:dt": dtSchema}
	nsInc := dtResolver{"dt.xsd": dtSchema, "urn:dt": dtIncluding}
	for _, c := range []struct {
		name, attrs string
		res         dtResolver
	}{
		{"schema-location", `namespace="urn:dt" schema-location="dt.xsd"`, res},
		{"xs:include", `namespace="urn:dt" schema-location="top.xsd"`, res},
		{"namespace only", `namespace="urn:dt"`, res},
		{"namespace only, xs:include", `namespace="urn:dt"`, nsInc},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:import-schema ` + c.attrs + `/>
</xsl:stylesheet>`
			stree, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			declared := func(s *Stylesheet) bool {
				return s.Schema() != nil && s.Schema().Elements[xdm.QName{URI: "urn:dt", Local: "e"}] != nil
			}
			sheet, err := Compile(stree.Root, CompileOptions{SchemaResolver: c.res})
			if strings.HasPrefix(c.name, "namespace only") {
				if err != nil || declared(sheet) {
					t.Errorf("default options: err = %v, declared = %v; want the DOCTYPE schema skipped", err, err == nil && declared(sheet))
				}
			} else if err == nil || !strings.Contains(err.Error(), "DOCTYPE") {
				t.Errorf("default options: err = %v, want a DOCTYPE refusal", err)
			}
			sheet, err = Compile(stree.Root, CompileOptions{SchemaResolver: c.res,
				SchemaParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}})
			if err != nil || !declared(sheet) {
				t.Errorf("AllowDOCTYPE: err = %v; want {urn:dt}e declared", err)
			}
		})
	}
}

// TestNamespaceOnlyImportSchemaReadsAsXSD11: the namespace-only path reads a
// schema under the same version as a schema-location does -- XSD 1.1 for an
// XSLT 3.0 processor -- rather than xsd.Load's 1.0 default.
func TestNamespaceOnlyImportSchemaReadsAsXSD11(t *testing.T) {
	const plain = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:dt">
  <xs:element name="e" type="xs:integer"/>
</xs:schema>`
	stree, err := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:import-schema namespace="urn:dt"/>
</xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := Compile(stree.Root, CompileOptions{SchemaResolver: dtResolver{"urn:dt": plain}})
	if err != nil {
		t.Fatal(err)
	}
	if v := sheet.Schema().Version; v != xsd.Version11 {
		t.Errorf("schema version = %v, want XSD 1.1", v)
	}
}
