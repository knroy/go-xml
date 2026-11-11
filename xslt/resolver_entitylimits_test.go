package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A FileResolver's entity limits govern every parse a transform makes on its
// behalf, not only the files it reads: fn:parse-xml and the stylesheet-text
// of a nested fn:transform parse with them too. A knob that only some paths
// honour is a bug -- the stylesheet simply moves its document to a path that
// ignores it.

// twoEntities declares two general entities and uses both, so a MaxEntities
// of 1 leaves the second undeclared.
const twoEntities = `<!DOCTYPE r [<!ENTITY a "x"><!ENTITY b "y">]><r>&a;&b;</r>`

// entityResolver is a FileResolver admitting a DOCTYPE, with MaxEntities n.
func entityResolver(t *testing.T, n int) *FileResolver {
	t.Helper()
	r, err := NewFileResolver(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.AllowDOCTYPE = true
	r.MaxEntities = n
	return r
}

// checkEntityRefusal fails unless err is the refusal of the undeclared &b;.
func checkEntityRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("MaxEntities 1 accepted a document declaring two entities")
	}
	if !strings.Contains(err.Error(), "&b;") {
		t.Fatalf("refused, but not for the entity past the limit: %v", err)
	}
}

func TestFileResolverMaxEntities(t *testing.T) {
	for _, n := range []int{0, 1} {
		r := entityResolver(t, n)
		path := filepath.Join(r.Roots[0], "doc.xml")
		if err := os.WriteFile(path, []byte(twoEntities), 0o600); err != nil {
			t.Fatal(err)
		}
		tree, err := r.ResolveDocument("doc.xml", fileURIOf(path))
		if n == 1 {
			checkEntityRefusal(t, err)
			continue
		}
		if err != nil {
			t.Fatalf("the default refused two entities: %v", err)
		}
		if got := tree.Root.StringValue(); got != "xy" {
			t.Fatalf("string value %q, want xy", got)
		}
	}
}

// runWithDoc runs src's template "go" through r with params.
func runWithDoc(t *testing.T, src string, r *FileResolver, params map[string]string) (string, error) {
	t.Helper()
	ps := map[string]xdm.Sequence{}
	for k, v := range params {
		ps[k] = xdm.Sequence{xdm.NewString(v)}
	}
	res, err := runNested(t, src, TransformOptions{Documents: r, InitialTemplate: "go", Params: ps})
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, it := range res.Nodes {
		if n, ok := it.(*xdm.Node); ok {
			sb.WriteString(n.StringValue())
		}
	}
	return sb.String(), nil
}

const parseXMLSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:param name="doc" as="xs:string"/>
  <xsl:template name="go"><xsl:value-of select="parse-xml($doc)"/></xsl:template>
</xsl:stylesheet>`

func TestParseXMLHonoursResolverEntityLimits(t *testing.T) {
	params := map[string]string{"doc": twoEntities}
	if got, err := runWithDoc(t, parseXMLSheet, entityResolver(t, 0), params); err != nil || got != "xy" {
		t.Fatalf("default: got %q, %v; want xy", got, err)
	}
	_, err := runWithDoc(t, parseXMLSheet, entityResolver(t, 1), params)
	checkEntityRefusal(t, err)
}

// nestedSheet runs $text through fn:transform, handing it $doc.
const nestedSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:param name="doc" as="xs:string"/>
  <xsl:param name="text" as="xs:string"/>
  <xsl:template name="go">
    <xsl:value-of select="transform(map{
      'stylesheet-text': $text,
      'initial-template': QName('','go'),
      'stylesheet-params': map{QName('','doc'): $doc},
      'delivery-format':'raw'})?output"/>
  </xsl:template>
</xsl:stylesheet>`

// A nested transform inherits the caller's resolver, so both of its parses
// -- its own stylesheet-text and the fn:parse-xml it calls -- are bound by
// the same limits.
func TestNestedTransformInheritsEntityLimits(t *testing.T) {
	inner := map[string]string{
		"stylesheet-text": `<!DOCTYPE xsl:stylesheet [<!ENTITY a "x"><!ENTITY b "y">]>
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:param name="doc" as="xs:string"/>
  <xsl:template name="go">&a;&b;</xsl:template>
</xsl:stylesheet>`,
		"parse-xml": parseXMLSheet,
	}
	for name, text := range inner {
		t.Run(name, func(t *testing.T) {
			params := map[string]string{"doc": twoEntities, "text": text}
			if got, err := runWithDoc(t, nestedSheet, entityResolver(t, 0), params); err != nil || got != "xy" {
				t.Fatalf("default: got %q, %v; want xy", got, err)
			}
			_, err := runWithDoc(t, nestedSheet, entityResolver(t, 1), params)
			checkEntityRefusal(t, err)
		})
	}
}
