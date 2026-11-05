package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestStreamXMLToJSONNestedMatches: xml-to-json of a path whose matches may
// nest (sf-xml-to-json-004's shape) streams, the writer matching below the
// first match: a second match, nested or not, raises the tree's XPTY0004,
// one match gives the tree's JSON.
func TestStreamXMLToJSONNestedMatches(t *testing.T) {
	const fn = `xmlns="http://www.w3.org/2005/xpath-functions"`
	docs := map[string]string{
		"one":    `<map ` + fn + `><array key="B"><map><number key="d">1</number></map></array></map>`,
		"nested": `<map ` + fn + `><array key="B"><map><array key="B"><null/></array></map></array></map>`,
		"deep":   `<map ` + fn + `><array key="B"><map><map key="x"><array key="B"><null/></array></map></map></array></map>`,
		"twice":  `<map ` + fn + `><array key="B"><null/><null/></array></map>`,
		"none":   `<map ` + fn + `><array key="C"><null/></array></map>`,
	}
	bodies := []string{
		`<j><xsl:value-of select="xml-to-json(//*[@key='B']/*, map{'indent': true()})"/></j>`,
		`<j><xsl:value-of select="xml-to-json(//*[@key='B']//*)"/></j>`,
	}
	for name, doc := range docs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		for bi, b := range bodies {
			sheet := coreSheetWith("", b)
			so, se, st := coreRun(sheet, dir, true, TransformOptions{})
			to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
			if so != to || se != te {
				t.Errorf("%s body %d:\ncore %q %q\ntree %q %q", name, bi, so, se, to, te)
			}
			if st.Runs[0] == 0 {
				t.Errorf("%s body %d: not streamed", name, bi)
			}
			if st.Guards != 0 || st.Reruns != 0 && te == "" {
				t.Errorf("%s body %d: guards %d, re-runs %d where the tree gives %q", name, bi, st.Guards, st.Reruns, te)
			}
			if wantErr := name == "nested" || name == "deep" || name == "twice" || bi == 1 && name == "one"; wantErr != strings.Contains(te, "XPTY0004") {
				t.Errorf("%s body %d: tree error %q", name, bi, te)
			}
		}
	}
}

