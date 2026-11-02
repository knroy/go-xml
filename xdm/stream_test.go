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
func (c *keepAll) Later(any) error      { c.grounded++; return nil }

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

// Grounded later, small elements under a deep spine share the copies of
// their ancestors, as ParseBursts' do: a Ground of each copied the whole
// spine every time (records x depth).
func TestDriveGroundLaterSharesTheSpine(t *testing.T) {
	const depth, n = 300, 3000
	var b strings.Builder
	b.WriteString(strings.Repeat("<w>", depth))
	for i := range n {
		fmt.Fprintf(&b, "<rec k='%d'><x/></rec>", i)
	}
	b.WriteString(strings.Repeat("</w>", depth))
	got := 0
	c := &laterAll{depth: depth, fn: func(r *Node) {
		d := 0
		for p := r.Parent(); p != nil && p.Kind() == KindElement; p = p.Parent() {
			d++
		}
		if d != depth || r.Attr("", "k").Value() != fmt.Sprint(got) {
			t.Fatalf("copy %d: %d ancestors, k=%s", got, d, r.Attr("", "k").Value())
		}
		got++
	}}
	bs := driver(c)
	if _, err := parse(strings.NewReader(b.String()), "", ParseOptions{bursts: bs}); err != nil {
		t.Fatal(err)
	}
	if got != n {
		t.Fatalf("%d copies, want %d", got, n)
	}
	if read := depth + 2*n; bs.spineCopies > read {
		t.Fatalf("%d ancestor records copied for %d elements read", bs.spineCopies, read)
	}
}

// laterAll keeps the spine and grounds every element below it for later.
type laterAll struct {
	depth int
	fn    func(*Node)
}

func (c *laterAll) Start(_ any, d int) (xdmburst.Action, error) {
	if d > c.depth {
		return xdmburst.GroundLater, nil
	}
	return xdmburst.Keep, nil
}
func (c *laterAll) Leaf(any, int) error           { return nil }
func (c *laterAll) End(_ any, _ int, _ any) error { return nil }
func (c *laterAll) Watch(any, int, bool)          {}
func (c *laterAll) Later(g any) error             { c.fn(g.(*Node)); return nil }
