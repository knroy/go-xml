package xdm

import (
	"testing"

	"github.com/knroy/go-xml/internal/genid"
)

// TestGenerateIDAcrossTreeStride pins the collision fn:generate-id() had while
// it was "N" plus Order(): Order folds the tree id and the document-order
// index into one integer with a stride of 2^20, so a tree with more order
// slots than that ran into the next tree's range, and node 2^20 of tree t
// answered the same id as node 0 of tree t+1.
func TestGenerateIDAcrossTreeStride(t *testing.T) {
	t1, t2 := &Tree{id: 7}, &Tree{id: 8}
	a := &Node{Kind: KindElement, tree: t1, order: treeIDStride}
	b := &Node{Kind: KindDocument, tree: t2}
	if a.Order() != b.Order() {
		t.Fatalf("precondition: Order() %d and %d no longer overlap; the stride changed", a.Order(), b.Order())
	}
	ida, idb := genid.Of(a), genid.Of(b)
	if ida == idb {
		t.Fatalf("generate-id collides across trees: both nodes answered %q", ida)
	}
	for _, id := range []string{ida, idb} {
		for i, c := range id {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
				t.Fatalf("generate-id %q is not ASCII alphanumeric starting with a letter", id)
			}
		}
	}
	if ida != "N7x1048576" {
		t.Errorf("generate-id = %q, want N7x1048576", ida)
	}
}
