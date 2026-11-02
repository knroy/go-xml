package xdm

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xdmburst"
)

// keepAll is a Drive client that descends into every element, as a cursor
// read with Next alone does, grounding every k-th element at depth 3.
type keepAll struct {
	k, n, leaves, grounded int
}

func (c *keepAll) Start(el any, d int) (xdmburst.Action, error) {
	if d != 3 {
		return xdmburst.Keep, nil
	}
	if c.n++; c.k > 0 && c.n%c.k == 0 {
		return xdmburst.Ground, nil
	}
	return xdmburst.Keep, nil
}
func (c *keepAll) Leaf(any, int) error { c.leaves++; return nil }
func (c *keepAll) End(_ any, _ int, g any) error {
	if g != nil {
		c.grounded++
	}
	return nil
}
func (c *keepAll) Watch(any, int, bool) {}

// A stream read element by element keeps only the open elements: what each
// element, leaf and grounded copy leaves in the parse's tree is taken back.
func TestDriveKeepsOnlyTheSpine(t *testing.T) {
	var b strings.Builder
	b.WriteString("<!--pre--><a><b>")
	for i := range 20000 {
		fmt.Fprintf(&b, "<r i='%d'><s>%s</s>t<!--c--></r>\n<?p x?>", i, strings.Repeat("v", i%50))
	}
	b.WriteString("</b></a><!--post-->")
	for _, k := range []int{0, 3} {
		c := &keepAll{k: k}
		tree, err := parse(strings.NewReader(b.String()), "", ParseOptions{bursts: driver(c)})
		if err != nil {
			t.Fatal(err)
		}
		if k > 0 && c.grounded != 20000/k {
			t.Fatalf("%d grounded", c.grounded)
		}
		if c.leaves < 4*20000-c.grounded*3 {
			t.Fatalf("%d leaves", c.leaves)
		}
		var bytes int
		for _, blk := range tree.text.blocks {
			bytes += len(blk)
		}
		// Left behind: the document node, and the few bytes stored in the
		// block that was current at the spine's marks (rewind closes it).
		if tree.n > 1 || bytes > 64 {
			t.Fatalf("k=%d: the parse kept %d records and %d text bytes %q", k, tree.n, bytes, tree.text.blocks)
		}
	}
}
