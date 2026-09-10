package xslt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// selfCallingSheet transforms itself: the named template calls fn:transform on
// the very text it was compiled from, passing that text down again as a
// parameter so every level can do the same.
const selfCallingSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:param name="sheet" as="xs:string" select="''"/>
  <xsl:template name="go">
    <xsl:sequence select="transform(map{
      'stylesheet-text': $sheet,
      'initial-template': QName('','go'),
      'stylesheet-params': map{QName('','sheet'): $sheet},
      'delivery-format':'raw'})?output"/>
  </xsl:template>
</xsl:stylesheet>`

// runNested compiles src and runs it over a trivial document.
func runNested(t *testing.T, src string, opts TransformOptions) (*Result, error) {
	t.Helper()
	sd, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing stylesheet: %v", err)
	}
	st, err := Compile(sd.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("compiling stylesheet: %v", err)
	}
	doc, err := xdm.ParseString(`<d/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing source: %v", err)
	}
	return st.Transform(context.Background(), doc.Root, opts)
}

// rt.depth is per-runtime, and before this was fixed every nested
// fn:transform built a new runtime starting at zero -- so MaxDepth bounded
// recursion within one level and never across levels, and a stylesheet
// calling fn:transform on itself exhausted the Go stack:
//
//	runtime: goroutine stack exceeds 1000000000-byte limit
//	fatal error: stack overflow
//
// That is a runtime fatal rather than a panic. recover() does not catch it, so
// the host process died, and it was reachable from any untrusted stylesheet.
// The bound must therefore be enforced as an ordinary error.
//
// MaxDepth is small and explicit here on purpose: if the charge is ever lost
// again this test does not fail, it takes the whole test binary down with it,
// and a low bound reaches the refusal long before the stack is in danger.
func TestSelfCallingTransformIsRefused(t *testing.T) {
	_, err := runNested(t, selfCallingSheet, TransformOptions{
		MaxDepth:        5,
		InitialTemplate: "go",
		Params: map[string]xdm.Sequence{
			"sheet": {xdm.NewString(selfCallingSheet)},
		},
	})
	if err == nil {
		t.Fatal("a stylesheet transforming itself was not refused; the " +
			"nesting budget did not bind")
	}
	if code := xdm.ErrorCode(err); code != "XPDY0001" {
		t.Errorf("code = %q, want XPDY0001 (error: %v)", code, err)
	}
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller cannot "+
			"tell a refusal to compute from a bad stylesheet", err)
	}
	if !strings.Contains(err.Error(), "fn:transform nesting") {
		t.Errorf("message %q is not the nesting refusal", err)
	}
}

// The other half of the bargain: bounding the recursion must not break
// fn:transform. This nests three deep -- outer calls inner calls innermost --
// and the innermost value has to come back out through both.
func TestLegitimateNestingStillTransforms(t *testing.T) {
	const innermost = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template name="go"><deep>42</deep></xsl:template>
</xsl:stylesheet>`
	inner := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:param name="sheet" as="xs:string" select="''"/>
  <xsl:template name="go">
    <mid><xsl:sequence select="transform(map{
      'stylesheet-text': $sheet,
      'initial-template': QName('','go'),
      'delivery-format':'raw'})?output"/></mid>
  </xsl:template>
</xsl:stylesheet>`
	outer := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:param name="inner" as="xs:string" select="''"/>
  <xsl:param name="innermost" as="xs:string" select="''"/>
  <xsl:template name="go">
    <top><xsl:sequence select="transform(map{
      'stylesheet-text': $inner,
      'initial-template': QName('','go'),
      'stylesheet-params': map{QName('','sheet'): $innermost},
      'delivery-format':'raw'})?output"/></top>
  </xsl:template>
</xsl:stylesheet>`
	res, err := runNested(t, outer, TransformOptions{
		MaxDepth:        50,
		InitialTemplate: "go",
		Params: map[string]xdm.Sequence{
			"inner":     {xdm.NewString(inner)},
			"innermost": {xdm.NewString(innermost)},
		},
	})
	if err != nil {
		t.Fatalf("three legitimate levels of fn:transform were refused: %v", err)
	}
	got := res.String()
	if !strings.Contains(got, "42") {
		t.Errorf("output %q does not carry the innermost result 42", got)
	}
	for _, want := range []string{"<top", "<mid>", "<deep>42</deep>"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q is missing %s; a level was lost", got, want)
		}
	}
}

// The budget bounds one CHAIN of nesting, not the lifetime of a runtime. A
// counter charged but never released would let a template calling fn:transform
// in a loop start refusing after enough iterations, which is a denial of
// service arriving on entirely legitimate work. Same shape as
// TestSequentialTransformsDoNotAccumulateBytes.
//
// MaxDepth is 12 against 200 sequential calls, so a counter that accumulated
// would fail long before the end.
func TestSequentialTransformsDoNotAccumulateDepth(t *testing.T) {
	const leaf = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template name="go"><n>1</n></xsl:template>
</xsl:stylesheet>`
	driver := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:param name="leaf" as="xs:string" select="''"/>
  <xsl:template name="go">
    <out><xsl:for-each select="1 to 200">
      <xsl:sequence select="transform(map{
        'stylesheet-text': $leaf,
        'initial-template': QName('','go'),
        'delivery-format':'raw'})?output"/>
    </xsl:for-each></out>
  </xsl:template>
</xsl:stylesheet>`
	res, err := runNested(t, driver, TransformOptions{
		MaxDepth:        12,
		InitialTemplate: "go",
		Params:          map[string]xdm.Sequence{"leaf": {xdm.NewString(leaf)}},
	})
	if err != nil {
		t.Fatalf("200 sequential fn:transform calls were refused, so the "+
			"nesting charge is accumulating instead of unwinding: %v", err)
	}
	if n := strings.Count(res.String(), "<n>1</n>"); n != 200 {
		t.Errorf("got %d results, want 200 (output %q)", n, res.String())
	}
}
