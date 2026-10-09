package xdm

import (
	"sync"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xdmclone"
)

const nameIndexDoc = `<r xmlns:a="urn:a" xmlns:b="urn:a">
  <a:x id="1"><x id="2"/><b:x id="3"><a:x id="4"/></b:x></a:x>
  <y><x id="5"/><a:x id="6"/></y>
  <x id="7"/><!-- x --><?x x?>
</r>`

// walkNamed is the walk the index replaces.
func walkNamed(n *Node, uri, local string) []*Node {
	var out []*Node
	for c := range n.Descendants() {
		if c.Kind() == KindElement && c.Name().URI == uri && c.Name().Local == local {
			out = append(out, c)
		}
	}
	return out
}

func indexNamed(t *testing.T, n *Node, uri, local string) ([]*Node, bool) {
	t.Helper()
	var out []*Node
	ok := n.namedDescendants(uri, local, func(c any) { out = append(out, c.(*Node)) })
	return out, ok
}

// The index answers exactly what the walk does, from every node of a parsed
// document, for names under several prefixes and for absent names.
func TestElementIndexMatchesTheWalk(t *testing.T) {
	tree, err := ParseString(nameIndexDoc, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	names := [][2]string{{"urn:a", "x"}, {"", "x"}, {"", "y"}, {"", "r"}, {"", "nope"}, {"urn:b", "x"}}
	var nodes []*Node
	nodes = append(nodes, tree.Root)
	for c := range tree.Root.Descendants() {
		nodes = append(nodes, c)
		for a := range c.Attrs() {
			nodes = append(nodes, a)
		}
	}
	for _, n := range nodes {
		for _, q := range names {
			got, ok := indexNamed(t, n, q[0], q[1])
			if !ok {
				t.Fatalf("no index on a parsed document")
			}
			want := walkNamed(n, q[0], q[1])
			if len(got) != len(want) {
				t.Fatalf("%v under %s: index %d nodes, walk %d", q, n.Name().Local, len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%v under %s: node %d differs", q, n.Name().Local, i)
				}
			}
		}
	}
}

// Only a parsed document is indexed: a tree under construction, and a clone
// of a parsed one, can still grow.
func TestElementIndexOnlyOnParsedTrees(t *testing.T) {
	tree, err := ParseString(nameIndexDoc, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := indexNamed(t, tree.Root, "urn:a", "x"); !ok || tree.elemIndex.Load() == nil {
		t.Fatal("parsed document not indexed")
	}
	m := xdmclone.Clone(tree.Root, xdmclone.Options{})
	c := m(tree.Root).(*Node)
	if _, ok := indexNamed(t, c, "urn:a", "x"); ok || c.tree.elemIndex.Load() != nil {
		t.Error("a clone answered from an index")
	}
	if _, ok := indexNamed(t, NewTree().Root, "", "x"); ok {
		t.Error("a constructed tree answered from an index")
	}
}

// Renaming an element of a parsed document drops the index rather than
// leave it answering for the old name.
func TestElementIndexDroppedOnRename(t *testing.T) {
	tree, err := ParseString(`<r><x/><y/></r>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := indexNamed(t, tree.Root, "", "x"); len(got) != 1 {
		t.Fatalf("x: %d", len(got))
	}
	walkNamed(tree.Root, "", "y")[0].SetName(QName{Local: "x"})
	if got, _ := indexNamed(t, tree.Root, "", "x"); len(got) != 2 {
		t.Errorf("after rename, x: %d nodes, want 2", len(got))
	}
}

// Trees are shared read-only across goroutines, so the first lookups may
// race to build the index. Run under -race.
func TestElementIndexConcurrentFirstUse(t *testing.T) {
	tree, err := ParseString(nameIndexDoc, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int
			tree.Root.namedDescendants("urn:a", "x", func(any) { n++ })
			if n != 4 {
				t.Errorf("got %d, want 4", n)
			}
		}()
	}
	wg.Wait()
}