// TestStreamMainLeafTop: a built-in copy of a comment, PI or text node at
// the top of a streamed principal result (si-apply-templates-008, si-try-200)
// puts the source's own node in Result.Nodes, as the tree does: its node in
// the document built as it is read (streammain_leaftop.go), whose parent,
// siblings and root are the tree's.
func TestStreamMainLeafTop(t *testing.T) {
	docs := []string{
		`<?a x?><!--c1--><r><s>t</s><!--in--></r><!--c2--><?b?>`,
		`<r><s id="1">t</s></r>`,
		`<!--only--><r xmlns:p="urn:p" p:a="1">x<?q?></r>`,
		`<?xml version="1.1"?><!DOCTYPE r [<!NOTATION n SYSTEM "n"><!ENTITY e SYSTEM "e.bin" NDATA n><!ATTLIST s id ID #IMPLIED>]><!--d--><r><s id="i1"/></r>`,
		"<!--w--><r>\n <s> t </s>\n <s xml:space='preserve'> </s><!--x--> </r>",
	}
	sheets := []struct {
		body   string
		stream bool
	}{
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><xsl:apply-templates/></xsl:template>`, true},
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><xsl:try><xsl:apply-templates/><xsl:catch/></xsl:try></xsl:template>`, true},
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><xsl:apply-templates/></xsl:template><xsl:template match="r"><xsl:apply-templates/></xsl:template>`, true},
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><xsl:if test="true()"><xsl:apply-templates/></xsl:if></xsl:template>`, true},
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><xsl:fork><xsl:sequence><xsl:apply-templates/></xsl:sequence></xsl:fork></xsl:template>`, false},
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:template match="/"><xsl:variable name="v" as="node()*"><xsl:apply-templates/></xsl:variable><o><xsl:value-of select="count($v/..)"/></o><xsl:apply-templates select="()"/></xsl:template>`, false},
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:strip-space elements="*"/><xsl:template match="/"><xsl:apply-templates/></xsl:template>`, true},
		{`<xsl:mode streamable="yes" on-no-match="shallow-copy"/><xsl:strip-space elements="*"/><xsl:template match="/"><xsl:apply-templates/></xsl:template><xsl:template match="r"><xsl:apply-templates/></xsl:template>`, true},
	}
	for di, doc := range docs {
		for si, sh := range sheets {
			sheet := mainSheet(sh.body)
			opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}}
			so, st := mainRun(sheet, doc, true, opts)
			to, _ := mainRun(sheet, doc, false, opts)
			so, to = so+leafTopShape(t, sheet, doc, true, opts), to+leafTopShape(t, sheet, doc, false, opts)
			if so != to {
				t.Errorf("doc %d sheet %d:\nstream %s\ntree   %s", di, si, so, to)
			}
			if streamed := st.Streamed() > 0; streamed != sh.stream || st.Guards != 0 {
				t.Errorf("doc %d sheet %d: streamed %v, guards %d", di, si, streamed, st.Guards)
			}
		}
	}
}

// leafTopShape describes, for each leaf of Result.Nodes, its parent's
// string value, children and root, which nodesShape does not.
func leafTopShape(t *testing.T, sheet, doc string, streamed bool, opts TransformOptions) string {
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: "file:///s/s.xsl"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var res *Result
	if streamed {
		res, err = s.TransformReader(context.Background(), readOnly{strings.NewReader(doc)}, opts)
	} else {
		src, perr := xdm.Parse(strings.NewReader(doc), opts.SourceParseOptions)
		if perr != nil {
			t.Fatal(perr)
		}
		res, err = s.Transform(context.Background(), src.Root, opts)
	}
	if err != nil {
		return "error: " + err.Error()
	}
	var b strings.Builder
	for _, it := range res.Nodes {
		n, ok := it.(*xdm.Node)
		if !ok || n.Kind() == xdm.KindElement || n.Parent() == nil {
			continue
		}
		p := n.Parent()
		fmt.Fprintf(&b, "[%v %q in %v %q:", n.Kind(), n.StringValue(), p.Kind(), p.StringValue())
		for c := range p.Children() {
			fmt.Fprintf(&b, " %v/%s/%q", c.Kind(), c.Name().Local, c.StringValue())
		}
		r := n.Root()
		fmt.Fprintf(&b, " root %v %q %v %q]", r.Kind(), r.StringValue(), r.Tree().DocType, r.Tree().XMLVersion)
	}
	return b.String()
}

// TestStreamSchemaTypedMax: max() below the document node over elements
// whose schema type is a list of one built-in atomic type
// (streamable-055) streams where the document is validated strictly or not
// at all (streamsplit_schema.go), with the tree's answer; a lax validation,
// an atomic type, a wildcard or two item types keep it on the tree.
func TestStreamSchemaTypedMax(t *testing.T) {
	schema := func(v, extra string) string {
		return `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:element name="acct"><xs:complexType><xs:sequence>
  <xs:element name="t" maxOccurs="unbounded"><xs:complexType><xs:sequence>` + v + `</xs:sequence></xs:complexType></xs:element>` + extra + `
 </xs:sequence></xs:complexType></xs:element>
 <xs:simpleType name="decs"><xs:list itemType="xs:decimal"/></xs:simpleType>
 <xs:simpleType name="dbls"><xs:list itemType="xs:double"/></xs:simpleType>
</xs:schema>`
	}
	list := `<xs:element name="v" type="decs"/>`
	schemas := []struct {
		name, schema string
		streams      bool
	}{
		{"list", schema(list, ""), true},
		{"atomic", schema(`<xs:element name="v" type="xs:decimal"/>`, ""), false},
		{"wildcard", schema(list, `<xs:any processContents="lax" minOccurs="0"/>`), false},
		{"two", schema(list, `<xs:element name="u" minOccurs="0"><xs:complexType><xs:sequence><xs:element name="v" type="dbls"/></xs:sequence></xs:complexType></xs:element>`), false},
	}
	doc := `<acct><t><v>1.5 2</v></t><t><v>-3 2.00</v></t><t><v>0.25</v></t></acct>`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, sc := range schemas {
		for _, attrs := range []string{`validation="strict"`, `validation="strip"`, `validation="lax"`} {
			sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:import-schema>` + sc.schema + `</xsl:import-schema>
<xsl:mode name="m" streamable="yes"/>
<xsl:template match="acct" mode="m"><m><xsl:value-of select="max(t/v)"/></m></xsl:template>
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml" ` + attrs +
				`><xsl:apply-templates select="." mode="m"/></xsl:source-document></top></xsl:template></xsl:stylesheet>`
			so, se, st := coreRun(sheet, dir, true, TransformOptions{})
			to, te, _ := coreRun(sheet, dir, false, TransformOptions{})
			if so != to || se != te {
				t.Errorf("%s %s:\ncore %q %q\ntree %q %q", sc.name, attrs, so, se, to, te)
			}
			want := sc.streams && attrs != `validation="lax"`
			if streamed := st.Runs[0] > 0; streamed != want || st.Guards != 0 || st.Reruns != 0 && te == "" {
				t.Errorf("%s %s: streamed %v (want %v), guards %d, re-runs %d", sc.name, attrs, streamed, want, st.Guards, st.Reruns)
			}
		}
	}
}

