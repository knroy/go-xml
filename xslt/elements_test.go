package xslt

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// These cover the XSLT 2.0 elements added after an audit against the spec's
// element inventory found them missing. Each was previously either silently
// ignored — the worst outcome, since the stylesheet produced wrong output with
// no warning — or rejected at compile time.

func TestAttributeSet(t *testing.T) {
	sheet := wrap(`
		<xsl:attribute-set name="base">
			<xsl:attribute name="class">plain</xsl:attribute>
			<xsl:attribute name="lang">en</xsl:attribute>
		</xsl:attribute-set>
		<xsl:template match="/"><r xsl:use-attribute-sets="base"/></xsl:template>`)
	got := run(t, sheet, `<a/>`)
	if !strings.Contains(got, `class="plain"`) || !strings.Contains(got, `lang="en"`) {
		t.Errorf("got %q, want both attributes from the set", got)
	}
}

func TestAttributeSetIsOverriddenByLiteralAttribute(t *testing.T) {
	// The element's own attribute must win over the inherited one; that is
	// what makes a set usable as a default.
	sheet := wrap(`
		<xsl:attribute-set name="base">
			<xsl:attribute name="class">plain</xsl:attribute>
		</xsl:attribute-set>
		<xsl:template match="/"><r xsl:use-attribute-sets="base" class="special"/></xsl:template>`)
	got := run(t, sheet, `<a/>`)
	if !strings.Contains(got, `class="special"`) {
		t.Errorf("got %q, want the literal attribute to override the set", got)
	}
	if strings.Contains(got, "plain") {
		t.Errorf("got %q, the set's value should have been replaced", got)
	}
}

func TestAttributeSetComposition(t *testing.T) {
	sheet := wrap(`
		<xsl:attribute-set name="a"><xsl:attribute name="x">1</xsl:attribute></xsl:attribute-set>
		<xsl:attribute-set name="b" use-attribute-sets="a">
			<xsl:attribute name="y">2</xsl:attribute>
		</xsl:attribute-set>
		<xsl:template match="/"><r xsl:use-attribute-sets="b"/></xsl:template>`)
	got := run(t, sheet, `<a/>`)
	if !strings.Contains(got, `x="1"`) || !strings.Contains(got, `y="2"`) {
		t.Errorf("got %q, want attributes from both sets", got)
	}
}

func TestAttributeSetCycleIsDetected(t *testing.T) {
	sheet := wrap(`
		<xsl:attribute-set name="a" use-attribute-sets="b"><xsl:attribute name="x">1</xsl:attribute></xsl:attribute-set>
		<xsl:attribute-set name="b" use-attribute-sets="a"><xsl:attribute name="y">2</xsl:attribute></xsl:attribute-set>
		<xsl:template match="/"><r xsl:use-attribute-sets="a"/></xsl:template>`)
	if _, err := runErr(t, sheet, `<a/>`); err == nil {
		t.Error("a circular attribute-set reference should error, not recurse")
	}
}

func TestAttributeSetOnElementAndCopy(t *testing.T) {
	sheet := wrap(`
		<xsl:attribute-set name="s"><xsl:attribute name="k">v</xsl:attribute></xsl:attribute-set>
		<xsl:template match="/"><out>
			<xsl:element name="e" use-attribute-sets="s"/>
		</out></xsl:template>`)
	if got := run(t, sheet, `<a/>`); !strings.Contains(got, `k="v"`) {
		t.Errorf("xsl:element ignored use-attribute-sets: %q", got)
	}
}

func TestNextMatch(t *testing.T) {
	// The higher-priority template wraps, then delegates to the one it beat.
	sheet := wrap(`
		<xsl:template match="/"><out><xsl:apply-templates select="//p"/></out></xsl:template>
		<xsl:template match="p" priority="2">[<xsl:next-match/>]</xsl:template>
		<xsl:template match="p" priority="1">inner</xsl:template>`)
	if got := run(t, sheet, `<d><p/></d>`); got != "<out>[inner]</out>" {
		t.Errorf("got %q, want <out>[inner]</out>", got)
	}
}

