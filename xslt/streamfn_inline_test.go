package xslt

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamInlinedCallsMatchTree: calls on stylesheet functions whose
// bodies are sequence constructors, planned as those bodies inside the
// caller's plan (streamfn_inline.go), against the tree on every core
// document and whitespace declaration. stream says the core must take the
// container.
func TestStreamInlinedCallsMatchTree(t *testing.T) {
	decls := `<xsl:mode name="m" streamable="yes"/>` +
		`<xsl:template match="rec" mode="m"><r k="{@k}"/></xsl:template>` +
		`<xsl:function name="p:wrap" as="element()" streamability="absorbing"><xsl:param name="n" as="node()*"/>` +
		`<w><xsl:copy-of select="$n"/></w></xsl:function>` +
		`<xsl:function name="p:app" streamability="absorbing"><xsl:param name="n" as="node()*"/>` +
		`<d><xsl:apply-templates select="$n" mode="m"/></d></xsl:function>` +
		`<xsl:function name="p:grp" streamability="absorbing"><xsl:param name="n" as="node()*"/><xsl:param name="x" as="xs:string"/>` +
		`<xsl:for-each-group select="$n" group-adjacent="string(@k)"><g k="{current-grouping-key()}" x="{$x}">` +
		`<xsl:value-of select="current-group()/@v"/></g></xsl:for-each-group></xsl:function>` +
		`<xsl:function name="p:self" streamability="absorbing"><xsl:param name="n" as="element()"/>` +
		`<s><xsl:value-of select="$n/sub/@x"/></s></xsl:function>` +
		// Refused: the body reads a focus it has not (XPDY0002 on the tree),
		// or the parameter where the body has a focus of its own.
		`<xsl:function name="p:focus"><xsl:param name="n" as="node()*"/>` +
		`<f><xsl:value-of select="name(.)"/></f><xsl:copy-of select="$n"/></xsl:function>` +
		`<xsl:function name="p:inner"><xsl:param name="n" as="node()*"/>` +
		`<xsl:for-each select="1 to 2"><xsl:copy-of select="$n"/></xsl:for-each></xsl:function>` +
		// Lane L2 round 4: recursion on the streamed current group (call
		// frames, streamfn_frame.go), su-absorbing-202's shape.
		`<xsl:function name="p:nest" streamability="absorbing"><xsl:param name="n" as="element()*"/><xsl:param name="d" as="xs:integer"/>` +
		`<xsl:for-each-group select="$n" group-starting-with="rec[@k = 'a']"><xsl:choose>` +
		`<xsl:when test="self::rec[@k = 'a']"><g k="{@k}" d="{$d}"><xsl:sequence select="p:nest(tail(current-group()), $d + 1)"/></g></xsl:when>` +
		`<xsl:otherwise><xsl:apply-templates select="p:nest(tail(current-group()), $d + 1)" mode="m"/></xsl:otherwise>` +
		`</xsl:choose></xsl:for-each-group></xsl:function>` +
		`<xsl:function name="p:nestn" streamability="absorbing"><xsl:param name="n" as="element(rec)*"/><xsl:param name="d" as="xs:integer"/>` +
		`<xsl:for-each-group select="$n" group-starting-with="rec[@k = 'a']"><xsl:choose>` +
		`<xsl:when test="self::rec[@k = 'a']"><g k="{@k}" d="{$d}"><xsl:sequence select="p:nestn(tail(current-group()), $d + 1)"/></g></xsl:when>` +
		`<xsl:otherwise><xsl:apply-templates select="p:nestn(tail(current-group()), $d + 1)" mode="m"/></xsl:otherwise>` +
		`</xsl:choose></xsl:for-each-group></xsl:function>`
	decls += `<xsl:function name="p:one" streamability="absorbing"><xsl:param name="n" as="element()"/>` +
		`<r k="{$n/@k}"><xsl:value-of select="$n/sub/@x"/></r></xsl:function>` +
		`<xsl:function name="p:chk" streamability="absorbing"><xsl:param name="n" as="node()"/>` +
		`<in x="{$n!node-name()}" y="{$n!@k}"/><xsl:text>&#10;</xsl:text><xsl:sequence select="$n!*!p:chk(.)"/></xsl:function>`
	bodies := []struct {
		body   string
		stream bool
	}{
		{`<o><xsl:copy-of select="p:wrap(doc/rec)"/></o>`, true},
		{`<o><xsl:sequence select="p:wrap(doc/rec)"/></o>`, true},
		{`<o><xsl:sequence select="count(p:wrap(doc/rec)/*)"/></o>`, true},
		{`<o><xsl:copy-of select="p:app(doc/rec)"/></o>`, true},
		{`<o><xsl:sequence select="p:grp(doc/rec, 'y')"/></o>`, true},
		{`<xsl:for-each select="doc/rec"><o><xsl:sequence select="p:self(.)"/></o></xsl:for-each>`, true},
		// A call given only a variable's copies reads nothing of the stream.
		{`<xsl:variable name="c" select="copy-of(doc/rec)"/><o><xsl:sequence select="p:wrap($c)"/></o>`, true},
		{`<o><xsl:copy-of select="p:focus(doc/rec)"/></o>`, false},
		{`<o><xsl:copy-of select="p:inner(doc/rec)"/></o>`, false},
		{`<o><xsl:sequence select="p:nest(doc/rec, 1)"/></o>`, true},
		{`<o><xsl:sequence select="p:nestn(doc/rec, 1)"/></o>`, true},
		// Lane L2 round 4: a one-node parameter given a path (a loop with
		// the tree's cardinality: two recs or none decline to the tree),
		// and recursion over the stream ($n!*!p:chk(.), su-absorbing-204).
		{`<o><xsl:sequence select="p:one(doc/rec)"/></o>`, true},
		{`<o><xsl:sequence select="p:chk(*)"/></o>`, true},
		{`<o><xsl:copy-of select="doc ! p:chk(.)"/></o>`, true},
	}
	n := 0
	for name, doc := range coreDocs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		for di, d := range coreDecls {
			for bi, b := range bodies {
				sheet := coreSheetWith(d+decls, b.body)
				so, se, st := coreRun(sheet, dir, true, TransformOptions{})
				to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
				if so != to || se != te {
					t.Errorf("%s decl %d body %d:\ncore %q %q\ntree %q %q", name, di, bi, so, se, to, te)
				}
				if st.Guards != 0 {
					t.Errorf("%s decl %d body %d: %d guard hits", name, di, bi, st.Guards)
				}
				if st.Reruns != 0 && te == "" {
					t.Errorf("%s decl %d body %d: re-run where the tree succeeds", name, di, bi)
				}
				if b.stream && te == "" && name != "empty" && name != "recroot" && st.Runs[0] == 0 {
					t.Errorf("%s decl %d body %d: not streamed", name, di, bi)
				}
				n += int(st.Runs[0])
			}
		}
	}
	if n == 0 {
		t.Fatal("nothing streamed")
	}
}
