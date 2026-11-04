package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xsd"
)

// The input side of validation on the stream (streamvalidin.go): an
// xsl:source-document with validation or type streams, and what the body
// reads -- typed values, annotations, default attributes, copies -- and the
// error an invalid document raises are the tree's. Each body is chosen so
// that the untyped document would give another answer.

const inSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:element name="list" type="listT"/>
 <xs:complexType name="listT"><xs:sequence>
   <xs:element ref="item" maxOccurs="unbounded"/><xs:element name="end" type="xs:date" minOccurs="0"/>
   <xs:element name="key" type="xs:ID" minOccurs="0" maxOccurs="unbounded"/>
 </xs:sequence><xs:assert test="count(item) le 50"/></xs:complexType>
 <xs:element name="item"><xs:complexType><xs:sequence>
   <xs:element name="price" type="money"/>
   <xs:element name="dims" type="dims" minOccurs="0"/>
   <xs:element name="note" type="xs:string" nillable="true" minOccurs="0"/>
  </xs:sequence>
  <xs:attribute name="id" type="xs:ID"/>
  <xs:attribute name="ref" type="xs:IDREF"/>
  <xs:attribute name="rate" type="xs:decimal" default="1.50"/>
  <xs:attribute name="n" type="xs:integer" use="required"/></xs:complexType></xs:element>
 <xs:complexType name="money"><xs:simpleContent><xs:extension base="xs:decimal">
   <xs:attribute name="cur" type="xs:token"/></xs:extension></xs:simpleContent></xs:complexType>
 <xs:simpleType name="dims"><xs:list itemType="xs:decimal"/></xs:simpleType>
</xs:schema>`

var inDocs = map[string]string{
	"ok": "<list>\n <item id=\"a\" n=\"1\"><price cur=\"USD\">4.50</price><dims>1 2.0 3</dims></item>\n" +
		" <item ref=\"a\" n=\"2\" rate=\"2.00\"><price>10</price><note xsi:nil=\"true\" xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\"/></item>\n" +
		strings.Repeat(" <item n=\"3\"><price>0.1</price></item>\n", 30) + " <end>2020-02-29</end>\n</list>",
	"bad-value":  `<list><item n="1"><price>4.5x</price></item></list>`,
	"bad-order":  `<list><item n="1"><dims>1</dims><price>1</price></item></list>`,
	"bad-ref":    `<list><item n="1" ref="nope"><price>1</price></item></list>`,
	"bad-late":   "<list>" + strings.Repeat(`<item n="1"><price>1</price></item>`, 20) + `<item><price>1</price></item></list>`,
	"bad-assert": "<list>" + strings.Repeat(`<item n="1"><price>1</price></item>`, 51) + `</list>`,
	// Valid to XSD 1.1, whose owner of an ID in content is the parent; not
	// to XSLT's document-level check (XTTE1555), whose owner is the element.
	"bad-ids": `<list><item n="1"><price>1</price></item><key>k</key><key>k</key></list>`,
}

func inSheet(attrs, body string) string { return inSheetWith("", attrs, body) }

// inSheetWith is inSheet with sheet's attributes on xsl:stylesheet.
func inSheetWith(sheet, attrs, body string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs" ` + sheet + `>
<xsl:import-schema>` + inSchema + `</xsl:import-schema>
<xsl:mode name="m" streamable="yes"/>
<xsl:template match="schema-element(item)" mode="m"><s n="{@n}" r="{data(@rate)}"/></xsl:template>
<xsl:template match="element(*, money)" mode="m"><p><xsl:value-of select="data(.) * 2"/></p></xsl:template>
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml" ` + attrs + `>` + body +
		`</xsl:source-document></top></xsl:template></xsl:stylesheet>`
}

