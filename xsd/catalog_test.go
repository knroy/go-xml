package xsd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/knroy/go-xml/xdm"
)

// The catalog exists because one schema is named several ways. Each of these
// is a spelling the XSLT 3.0 schema or the suite's copies actually use.
func TestCatalogResolvesEverySpelling(t *testing.T) {
	const src = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`
	r := NewCatalogResolver()
	r.Add(NSSchema, []byte(src),
		"http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd",
		"XMLSchema.xsd")

	cases := []struct {
		name, namespace, location, base string
	}{
		{"absolute alias", "", "http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd", ""},
		{"relative alias", "", "XMLSchema.xsd", ""},
		{"namespace only", NSSchema, "", ""},
		// A relative reference is resolved against the referring
		// document's base before it reaches a resolver, so what arrives
		// is an absolute URI nobody registered.
		{"relative, already resolved against base", "",
			"file:///schemas/XMLSchema.xsd", "file:///schemas/x.xsd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rc, _, err := r.Resolve(c.namespace, c.location, c.base)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if rc == nil {
				t.Fatal("no entry found")
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			if string(b) != src {
				t.Errorf("got %q", b)
			}
		})
	}
}

// A miss must be an error rather than a silent nil, or a schema that names
// something the catalog does not have loads with the reference unresolved and
// fails much later.
func TestCatalogMissIsAnError(t *testing.T) {
	r := NewCatalogResolver()
	_, _, err := r.Resolve("", "http://example.invalid/x.xsd", "")
	if err == nil {
		t.Fatal("a miss with no fallback should be an error")
	}
	if !strings.Contains(err.Error(), "x.xsd") {
		t.Errorf("the error should name what was missing: %v", err)
	}
}

func TestCatalogFallback(t *testing.T) {
	r := NewCatalogResolver()
	r.SetFallback(resolverFunc(func(ns, loc, base string) (io.ReadCloser, string, error) {
		return io.NopCloser(strings.NewReader("<from-fallback/>")), loc, nil
	}))
	rc, _, err := r.Resolve("", "anything.xsd", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "<from-fallback/>" {
		t.Errorf("got %q", b)
	}
}

// A later Add of the same alias wins, so a caller can override a bundled entry
// with its own copy.
func TestCatalogAddOverrides(t *testing.T) {
	r := NewCatalogResolver()
	r.Add("", []byte("first"), "a.xsd")
	r.Add("", []byte("second"), "a.xsd")
	rc, _, err := r.Resolve("", "a.xsd", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "second" {
		t.Errorf("got %q, want the later registration", b)
	}
}

func TestCatalogAddFromFS(t *testing.T) {
	fsys := fstest.MapFS{
		"XMLSchema.xsd": {Data: []byte("<schema-for-schemas/>")},
		"xml.xsd":       {Data: []byte("<xml-namespace/>")},
	}
	r := NewCatalogResolver()
	if err := r.AddFromFS(fsys, W3CEntries()); err != nil {
		t.Fatalf("AddFromFS: %v", err)
	}
	// Registered under the namespace, so an xs:import with no
	// schemaLocation finds it.
	rc, _, err := r.Resolve(NSXML, "", "")
	if err != nil || rc == nil {
		t.Fatalf("xml namespace not resolved: %v", err)
	}
	rc.Close()
	rc, _, err = r.Resolve("", "http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd", "")
	if err != nil || rc == nil {
		t.Fatalf("schema for schemas not resolved: %v", err)
	}
	rc.Close()
}

// A missing file is an error naming it: a catalog quietly smaller than the
// caller asked for fails later and somewhere less obvious.
func TestCatalogAddFromFSReportsMissing(t *testing.T) {
	r := NewCatalogResolver()
	err := r.AddFromFS(fstest.MapFS{}, W3CEntries())
	if err == nil {
		t.Fatal("a missing catalog file should be an error")
	}
	if !strings.Contains(err.Error(), "XMLSchema.xsd") {
		t.Errorf("the error should name the file: %v", err)
	}
}

type resolverFunc func(ns, loc, base string) (io.ReadCloser, string, error)

func (f resolverFunc) Resolve(ns, loc, base string) (io.ReadCloser, string, error) {
	return f(ns, loc, base)
}

// The case this was built for: the W3C schema for XSLT 3.0, with the remote
// import it is published with, loaded without touching the network.
//
// The suite ships a copy whose schemaLocation was rewritten to a relative path
// in 2021 "because of W3C web site throttling" -- so this restores the
// published form and resolves it through the catalog instead.
func TestCatalogLoadsSchemaForXSLT30(t *testing.T) {
	dir := os.Getenv("GOXSLT_XSLTS")
	if dir == "" {
		t.Skip("set GOXSLT_XSLTS to a checkout of w3c/xslt30-test")
	}
	base := filepath.Join(dir, "tests", "misc", "catalog")
	sheet, err := os.ReadFile(filepath.Join(base, "schema-for-xslt30.xsd"))
	if err != nil {
		t.Skipf("schema-for-xslt30.xsd not available: %v", err)
	}
	xmlSchema, err := os.ReadFile(filepath.Join(base, "XMLSchema.xsd"))
	if err != nil {
		t.Skipf("XMLSchema.xsd not available: %v", err)
	}

	// Put the published remote location back.
	published := strings.Replace(string(sheet),
		`schemaLocation="XMLSchema.xsd"`,
		`schemaLocation="http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd"`, 1)
	if published == string(sheet) {
		t.Fatal("the local copy no longer has the relative import this rewrites")
	}

	r := NewCatalogResolver()
	r.Add(NSSchema, xmlSchema,
		"http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd", "XMLSchema.xsd")
	// No fallback: a fetch would be an error, so a pass proves nothing was
	// fetched.

	tree, err := xdm.Parse(strings.NewReader(published),
		xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s, err := xsdLoadForTest(tree.Root, r)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Elements) < 100 {
		t.Errorf("only %d global element declarations", len(s.Elements))
	}
}

func xsdLoadForTest(root *xdm.Node, r Resolver) (*Schema, error) {
	return Load(root, "", Options{
		Version:      Version11,
		Resolver:     r,
		ParseOptions: xdm.ParseOptions{AllowDOCTYPE: true},
	})
}
