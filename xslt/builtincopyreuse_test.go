package xslt

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// The built-in shallow-copy rule writing to Output reuses the builder of
// each element it has copied once the element is written (xdmsink.Release):
// before, every copied element cost a builder of its own, 144 bytes (audit
// A3-4). 1,000 <a x="1"><b>t</b></a>: before 2.1 allocations per <a> in the
// tree, 2.2 streamed; after 0.1 and 0.2.
const builtInCopyN = 1000

func builtInCopySource() string {
	return "<d>" + strings.Repeat(`<a x="1"><b>t</b></a>`, builtInCopyN) + "</d>"
}

const builtInCopySheet = `<xsl:stylesheet version="3.0"
	xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:mode streamable="yes" on-no-match="shallow-copy"/>
</xsl:stylesheet>`

func checkBuiltInCopy(t *testing.T, out string, allocs, limit float64) {
	t.Helper()
	if want := `<?xml version="1.0" encoding="UTF-8"?>` + builtInCopySource(); out != want {
		t.Fatalf("output %.80q..., want %.80q...", out, want)
	}
	per := allocs / builtInCopyN
	t.Logf("%.1f allocations per copied <a>", per)
	if per > limit {
		t.Errorf("%.1f allocations per copied <a>, want <= %.0f", per, limit)
	}
}

func TestBuiltInCopyReusesBuildersTree(t *testing.T) {
	s := compileString(t, builtInCopySheet)
	doc, err := xdm.ParseString(builtInCopySource(), xdm.ParseOptions{})
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
	checkBuiltInCopy(t, out.String(), allocs, 1)
}

func TestBuiltInCopyReusesBuildersStreamed(t *testing.T) {
	s := compileString(t, builtInCopySheet)
	src := builtInCopySource()
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
	checkBuiltInCopy(t, out.String(), allocs, 1)
}
