package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// l6Bodies are container bodies of lane L6's shapes (streamsplit_l6.go);
// stream says the core must take them.
var l6Bodies = []struct {
	body   string
	stream bool
}{
	// A loop over attributes or leaves whose body reads the item whole (leafLoop).
	{`<out><xsl:for-each select="doc/rec/@k"><xsl:document><xsl:sequence select="data(.)"/></xsl:document></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="//sub/text()"><xsl:document><xsl:copy-of select="."/></xsl:document></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="//rec/@*"><a n="{name()}"><xsl:value-of select="."/></a></xsl:for-each></out>`, true},
	{`<xsl:variable name="d" as="document-node()*"><xsl:for-each select="doc/rec[@v gt 1]/@v"><xsl:document><xsl:sequence select="data(.)"/></xsl:document></xsl:for-each></xsl:variable><out><xsl:copy-of select="data($d)"/></out>`, true},
	// The range operator as a loop's population, the body reading each value (streamable-077).
	{`<out><xsl:for-each select="doc/rec"><e><xsl:for-each select="1 to count(.//*)"><xsl:value-of select="concat(., ' ')"/></xsl:for-each></e></xsl:for-each></out>`, true},
	// xsl:iterate over attributes and over values (si-iterate-134's shape).
	{`<out><xsl:iterate select="//rec/@k"><xsl:param name="s" select="''"/><xsl:on-completion><xsl:value-of select="$s"/></xsl:on-completion><xsl:next-iteration><xsl:with-param name="s" select="concat($s, .)"/></xsl:next-iteration></xsl:iterate></out>`, true},
	{`<out><xsl:iterate select=".//*/name()"><xsl:param name="n" select="0"/><xsl:on-completion><xsl:value-of select="$n"/></xsl:on-completion><xsl:next-iteration><xsl:with-param name="n" select="$n + string-length(.)"/></xsl:next-iteration></xsl:iterate></out>`, true},
	// A for-each over copies of nested matches (applyCopies on a loop op).
	{`<out><xsl:for-each select=".//rec/copy-of(.)"><r k="{@k}" n="{count(.//sub)}"/></xsl:for-each></out>`, true},
	// A grounded loop body reading last() stays on the tree (groundLastWhy).
	{`<out><xsl:for-each select="copy-of(doc/rec)"><r n="{position()}" of="{last()}"/></xsl:for-each></out>`, false},
	{`<out><xsl:for-each select="doc/rec/copy-of(.)"><r of="{last()}" n="{count(.//sub)}"/></xsl:for-each></out>`, false},
	// Simple content over the context's subtree, from its grounded copy (groundContextSplit).
	{`<out><xsl:for-each select="doc/rec"><e><xsl:value-of select="descendant-or-self::*" separator="~"/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="doc/rec"><e><xsl:value-of select="(sub/@x, .//sub)" separator="|"/></e></xsl:for-each></out>`, true},
	{`<out><xsl:for-each select="doc/rec"><e><xsl:attribute name="a" select="descendant-or-self::*/name()"/></e></xsl:for-each></out>`, true},
	// A motionless xsl:iterate over the ancestors' views (motionlessIterate), and a
	// motionless for-each over a variable's items with xsl:sort (foreignSort).
	{`<out><xsl:for-each select="doc/rec/sub"><xsl:iterate select="ancestor::*"><a n="{name()}"><xsl:copy-of select="@*"/></a></xsl:iterate></xsl:for-each></out>`, true},
	{`<xsl:variable name="ks" select="('b', 'a', 'c')"/><out><xsl:for-each select="doc/rec"><e><xsl:for-each select="$ks"><xsl:sort select="." order="descending"/><k><xsl:value-of select="."/></k></xsl:for-each></e></xsl:for-each></out>`, true},
	// A motionless loop over the view's attributes (motionlessLoop, leafBody).
	{`<out><xsl:for-each select="doc/rec"><e><xsl:for-each select="@*"><n><xsl:value-of select="name(.)"/></n><v><xsl:value-of select="."/></v></xsl:for-each></e></xsl:for-each></out>`, true},
}

