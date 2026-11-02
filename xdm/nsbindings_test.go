package xdm

import (
	"maps"
	"runtime"
	"testing"
)

func TestInScopeNamespaceBindings(t *testing.T) {
	tr, err := ParseString(`<r xmlns="urn:d" xmlns:a="urn:a" xmlns:b="urn:b"><s xmlns:a="urn:a2" xmlns="" ><t xmlns:c="urn:c"/></s></r>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Kind() == KindElement {
			got := map[string]string{}
			for p, u := range n.InScopeNamespaceBindings() {
				if _, dup := got[p]; dup {
					t.Errorf("%s: prefix %q yielded twice", n.Name().Local, p)
				}
				got[p] = u
			}
			if want := n.InScopeNamespaces(); !maps.Equal(got, want) {
				t.Errorf("%s: got %v, want %v", n.Name().Local, got, want)
			}
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(tr.Root)
	// Yielding stops when asked, and no namespace node is made on the way.
	r := tr.Root.FirstChild()
	k := 0
	for range r.InScopeNamespaceBindings() {
		k++
		break
	}
	if k != 1 {
		t.Errorf("break did not stop the iteration")
	}
	if tr.side != nil {
		t.Errorf("iterating made %d namespace nodes", len(tr.side.m))
	}
	if a := testing.AllocsPerRun(10, func() {
		for range r.InScopeNamespaceBindings() {
		}
	}); a != 0 {
		t.Errorf("iteration allocated %v times", a)
	}
}

// Making the namespace nodes of an element must not rebuild its scope for
// each one: that was quadratic in the bindings in scope.
func TestNamespaceNodesAllocation(t *testing.T) {
	doc := `<r`
	for i := range 40 {
		doc += ` xmlns:p` + string(rune('a'+i/10)) + string(rune('0'+i%10)) + `="urn:` + string(rune('a'+i/10)) + string(rune('0'+i%10)) + `"`
	}
	doc += `/>`
	tr, err := ParseString(doc, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := tr.Root.FirstChild()
	n := 0
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	for range r.NamespaceNodes() {
		n++
	}
	runtime.ReadMemStats(&b)
	if n != 41 {
		t.Fatalf("got %d namespace nodes, want 41", n)
	}
	if m := b.Mallocs - a.Mallocs; m > 200 { // a scope rebuild per node cost over 1000
		t.Errorf("%d allocations for 41 namespace nodes", m)
	}
}
