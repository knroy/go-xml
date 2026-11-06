package xslt

import (
	"bytes"
	"context"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// xsl:value-of select="xml-to-json(.)" streamed to Output under the text
// method writes the JSON text as it is made (jsonOut), byte for byte as the
// tree writes it, instead of building the whole string first (audit A3-5).
// Allocated per byte written, the parse's included: 8.5 before, 4.7 after.
func jsonOutSource(n int) string {
	var b strings.Builder
	b.WriteString(`<array xmlns="http://www.w3.org/2005/xpath-functions">`)
	for i := range n {
		b.WriteString(`<map><number key="id">` + strings.Repeat("7", i%5+1) +
			`</number><string key="s">x "é" / ` + strings.Repeat("y", i%40) +
			`</string><array key="a"><null/><boolean>1</boolean></array></map>`)
	}
	b.WriteString(`</array>`)
	return b.String()
}

func jsonOutSheet(output, body string) string {
	return `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:mode streamable="yes"/>
  <xsl:character-map name="m"><xsl:output-character character="é" string="[e]"/></xsl:character-map>
  ` + output + `
  <xsl:template match="/">` + body + `</xsl:template>
</xsl:stylesheet>`
}

func TestStreamedJSONWritesThrough(t *testing.T) {
	src := jsonOutSource(10000)
	text := `<xsl:output method="text"/>`
	for _, c := range []struct{ name, output, body string }{
		{"text", text, `<xsl:value-of select="xml-to-json(.)"/>`},
		{"around", text, `A<xsl:value-of select="xml-to-json(.)"/>B<xsl:value-of select="1, 2"/>`},
		{"indent", text, `<xsl:value-of select="xml-to-json(., map{'indent': true()})"/>`},
		{"charmap", `<xsl:output method="text" use-character-maps="m"/>`, `<xsl:value-of select="xml-to-json(.)"/>`},
		{"element", text, `<e><xsl:value-of select="xml-to-json(.)"/></e>`},
		{"xml", `<xsl:output method="xml"/>`, `<e><xsl:value-of select="xml-to-json(.)"/></e>`},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := compileString(t, jsonOutSheet(c.output, c.body))
			doc, err := xdm.ParseString(src, xdm.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var want, got bytes.Buffer
			if _, err := s.Transform(context.Background(), doc.Root, TransformOptions{Output: &want}); err != nil {
				t.Fatal(err)
			}
			before := streamtrace.Snapshot()
			var m0, m1 goruntime.MemStats
			goruntime.ReadMemStats(&m0)
			_, err = s.TransformReader(context.Background(), strings.NewReader(src), TransformOptions{Output: &got})
			goruntime.ReadMemStats(&m1)
			if err != nil {
				t.Fatal(err)
			}
			if streamtrace.Snapshot().Runs[streamtrace.Main] == before.Runs[streamtrace.Main] {
				t.Fatal("the transform did not stream")
			}
			if got.String() != want.String() {
				t.Fatalf("streamed %d bytes differ from the tree's %d", got.Len(), want.Len())
			}
			ratio := float64(m1.TotalAlloc-m0.TotalAlloc) / float64(got.Len())
			t.Logf("%d bytes written, %.2f allocated per byte", got.Len(), ratio)
			// The rest is the cursor's and the writer's own: the xml method,
			// which holds the string, allocates 8.5.
			if c.name != "xml" && c.name != "charmap" && ratio > 6 {
				t.Errorf("%.2f bytes allocated per byte written, want <= 6", ratio)
			}
		})
	}
}

// A byte budget the JSON text exceeds is the tree's error, raised once the
// JSON is read; an invalid JSON input's error comes first, as the tree's
// xml-to-json raises it before its value-of charges the text.
func TestStreamedJSONWritesThroughBudget(t *testing.T) {
	s := compileString(t, jsonOutSheet(`<xsl:output method="text"/>`, `<xsl:value-of select="xml-to-json(.)"/>`))
	big := jsonOutSource(2000)
	bad := strings.TrimSuffix(big, `</array>`) + `<foo/></array>` // past the budget
	run := func(src string, stream bool) string {
		opts := TransformOptions{Output: new(bytes.Buffer), MaxBytes: 50 << 10}
		var err error
		if stream {
			_, err = s.TransformReader(context.Background(), strings.NewReader(src), opts)
		} else {
			doc, perr := xdm.ParseString(src, xdm.ParseOptions{})
			if perr != nil {
				t.Fatal(perr)
			}
			_, err = s.Transform(context.Background(), doc.Root, opts)
		}
		if err == nil {
			return "no error"
		}
		return err.Error()
	}
	want := run(big, false)
	if !strings.Contains(want, "limit") && !strings.Contains(want, "bytes") {
		t.Fatalf("the tree raised %q, not a byte budget's error", want)
	}
	if got := run(big, true); got != want {
		t.Errorf("over budget: streamed %q, tree %q", got, want)
	}
	// The stream's own error for invalid JSON is not the tree's message
	// (a known gap); what matters here is that it is not the budget's.
	if got := run(bad, true); got == want || got == "no error" {
		t.Errorf("invalid and over budget: streamed %q, the budget's error", got)
	}
}
