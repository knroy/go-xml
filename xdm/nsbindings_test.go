package xdm

import (
	"maps"
	"runtime"
	"strconv"
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

// Copying an element that declares K namespaces adds them one at a time.
// Each AddNamespace used to copy and intern the whole frame built so far, so
// the copy's tree kept K(K+1)/2 bindings and about K^2/2 bytes of index keys.
func TestAddNamespaceLinearFrames(t *testing.T) {
	const k = 400
	doc := `<r`
	for i := range k {
		doc += ` xmlns:p` + strconv.Itoa(i) + `="u"`
	}
	doc += `><c xmlns:q="v"/><c xmlns:q="v" xmlns:s="w"/></r>`
	tr, err := ParseString(doc, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := Copy(tr.Root)
	ct := c.tree
	// The copy's frames: K bindings for r, plus a few for the children.
	if n := len(ct.frameData); n > 2*k {
		t.Errorf("copy kept %d bindings for %d declarations", n, k)
	}
	// Rebuilding the indexes (as a clone or a frozen tree does) must not
	// index each intermediate frame.
	ct.frameOne, ct.frameMany = nil, nil
	ct.internFrame([]nsBinding{{"x", "y"}, {"z", "w"}})
	keyBytes := 0
	for key := range ct.frameMany {
		keyBytes += len(key)
	}
	if keyBytes > 32*k {
		t.Errorf("rebuilt index keeps %d key bytes for %d declarations", keyBytes, k)
	}
	// The copy declares exactly what the source does.
	if !maps.Equal(maps.Collect(c.DeclaredNamespaces()), maps.Collect(tr.Root.DeclaredNamespaces())) {
		t.Errorf("copy's declarations differ from the source's")
	}
	for s, d := tr.Root.FirstChild(), c.FirstChild(); s != nil; s, d = s.NextSibling(), d.NextSibling() {
		if !maps.Equal(maps.Collect(d.InScopeNamespaceBindings()), maps.Collect(s.InScopeNamespaceBindings())) {
			t.Errorf("child's in-scope bindings differ from the source's")
		}
	}
}