func TestStreamInputValidation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(inDocs["ok"]), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, e, _ := coreRun(inSheet(`validation="strict"`, `<v/>`), dir, true, TransformOptions{}); e != "" {
		t.Fatalf("the sheet: %s", e)
	}
	bodies := []string{
		`<v><xsl:value-of select="sum(list/item/price)"/></v>`,
		`<v><xsl:value-of select="avg(list/item/price)"/></v>`,
		`<xsl:for-each select="list/item"><i r="{data(@rate)}" n="{@n + 1}" t="{. instance of element(item)}"/></xsl:for-each>`,
		`<o><xsl:copy-of select="list/item[2]"/></o>`,
		`<v><xsl:value-of select="count(list/item/dims/data())"/></v>`,
		`<v><xsl:value-of select="list/end/data() + xs:dayTimeDuration('P1D')"/></v>`,
		`<xsl:apply-templates select="list/item" mode="m"/>`,
		`<xsl:apply-templates mode="m"/>`,
		`<v><xsl:value-of select="count(list/text())"/></v>`,
		`<xsl:for-each select="list/item/note"><n v="{nilled(.)}"/></xsl:for-each>`,
		`<v><xsl:value-of select="head(list/item/price) instance of element(*, money)"/></v>`,
	}
	for _, attrs := range []string{`validation="strict"`, `type="listT"`, `validation="lax"`} {
		for name, doc := range inDocs {
			if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, body := range bodies {
				coreCheck(t, inSheet(attrs, body), dir, attrs+" "+name+": "+body, true, TransformOptions{})
			}
		}
	}
	// input-type-annotations="strip": validated (an invalid document is
	// still an error), then the annotations go, and xsi:nil with them.
	for name, doc := range inDocs {
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{bodies[0], bodies[2], bodies[9],
			`<xsl:for-each select="list/item/note"><n a="{count(@*)}" t="{. instance of element(*, xs:untyped)}"/></xsl:for-each>`,
			`<o><xsl:copy-of select="list/item[2]"/></o>`,
		} {
			coreCheck(t, inSheetWith(`input-type-annotations="strip"`, `validation="strict"`, body), dir,
				"strip "+name+": "+body, true, TransformOptions{})
		}
	}
	// A DOCTYPE: the tree's copy has no DTD (an xs:ENTITY value is then
	// invalid), so such a document is the tree's, decided before anything
	// is read.
	dtd := "<!DOCTYPE list [<!ENTITY e \"4.5\">]>\n" + inDocs["ok"]
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(dtd), 0o600); err != nil {
		t.Fatal(err)
	}
	coreCheck(t, inSheet(`validation="strict"`, bodies[0]), dir, "DOCTYPE: "+bodies[0], false, TransformOptions{})
	coreCheck(t, coreSheet(bodies[0]), dir, "DOCTYPE, not validated: "+bodies[0], true, TransformOptions{})
	// No schema needed: the copy as read, untyped, without its document URI.
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(inDocs["ok"]), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, attrs := range []string{`validation="preserve"`, `validation="strip"`} {
		for _, b := range []struct {
			body   string
			stream bool
		}{
			{`<v><xsl:value-of select="sum(list/item/price)"/></v>`, true},
			{`<xsl:for-each select="list/item"><i r="{data(@rate)}" n="{@n + 1}"/></xsl:for-each>`, true},
			// What the copy changes, its document URI, no streamed body may
			// read: the planner refuses these with or without validation.
			{`<v d="{document-uri(/)}" b="{ends-with(base-uri(/), 'doc.xml')}"/>`, false},
			{`<xsl:for-each select="list/item"><i d="{document-uri(root(.))}"/></xsl:for-each>`, false},
		} {
			coreCheck(t, coreSheet(b.body), dir, "plain: "+b.body, b.stream, TransformOptions{})
			coreCheck(t, inSheet(attrs, b.body), dir, attrs+": "+b.body, b.stream, TransformOptions{})
		}
	}
}