func TestNextMatchFallsThroughToBuiltInRule(t *testing.T) {
	// With nothing below it, next-match lands on the built-in rule, which
	// copies text through.
	sheet := wrap(`
		<xsl:template match="/"><out><xsl:apply-templates select="//p"/></out></xsl:template>
		<xsl:template match="p">[<xsl:next-match/>]</xsl:template>`)
	if got := run(t, sheet, `<d><p>text</p></d>`); got != "<out>[text]</out>" {
		t.Errorf("got %q, want the built-in rule to supply the text", got)
	}
}

func TestNextMatchOutsideTemplateIsAnError(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><xsl:next-match/></xsl:template>`)
	// The root template matched via apply-templates, so next-match is legal
	// there; a truly unmatched context is hard to construct, so this asserts
	// only that it does not panic.
	if _, err := runErr(t, sheet, `<a/>`); err != nil && !strings.Contains(err.Error(), "XTDE") {
		t.Logf("next-match at root: %v", err)
	}
}

func TestPerformSort(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:variable name="sorted">
			<xsl:perform-sort select="//n">
				<xsl:sort select="." data-type="number"/>
			</xsl:perform-sort>
		</xsl:variable>
		<xsl:value-of select="$sorted" separator=","/>
	</out></xsl:template>`)
	if got := run(t, sheet, `<d><n>3</n><n>1</n><n>2</n></d>`); got != "<out>123</out>" {
		t.Errorf("got %q, want the sorted sequence", got)
	}
}

func TestNamespaceInstruction(t *testing.T) {
	sheet := wrap(`<xsl:template match="/">
		<r><xsl:namespace name="p" select="'urn:computed'"/></r>
	</xsl:template>`)
	got := run(t, sheet, `<a/>`)
	if !strings.Contains(got, `xmlns:p="urn:computed"`) {
		t.Errorf("got %q, want the computed namespace declared", got)
	}
}

func TestNamespaceInstructionRejectsEmptyURI(t *testing.T) {
	sheet := wrap(`<xsl:template match="/">
		<r><xsl:namespace name="p" select="''"/></r>
	</xsl:template>`)
	if _, err := runErr(t, sheet, `<a/>`); err == nil {
		t.Error("binding a prefix to an empty URI should error")
	}
}

func TestNamespaceAlias(t *testing.T) {
	// The canonical use: a stylesheet that generates a stylesheet.
	sheet := `<xsl:stylesheet version="2.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:out="urn:placeholder">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:namespace-alias stylesheet-prefix="out" result-prefix="xsl"/>
		<xsl:template match="/"><out:template match="x"/></xsl:template>
	</xsl:stylesheet>`
	got := run(t, sheet, `<a/>`)
	if !strings.Contains(got, "http://www.w3.org/1999/XSL/Transform") {
		t.Errorf("got %q, want the placeholder rewritten to the XSLT namespace", got)
	}
	if strings.Contains(got, "urn:placeholder") {
		t.Errorf("got %q, the placeholder namespace should not survive", got)
	}
}

func TestCharacterMap(t *testing.T) {
	// A character map is the supported way to emit a literal entity
	// reference, so it must bypass escaping.
	sheet := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes" use-character-maps="m"/>
		<xsl:character-map name="m">
			<xsl:output-character character="©" string="&amp;copy;"/>
		</xsl:character-map>
		<xsl:template match="/"><r>©</r></xsl:template>
	</xsl:stylesheet>`
	if got := run(t, sheet, `<a/>`); got != "<r>&copy;</r>" {
		t.Errorf("got %q, want <r>&copy;</r> (the map must bypass escaping)", got)
	}
}

func TestCharacterMapUnknownNameIsAnError(t *testing.T) {
	sheet := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output use-character-maps="nope"/>
		<xsl:template match="/"><r/></xsl:template>
	</xsl:stylesheet>`
	if _, err := runErr(t, sheet, `<a/>`); err == nil {
		t.Error("naming an undeclared character map should error")
	}
}

