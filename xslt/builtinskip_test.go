package xslt

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// A skipping mode with a rule gives the nodes no rule matches the built-in
// rule without making them a focus (applyBuiltInChild, liveBuiltIn, leaf):
// the rule's own node still gets one. Before, each skipped element, its
// attribute and its text each cost a runtime, a context and a host: 9.1
// allocations per <a x="1">t</a> in the tree, 9.2 streamed; after, 0.1 and
// 3.2 (audit A3-3).
const builtInSkipSheet = `<xsl:stylesheet version="3.0"
	xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:mode streamable="yes" on-no-match="shallow-skip"/>
	<xsl:template match="b"><r p="{position()}"><xsl:value-of select="."/></r></xsl:template>
</xsl:stylesheet>`

const builtInSkipN = 1000

func builtInSkipSource() string {
	return "<d>" + strings.Repeat(`<a x="1">t</a>`, builtInSkipN) + "<b>z</b></d>"
}

const builtInSkipWant = `<?xml version="1.0" encoding="UTF-8"?><r p="1001">z</r>`

func TestBuiltInSkipMakesNoFocusTree(t *testing.T) {
	s := compileString(t, builtInSkipSheet)
	doc, err := xdm.ParseString(builtInSkipSource(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	allocs := testing.AllocsPerRun(3, func() {
		out.Reset()
		_, err = s.Transform(context.Background(), doc.Root, TransformOptions{Output: &out})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != builtInSkipWant {
		t.Fatalf("output %q, want %q", got, builtInSkipWant)
	}
	t.Logf("%.1f allocations per skipped element", allocs/builtInSkipN)
	if per := allocs / builtInSkipN; per > 2 {
		t.Errorf("%.1f allocations per skipped element, want <= 2", per)
	}
}

func TestBuiltInSkipMakesNoFocusStreamed(t *testing.T) {
	s := compileString(t, builtInSkipSheet)
	src := builtInSkipSource()
	var out bytes.Buffer
	before := streamtrace.Snapshot()
	var err error
	allocs := testing.AllocsPerRun(3, func() {
		out.Reset()
		_, err = s.TransformReader(context.Background(), strings.NewReader(src), TransformOptions{Output: &out})
	})
	if err != nil {
		t.Fatal(err)
	}
	if streamtrace.Snapshot().Runs[streamtrace.Main] == before.Runs[streamtrace.Main] {
		t.Fatal("the transform did not stream")
	}
	if got := out.String(); got != builtInSkipWant {
		t.Fatalf("output %q, want %q", got, builtInSkipWant)
	}
	t.Logf("%.1f allocations per skipped element", allocs/builtInSkipN)
	if per := allocs / builtInSkipN; per > 4 {
		t.Errorf("%.1f allocations per skipped element, want <= 4", per)
	}
}
