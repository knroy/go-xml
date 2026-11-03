package xslt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// identitySheet is a stylesheet whose unnamed mode is streamable, with the
// rules rules.
func identitySheet(rules string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:mode streamable="yes"/>` + rules + `</xsl:stylesheet>`
}

// The identity template written the guaranteed-streamable way: attributes,
// then children, by two instructions.
const identityRule = `<xsl:template match="node()|@*"><xsl:copy>` +
	`<xsl:apply-templates select="@*"/><xsl:apply-templates select="node()"/></xsl:copy></xsl:template>`

// TestStreamIdentity: the identity template in a streamable mode streams the
// principal input through TransformReader, byte-identical to Parse +
// Transform; the @*|node() form is not guaranteed-streamable (§19.8.8.4
// makes the union crawling, §19.8.4.5 a crawling select roaming), and is
// XTSE3430 as the catalog's sx-union-202 is.
func TestStreamIdentity(t *testing.T) {
	opts := TransformOptions{SourceParseOptions: xdm.ParseOptions{AllowDOCTYPE: true,
		BaseURI: "file:///d/doc.xml", DocumentURI: "file:///d/doc.xml"}}
	sheets := []string{
		identityRule,
		`<xsl:template match="node()|@*"><xsl:copy><xsl:apply-templates select="node()"/></xsl:copy></xsl:template>`,
		identityRule + `<xsl:template match="sub"/>`,
		identityRule + `<xsl:template match="@k"><xsl:attribute name="key" select="."/></xsl:template>`,
		identityRule + `<xsl:template match="text()"><xsl:value-of select="upper-case(.)"/></xsl:template>`,
		`<xsl:strip-space elements="*"/>` + identityRule,
	}
	docs := map[string]string{}
	for name, d := range coreDocs {
		docs[name] = d
	}
	docs["attrs"] = `<?xml version="1.0"?>` + "\n" + `<!--c--><doc a="1" b="2" xmlns:q="urn:q"><r q:x="y">t<s/><?p q?></r>tail<!--z--><e></e></doc>`
	docs["doctype"] = `<!DOCTYPE doc [<!ATTLIST doc d CDATA "def">]><doc><r k="v"/></doc>`
	var why string
	traceMain = func(m, w string) {
		if m == "" {
			why = w
		}
	}
	defer func() { traceMain = nil }()
	for name, doc := range docs {
		for _, rules := range sheets {
			sheet := identitySheet(rules)
			so, st := mainRun(sheet, doc, true, opts)
			to, _ := mainRun(sheet, doc, false, opts)
			where := fmt.Sprintf("doc %s, rules %s", name, rules)
			if so != to {
				t.Errorf("%s:\nstreamed %s\ntree     %s", where, so, to)
			}
			if st.Guards != 0 {
				t.Errorf("%s: %d guard hits", where, st.Guards)
			}
			if st.Runs[streamtrace.Main] != 1 && !strings.HasPrefix(to, "error:") {
				t.Errorf("%s: not streamed: %s", where, why)
			}
		}
	}
	out, _ := mainRun(identitySheet(`<xsl:template match="node()|@*"><xsl:copy>`+
		`<xsl:apply-templates select="@*|node()"/></xsl:copy></xsl:template>`), docs["attrs"], true, opts)
	if !strings.Contains(out, "XTSE3430") {
		t.Errorf("@*|node(): %s, want XTSE3430", out)
	}
}
