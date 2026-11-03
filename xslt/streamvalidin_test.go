package xslt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
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