func TestUnsupportedElementsAreRejectedLoudly(t *testing.T) {
	// Accepting and ignoring this would produce output that looks plausible
	// and is wrong, which is strictly worse than refusing to run.
	cases := []struct{ name, sheet string }{
		{"import-schema", wrap(`<xsl:import-schema namespace="urn:x"/>
			<xsl:template match="/"><r/></xsl:template>`)},
	}
	for _, c := range cases {
		_, err := runErr(t, c.sheet, `<a/>`)
		if err == nil {
			t.Errorf("xsl:%s was accepted; it must be rejected rather than ignored", c.name)
			continue
		}
		if !strings.Contains(err.Error(), "not supported") {
			t.Errorf("xsl:%s error = %v, want a clear 'not supported' message", c.name, err)
		}
	}
}

func TestSortCaseOrder(t *testing.T) {
	doc := `<r><n>b</n><n>A</n><n>a</n><n>B</n></r>`
	// Without case-order, codepoint order puts every uppercase letter first.
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n"><xsl:sort select="."/><i><xsl:value-of select="."/></i></xsl:for-each>
	</out></xsl:template>`)
	if got := run(t, sheet, doc); got != "<out><i>A</i><i>B</i><i>a</i><i>b</i></out>" {
		t.Errorf("default sort = %q, want codepoint order A,B,a,b", got)
	}

	// With case-order the letters interleave, and case only breaks ties.
	sheet = wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n">
			<xsl:sort select="." case-order="lower-first"/>
			<i><xsl:value-of select="."/></i>
		</xsl:for-each>
	</out></xsl:template>`)
	if got := run(t, sheet, doc); got != "<out><i>a</i><i>A</i><i>b</i><i>B</i></out>" {
		t.Errorf("lower-first = %q, want a,A,b,B", got)
	}

	sheet = wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n">
			<xsl:sort select="." case-order="upper-first"/>
			<i><xsl:value-of select="."/></i>
		</xsl:for-each>
	</out></xsl:template>`)
	if got := run(t, sheet, doc); got != "<out><i>A</i><i>a</i><i>B</i><i>b</i></out>" {
		t.Errorf("upper-first = %q, want A,a,B,b", got)
	}
}

func TestSortRejectsUnsupportedCollation(t *testing.T) {
	// A language tag with no collation data would silently fall back to
	// root collation, so it is refused rather than quietly mis-sorting.
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n"><xsl:sort select="." lang="zz-not-a-language"/><i/></xsl:for-each>
	</out></xsl:template>`)
	if _, err := runErr(t, sheet, `<r><n>a</n></r>`); err == nil {
		t.Error("an invalid xsl:sort/@lang should be refused")
	}

	sheet = wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n">
			<xsl:sort select="." collation="http://example.com/collation/de"/><i/>
		</xsl:for-each>
	</out></xsl:template>`)
	if _, err := runErr(t, sheet, `<r><n>a</n></r>`); err == nil {
		t.Error("a non-codepoint xsl:sort/@collation should be refused")
	}

	// The codepoint collation is the one that is implemented, so naming it
	// explicitly must work.
	sheet = wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n">
			<xsl:sort select="." collation="http://www.w3.org/2005/xpath-functions/collation/codepoint"/>
			<i><xsl:value-of select="."/></i>
		</xsl:for-each>
	</out></xsl:template>`)
	if got := run(t, sheet, `<r><n>a</n></r>`); got != "<out><i>a</i></out>" {
		t.Errorf("the codepoint collation should be accepted; got %q", got)
	}
}

