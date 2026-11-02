package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// groupDocs are documents with runs of keys: equal neighbours, a key on the
// first and the last item alone, a missing key (XTTE1100), other elements
// between the members, members under several parents.
var groupDocs = map[string]string{
	"runs": `<doc><t d="1" v="5" k="a"><n>x</n></t><t d="1" v="-2" k="b"/><t d="2" v="3" k="a"><n>y</n><n>z</n></t>` +
		`<t d="2" v="-1" k="a"/><t d="2" v="4" k="c"/><t d="3" v="-7" k="c"><n/></t></doc>`,
	"single": `<doc><t d="1" v="1" k="a">only</t></doc>`,
	"none":   `<doc><u/></doc>`,
	"mixed": `<doc>a<t d="1" v="1" k="x">p</t><u d="1"/>b<t d="1" v="-1" k="y"><n>q</n></t><!--c--><t d="2" v="2" k="y"/>` +
		`<?pi x?><t d="2" v="-3" k="z"/></doc>`,
	"nokey":  `<doc><t d="1" v="1" k="a"/><t v="2" k="b"/><t d="1" v="3" k="c"/></doc>`,
	"parent": `<doc><g><t d="1" v="1" k="a"/><t d="1" v="2" k="a"/></g><g><t d="1" v="-3" k="b"/><t d="2" v="4" k="b"/></g></doc>`,
	"bad":    `<doc><t d="1" v="1"/><t d="1" v="2"></doc>`,
	"wide":   "<doc>" + strings.Repeat(`<t d="1" v="1" k="a"><n>s</n></t><t d="1" v="-2" k="b"/><t d="2" v="3" k="b"/>`, 300) + "</doc>",
	"nested": `<doc><t d="1" v="1" k="a"><t d="1" v="2" k="b"/></t><t d="2" v="-1" k="b"><t d="9" v="1" k="c"/></t></doc>`,
}

