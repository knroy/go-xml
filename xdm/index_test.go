package xdm

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// checkIndex walks every node of t, attributes included, in document order and
// checks that Index counts 0, 1, 2, ... with no gap, that NodeAt inverts it,
// that a subtree's SubtreeEnd is one past its last index, and that namespace
// nodes have no index.
func checkIndex(t *testing.T, tr *Tree) {
	t.Helper()
	next := 0
	var walk func(n *Node)
	walk = func(n *Node) {
		if got := n.Index(); got != next {
			t.Fatalf("%v %v: Index %d, want %d", n.Kind(), n.Name(), got, next)
		}
		if tr.NodeAt(next) != n {
			t.Fatalf("NodeAt(%d) is not the node whose Index is %d", next, next)
		}
		next++
		for ns := range n.NamespaceNodes() {
			if ns.Index() != -1 || ns.SubtreeEnd() != -1 {
				t.Fatalf("namespace node has Index %d", ns.Index())
			}
		}
		for a := range n.Attrs() {
			if a.Index() != next || a.SubtreeEnd() != next+1 {
				t.Fatalf("attribute %v: Index %d End %d, want %d", a.Name(), a.Index(), a.SubtreeEnd(), next)
			}
			if tr.NodeAt(next) != a {
				t.Fatalf("NodeAt(%d) is not the attribute", next)
			}
			next++
		}
		for c := range n.Children() {
			walk(c)
		}
		if n.SubtreeEnd() != next {
			t.Fatalf("%v: SubtreeEnd %d, want %d", n.Name(), n.SubtreeEnd(), next)
		}
	}
	walk(tr.Root)
	if tr.Len() != next {
		t.Fatalf("Len %d, want %d", tr.Len(), next)
	}
	if tr.NodeAt(-1) != nil || tr.NodeAt(next) != nil || tr.NodeAt(next+1<<20) != nil {
		t.Fatal("NodeAt out of range is not nil")
	}
}

func TestNodeIndexDense(t *testing.T) {
	var ns strings.Builder
	ns.WriteString(`<a xmlns="urn:d" xmlns:p="urn:p" p:x="1" y="2">`)
	for i := 0; i < 50; i++ {
		ns.WriteString(`<p:b xmlns:q="urn:q" q:z="1"><c xmlns="urn:e"/>t<!--c--><?pi d?></p:b>`)
	}
	ns.WriteString(`</a>`)
	docs := map[string][]byte{"namespaces": []byte(ns.String())}
	for name, glob := range map[string]string{
		"docbook": "xsltng/src/test/resources/xml/*.xml",
		"xmark":   "bench/xmark/auction-0.01.xml",
	} {
		paths, _ := filepath.Glob(filepath.Join("..", "testdata", filepath.FromSlash(glob)))
		sort.Strings(paths)
		if len(paths) > 20 {
			paths = paths[:20]
		}
		for _, p := range paths {
			if data, err := os.ReadFile(p); err == nil {
				docs[name+":"+filepath.Base(p)] = data
			}
		}
	}
	for name, data := range docs {
		tr, err := Parse(bytes.NewReader(data), ParseOptions{MaxBytes: -1})
		if err != nil {
			continue // a corpus file that needs more than defaults
		}
		t.Run(name, func(t *testing.T) { checkIndex(t, tr) })
	}
}

// A tree still being built numbers what it has so far, and taking back the
// last subtree frees its indices.
func TestNodeIndexBuilt(t *testing.T) {
	root := NewNode(KindElement, QName{Local: "r"}, "")
	a := root.AppendElement(QName{Local: "a"})
	if root.Index() != 0 || a.Index() != 1 || root.SubtreeEnd() != 2 {
		t.Fatalf("index %d %d end %d", root.Index(), a.Index(), root.SubtreeEnd())
	}
	tr := root.tree
	if tr.NodeAt(1) != a || tr.NodeAt(2) != nil {
		t.Fatal("NodeAt on a built tree")
	}
	b := root.AppendElement(QName{Local: "b"})
	if b.Index() != 2 || root.SubtreeEnd() != 3 || tr.Len() != 3 {
		t.Fatalf("after append: %d end %d len %d", b.Index(), root.SubtreeEnd(), tr.Len())
	}
	root.RemoveLastChild()
	if tr.Len() != 2 || tr.NodeAt(2) != nil {
		t.Fatalf("after take-back: len %d", tr.Len())
	}
}
