package xslt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// TestSM03AccumReadChain: a chain of streamable accumulators, each rule
// reading the next one's value at its node, declared so that each read
// finds the next not stepped yet, runs streamed under a 1 MiB stack (the
// nesting it made, about 2.5 KB a link, needed 1.5 MB) with the values the
// chain gives.
func TestSM03AccumReadChain(t *testing.T) {
	const n = 600
	var b strings.Builder
	for i := range n {
		sel := fmt.Sprintf("accumulator-before('a%d') + 1", i+1)
		if i == n-1 {
			sel = "$value + 1"
		}
		fmt.Fprintf(&b, `<xsl:accumulator name="a%d" initial-value="0" streamable="yes" as="xs:integer">`+
			`<xsl:accumulator-rule match="rec" select="%s"/></xsl:accumulator>`, i, sel)
	}
	sheet := `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
 xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs">` + b.String() + `
<xsl:template name="main"><out><xsl:source-document streamable="yes" href="doc.xml" use-accumulators="#all">` +
		`<xsl:for-each select="/doc/rec"><v><xsl:value-of select="accumulator-before('a0')"/></v></xsl:for-each>` +
		`</xsl:source-document></out></xsl:template></xsl:stylesheet>`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.xml"), []byte("<doc><rec/><rec/><rec/></doc>"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString(sheet, xdm.ParseOptions{BaseURI: fileURIOf(filepath.Join(dir, "s.xsl"))})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(tree.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	docs, _ := NewFileResolver(dir)
	before := streamtrace.Snapshot()
	old := debug.SetMaxStack(1 << 20)
	res, err := s.Transform(context.Background(), nil, TransformOptions{InitialTemplate: "main", Documents: docs})
	debug.SetMaxStack(old)
	if err != nil {
		t.Fatal(err)
	}
	if tr := streamtrace.Snapshot().Sub(before); tr.Reruns != 0 || tr.Runs == [3]int64{} {
		t.Fatalf("not streamed: %+v", tr)
	}
	want := fmt.Sprintf("<out><v>%d</v><v>%d</v><v>%d</v></out>", n, n+1, n+2)
	if got := res.String(); !strings.Contains(got, want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}
