package xdm

import "testing"

// TreeHasTyping is false until some node of the tree is given typing, and
// a node given only the zero typing does not make it true.
func TestTreeHasTyping(t *testing.T) {
	tree, err := ParseString("<a><b/></a>", ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Root.FirstChild()
	b := a.FirstChild()
	if a.TreeHasTyping() {
		t.Fatal("a parsed tree reports typing")
	}
	b.ApplyTyping(Typing{})
	b.StripTyping()
	if a.TreeHasTyping() {
		t.Fatal("zero typing made the tree typed")
	}
	b.SetTypeAnnotation("string")
	if !a.TreeHasTyping() || !tree.Root.TreeHasTyping() {
		t.Fatal("an annotated node left the tree untyped")
	}
}
