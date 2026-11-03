package xdmstream

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A leaf's copy (Leaf) is the node of a full parse to what reads it once
// the cursor has moved on: its value, its ancestors' names and identities,
// its own identity, and its order among the views and the other copies.
func TestLeafMatchesTree(t *testing.T) {
	o := opts
	o.AllowDOCTYPE = true
	docs := viewDocs()
	docs["mixed"] = `<?p0 x?><!--c0--><a xml:base="http://ex/m/">t1<b k="v">t2<!--c1--><?p1 y?></b>t3<b/><c>` +
		strings.Repeat(`<d>u<e/>w</d>`, 3000) + `</c></a><!--c2--><?p2 z?>`
	for name, src := range docs {
		t.Run(name, func(t *testing.T) {
			tree, err := xdm.ParseString(src, o)
			if err != nil {
				t.Fatal(err)
			}
			var want []*xdm.Node // elements and leaves, in document order
			for n := range tree.Root.Descendants() {
				want = append(want, n)
			}
			c := Open(strings.NewReader(src), o)
			defer c.Close()
			var got []*xdm.Node
			var at []int // the index in want of each node got
			i := 0
			for {
				e, err := c.Next()
				if err != nil {
					t.Fatal(err)
				}
				if e.Kind == EndDocument {
					break
				}
				switch e.Kind {
				case EndElement:
					continue
				case StartElement:
					if _, err := c.Leaf(); err == nil {
						t.Fatal("Leaf at a start tag")
					}
					if i%2 == 0 {
						v, err := c.View()
						if err != nil {
							t.Fatal(err)
						}
						got, at = append(got, v), append(at, i)
					}
				default:
					l, err := c.Leaf()
					if err != nil {
						t.Fatal(err)
					}
					if again, _ := c.Leaf(); again != l {
						t.Fatal("a second Leaf is another node")
					}
					got, at = append(got, l), append(at, i)
					if i%7 == 0 {
						if _, err := c.Peek(); err != nil {
							t.Fatal(err)
						}
						if _, err := c.Leaf(); err == nil {
							t.Fatal("Leaf after Peek")
						}
					}
				}
				i++
			}
			if i != len(want) {
				t.Fatalf("%d events, %d nodes", i, len(want))
			}
			for k, n := range got {
				w := want[at[k]]
				if g, x := leafProps(n), leafProps(w); g != x {
					t.Fatalf("node %d:\n%s\nwant\n%s", at[k], g, x)
				}
				if !n.Is(n) || n.Identity() != n.Identity() {
					t.Fatal("identity")
				}
			}
			sorted := slices.Clone(got)
			slices.SortStableFunc(sorted, func(a, b *xdm.Node) int { return a.Compare(b) })
			if !slices.Equal(sorted, got) {
				t.Fatal("the copies do not order as the originals")
			}
			for k := 1; k < len(got); k++ {
				if got[k].Is(got[k-1]) || got[k].Compare(got[k-1]) <= 0 {
					t.Fatalf("nodes %d and %d", at[k-1], at[k])
				}
			}
		})
	}
}

// leafProps is what a copy must answer as the original does: its kind,
// name, identity and value (a leaf's; an element's is content), base URI,
// and its ancestors' names and identities.
func leafProps(n *xdm.Node) string {
	var b strings.Builder
	v := ""
	if n.Kind() != xdm.KindElement {
		v = n.StringValue()
	}
	fmt.Fprintf(&b, "%v %v %s %q base=%q\n", n.Kind(), n.Name(), key(n), v, n.BaseURI())
	for p := n.Parent(); p != nil; p = p.Parent() {
		fmt.Fprintf(&b, "^%v %v %s\n", p.Kind(), p.Name(), key(p))
	}
	return b.String()
}
