package xslt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// countingModules counts the module resolutions a compilation makes.
type countingModules struct {
	memModules
	calls int
}

func (m *countingModules) ResolveModule(href, base string) (*xdm.Node, string, error) {
	m.calls++
	return m.memModules.ResolveModule(href, base)
}

// diamondChain builds L0..L{depth}: each level imports the next twice and
// names a template t after itself; the leaf also has a template leaf.
func diamondChain(depth int) *countingModules {
	m := &countingModules{memModules: memModules{}}
	for i := 0; i <= depth; i++ {
		body := fmt.Sprintf(`<xsl:template name="t">L%d</xsl:template>`, i)
		if i < depth {
			imp := fmt.Sprintf(`<xsl:import href="L%d.xsl"/>`, i+1)
			body = imp + imp + body
		} else {
			body += `<xsl:template name="leaf">leaf</xsl:template>`
		}
		m.memModules[fmt.Sprintf("L%d.xsl", i)] = `<xsl:stylesheet ` +
			`xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">` +
			body + `</xsl:stylesheet>`
	}
	return m
}

const diamondMain = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
	<xsl:import href="L0.xsl"/>
	<xsl:output method="text"/>
	<xsl:template name="xsl:initial-template">
		<xsl:call-template name="t"/><xsl:text> </xsl:text><xsl:call-template name="leaf"/>
	</xsl:template>
</xsl:stylesheet>`

// A module imported on several paths is compiled once per path, since each
// occurrence has its own precedence, so a chain importing each level twice
// costs 2^depth. The compilation's module visits are bounded instead: 20
// levels (about two million visits) is refused as a resource limit after at
// most maxModuleVisits resolutions.
func TestImportDiamondIsBounded(t *testing.T) {
	m := diamondChain(20)
	tree, err := xdm.ParseString(diamondMain, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(tree.Root, CompileOptions{Resolver: m})
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("got %v after %d resolutions, want a resource limit", err, m.calls)
	}
	if !strings.Contains(err.Error(), "module visits") {
		t.Errorf("error %q does not name the module-visit bound", err)
	}
	if m.calls > maxModuleVisits+100 {
		t.Errorf("%d resolutions, above the bound of %d", m.calls, maxModuleVisits)
	}

	// A modest diamond still compiles, and the importer's own L0 outranks
	// every module it reaches.
	small := diamondChain(5)
	s, err := Compile(tree.Root, CompileOptions{Resolver: small})
	if err != nil {
		t.Fatalf("5-level diamond: %v", err)
	}
	res, err := s.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "initial-template", InitialTemplateURI: xdm.NSXSL})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(res.String()); got != "L0 leaf" {
		t.Errorf("5-level diamond: got %q, want %q", got, "L0 leaf")
	}
}
