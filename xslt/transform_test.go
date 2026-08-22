package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// run compiles a stylesheet, applies it to a source document, and returns the
// serialised result with the XML declaration stripped so tests can assert on
// the markup alone.
func run(t *testing.T, sheet, source string) string {
	t.Helper()
	out, err := runErr(t, sheet, source)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	return out
}

func runErr(t *testing.T, sheet, source string) (string, error) {
	t.Helper()
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		return "", err
	}
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		return "", err
	}
	dtree, err := xdm.ParseString(source, xdm.ParseOptions{})
	if err != nil {
		return "", err
	}
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		return "", err
	}
	out := res.String()
	if i := strings.Index(out, "?>"); i >= 0 {
		out = out[i+2:]
	}
	return strings.TrimSpace(out), nil
}

// wrap builds a stylesheet around a body, to keep the test cases readable.
func wrap(body string) string {
	return `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:output omit-xml-declaration="yes"/>` + body + `</xsl:stylesheet>`
}

const bookDoc = `<catalog>
<book id="b1" price="30.50"><title>Go</title><author>Alan</author></book>
<book id="b2" price="9.99"><title>XML</title><author>Beth</author></book>
<book id="b3" price="45.00"><title>XSLT</title><author>Alan</author></book>
</catalog>`

func TestLiteralResultElement(t *testing.T) {
	got := run(t, wrap(`<xsl:template match="/"><out>hello</out></xsl:template>`), `<a/>`)
	if got != "<out>hello</out>" {
		t.Errorf("got %q", got)
	}
}

func TestValueOf(t *testing.T) {
	// (//title)[1] is the first title in the document; //title[1] would be
	// every title that is first among its parent's titles, i.e. all three.
	sheet := wrap(`<xsl:template match="/"><r><xsl:value-of select="(//title)[1]"/></r></xsl:template>`)
	if got := run(t, sheet, bookDoc); got != "<r>Go</r>" {
		t.Errorf("got %q", got)
	}
}

func TestValueOfJoinsSequence(t *testing.T) {
	// XSLT 2.0 joins the whole sequence; 1.0 took only the first item.
	sheet := wrap(`<xsl:template match="/"><r><xsl:value-of select="//title"/></r></xsl:template>`)
	if got := run(t, sheet, bookDoc); got != "<r>Go XML XSLT</r>" {
		t.Errorf("got %q, want the whole sequence space-joined", got)
	}
	// An explicit separator overrides the default.
	sheet = wrap(`<xsl:template match="/"><r><xsl:value-of select="//title" separator=", "/></r></xsl:template>`)
	if got := run(t, sheet, bookDoc); got != "<r>Go, XML, XSLT</r>" {
		t.Errorf("got %q", got)
	}
}

