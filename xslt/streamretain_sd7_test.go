package xslt

import (
	goruntime "runtime"
	"sync"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

// syncMapLen is the number of entries in m.
func syncMapLen(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ any) bool { n++; return true })
	return n
}

// dropsTo collects garbage until m holds at most want entries, or a second
// passes; it returns the final count.
func dropsTo(m *sync.Map, want int) int {
	deadline := time.Now().Add(time.Second)
	for {
		goruntime.GC()
		if n := syncMapLen(m); n <= want || time.Now().After(deadline) {
			return n
		}
		time.Sleep(time.Millisecond)
	}
}

// retainedAfterCompiles compiles sheet n times, dropping each result, and
// reports the entries m gained while the stylesheets lived and the entries
// left once they are collected.
func retainedAfterCompiles(t *testing.T, m *sync.Map, sheet string, opts CompileOptions, n int) (gained, left int) {
	t.Helper()
	base := dropsTo(m, 0)
	for range n {
		tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: "file:///s/s.xsl"})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Compile(tree.Root, opts)
		if err != nil {
			t.Fatal(err)
		}
		gained = max(gained, syncMapLen(m)-base)
		goruntime.KeepAlive(s)
	}
	return gained, dropsTo(m, base) - base
}

// TestDynParamsDieWithStylesheet: a global xsl:param bound to
// accumulator-after#1 and called as $aa(...) is recorded per stylesheet
// document; the record must not outlive the compiled stylesheet.
func TestDynParamsDieWithStylesheet(t *testing.T) {
	sheet := mainSheet(`<xsl:param name="aa" select="accumulator-after#1"/>
	  <xsl:accumulator name="n" as="xs:integer" initial-value="0" streamable="yes">
	    <xsl:accumulator-rule match="rec" select="$value + 1"/></xsl:accumulator>
	  <xsl:mode streamable="yes" use-accumulators="#all" on-no-match="shallow-skip"/>
	  <xsl:template match="/"><o><xsl:value-of select="$aa('n')"/></o></xsl:template>`)
	gained, left := retainedAfterCompiles(t, &dynParams, sheet, CompileOptions{}, 20)
	if gained == 0 {
		t.Fatal("no dynParams entry recorded: the path under test did not run")
	}
	if left != 0 {
		t.Errorf("%d dynParams entries outlived their stylesheets", left)
	}
}

// TestPkgFrameRootsDieWithStylesheet: a used package with a frame function
// gets a pkgFrameState; neither it nor its entry may outlive the compile's
// stylesheet.
func TestPkgFrameRootsDieWithStylesheet(t *testing.T) {
	const pkg = `<xsl:package xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema"
		xmlns:j="urn:j" name="urn:j" package-version="1.0" version="3.0" exclude-result-prefixes="xs j">
		<xsl:mode name="m" streamable="yes" visibility="public"/>
		<xsl:function name="j:conv" as="xs:string" visibility="public" streamability="absorbing">
			<xsl:param name="input" as="node()"/>
			<xsl:value-of><xsl:apply-templates select="$input" mode="m"/></xsl:value-of>
		</xsl:function>
	</xsl:package>`
	const top = `<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:j="urn:j" version="3.0" exclude-result-prefixes="j">
		<xsl:use-package name="urn:j" package-version="1.0"/>
		<xsl:template name="main"><xsl:source-document streamable="yes" href="doc.xml"><out><xsl:sequence select="j:conv(.)"/></out></xsl:source-document></xsl:template>
	</xsl:stylesheet>`
	gained, left := retainedAfterCompiles(t, &pkgFrameRoots, top, CompileOptions{PackageResolver: fixedPackages{"urn:j": pkg}}, 20)
	if gained == 0 {
		t.Fatal("no pkgFrameRoots entry recorded: the path under test did not run")
	}
	if left != 0 {
		t.Errorf("%d pkgFrameRoots entries outlived their stylesheets", left)
	}
}
