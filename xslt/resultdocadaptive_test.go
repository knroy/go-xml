package xslt

import (
	"context"
	"strings"
	"testing"
)

// TestResultDocumentBooleanSynonyms covers the yes/no synonyms on
// xsl:result-document's serialization attributes.
//
// XSLT 3.0 §3.5 declares each of them as { boolean }, and the
// schema-for-stylesheets types them xsl:yes-or-no: "One of the values 'yes' or
// 'no': the values 'true' or 'false', or '1' or '0' are accepted as
// synonyms." result-document-0304 writes omit-xml-declaration="true" in a
// version="2.0" module and is scoped XSLT30+, exactly as message-0009 writes
// terminate="true" there, so the spelling follows the processor rather than
// the module's @version. It was refused with XTSE0020.
func TestResultDocumentBooleanSynonyms(t *testing.T) {
	for _, attr := range []string{
		`omit-xml-declaration="true"`,
		`omit-xml-declaration="1"`,
		`indent="false"`,
		`undeclare-prefixes="0"`,
	} {
		t.Run(attr, func(t *testing.T) {
			src := `<xsl:stylesheet version="2.0"
			    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
			  <xsl:template name="main">
			    <xsl:result-document href="out.xml" ` + attr + `><e/></xsl:result-document>
			  </xsl:template>
			</xsl:stylesheet>`
			if _, err := Compile(mustParse(t, src), CompileOptions{}); err != nil {
				t.Fatalf("%s is a legal boolean synonym: %v", attr, err)
			}
		})
	}
}

// TestResultDocumentBooleanSynonymsRejectNonBoolean guards the widening: only
// the four boolean spellings are admitted, not any value at all.
func TestResultDocumentBooleanSynonymsRejectNonBoolean(t *testing.T) {
	src := `<xsl:stylesheet version="2.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <xsl:result-document href="out.xml" omit-xml-declaration="maybe"><e/></xsl:result-document>
	  </xsl:template>
	</xsl:stylesheet>`
	_, err := Compile(mustParse(t, src), CompileOptions{})
	if err == nil {
		t.Fatal("omit-xml-declaration=\"maybe\" must be XTSE0020")
	}
	if !strings.Contains(err.Error(), "XTSE0020") {
		t.Fatalf("got %v, want XTSE0020", err)
	}
}

// TestResultDocumentAdaptiveItemSeparatorNotDoubled covers the second half of
// result-document-0304.
//
// Serialization 3.1 §2 applies sequence normalisation -- and so item-separator
// insertion -- to "the XML, XHTML, HTML and Text output methods" only. The
// adaptive and json methods separate their own items, adaptive with this very
// parameter (§10). xsl:result-document inserted the separator into the
// recorded nodes as well, so an adaptive document got it twice: once as an
// inserted text item and again as adaptive's own join.
func TestResultDocumentAdaptiveItemSeparatorNotDoubled(t *testing.T) {
	src := `<xsl:transform version="2.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <xsl:result-document method="adaptive" item-separator="|"
	        build-tree="false" omit-xml-declaration="true">
	      <xsl:map><xsl:map-entry key="'a'" select="22"/></xsl:map>
	      <elem/>
	      <xsl:attribute name="a" select="5"/>
	    </xsl:result-document>
	  </xsl:template>
	</xsl:transform>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := sheet.Transform(context.Background(), nil,
		TransformOptions{InitialTemplate: "main"})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(res.Secondary) != 1 {
		t.Fatalf("got %d secondary results, want 1", len(res.Secondary))
	}
	got := res.Secondary[0].String()
	const want = `map{"a":22}|<elem/>|a="5"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResultDocumentDuplicateURIAfterResolution covers XTDE1490 being a
// collision of URIs rather than of href strings.
//
// XSLT 3.0 §24.2: "[ERR XTDE1490] It is a dynamic error for a transformation
// to generate two or more final result trees with the same URI." §24.1 says
// what that URI is: the href "may be absolute or relative. If it is relative,
// then it is resolved against the base output URI". So href="out.xml" and
// href="./out.xml" denote one tree, and writing both is the error. The check
// compared the raw hrefs, so the pair went through and the second document
// silently overwrote the first.
func TestResultDocumentDuplicateURIAfterResolution(t *testing.T) {
	src := `<xsl:transform version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <xsl:result-document href="out.xml"><a/></xsl:result-document>
	    <xsl:result-document href="./out.xml"><b/></xsl:result-document>
	  </xsl:template>
	</xsl:transform>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = sheet.Transform(context.Background(), nil, TransformOptions{
		InitialTemplate: "main",
		BaseOutputURI:   "file:///w/x.xml",
	})
	if err == nil {
		t.Fatal(`href="out.xml" and href="./out.xml" resolve to one URI: want XTDE1490`)
	}
	if !strings.Contains(err.Error(), "XTDE1490") {
		t.Fatalf("got %v, want XTDE1490", err)
	}
}

// TestResultDocumentDistinctURIsStillAllowed guards the narrowing above: two
// hrefs that resolve to genuinely different URIs must still both be written.
// Resolution yields "" for every document when no base output URI is known,
// and treating that as a collision would reject every multi-document
// stylesheet run without one.
func TestResultDocumentDistinctURIsStillAllowed(t *testing.T) {
	src := `<xsl:transform version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:template name="main">
	    <xsl:result-document href="one.xml"><a/></xsl:result-document>
	    <xsl:result-document href="two.xml"><b/></xsl:result-document>
	  </xsl:template>
	</xsl:transform>`
	sheet, err := Compile(mustParse(t, src), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, base := range []string{"", "file:///w/x.xml"} {
		res, err := sheet.Transform(context.Background(), nil, TransformOptions{
			InitialTemplate: "main",
			BaseOutputURI:   base,
		})
		if err != nil {
			t.Fatalf("base %q: %v", base, err)
		}
		if len(res.Secondary) != 2 {
			t.Fatalf("base %q: got %d secondary results, want 2", base, len(res.Secondary))
		}
	}
}
