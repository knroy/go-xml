package xdm

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
)

// spineAttrsDoc is an element of s attributes over n empty children, under
// depth elements of none.
func spineAttrsDoc(depth, s, n int) string {
	var b strings.Builder
	b.WriteString(strings.Repeat("<w>", depth))
	b.WriteString("<r")
	for i := range s {
		fmt.Fprintf(&b, ` a%d=""`, i)
	}
	b.WriteString(">")
	b.WriteString(strings.Repeat("<x/>", n))
	b.WriteString("</r>")
	b.WriteString(strings.Repeat("</w>", depth))
	return b.String()
}

// groundAt grounds every element at depth d, each at once (Ground).
type groundAt struct{ d, grounded int }

func (c *groundAt) Start(_ any, d int) (xdmburst.Action, error) {
	if d == c.d {
		return xdmburst.Ground, nil
	}
	return xdmburst.Keep, nil
}
func (c *groundAt) Leaf(any, int) error { return nil }
func (c *groundAt) End(_ any, _ int, g any) error {
	if g != nil {
		c.grounded++
	}
	return nil
}
func (c *groundAt) Watch(any, int, bool, uint64) {}
func (c *groundAt) Later(any) error              { return nil }

// Each grounded copy carries its ancestors and their attributes: past the
// floor, a parse that copies more than spineCopiesPerRecord of them (the
// document node, the elements, the attributes) for each record grounded is
// refused (a ResourceLimit), where one at the rate passes, of attributes or
// of attribute-free ancestors alike. Copies in trees of their own (deliver),
// as a large burst is: small ones share their ancestors' copies
// (TestDriveGroundSharesTheSpine).
func TestDriveGroundSpineAttrsRefused(t *testing.T) {
	defer func(f int, s bool) { spineCopiesFloor, groundShared = f, s }(spineCopiesFloor, groundShared)
	spineCopiesFloor, groundShared = 1000, false
	for _, c := range []struct {
		depth, s, n int
		refused     bool
	}{
		{0, spineCopiesPerRecord - 2, 100, false}, // the document node and r
		{0, 4 * spineCopiesPerRecord, 100, true},
		{spineCopiesPerRecord - 2, 0, 100, false},
		{4 * spineCopiesPerRecord, 0, 100, true},
	} {
		g := &groundAt{d: c.depth + 2}
		bs := driver(g)
		_, err := parse(strings.NewReader(spineAttrsDoc(c.depth, c.s, c.n)), "", ParseOptions{bursts: bs})
		if refused := errors.Is(err, ErrResourceLimit); refused != c.refused || err != nil && !refused {
			t.Errorf("depth %d, %d attributes: %v, want refused %v", c.depth, c.s, err, c.refused)
		}
		if want := (c.depth + 2 + c.s) * c.n; !c.refused && (g.grounded != c.n || bs.spineCopies != want) {
			t.Errorf("depth %d, %d attributes: %d grounded, %d ancestors copied, want %d", c.depth, c.s, g.grounded, bs.spineCopies, want)
		}
	}
}

// Small bursts under an element of more attributes than a batch's records
// still share one copy of it: a batch is delivered once it holds four times
// what its ancestors' copies take, not after every burst.
func TestParseBurstsManyAttrSpineBatched(t *testing.T) {
	const s, n = 5000, 2000
	got := 0
	bs := &burster{c: &patternClient{steps: []xdmburst.Step{{Local: "r"}, {Local: "x"}}, states: []uint64{1}}, gens: ordsOf(0),
		fn: func(x *Node) error {
			if x.Parent().Attr("", "a4999") == nil {
				t.Fatalf("burst %d: its parent's attributes are missing", got)
			}
			got++
			return nil
		}}
	if _, err := parse(strings.NewReader(spineAttrsDoc(0, s, n)), "", ParseOptions{bursts: bs}); err != nil {
		t.Fatal(err)
	}
	if got != n || bs.spineCopies > 2*(s+1) {
		t.Errorf("%d bursts; %d ancestor records copied for %d attributes", got, bs.spineCopies, s)
	}
}
