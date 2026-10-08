package xdm

import (
	"fmt"
	"strings"
	"testing"
)

// Text, attribute values and comments were one heap object each, which made
// a parse cost about one allocation per value. They now share arena blocks
// and the node chunks, so a document of thousands of elements costs a few
// hundred allocations.
func TestParseValuesShareBlocks(t *testing.T) {
	var b strings.Builder
	b.WriteString("<r>")
	for i := range 5000 {
		fmt.Fprintf(&b, "<e a=\"v%d\" b=\"w%d\">text %d<!--c%d--></e>", i, i, i, i)
	}
	b.WriteString("</r>")
	doc := b.String()
	n := testing.AllocsPerRun(5, func() {
		if _, err := ParseString(doc, ParseOptions{MaxBytes: -1, MaxNodes: -1}); err != nil {
			t.Fatal(err)
		}
	})
	// 25,000 values; before, at least that many allocations.
	if n > 2500 {
		t.Errorf("parsing 5,000 elements with 25,000 values allocated %.0f times, want under 2,500", n)
	}

	// The values are copies: each one still reads back as written once the
	// decoder has moved on and reused its buffers.
	tree, err := ParseString(doc, ParseOptions{MaxBytes: -1, MaxNodes: -1})
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range tree.Root.Children[0].Children {
		got := fmt.Sprintf("%s %s %s %s", e.Attrs[0].Value, e.Attrs[1].Value, e.Children[0].Value, e.Children[1].Value)
		if want := fmt.Sprintf("v%d w%d text %d c%d", i, i, i, i); got != want {
			t.Fatalf("element %d holds %q, want %q", i, got, want)
		}
	}
}

// An end tag is compared with its start tag by prefix and local name before
// any lexical name is built; the verdicts and messages are the lexical ones.
func TestEndTagMatching(t *testing.T) {
	for _, c := range []struct{ doc, err string }{
		{`<p:a xmlns:p="urn:p"></p:a>`, ""},
		{`<a></a >`, ""},
		{`<p:a xmlns:p="urn:p"></a>`, `parse XML: element "p:a" closed by end element "a"`},
		{`<a></p:a>`, `parse XML: element "a" closed by end element "p:a"`},
		{`<p:a xmlns:p="urn:p" xmlns:q="urn:p"></q:a>`, `parse XML: element "p:a" closed by end element "q:a"`},
		{`<a><b></a>`, `parse XML: element "b" closed by end element "a"`},
	} {
		_, err := ParseString(c.doc, ParseOptions{})
		if got := fmt.Sprint(err); (c.err == "" && err != nil) || (c.err != "" && got != c.err) {
			t.Errorf("%s: err = %v, want %q", c.doc, err, c.err)
		}
	}
}