// TestStreamMergeValidation: streamed merge sources with validation give the
// tree's groups, typed items and errors; a path key on a validated source,
// whose type the stream cannot tell from how it is written, is the tree's.
func TestStreamMergeValidation(t *testing.T) {
	dir := t.TempDir()
	write := func(name, doc string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.xml", `<list><item n="1"><price>1.10</price></item><item n="3" rate="3.0"><price>2</price></item></list>`)
	write("b.xml", `<list><item n="2"><price>0.20</price></item><item n="3"><price>4</price></item></list>`)
	// Invalid where the action reads nothing: only the validation finds it.
	write("bad.xml", `<list><item n="2"><price>0.20</price></item><item n="4"><price>4</price><zzz/></item></list>`)
	sheet := func(docs, valid, key, action string) string {
		return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">
<xsl:import-schema>` + inSchema + `</xsl:import-schema>
<xsl:template name="main"><top><xsl:merge>
 <xsl:merge-source for-each-source="` + docs + `" select="list/item" streamable="yes" ` + valid + `>
  <xsl:merge-key select="` + key + `"/></xsl:merge-source>
 <xsl:merge-action>` + action + `</xsl:merge-action>
</xsl:merge></top></xsl:template></xsl:stylesheet>`
	}
	action := `<g k="{current-merge-key()}" s="{sum(current-merge-group()/price)}" r="{current-merge-group()/data(@rate)}"
	   t="{current-merge-group() instance of schema-element(item)+}"/>`
	for _, r := range []struct {
		docs, valid, key string
		stream           bool
	}{
		{"'a.xml', 'b.xml'", `validation="strict"`, "xs:integer(@n)", true},
		{"'a.xml', 'b.xml'", `type="listT"`, "xs:integer(@n)", true},
		{"'a.xml', 'bad.xml'", `validation="strict"`, "xs:integer(@n)", true},
		{"'a.xml', 'b.xml'", `validation="strict"`, "@n", false},
		{"'a.xml', 'b.xml'", ``, "@n", true},
	} {
		where := r.docs + " " + r.valid + " " + r.key
		so, se, st := coreRun(sheet(r.docs, r.valid, r.key, action), dir, true, TransformOptions{})
		to, te, _ := coreRun(sheet(r.docs, r.valid, r.key, action), dir, false, TransformOptions{})
		if so != to || se != te || st.Guards != 0 {
			t.Errorf("%s:\nstream %q %q (%d guard hits)\ntree   %q %q", where, so, se, st.Guards, to, te)
		}
		if strings.HasPrefix(se, "COMPILE") || strings.HasPrefix(se, "SHEET") {
			t.Errorf("%s: %s", where, se)
		}
		if merged := st.Runs[streamtrace.Merge] > 0; merged != r.stream {
			t.Errorf("%s: streamed %v, want %v", where, merged, r.stream)
		}
		if st.Reruns > 0 && te == "" {
			t.Errorf("%s: re-run where the tree succeeds", where)
		}
	}
}

// TestStreamMainValidation: TransformOptions.SourceValidation. TransformReader
// streams the validated principal input and gives what Transform gives with
// the field over the parsed document -- outputs and errors alike -- and, for
// a valid document, what Parse, xsd ValidateCopy and Transform give: ignorable
// whitespace dropped, xsl:strip-space after the validation (sparing simple
// content), typed values, defaults. A schema the stream cannot validate (a
// content model whose count readings may outgrow the budget) takes the tree
// path; strict with no schema is XTSE1660.
func TestStreamMainValidation(t *testing.T) {
	// More count readings than the budget on a stream (a bounded maximum
	// above the child count is unbounded only to the tree): the tree's.
	bounded := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r"><xs:complexType>
  <xs:sequence maxOccurs="100"><xs:element name="price" maxOccurs="100" type="xs:decimal"/></xs:sequence></xs:complexType></xs:element></xs:schema>`
	boundedDoc := "<r>" + strings.Repeat("<price>1</price>", 99) + "</r>"
	keyed := strings.Replace(inSchema, `<xs:element name="list" type="listT"/>`,
		`<xs:element name="list" type="listT"><xs:unique name="U"><xs:selector xpath="item"/><xs:field xpath="@id"/></xs:unique></xs:element>`, 1)
	sheet := func(schema, decl, rules string) string {
		imp := ""
		if schema != "" {
			imp = `<xsl:import-schema>` + schema + `</xsl:import-schema>`
		}
		return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">` + imp + decl + `
<xsl:mode streamable="yes" on-no-match="shallow-copy"/>` + rules + `</xsl:stylesheet>`
	}
	// how: "reader" TransformReader, "field" Parse + Transform with the
	// field, "manual" Parse + xsd ValidateCopy + Transform.
	run := func(src, doc, how string, v SourceValidation) (string, streamtrace.Counts) {
		before := streamtrace.Snapshot()
		tree, err := xdm.ParseString(src, xdm.ParseOptions{BaseURI: "file:///s/s.xsl"})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(tree.Root, CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		opts := TransformOptions{SourceValidation: v}
		var res *Result
		switch how {
		case "reader":
			res, err = s.TransformReader(context.Background(), readOnly{strings.NewReader(doc)}, opts)
		default:
			var in *xdm.Tree
			if in, err = xdm.Parse(strings.NewReader(doc), xdm.ParseOptions{}); err == nil {
				root := in.Root
				if how == "manual" {
					opts.SourceValidation = 0
					root, err = s.Schema().ValidateCopy(in.Root, xsd.ValidateOptions{})
				}
				if err == nil {
					res, err = s.Transform(context.Background(), root, opts)
				}
			}
		}
		if err != nil {
			return "error: " + err.Error(), streamtrace.Snapshot().Sub(before)
		}
		return res.String(), streamtrace.Snapshot().Sub(before)
	}
	spaced := "<list>\n <item n=\"1\">\n  <price> 4.50 </price>\n  <note> </note>\n </item>\n" +
		" <item n=\"2\"><price>0.1</price><dims> 1 2 </dims><note>\n</note></item>\n</list>"
	rules := []string{
		``,
		`<xsl:template match="price"><p v="{data(.) * 2}"/></xsl:template>`,
		`<xsl:template match="price"><p s="[{.}]"/></xsl:template>`,
		`<xsl:template match="item"><i r="{data(@rate)}" c="{count(node())}"/></xsl:template>`,
		`<xsl:template match="note"><n c="{count(node())}" nil="{nilled(.)}"/></xsl:template>`,
		`<xsl:template match="/"><o><xsl:value-of select="sum(list/item/price)"/></o></xsl:template>`,
	}
	docs := map[string]string{"spaced": spaced, "ok": inDocs["ok"], "bad": inDocs["bad-order"],
		"bad-ref": inDocs["bad-ref"], "bad-assert": inDocs["bad-assert"], "undeclared": `<other><x>1</x></other>`,
		// Two failures, the tree meeting the text before the child's: the
		// error is the stream's either way (sourceInvalid).
		"bad-two": `<list><item n="1"><price>x</price></item>junk</list>`}
	for _, v := range []SourceValidation{SourceStrict, SourceLax} {
		for _, decl := range []string{"", `<xsl:strip-space elements="*"/>`, `<xsl:strip-space elements="note price"/>`} {
			for _, r := range rules {
				for name, doc := range docs {
					where := fmt.Sprintf("%v %s %s %s", v, name, decl, r)
					src := sheet(inSchema, decl, r)
					got, tr := run(src, doc, "reader", v)
					want, _ := run(src, doc, "field", v)
					if got != want {
						t.Errorf("%s:\nreader %q\nfield  %q", where, got, want)
					}
					invalid := strings.HasPrefix(name, "bad") || name == "undeclared" && v == SourceStrict
					if strings.HasPrefix(want, "error") != invalid {
						t.Errorf("%s: %q", where, want)
					}
					if !invalid {
						if man, _ := run(src, doc, "manual", v); man != want && name != "undeclared" {
							t.Errorf("%s:\nfield  %q\nmanual %q", where, want, man)
						}
					}
					if tr.Runs[streamtrace.Main] == 0 {
						t.Errorf("%s: not streamed", where)
					}
				}
			}
		}
	}
	// The codes: strict XTTE1510, lax XTTE1515; no schema; a schema the
	// stream refuses.
	for _, r := range []struct {
		schema, doc string
		v           SourceValidation
		code        string
		stream      bool
	}{
		{inSchema, inDocs["bad-ref"], SourceStrict, "XTTE1510", true},
		{inSchema, inDocs["bad-ref"], SourceLax, "XTTE1515", true},
		{"", inDocs["ok"], SourceStrict, "XTSE1660", false},
		{"", inDocs["ok"], SourceLax, "", true},
		{bounded, boundedDoc, SourceStrict, "", false},
		{keyed, inDocs["ok"], SourceStrict, "", true},
		{keyed, strings.Replace(inDocs["ok"], `<item ref="a"`, `<item id="b" ref="a"`, 1), SourceStrict, "", true},
		{keyed, strings.Replace(inDocs["ok"], `<item n="3">`, `<item n="3" id="z">`, -1), SourceStrict, "XTTE1510", true},
	} {
		src := sheet(r.schema, "", rules[1])
		got, tr := run(src, r.doc, "reader", r.v)
		want, _ := run(src, r.doc, "field", r.v)
		if got != want || !strings.Contains(got, r.code) || (r.code == "") != !strings.HasPrefix(got, "error") {
			t.Errorf("%v %q: reader %q, field %q, want %s", r.v, r.code, got, want, r.code)
		}
		if streamed := tr.Runs[streamtrace.Main] > 0; streamed != r.stream {
			t.Errorf("%v %q: streamed %v, want %v", r.v, r.code, streamed, r.stream)
		}
	}
	// Schemas the stream once refused, streamed now: what the body reads,
	// and the verdict, are the tree's.
	for _, c := range []struct {
		name, schema string
		rules        []string
		docs         map[string]string // name "bad..." is invalid
	}{
		{"values", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:element name="r"><xs:complexType><xs:sequence>
   <xs:element name="i" type="xs:integer" default="5" minOccurs="0" maxOccurs="unbounded"/>
   <xs:element name="m" fixed="ab" minOccurs="0" maxOccurs="unbounded"><xs:complexType mixed="true"><xs:sequence>
     <xs:element name="x" minOccurs="0"/></xs:sequence></xs:complexType></xs:element>
 </xs:sequence></xs:complexType></xs:element></xs:schema>`,
			[]string{`<xsl:template match="i|m"><v d="{data(.)}" t="{. instance of element(*, xs:integer)}"/></xsl:template>`,
				`<xsl:template match="i|m"><v s="[{.}]"/></xsl:template>`,
				`<xsl:template match="i|m"><v n="{count(node())}"/></xsl:template>`,
				`<xsl:template match="/"><o><xsl:value-of select="r/i/string()" separator=","/></o></xsl:template>`},
			map[string]string{"ok": `<r><i/><i>7</i><i></i><m/><m>ab</m><m>a<?p?>b</m></r>`,
				"bad-fixed": `<r><m>ba</m></r>`, "bad-child": `<r><m>ab<x/></m></r>`, "bad-comment": `<r><i>x<!----></i></r>`}},
		{"open", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:o="urn:o" xmlns:vc="http://www.w3.org/2007/XMLSchema-versioning" vc:minVersion="1.1">
 <xs:element name="r"><xs:complexType>
   <xs:openContent><xs:any namespace="urn:o" processContents="lax"/></xs:openContent>
   <xs:sequence><xs:element name="i" type="xs:integer" maxOccurs="3"/></xs:sequence></xs:complexType></xs:element></xs:schema>`,
			[]string{`<xsl:template match="i"><v d="{data(.) + 1}"/></xsl:template>`,
				`<xsl:template match="/"><o><xsl:value-of select="count(r/*)"/></o></xsl:template>`},
			map[string]string{"ok": `<r xmlns:o="urn:o"><o:x>a</o:x><i>1</i>` + "\n" + `<o:y/><i>2</i></r>`,
				"bad-name": `<r><i>1</i><x/></r>`, "bad-count": `<r xmlns:o="urn:o"><i>1</i><o:x/><i>2</i><i>3</i><i>4</i></r>`}},
		{"keys", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:element name="r"><xs:complexType><xs:sequence>
   <xs:element name="i" maxOccurs="unbounded"><xs:complexType><xs:simpleContent><xs:extension base="xs:string">
     <xs:attribute name="id" type="xs:decimal"/></xs:extension></xs:simpleContent></xs:complexType></xs:element>
   <xs:element name="g" minOccurs="0" maxOccurs="unbounded"><xs:complexType><xs:sequence>
     <xs:element name="j" minOccurs="0" maxOccurs="unbounded" type="xs:int"/></xs:sequence></xs:complexType>
     <xs:unique name="J"><xs:selector xpath="j"/><xs:field xpath="."/></xs:unique></xs:element>
   <xs:element name="ref" minOccurs="0" maxOccurs="unbounded" type="xs:decimal"/>
 </xs:sequence></xs:complexType>
 <xs:key name="K"><xs:selector xpath="i"/><xs:field xpath="@id"/></xs:key>
 <xs:keyref name="R" refer="K"><xs:selector xpath="ref"/><xs:field xpath="."/></xs:keyref>
 </xs:element></xs:schema>`,
			[]string{`<xsl:template match="i"><v d="{data(@id) * 2}" s="{.}"/></xsl:template>`,
				`<xsl:template match="/"><o><xsl:value-of select="sum(r/ref)"/></o></xsl:template>`},
			map[string]string{"ok": `<r><i id="1">a</i><i id="2.0">b</i><g><j>1</j><j>2</j></g><g><j>1</j></g><ref>1.0</ref><ref>2</ref></r>`,
				"bad-dup": `<r><i id="1">a</i><i id="1.0">b</i></r>`, "bad-ref": `<r><i id="1">a</i><ref>3</ref></r>`,
				"bad-unique": `<r><i id="1">a</i><g><j>1</j><j>01</j></g></r>`, "bad-missing": `<r><i>a</i></r>`}},
		{"ns defaults", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:a="urn:a" targetNamespace="urn:a"
   elementFormDefault="qualified" attributeFormDefault="qualified">
 <xs:element name="r"><xs:complexType><xs:sequence><xs:element name="e" maxOccurs="unbounded"><xs:complexType><xs:sequence>
   <xs:element name="c" minOccurs="0"><xs:complexType><xs:attribute name="k" type="xs:int" default="3"/></xs:complexType></xs:element>
 </xs:sequence><xs:attribute name="foo" type="xs:int" default="1"/></xs:complexType></xs:element></xs:sequence></xs:complexType></xs:element></xs:schema>`,
			[]string{`<xsl:template match="*:e"><v f="{data(@*:foo) + 1}" n="{@*/name()}"><xsl:apply-templates/></v></xsl:template>`,
				`<xsl:template match="*:c"><w k="{@*/name()}"/></xsl:template>`,
				`<xsl:template match="*:e"><xsl:copy-of select="."/></xsl:template>`,
				``},
			map[string]string{"ok": `<r xmlns="urn:a"><e><c/></e><e xmlns:p="urn:a"><c/></e><e xmlns:ns0="urn:o"/></r>`,
				"bad-value": `<r xmlns="urn:a" xmlns:a="urn:a"><e a:foo="x"/></r>`}},
	} {
		for _, r := range c.rules {
			for name, doc := range c.docs {
				where := c.name + " " + name + " " + r
				src := sheet(c.schema, "", r)
				got, tr := run(src, doc, "reader", SourceStrict)
				want, _ := run(src, doc, "field", SourceStrict)
				if got != want {
					t.Errorf("%s:\nreader %q\nfield  %q", where, got, want)
				}
				if strings.HasPrefix(want, "error") != strings.HasPrefix(name, "bad") {
					t.Errorf("%s: %q", where, want)
				}
				if man, _ := run(src, doc, "manual", SourceStrict); !strings.HasPrefix(name, "bad") && man != want {
					t.Errorf("%s:\nfield  %q\nmanual %q", where, want, man)
				}
				if tr.Runs[streamtrace.Main] == 0 {
					t.Errorf("%s: not streamed", where)
				}
			}
		}
	}
}
