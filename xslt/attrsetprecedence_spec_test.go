package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

type memModules map[string]string

func (m memModules) ResolveModule(href, base string) (*xdm.Node, string, error) {
	src, ok := m[href]
	if !ok {
		return nil, "", nil
	}
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		return nil, "", err
	}
	return tree.Root, href, nil
}

func (m memModules) ResolveDocument(href, base string) (*xdm.Node, string, error) {
	return m.ResolveModule(href, base)
}

// 10.2.1: the declarations of one attribute set are applied "first in
// increasing order of import precedence, and within each precedence, in
// declaration order", so the highest-precedence declaration is applied last
// and wins.
//
// M includes I, and I imports B. B is imported, so it ranks BELOW M; M's own
// declaration of the set must therefore be applied after B's and win.
func TestAttributeSetImportPrecedenceOrder(t *testing.T) {
	mods := memModules{
		"i.xsl": `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
			<xsl:import href="b.xsl"/>
		</xsl:stylesheet>`,
		"b.xsl": `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
			<xsl:attribute-set name="s"><xsl:attribute name="a">B</xsl:attribute></xsl:attribute-set>
		</xsl:stylesheet>`,
	}
	const main = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:attribute-set name="s"><xsl:attribute name="a">M</xsl:attribute></xsl:attribute-set>
		<xsl:include href="i.xsl"/>
		<xsl:template name="xsl:initial-template">
			<out xsl:use-attribute-sets="s"/>
		</xsl:template>
	</xsl:stylesheet>`
	tree, err := xdm.ParseString(main, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{Resolver: mods})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := s.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "initial-template", InitialTemplateURI: xdm.NSXSL})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if got := res.String(); !strings.Contains(got, `a="M"`) {
		t.Errorf("got %q, want a=\"M\": the including module's declaration "+
			"has the higher import precedence and must be applied last", got)
	}
}
