package xslt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
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
	"nsattr": `<doc xmlns:p="urn:p"><t p:z="1" z="9" d="1"/><t p:z="1" z="8" d="1"/><t p:z="2" z="8" d="2"/><t z="8" d="2"/></doc>`,
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
	// A key read off the start tag (an attribute by name), namespaced or not.
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@p:z"><g k="{current-grouping-key()}" n="{count(current-group())}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@z"><g k="{current-grouping-key()}"><xsl:copy-of select="current-group()"/></g></xsl:for-each-group></out>`, true},
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
		`<xsl:next-iteration><xsl:with-param name="s" select="concat($s, string(.))"/></xsl:next-iteration></xsl:iterate></out>`, true},
	{`<out><xsl:iterate select="/*/t"><xsl:if test="@v &lt; 0"><xsl:break select="copy-of(.)"/></xsl:if><i k="{@k}"/></xsl:iterate></out>`, false},
	{`<out><xsl:iterate select="/*/t"><xsl:param name="all" select="()"/><xsl:on-completion><xsl:copy-of select="$all"/></xsl:on-completion>` +
		`<xsl:next-iteration><xsl:with-param name="all" select="($all, copy-of(.))"/></xsl:next-iteration></xsl:iterate></out>`, true},
	// A population of several steps: levels open and close, counters per
	// parent.
	{`<out><xsl:for-each-group select="*/g/t" group-adjacent="@d"><g n="{count(current-group())}" k="{@k}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="*/g/t[1]" group-starting-with="*[@v &lt; 0]"><g><xsl:copy-of select="current-group()"/></g></xsl:for-each-group></out>`, true},
	// A group of the document element: a copy of it, or a min or max over
	// it, would give up, which only a container's select at the document
	// node may (it falls back to the tree): refused.
	{`<xsl:for-each-group select="*" group-starting-with="*[@k = 'a']"><g><xsl:sequence select="current-group()"/></g></xsl:for-each-group>`, false},
	{`<xsl:for-each-group select="*" group-adjacent="name()"><g><xsl:value-of select="max(current-group()/@v)"/></g></xsl:for-each-group>`, false},
	{`<xsl:for-each-group select="*" group-adjacent="name()"><g><xsl:value-of select="count(current-group()/t)"/></g></xsl:for-each-group>`, true},
	// Refused by the memory rule: the members collected as grounded
	// copies (a group may be all of the document element's children).
	{`<out><xsl:for-each-group select="*/g/t" group-adjacent="@d"><g><xsl:value-of select="string-join(current-group() ! name(), ',')"/></g></xsl:for-each-group></out>`, false},
	// A grounded population (§19.8.4.19's first rule) of values, grouped by
	// the tree's code, group-by and xsl:sort too; of copies too, numbered as
	// the tree's path sort numbers them (numberCopies), so current-group()/@d
	// orders them alike.
	{`<out><xsl:for-each-group select="/doc/t/@k/string()" group-by="."><xsl:sort select="current-grouping-key()" order="descending"/>` +
		`<g k="{.}" n="{count(current-group())}" p="{position()}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t/@v/number()" group-adjacent=". gt 0"><g n="{current-group()}" k="{current-grouping-key()}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="data(/doc/*/@d)" group-by=". mod 2" collation="http://www.w3.org/2005/xpath-functions/collation/codepoint"><g v="{current-group()}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t/copy-of()" group-adjacent="@v idiv 5"><g d="{current-group()/@d}" v="{current-group()/@v}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="copy-of(/doc/t)" group-by="@d"><g n="{count(current-group())}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each select="/doc/g"><xsl:for-each-group select="t/copy-of()" group-adjacent="@v idiv 5"><g d="{current-group()/@d}" v="{current-group()/@v}"/></xsl:for-each-group></xsl:for-each></out>`, true},
	// A streamed population grouped by key (only in xsl:fork, by
	// §19.8.4.19): held as copies, grouped by the tree's code (J1,
	// streamgroup_held.go).
	{`<out><xsl:fork><xsl:for-each-group select="/doc/t" group-by="@d"><g k="{current-grouping-key()}"><xsl:copy-of select="current-group()"/></g></xsl:for-each-group></xsl:fork></out>`, true},
	{`<out><xsl:fork><xsl:for-each-group select="/doc/*" group-by="@k"><g k="{current-grouping-key()}" n="{count(current-group())}"/></xsl:for-each-group></xsl:fork></out>`, true},
	{`<out><xsl:for-each select="/doc/g"><xsl:fork><xsl:for-each-group select="t" group-by="@d"><g s="{sum(current-group()/@v)}"/></xsl:for-each-group></xsl:fork></xsl:for-each></out>`, true},
	// A window over the population, subsequence(P, a, b) with integer
	// literals (L5, streamgroup_window.go): the matches outside it are
	// passed over, counted across parents.
	{`<out><xsl:for-each-group select="subsequence(/doc/t, 2, 3)" group-adjacent="@d"><g k="{current-grouping-key()}"><xsl:copy-of select="current-group()"/></g></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="subsequence(*/g/t, 2, 2)" group-adjacent="position()"><g n="{count(current-group())}" k="{@k}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="subsequence(/doc/t, 3)" group-ending-with="."><g><xsl:copy-of select="current-group()"/></g></xsl:for-each-group></out>`, true},
	// A window by a test of position() alone, (P)[p]; a crawling population
	// (its matches nest) under a body that reads nothing of the group, text
	// value templates in the body (L5, streamgroup_crawl.go).
	{`<out><xsl:for-each-group select="(/doc/t)[position() mod 2 = 1]" group-adjacent="@d"><g k="{current-grouping-key()}"><xsl:copy-of select="current-group()"/></g></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="(//t)[position() = 1 to 3]" group-adjacent="position()"><g p="{position()}" n="{name()}" k="{@k}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="//t" group-ending-with="." expand-text="yes">{position()}:{@d};</xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d" expand-text="yes"><g>{current-grouping-key()}</g></xsl:for-each-group></out>`, true},
	// A population of attributes, leaves or outermost() matches, held and
	// grouped by the tree's code (L5, streamgroup_population.go); refused
	// when the body reaches a template.
	{`<out><xsl:for-each-group select="/doc/t/@v" group-adjacent="position()"><xsl:if test=". &gt;= 0"><xsl:sequence select="current-group()/string(.)"/></xsl:if></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t/@d" group-adjacent="."><g d="{current-grouping-key()}" v="{current-group()/../@v}"/></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="outermost(//t)" group-ending-with="."><g><xsl:copy-of select="current-group()"/></g></xsl:for-each-group></out>`, true},
	{`<out><xsl:for-each-group select="/doc/t/@d" group-adjacent="."><xsl:apply-templates select="current-group()"/></xsl:for-each-group></out>`, false},
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

