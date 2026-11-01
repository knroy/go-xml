package xpath

import (
	"runtime"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

func gid(n *xdm.Node) string {
	s, _ := MustCompile("generate-id(.)", nil).EvalString(NewContext(n, Builtins()))
	return s
}

// settle runs enough collections, and waits long enough for the cleanup that
// sweeps the namespace-node table, that anything unreferenced is gone.
func settle() {
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
}

// TestNamespaceNodeIdentityAcrossGC pins what holding namespace nodes weakly
// must keep: while a caller holds a namespace node, every later request for
// it returns that same node, whatever collections ran in between, so is,
// deep-equal, generate-id, union de-duplication and use as a Go map key agree
// between two evaluations that both hold it.
func TestNamespaceNodeIdentityAcrossGC(t *testing.T) {
	const doc = `<r xmlns:a="urn:a" xmlns:b="urn:b"><c xmlns:a="urn:a2"><d/></c></r>`
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Root.FirstChild()
	eval := func(expr string, vars map[string]xdm.Sequence) xdm.Sequence {
		t.Helper()
		ctx := NewContext(tree.Root, Builtins()).WithVersion(XPath31)
		for k, v := range vars {
			ctx = ctx.WithVar(xdm.QName{Local: k}, v)
		}
		seq, err := Eval(expr, ctx, nil)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		return seq
	}
	ebv := func(expr string, vars map[string]xdm.Sequence) bool {
		t.Helper()
		b, err := EffectiveBooleanValue(eval(expr, vars))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	held := NamespaceNodesOf(root.FirstChild()) // c's axis: a, b, xml
	if len(held) != 3 {
		t.Fatalf("got %d namespace nodes, want 3", len(held))
	}
	ids := make([]string, len(held))
	keys := map[xdm.IdentityKey]int{}
	byPtr := map[*xdm.Node]int{}
	for i, n := range held {
		s := gid(n)
		ids[i] = s
		keys[n.Identity()] = i
		byPtr[n] = i
	}
	heldSeq := make(xdm.Sequence, len(held))
	for i, n := range held {
		heldSeq[i] = n
	}

	// Other namespace nodes come and go while these are held.
	for i := 0; i < 4; i++ {
		eval("count(//namespace::*)", nil)
		settle()
	}

	again := NamespaceNodesOf(root.FirstChild())
	for i, n := range again {
		if n != held[i] || !n.Is(held[i]) || n.Identity() != held[i].Identity() {
			t.Errorf("node %d: not the node still held", i)
		}
		if keys[n.Identity()] != i || byPtr[n] != i {
			t.Errorf("node %d: map key lookup differs", i)
		}
		s := gid(n)
		if s != ids[i] {
			t.Errorf("node %d: generate-id %q, was %q", i, s, ids[i])
		}
	}

	vars := map[string]xdm.Sequence{"held": heldSeq}
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`/r/c/namespace::b is $held[2]`, true},
		{`/r/c/namespace::a is $held[1]`, true},
		{`/r/namespace::a is $held[1]`, false},
		{`deep-equal(/r/c/namespace::*, $held)`, true},
		{`count(/r/c/namespace::* | $held) = 3`, true},
		{`count(/r/c/namespace::* intersect $held) = 3`, true},
		{`count(/r/c/namespace::* except $held) = 0`, true},
		{`count(distinct-values(($held, /r/c/namespace::*)!string())) = 3`, true},
		{`generate-id(/r/c/namespace::b) = generate-id($held[2])`, true},
		{`/r/c/namespace::b << /r/c/namespace::a`, false},
		{`/r/c/namespace::a << /r/c/namespace::b`, true},
	} {
		if got := ebv(tc.expr, vars); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
		}
	}
	runtime.KeepAlive(held)
	runtime.KeepAlive(tree)
}

// TestNamespaceNodesAreReleased checks the point of holding them weakly: a
// query that touches many namespace nodes and keeps none leaves nothing
// behind, and a node made again after its predecessor was freed is
// indistinguishable by every observable.
func TestNamespaceNodesAreReleased(t *testing.T) {
	var b []byte
	b = append(b, `<r xmlns:p="urn:p">`...)
	for i := 0; i < 2000; i++ {
		b = append(b, `<e xmlns:q="urn:q"><f/></e>`...)
	}
	b = append(b, `</r>`...)
	tree, err := xdm.ParseString(string(b), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	probe := func() (id string, order int) {
		e := tree.Root.FirstChild().FirstChild()
		n := NamespaceNodesOf(e)[1]
		s := gid(n)
		return s, n.Order()
	}
	id0, ord0 := probe()

	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	before := m.HeapAlloc
	for i := 0; i < 3; i++ {
		seq, err := Eval("count(//namespace::*)", NewContext(tree.Root, Builtins()), nil)
		if err != nil || len(seq) != 1 {
			t.Fatal(err)
		}
	}
	settle()
	runtime.ReadMemStats(&m)
	if grown := int64(m.HeapAlloc) - int64(before); grown > 64<<10 {
		t.Errorf("heap grew %d B after a query that kept no namespace node", grown)
	}

	id1, ord1 := probe()
	if id0 != id1 || ord0 != ord1 {
		t.Errorf("remade node: generate-id %q order %d, was %q order %d", id1, ord1, id0, ord0)
	}
	runtime.KeepAlive(tree)
}
