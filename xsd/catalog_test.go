package xsd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/knroy/go-xml/v2/xdm"
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

// One entry must report one name however it was found.
//
// The assembler keys the documents it has read on the location the resolver
// returned, so an entry answering with the caller's spelling makes two
// documents of one file the moment a schema set reaches it two ways. That is
// not hypothetical: the XSLT 3.0 schema imports the xml: namespace with no
// location, and the schema for schemas it also imports names
// http://www.w3.org/2001/xml.xsd for the same document. Before this, loading
// it reported "duplicate attribute declaration space" and three more like it.
func TestCatalogReportsOneNamePerEntry(t *testing.T) {
	r := NewCatalogResolver()
	r.Add(NSXML, []byte("<x/>"),
		"http://www.w3.org/2001/xml.xsd", "xml.xsd")

	var names []string
	for _, look := range []struct{ ns, loc string }{
		{"", "http://www.w3.org/2001/xml.xsd"},
		{"", "xml.xsd"},
		{NSXML, ""},
	} {
		rc, name, err := r.Resolve(look.ns, look.loc, "")
		if err != nil || rc == nil {
			t.Fatalf("resolve(%q, %q): %v", look.ns, look.loc, err)
		}
		rc.Close()
		names = append(names, name)
	}
	for _, n := range names[1:] {
		if n != names[0] {
			t.Errorf("one entry reported several names: %q", names)
			break
		}
	}
	if names[0] != "http://www.w3.org/2001/xml.xsd" {
		t.Errorf("canonical name is %q, want the absolute alias", names[0])
	}
}

// With a fallback, a location the catalog matches only by spelling is read
// from the fallback when it can be. Both shapes were shadowed before: a schema
// set's own xml.xsd (DocBook slides declares xml:space1 in one) answered by the
// bundled copy because of its file name, and an explicit local schemaLocation
// for the xml: namespace (msData/additional/test264908_1.xsd) answered by the
// namespace entry.
func TestCatalogFallbackReadsLocalCopies(t *testing.T) {
	const stub = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	targetNamespace="http://www.w3.org/XML/1998/namespace">
	<xs:attribute name="lang" type="xs:string"/></xs:schema>`
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	local := func(attr string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	targetNamespace="http://www.w3.org/XML/1998/namespace">
	<xs:attribute name="` + attr + `" type="xs:string"/></xs:schema>`
	}
	importer := func(loc, attr string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	xmlns:xml="http://www.w3.org/XML/1998/namespace">
	<xs:import namespace="http://www.w3.org/XML/1998/namespace" schemaLocation="` + loc + `"/>
	<xs:element name="e"><xs:complexType>
	<xs:attribute ref="xml:` + attr + `"/></xs:complexType></xs:element></xs:schema>`
	}
	write("xml.xsd", local("space1"))
	write("mine.xsd", local("blah"))

	catalog := func() *CatalogResolver {
		r := NewCatalogResolver()
		r.Add(NSXML, []byte(stub), "http://www.w3.org/2001/xml.xsd", "xml.xsd")
		r.SetFallback(&FileResolver{})
		return r
	}
	for _, c := range []struct{ file, loc, attr string }{
		{"own-xml.xsd", "xml.xsd", "space1"},
		{"explicit.xsd", "mine.xsd", "blah"},
	} {
		p := write(c.file, importer(c.loc, c.attr))
		if _, err := LoadFile(p, Options{Resolver: catalog()}); err != nil {
			t.Errorf("%s: the local %s was shadowed: %v", c.file, c.loc, err)
		}
	}

	// The bundled copy still answers a W3C spelling and a namespace-only
	// import whatever the fallback holds, and anything the fallback cannot
	// read.
	everything := catalog()
	everything.SetFallback(resolverFunc(func(ns, loc, base string) (io.ReadCloser, string, error) {
		return io.NopCloser(strings.NewReader("<from-fallback/>")), loc, nil
	}))
	base := filepath.Join(dir, "own-xml.xsd")
	for _, look := range []struct {
		r       *CatalogResolver
		ns, loc string
	}{
		{everything, "", "http://www.w3.org/2009/01/xml.xsd"},
		{everything, NSXML, ""},
		{catalog(), NSXML, "missing.xsd"},
	} {
		rc, _, err := look.r.Resolve(look.ns, look.loc, base)
		if err != nil || rc == nil {
			t.Fatalf("resolve(%q, %q): %v", look.ns, look.loc, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != stub {
			t.Errorf("resolve(%q, %q) did not answer the bundled copy", look.ns, look.loc)
		}
	}
}