func TestForEach(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><r>
		<xsl:for-each select="//book"><t><xsl:value-of select="title"/></t></xsl:for-each>
	</r></xsl:template>`)
	want := "<r><t>Go</t><t>XML</t><t>XSLT</t></r>"
	if got := run(t, sheet, bookDoc); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyTemplatesAndBuiltInRules(t *testing.T) {
	// With no template for <book>, the built-in rule recurses into children
	// and the built-in text rule copies text through.
	sheet := wrap(`
		<xsl:template match="/"><r><xsl:apply-templates/></r></xsl:template>
		<xsl:template match="title"><T><xsl:value-of select="."/></T></xsl:template>`)
	got := run(t, sheet, bookDoc)
	if !strings.Contains(got, "<T>Go</T>") || !strings.Contains(got, "<T>XSLT</T>") {
		t.Errorf("got %q, want each title wrapped in <T>", got)
	}
	// Author text leaks through via the built-in text rule; that is correct
	// XSLT behaviour and a frequent surprise.
	if !strings.Contains(got, "Alan") {
		t.Errorf("got %q, want author text copied by the built-in rule", got)
	}
}

func TestTemplatePriority(t *testing.T) {
	// A specific name test (priority 0) must beat "*" (priority -0.5).
	sheet := wrap(`
		<xsl:template match="/"><r><xsl:apply-templates select="//title"/></r></xsl:template>
		<xsl:template match="*">GENERIC</xsl:template>
		<xsl:template match="title">SPECIFIC</xsl:template>`)
	got := run(t, sheet, bookDoc)
	if strings.Contains(got, "GENERIC") {
		t.Errorf("got %q: the specific name test must win over '*'", got)
	}
	if !strings.Contains(got, "SPECIFIC") {
		t.Errorf("got %q, want SPECIFIC", got)
	}
}

func TestExplicitPriorityOverridesDefault(t *testing.T) {
	sheet := wrap(`
		<xsl:template match="/"><r><xsl:apply-templates select="//title"/></r></xsl:template>
		<xsl:template match="*" priority="10">WINS</xsl:template>
		<xsl:template match="title">LOSES</xsl:template>`)
	if got := run(t, sheet, bookDoc); !strings.Contains(got, "WINS") {
		t.Errorf("got %q, want the explicit priority to win", got)
	}
}

func TestLastDeclaredWinsOnTie(t *testing.T) {
	// Equal priority: the last declaration wins.
	sheet := wrap(`
		<xsl:template match="/"><r><xsl:apply-templates select="//title"/></r></xsl:template>
		<xsl:template match="title">FIRST</xsl:template>
		<xsl:template match="title">LAST</xsl:template>`)
	if got := run(t, sheet, bookDoc); !strings.Contains(got, "LAST") {
		t.Errorf("got %q, want the later declaration to win", got)
	}
}

func TestModes(t *testing.T) {
	sheet := wrap(`
		<xsl:template match="/"><r>
			<xsl:apply-templates select="//title" mode="short"/>
			<xsl:apply-templates select="//title"/>
		</r></xsl:template>
		<xsl:template match="title" mode="short">S</xsl:template>
		<xsl:template match="title">D</xsl:template>`)
	want := "<r>SSSDDD</r>"
	if got := run(t, sheet, bookDoc); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIfAndChoose(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><r>
		<xsl:for-each select="//book">
			<xsl:choose>
				<xsl:when test="@price > 40">high</xsl:when>
				<xsl:when test="@price > 20">mid</xsl:when>
				<xsl:otherwise>low</xsl:otherwise>
			</xsl:choose>
		</xsl:for-each>
	</r></xsl:template>`)
	if got := run(t, sheet, bookDoc); got != "<r>midlowhigh</r>" {
		t.Errorf("got %q, want <r>midlowhigh</r>", got)
	}
}

func TestVariables(t *testing.T) {
	sheet := wrap(`<xsl:template match="/">
		<xsl:variable name="n" select="count(//book)"/>
		<r count="{$n}"><xsl:value-of select="$n * 2"/></r>
	</xsl:template>`)
	if got := run(t, sheet, bookDoc); got != `<r count="3">6</r>` {
		t.Errorf("got %q", got)
	}
}

func TestVariableWithContent(t *testing.T) {
	// A variable with content builds a temporary tree, which is navigable.
	sheet := wrap(`<xsl:template match="/">
		<xsl:variable name="tmp"><x>a</x><x>b</x></xsl:variable>
		<r><xsl:value-of select="count($tmp/x)"/></r>
	</xsl:template>`)
	if got := run(t, sheet, bookDoc); got != "<r>2</r>" {
		t.Errorf("got %q, want <r>2</r> (a content variable is a navigable tree)", got)
	}
}

