package xquery_test

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xquery"
)

// doctypeSchema is hatsSchema behind an internal-subset DOCTYPE whose entity
// supplies a facet, so a load that succeeds has also expanded it.
const doctypeSchema = `<!DOCTYPE xs:schema [<!ENTITY max "12">]>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="http://example.org/hats">
  <xs:simpleType name="hatsize">
    <xs:restriction base="xs:integer"><xs:maxInclusive value="&max;"/></xs:restriction>
  </xs:simpleType>
</xs:schema>`

// includingSchema reaches doctypeSchema only through xs:include, which is
// xsd.Load's own parse rather than this package's.
const includingSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="http://example.org/hats">
  <xs:include schemaLocation="dt.xsd"/>
</xs:schema>`

type docResolver map[string]string

func (r docResolver) Resolve(ns, loc, base string) (io.ReadCloser, string, error) {
	src, ok := r[loc]
	if !ok {
		return nil, "", fmt.Errorf("no %q", loc)
	}
	return io.NopCloser(strings.NewReader(src)), loc, nil
}

// TestSchemaParseOptionsReachEverySchemaParse: a schema carrying a DOCTYPE is
// refused by default and loads with SchemaParseOptions.AllowDOCTYPE, on every
// path a schema document reaches the parser by -- the Schemas store's Source,
// the resolver, an xs:include inside a resolved schema, a library module's
// import, and fn:load-xquery-module.
func TestSchemaParseOptionsReachEverySchemaParse(t *testing.T) {
	importQ := fmt.Sprintf(`import schema namespace h = %q at "dt.xsd"; 12 cast as h:hatsize`, hatsNS)
	lib := xquery.Module{Namespace: "urn:pol", Source: fmt.Sprintf(`
module namespace p = "urn:pol";
import schema namespace h = %q at "dt.xsd";
declare function p:f() { 12 cast as h:hatsize };`, hatsNS)}
	for _, c := range []struct {
		name, query string
		opts        xquery.Options
	}{
		{"store Source", fmt.Sprintf(`import schema namespace h = %q; 12 cast as h:hatsize`, hatsNS),
			xquery.Options{Schemas: []xquery.Schema{{Namespace: hatsNS, Source: doctypeSchema}}}},
		{"resolver", importQ,
			xquery.Options{SchemaResolver: docResolver{"dt.xsd": doctypeSchema}}},
		{"xs:include", fmt.Sprintf(`import schema namespace h = %q at "top.xsd"; 12 cast as h:hatsize`, hatsNS),
			xquery.Options{SchemaResolver: docResolver{"top.xsd": includingSchema, "dt.xsd": doctypeSchema}}},
		{"import module", `import module namespace p = "urn:pol"; p:f()`,
			xquery.Options{Modules: []xquery.Module{lib}, SchemaResolver: docResolver{"dt.xsd": doctypeSchema}}},
		{"load-xquery-module", `load-xquery-module("urn:pol")("functions")(QName("urn:pol", "f"))(0)()`,
			xquery.Options{Modules: []xquery.Module{lib}, SchemaResolver: docResolver{"dt.xsd": doctypeSchema}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := run(t, c.query, c.opts); err == nil || !strings.Contains(err.Error(), "DOCTYPE") {
				t.Errorf("default options: err = %v, want a DOCTYPE refusal", err)
			}
			c.opts.SchemaParseOptions.AllowDOCTYPE = true
			if got, err := run(t, c.query, c.opts); err != nil || got != "12" {
				t.Errorf("AllowDOCTYPE: got %q, %v; want 12", got, err)
			}
		})
	}
}
