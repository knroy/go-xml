package xdm

import (
	"slices"
	"testing"
)

// TestCopyKeepsEveryBaseURI: a copy passes each node's base URI down from its
// parent's instead of walking up to the nearest stored one (which made a deep
// copy quadratic); every node of the copy must still have its original's
// base, whether stored on it, inherited, or none at all.
func TestCopyKeepsEveryBaseURI(t *testing.T) {
	src, err := ParseString(`<a><b xml:base="b/"><c>t<!--k--><d xml:base="/abs/"><e/>u</d></c><f xml:base="b/"/></b><g/></a>`,
		ParseOptions{BaseURI: "file:///r/doc.xml"})
	if err != nil {
		t.Fatal(err)
	}
	bases := func(n *Node) []string {
		out := []string{n.BaseURI()}
		for d := range n.Descendants() {
			out = append(out, d.BaseURI())
		}
		return out
	}
	want := bases(src.Root.FirstChild())
	if got := bases(Copy(src.Root.FirstChild())); !slices.Equal(got, want) {
		t.Errorf("Copy:\n got %q\nwant %q", got, want)
	}
	// Under a parent with a base of its own, the copy's top keeps its base.
	dst := NewTree()
	p := dst.Root.AppendElement(QName{Local: "p"})
	p.SetBaseURI("http://other/x/")
	if got := bases(p.AppendCopy(src.Root.FirstChild())); !slices.Equal(got, want) {
		t.Errorf("AppendCopy:\n got %q\nwant %q", got, want)
	}
}
