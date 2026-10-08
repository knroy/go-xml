package xdm

import "testing"

// TestStringValueSingleText pins StringValue's fast path: an element whose
// only child is one text node has that text as its string value, returned
// without building a copy, and every other shape still concatenates the
// descendant text in document order, skipping comments and PIs.
func TestStringValueSingleText(t *testing.T) {
	for _, c := range []struct{ doc, want string }{
		{`<r/>`, ""},
		{`<r>abc</r>`, "abc"},
		{`<r><!--c--></r>`, ""},
		{`<r><!--c-->x</r>`, "x"},
		{`<r><?p q?>x</r>`, "x"},
		{`<r><a>x</a></r>`, "x"},
		{`<r>a<b>b</b>c</r>`, "abc"},
		{`<r><a/></r>`, ""},
	} {
		tree, err := ParseString(c.doc, ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		el := tree.Root.ChildElements()[0]
		if got := el.StringValue(); got != c.want {
			t.Errorf("StringValue(%s) = %q, want %q", c.doc, got, c.want)
		}
		if got := tree.Root.StringValue(); got != c.want {
			t.Errorf("document StringValue(%s) = %q, want %q", c.doc, got, c.want)
		}
	}
	tree, err := ParseString(`<r>a simple value</r>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	el := tree.Root.ChildElements()[0]
	if allocs := testing.AllocsPerRun(100, func() { _ = el.StringValue() }); allocs != 0 {
		t.Errorf("StringValue of a single text child allocated %v times, want 0", allocs)
	}
}