// TestStreamGroupNodeFreeBody: a group-by over the document element's
// children whose body returns arrays, maps or simple maps of atomic values
// (si-fork-815) holds its members as copies (streamcopy_l3group.go), which
// no result can hand on as nodes (streamcopy_nodefree.go): it streams, with
// the tree's output and Result.Nodes. A body returning the members does not.
func TestStreamGroupNodeFreeBody(t *testing.T) {
	doc := `<a><n>1</n><t v="1.5" d="x"/><t v="2" d="y"/><t v="-3" d="x"/><t v="0.25" d="z"/></a>`
	rule := func(body string) string {
		return `<xsl:mode streamable="yes"/><xsl:output method="json"/>` +
			`<xsl:template match="/"><xsl:apply-templates select="a"/></xsl:template>` +
			`<xsl:template match="a"><xsl:sequence select="array { p:g(.) }"/></xsl:template>` +
			`<xsl:function name="p:g" as="item()*" streamability="absorbing"><xsl:param name="in" as="element()"/>` +
			`<xsl:fork><xsl:for-each-group select="$in/t" group-by="@d">` + body + `</xsl:for-each-group></xsl:fork></xsl:function>`
	}
	rows := []struct {
		body   string
		stream bool
	}{
		{`<xsl:sequence select="array { current-group()!@v!xs:decimal(.) }"/>`, true},
		{`<xsl:sequence select="map { 'k': current-grouping-key(), 'n': count(current-group()) }"/>`, true},
		{`<xsl:sequence select="[ sum(current-group()/@v), current-group()!string(@v) ]"/>`, true},
		{`<xsl:sequence select="array { current-group() }"/>`, false},
	}
	for i, r := range rows {
		sheet := mainSheet(rule(r.body))
		so, st := mainRun(sheet, doc, true, TransformOptions{})
		to, _ := mainRun(sheet, doc, false, TransformOptions{})
		so, _, _ = strings.Cut(so, "\n") // the shape prints an array's address
		to, _, _ = strings.Cut(to, "\n")
		if so != to {
			t.Errorf("row %d:\nstream %s\ntree   %s", i, so, to)
		}
		if streamed := st.Streamed() > 0; streamed != r.stream || st.Guards != 0 {
			t.Errorf("row %d: streamed %v (want %v), guards %d", i, streamed, r.stream, st.Guards)
		}
	}
}

// TestStreamSourceDocumentFragment: xsl:source-document href="doc.xml#id"
// (docbook-003) streams to the element with that ID and runs the body on its
// grounded copy (streamfrag.go), with the tree's output and errors; with no
// such element its context is the document node, run on the tree. A body
// that could tell the copy from the element stays on the tree.
func TestStreamSourceDocumentFragment(t *testing.T) {
	docs := map[string]string{
		"xmlid": `<r xmlns="urn:r" xmlns:p="urn:p" xml:base="http://b/"><s xml:id="a"><t>1</t></s><s xml:id="k" p:x="2"><t>k1</t><u/></s><s xml:id="k">dup</s></r>`,
		"dtd":   `<!DOCTYPE r [<!ATTLIST s id ID #IMPLIED>]><r><s id="j">no</s><s id="k"><t>d</t></s></r>`,
		"none":  `<r><s id="k">not an ID</s></r>`,
		"late":  `<r><s xml:id="k"><t>x</t></s><bad></r>`,
		"inner": `<r><s xml:id="z"><s xml:id="k"><t>in</t></s></s></r>`,
	}
	bodies := []struct {
		body   string
		stream bool
	}{
		{`<xsl:copy-of select="."/>`, true},
		// The document node's plan refuses it (two consuming operands): a
		// fragment run needs one, for the instruction to stream at all.
		{`<o n="{count(*)}"><xsl:value-of select="t"/><xsl:copy-of select="@*"/></o>`, false},
		{`<o><xsl:copy-of select="t"/></o>`, true},
		{`<xsl:copy><xsl:copy-of select="*/name()"/></xsl:copy>`, true},
		{`<o><xsl:value-of select="name(..)"/></o>`, false},
		{`<xsl:sequence select="."/>`, false},
		{`<o><xsl:value-of select="exists(/r)"/></o>`, false},
	}
	for name, doc := range docs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		for bi, b := range bodies {
			sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml#k">` + b.body +
				`</xsl:source-document></top></xsl:template></xsl:stylesheet>`
			opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}}
			so, se, st := coreRun(sheet, dir, true, opts)
			to, te, _ := coreRun(sheet, dir, false, opts)
			if so != to || se != te {
				t.Errorf("%s body %d:\ncore %q %q\ntree %q %q", name, bi, so, se, to, te)
			}
			want := b.stream && name != "none" && name != "late"
			if streamed := st.Runs[0] > 0 && st.Reruns == 0; streamed != want || st.Guards != 0 {
				t.Errorf("%s body %d: streamed %v (want %v), runs %v re-runs %d guards %d", name, bi, streamed, want, st.Runs, st.Reruns, st.Guards)
			}
		}
	}
}
