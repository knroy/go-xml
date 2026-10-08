package xdm

import (
	"strings"
	"testing"
)

// SortDocumentOrder returns a sequence that is already one tree in strictly
// increasing order without sorting or copying it. Everything else still goes
// through the sort: reversed input, a node twice, two pointers to one
// synthesized namespace node, and detached roots, which the sort numbers.
func TestSortDocumentOrderInOrderFastPath(t *testing.T) {
	tree, err := ParseString(`<r a="1"><x/><y/><z/></r>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := tree.Root.Children[0]
	a, x, y, z := r.Attrs[0], r.Children[0], r.Children[1], r.Children[2]

	names := func(s Sequence) string {
		var b []string
		for _, it := range s {
			n := it.(*Node)
			switch n.Kind {
			case KindNamespace:
				b = append(b, "ns:"+n.Name.Local)
			case KindDocument:
				b = append(b, "/")
			default:
				b = append(b, n.Name.Local)
			}
		}
		return strings.Join(b, " ")
	}

	for _, c := range []struct {
		in   Sequence
		want string
	}{
		{Sequence{tree.Root, r, a, x, y, z}, "/ r a x y z"},
		{Sequence{z, y, x}, "x y z"},
		{Sequence{x, x, y}, "x y"},
		{Sequence{x, z, y}, "x y z"},
	} {
		if got := names(SortDocumentOrder(c.in)); got != c.want {
			t.Errorf("SortDocumentOrder = %q, want %q", got, c.want)
		}
	}

	// Two trees are ordered by tree, whatever their order numbers say, so
	// both orders of the pair must sort to the same answer.
	other, err := ParseString(`<o/>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	o := other.Root.Children[0]
	if p, q := names(SortDocumentOrder(Sequence{x, o})), names(SortDocumentOrder(Sequence{o, x})); p != q {
		t.Errorf("two trees: %q and %q", p, q)
	}

	// In order: the same nodes come back, capped so an append cannot reach
	// the caller's spare capacity, and nothing is allocated.
	in := make(Sequence, 3, 8)
	copy(in, Sequence{x, y, z})
	out := SortDocumentOrder(in)
	if len(out) != 3 || cap(out) != 3 || out[0] != x || out[2] != z {
		t.Errorf("in-order input: len %d cap %d", len(out), cap(out))
	}
	if n := testing.AllocsPerRun(100, func() { SortDocumentOrder(in) }); n != 0 {
		t.Errorf("an in-order sequence allocated %v times, want 0", n)
	}

	// Two walks of the namespace axis synthesize two pointers for one
	// binding, in increasing position; they are one node and must merge.
	ns1 := &Node{Kind: KindNamespace, Name: QName{Local: "p"}, Value: "urn:p", Parent: r}
	ns2 := &Node{Kind: KindNamespace, Name: QName{Local: "p"}, Value: "urn:p", Parent: r}
	ns1.SetSynthesizedOrder(r, 0)
	ns2.SetSynthesizedOrder(r, 0)
	if got := names(SortDocumentOrder(Sequence{r, ns1, ns2})); got != "r ns:p" {
		t.Errorf("namespace duplicates: got %q, want %q", got, "r ns:p")
	}

	// Detached roots have no tree; the sort numbers them in the order given.
	d1, d2 := &Node{Kind: KindElement, Name: QName{Local: "d1"}},
		&Node{Kind: KindElement, Name: QName{Local: "d2"}}
	if got := names(SortDocumentOrder(Sequence{d1, d2})); got != "d1 d2" {
		t.Errorf("detached roots: got %q", got)
	}
	if d1.detachedID == 0 || d2.detachedID == 0 {
		t.Error("detached roots were not numbered")
	}
}
