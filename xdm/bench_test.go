package xdm

import (
	"strings"
	"testing"
)

// Parser benchmarks, with attention to the paths that were recently added.
//
// The entity figures matter because a document declaring an entity whose
// replacement text holds markup is parsed *twice*: once far enough to read the
// DOCTYPE, then again over the substituted source. That is a real cost and it
// should be visible here rather than discovered in production.

func benchParse(b *testing.B, src string, opts ParseOptions) {
	b.Helper()
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseString(src, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func benchDocument(n int) string {
	var sb strings.Builder
	sb.WriteString(`<root xmlns:a="http://example.com/a">`)
	for i := 0; i < n; i++ {
		sb.WriteString(`<item id="x" a:kind="k"><name>a name</name>` +
			`<value>123.45</value></item>`)
	}
	sb.WriteString(`</root>`)
	return sb.String()
}

// The ordinary case: no DOCTYPE, so none of the entity machinery runs.
func BenchmarkParsePlain(b *testing.B) {
	benchParse(b, benchDocument(500), ParseOptions{})
}

// AllowDOCTYPE with no DOCTYPE present. This is the cost of *permitting* the
// feature rather than using it: the source is teed into a buffer in case a
// re-parse turns out to be needed, and for this document it never is.
func BenchmarkParseAllowDoctypeUnused(b *testing.B) {
	benchParse(b, benchDocument(500), ParseOptions{AllowDOCTYPE: true})
}

// A DOCTYPE declaring text entities: the old path, one parse, expansion
// through the decoder's own entity map.
func BenchmarkParseTextEntities(b *testing.B) {
	src := `<!DOCTYPE root [<!ENTITY co "Example Ltd">]>` + benchDocumentEnt(500, "&co;")
	benchParse(b, src, ParseOptions{AllowDOCTYPE: true})
}

// A DOCTYPE declaring an entity that holds markup: the new path, which
// rewrites the source and parses it a second time.
func BenchmarkParseMarkupEntities(b *testing.B) {
	src := `<!DOCTYPE root [<!ENTITY co "<org>Example Ltd</org>">]>` +
		benchDocumentEnt(500, "&co;")
	benchParse(b, src, ParseOptions{AllowDOCTYPE: true})
}

func benchDocumentEnt(n int, ref string) string {
	var sb strings.Builder
	sb.WriteString(`<root>`)
	for i := 0; i < n; i++ {
		sb.WriteString(`<item><name>` + ref + `</name></item>`)
	}
	sb.WriteString(`</root>`)
	return sb.String()
}

// Position tracking retains the source for the life of the tree, which is the
// one option here that trades memory for a feature.
func BenchmarkParseTrackPositions(b *testing.B) {
	benchParse(b, benchDocument(500), ParseOptions{TrackPositions: true})
}
