package xslt

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The golden files in testdata were produced by Saxon-HE 12.4, the reference
// XSLT 2.0 implementation, from the same inputs. They are checked in rather
// than regenerated so the comparison keeps working without a JVM.
//
// The stylesheet is a real production one (a UBL Invoice/CreditNote to HTML
// renderer, 76 templates, ~100KB) and the documents are the OpenPEPPOL BIS 3.0
// examples. This is deliberately not a synthetic test: it exercises template
// priority, modes, xsl:function, xsl:key, grouping, format-number, attribute
// value templates and the HTML output method against inputs nobody wrote to
// suit this engine.
//
// Three real bugs were found by running it, all fixed: a path rooted at a
// variable ("$codelists/cl[...]") required a context item it should not have;
// fn:format-number was missing entirely; and U+00A0 was being stripped as
// whitespace because strings.TrimSpace treats it as such while XML does not.

var goldenCases = []string{
	"Allowance-example",
	"base-example",
	"base-creditnote-correction",
	"vat-category-O",
}

func TestMatchesSaxonOutput(t *testing.T) {
	sheetSrc, err := os.ReadFile(testdataPath("ubl-invoice.xslt"))
	if err != nil {
		t.Skipf("testdata not present: %v", err)
	}
	sheetTree, err := xdm.ParseString(string(sheetSrc), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	sheet, err := Compile(sheetTree.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling the stylesheet: %v", err)
	}

	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(testdataPath(name + ".xml"))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(testdataPath(name + ".saxon.html"))
			if err != nil {
				t.Fatal(err)
			}

			docTree, err := xdm.ParseString(string(src), xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			res, err := sheet.Transform(context.Background(), docTree.Root, TransformOptions{})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}

			got := normalizeHTML(res.String())
			expected := normalizeHTML(string(want))
			if got == expected {
				return
			}

			// Report the first divergence with surrounding context; a whole-file
			// diff of 45KB of HTML is unreadable in test output.
			i := 0
			for i < len(got) && i < len(expected) && got[i] == expected[i] {
				i++
			}
			lo := max(0, i-100)
			t.Errorf("output diverges from Saxon at offset %d of %d\n  go-xml: %q\n  saxon : %q",
				i, len(expected),
				got[lo:min(len(got), i+140)],
				expected[lo:min(len(expected), i+140)])
		})
	}
}

// normalizeHTML collapses insignificant whitespace so the comparison is about
// content and structure rather than indentation, which xsl:output indent="no"
// leaves implementation-defined.
var (
	betweenTags = regexp.MustCompile(`>[ \t\n\r]+<`)
	runsOfSpace = regexp.MustCompile(`[ \t\n\r]+`)
)

func normalizeHTML(s string) string {
	s = betweenTags.ReplaceAllString(s, "><")
	s = runsOfSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func TestRealStylesheetCompiles(t *testing.T) {
	// A cheap guard that the 100KB production stylesheet still parses and
	// compiles even if the golden comparison is skipped.
	src, err := os.ReadFile(testdataPath("ubl-invoice.xslt"))
	if err != nil {
		t.Skipf("testdata not present: %v", err)
	}
	tree, err := xdm.ParseString(string(src), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Compile(tree.Root, CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}
}

// Section 16.3 allows a key's value to be given as a sequence constructor
// instead of a use attribute, which is what lets a key be computed by
// anything a constructor can express — an xsl:choose over the matched node
// rather than a single expression.
func TestKeyWithSequenceConstructor(t *testing.T) {
	const sheet = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="2.0">
	  <xsl:key name="k" match="p">
	    <xsl:choose>
	      <xsl:when test="@id"><xsl:value-of select="@id"/></xsl:when>
	      <xsl:otherwise>none</xsl:otherwise>
	    </xsl:choose>
	  </xsl:key>
	  <xsl:template match="/">
	    <out>
	      <xsl:value-of select="key('k','b')"/>
	      <xsl:text>|</xsl:text>
	      <xsl:value-of select="key('k','none')"/>
	    </out>
	  </xsl:template>
	</xsl:stylesheet>`
	got := run(t, sheet, `<r><p id="a">first</p><p id="b">second</p><p>third</p></r>`)
	if !strings.Contains(got, "second|third") {
		t.Errorf("key by sequence constructor gave %q, want it to contain %q",
			got, "second|third")
	}
}

// Giving the value both ways leaves no rule for reconciling them, and giving
// it neither leaves the key undefined. Both are static errors.
func TestKeyNeedsExactlyOneValueForm(t *testing.T) {
	const both = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="2.0">
	  <xsl:key name="k" match="p" use="@id"><xsl:value-of select="@id"/></xsl:key>
	  <xsl:template match="/"><out/></xsl:template></xsl:stylesheet>`
	const neither = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="2.0">
	  <xsl:key name="k" match="p"/>
	  <xsl:template match="/"><out/></xsl:template></xsl:stylesheet>`
	for name, src := range map[string]string{"both": both, "neither": neither} {
		doc, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Compile(doc.Root, CompileOptions{}); err == nil {
			t.Errorf("xsl:key with %s form was accepted", name)
		}
	}
}
