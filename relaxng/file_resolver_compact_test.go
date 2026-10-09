package relaxng

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestFileResolverReadsCompactIncludes pins that FileResolver reads a fetched
// schema in whichever syntax it is written in. A compact schema's
// `include "common.rnc"` reaches the resolver as an <include href>, and the
// resolver used to parse every fetch as XML, so a modular .rnc schema could
// not be compiled with it at all. An XML schema including a .rnc, and a .rnc
// starting with a byte order mark and a comment, take the same path.
func TestFileResolverReadsCompactIncludes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("common.rnc", "\uFEFF# shared\ncard = element card { attribute name { text } }\n")
	main := write("main.rnc", "include \"common.rnc\"\nstart = element book { card* }\n")
	xmlMain := write("main.rng", `<grammar xmlns="http://relaxng.org/ns/structure/1.0">`+
		`<include href="common.rnc"/>`+
		`<start><element name="book"><zeroOrMore><ref name="card"/></zeroOrMore></element></start>`+
		`</grammar>`)

	r := &FileResolver{Root: dir}
	ok, _ := xdm.ParseString(`<book><card name="a"/></book>`, xdm.ParseOptions{})
	bad, _ := xdm.ParseString(`<book><card/></book>`, xdm.ParseOptions{})
	for _, p := range []string{main, xmlMain} {
		doc, err := r.ResolveSchema(p)
		if err != nil {
			t.Fatalf("ResolveSchema(%s): %v", filepath.Base(p), err)
		}
		s, err := CompileWithOptions(doc, Options{Resolver: r, BaseURI: p})
		if err != nil {
			t.Fatalf("compiling %s: %v", filepath.Base(p), err)
		}
		if err := s.Validate(ok.Root); err != nil {
			t.Errorf("%s: valid document refused: %v", filepath.Base(p), err)
		}
		if s.Validate(bad.Root) == nil {
			t.Errorf("%s: a card without its name was accepted; the included "+
				"compact definition is not what was compiled", filepath.Base(p))
		}
	}
}
