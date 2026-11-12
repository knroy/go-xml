package xslt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xsdstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xsd"
)

// TestStreamFinalErrors: a container whose streamed body fails with a
// family of finalCodes (or mergeFinalCodes, for xsl:merge) returns the
// stream's error with no re-run on the tree, and it is the tree's error.
// Each family is then dropped from its list (the sabotage): the container
// re-runs, which the case must see, so every entry is shown to be live.
//
// An input's invalidity (inputFinalCodes: XTTE1510 for validation="strict",
// XTTE1540 for type=) is final with the stream's first failure, said so
// (streamtrace.FirstFailure), where the tree's text lists every failure:
// the same error by code (streamtrace.SameError, XSLT 3.0 §2.14), whether
// the body succeeded or failed first (FOAR0001 at the first item), since the
// tree validates before its body runs.
func TestStreamFinalErrors(t *testing.T) {
	dir := t.TempDir()
	doc := `<doc><rec n="3" v="x" bad="1" p="zz:e" w="0" empty="" ns="http://www.w3.org/2000/xmlns/"/><rec n="1" v="y"/></doc>`
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	acc := `<xsl:accumulator name="a" initial-value="0" streamable="yes"><xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>`
	merge := func(key, action string) string {
		return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:template name="main"><top><xsl:merge>
<xsl:merge-source for-each-source="'doc.xml'" streamable="yes" select="doc/rec"><xsl:merge-key select="` + key + `"/></xsl:merge-source>
<xsl:merge-action>` + action + `</xsl:merge-action></xsl:merge></top></xsl:template>
<xsl:template name="n"><xsl:value-of select="current-merge-group()"/></xsl:template>
<xsl:function name="Q{urn:f}k"><xsl:sequence select="current-merge-key()"/></xsl:function>
</xsl:stylesheet>`
	}
	inDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(inDir, "doc.xml"), []byte(inputInvalidDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	inOK := `<v><xsl:value-of select="count(list/item)"/></v>`
	inBad := `<v><xsl:value-of select="sum(list/item/@n ! (xs:integer(.) idiv 0))"/></v>`
	for _, c := range []struct {
		code, sheet string
		merge, in   bool
	}{
		{"XPDY0050", coreSheet(`<xsl:value-of select="data(//rec/@v) treat as xs:integer*"/>`), false, false},
		{"XPTY0004", coreSheetWith(`<xsl:variable name="three" select="3"/>`, `<xsl:value-of select="count(//rec except $three)"/>`), false, false},
		{"FOTY0013", coreSheet(`<xsl:value-of select="data((//rec/@v, true#0))"/>`), false, false},
		{"FORG0001", coreSheet(`<xsl:value-of select="sum(//rec/xs:decimal(@v))"/>`), false, false},
		{"FORG0003", coreSheet(`<xsl:value-of select="string(zero-or-one(//rec/@n))"/>`), false, false},
		{"FORG0004", coreSheet(`<xsl:value-of select="count(one-or-more(//rec/@none))"/>`), false, false},
		{"FORG0005", coreSheet(`<xsl:value-of select="string(exactly-one(//rec/@n))"/>`), false, false},
		{"FORG0006", coreSheet(`<xsl:value-of select="sum(//rec/@v ! string(.))"/>`), false, false},
		{"FOCH0001", coreSheet(`<xsl:value-of select="codepoints-to-string(//rec/@w ! xs:integer(.))"/>`), false, false},
		{"FOCH0002", coreSheet(`<xsl:value-of select="distinct-values(//rec/@v, 'urn:no-such-collation')"/>`), false, false},
		{"FOAR0001", coreSheet(`<xsl:value-of select="sum(//rec/@n ! (xs:integer(.) idiv 0))"/>`), false, false},
		{"XTDE0820", coreSheet(`<xsl:for-each select="//rec"><xsl:element name="{@bad}"/></xsl:for-each>`), false, false},
		{"XTDE0830", coreSheet(`<xsl:for-each select="//rec"><xsl:element name="{@p}"/></xsl:for-each>`), false, false},
		{"XTDE0835", coreSheet(`<xsl:for-each select="//rec"><xsl:element name="e" namespace="{@ns}"/></xsl:for-each>`), false, false},
		{"XTDE0850", coreSheet(`<xsl:for-each select="//rec"><e><xsl:attribute name="{@bad}"/></e></xsl:for-each>`), false, false},
		{"XTDE0855", coreSheet(`<xsl:for-each select="//rec"><e><xsl:attribute name="xmlns{@empty}"/></e></xsl:for-each>`), false, false},
		{"XTDE0860", coreSheet(`<xsl:for-each select="//rec"><e><xsl:attribute name="{@p}"/></e></xsl:for-each>`), false, false},
		{"XTDE0865", coreSheet(`<xsl:for-each select="//rec"><e><xsl:attribute name="a" namespace="{@ns}"/></e></xsl:for-each>`), false, false},
		{"XTDE0930", coreSheet(`<xsl:for-each select="//rec"><e><xsl:namespace name="p" select="string(@empty)"/></e></xsl:for-each>`), false, false},
		{"XTDE0450", coreSheet(`<xsl:for-each select="//rec"><e><xsl:sequence select="true#0"/></e></xsl:for-each>`), false, false},
		{"XTTE3180", coreSheet(`<xsl:copy select="//rec/@n"/>`), false, false},
		{"XTTE0570", coreSheet(`<xsl:for-each select="//rec"><xsl:variable name="x" as="xs:integer" select="string(@v)"/><xsl:value-of select="$x"/></xsl:for-each>`), false, false},
		{"XTTE3375", coreSheet(`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:sequence select="//rec/@v/string()"/></xsl:map></xsl:variable><xsl:value-of select="map:size($m)" xmlns:map="http://www.w3.org/2005/xpath-functions/map"/>`), false, false},
		{"XTDE3365", coreSheet(`<xsl:variable name="m" as="map(*)"><xsl:map><xsl:for-each select="//rec"><xsl:map-entry key="'k'" select="1"/></xsl:for-each></xsl:map></xsl:variable><xsl:value-of select="map:size($m)" xmlns:map="http://www.w3.org/2005/xpath-functions/map"/>`), false, false},
		{"XTDE3362", strings.Replace(coreSheetWith(acc, `<xsl:for-each select="//rec"><xsl:value-of select="accumulator-before('a')"/></xsl:for-each>`),
			`href="doc.xml"`, `href="doc.xml" use-accumulators=""`, 1), false, false},
		{"XTDE2220", merge("xs:integer(@n)", `<g/>`), true, false},
		{"XTDE3480", merge(`xs:integer(@n)" order="descending`, `<g><xsl:call-template name="n"/></g>`), true, false},
		{"XTDE3510", merge(`xs:integer(@n)" order="descending`, `<g xmlns:f="urn:f"><xsl:value-of select="f:k()"/></g>`), true, false},
		{"XTTE1510", inSheet(`validation="strict"`, inOK), false, true},
		{"XTTE1510", inSheet(`validation="strict"`, inBad), false, true},
		{"XTTE1540", inSheet(`type="listT"`, inOK), false, true},
		{"XTTE1540", inSheet(`type="listT"`, inBad), false, true},
	} {
		t.Run(c.code, func(t *testing.T) {
			dir := dir
			if c.in {
				dir = inDir
			}
			_, te, _ := coreRun(c.sheet, dir, false, TransformOptions{})
			_, se, st := coreRun(c.sheet, dir, true, TransformOptions{})
			if xdm.ErrorCode(errors.New(te)) != c.code {
				t.Fatalf("tree: %q, want %s", te, c.code)
			}
			if c.in && (se == te || !strings.Contains(se, streamtrace.FirstFailure) || !streamtrace.SameError(se, te)) ||
				!c.in && se != te || st.Streamed() == 0 || st.Reruns != 0 {
				t.Fatalf("streamed %q (trace %+v); want the tree's %q, streamed, no re-run", se, st, te)
			}
			list := finalCodes
			switch {
			case c.merge:
				list = mergeFinalCodes
			case c.in:
				list = inputFinalCodes
			}
			delete(list, c.code)
			defer func() { list[c.code] = true }()
			if _, se, st = coreRun(c.sheet, dir, true, TransformOptions{}); se != te || st.Reruns != 1 {
				t.Errorf("with %s dropped: %q, %d re-runs; want the re-run's %q", c.code, se, st.Reruns, te)
			}
		})
	}
}

// TestStreamOpenAndTypeCodes: the streamed container's own errors carry the
// tree's codes. A document that cannot be opened is FODC0002, final (the
// tree's load opens the same file); a document invalid against type= is
// XTTE1540, as the tree's assess raises it, final too, with its first
// failure (TestStreamInputInvalidFinal).
func TestStreamOpenAndTypeCodes(t *testing.T) {
	dir := t.TempDir()
	sheet := coreSheet(`<xsl:value-of select="count(//rec)"/>`)
	_, te, _ := coreRun(sheet, dir, false, TransformOptions{})
	_, se, st := coreRun(sheet, dir, true, TransformOptions{})
	if xdm.ErrorCode(errors.New(te)) != "FODC0002" || xdm.ErrorCode(errors.New(se)) != "FODC0002" || st.Reruns != 0 {
		t.Errorf("missing document: streamed %q (%d re-runs), tree %q; want FODC0002 with no re-run", se, st.Reruns, te)
	}
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(inDocs["bad-value"]), 0o600); err != nil {
		t.Fatal(err)
	}
	sheet = inSheet(`type="listT"`, `<v><xsl:value-of select="count(list/item)"/></v>`)
	_, te, _ = coreRun(sheet, dir, false, TransformOptions{})
	_, se, st = coreRun(sheet, dir, true, TransformOptions{})
	if xdm.ErrorCode(errors.New(te)) != "XTTE1540" || xdm.ErrorCode(errors.New(se)) != "XTTE1540" || st.Reruns != 0 {
		t.Errorf("type=: streamed %q (%d re-runs), tree %q; want XTTE1540, final", se, st.Reruns, te)
	}
}

// TestStreamInputInvalidReruns: a streamed input whose first failure does
// not tell the tree's code re-runs, the tree's error then given: under
// validation="lax" (XTTE1515, or XTTE1510 when every failure is an
// unresolvable xsi:type) and a first failure of an ID's under "strict"
// (XTTE1555 when every failure is one).
func TestStreamInputInvalidReruns(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ code, attrs, doc string }{
		{"XTTE1515", `validation="lax"`, inputInvalidDoc},
		{"XTTE1555", `validation="strict"`, `<list><item id="a" n="1"><price>1</price></item><item id="a" n="2"><price>1</price></item></list>`},
	} {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(c.doc), 0o600); err != nil {
			t.Fatal(err)
		}
		sheet := inSheet(c.attrs, `<v><xsl:value-of select="count(list/item)"/></v>`)
		_, te, _ := coreRun(sheet, dir, false, TransformOptions{})
		_, se, st := coreRun(sheet, dir, true, TransformOptions{})
		if xdm.ErrorCode(errors.New(te)) != c.code || se != te || st.Streamed() == 0 || st.Reruns != 1 {
			t.Errorf("%s: streamed %q (trace %+v), tree %q; want the re-run's %s", c.attrs, se, st, te, c.code)
		}
	}
}

// inputInvalidDoc is invalid against inSchema twice over: the tree lists
// both failures, the stream its first.
const inputInvalidDoc = `<list><item n="1"><price>4.5x</price></item><item><price>1</price></item></list>`

// TestStreamGroundLaterErrorFinal: a final error raised in what a
// GroundLater copy feeds (si-map-007: an xsl:map with a duplicate key, in a
// for-each over grounded records, as xsl:map or a map constructor, which
// sx-MapExpr-007 has; stream-106's copy-of validated strict, invalid) no
// longer stops the parse, so settle reads the rest of the document and the
// error is the tree's, with no re-run.
func TestStreamGroundLaterErrorFinal(t *testing.T) {
	dir, inDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(`<doc><rec><sub>a</sub><t>1</t></rec><rec><sub>b</sub><t>2</t></rec><rec/></doc>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inDir, "doc.xml"), []byte(`<list><item n="1"><price>1</price></item><item n="2"><price>x</price></item><item n="3"><price>3</price></item></list>`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sheet := range []string{
		coreSheetWith(`<xsl:param name="a" select="'k'"/>`, `<xsl:for-each select="doc/rec"><xsl:variable name="m" as="map(*)"><xsl:map>`+
			`<xsl:map-entry key="'k'" select="string(sub)"/><xsl:map-entry key="$a" select="string(t)"/></xsl:map></xsl:variable>`+
			`<r v="{$m('k')}"/></xsl:for-each>`),
		coreSheetWith(`<xsl:param name="a" select="'k'"/>`, `<xsl:for-each select="doc/rec"><xsl:variable name="m" as="map(*)" select="map{'k': string(sub), $a: string(t)}"/>`+
			`<r v="{$m('k')}"/></xsl:for-each>`),
		inSheet("", `<o><xsl:copy-of select="list/item[2]" validation="strict"/></o>`),
	} {
		dir := dir
		if strings.Contains(sheet, "import-schema") {
			dir = inDir
		}
		_, te, _ := coreRun(sheet, dir, false, TransformOptions{})
		_, se, st := coreRun(sheet, dir, true, TransformOptions{})
		if code := xdm.ErrorCode(errors.New(te)); !finalCodes[code] && !strings.HasPrefix(code, "XTTE15") || se != te || st.Streamed() == 0 || st.Reruns != 0 {
			t.Errorf("streamed %q (trace %+v), tree %q; want the tree's final error, streamed, no re-run", se, st, te)
		}
	}
}

// TestStreamInputInvalidMarkedFinal: a document the harness preloads and
// marks (xsdstream.MarkSource) although its validation failed (annotate
// keeps the annotations up to the failure and reports nothing), read by an
// xsl:source-document validation="strict": the stream's first validation,
// the harness's, fails, and so does the stylesheet's, whose error is the
// tree's (streamable-149). It is final with its first failure; with
// XTTE1510 dropped from inputFinalCodes, the container re-runs.
func TestStreamInputInvalidMarkedFinal(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir()) // the resolver's key: see TestStreamAsReadMarked
	if err != nil {
		t.Fatal(err)
	}
	path, doc := filepath.Join(dir, "doc.xml"), `<doc><v>x</v><v>10</v><v>y</v></doc>`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	xsdText := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="doc"><xs:complexType><xs:sequence>` +
		`<xs:element name="v" type="xs:integer" maxOccurs="unbounded"/></xs:sequence></xs:complexType></xs:element></xs:schema>`
	st, err := xdm.ParseString(xsdText, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := xsd.Load(st.Root, "s.xsd", xsd.Options{})
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
		`<xsl:import-schema>`+xsdText+`</xsl:import-schema><xsl:template name="main"><out>`+
		`<xsl:source-document streamable="yes" href="doc.xml" validation="strict"><n><xsl:value-of select="count(doc/v)"/></n></xsl:source-document>`+
		`</out></xsl:template></xsl:stylesheet>`, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(sheet.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	run := func(core bool) (string, streamtrace.Counts) {
		before := streamtrace.Snapshot()
		docs, err := NewFileResolver(dir)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := xdm.ParseString(doc, xdm.ParseOptions{BaseURI: fileURIOf(path), DocumentURI: fileURIOf(path)})
		if err != nil {
			t.Fatal(err)
		}
		typed, _ := schema.ValidateCopy(parsed.Root, xsd.ValidateOptions{}) // as the harness's annotate
		docs.Preload(fileURIOf(path), typed.Tree())
		xsdstream.MarkSource(docs, fileURIOf(path), schema)
		noBursts = !core
		defer func() { noBursts = false }()
		_, err = s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
		if err == nil {
			t.Fatal("no error")
		}
		return err.Error(), streamtrace.Snapshot().Sub(before)
	}
	te, _ := run(false)
	se, st2 := run(true)
	if xdm.ErrorCode(errors.New(te)) != "XTTE1510" || !strings.Contains(se, streamtrace.FirstFailure) || !streamtrace.SameError(se, te) ||
		st2.Streamed() == 0 || st2.Reruns != 0 {
		t.Fatalf("streamed %q (trace %+v), tree %q; want the tree's XTTE1510, its first failure, no re-run", se, st2, te)
	}
	delete(inputFinalCodes, "XTTE1510")
	defer func() { inputFinalCodes["XTTE1510"] = true }()
	if se, st2 = run(true); se != te || st2.Reruns != 1 {
		t.Errorf("with XTTE1510 dropped: %q, %d re-runs; want the re-run's %q", se, st2.Reruns, te)
	}
}
