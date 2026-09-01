package w3cschemas

import (
	"os"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// Every bundled document must load as a schema in its own right. A file that
// was truncated or replaced by an error page would otherwise be found only by
// whatever imports it, and much later.
func TestBundledSchemasLoad(t *testing.T) {
	for _, e := range xsd.W3CEntries() {
		t.Run(e.Path, func(t *testing.T) {
			b, err := os.ReadFile("schemas/" + e.Path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			tree, err := xdm.Parse(strings.NewReader(string(b)),
				xdm.ParseOptions{AllowDOCTYPE: true})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			r, err := Catalog()
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
			// The schema has no target-namespace field to read, so
			// check the components landed where they should: every
			// global name a document defines carries its namespace.
			if e.Namespace == "" {
				return
			}
			var seen bool
			for n := range s.Attributes {
				if n.URI == e.Namespace {
					seen = true
					break
				}
			}
			for n := range s.Types {
				if seen {
					break
				}
				if n.URI == e.Namespace {
					seen = true
				}
			}
			if !seen {
				t.Errorf("no global component in namespace %q", e.Namespace)
			}
		})
	}
}

// The aliases are the point of the module: each spelling must find the file.
func TestCatalogAnswersEverySpelling(t *testing.T) {
	r, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range xsd.W3CEntries() {
		for _, alias := range e.Aliases {
			rc, _, err := r.Resolve("", alias, "")
			if err != nil || rc == nil {
				t.Errorf("alias %q did not resolve: %v", alias, err)
				continue
			}
			rc.Close()
		}
		if e.Namespace != "" {
			rc, _, err := r.Resolve(e.Namespace, "", "")
			if err != nil || rc == nil {
				t.Errorf("namespace %q did not resolve: %v", e.Namespace, err)
				continue
			}
			rc.Close()
		}
	}
}

// Nothing outside the bundle is answered, so a schema naming something else
// fails here rather than reaching the network.
func TestCatalogRefusesWhatItDoesNotHave(t *testing.T) {
	r, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Resolve("", "http://example.invalid/other.xsd", ""); err == nil {
		t.Error("an unbundled location should be an error, not a fetch")
	}
}
