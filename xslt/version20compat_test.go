package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// compileAtVersion compiles the version="2.0" stylesheet src, written to
// dir/main.xsl beside any modules it imports, at the given MaxVersion.
func compileAtVersion(t *testing.T, dir, src string, maxVersion float64) (*Stylesheet, error) {
	t.Helper()
	path := filepath.Join(dir, "main.xsl")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	stree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: path})
	if err != nil {
		t.Fatal(err)
	}
	return Compile(stree.Root, CompileOptions{
		Resolver: r, BaseURI: path, MaxVersion: maxVersion})
}

// Section 3.9.2: a 3.0 processor treats a version="2.0" module exactly as a
// version="3.0" one, so the 3.0 pattern root() (SchXslt-compiled Peppol
// validators) and a late xsl:import (XRechnung's xrechnung-html.xsl) both
// compile. A 2.0 processor still refuses each, with XTSE0340 and XTSE0200.
func TestVersion20ModuleUnder30Processor(t *testing.T) {
	cases := []struct {
		name, src, code string
	}{
		{"root-pattern", `<xsl:stylesheet version="2.0"
			xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			<xsl:template match="root()"><out/></xsl:template>
		</xsl:stylesheet>`, "XTSE0340"},
		{"late-import", `<xsl:stylesheet version="2.0"
			xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			<xsl:output method="xml"/>
			<xsl:import href="lib.xsl"/>
			<xsl:template match="/"><out/></xsl:template>
		</xsl:stylesheet>`, "XTSE0200"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			lib := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"/>`
			if err := os.WriteFile(filepath.Join(dir, "lib.xsl"), []byte(lib), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := compileAtVersion(t, dir, tc.src, 0)
			if err != nil {
				t.Fatalf("3.0 processor: %v", err)
			}
			dtree, _ := xdm.ParseString(`<d/>`, xdm.ParseOptions{})
			res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got := res.String(); !strings.HasSuffix(got, "<out/>") {
				t.Errorf("got %q, want <out/>", got)
			}
			if _, err := compileAtVersion(t, dir, tc.src, 2.0); err == nil ||
				!strings.Contains(err.Error(), tc.code) {
				t.Errorf("2.0 processor: got %v, want %s", err, tc.code)
			}
		})
	}
}

// Section 5.5's grammar, which the 3.0 processor now applies to 2.0 modules
// too: "/ union /*" is the path /union/* by XPath's leading-lone-slash rule
// (match-038), and copy-of() is no OuterFunctionName (match-077).
func TestPattern30GrammarEdges(t *testing.T) {
	src := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:template match="/ union /*" priority="5"><wrong/></xsl:template>
		<xsl:template match="/"><out/></xsl:template>
	</xsl:stylesheet>`
	s, err := compileAtVersion(t, t.TempDir(), src, 0)
	if err != nil {
		t.Fatal(err)
	}
	dtree, _ := xdm.ParseString(`<d/>`, xdm.ParseOptions{})
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.String(); !strings.HasSuffix(got, "<out/>") {
		t.Errorf("got %q: \"/ union /*\" was read as a union", got)
	}

	src = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:template match="copy-of($x)//a"/>
		<xsl:variable name="x"><a/></xsl:variable>
	</xsl:stylesheet>`
	if _, err := compileAtVersion(t, t.TempDir(), src, 0); err == nil ||
		!strings.Contains(err.Error(), "XTSE0340") {
		t.Errorf("copy-of($x)//a: got %v, want XTSE0340", err)
	}
}
