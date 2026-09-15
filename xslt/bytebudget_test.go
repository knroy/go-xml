package xslt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// runSheet compiles src and transforms a trivial document with it.
func runByteSheet(t *testing.T, src string) (string, error) {
	t.Helper()
	sd, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	doc, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	res, err := st.Transform(context.Background(), doc.Root, TransformOptions{})
	if err != nil {
		return "", err
	}
	return res.String(), nil
}

// doublingSheet is the stylesheet form of the string bomb: a run of SIBLING
// xsl:variable declarations, each holding two xsl:value-of of the one before
// it, so each added line doubles the result.
func doublingSheet(n int, seed string) string {
	var b strings.Builder
	b.WriteString(`<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:template match="/">` +
		`<xsl:variable name="v0">` + seed + `</xsl:variable>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<xsl:variable name="v%d">`+
			`<xsl:value-of select="$v%d"/><xsl:value-of select="$v%d"/>`+
			`</xsl:variable>`, i, i-1, i-1)
	}
	fmt.Fprintf(&b, `<out><xsl:value-of select="string-length($v%d)"/></out>`+
		`</xsl:template></xsl:stylesheet>`, n)
	return b.String()
}

func wantByteRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected XPDY0130; the MaxBytes budget did not bind, and " +
			"the transform built the whole string")
	}
	if code := xdm.ErrorCode(err); code != "XPDY0130" {
		t.Errorf("code = %q, want XPDY0130 (error: %v)", code, err)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell a refusal to allocate from a bad stylesheet", err)
	}
	if !strings.Contains(err.Error(), "bytes of string content") {
		t.Errorf("message %q is not the byte-budget refusal", err)
	}
}

// The defect is reachable from a stylesheet as well as from a query, and this
// is the shape that defeats a narrower boundary: the variables are SIBLINGS,
// so no one of them is large and Compiled.Eval's per-expression reset clears
// the counter between every one. Only a budget held across the transform sees
// the doubling.
// avtDoublingSheet is doublingSheet's shape with the concatenation moved into
// an attribute value template. The two differ only in where the string is
// built -- xsl:value-of into a text node, or {$v}{$v} into an attribute -- so
// a budget that binds one and not the other is a hole rather than a policy.
func avtDoublingSheet(n int, seed string) string {
	var b strings.Builder
	b.WriteString(`<xsl:stylesheet version="3.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">` +
		`<xsl:template match="/">` +
		`<xsl:variable name="v0" select="'` + seed + `'"/>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<xsl:variable name="t%d"><x a="{$v%d}{$v%d}"/>`+
			`</xsl:variable>`+
			`<xsl:variable name="v%d" select="string($t%d/x/@a)"/>`,
			i, i-1, i-1, i, i)
	}
	fmt.Fprintf(&b, `<out><xsl:value-of select="string-length($v%d)"/></out>`+
		`</xsl:template></xsl:stylesheet>`, n)
	return b.String()
}

// An attribute value template concatenates as it builds, exactly as
// xsl:value-of does, and for a while it was the way around the budget: the
// same doubling chain that XPDY0130 refuses through a text node ran to
// completion through an attribute. Charged at avt.eval now.
func TestAVTStringBombIsRefused(t *testing.T) {
	_, err := runByteSheet(t, avtDoublingSheet(28, "AAAAAAAAAA"))
	wantByteRefusal(t, err)
}

// The other half: an attribute built by a template that is merely large must
// still be built, or the charge is refusing legitimate stylesheets.
func TestALegitimateAVTStillBuilds(t *testing.T) {
	res, err := runByteSheet(t, avtDoublingSheet(3, "AAAAAAAAAA"))
	if err != nil {
		t.Fatalf("three doublings inside an attribute is 80 bytes and must "+
			"still build: %v", err)
	}
	if !strings.Contains(fmt.Sprint(res), "80") {
		t.Errorf("got %v, want the attribute to be 80 characters", res)
	}
}

func TestStylesheetStringBombIsRefused(t *testing.T) {
	sheet := doublingSheet(26, "AAAAAAAAAA")
	if len(sheet) > 3000 {
		t.Fatalf("the bomb is %d bytes; it is meant to be a few kilobytes",
			len(sheet))
	}
	_, err := runByteSheet(t, sheet)
	wantByteRefusal(t, err)
}

