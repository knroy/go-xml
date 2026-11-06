package xdmstream

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// observeDocs are documents with leaves everywhere an accumulator rule may
// meet one: the prolog and epilog, mixed content, CDATA, deep and wide.
var observeDocs = []string{
	`<?xml version="1.0"?><!--p1--><?pi one?><doc a="1">t0<r k="x">in<![CDATA[cd]]><s/>tail<!--c--></r> mid <?p2 x?><r><s>deep<t>er</t></s></r>end</doc><!--e1--><?e2 y?>`,
	`<doc/>`,
	`<!DOCTYPE doc [<!ENTITY e "ent">]><doc>&e;<r>&e;x</r></doc>`,
	"<doc>\n <r>\n  <s> </s>\n </r>\n</doc>\n<!--after-->",
	"<d>" + strings.Repeat(`<r i="1">a<s>b<!--c-->d</s>e<?p q?></r>`, 300) + "</d>",
	strings.Repeat("<x>t", 40) + strings.Repeat("</x>", 40),
}

// TestObserverSeesEverything: an observer is told of every node a tree walk
// visits -- elements at start and end, leaves once -- in order, with the
// depth and the ordinal the tree-mode parse gives, then of the document's
// end, however the consumer reads: event by event, skipping, grounding,
// reading text, grounding later, through NextTag filters. Ordinal gives a
// view, and a node of a grounded copy, its original's ordinal.
func TestObserverSeesEverything(t *testing.T) {
	desc := func(n *xdm.Node, d int, end bool, ord uint64) string {
		if n == nil {
			return fmt.Sprintf("%d end-doc", d)
		}
		if n.Kind() == xdm.KindElement {
			return fmt.Sprintf("%d %v %v #%d", d, end, n.Name(), ord)
		}
		return fmt.Sprintf("%d %v %q #%d", d, n.Kind(), n.StringValue(), ord)
	}
	for di, src := range observeDocs {
		tree, err := xdm.Parse(strings.NewReader(src), opts)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		var walk func(n *xdm.Node, d int)
		walk = func(n *xdm.Node, d int) {
			for c := range n.Children() {
				want = append(want, desc(c, d, false, uint64(c.Index())))
				if c.Kind() == xdm.KindElement {
					walk(c, d+1)
					want = append(want, desc(c, d, true, uint64(c.Index())))
				}
			}
		}
		walk(tree.Root, 1)
		want = append(want, desc(nil, 0, true, 0))
		for mode := range 7 {
			rng := rand.New(rand.NewSource(int64(di*7 + mode)))
			c := Open(strings.NewReader(src), opts)
			var got []string
			c.Observe(func(n *xdm.Node, d int, end bool, ord uint64) { got = append(got, desc(n, d, end, ord)) })
			checkOrd := func(n *xdm.Node, want uint64, what string) {
				if o, ok := c.Ordinal(n); !ok || o != want {
					t.Errorf("doc %d mode %d: Ordinal(%s %v) = %d, %v; want %d", di, mode, what, n.Name(), o, ok, want)
				}
			}
			// ords of the open elements, by depth, from the observer's own view.
			var open []uint64
			c.Observe(func(n *xdm.Node, d int, end bool, ord uint64) {
				if n != nil && n.Kind() == xdm.KindElement && !end {
					open = append(open[:d-1], ord)
				}
			})
			filt := &Filter{Leaves: rng.Intn(2) == 0, Accept: func(el *xdm.Node) bool { return el.Name().Local != "s" }}
			for {
				var ev Event
				if mode == 6 {
					ev, err = c.NextTag(filt)
				} else {
					ev, err = c.Next()
				}
				if err != nil {
					t.Fatal(err)
				}
				if ev.Kind == EndDocument {
					break
				}
				if ev.Kind != StartElement || ev.Depth < 2 {
					continue
				}
				v, err := c.View()
				if err != nil {
					t.Fatal(err)
				}
				checkOrd(v, open[ev.Depth-1], "view")
				switch k := (mode + rng.Intn(5)) % 6; k {
				case 1:
					err = c.Skip()
				case 2:
					var g *xdm.Node
					if g, err = c.Ground(); err == nil {
						checkOrd(g, open[ev.Depth-1], "copy")
					}
				case 3:
					_, err = c.Text()
				case 4:
					want := open[ev.Depth-1]
					err = c.GroundLater(func(g *xdm.Node) error { checkOrd(g, want, "later copy"); return nil })
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			c.Close()
			if len(got) != len(want) {
				t.Errorf("doc %d mode %d: %d observations, want %d", di, mode, len(got), len(want))
			}
			same(t, got, want)
			if x, ok := c.Ordinal(tree.Root.FirstChild()); ok {
				t.Errorf("Ordinal of a node of another tree: %d", x)
			}
		}
	}
}

// TestObserverAddedLate: an observer added at any event, after a Peek or
// not, is told what one added at the start is told from there on, ordinals
// too: the parse's watch is off until the first observer, and is turned on
// there knowing the elements then open.
func TestObserverAddedLate(t *testing.T) {
	desc := func(into *[]string) Observer {
		return func(n *xdm.Node, d int, end bool, ord uint64) {
			s := fmt.Sprintf("%d end-doc", d)
			if n != nil {
				s = fmt.Sprintf("%d %v %v %q #%d", d, end, n.Kind(), n.Name(), ord)
			}
			*into = append(*into, s)
		}
	}
	// read drains src, adding o before event at (-1: before the first).
	read := func(src string, at int, peek bool, o Observer) int {
		c := Open(strings.NewReader(src), opts)
		defer c.Close()
		for i := 0; ; i++ {
			if i == at {
				if peek {
					c.Peek()
				}
				c.Observe(o)
			}
			ev, err := c.Next()
			if err != nil {
				t.Fatal(err)
			}
			if ev.Kind == EndDocument {
				return i + 1
			}
		}
	}
	for di, src := range observeDocs {
		var all []string
		n := read(src, 0, false, desc(&all))
		for at := 1; at < n; at++ {
			for _, peek := range []bool{false, true} {
				var late []string
				read(src, at, peek, desc(&late))
				if len(late) > len(all) || len(late) == 0 && !peek {
					t.Fatalf("doc %d at %d: %d observations of %d", di, at, len(late), len(all))
				}
				same(t, late, all[len(all)-len(late):])
			}
		}
	}
}
