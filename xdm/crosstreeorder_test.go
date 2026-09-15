package xdm

import "testing"

// A node built by a sequence constructor sorts after a node the parser built,
// not before it.
//
// Compare read a treeless node's tree id as the zero value, and the parser
// draws tree ids from a counter starting at one, so every constructed node
// came out ahead of every parsed document. A union of a streamed selection
// with a variable holding elements -- the shape sx-union-012, -017, -022 and
// -035 all take, and the one si-fork-118 takes through a path expression over
// current-group() -- put the variable's contents first.
//
// Order() already biases constructed roots above tree ids for the identity it
// hands fn:generate-id(); crossTreeRank makes Compare agree with it.
func TestCompareOrdersConstructedTreesAfterParsed(t *testing.T) {
	tree, err := ParseString(`<doc><p/></doc>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parsed := tree.Root.Children[0]

	// Two parentless elements, as a variable holding element()* produces.
	a := &Node{Kind: KindElement, Name: QName{Local: "a"}}
	b := &Node{Kind: KindElement, Name: QName{Local: "b"}}

	if got := parsed.Compare(a); got >= 0 {
		t.Errorf("parsed.Compare(constructed) = %d, want < 0", got)
	}
	if got := a.Compare(parsed); got <= 0 {
		t.Errorf("constructed.Compare(parsed) = %d, want > 0", got)
	}

	// The union puts the document's node first and keeps the two constructed
	// ones in the order the caller supplied.
	got := Union(Sequence{parsed}, Sequence{a, b})
	want := []*Node{parsed, a, b}
	if len(got) != len(want) {
		t.Fatalf("union has %d nodes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].(*Node) != want[i] {
			t.Errorf("union[%d] = %v, want %v",
				i, got[i].(*Node).Name.Local, want[i].Name.Local)
		}
	}
}

// The order holds however many trees have been made before.
//
// detachedRootID used to draw from its own counter and add a fixed
// detachedIDBias of 1<<20 to clear the tree ids. A fixed offset is only right
// until a run overruns it: the xslt30 suite reaches tree id 1272658, past the
// bias, and parsed documents began sorting after constructed ones again --
// which is why sx-union-012, -017, -022, -035 and si-fork-118 failed in a full
// suite run and passed when their test-set ran alone.
//
// Making one node of each kind AFTER a large number of trees exist is the same
// shape without the suite. With a shared counter the answer does not depend on
// how many came before.
func TestCompareOrderSurvivesManyTrees(t *testing.T) {
	// Enough trees that a 1<<20 offset would be overrun.
	for i := 0; i < (1<<20)+16; i++ {
		NewTree()
	}
	tree, err := ParseString(`<doc><p/></doc>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parsed := tree.Root.Children[0]
	made := &Node{Kind: KindElement, Name: QName{Local: "a"}}

	// The constructed node is made after the parsed one, so it sorts after it.
	if got := parsed.Compare(made); got >= 0 {
		t.Errorf("parsed.Compare(constructed) = %d, want < 0", got)
	}
	got := Union(Sequence{parsed}, Sequence{made})
	if len(got) != 2 || got[0].(*Node) != parsed || got[1].(*Node) != made {
		t.Errorf("union = %v, want the parsed node first", got)
	}
}
