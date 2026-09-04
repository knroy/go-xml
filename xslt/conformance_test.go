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

// A named picture component takes its case from the modifier's own letters,
// and its width from the width modifier.
//
// "[FN]" is MONDAY, "[FNn]" is Monday, "[Fn]" is monday. The width modifier
// then selects the abbreviation: "[FNn,*-3]" is Mon, because a maximum of
// three characters asks for the shortest form that fits. Dropping the width —
// which is what this did — made every abbreviated date come out in full,
// which is the failure mode the suite's format-date tests are full of.
func TestFormatDatePresentationModifiers(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`format-date(xs:date('2026-08-24'), '[FNn]')`, "Monday"},
		{`format-date(xs:date('2026-08-24'), '[FN]')`, "MONDAY"},
		{`format-date(xs:date('2026-08-24'), '[Fn]')`, "monday"},
		{`format-date(xs:date('2026-08-24'), '[FNn,*-3]')`, "Mon"},
		{`format-date(xs:date('2026-08-24'), '[MNn]')`, "August"},
		{`format-date(xs:date('2026-08-24'), '[MN,*-3]')`, "AUG"},
		{`format-date(xs:date('2026-08-24'), '[MNn] [D], [Y]')`, "August 24, 2026"},
		// A numeric month is still numeric: only N, n and Nn ask for a name.
		{`format-date(xs:date('2026-08-24'), '[M01]')`, "08"},
		{`format-dateTime(xs:dateTime('2026-08-24T15:05:00'), '[h]:[m01] [PN]')`,
			"3:05 PM"},
		// "Nn" title-cases rather than word-splitting: am becomes Am.
		{`format-dateTime(xs:dateTime('2026-08-24T09:00:00'), '[PNn]')`, "Am"},
	}
	for _, c := range cases {
		sheet := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		  xmlns:xs="http://www.w3.org/2001/XMLSchema" version="2.0">
		  <xsl:template match="/"><out><xsl:value-of select="` +
			strings.ReplaceAll(c.expr, `'`, "&apos;") + `"/></out></xsl:template>
		</xsl:stylesheet>`
		got := run(t, sheet, `<r/>`)
		if !strings.Contains(got, ">"+c.want+"<") {
			t.Errorf("%s\n  got  %s\n  want it to contain %q", c.expr, got, c.want)
		}
	}
}

// A format picture's characters outside the tokens are part of the output.
//
// Section 12.3: whatever precedes the first token is a prefix, whatever
// follows the last is a suffix, and what sits between tokens separates the
// numbers. Only the separators were emitted, so the two commonest formats —
// "(1)" and "[1]" — produced a bare number.
func TestNumberFormatPrefixAndSuffix(t *testing.T) {
	cases := []struct{ format, value, want string }{
		{"(1)", "7", "(7)"},
		{"[1]", "7", "[7]"},
		{"1.", "7", "7."},
		{"1", "7", "7"},
		{"a", "3", "c"},
		{"I", "9", "IX"},
		// A sequence of numbers takes the separator between each, with the
		// prefix and suffix wrapping the whole.
		{"(1).", "1 to 3", "(1.2.3)."},
	}
	for _, c := range cases {
		sheet := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="2.0">
		  <xsl:template match="/"><out><xsl:number value="` + c.value +
			`" format="` + c.format + `"/></out></xsl:template></xsl:stylesheet>`
		got := run(t, sheet, `<r/>`)
		if !strings.Contains(got, ">"+c.want+"<") {
			t.Errorf("format=%q value=%q\n  got  %s\n  want it to contain %q",
				c.format, c.value, got, c.want)
		}
	}
}

// The value attribute is a sequence, not a single number. Taking only the
// first silently dropped the rest.
func TestNumberValueIsASequence(t *testing.T) {
	const sheet = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="2.0">
	  <xsl:template match="/"><out><xsl:number value="1 to 5" format="1."/></out>
	  </xsl:template></xsl:stylesheet>`
	got := run(t, sheet, `<r/>`)
	if !strings.Contains(got, ">1.2.3.4.5.<") {
		t.Errorf("got %s, want it to contain %q", got, "1.2.3.4.5.")
	}
}

// A collation decides which grouping keys count as the same group, so
// xsl:for-each-group must honour @collation. Ignoring it silently grouped by
// codepoint, which puts "thou" and "THOU" in different groups under a
// case-insensitive collation.
func TestForEachGroupHonoursCollation(t *testing.T) {
	const ci = "http://www.w3.org/2005/xpath-functions/collation/html-ascii-case-insensitive"
	const sheet = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="2.0">
	  <xsl:template match="/"><out>
	    <xsl:for-each-group select="//w" group-by="." collation="` + ci + `">
	      <g><xsl:value-of select="current-grouping-key()"/>:<xsl:value-of
	        select="count(current-group())"/></g>
	    </xsl:for-each-group>
	  </out></xsl:template></xsl:stylesheet>`
	got := run(t, sheet, `<r><w>thou</w><w>THOU</w><w>Thou</w><w>other</w></r>`)
	if !strings.Contains(got, "<g>thou:3</g>") {
		t.Errorf("got %s, want the three spellings in one group", got)
	}
	if !strings.Contains(got, "<g>other:1</g>") {
		t.Errorf("got %s, want a separate group for other", got)
	}
}