// groupBodies are container bodies holding a for-each-group. stream says the
// core must take the container.
var groupBodies = []struct {
	body   string
	stream bool
}{
	// The body reads nothing of the group: only keys and positions.
	{`<xsl:for-each-group select="/doc/t" group-adjacent="@d"><g k="{current-grouping-key()}" p="{position()}" f="{@v}"/></xsl:for-each-group>`, true},
	{`<out><xsl:for-each-group select="*/t" group-starting-with="*[@v &lt; 0]"><g f="{@k}" p="{position()}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="*/t" group-ending-with="t[@v &lt; 0]"><g f="{@k}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="*/t" group-adjacent="@k" collation="http://www.w3.org/2005/xpath-functions/collation/codepoint"><g k="{current-grouping-key()}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="*/t" group-adjacent="@d, @k" composite="yes"><g k="{current-grouping-key()}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="*/t" group-adjacent="position() idiv 2"><g k="{current-grouping-key()}" n="{name()}"/></xsl:for-each-group></out>`, true},
	// The body reads the group: copy-of streamed a member at a time, splits.
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><batch d="{current-grouping-key()}"><xsl:copy-of select="current-group()"/></batch></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><b><xsl:value-of select="count(current-group())"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><b><xsl:value-of select="sum(current-group()/@v)"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><b><xsl:value-of select="current-group()/@k" separator="-"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><b><xsl:value-of select="current-group()"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-starting-with="*[@v &lt; 0]"><b><xsl:copy-of select="current-group()[1]"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-ending-with="*[@v &lt; 0]"><b><xsl:copy-of select="current-group()[2]"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><b><xsl:copy-of select="current-group()[@v &gt; 0]"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><b><xsl:copy-of select="current-group()/n"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><b><xsl:value-of select="string-join(current-group()/n, '+')"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><b x="{@k}"><xsl:value-of select="exists(current-group()[@v = 3])"/></b></xsl:for-each-group></out>`, true},
	// xsl:sequence in an element's content: a copy at a time, as copy-of.
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><b><xsl:sequence select="current-group()"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><b><xsl:sequence select="current-group()[@v &gt; 0]/n"/></b></xsl:for-each-group></out>`, true},
	{`<out><xsl:sequence select="/doc/t[@v &gt; 0]"/></out>`, true},
	{`<xsl:for-each-group select="/doc/t" group-adjacent="@d"><xsl:sequence select="current-group()"/></xsl:for-each-group>`, false},
	// The body reads the first item only.
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><f><xsl:copy-of select="."/></f></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><f><xsl:value-of select="."/></f></xsl:for-each-group></out>`, true},
	// Loops over the group.
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><g><xsl:for-each select="current-group()"><m p="{position()}" v="{@v}"><xsl:value-of select="."/></m></xsl:for-each></g></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><g><xsl:iterate select="current-group()"><xsl:param name="s" select="0"/>` +
		`<xsl:on-completion><s v="{$s}"/></xsl:on-completion><xsl:next-iteration><xsl:with-param name="s" select="$s + @v"/></xsl:next-iteration></xsl:iterate></g></xsl:for-each-group></out>`, true},
	// Nested groups over the current group.
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><w d="{current-grouping-key()}"><xsl:for-each-group select="current-group()" group-adjacent="@k">` +
		`<k v="{current-grouping-key()}"><xsl:copy-of select="current-group()"/></k></xsl:for-each-group></w></xsl:for-each-group></out>`, true},
	// A consuming attribute value template, of the group or of a context;
	// two of them are two consumers.
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><g n="{count(current-group())}" s="{sum(current-group()/@v)}" k="{current-grouping-key()}"/></xsl:for-each-group></out>`, false},
	{`<out><xsl:for-each-group select="/doc/t" group-starting-with="*[@v &lt; 0]"><w><xsl:for-each-group select="current-group()" group-ending-with="*[@k = 'a']">` +
		`<k n="{count(current-group())}"/></xsl:for-each-group></w></xsl:for-each-group></out>`, true},
	{`<out n="{count(//t)}" k="{name(/*)}"/>`, false},
	{`<xsl:for-each select="/doc/t"><r k="{@k}" n="{count(n)}"/></xsl:for-each>`, true},
	{`<out><xsl:for-each select="/doc/g"><g><xsl:for-each-group select="t" group-adjacent="@d"><n c="{count(current-group())}"/></xsl:for-each-group></g></xsl:for-each></out>`, true},
	{`<out n="{count(//t)}"><xsl:value-of select="count(//n)"/></out>`, false},
	// xsl:iterate carrying a copy of the item (si-iterate-136..138), or all
	// of them (the copies are the tree's too), and xsl:break with a value
	// read off it (the analysis does not trust it: on the tree).
	{`<out><xsl:iterate select="/*/t"><xsl:param name="prev" select="()"/><xsl:on-completion><y><xsl:copy-of select="$prev"/></y></xsl:on-completion>` +
		`<xsl:if test="$prev"><x><xsl:copy-of select="$prev"/></x></xsl:if><xsl:next-iteration><xsl:with-param name="prev" select="copy-of(.)"/></xsl:next-iteration></xsl:iterate></out>`, true},
	{`<out><xsl:iterate select="/*/t"><xsl:param name="prev" select="()"/><xsl:param name="this" select="()"/><xsl:on-completion><y><xsl:copy-of select="$prev"/></y></xsl:on-completion>` +
		`<z><xsl:copy-of select="$this"/></z><xsl:next-iteration><xsl:with-param name="prev" select="copy-of(.)"/><xsl:with-param name="this" select="copy-of($prev)"/></xsl:next-iteration></xsl:iterate></out>`, true},
	{`<out><xsl:iterate select="/*/t"><xsl:param name="n" select="1" as="xs:integer"/><xsl:param name="prev" select="()"/><x n="{$n}" v="{$prev/@v}"/>` +
		`<xsl:next-iteration><xsl:with-param name="n" select="$n + 1"/><xsl:with-param name="prev" select="copy-of(.)[$n gt 2]"/></xsl:next-iteration></xsl:iterate></out>`, true},
	{`<out><xsl:iterate select="/*/t"><xsl:param name="s" select="''"/><xsl:on-completion><s v="{$s}"/></xsl:on-completion>` +
		`<xsl:next-iteration><xsl:with-param name="s" select="concat($s, string(.))"/></xsl:next-iteration></xsl:iterate></out>`, false},
	{`<out><xsl:iterate select="/*/t"><xsl:if test="@v &lt; 0"><xsl:break select="copy-of(.)"/></xsl:if><i k="{@k}"/></xsl:iterate></out>`, false},
	{`<out><xsl:iterate select="/*/t"><xsl:param name="all" select="()"/><xsl:on-completion><xsl:copy-of select="$all"/></xsl:on-completion>` +
		`<xsl:next-iteration><xsl:with-param name="all" select="($all, copy-of(.))"/></xsl:next-iteration></xsl:iterate></out>`, true},
	// Refused: group-by, sort, a crawling population, a key a view cannot
	// decide, current-group() read twice.
	{`<out><xsl:for-each-group select="/doc/t" group-by="@d"><g/></xsl:for-each-group></out>`, false},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><xsl:sort select="@k"/><g/></xsl:for-each-group></out>`, false},
	{`<out><xsl:for-each-group select="//t" group-adjacent="@d"><g n="{count(current-group())}"/></xsl:for-each-group></out>`, false},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="string(.)"><g/></xsl:for-each-group></out>`, false},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><g n="{count(current-group())}"><xsl:copy-of select="current-group()"/></g></xsl:for-each-group></out>`, false},
}

// TestStreamGroupMatchesTree: every grouping body over every document, under
// every whitespace declaration, gives on the core the tree's output or the
// tree's error (XTTE1100 for a missing key, a parse error), Result.Nodes
// aside; the ones that must stream ran on the core without a re-run.
func TestStreamGroupMatchesTree(t *testing.T) {
	dir := t.TempDir()
	streamed := 0
	for name, doc := range groupDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range groupBodies {
			for _, d := range coreDecls {
				where := fmt.Sprintf("doc %s, decls %q, body %s", name, d, c.body)
				streamed += coreCheck(t, coreSheetWith(d, c.body), dir, where, c.stream, TransformOptions{})
			}
		}
	}
	if streamed == 0 {
		t.Error("nothing streamed without a re-run")
	}
}
