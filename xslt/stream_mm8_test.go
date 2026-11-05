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
