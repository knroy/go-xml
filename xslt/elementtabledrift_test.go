package xslt

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The element table is transcribed from the element syntax summaries of the
// vendored Last Call draft, testdata/xslt30-test/specs/xslt-lcwd30.xml. These
// tests pin four places where the transcription had drifted from it, each of
// which either rejected a conforming stylesheet or accepted a name the
// grammar does not define.

// compileDrift compiles src and returns the error, if any.
//
// The neighbouring compileSheet fatals on a compile error, which cannot
// express the half of these tests that require one.
func compileDrift(t *testing.T, src string) error {
	t.Helper()
	_, err := Compile(mustParse(t, src), CompileOptions{})
	return err
}

// TestFunctionCacheEnumeration covers xsl:function/@cache.
//
// The summary at xslt-lcwd30.xml:14648 types it cache? = "full" | "partial" |
// "no", and 10.3.8 at 14963-14970 names all three values in prose. The W3C
// suite disagrees: schema-for-xslt30.xsd:803 types @cache as xsl:yes-or-no
// and nine stylesheets write cache="yes", function-1031 among them. The table
// carries the union, so a stylesheet written against either source compiles.
func TestFunctionCacheEnumeration(t *testing.T) {
	sheet := func(cache string) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    xmlns:x="http://xxx.com/" xmlns:xs="http://www.w3.org/2001/XMLSchema"
		    version="3.0" exclude-result-prefixes="xs x">
		  <xsl:function name="x:f" ` + cache + ` as="xs:integer">
		    <xsl:param name="n" as="xs:integer"/>
		    <xsl:sequence select="$n"/>
		  </xsl:function>
		  <xsl:template name="main"><out><xsl:value-of select="x:f(1)"/></out></xsl:template>
		</xsl:stylesheet>`
	}
	// The spec's three and the suite's boolean spellings all compile.
	for _, v := range []string{
		`cache="full"`, `cache="partial"`, `cache="no"`,
		`cache="yes"`, `cache="true"`, `cache="false"`, `cache="1"`, `cache="0"`,
	} {
		if err := compileDrift(t, sheet(v)); err != nil {
			t.Errorf("%s was refused: %v", v, err)
		}
	}
	// A value from neither vocabulary is still XTSE0020. Without this the
	// test would pass against an empty enumeration, which accepts everything.
	err := compileDrift(t, sheet(`cache="sometimes"`))
	if err == nil || !strings.Contains(err.Error(), "XTSE0020") {
		t.Errorf("cache=\"sometimes\" should be XTSE0020, got %v", err)
	}
}

// TestFunctionCacheFullMemoises covers the runtime half of the same fix.
//
// 10.3.8 says cache="full" "encourages the processor to retain memory of all
// previous calls of this function ... and to reuse results from this memory".
// Accepting the value in the grammar while reading it with isYes -- which
// matches yes/true/1 only -- gave a conforming cache="full" the unmemoised
// path, the opposite of what the value asks for. compile.go reads it through
// cacheMemoises instead.
//
// The assertion is on time rather than on a counter because memoisation has
// no other observable effect here: fib(27) by naive double recursion is some
// 600k calls unmemoised and 27 memoised, a difference of four orders of
// magnitude. The threshold is loose enough to survive a loaded machine.
func TestFunctionCacheFullMemoises(t *testing.T) {
	run := func(t *testing.T, cache string) time.Duration {
		t.Helper()
		src := `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    xmlns:x="http://xxx.com/" xmlns:xs="http://www.w3.org/2001/XMLSchema"
		    version="3.0" exclude-result-prefixes="xs x">
		  <xsl:function name="x:fib" ` + cache + ` as="xs:integer">
		    <xsl:param name="n" as="xs:integer"/>
		    <xsl:sequence select="if ($n = (1,2)) then 1 else x:fib($n - 1) + x:fib($n - 2)"/>
		  </xsl:function>
		  <xsl:template name="main"><out><xsl:value-of select="x:fib(27)"/></out></xsl:template>
		</xsl:stylesheet>`
		sh, err := Compile(mustParse(t, src), CompileOptions{})
		if err != nil {
			t.Fatalf("Compile %s: %v", cache, err)
		}
		start := time.Now()
		res, err := sh.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main"})
		if err != nil {
			t.Fatalf("Transform %s: %v", cache, err)
		}
		if !strings.Contains(res.String(), "196418") {
			t.Fatalf("%s: wrong answer %q", cache, res.String())
		}
		return time.Since(start)
	}
	// Each memoising spelling must be far faster than the unmemoised
	// default. Measuring the default in the same run is what makes this a
	// comparison rather than a fixed timing assertion.
	slow := run(t, `cache="no"`)
	for _, v := range []string{`cache="full"`, `cache="partial"`, `cache="yes"`} {
		if fast := run(t, v); fast*20 > slow {
			t.Errorf("%s took %v against cache=\"no\" %v: it did not memoise",
				v, fast, slow)
		}
	}
}

// TestFunctionIdentitySensitive covers xsl:function/@identity-sensitive,
// which the table did not define at all.
//
// The summary at xslt-lcwd30.xml:14648 types it identity-sensitive? =
// boolean; 10.3.7 at 14923-14929 says "the attribute identity-sensitive=\"no\"
// may be specified (the default is yes)"; and the override-compatibility rule
// at 4574-4575 reads the value back. It sits in the same summary as
// @visibility and @streamability, and was the third member missed when those
// were added.
func TestFunctionIdentitySensitive(t *testing.T) {
	sheet := func(attr string) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
		    xmlns:x="http://xxx.com/" xmlns:xs="http://www.w3.org/2001/XMLSchema"
		    version="3.0" exclude-result-prefixes="xs x">
		  <xsl:function name="x:f" ` + attr + ` as="xs:integer">
		    <xsl:param name="n" as="xs:integer"/>
		    <xsl:sequence select="$n"/>
		  </xsl:function>
		  <xsl:template name="main"><out><xsl:value-of select="x:f(1)"/></out></xsl:template>
		</xsl:stylesheet>`
	}
	for _, v := range []string{
		`identity-sensitive="no"`, `identity-sensitive="yes"`,
		`identity-sensitive="true"`, `identity-sensitive="false"`,
		`identity-sensitive="1"`, `identity-sensitive="0"`,
	} {
		if err := compileDrift(t, sheet(v)); err != nil {
			t.Errorf("%s was refused: %v", v, err)
		}
	}
	// It is a boolean, so a non-boolean is XTSE0020 rather than accepted.
	// This is what distinguishes "defined as a boolean" from "defined with no
	// enumeration", which would swallow anything.
	err := compileDrift(t, sheet(`identity-sensitive="maybe"`))
	if err == nil || !strings.Contains(err.Error(), "XTSE0020") {
		t.Errorf("identity-sensitive=\"maybe\" should be XTSE0020, got %v", err)
	}
}