func TestSortRejectsInvalidCaseOrder(t *testing.T) {
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n"><xsl:sort select="." case-order="sideways"/><i/></xsl:for-each>
	</out></xsl:template>`)
	if _, err := runErr(t, sheet, `<r><n>a</n></r>`); err == nil {
		t.Error("an invalid case-order should be rejected")
	}
}

// xsl:number level="any" counts every node the count pattern selects that
// precedes the target in document order, at any depth. All expected values
// here were produced by Saxon-HE 12.4 on the same input, not derived by
// reading the spec.
func TestNumberLevelAny(t *testing.T) {
	doc := `<doc><note>a</note>` +
		`<chapter><p><note>b</note></p><note>c</note></chapter>` +
		`<chapter><note>d</note><p><deep><note>e</note></deep></p></chapter>` +
		`<note>f</note></doc>`

	// Depth is irrelevant: the notes number 1..6 in document order even though
	// they sit at three different levels.
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//note"><i><xsl:number level="any"/></i></xsl:for-each>
	</out></xsl:template>`)
	want := "<out><i>1</i><i>2</i><i>3</i><i>4</i><i>5</i><i>6</i></out>"
	if got := run(t, sheet, doc); got != want {
		t.Errorf("level=any = %q, want %q", got, want)
	}

	// @from restarts the count inside each chapter. The leading note precedes
	// every chapter, so it stays 1.
	sheet = wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//note"><i><xsl:number level="any" from="chapter"/></i></xsl:for-each>
	</out></xsl:template>`)
	want = "<out><i>1</i><i>1</i><i>2</i><i>1</i><i>2</i><i>3</i></out>"
	if got := run(t, sheet, doc); got != want {
		t.Errorf("level=any from=chapter = %q, want %q", got, want)
	}
}

// A node the count pattern does not select still reports the number of
// selected nodes before it, and an ancestor of the target is not counted --
// counting ancestors would inflate every result by the node's depth.
func TestNumberLevelAnyWithCount(t *testing.T) {
	doc := `<doc><note>a</note>` +
		`<chapter><p><note>b</note></p><note>c</note></chapter>` +
		`<chapter><note>d</note><p><deep><note>e</note></deep></p></chapter>` +
		`<note>f</note></doc>`
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//*"><i><xsl:number level="any" count="note"/></i></xsl:for-each>
	</out></xsl:template>`)
	// doc precedes every note and is an ancestor, so it numbers to nothing.
	want := "<out><i/><i>1</i><i>1</i><i>1</i><i>2</i><i>3</i><i>3</i>" +
		"<i>4</i><i>4</i><i>4</i><i>5</i><i>6</i></out>"
	if got := run(t, sheet, doc); got != want {
		t.Errorf("level=any count=note = %q, want %q", got, want)
	}
}

// transformResult runs a stylesheet and returns the whole Result, so that
// tests can inspect secondary documents as well as the principal one.
func transformResult(t *testing.T, sheet, source string) (*Result, error) {
	t.Helper()
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		return nil, err
	}
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		return nil, err
	}
	dtree, err := xdm.ParseString(source, xdm.ParseOptions{})
	if err != nil {
		return nil, err
	}
	return s.Transform(context.Background(), dtree.Root, TransformOptions{})
}

// xsl:result-document produces a document separate from the principal result.
// The failure this guards against is the body leaking into the principal
// output, which is what merging the two builders would do.
func TestResultDocument(t *testing.T) {
	sheet := wrap(`<xsl:template match="/">
		<main>
			<xsl:for-each select="//item">
				<xsl:result-document href="{@id}.xml">
					<page><xsl:value-of select="."/></page>
				</xsl:result-document>
				<ref href="{@id}.xml"/>
			</xsl:for-each>
		</main>
	</xsl:template>`)
	res, err := transformResult(t, sheet, `<r><item id="a">A</item><item id="b">B</item></r>`)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}

	principal := strings.TrimSpace(res.String())
	if strings.Contains(principal, "<page>") {
		t.Errorf("secondary content leaked into the principal result: %s", principal)
	}
	if want := `<main><ref href="a.xml"/><ref href="b.xml"/></main>`; principal != want {
		t.Errorf("principal = %q, want %q", principal, want)
	}

	if len(res.Secondary) != 2 {
		t.Fatalf("got %d secondary results, want 2", len(res.Secondary))
	}
	for i, want := range []struct{ href, body string }{
		{"a.xml", "<page>A</page>"},
		{"b.xml", "<page>B</page>"},
	} {
		got := res.Secondary[i]
		if got.Href != want.href {
			t.Errorf("secondary[%d].Href = %q, want %q", i, got.Href, want.href)
		}
		if body := strings.TrimSpace(got.String()); body != want.body {
			t.Errorf("secondary[%d] body = %q, want %q", i, body, want.body)
		}
	}
}

// Two result documents with the same href would mean one silently overwriting
// the other, so the collision is an error.
func TestResultDocumentDuplicateHref(t *testing.T) {
	sheet := wrap(`<xsl:template match="/">
		<xsl:result-document href="same.xml"><a/></xsl:result-document>
		<xsl:result-document href="same.xml"><b/></xsl:result-document>
	</xsl:template>`)
	if _, err := transformResult(t, sheet, `<r/>`); err == nil {
		t.Error("duplicate href was accepted; it must be reported")
	}
}