// l6DeclBodies are l6Bodies that need top-level declarations.
var l6DeclBodies = []struct {
	decl, body string
	stream     bool
}{
	// si-apply-templates-001's rule: loops over its own and its ancestors' attributes.
	{`<xsl:mode name="m" streamable="yes" on-no-match="shallow-skip"/>
<xsl:template match="rec" mode="m"><r><xsl:for-each select="@*"><n><xsl:value-of select="name(.)"/></n><v><xsl:value-of select="."/></v></xsl:for-each><xsl:for-each select="ancestor::*"><a n="{name(.)}"><xsl:for-each select="@*"><xsl:value-of select="."/></xsl:for-each></a></xsl:for-each></r></xsl:template>`,
		`<out><xsl:apply-templates mode="m"/></out>`, true},
	// streamable-032's rule: descendant-or-self::* in xsl:value-of.
	{`<xsl:mode name="g" streamable="yes" on-no-match="shallow-skip"/>
<xsl:template match="rec" mode="g"><v><xsl:value-of select="descendant-or-self::*" separator=" ~ "/></v></xsl:template>`,
		`<out><xsl:apply-templates mode="g"/></out>`, true},
	// key() over a global variable's document in a pattern and a rule body (viewKeyOK).
	{`<xsl:variable name="kd"><ch id="a">K</ch><ch id="c">C</ch></xsl:variable><xsl:key name="kk" match="ch" use="@id"/>
<xsl:mode name="k" streamable="yes" on-no-match="shallow-skip"/>
<xsl:template match="rec[key('kk', @k, $kd)]" mode="k"><hit k="{@k}"><xsl:value-of select="key('kk', @k, $kd)"/></hit></xsl:template>`,
		`<out><xsl:apply-templates mode="k"/></out>`, true},
	// current() in a rule's pattern is the candidate (sf-current-100).
	{`<xsl:mode name="c" streamable="yes" on-no-match="shallow-copy"/>
<xsl:template match="*[ancestor::*[node-name(.) = node-name(current())]]" mode="c"><nested n="{name()}"/></xsl:template>`,
		`<out><xsl:apply-templates mode="c"/></out>`, true},
	// current() in an instruction is its own item (streamBuilder.expr curOK), and
	// apply-templates select="@*" at the document node selects nothing (docAttrSelect):
	// si-apply-templates-010's rules.
	{`<xsl:mode name="tc" streamable="yes" on-no-match="shallow-copy"/>
<xsl:template match="sub/text()" mode="tc"><xsl:value-of select="concat(current(), '_T')"/></xsl:template>`,
		`<out><xsl:apply-templates mode="tc"/></out>`, true},
	{`<xsl:mode name="dot" streamable="yes"/>
<xsl:template match="." mode="dot"><xsl:copy><xsl:apply-templates select="@*" mode="dot"/><xsl:apply-templates select="node()" mode="dot"/></xsl:copy></xsl:template>`,
		`<out><xsl:apply-templates select="." mode="dot"/></out>`, true},
	// @* except @v in a rule that also matches text nodes (leafAttrSetOp, streamable-063).
	{`<xsl:mode name="n" streamable="yes"/>
<xsl:template match="node()" mode="n"><xsl:copy><xsl:copy-of select="@* except @v"/><xsl:apply-templates mode="n"/></xsl:copy></xsl:template>`,
		`<out><xsl:apply-templates mode="n"/></out>`, true},
	// xsl:apply-templates over copies of the matches, to the tree's rules of a mode
	// that is not streamable (executeCopies): positions and last() are the tree's.
	{l6CopyRules, `<out><xsl:apply-templates select="doc/rec/copy-of(.)" mode="t"/></out>`, true},
	{l6CopyRules, `<out><xsl:apply-templates select="copy-of(doc/rec)" mode="t"/></out>`, true},
	{l6CopyRules, `<out><xsl:apply-templates select="doc/rec/copy-of()" mode="t"><xsl:with-param name="p" select="'w'"/></xsl:apply-templates></out>`, true},
	{l6CopyRules, `<out><xsl:apply-templates select=".//rec/copy-of(.)" mode="t"><xsl:with-param name="p" select="7"/></xsl:apply-templates></out>`, true},
	// Accumulators declared: the copies' values are not held (copiesLinkWhy).
	{l6CopyRules + `<xsl:accumulator name="a" as="xs:integer" initial-value="0" streamable="yes"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>`,
		`<out><xsl:apply-templates select="doc/rec/copy-of(.)" mode="t"/></out>`, false},
}

// l6CopyRules are the rules of a mode that is not streamable, for copies.
const l6CopyRules = `<xsl:template match="rec" mode="t"><xsl:param name="p" select="'-'"/><r k="{@k}" p="{$p}" n="{position()}" of="{last()}"><xsl:value-of select="count(.//sub)"/></r></xsl:template>`

func TestStreamL6MatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range coreDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range l6Bodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
		for _, c := range l6DeclBodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s %s", name, d, c.decl, c.body)
				streamed += coreCheck(t, coreSheetWith(d+c.decl, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}
