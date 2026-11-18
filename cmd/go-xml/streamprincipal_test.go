package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/internal/fileuri"
	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xslt"
)

// The transform command reads its source through Stylesheet.TransformReader,
// so a stylesheet whose initial mode is streamable (xsl:mode
// streamable="yes") streams the principal input rather than building its
// tree first (issue #40). Every other stylesheet takes the same call, which
// parses the document and runs on the tree. Each case counts the principal
// inputs the engine streamed, which is the only thing that tells the two
// apart: the output is the same either way. Paths are built under t.TempDir
// with filepath, so the cases run unchanged on Windows.

const streamSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:mode streamable="yes" on-no-match="shallow-skip"/>
  <xsl:output method="text"/>
  <xsl:template match="item"><xsl:value-of select="."/>;</xsl:template>
</xsl:stylesheet>`

// treeSheet is streamSheet without xsl:mode streamable="yes".
const treeSheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:mode on-no-match="shallow-skip"/>
  <xsl:output method="text"/>
  <xsl:template match="item"><xsl:value-of select="."/>;</xsl:template>
</xsl:stylesheet>`

func fileResolver(t *testing.T, roots ...string) *xslt.FileResolver {
	t.Helper()
	r, err := xslt.NewFileResolver(roots...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// streamRun runs transformOne over in with cfg and returns its output and
// how many principal inputs it streamed.
func streamRun(t *testing.T, sheetSrc, in string, cfg transformCfg) (string, int64) {
	t.Helper()
	dir := t.TempDir()
	sheetPath := filepath.Join(dir, "s.xsl")
	writeSchema(t, sheetPath, sheetSrc)
	if cfg.resolver == nil {
		cfg.resolver = fileResolver(t, filepath.Dir(in), dir)
	}
	sheet, err := compileStylesheet(sheetPath, cfg.resolver, schemaFiles{cfg.resolver}, "", xslt.Compatibility{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.timeout = time.Minute
	out := filepath.Join(dir, "out.txt")
	before := streamtrace.Snapshot().Runs[streamtrace.Main]
	if err := transformOne(sheet, in, out, cfg); err != nil {
		t.Fatal(err)
	}
	streamed := streamtrace.Snapshot().Runs[streamtrace.Main] - before
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b)), streamed
}

func TestTransformStreamsPrincipalInput(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "sub dir", "in.xml")
	writeSchema(t, in, `<list><item>a</item><item>b</item><item>c</item></list>`)

	got, streamed := streamRun(t, streamSheet, in, transformCfg{})
	if got != "a;b;c;" {
		t.Errorf("streamed output = %q, want %q", got, "a;b;c;")
	}
	if streamed != 1 {
		t.Errorf("a streamable initial mode streamed %d principal inputs, want 1", streamed)
	}

	got, streamed = streamRun(t, treeSheet, in, transformCfg{})
	if got != "a;b;c;" {
		t.Errorf("tree output = %q, want %q", got, "a;b;c;")
	}
	if streamed != 0 {
		t.Errorf("a non-streamable mode streamed %d principal inputs, want 0", streamed)
	}
}

// -xinclude needs the finished tree, so the input is parsed and not
// streamed, and the inclusion is still made.
func TestTransformXIncludeDoesNotStream(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, filepath.Join(dir, "part.xml"), `<item>b</item>`)
	in := filepath.Join(dir, "in.xml")
	writeSchema(t, in, `<list xmlns:xi="http://www.w3.org/2001/XInclude"><item>a</item><xi:include href="part.xml"/></list>`)

	got, streamed := streamRun(t, streamSheet, in, transformCfg{xinclude: true})
	if got != "a;b;" {
		t.Errorf("output = %q, want %q", got, "a;b;")
	}
	if streamed != 0 {
		t.Errorf("-xinclude streamed %d principal inputs, want 0", streamed)
	}
}

// -validate is the library's source validation: a streamed input is
// validated as it is read and still typed, and an invalid one still fails.
func TestTransformValidateStreams(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, filepath.Join(dir, "order.xsd"), priceSchema)
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
    xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:import-schema schema-location="` + fileuri.Of(filepath.Join(dir, "order.xsd")) + `"/>
  <xsl:mode streamable="yes" on-no-match="shallow-skip"/>
  <xsl:output method="text"/>
  <xsl:template match="price"><xsl:value-of select="data(.) instance of xs:decimal"/></xsl:template>
</xsl:stylesheet>`
	in := filepath.Join(dir, "in.xml")
	writeSchema(t, in, `<order><price>10.50</price><qty>3</qty></order>`)

	for _, mode := range []string{"strict", "lax"} {
		cfg := transformCfg{validate: mode, resolver: fileResolver(t, dir)}
		got, streamed := streamRun(t, sheet, in, cfg)
		if got != "true" {
			t.Errorf("-validate %s: typed = %q, want true", mode, got)
		}
		if streamed != 1 {
			t.Errorf("-validate %s streamed %d principal inputs, want 1", mode, streamed)
		}
	}
}
