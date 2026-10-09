package xslt

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// xsl:copy-of and xsl:sequence copy a node with a parent into an open element
// once, straight into the tree being built. They used to copy it detached
// first and let the builder copy that again: a fragment, its first record
// chunk and its name table per copied node, thrown away at once. The output
// is the same either way; what this pins is that the per-node cost no longer
// carries that intermediate tree.
func TestCopyIntoElementCopiesOnce(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:template match="/">
  <out xmlns:e="urn:e">
    <a><xsl:copy-of select="/r/rec"/></a>
    <b><xsl:sequence select="/r/rec"/></b>
    <c><xsl:copy-of select="/r/rec/node()"/></c>
  </out>
</xsl:template>
</xsl:stylesheet>`
	st, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(st.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src := func(n int) *xdm.Node {
		var b strings.Builder
		b.WriteString(`<r xmlns:p="urn:p">`)
		for range n {
			b.WriteString(`<rec p:k="1" xml:base="s/"><x/><!--c-->t<?pi d?></rec>`)
		}
		b.WriteString(`</r>`)
		tree, err := xdm.ParseString(b.String(), xdm.ParseOptions{BaseURI: "http://h/d.xml"})
		if err != nil {
			t.Fatal(err)
		}
		return tree.Root
	}
	run := func(doc *xdm.Node) string {
		res, err := s.Transform(context.Background(), doc, TransformOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := res.Serialize(&out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	const rec = `<rec xmlns:p="urn:p" p:k="1" xml:base="s/"><x/><!--c-->t<?pi d?></rec>`
	want := `<out xmlns:e="urn:e"><a>` + rec + `</a><b>` + rec + `</b><c><x xmlns:p="urn:p"/><!--c-->t<?pi d?></c></out>`
	if got := run(src(1)); !strings.HasSuffix(got, want) {
		t.Fatalf("got  %s\nwant …%s", got, want)
	}

	if raceEnabled {
		t.Skip("allocation counts are not stable under -race")
	}
	few, many := src(50), src(250)
	count := func(doc *xdm.Node) float64 {
		return testing.AllocsPerRun(10, func() { run(doc) })
	}
	// Each record is copied whole by xsl:copy-of and by xsl:sequence, and
	// child by child by xsl:copy-of. Copied once, that measured 16 allocations
	// a record (the result tree's growth, the copies' namespace frames, the
	// bindings each copy is given); copied twice, 72.
	per := (count(many) - count(few)) / 200
	t.Logf("%.2f allocations per copied record", per)
	if per > 30 {
		t.Errorf("%.1f allocations per copied record, want at most 30", per)
	}
}

// xsl:attribute made a parentless attribute node, a fragment tree of its
// own, to hand to validation even when validation was strip or preserve with
// no type, which leave a new attribute untyped. It now makes none then; under
// validation="strict" or a type it still assesses one (validate tests).
func TestAttributeBuildsNoNodeUnassessed(t *testing.T) {
	st, err := xdm.ParseString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:param name="n" select="1"/>
<xsl:template match="/">
  <out><xsl:for-each select="1 to $n"><e><xsl:attribute name="a" select="."/><xsl:attribute name="b" validation="preserve">x</xsl:attribute></e></xsl:for-each></out>
</xsl:template>
</xsl:stylesheet>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Compile(st.Root, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	run := func(n int) string {
		res, err := s.Transform(context.Background(), doc.Root, TransformOptions{
			Params: map[string]xdm.Sequence{"n": xdm.One(xdm.NewInteger(int64(n)))}})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := res.Serialize(&out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got, want := run(2), `<out><e a="1" b="x"/><e a="2" b="x"/></out>`; !strings.HasSuffix(got, want) {
		t.Fatalf("got  %s\nwant …%s", got, want)
	}
	if raceEnabled {
		t.Skip("allocation counts are not stable under -race")
	}
	count := func(n int) float64 { return testing.AllocsPerRun(10, func() { run(n) }) }
	per := (count(250) - count(50)) / 200
	t.Logf("%.2f allocations per element with two attributes", per)
	// Measured 35 with no node made, 47 with one per attribute.
	if per > 41 {
		t.Errorf("%.1f allocations per element, want at most 41", per)
	}
}
