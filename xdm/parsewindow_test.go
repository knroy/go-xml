package xdm

import (
	"io"
	"runtime"
	"strings"
	"testing"
)

// A reader that knows its length gets read buffers and a first arena block
// sized to it. The tree must not depend on that: each document here parses
// to the same tree through a reader that hides its length (default sizes),
// including tokens longer than the window and values that overflow the first
// arena block.
func TestSizedParseMatchesUnsized(t *testing.T) {
	long := strings.Repeat("v", 900)
	docs := []string{
		`<r/>`,
		"<?xml version='1.0'?>\r\n<r a='1'>t\r\nu</r>",
		`<r xmlns="urn:d" xmlns:p="urn:p"><p:e a="` + long + `" b="` + long + `">` + strings.Repeat("text ", 300) + `</p:e><!--` + long + `--></r>`,
		`<r><e a="x"/>` + strings.Repeat(`<e a="`+strings.Repeat("y", 40)+`"/>`, 200) + `</r>`,
		"\xEF\xBB\xBF<r>bom</r>",
	}
	for _, d := range docs {
		for _, opts := range []ParseOptions{{}, {TrackPositions: true}, {AllowDOCTYPE: true}} {
			sized, err := ParseString(d, opts)
			if err != nil {
				t.Fatalf("%.40q: %v", d, err)
			}
			plain, err := Parse(io.MultiReader(strings.NewReader(d)), opts)
			if err != nil {
				t.Fatalf("%.40q unsized: %v", d, err)
			}
			if a, b := dump(sized.Root), dump(plain.Root); a != b {
				t.Errorf("%.40q %+v: sized parse\n%s\nunsized\n%s", d, opts, a, b)
			}
		}
	}
}

// A small document's parse allocated three 4 KB read windows whatever its
// size. Measured on this document: 47,576 bytes a parse with them, 36,942
// with windows sized to it (most of the rest is the 32 KB string arena,
// which is kept: see docs/profiling.md, "Measured and rejected").
func TestSmallParseAllocatesLittle(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation sizes are not stable under -race")
	}
	const doc = `<book xmlns="http://docbook.org/ns/docbook" version="5.0"><title>T</title><para>Some text.</para></book>`
	const n = 200
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	for range n {
		if _, err := ParseString(doc, ParseOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&m1)
	per := (m1.TotalAlloc - m0.TotalAlloc) / n
	t.Logf("%d bytes per parse", per)
	if per > 42<<10 {
		t.Errorf("%d bytes per parse of a %d-byte document, want at most 42 KB", per, len(doc))
	}
}
