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
	r := kids(tree.Root)[0]
	a, x, y, z := attrsOf(r)[0], kids(r)[0], kids(r)[1], kids(r)[2]

	names := func(s Sequence) string {
		var b []string
		for _, it := range s {
			n := it.(*Node)
			switch n.Kind() {
			case KindNamespace:
				b = append(b, "ns:"+n.Name().Local)
			case KindDocument:
				b = append(b, "/")
			default:
				b = append(b, n.Name().Local)
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
	o := kids(other.Root)[0]
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

	// The namespace axis gives one node per binding, however often it is
	// walked; a sequence holding it twice merges to one.
	var ns1, ns2 *Node
	for ns := range r.NamespaceNodes() {
		if ns.Name().Local == "xml" {
			ns1 = ns
		}
	}
	for ns := range r.NamespaceNodes() {
		if ns.Name().Local == "xml" {
			ns2 = ns
		}
	}
	if ns1 != ns2 {
		t.Fatal("two walks of the namespace axis gave two nodes for one binding")
	}
	if got := names(SortDocumentOrder(Sequence{ns2, r, ns1})); got != "r ns:xml" {
		t.Errorf("namespace duplicates: got %q, want %q", got, "r ns:xml")
	}

	// Parentless constructed nodes of separate fragments are numbered in
	// the order the sequence holds them, the first time they are compared.
	d1 := NewNode(KindElement, QName{Local: "d1"}, "")
	d2 := NewNode(KindElement, QName{Local: "d2"}, "")
	if got := names(SortDocumentOrder(Sequence{d2, d1})); got != "d2 d1" {
		t.Errorf("detached roots: got %q", got)
	}
	if got := names(SortDocumentOrder(Sequence{d1, d2})); got != "d2 d1" {
		t.Errorf("detached roots, asked again: got %q", got)
	}
}
