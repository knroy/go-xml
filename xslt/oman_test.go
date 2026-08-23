package xslt

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The Oman PINT rule set is a Schematron-compiled validating stylesheet — the
// use case this engine exists for. Unlike the UBL renderer test, what matters
// here is not the markup but the *verdict*: which assertions fired, against
// which nodes. A validator that reports a different set of failures than the
// reference implementation is worse than useless, because it rejects valid
// invoices.
//
// The golden files were produced by Saxon-HE 12.4 from the same inputs.
//
// Running this against the full 39-document corpus found two real bugs:
//
//   - fn:current() returned the predicate's context item rather than the node
//     the enclosing instruction was processing. Schematron's location-path
//     generator counts preceding siblings with
//     "[local-name() = local-name(current())]", so the test was trivially true
//     and every sibling was counted: a document with 2 cac:TaxTotal elements
//     reported "TaxTotal[18]". That mis-numbering also produced a false
//     failed-assert on ALIGNED-IBRP-S-08-OM.
//   - xsl:number level="multiple" was unimplemented, so the rule set would not
//     compile at all.

// assertion is one SVRL verdict, reduced to the parts a caller acts on.
type assertion struct {
	kind     string
	id       string
	location string
}

var (
	reAssert = regexp.MustCompile(`<svrl:(failed-assert|successful-report)\b([^>]*)>`)
	reFired  = regexp.MustCompile(`<svrl:fired-rule\b`)
)

// parseSVRL extracts the fired-rule count and every assertion from a report.
func parseSVRL(s string) (int, []assertion) {
	var out []assertion
	for _, m := range reAssert.FindAllStringSubmatch(s, -1) {
		attrs := m[2]
		out = append(out, assertion{
			kind:     m[1],
			id:       svrlAttr(attrs, "id"),
			location: svrlAttr(attrs, "location"),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].id != out[j].id {
			return out[i].id < out[j].id
		}
		return out[i].location < out[j].location
	})
	return len(reFired.FindAllString(s, -1)), out
}

func svrlAttr(attrs, name string) string {
	m := regexp.MustCompile(name + `="([^"]*)"`).FindStringSubmatch(attrs)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestOmanPINTMatchesSaxon(t *testing.T) {
	dir := testdataPath("oman")
	sheetSrc, err := os.ReadFile(filepath.Join(dir, "pint-om-rules.xslt"))
	if err != nil {
		t.Skipf("Oman testdata not present: %v", err)
	}
	sheetTree, err := xdm.ParseString(string(sheetSrc), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the rule set: %v", err)
	}
	sheet, err := Compile(sheetTree.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling the rule set: %v", err)
	}

	golden, _ := filepath.Glob(filepath.Join(dir, "*.saxon.svrl"))
	if len(golden) == 0 {
		t.Skip("no Oman golden files present")
	}
	sort.Strings(golden)

	for _, gp := range golden {
		name := filepath.Base(gp)
		name = name[:len(name)-len(".saxon.svrl")]

		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(dir, name+".xml"))
			if err != nil {
				t.Fatal(err)
			}
			wantSVRL, err := os.ReadFile(gp)
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

			gotFired, gotAsserts := parseSVRL(res.String())
			wantFired, wantAsserts := parseSVRL(string(wantSVRL))

			if gotFired != wantFired {
				t.Errorf("fired-rule count = %d, Saxon reports %d", gotFired, wantFired)
			}

			// A missing failure means a bad invoice would be accepted; an
			// extra one means a good invoice would be rejected. Both are
			// reported separately because they fail in opposite directions.
			want := map[assertion]bool{}
			for _, a := range wantAsserts {
				want[a] = true
			}
			got := map[assertion]bool{}
			for _, a := range gotAsserts {
				got[a] = true
			}
			for a := range want {
				if !got[a] {
					t.Errorf("missed assertion (false negative): %s at %s", a.id, a.location)
				}
			}
			for a := range got {
				if !want[a] {
					t.Errorf("spurious assertion (false positive): %s at %s", a.id, a.location)
				}
			}
		})
	}
}

func TestCurrentIsTheInstructionFocus(t *testing.T) {
	// The bug the Oman corpus exposed, reduced: inside a predicate, current()
	// must still be the node the enclosing instruction is processing, not the
	// item the predicate is testing.
	doc := `<r><a/><b/><a/><b/><a/></r>`
	sheet := wrap(`<xsl:template match="/"><out>
		<xsl:for-each select="//a">
			<n><xsl:value-of select="count(preceding-sibling::*[local-name() = local-name(current())])"/></n>
		</xsl:for-each>
	</out></xsl:template>`)
	// Each <a> has 0, 1 and 2 preceding <a> siblings. If current() wrongly
	// returned the predicate's own context item the test would be trivially
	// true and the counts would be 0, 2, 4.
	if got := run(t, sheet, doc); got != "<out><n>0</n><n>1</n><n>2</n></out>" {
		t.Errorf("got %q, want counts 0,1,2 (current() must be the for-each item)", got)
	}
}

func TestNumberLevelMultiple(t *testing.T) {
	doc := `<r><s><p/><p/></s><s><p/><p/><p/></s></r>`
	sheet := wrap(`
		<xsl:template match="/"><out><xsl:apply-templates select="//p"/></out></xsl:template>
		<xsl:template match="p"><n><xsl:number level="multiple" count="*"/></n></xsl:template>`)
	// Levels are r.s.p: the first <p> is 1.1.1, the last is 1.2.3.
	want := "<out><n>1.1.1</n><n>1.1.2</n><n>1.2.1</n><n>1.2.2</n><n>1.2.3</n></out>"
	if got := run(t, sheet, doc); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAsTypeCoercesUntypedAtomic(t *testing.T) {
	// A parameter declared as xs:decimal must receive an xs:decimal, so the
	// arithmetic inside the function is exact rather than floating point.
	sheet := `<xsl:stylesheet version="2.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		xmlns:xs="http://www.w3.org/2001/XMLSchema"
		xmlns:u="urn:u">
		<xsl:output omit-xml-declaration="yes"/>
		<xsl:function name="u:add" as="xs:decimal">
			<xsl:param name="a" as="xs:decimal"/>
			<xsl:param name="b" as="xs:decimal"/>
			<xsl:sequence select="$a + $b"/>
		</xsl:function>
		<xsl:template match="/"><r><xsl:value-of select="u:add(//x, //y)"/></r></xsl:template>
	</xsl:stylesheet>`
	// 0.1 + 0.2 is exactly 0.3 in decimal arithmetic and 0.30000000000000004
	// in double arithmetic; the "as" declaration is what selects the former.
	// What is under test is the arithmetic, not the serialised namespace
	// declarations: the u: and xs: prefixes are in scope on the literal
	// result element and section 11.1.3 copies them to it.
	if got := run(t, sheet, `<d><x>0.1</x><y>0.2</y></d>`); !strings.Contains(got, ">0.3<") {
		t.Errorf("got %q, want 0.3 (the as-declaration must coerce to xs:decimal)", got)
	}
}