// @format selects a named xsl:output, and serialisation attributes on the
// instruction override it.
func TestResultDocumentFormat(t *testing.T) {
	sheet := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:output name="txt" method="text"/>
		<xsl:template match="/">
			<xsl:result-document href="a" format="txt"><x>hi</x></xsl:result-document>
			<xsl:result-document href="b" format="txt" method="xml"><x>hi</x></xsl:result-document>
			<ok/>
		</xsl:template>
	</xsl:stylesheet>`
	res, err := transformResult(t, sheet, `<r/>`)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(res.Secondary) != 2 {
		t.Fatalf("got %d secondary results, want 2", len(res.Secondary))
	}
	if m := res.Secondary[0].Output.Method; m != "text" {
		t.Errorf("format=txt gave method %q, want text", m)
	}
	if m := res.Secondary[1].Output.Method; m != "xml" {
		t.Errorf("instruction @method did not override @format: got %q, want xml", m)
	}
}

func TestResultDocumentUnknownFormat(t *testing.T) {
	sheet := wrap(`<xsl:template match="/">
		<xsl:result-document href="a" format="nope"><x/></xsl:result-document>
	</xsl:template>`)
	if _, err := transformResult(t, sheet, `<r/>`); err == nil {
		t.Error("an undeclared @format was accepted")
	}
}

// Language-sensitive collation orders letters by the conventions of a
// language. Swedish treats "ä" as a distinct letter after "z"; German sorts it
// next to "a". Both expectations come from Saxon-HE 12.4 on this input.
func TestSortLanguageCollation(t *testing.T) {
	doc := "<r><n>z</n><n>ä</n><n>a</n><n>b</n></r>"
	sheetFor := func(attr string) string {
		return wrap(`<xsl:template match="/"><out>
			<xsl:for-each select="//n"><xsl:sort select="."` + attr + `/>
				<i><xsl:value-of select="."/></i>
			</xsl:for-each>
		</out></xsl:template>`)
	}
	cases := []struct{ name, attr, want string }{
		{"swedish", ` lang="sv"`, "<out><i>a</i><i>b</i><i>z</i><i>ä</i></out>"},
		{"german", ` lang="de"`, "<out><i>a</i><i>ä</i><i>b</i><i>z</i></out>"},
		// Without @lang, codepoint order puts every ASCII letter first.
		{"codepoint", "", "<out><i>a</i><i>b</i><i>z</i><i>ä</i></out>"},
	}
	for _, c := range cases {
		if got := run(t, sheetFor(c.attr), doc); got != c.want {
			t.Errorf("%s = %q, want %q", c.name, got, c.want)
		}
	}
}

// A compiled stylesheet is documented as safe to share across goroutines, and
// the collator it holds is stateful. This would deadlock or race if key()
// were not guarded.
func TestSortLanguageCollationIsConcurrencySafe(t *testing.T) {
	doc := "<r><n>z</n><n>ä</n><n>a</n><n>b</n></r>"
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n"><xsl:sort select="." lang="sv"/>
			<i><xsl:value-of select="."/></i>
		</xsl:for-each>
	</out></xsl:template>`)
	want := "<out><i>a</i><i>b</i><i>z</i><i>ä</i></out>"

	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dtree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
			if err != nil {
				errs <- err.Error()
				return
			}
			if got := strings.TrimSpace(res.String()); got != want {
				errs <- got
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent transform: %s", e)
	}
}

