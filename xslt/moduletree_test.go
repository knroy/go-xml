package xslt

import (
	"context"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// moduleWithPayload serves one imported module carrying a 4 MB attribute the
// compiler ignores, so that whether the compiled stylesheet keeps the module's
// tree alive shows in the retained heap.
type moduleWithPayload struct{}

func (moduleWithPayload) ResolveModule(href, base string) (*xdm.Node, string, error) {
	mod := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:p="urn:p"
  p:payload="` + strings.Repeat("x", 4<<20) + `">
<xsl:template name="t">
  <xsl:element name="p:e"><xsl:attribute name="p:a" select="1"/></xsl:element>
  <xsl:message error-code="p:c" terminate="no"/>
</xsl:template>
</xsl:stylesheet>`
	d, err := xdm.ParseString(mod, xdm.ParseOptions{})
	if err != nil {
		return nil, "", err
	}
	return d.Root, "urn:mod", nil
}

func heapAlloc() uint64 {
	var m goruntime.MemStats
	goruntime.GC()
	goruntime.GC()
	goruntime.ReadMemStats(&m)
	return m.HeapAlloc
}

// A compiled stylesheet does not keep an imported module's tree alive:
// xsl:element, xsl:attribute and xsl:message/@error-code keep the in-scope
// namespaces they resolve computed names against, not the element.
func TestCompiledSheetReleasesModuleTree(t *testing.T) {
	const main = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:import href="mod.xsl"/>
<xsl:output method="xml" omit-xml-declaration="yes"/>
</xsl:stylesheet>`
	doc, err := xdm.ParseString(main, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := heapAlloc()
	st, err := Compile(doc.Root, CompileOptions{Resolver: moduleWithPayload{}})
	if err != nil {
		t.Fatal(err)
	}
	if kept := int64(heapAlloc()) - int64(before); kept > 2<<20 {
		t.Errorf("compiled stylesheet retains %d bytes; the 4 MB module tree is still reachable", kept)
	}
	res, err := st.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "t"})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != `<p:e xmlns:p="urn:p" p:a="1"/>` {
		t.Errorf("output %q", got)
	}
}
