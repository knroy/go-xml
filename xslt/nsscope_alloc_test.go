package xslt

import (
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// inheritNamespaces and rootNamespaces ran once per copied or serialized
// element and each built InScopeNamespaces' map plus a set of the element's
// own prefixes (about 10 allocations). They now walk the ancestors' frames.
func TestNamespaceScopeWalkDoesNotBuildMaps(t *testing.T) {
	doc, err := xdm.ParseString(`<a xmlns:p="urn:p" xmlns:q="urn:q" xmlns="urn:d">`+
		`<b xmlns:r="urn:r"><c/></b></a>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := doc.Root.FirstChild().FirstChild().FirstChild()
	if c.Name().Local != "c" {
		t.Fatalf("picked %s", c.Name().Local)
	}
	rn := rootNamespaces(c)
	if len(rn) != 4 || rn[0].prefix != "" || rn[1].prefix != "p" ||
		rn[2].prefix != "q" || rn[3].prefix != "r" {
		t.Errorf("rootNamespaces = %v, want prefixes \"\", p, q, r", rn)
	}
	if n := testing.AllocsPerRun(20, func() { _ = rootNamespaces(c) }); n > 2 {
		t.Errorf("rootNamespaces made %.0f allocations, want <= 2", n)
	}
	cp := xdm.Copy(c)
	inheritNamespaces(cp, c)
	if cp.NumNamespaceDecls() != 4 {
		t.Errorf("copy has %d declarations, want 4", cp.NumNamespaceDecls())
	}
}