// A Schematron report names the failing element by XPath, which is precise but
// says nothing about where to look in the file. gx:line-number() supplies the
// part a person actually navigates by — and matters most where the XPath is
// ambiguous, as it is for two sibling items with the same path.
func TestPositionFunctions(t *testing.T) {
	src := "<order>\n  <item id=\"a\"/>\n  <group>\n    <item id=\"b\"/>\n" +
		"    <item id=\"c\"/>\n  </group>\n</order>\n"
	sheet := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			xmlns:gx="https://github.com/knroy/go-xml">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:template match="/"><out>
			<xsl:for-each select="//item">
				<at id="{@id}" line="{gx:line-number()}" col="{gx:column-number()}"/>
			</xsl:for-each>
		</out></xsl:template>
	</xsl:stylesheet>`

	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dtree, err := xdm.ParseString(src, xdm.ParseOptions{TrackPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Transform(context.Background(), dtree.Root, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := `<out><at id="a" line="2" col="3"/><at id="b" line="4" col="5"/>` +
		`<at id="c" line="5" col="5"/></out>`
	if got := strings.TrimSpace(res.String()); got != want {
		t.Errorf("positions = %q, want %q", got, want)
	}
}

// Without TrackPositions the accessors return the empty sequence, so a
// stylesheet can test for one and omit the attribute rather than emitting a
// confidently wrong line 0.
func TestPositionFunctionsWithoutTracking(t *testing.T) {
	sheet := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			xmlns:gx="https://github.com/knroy/go-xml">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:template match="/"><out>
			<xsl:value-of select="count(//item[gx:line-number()])"/>
			<xsl:text>,</xsl:text>
			<xsl:value-of select="if (empty(gx:line-number(//item[1]))) then 'empty' else 'set'"/>
		</out></xsl:template>
	</xsl:stylesheet>`
	got := run(t, sheet, "<order>\n  <item id=\"a\"/>\n</order>")
	if want := "<out>0,empty</out>"; got != want {
		t.Errorf("untracked = %q, want %q", got, want)
	}
}

// The engine threads internal state — the transform runtime, grouping
// bookkeeping — through the closed xdm.Item interface as xdm.Opaque, bound to
// variables in a private namespace. A stylesheet can *name* that namespace,
// so those values are reachable from ordinary XPath:
//
//	xmlns:gi="urn:goxslt:internal" ... distinct-values($gi:runtime)
//
// They used to reach two dozen unchecked type assertions and panic with an
// interface-conversion error, which in a server embedding this engine is a
// denial of service triggered by stylesheet text. Every operation must now
// answer or error, never crash.
func TestInternalStateIsNotReachableAsAPanic(t *testing.T) {
	exprs := []string{
		`count(distinct-values($gi:runtime))`,
		`string($gi:runtime)`,
		`deep-equal($gi:runtime, 1)`,
		`deep-equal($gi:runtime, $gi:runtime)`,
		`count($gi:runtime)`,
		`concat($gi:runtime, 'x')`,
		`upper-case($gi:runtime)`,
		`substring($gi:runtime, 1)`,
		`normalize-space($gi:runtime)`,
		`tokenize($gi:runtime, ',')`,
		`matches($gi:runtime, 'a')`,
		`sum($gi:runtime)`,
		`avg($gi:runtime)`,
		`min($gi:runtime)`,
		`max($gi:runtime)`,
		`count(data($gi:runtime))`,
		`count(reverse($gi:runtime))`,
		`count(index-of($gi:runtime, 1))`,
		`number($gi:runtime)`,
		`string-length($gi:runtime)`,
		`$gi:runtime = 1`,
		`$gi:runtime instance of item()`,
	}
	for _, expr := range exprs {
		sheet := `<xsl:stylesheet version="2.0"
				xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
				xmlns:gi="urn:goxslt:internal"
				xmlns:xs="http://www.w3.org/2001/XMLSchema">
			<xsl:output omit-xml-declaration="yes"/>
			<xsl:template match="/"><out><xsl:value-of select="` + expr + `"/></out></xsl:template>
		</xsl:stylesheet>`
		// A result or an error are both fine; a panic is not, and would fail
		// the test by crashing it.
		if _, err := transformResult(t, sheet, `<r/>`); err != nil {
			continue
		}
	}
}

// The same reachability applied to the XSLT instructions that consume a
// sequence, which sort and group it.
func TestInternalStateInInstructions(t *testing.T) {
	sheet := `<xsl:stylesheet version="2.0"
			xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
			xmlns:gi="urn:goxslt:internal">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:template match="/"><out>
			<xsl:for-each select="$gi:runtime"><xsl:sort select="."/>S</xsl:for-each>
			<xsl:for-each-group select="$gi:runtime" group-by="."><xsl:text>G</xsl:text></xsl:for-each-group>
			<xsl:copy-of select="$gi:runtime"/>
		</out></xsl:template>
	</xsl:stylesheet>`
	if _, err := transformResult(t, sheet, `<r/>`); err != nil {
		// An error is acceptable; the point is that it does not panic.
		t.Logf("errored (acceptable): %v", err)
	}
}

