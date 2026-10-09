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
	for i, e := range kids(kids(tree.Root)[0]) {
		got := fmt.Sprintf("%s %s %s %s", attrsOf(e)[0].Value(), attrsOf(e)[1].Value(), kids(e)[0].Value(), kids(e)[1].Value())
		if want := fmt.Sprintf("v%d w%d text %d c%d", i, i, i, i); got != want {
			t.Fatalf("element %d holds %q, want %q", i, got, want)
		}
	}
}

// Namespace declarations and processing instructions were one heap object
// each (two, with the namespace slice and the PI's value), where every other
// node of a parse comes from the shared node chunks.
func TestParseNamespacesAndPIsShareChunks(t *testing.T) {
	var b strings.Builder
	b.WriteString("<r>")
	for i := range 2000 {
		fmt.Fprintf(&b, `<e xmlns:p="urn:p%d"><?pi data %d?></e>`, i, i)
	}
	b.WriteString("</r>")
	doc := b.String()
	n := testing.AllocsPerRun(5, func() {
		if _, err := ParseString(doc, ParseOptions{MaxBytes: -1, MaxNodes: -1}); err != nil {
			t.Fatal(err)
		}
	})
	// 2,000 namespace nodes and 2,000 PIs: 10,070 allocations before, 2,110
	// after.
	if n > 3000 {
		t.Errorf("parsing 2,000 namespace declarations and PIs allocated %.0f times, want under 3,000", n)
	}
	tree, err := ParseString(doc, ParseOptions{MaxBytes: -1, MaxNodes: -1})
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range kids(kids(tree.Root)[0]) {
		ns, pi := nsOf(e)[0], kids(e)[0]
		if ns.Parent() != e || ns.Name().Local != "p" || ns.Value() != fmt.Sprintf("urn:p%d", i) ||
			pi.Kind() != KindPI || pi.Value() != fmt.Sprintf("data %d", i) {
			t.Fatalf("element %d: namespace %s=%q, PI %q", i, ns.Name().Local, ns.Value(), pi.Value())
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