// TestMergeSourceAcceptsBothSpellings covers xsl:merge-source.
//
// The vendored draft types the attribute for-each-stream at
// xslt-lcwd30.xml:19881 and uses that name 33 times, never for-each-source.
// The Recommendation renamed it, and the suite follows the later name: 56
// files write for-each-source against 6 for for-each-stream. The runtime
// already reads either spelling (streaminstructions.go:2096, 2159), so the
// table listing only one was the sole thing rejecting the other.
func TestMergeSourceAcceptsBothSpellings(t *testing.T) {
	sheet := func(attr string) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		  <xsl:template name="main">
		    <out>
		      <xsl:merge>
		        <xsl:merge-source ` + attr + ` select="log/e" streamable="yes">
		          <xsl:merge-key select="@k"/>
		        </xsl:merge-source>
		        <xsl:merge-action><xsl:sequence select="."/></xsl:merge-action>
		      </xsl:merge>
		    </out>
		  </xsl:template>
		</xsl:stylesheet>`
	}
	for _, v := range []string{
		`for-each-source="'a.xml'"`,
		`for-each-stream="'a.xml'"`,
	} {
		if err := compileDrift(t, sheet(v)); err != nil {
			t.Errorf("%s was refused: %v", v, err)
		}
	}
	// A third spelling is still XTSE0090, so accepting two names is not the
	// same as accepting any name.
	err := compileDrift(t, sheet(`for-each-file="'a.xml'"`))
	if err == nil || !strings.Contains(err.Error(), "XTSE0090") {
		t.Errorf("for-each-file should be XTSE0090, got %v", err)
	}
}

// TestAccumulatorRulePriorityRejected covers xsl:accumulator-rule/@priority,
// which the table accepted and the grammar does not define.
//
// The summary at xslt-lcwd30.xml:22181 names match, phase? and select? and
// nothing else, and no prose in the draft associates a priority with an
// accumulator rule. No suite case writes one. Accepting it silently swallowed
// a misspelling of @phase.
//
// The suite's schema is stale on this element rather than authoritative: it
// omits @select too, which both the summary and this table have -- so the
// summary is taken as primary.
func TestAccumulatorRulePriorityRejected(t *testing.T) {
	sheet := func(attr string) string {
		return `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" version="3.0">
		  <xsl:accumulator name="a" initial-value="0">
		    <xsl:accumulator-rule match="x" ` + attr + ` select="1"/>
		  </xsl:accumulator>
		  <xsl:template name="main"><out/></xsl:template>
		</xsl:stylesheet>`
	}
	err := compileDrift(t, sheet(`priority="2"`))
	if err == nil || !strings.Contains(err.Error(), "XTSE0090") {
		t.Errorf("priority on xsl:accumulator-rule should be XTSE0090, got %v", err)
	}
	// The three attributes the summary does define must still be accepted,
	// so the fix removed one name rather than breaking the element.
	for _, v := range []string{`phase="start"`, `phase="end"`, ``} {
		if err := compileDrift(t, sheet(v)); err != nil {
			t.Errorf("accumulator-rule with %q was refused: %v", v, err)
		}
	}
}