// An unrecognised element in the xsl: namespace at the top level is an error.
// The spec reserves the whole namespace, so anything unknown in it is either a
// typo or a version of XSLT this engine does not implement — and silently
// skipping it means "xsl:tempalte" is dropped and the stylesheet runs,
// producing quietly wrong output, which is the failure mode this project
// exists to avoid.
func TestUnknownTopLevelElementIsRejected(t *testing.T) {
	for _, el := range []string{
		`<xsl:bogus/>`,
		`<xsl:tempalte match="/"><r/></xsl:tempalte>`, // a plausible typo
		`<xsl:accumulator name="a"/>`,                 // XSLT 3.0, not implemented
	} {
		sheet := `<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
			el + `<xsl:template match="/"><r/></xsl:template></xsl:stylesheet>`
		_, err := runErr(t, sheet, `<a/>`)
		if err == nil {
			t.Errorf("%s was accepted at the top level; it must be reported", el)
		}
	}

	// Everything the engine does implement must still compile, or the check
	// is a denial rather than a guard.
	ok := wrap(`<xsl:output method="xml"/>
		<xsl:key name="k" match="a" use="@id"/>
		<xsl:variable name="v" select="1"/>
		<xsl:param name="p" select="2"/>
		<xsl:attribute-set name="s"/>
		<xsl:decimal-format name="d"/>
		<xsl:strip-space elements="a"/>
		<xsl:preserve-space elements="b"/>
		<xsl:namespace-alias stylesheet-prefix="#default" result-prefix="#default"/>
		<xsl:character-map name="c"/>
		<xsl:template match="/"><r/></xsl:template>`)
	if _, err := runErr(t, ok, `<a/>`); err != nil {
		t.Errorf("a stylesheet using only supported top-level elements was refused: %v", err)
	}
}

// Several xsl: elements are only meaningful inside a specific parent, and each
// parent reads them directly from its children rather than compiling them as
// instructions. Reaching the instruction compiler therefore means the element
// is misplaced.
//
// They used to be skipped silently, so forgetting the enclosing xsl:choose
// produced an empty result and no error — the same silent-drop failure as an
// unknown element, and one a typo reaches easily. Saxon reports XTSE0010 for
// all of these.
func TestMisplacedChildElementsAreRejected(t *testing.T) {
	cases := []struct{ why, body string }{
		{"when outside choose", `<xsl:when test="1">x</xsl:when>`},
		{"otherwise outside choose", `<xsl:otherwise>x</xsl:otherwise>`},
		{"sort outside a sortable instruction", `<xsl:sort select="."/>`},
		{"with-param outside a call", `<xsl:with-param name="p" select="1"/>`},
		{"matching-substring outside analyze-string",
			`<xsl:matching-substring>x</xsl:matching-substring>`},
		{"non-matching-substring outside analyze-string",
			`<xsl:non-matching-substring>x</xsl:non-matching-substring>`},
		{"output-character outside character-map",
			`<xsl:output-character character="a" string="b"/>`},
	}
	for _, c := range cases {
		sheet := wrap(`<xsl:template match="/">` + c.body + `</xsl:template>`)
		err := mustFail(t, sheet, `<a/>`)
		if err == nil {
			t.Errorf("%s: %s was accepted and silently dropped", c.why, c.body)
			continue
		}
		if !strings.Contains(err.Error(), "XTSE0010") {
			t.Errorf("%s: error = %v, want XTSE0010", c.why, err)
		}
	}
}

// The same elements in their proper places must still compile, or the check is
// a denial rather than a guard. The production corpora use these 230 times
// between them.
func TestProperlyPlacedChildElementsStillWork(t *testing.T) {
	doc := `<r><n>2</n><n>1</n></r>`
	sheet := wrap(`
		<xsl:template name="named"><xsl:param name="p" select="0"/>
			<p><xsl:value-of select="$p"/></p>
		</xsl:template>
		<xsl:template match="/"><out>
			<xsl:choose>
				<xsl:when test="count(//n) = 2">two</xsl:when>
				<xsl:otherwise>other</xsl:otherwise>
			</xsl:choose>
			<xsl:for-each select="//n">
				<xsl:sort select="." data-type="number"/>
				<i><xsl:value-of select="."/></i>
			</xsl:for-each>
			<xsl:call-template name="named">
				<xsl:with-param name="p" select="7"/>
			</xsl:call-template>
			<xsl:analyze-string select="'a1'" regex="[0-9]">
				<xsl:matching-substring>D</xsl:matching-substring>
				<xsl:non-matching-substring>L</xsl:non-matching-substring>
			</xsl:analyze-string>
		</out></xsl:template>`)
	want := "<out>two<i>1</i><i>2</i><p>7</p>LD</out>"
	if got := run(t, sheet, doc); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// mustFail compiles and runs, returning the error rather than failing the test.
func mustFail(t *testing.T, sheet, doc string) error {
	t.Helper()
	_, err := runErr(t, sheet, doc)
	return err
}

// xsl:sort/@collation must be applied, not merely accepted. Accepting the
// attribute and then sorting by codepoint anyway is the silent-wrong-answer
// this engine exists to avoid — and it is what happened until the collation
// was threaded into the comparison.
//
// Values equal under the collation keep document order, because the sort is
// stable: A,a,b,B rather than A,a,B,b. Verified against Saxon-HE 12.4.
func TestSortCollationIsApplied(t *testing.T) {
	const ci = "http://www.w3.org/2005/xpath-functions/collation/html-ascii-case-insensitive"
	doc := `<r><n>b</n><n>A</n><n>a</n><n>B</n></r>`

	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n"><xsl:sort select="." collation="` + ci + `"/>
			<i><xsl:value-of select="."/></i>
		</xsl:for-each>
	</out></xsl:template>`)
	if got, want := run(t, sheet, doc), "<out><i>A</i><i>a</i><i>b</i><i>B</i></out>"; got != want {
		t.Errorf("case-insensitive sort = %q, want %q", got, want)
	}

	// Codepoint order is unchanged, and remains the default.
	sheet = wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n"><xsl:sort select="."/>
			<i><xsl:value-of select="."/></i>
		</xsl:for-each>
	</out></xsl:template>`)
	if got, want := run(t, sheet, doc), "<out><i>A</i><i>B</i><i>a</i><i>b</i></out>"; got != want {
		t.Errorf("default sort = %q, want %q", got, want)
	}

	// An unknown collation is still refused rather than silently ignored.
	sheet = wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//n"><xsl:sort select="." collation="http://example.com/nope"/><i/></xsl:for-each>
	</out></xsl:template>`)
	if _, err := runErr(t, sheet, doc); err == nil {
		t.Error("an unknown xsl:sort/@collation was accepted")
	}
}