// Four more lines is ten gigabytes, and must be refused just as promptly
// rather than attempted.
func TestALargerStylesheetBombIsRefused(t *testing.T) {
	_, err := runByteSheet(t, doublingSheet(30, "AAAAAAAAAA"))
	wantByteRefusal(t, err)
}

// The hazard on the other side, and the worse one: a bound set too low
// refuses valid work. Measured, the largest string the XSLT 3.0 suite
// legitimately builds is 14,516,346 bytes and the DocBook xslTNG and XSpec
// corpora peak at 1,031,269 across 877 documents; 20 MB is above both and
// must still transform.
func TestALargeLegitimateStylesheetStringStillBuilds(t *testing.T) {
	out, err := runByteSheet(t, doublingSheet(21, "AAAAAAAAAA"))
	if err != nil {
		t.Fatalf("a 20 MB string -- larger than anything the suites or the "+
			"real-world corpora build -- was refused: %v", err)
	}
	if !strings.Contains(out, "20971520") {
		t.Errorf("output %q does not hold the expected length 20971520", out)
	}
}

// A stylesheet that emits a large string once per node of a large document is
// ordinary work -- a report concatenating thousands of rows -- and the budget
// must not turn the sum of many legitimate values into a refusal. The
// per-transform boundary is deliberately wide enough for that.
func TestManyModerateValuesAcrossOneTransformStillTransform(t *testing.T) {
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template match="/">
    <out><xsl:for-each select="1 to 2000">
      <row><xsl:value-of select="string-join(for $i in 1 to 200 return 'abcdefghij', '-')"/></row>
    </xsl:for-each></out>
  </xsl:template>
</xsl:stylesheet>`
	if _, err := runByteSheet(t, sheet); err != nil {
		t.Fatalf("2,000 rows of 2 KB each -- 4 MB of ordinary report output "+
			"-- was refused: %v", err)
	}
}

// The budget bounds ONE transform, not the lifetime of a compiled stylesheet.
// A counter armed but never reset would let a long-lived Stylesheet start
// refusing valid documents after enough use, which is a denial of service
// arriving on legitimate traffic.
//
// Each run here builds about 80 MB, so thirty of them is well past a gigabyte
// and a stylesheet that leaked would fail before the end.
func TestSequentialTransformsDoNotAccumulateBytes(t *testing.T) {
	sd, err := xdm.ParseString(doublingSheet(23, "AAAAAAAAAA"),
		xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	doc, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	for i := 0; i < 30; i++ {
		res, err := st.Transform(context.Background(), doc.Root,
			TransformOptions{})
		if err != nil {
			t.Fatalf("transform %d of 30 on one stylesheet was refused: "+
				"%v; the byte budget is leaking across transforms", i+1, err)
		}
		if !strings.Contains(res.String(), "83886080") {
			t.Fatalf("transform %d: output %q lacks the expected length",
				i+1, res.String())
		}
	}
}

// The reverse hazard: a budget reset so often that it never binds. One
// stylesheet run repeatedly must be refused every time -- if the refusal came
// only from residue an earlier run left, the first would pass.
func TestStylesheetByteRefusalIsReproducible(t *testing.T) {
	sd, err := xdm.ParseString(doublingSheet(26, "AAAAAAAAAA"),
		xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	doc, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	for i := 0; i < 3; i++ {
		_, err := st.Transform(context.Background(), doc.Root,
			TransformOptions{})
		wantByteRefusal(t, err)
	}
}

// A refused transform must leave no residue: a small transform on the same
// compiled stylesheet afterwards succeeds.
func TestASmallTransformAfterARefusedOneSucceeds(t *testing.T) {
	sd, _ := xdm.ParseString(doublingSheet(26, "AAAAAAAAAA"),
		xdm.ParseOptions{})
	big, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	doc, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	if _, err := big.Transform(context.Background(), doc.Root,
		TransformOptions{}); err == nil {
		t.Fatal("expected the bomb to be refused")
	}
	out, err := runByteSheet(t, `<xsl:stylesheet version="3.0" `+
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform">`+
		`<xsl:template match="/"><out>ok</out></xsl:template>`+
		`</xsl:stylesheet>`)
	if err != nil {
		t.Fatalf("a trivial transform after a refused one was refused too: "+
			"%v; the failed transform left its charges behind", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("output %q", out)
	}
}