// TestStreamGroupErrors: a streamed principal input (TransformReader, which
// has no tree to re-run on) whose grouping meets errors in an order other
// than the tree's -- a body's error before a later item's key error or a
// later predicate's, a key error before a later predicate's -- returns the
// tree's error: the select's predicates first, then the keys, then the
// bodies (groupRun.settle).
func TestStreamGroupErrors(t *testing.T) {
	rule := func(sel, by, body string) string {
		return mainSheet(`<xsl:mode streamable="yes"/><xsl:template match="doc"><out><xsl:for-each-group select="` + sel +
			`" ` + by + `>` + body + `</xsl:for-each-group></out></xsl:template>`)
	}
	boom := `<g><xsl:value-of select="xs:integer(@v)"/></g>`
	docs := []string{
		// the body fails at the first group, a key (XTTE1100) at the fourth item
		`<doc><t k="a" v="x"/><t k="a" v="1"/><t k="b" v="2"/><t v="3"/><t k="c" v="4"/></doc>`,
		// the body fails at the first group, a predicate at the last item
		`<doc><t k="a" v="x" n="1"/><t k="b" v="1" n="2"/><t k="b" v="2" n="z"/></doc>`,
		// a key fails at the second item, a predicate at the third
		`<doc><t k="a" v="1" n="1"/><t v="1" n="2"/><t k="b" v="2" n="z"/></doc>`,
		// no error: every group
		`<doc><t k="a" v="1" n="1"/><t k="a" v="2" n="2"/><t k="b" v="3" n="3"/></doc>`,
	}
	sheets := []string{
		rule("t", `group-adjacent="@k"`, boom),
		rule("t[xs:integer(@n) gt 0]", `group-adjacent="@k"`, boom),
		rule("t[xs:integer(@n) gt 0]", `group-adjacent="@k"`, `<g n="{count(current-group())}"/>`),
		rule("t", `group-starting-with="t[xs:integer(@n) = 2]"`, boom),
		rule("t", `group-ending-with="t[xs:integer(@n) = 2]"`, `<g><xsl:copy-of select="current-group()"/></g>`),
	}
	opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{BaseURI: "file:///f/doc.xml"}}
	streamed := 0
	for i, sheet := range sheets {
		for j, doc := range docs {
			so, st := mainRun(sheet, doc, true, opts)
			to, _ := mainRun(sheet, doc, false, opts)
			if so != to {
				t.Errorf("sheet %d, doc %d:\nstreamed %s\ntree     %s", i, j, so, to)
			}
			if st.Guards != 0 {
				t.Errorf("sheet %d, doc %d: %d guard hits", i, j, st.Guards)
			}
			if st.Runs[streamtrace.Main] > 0 {
				streamed++
			}
		}
	}
	if streamed != len(sheets)*len(docs) {
		t.Errorf("%d of %d runs streamed", streamed, len(sheets)*len(docs))
	}
}

