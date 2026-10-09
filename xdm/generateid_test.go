package xdm

import (
	"strconv"
	"testing"

	"github.com/knroy/go-xml/v2/internal/genid"
)

// TestGenerateIDAcrossTrees pins the collision fn:generate-id() once had
// while it folded the tree id and the position into one integer: node 2^20 of
// tree t answered the same id as node 0 of tree t+1. The id spells the two
// apart, so nodes at the same position of two trees differ.
func TestGenerateIDAcrossTrees(t *testing.T) {
	t1, t2 := NewTree(), NewTree()
	a, b := t1.Root, t2.Root
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
	if want := "N" + strconv.FormatInt(t1.ident(), 10) + "x0"; ida != want {
		t.Errorf("generate-id = %q, want %q", ida, want)
	}
}
