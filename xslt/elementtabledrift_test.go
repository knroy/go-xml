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
// The Recommendation's summary at 10.3 types it cache? = boolean, and the
// suite's schema-for-xslt30.xsd:803 agrees. The Last Call draft's "full" |
// "partial" | "no" was withdrawn, so those two spellings are XTSE0020 like
// any other value outside the enumeration.
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
	for _, v := range []string{
		`cache="no"`, `cache="yes"`, `cache="true"`, `cache="false"`,
		`cache="1"`, `cache="0"`,
	} {
		if err := compileDrift(t, sheet(v)); err != nil {
			t.Errorf("%s was refused: %v", v, err)
		}
	}
	// The draft's spellings and an invented one are all XTSE0020. Without
	// this the test would pass against an empty enumeration, which accepts
	// everything.
	for _, v := range []string{`cache="full"`, `cache="partial"`, `cache="sometimes"`} {
		err := compileDrift(t, sheet(v))
		if err == nil || !strings.Contains(err.Error(), "XTSE0020") {
			t.Errorf("%s should be XTSE0020, got %v", v, err)
		}
	}
}

// TestFunctionCacheMemoises covers the runtime half of @cache.
//
// 10.3.8 says a true value "encourages the processor to retain memory of all
// previous calls of this function ... and to reuse results from this
// memory". compile.go reads the hint through cacheMemoises, and every
// boolean true spelling must reach the memoised path.
//
// The assertion is on time rather than on a counter because memoisation has
// no other observable effect here: fib(27) by naive double recursion is some
// 600k calls unmemoised and 27 memoised, a difference of four orders of
// magnitude. The threshold is loose enough to survive a loaded machine.
func TestFunctionCacheMemoises(t *testing.T) {
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
	for _, v := range []string{`cache="yes"`, `cache="true"`, `cache="1"`} {
		if fast := run(t, v); fast*20 > slow {
			t.Errorf("%s took %v against cache=\"no\" %v: it did not memoise",
				v, fast, slow)
		}
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
