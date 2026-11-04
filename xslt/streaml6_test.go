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
	// A motionless xsl:apply-templates over items not of the stream (applyForeign):
	// copies of the view's attributes, a map of its values, a variable's copies; the
	// rules are the tree's, in a mode that is not streamable.
	{l6ForeignRules + `<xsl:mode name="f" streamable="yes"/>
<xsl:template match="rec" mode="f"><r><xsl:apply-templates select="copy-of(@*)" mode="a"/><xsl:apply-templates select="map{'k': string(@k)}" mode="a"/></r></xsl:template>`,
		`<out><xsl:apply-templates select="doc/rec" mode="f"/></out>`, true},
	{l6ForeignRules, `<out><xsl:for-each select="doc/rec"><xsl:variable name="m" select="map{1: copy-of(sub)}"/><e><xsl:apply-templates select="$m?*" mode="a"/></e></xsl:for-each></out>`, true},
	// A variable holding the stream's own (grounded) nodes is not fresh: refused.
	{l6ForeignRules, `<out><xsl:for-each select="doc/rec"><xsl:variable name="v" select="sub"/><e><xsl:apply-templates select="$v" mode="a"/></e></xsl:for-each></out>`, false},
	// has-children of the element dispatched, answered by a Peek (streamkids_l6.go):
	// in a motionless xsl:attribute before the consumer (sf-has-children-001), in
	// patterns (streamable-135), and refused where a rule reads its element whole.
	{`<xsl:mode name="h" streamable="yes"/>
<xsl:template match="*" mode="h"><xsl:copy><xsl:copy-of select="@*"/><xsl:attribute name="hc" select="has-children(.)"/><xsl:apply-templates mode="h"/></xsl:copy></xsl:template>`,
		`<out><xsl:apply-templates mode="h"/></out>`, true},
	{`<xsl:mode name="h1" streamable="yes"/>
<xsl:template match="*[has-children()]" mode="h1"><xsl:copy><xsl:copy-of select="@*"/><xsl:apply-templates mode="h1"/></xsl:copy></xsl:template>
<xsl:template match="*[not(has-children())]" mode="h1"><xsl:copy><xsl:copy-of select="@*"/><xsl:attribute name="empty" select="true()"/></xsl:copy></xsl:template>`,
		`<out><xsl:apply-templates mode="h1"/></out>`, true},
	{`<xsl:mode name="h2" streamable="yes"/>
<xsl:template match="rec[has-children()]" mode="h2"><xsl:copy-of select="."/></xsl:template>`,
		`<out><xsl:apply-templates mode="h2"/></out>`, false},
	// A choice of two streamed populations (choiceOfStreams), and F union P with F
	// fresh (unionPop), in loops and applies (sx-if-115/118, sx-union-115/118).
	{l6UnionDecls, `<xsl:variable name="c" select="true()"/><out><xsl:for-each select="if ($c) then doc/rec else doc/rec/sub"><e n="{name()}"><xsl:value-of select="string(.)"/></e></xsl:for-each></out>`, true},
	{l6UnionDecls, `<xsl:variable name="c" select="false()"/><out><xsl:for-each select="if ($c) then doc/rec else doc/rec/sub"><e n="{name()}"><xsl:value-of select="string(.)"/></e></xsl:for-each></out>`, true},
	{l6UnionDecls, `<out><xsl:for-each select="($ins union doc/rec/sub)"><e n="{name()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{l6UnionDecls, `<xsl:variable name="c" select="false()"/><out><xsl:apply-templates select="if ($c) then doc/rec[1]/* else doc/rec[2]/*" mode="u"/></out>`, true},
	{l6UnionDecls, `<out><xsl:apply-templates select="($ins union doc/rec[1]/*)" mode="u"/></out>`, true},
	// apply-templates over values read off the stream (sx-if-218): collected, dispatched.
	{`<xsl:mode name="v" streamable="yes"/><xsl:template match=".[. instance of xs:integer]" mode="v"><i p="{position()}" of="{last()}"><xsl:value-of select="."/></i></xsl:template>`,
		`<out><xsl:apply-templates select="if (doc/rec/sub) then 0 else 1" mode="v"/></out>`, true},
	// [A, B]?* read as (A, B) (flatArray), the select's split refused (sx-square-array-*).
	{l6UnionDecls, `<out><xsl:for-each select="[$ins, doc/rec/sub/copy-of()]?*"><e n="{name()}"><xsl:value-of select="."/></e></xsl:for-each></out>`, true},
	{l6UnionDecls, `<out><xsl:apply-templates select="[doc/rec[1]/*/snapshot(), $ins]?*" mode="u"/></out>`, true},
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

// l6ForeignRules are the rules of a mode that is not streamable, for items not
// of the stream.
const l6ForeignRules = `<xsl:template match="@*" mode="a"><a n="{name()}" v="{.}" p="{position()}" of="{last()}"/></xsl:template>
<xsl:template match=".[. instance of map(*)]" mode="a"><m k="{.?k}"/></xsl:template>
<xsl:template match="sub" mode="a"><s x="{@x}" up="{name(..)}"/></xsl:template>`

// l6UnionDecls are a fresh global and a streamable mode for populations.
const l6UnionDecls = `<xsl:variable name="ins" as="element()*"><a>A</a><b>B</b></xsl:variable>
<xsl:mode name="u" streamable="yes" on-no-match="deep-skip"/>
<xsl:template match="rec/*" mode="u"><u p="{position()}"><xsl:value-of select="."/></u></xsl:template>`

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