// @collation is an attribute value template on both xsl:sort and
// xsl:for-each-group, so a stylesheet may compute which collation to use.
// Resolving it at compile time refuses the literal braces as an unknown
// collation URI, which is a stylesheet that will not compile at all.
func TestCollationMayBeAnAVT(t *testing.T) {
	const ci = "http://www.w3.org/2005/xpath-functions/collation/html-ascii-case-insensitive"
	const sheet = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="2.0">
	  <xsl:param name="c" select="'` + ci + `'"/>
	  <xsl:template match="/"><out>
	    <xsl:for-each select="//w"><xsl:sort select="." collation="{$c}"/>
	      <w><xsl:value-of select="."/></w></xsl:for-each>
	  </out></xsl:template></xsl:stylesheet>`
	got := run(t, sheet, `<r><w>b</w><w>A</w><w>a</w></r>`)
	// Under the case-insensitive collation A and a are equal, so the stable
	// sort keeps their input order and both precede b.
	if !strings.Contains(got, "<w>A</w><w>a</w><w>b</w>") {
		t.Errorf("got %s, want A,a,b under a computed collation", got)
	}
}

// Section 19.2: a constructed element may be assessed against the schema, in
// one of four modes. strict requires a global declaration and validity
// against it; lax passes an element the schema does not describe; preserve
// and strip assess nothing, and strip is the default — which is why a
// stylesheet saying nothing about validation gets none.
func TestElementValidation(t *testing.T) {
	const head = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	  xmlns:xs="http://www.w3.org/2001/XMLSchema" version="2.0">
	  <xsl:import-schema>
	    <xs:schema><xs:element name="n" type="xs:integer"/></xs:schema>
	  </xsl:import-schema>
	  <xsl:template match="/"><out>`
	const tail = `</out></xsl:template></xsl:stylesheet>`

	cases := []struct {
		name, body string
		wantErr    bool
	}{
		{"strict and valid", `<xsl:element name="n" validation="strict">42</xsl:element>`, false},
		{"strict and invalid", `<xsl:element name="n" validation="strict">abc</xsl:element>`, true},
		{"strict with no declaration", `<xsl:element name="zz" validation="strict">x</xsl:element>`, true},
		{"lax with no declaration", `<xsl:element name="zz" validation="lax">x</xsl:element>`, false},
		{"lax and invalid", `<xsl:element name="n" validation="lax">abc</xsl:element>`, true},
		{"a named type", `<xsl:element name="q" type="xs:integer">7</xsl:element>`, false},
		{"a named type, violated", `<xsl:element name="q" type="xs:integer">no</xsl:element>`, true},
		// strip is the default, so an invalid element passes unremarked.
		{"strip is the default", `<xsl:element name="n">abc</xsl:element>`, false},
		{"preserve assesses nothing",
			`<xsl:element name="n" validation="preserve">abc</xsl:element>`, false},
	}
	for _, c := range cases {
		_, err := runErr(t, head+c.body+tail, `<r/>`)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, want error = %v", c.name, err, c.wantErr)
		}
	}
}

// validation and type name the same thing two ways, so giving both leaves no
// rule for reconciling them.
func TestValidationAndTypeAreExclusive(t *testing.T) {
	const sheet = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	  xmlns:xs="http://www.w3.org/2001/XMLSchema" version="2.0">
	  <xsl:template match="/">
	    <xsl:element name="n" validation="strict" type="xs:integer">1</xsl:element>
	  </xsl:template></xsl:stylesheet>`
	doc, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(doc.Root, CompileOptions{}); err == nil {
		t.Error("an element with both validation and type was accepted")
	}
}

// A type in the XSD namespace needs no imported schema: the built-ins are
// always available, and requiring an import for type="xs:integer" would
// refuse stylesheets that import nothing and need nothing.
func TestBuiltinTypeNeedsNoImport(t *testing.T) {
	const sheet = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	  xmlns:xs="http://www.w3.org/2001/XMLSchema" version="2.0">
	  <xsl:template match="/"><out>
	    <xsl:element name="d" type="xs:date">2026-08-24</xsl:element>
	  </out></xsl:template></xsl:stylesheet>`
	if got := run(t, sheet, `<r/>`); !strings.Contains(got, "2026-08-24") {
		t.Errorf("got %s, want the date element", got)
	}
}

