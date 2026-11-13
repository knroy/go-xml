package xslt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// A stylesheet compiled from its xsl:stylesheet element, rather than from the
// document node above it, streams exactly as the document would: a host such
// as an XML-DSig ds:Transform holds the stylesheet as one element of a larger
// tree and has no document node of its own to give Compile. Each case is
// compiled three ways -- from the document node, from the element under it,
// and from the element inside a signature whose other ds:Transform holds a
// module that includes another -- and must give the document node's output
// on the streamed path it takes.

const elemSigHead = `<ds:Signature xmlns:ds="http://www.w3.org/2000/09/xmldsig#"><ds:SignedInfo><ds:Reference><ds:Transforms>` +
	`<ds:Transform Algorithm="http://www.w3.org/TR/1999/REC-xslt-19991116">` +
	`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:include href="other.xsl"/></xsl:stylesheet>` +
	`</ds:Transform><ds:Transform Algorithm="http://www.w3.org/TR/1999/REC-xslt-19991116">`

const elemSigTail = `</ds:Transform></ds:Transforms></ds:Reference></ds:SignedInfo></ds:Signature>`

// elemRoots returns the three compile roots of sheet, parsed at base.
func elemRoots(t *testing.T, sheet, base string) map[string]*xdm.Node {
	t.Helper()
	parse := func(s string) *xdm.Node {
		tr, err := xdm.ParseString(s, xdm.ParseOptions{BaseURI: base})
		if err != nil {
			t.Fatal(err)
		}
		return tr.Root
	}
	var nested *xdm.Node
	walkElements(parse(elemSigHead+sheet+elemSigTail), func(el *xdm.Node) bool {
		if isModuleElement(el) && el.Attr("", "id") != nil {
			nested = el
		}
		return nested == nil
	})
	if nested == nil {
		t.Fatal("no stylesheet with an id in the signature")
	}
	return map[string]*xdm.Node{
		"document": parse(sheet),
		"element":  firstElement(parse(sheet)),
		"nested":   nested,
	}
}

func TestStreamElementCompiledMain(t *testing.T) {
	const sheet = `<xsl:stylesheet id="s" version="3.0" exclude-result-prefixes="#all" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:mode streamable="yes" on-no-match="shallow-copy"/>
</xsl:stylesheet>`
	const in = `<r><x>1</x><x>2</x></r>`
	for name, root := range elemRoots(t, sheet, "") {
		s, err := Compile(root, CompileOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		before := streamtrace.Snapshot()
		res, err := s.TransformReader(context.Background(), strings.NewReader(in), TransformOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := res.String(); !strings.Contains(got, in) {
			t.Errorf("%s: output %q", name, got)
		}
		if streamtrace.Snapshot().Sub(before).Runs[streamtrace.Main] == 0 {
			t.Errorf("%s: the principal input was not streamed", name)
		}
	}
}

func TestStreamElementCompiledSourceDocument(t *testing.T) {
	const sheet = `<xsl:stylesheet id="s" version="3.0" exclude-result-prefixes="#all" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:mode streamable="yes"/>
<xsl:template match="rec"><i k="{@k}"><xsl:value-of select="."/></i></xsl:template>
<xsl:template name="main"><out><xsl:source-document streamable="yes" href="doc.xml"><xsl:apply-templates select="doc/rec"/></xsl:source-document></out></xsl:template>
</xsl:stylesheet>`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(`<doc><rec k="a">1</rec><rec k="b">2</rec></doc>`), 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := NewFileResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	const want = `<out><i k="a">1</i><i k="b">2</i></out>`
	for name, root := range elemRoots(t, sheet, fileURIOf(filepath.Join(dir, "s.xsl"))) {
		s, err := Compile(root, CompileOptions{Resolver: docs})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		before := streamtrace.Snapshot()
		res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := res.String(); !strings.Contains(got, want) {
			t.Errorf("%s: output %q, want %q", name, got, want)
		}
		if st := streamtrace.Snapshot().Sub(before); st.Runs[streamtrace.Core] == 0 || st.Reruns != 0 {
			t.Errorf("%s: xsl:source-document did not stream on the core (runs %d, re-runs %d)", name, st.Runs[streamtrace.Core], st.Reruns)
		}
	}
}
