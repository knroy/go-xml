package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// callDecls are stylesheet functions of each category, some that are their
// bodies exactly and some that are not (a conversion that may fail, a
// recursive body, a global variable, a cache).
const callDecls = `<xsl:variable name="g" select="'k'"/>
<xsl:function name="p:depth" as="xs:integer" streamability="inspection"><xsl:param name="e" as="node()"/><xsl:sequence select="count($e/ancestor-or-self::*)"/></xsl:function>
<xsl:function name="p:att" as="xs:string" streamability="inspection"><xsl:param name="e" as="node()"/><xsl:param name="n" as="xs:string"/><xsl:sequence select="string($e/@*[local-name() = $n])"/></xsl:function>
<xsl:function name="p:cd" as="xs:integer" streamability="absorbing"><xsl:param name="in" as="node()*"/><xsl:sequence select="count($in/descendant::*)"/></xsl:function>
<xsl:function name="p:cd2" as="xs:integer" streamability="absorbing"><xsl:param name="in" as="node()*"/><xsl:sequence select="count($in ! descendant::*)"/></xsl:function>
<xsl:function name="p:txt" as="xs:string" streamability="absorbing"><xsl:param name="in" as="node()"/><xsl:sequence select="string($in)"/></xsl:function>
<xsl:function name="p:keep" as="node()?" streamability="filter"><xsl:param name="in" as="node()"/><xsl:param name="t" as="xs:boolean"/><xsl:sequence select="$in[$t]"/></xsl:function>
<xsl:function name="p:kids" as="node()*" streamability="shallow-descent"><xsl:param name="in" as="node()"/><xsl:sequence select="$in/sub"/></xsl:function>
<xsl:function name="p:all" as="node()*" streamability="deep-descent"><xsl:param name="in" as="node()"/><xsl:sequence select="$in//sub"/></xsl:function>
<xsl:function name="p:up" as="node()*" streamability="ascent"><xsl:param name="in" as="node()"/><xsl:sequence select="$in/ancestor::*"/></xsl:function>
<xsl:function name="p:num" as="xs:integer" streamability="inspection"><xsl:param name="e" as="node()"/><xsl:sequence select="xs:integer($e/@v)"/></xsl:function>
<xsl:function name="p:rec" as="xs:integer" streamability="inspection"><xsl:param name="e" as="node()"/><xsl:sequence select="if ($e/.. instance of document-node()) then 1 else p:rec($e/..) + 1"/></xsl:function>
<xsl:function name="p:glob" as="xs:boolean" streamability="inspection"><xsl:param name="e" as="node()"/><xsl:sequence select="exists($e/@*[name() = $g])"/></xsl:function>
<xsl:function name="p:cached" as="xs:integer" streamability="inspection" cache="yes"><xsl:param name="e" as="node()"/><xsl:sequence select="count($e/ancestor::*)"/></xsl:function>
<xsl:function name="p:sum" as="xs:double" streamability="unclassified"><xsl:param name="in" as="xs:double*"/><xsl:sequence select="sum($in)"/></xsl:function>`

// callBodies are container bodies calling them; stream says the core must
// take the container.
var callBodies = []struct {
	body   string
	stream bool
}{
	{`<o><xsl:value-of select="/doc/rec ! p:depth(.)"/></o>`, true},
	{`<o><xsl:value-of select="//sub ! p:depth(.)"/></o>`, true},
	{`<o><xsl:value-of select="/doc/rec ! p:att(., 'k')"/></o>`, true},
	{`<o><xsl:value-of select="/doc/rec ! p:cd(.)"/></o>`, true},
	{`<o><xsl:value-of select="/doc/rec ! p:cd2(.)"/></o>`, true},
	{`<o><xsl:value-of select="p:cd(/doc/rec)"/></o>`, true},
	{`<o><xsl:value-of select="/doc/rec ! p:txt(.)" separator="|"/></o>`, true},
	// A call in a motionless instruction is not inlined (the builder's
	// view check refuses it): the container stays on the tree.
	{`<o><xsl:for-each select="/doc/rec"><r d="{p:depth(.)}"><xsl:value-of select="p:cd(.)"/></r></xsl:for-each></o>`, false},
	{`<o><xsl:value-of select="count(p:kids(/doc/rec[1]))"/></o>`, false},
	// The simple map of atomic values as a path, with no function.
	{`<o><xsl:value-of select="/doc/rec ! name()"/></o>`, true},
	{`<o><xsl:value-of select="sum(/doc/rec ! string-length(@k))"/></o>`, true},
	{`<o><xsl:value-of select="/doc/rec ! (@k || '-')"/></o>`, false},
	// Refused: a result that may be empty where the type wants one (p:num),
	// a recursive call, a global, a cache, an atomic parameter given a
	// path, a node result kept.
	{`<o><xsl:value-of select="/doc/rec ! p:num(.)"/></o>`, false},
	{`<o><xsl:value-of select="/doc/rec ! p:rec(.)"/></o>`, false},
	{`<o><xsl:value-of select="/doc/rec ! p:glob(.)"/></o>`, false},
	{`<o><xsl:value-of select="/doc/rec ! p:cached(.)"/></o>`, false},
	{`<o><xsl:value-of select="p:sum(/doc/rec/@v)"/></o>`, false},
	{`<o><xsl:copy-of select="/doc/rec ! p:keep(., true())"/></o>`, false},
}

// TestStreamCallMatchesTree: calls on stylesheet functions in streamed
// selects, inlined where the body is the call exactly, give the tree's
// output or error over every document and whitespace declaration.
func TestStreamCallMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	docs := map[string]string{"try": tryDocs["nan3"], "bad": tryDocs["bad"]}
	for k, v := range coreDocs {
		docs[k] = v
	}
	for name, doc := range docs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range callBodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d+callDecls, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}