// A calendar this implementation does not have is refused rather than
// rendered with Gregorian fields.
//
// "OS" is Old Style — the Julian calendar — and section 9.8.4.3 requires the
// value be "converted to a value in the specified calendar", not merely
// labelled with its name. There is no Julian arithmetic here, so accepting
// "OS" and formatting the Gregorian fields silently reported 2026-08-24 for a
// date the Julian calendar puts on 11 August, thirteen days out. Which
// calendars are supported is implementation-defined, so declining it is the
// conformant answer, and the supported set is exactly the two Gregorian
// spellings the formatter actually implements.
func TestFormatDateCalendarArgument(t *testing.T) {
	const date = `xs:date('2026-08-24')`
	// A calendar in a namespace names another implementation's extension and
	// is left alone; a name in no namespace that is not supported is
	// FOFD1340, whether or not it appears in the specification's list.
	cases := []struct{ cal, want, errCode string }{
		{`'AD'`, "2026-08-24", ""},
		{`'ISO'`, "2026-08-24", ""},
		{`'Q{}ISO'`, "2026-08-24", ""},
		{`()`, "2026-08-24", ""},
		{`'Q{http://example.com/cal}OS'`, "2026-08-24", ""},
		{`'OS'`, "", "FOFD1340"},
		{`'ZODIAC'`, "", "FOFD1340"},
		{`':w'`, "", "FOFD1340"},
	}
	for _, c := range cases {
		expr := `format-date(` + date + `, '[Y0001]-[M01]-[D01]', (), ` + c.cal + `, ())`
		sheet := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		  xmlns:xs="http://www.w3.org/2001/XMLSchema" version="2.0">
		  <xsl:template match="/"><out><xsl:value-of select="` +
			strings.ReplaceAll(expr, `'`, "&apos;") + `"/></out></xsl:template>
		</xsl:stylesheet>`
		got, err := runErr(t, sheet, `<r/>`)
		if c.errCode != "" {
			if err == nil {
				t.Errorf("calendar %s: got %s, want %s", c.cal, got, c.errCode)
			} else if !strings.Contains(err.Error(), c.errCode) {
				t.Errorf("calendar %s: got %v, want %s", c.cal, err, c.errCode)
			}
			continue
		}
		if err != nil {
			t.Errorf("calendar %s: %v", c.cal, err)
			continue
		}
		if !strings.Contains(got, ">"+c.want+"<") {
			t.Errorf("calendar %s: got %s, want it to contain %q", c.cal, got, c.want)
		}
	}
}

// The ISO calendar's week components follow ISO 8601 rather than a
// locale-dependent convention.
//
// Section 9.8.4.3 fixes them: weeks run Monday to Sunday, week 1 of a year is
// the one containing its first Thursday, and a week belongs to the month
// containing its Thursday. 2005-01-01 is therefore week 53 of 2004, and
// 2007-01-01 opens week 1 — the two dates that separate the ISO rule from
// numbering weeks from 1 January.
func TestFormatDateISOWeekNumbering(t *testing.T) {
	cases := []struct{ date, pic, want string }{
		{"2005-01-01", "[W]", "53"},
		{"2007-01-01", "[W]", "1"},
		{"2004-05-01", "[W]", "18"},
		// Day of the week is numbered from 1 = Monday; 2004-01-01 is a
		// Thursday.
		{"2004-01-01", "[F01]", "04"},
		// Week within the month: 2006-01-30 is in a week whose Thursday
		// falls in February, so it is still January's fifth week.
		{"2006-01-30", "[w]", "5"},
		{"2005-12-04", "[w]", "1"},
	}
	for _, c := range cases {
		expr := `format-date(xs:date('` + c.date + `'), '` + c.pic + `', (), 'ISO', ())`
		sheet := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		  xmlns:xs="http://www.w3.org/2001/XMLSchema" version="2.0">
		  <xsl:template match="/"><out><xsl:value-of select="` +
			strings.ReplaceAll(expr, `'`, "&apos;") + `"/></out></xsl:template>
		</xsl:stylesheet>`
		if got := run(t, sheet, `<r/>`); !strings.Contains(got, ">"+c.want+"<") {
			t.Errorf("%s %s: got %s, want %q", c.date, c.pic, got, c.want)
		}
	}
}
