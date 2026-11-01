package xdm

import (
	"sort"
	"testing"
)

// scopeSlots must list the in-scope bindings once each, ordered by prefix,
// with inner declarations and undeclarations in force; and must not cost a map
// and a reflection-built sorter per call.
func TestScopeSlotsOrderAndAllocs(t *testing.T) {
	tr, err := ParseString(`<r xmlns="urn:d" xmlns:z="urn:z" xmlns:a="urn:a" xmlns:b="urn:b"><s xmlns:a="urn:a2" xmlns="" xmlns:m="urn:m"><t xmlns:c="urn:c" xmlns:z="urn:z2"/></s></r>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var leaf *Node
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Kind() == KindElement {
			leaf = n
			want := []string{"xml"}
			for p, u := range n.InScopeNamespaces() {
				if p != "xml" && u != "" {
					want = append(want, p)
				}
			}
			sort.Strings(want)
			var got []string
			for _, s := range n.scopeSlots() {
				got = append(got, s.prefix)
			}
			if len(got) != len(want) {
				t.Fatalf("%s: got %v, want %v", n.Name().Local, got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%s: got %v, want %v", n.Name().Local, got, want)
				}
			}
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(tr.Root)
	if a := testing.AllocsPerRun(20, func() { leaf.scopeSlots() }); a > 2 {
		t.Errorf("scopeSlots allocated %v times", a)
	}
}