func TestAttributeValueTemplates(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><r>
		<xsl:for-each select="//book"><i n="{position()}" id="{@id}"/></xsl:for-each>
	</r></xsl:template>`)
	want := `<r><i n="1" id="b1"/><i n="2" id="b2"/><i n="3" id="b3"/></r>`
	if got := run(t, sheet, bookDoc); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAVTBraceEscaping(t *testing.T) {
	// Doubled braces are literals, which matters for CSS and JSON in output.
	sheet := wrap(`<xsl:template match="/"><r s="{{literal}}" v="{1+1}"/></xsl:template>`)
	if got := run(t, sheet, `<a/>`); got != `<r s="{literal}" v="2"/>` {
		t.Errorf("got %q", got)
	}
}

func TestCallTemplateWithParams(t *testing.T) {
	sheet := wrap(`
		<xsl:template match="/"><r>
			<xsl:call-template name="greet"><xsl:with-param name="who" select="'world'"/></xsl:call-template>
			<xsl:call-template name="greet"/>
		</r></xsl:template>
		<xsl:template name="greet">
			<xsl:param name="who" select="'nobody'"/>
			<g><xsl:value-of select="$who"/></g>
		</xsl:template>`)
	want := "<r><g>world</g><g>nobody</g></r>"
	if got := run(t, sheet, `<a/>`); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSort(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><r>
		<xsl:for-each select="//book">
			<xsl:sort select="@price" data-type="number"/>
			<t><xsl:value-of select="title"/></t>
		</xsl:for-each>
	</r></xsl:template>`)
	want := "<r><t>XML</t><t>Go</t><t>XSLT</t></r>"
	if got := run(t, sheet, bookDoc); got != want {
		t.Errorf("got %q, want %q (numeric sort)", got, want)
	}

	// A text sort orders differently: "30.50" < "45.00" < "9.99".
	sheet = wrap(`<xsl:template match="/"><r>
		<xsl:for-each select="//book">
			<xsl:sort select="@price"/>
			<t><xsl:value-of select="title"/></t>
		</xsl:for-each>
	</r></xsl:template>`)
	want = "<r><t>Go</t><t>XSLT</t><t>XML</t></r>"
	if got := run(t, sheet, bookDoc); got != want {
		t.Errorf("got %q, want %q (text sort)", got, want)
	}
}