// TestStreamGroupMemory: a streamed grouping over 150,000 records holds one
// item of look-ahead and its output, never the tree: counting each group,
// and copying each group a member at a time.
func TestStreamGroupMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("memory test")
	}
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range 150000 {
		fmt.Fprintf(&b, `<t d="%d" v="%d"><n>%d</n><n x="1">x</n></t>`, i/7, i%100, i%10)
	}
	b.WriteString("</doc>")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		body string
		frac float64 // the most of the tree's peak the core may take
	}{
		{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><g n="{count(current-group())}"/></xsl:for-each-group></out>`, 0.25},
		{`<out><xsl:for-each-group select="/doc/t" group-starting-with="t[@v = '0']"><g><xsl:value-of select="sum(current-group()/@v)"/></g></xsl:for-each-group></out>`, 0.25},
		// Its output is a copy of the whole document: what the tree holds
		// beside the document.
		{`<out><xsl:for-each-group select="/doc/t" group-adjacent="@d"><g><xsl:copy-of select="current-group()"/></g></xsl:for-each-group></out>`, 0.9},
	} {
		sheet := coreSheet(c.body)
		var st streamtrace.Counts
		core := splitPeak(func() { _, _, st = coreRun(sheet, dir, true, TransformOptions{}) })
		tree := splitPeak(func() { coreRun(sheet, dir, false, TransformOptions{}) })
		t.Logf("%s: core %.1f MB, tree %.1f MB", c.body, float64(core)/1e6, float64(tree)/1e6)
		if st.Runs[streamtrace.Core] != 1 || st.Reruns != 0 {
			t.Errorf("%s: trace %+v", c.body, st)
		}
		if float64(core) > c.frac*float64(tree) && !raceEnabled {
			t.Errorf("%s: peak live heap %.1f MB, tree %.1f MB (bound %.0f%%)", c.body,
				float64(core)/1e6, float64(tree)/1e6, 100*c.frac)
		}
	}
}
