package w3cschemas_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/w3cschemas"
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// The case the module exists for: the W3C schema for XSLT 3.0 imports the XSD
// 1.1 schema for schemas by absolute URL, and that URL cannot be relied on.
// Here it is loaded with no network access at all -- no fallback resolver is
// set, so a fetch would be an error and a pass proves none happened.
func TestLoadsSchemaForXSLT30WithoutTheNetwork(t *testing.T) {
	suite := os.Getenv("GOXSLT_XSLTS")
	if suite == "" {
		t.Skip("set GOXSLT_XSLTS to a checkout of w3c/xslt30-test")
	}
	path := filepath.Join(suite, "tests", "misc", "catalog", "schema-for-xslt30.xsd")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("schema-for-xslt30.xsd not available: %v", err)
	}
	// Put back the published import. The suite's copy was rewritten to a
	// relative path in 2021 because of W3C web site throttling.
	published := strings.Replace(string(src),
		`schemaLocation="XMLSchema.xsd"`,
		`schemaLocation="http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd"`, 1)

	tree, err := xdm.Parse(strings.NewReader(published),
		xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	r, err := w3cschemas.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	s, err := xsd.Load(tree.Root, "", xsd.Options{
		Version:      xsd.Version11,
		Resolver:     r,
		ParseOptions: xdm.ParseOptions{AllowDOCTYPE: true},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(s.Elements) < 100 {
		t.Fatalf("only %d global element declarations", len(s.Elements))
	}
	fmt.Printf("loaded %d global element declarations\n", len(s.Elements))
}