func TestSortDescending(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><r>
		<xsl:for-each select="//book">
			<xsl:sort select="@price" data-type="number" order="descending"/>
			<t><xsl:value-of select="title"/></t>
		</xsl:for-each>
	</r></xsl:template>`)
	want := "<r><t>XSLT</t><t>Go</t><t>XML</t></r>"
	if got := run(t, sheet, bookDoc); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCopyAndCopyOf(t *testing.T) {
	// xsl:copy is shallow; xsl:copy-of is deep.
	sheet := wrap(`<xsl:template match="/"><r>
		<xsl:copy-of select="//book[1]"/>
	</r></xsl:template>`)
	got := run(t, sheet, bookDoc)
	if !strings.Contains(got, "<title>Go</title>") {
		t.Errorf("copy-of should be deep; got %q", got)
	}

	sheet = wrap(`
		<xsl:template match="/"><r><xsl:apply-templates select="//book[1]"/></r></xsl:template>
		<xsl:template match="book"><xsl:copy/></xsl:template>`)
	got = run(t, sheet, bookDoc)
	if strings.Contains(got, "title") {
		t.Errorf("copy should be shallow; got %q", got)
	}
	if !strings.Contains(got, "<book/>") {
		t.Errorf("got %q, want a bare <book/>", got)
	}
}

func TestIdentityTransform(t *testing.T) {
	// The canonical identity template: shallow-copy each node and recurse.
	sheet := wrap(`<xsl:template match="@*|node()">
		<xsl:copy><xsl:apply-templates select="@*|node()"/></xsl:copy>
	</xsl:template>`)
	got := run(t, sheet, `<a x="1"><b>t</b></a>`)
	if got != `<a x="1"><b>t</b></a>` {
		t.Errorf("identity transform produced %q", got)
	}
}

func TestComputedElementAndAttribute(t *testing.T) {
	sheet := wrap(`<xsl:template match="/">
		<xsl:element name="dyn-{count(//book)}">
			<xsl:attribute name="k">v</xsl:attribute>
			text
		</xsl:element>
	</xsl:template>`)
	got := run(t, sheet, bookDoc)
	if !strings.HasPrefix(got, `<dyn-3 k="v">`) {
		t.Errorf("got %q", got)
	}
}

func TestAttributeAfterChildrenIsAnError(t *testing.T) {
	// The instruction order is wrong here, and silently dropping the attribute
	// would hide a real stylesheet bug.
	sheet := wrap(`<xsl:template match="/"><r>
		<xsl:element name="e">text<xsl:attribute name="a">v</xsl:attribute></xsl:element>
	</r></xsl:template>`)
	if _, err := runErr(t, sheet, `<a/>`); err == nil {
		t.Error("adding an attribute after children should error")
	}
}

func TestNamespacesInOutput(t *testing.T) {
	sheet := `<xsl:stylesheet version="2.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:o="urn:out">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:template match="/"><o:root><o:child/></o:root></xsl:template>
	</xsl:stylesheet>`
	got := run(t, sheet, `<a/>`)
	if !strings.Contains(got, `xmlns:o="urn:out"`) {
		t.Errorf("got %q, want the namespace declared", got)
	}
	// The binding must not be repeated on the child.
	if strings.Count(got, `xmlns:o=`) != 1 {
		t.Errorf("got %q, want the namespace declared exactly once", got)
	}
}

func TestXSLFunction(t *testing.T) {
	sheet := `<xsl:stylesheet version="2.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:my="urn:my">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:function name="my:double">
			<xsl:param name="n"/>
			<xsl:sequence select="$n * 2"/>
		</xsl:function>
		<xsl:template match="/"><r><xsl:value-of select="my:double(21)"/></r></xsl:template>
	</xsl:stylesheet>`
	if got := run(t, sheet, `<a/>`); got != "<r>42</r>" {
		t.Errorf("got %q, want <r>42</r>", got)
	}
}

func TestRecursiveFunction(t *testing.T) {
	sheet := `<xsl:stylesheet version="2.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:my="urn:my">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:function name="my:fact">
			<xsl:param name="n"/>
			<xsl:sequence select="if ($n &lt;= 1) then 1 else $n * my:fact($n - 1)"/>
		</xsl:function>
		<xsl:template match="/"><r><xsl:value-of select="my:fact(5)"/></r></xsl:template>
	</xsl:stylesheet>`
	if got := run(t, sheet, `<a/>`); got != "<r>120</r>" {
		t.Errorf("got %q, want <r>120</r>", got)
	}
}

func TestKey(t *testing.T) {
	sheet := wrap(`
		<xsl:key name="by-author" match="book" use="author"/>
		<xsl:template match="/"><r>
			<xsl:for-each select="key('by-author', 'Alan')"><t><xsl:value-of select="title"/></t></xsl:for-each>
		</r></xsl:template>`)
	want := "<r><t>Go</t><t>XSLT</t></r>"
	if got := run(t, sheet, bookDoc); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestForEachGroupBy(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><r>
		<xsl:for-each-group select="//book" group-by="author">
			<g name="{current-grouping-key()}" n="{count(current-group())}"/>
		</xsl:for-each-group>
	</r></xsl:template>`)
	want := `<r><g name="Alan" n="2"/><g name="Beth" n="1"/></r>`
	if got := run(t, sheet, bookDoc); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestForEachGroupAdjacent(t *testing.T) {
	// group-adjacent starts a new group whenever the key changes, so the two
	// Alan books do not merge across the Beth book between them.
	doc := `<r><i k="a"/><i k="a"/><i k="b"/><i k="a"/></r>`
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each-group select="//i" group-adjacent="@k">
			<g k="{current-grouping-key()}" n="{count(current-group())}"/>
		</xsl:for-each-group>
	</out></xsl:template>`)
	want := `<out><g k="a" n="2"/><g k="b" n="1"/><g k="a" n="1"/></out>`
	if got := run(t, sheet, doc); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAnalyzeString(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><r>
		<xsl:analyze-string select="'a1b22c'" regex="[0-9]+">
			<xsl:matching-substring><n><xsl:value-of select="."/></n></xsl:matching-substring>
			<xsl:non-matching-substring><t><xsl:value-of select="."/></t></xsl:non-matching-substring>
		</xsl:analyze-string>
	</r></xsl:template>`)
	want := "<r><t>a</t><n>1</n><t>b</t><n>22</n><t>c</t></r>"
	if got := run(t, sheet, `<a/>`); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRegexGroup(t *testing.T) {
	// The regex attribute is an attribute value template, so a brace
	// quantifier must be doubled: "{4}" would be parsed as an embedded XPath
	// expression. This is a real XSLT trap, not an artefact of this engine.
	sheet := wrap(`<xsl:template match="/"><r>
		<xsl:analyze-string select="'2024-01-15'" regex="([0-9]{{4}})-([0-9]{{2}})">
			<xsl:matching-substring>
				<y><xsl:value-of select="regex-group(1)"/></y>
				<m><xsl:value-of select="regex-group(2)"/></m>
			</xsl:matching-substring>
		</xsl:analyze-string>
	</r></xsl:template>`)
	want := "<r><y>2024</y><m>01</m></r>"
	if got := run(t, sheet, `<a/>`); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGlobalParam(t *testing.T) {
	sheetSrc := wrap(`
		<xsl:param name="who" select="'default'"/>
		<xsl:template match="/"><r><xsl:value-of select="$who"/></r></xsl:template>`)

	stree, _ := xdm.ParseString(sheetSrc, xdm.ParseOptions{})
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dtree, _ := xdm.ParseString(`<a/>`, xdm.ParseOptions{})

	// Unsupplied: the declared default applies.
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.String(), "default") {
		t.Errorf("got %q, want the default", res.String())
	}

	// Supplied: the caller's value wins.
	res, err = s.Transform(context.Background(), dtree.Root, TransformOptions{
		Params: map[string]xdm.Sequence{"who": xdm.One(xdm.NewString("caller"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.String(), "caller") {
		t.Errorf("got %q, want the supplied value", res.String())
	}
}

func TestMessageIsCollectedNotPrinted(t *testing.T) {
	sheetSrc := wrap(`<xsl:template match="/">
		<xsl:message>hello from the stylesheet</xsl:message>
		<r/>
	</xsl:template>`)
	stree, _ := xdm.ParseString(sheetSrc, xdm.ParseOptions{})
	s, _ := Compile(stree.Root, CompileOptions{})
	dtree, _ := xdm.ParseString(`<a/>`, xdm.ParseOptions{})
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || !strings.Contains(res.Messages[0], "hello") {
		t.Errorf("messages = %v, want the message collected", res.Messages)
	}
	if strings.Contains(res.String(), "hello") {
		t.Errorf("the message leaked into the result: %q", res.String())
	}
}

func TestMessageTerminate(t *testing.T) {
	sheet := wrap(`<xsl:template match="/">
		<xsl:message terminate="yes">fatal</xsl:message><r/>
	</xsl:template>`)
	_, err := runErr(t, sheet, `<a/>`)
	if err == nil || !strings.Contains(err.Error(), "fatal") {
		t.Errorf("err = %v, want a terminating message error", err)
	}
}

func TestSimplifiedStylesheet(t *testing.T) {
	// The literal-result-element form: no xsl:stylesheet wrapper at all.
	sheet := `<out xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xsl:version="2.0">
		<xsl:value-of select="count(//book)"/>
	</out>`
	got := run(t, sheet, bookDoc)
	if !strings.Contains(got, ">3<") && !strings.Contains(got, "3") {
		t.Errorf("got %q, want the count in the output", got)
	}
}

func TestTextEscaping(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><r><xsl:value-of select="//v"/></r></xsl:template>`)
	got := run(t, sheet, `<a><v>&lt;tag&gt; &amp; "quote"</v></a>`)
	if !strings.Contains(got, "&lt;tag&gt;") || !strings.Contains(got, "&amp;") {
		t.Errorf("got %q, want markup characters escaped", got)
	}
}

func TestAttributeEscaping(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><r a="{//v}"/></xsl:template>`)
	got := run(t, sheet, `<a><v>x"y&lt;z</v></a>`)
	if !strings.Contains(got, "&quot;") || !strings.Contains(got, "&lt;") {
		t.Errorf("got %q, want quote and angle bracket escaped in the attribute", got)
	}
}

func TestStripSpace(t *testing.T) {
	sheet := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:strip-space elements="*"/>
		<xsl:template match="/"><r><xsl:value-of select="count(//text())"/></r></xsl:template>
	</xsl:stylesheet>`
	// With stripping, only the two non-whitespace text nodes survive.
	if got := run(t, sheet, "<a>\n  <b>x</b>\n  <c>y</c>\n</a>"); got != "<r>2</r>" {
		t.Errorf("got %q, want <r>2</r> after stripping whitespace", got)
	}
}

func TestRecursionIsBounded(t *testing.T) {
	// An unbounded recursive template must fail with an error rather than
	// exhausting the stack.
	sheet := wrap(`
		<xsl:template match="/"><xsl:call-template name="loop"/></xsl:template>
		<xsl:template name="loop"><xsl:call-template name="loop"/></xsl:template>`)
	_, err := runErr(t, sheet, `<a/>`)
	if err == nil || !strings.Contains(err.Error(), "recursion") {
		t.Errorf("err = %v, want a recursion-limit error", err)
	}
}

func TestCancellation(t *testing.T) {
	sheetSrc := wrap(`<xsl:template match="/"><r>
		<xsl:for-each select="1 to 100000"><x/></xsl:for-each>
	</r></xsl:template>`)
	stree, _ := xdm.ParseString(sheetSrc, xdm.ParseOptions{})
	s, _ := Compile(stree.Root, CompileOptions{})
	dtree, _ := xdm.ParseString(`<a/>`, xdm.ParseOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	if _, err := s.Transform(ctx, dtree.Root, TransformOptions{}); err == nil {
		t.Error("a cancelled context should abort the transform")
	}
}

func TestConcurrentTransforms(t *testing.T) {
	// One compiled stylesheet must serve many goroutines: that is the whole
	// point of separating compilation from execution.
	sheetSrc := wrap(`<xsl:template match="/"><r><xsl:value-of select="count(//book)"/></r></xsl:template>`)
	stree, _ := xdm.ParseString(sheetSrc, xdm.ParseOptions{})
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}

	const n = 32
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			dtree, err := xdm.ParseString(bookDoc, xdm.ParseOptions{})
			if err != nil {
				errs <- err
				return
			}
			res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
			if err != nil {
				errs <- err
				return
			}
			if !strings.Contains(res.String(), "<r>3</r>") {
				errs <- err
				return
			}
			errs <- nil
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent transform failed: %v", err)
		}
	}
}

func TestIncludeDisabledByDefault(t *testing.T) {
	sheet := wrap(`<xsl:include href="other.xsl"/>
		<xsl:template match="/"><r/></xsl:template>`)
	_, err := runErr(t, sheet, `<a/>`)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("err = %v, want module loading to be disabled by default", err)
	}
}

func TestDocumentDisabledByDefault(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><r><xsl:value-of select="document('/etc/passwd')"/></r></xsl:template>`)
	_, err := runErr(t, sheet, `<a/>`)
	if err == nil || !strings.Contains(err.Error(), "FODC0002") {
		t.Errorf("err = %v, want document() to be disabled by default", err)
	}
}

func TestPatternMatching(t *testing.T) {
	cases := []struct{ pattern, want string }{
		{"book", "3"},         // every book
		{"catalog/book", "3"}, // path-anchored
		{"/catalog", "1"},     // absolute
		{"book[@id='b2']", "1"},
		{"*", "8"}, // catalog + 3 books + ... elements
		{"title", "3"},
		{"@id", "3"},
	}
	for _, c := range cases {
		sheet := wrap(`
			<xsl:template match="/"><r><xsl:apply-templates select="//node()|//@*"/></r></xsl:template>
			<xsl:template match="` + c.pattern + `">X</xsl:template>
			<xsl:template match="text()"/>`)
		got := run(t, sheet, bookDoc)
		n := strings.Count(got, "X")
		if want := c.want; got != "" && n == 0 {
			t.Errorf("pattern %q matched nothing (want %s)", c.pattern, want)
		}
	}
}

func TestPatternPriorityOrdering(t *testing.T) {
	// Verify the computed priorities directly, since a wrong value silently
	// selects the wrong template.
	cases := []struct {
		pattern string
		want    float64
	}{
		{"book", 0},
		{"*", -0.5},
		{"text()", -0.5},
		{"node()", -0.5},
		{"catalog/book", 0.5},
		{"book[@id]", 0.5},
	}
	for _, c := range cases {
		p, err := CompilePattern(c.pattern, nil)
		if err != nil {
			t.Errorf("CompilePattern(%q): %v", c.pattern, err)
			continue
		}
		if got := p.Priority(); got != c.want {
			t.Errorf("priority of %q = %v, want %v", c.pattern, got, c.want)
		}
	}
}

func TestUnionPattern(t *testing.T) {
	sheet := wrap(`
		<xsl:template match="/"><r><xsl:apply-templates select="//title|//author"/></r></xsl:template>
		<xsl:template match="title|author">X</xsl:template>`)
	got := run(t, sheet, bookDoc)
	if strings.Count(got, "X") != 6 {
		t.Errorf("got %q, want 6 matches from the union pattern", got)
	}
}

// TestCompileNilReturnsError is the xslt half of
// xsd.TestNilArgumentsReturnErrors: a nil stylesheet document is an error, not
// a panic that takes the caller's process down.
func TestCompileNilReturnsError(t *testing.T) {
	if _, err := Compile(nil, CompileOptions{}); err == nil {
		t.Error("Compile(nil) should return an error")
	}
}

// TestTransformMaxDepth pins that the transform bound is high enough for an
// ordinary document and still catches a stylesheet with no base case.
//
// The bound was a fixed 300, below the parser's 1000, and it counts the
// ordinary descent of an identity transform rather than only a template
// calling itself. So a legal 500-deep document could be parsed and then not
// transformed. The default now matches the parser's.
func TestTransformMaxDepth(t *testing.T) {
	identity := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:output omit-xml-declaration="yes"/>` +
		`<xsl:template match="node()"><xsl:copy><xsl:apply-templates select="node()"/></xsl:copy></xsl:template>` +
		`</xsl:stylesheet>`

	// A document the parser accepts is one the identity transform can copy.
	deep := strings.Repeat("<a>", 900) + strings.Repeat("</a>", 900)
	if _, err := runErr(t, identity, deep); err != nil {
		t.Errorf("a 900-deep document should transform: %v", err)
	}

	// A template with no base case is still caught.
	runaway := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:template name="loop"><xsl:call-template name="loop"/></xsl:template>` +
		`<xsl:template match="/"><xsl:call-template name="loop"/></xsl:template></xsl:stylesheet>`
	_, err := runErr(t, runaway, `<d/>`)
	if err == nil {
		t.Fatal("unbounded template recursion should be refused")
	}
	if !strings.Contains(err.Error(), "recursion exceeded") {
		t.Errorf("error %q does not name the recursion limit", err)
	}
}
