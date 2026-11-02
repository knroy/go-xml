package xslt_test

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestKeyOverNamespaceNodesAcrossGC checks xsl:key over namespace nodes, whose
// index walk (xpath.NamespaceNodesOf) makes them: they are held weakly, so
// the index must keep them, and a lookup made after collections must still
// find the node the index holds.
func TestKeyOverNamespaceNodesAcrossGC(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:output method="text"/>
<xsl:key name="byuri" match="namespace-node()" use="string(.)"/>
<xsl:template match="/">
  <xsl:variable name="root" select="/"/>
  <xsl:for-each select="1 to 3">
    <xsl:value-of select="count(key('byuri', 'urn:a', $root))"/>
    <xsl:value-of select="key('byuri', 'urn:a', $root)[1] is $root/r/namespace::a"/>
    <xsl:value-of select="key('byuri', 'urn:b', $root)[1] is $root/r/namespace::b"/>
    <xsl:value-of select="generate-id(key('byuri', 'urn:b', $root)[1]) = generate-id($root/r/namespace::b)"/>
    <xsl:text>;</xsl:text>
  </xsl:for-each>
</xsl:template>
</xsl:stylesheet>`
	const doc = `<r xmlns:a="urn:a" xmlns:b="urn:b"><c/><c/></r>`
	got := transformTo(t, sheet, doc)
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	runtime.GC()
	// urn:a is in scope on r and both c elements.
	if want := strings.Repeat("3truetruetrue;", 3); !strings.HasSuffix(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