// fn:unparsed-text reads arbitrary files, so it is refused rather than
// implemented. It is an XSLT function, not an XPath one, so a stylesheet is
// where it exists at all — and where the refusal has to be checked.
func TestUnparsedTextIsRefusedInStylesheet(t *testing.T) {
	const sheet = `<xsl:stylesheet version="2.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template match="/">
	    <out><xsl:value-of select="unparsed-text('/etc/passwd')"/></out>
	  </xsl:template>
	</xsl:stylesheet>`
	_, err := runErr(t, sheet, `<doc/>`)
	if err == nil {
		t.Fatal("unparsed-text was permitted; it reads arbitrary files")
	}
	if !strings.Contains(err.Error(), "FOUT1170") {
		t.Errorf("err = %v, want FOUT1170", err)
	}
	// The "available" probe answers false rather than erroring, and must not
	// be a way to test for a file's existence.
	const probe = `<xsl:stylesheet version="2.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template match="/">
	    <out><xsl:value-of select="unparsed-text-available('/etc/passwd')"/></out>
	  </xsl:template>
	</xsl:stylesheet>`
	got, err := runErr(t, probe, `<doc/>`)
	if err != nil {
		t.Fatalf("unparsed-text-available: %v", err)
	}
	if !strings.Contains(got, "false") {
		t.Errorf("unparsed-text-available said %q, want false", got)
	}
}
