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
func (c *keepAll) Watch(any, int, bool, uint64) {}
func (c *keepAll) Later(any) error              { c.grounded++; return nil }

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
		// Left behind: the document node, the shared whitespace, and what
		// the block values go on being appended to holds: at most one block.
		if tree.n > 1 || bytes > textBlock+spaceHome {
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
func (c *laterAll) Watch(any, int, bool, uint64)  {}
func (c *laterAll) Later(g any) error             { c.fn(g.(*Node)); return nil }

// viewAll keeps every element and views each one.
type viewAll struct {
	view func() any
	fn   func(*Node, int)
}

func (c *viewAll) Viewer(v func() any) { c.view = v }
func (c *viewAll) Start(_ any, d int) (xdmburst.Action, error) {
	c.fn(c.view().(*Node), d)
	return xdmburst.Keep, nil
}
func (c *viewAll) Leaf(any, int) error           { return nil }
func (c *viewAll) End(_ any, _ int, _ any) error { return nil }
func (c *viewAll) Watch(any, int, bool, uint64)  {}
func (c *viewAll) Later(any) error               { return nil }

// Views under a deep spine share the copies of their ancestors: a view
// costs what the elements opened since the last one do, not the depth
// (records x depth), and a new batch copies the spine again only after the
// old one has paid for it.
func TestDriveViewsShareTheSpine(t *testing.T) {
	const depth, n = 1000, 20000
	var b strings.Builder
	b.WriteString(strings.Repeat("<w a='1' b='2' c='3'>", depth))
	for i := range n {
		fmt.Fprintf(&b, "<rec k='%d'><x/></rec>", i)
	}
	b.WriteString(strings.Repeat("</w>", depth))
	views := 0
	c := &viewAll{fn: func(v *Node, d int) {
		views++
		if views%1000 == 0 {
			k := 0
			for p := v.Parent(); p != nil && p.Kind() == KindElement; p = p.Parent() {
				k++
			}
			if k != d-1 {
				t.Fatalf("view at depth %d has %d ancestors", d, k)
			}
		}
	}}
	bs := driver(c)
	if _, err := parse(strings.NewReader(b.String()), "", ParseOptions{bursts: bs, MaxDepth: 2000}); err != nil {
		t.Fatal(err)
	}
	if views != depth+2*n {
		t.Fatalf("%d views", views)
	}
	// The spine once (4 records an element), and again for each batch the
	// views fill: at most a quarter of the records made.
	t.Logf("%d ancestor records copied, %d copies looked past, for %d views", bs.viewCopies, bs.viewScans, views)
	if read := 4*depth + (4*depth+3*2*n)/3; bs.viewCopies > read {
		t.Fatalf("%d ancestor records copied for %d views", bs.viewCopies, views)
	}
	// Finding the copies a view shares looks past only those it drops.
	if bs.viewScans > views+bs.viewCopies {
		t.Fatalf("%d copies looked past for %d views", bs.viewScans, views)
	}
}

// A view batch is replaced at a bounded size when the views' ancestors are
// themselves never viewed, each one new: views of x under rec after rec
// (the C4 split's view of a match whose parent it only reads past).
func TestDriveViewBatchBounded(t *testing.T) {
	const n = 20000
	var b strings.Builder
	b.WriteString("<doc>")
	for i := range n {
		fmt.Fprintf(&b, "<rec k='%d' v='1'><x a='1'/><x a='2'/></rec>", i)
	}
	b.WriteString("</doc>")
	most := 0
	var bs *burster
	c := &viewAll{}
	c.fn = func(v *Node, d int) {}
	vc := &viewSome{viewAll: c, want: "x", fn: func() {
		if n := int(bs.views.t.n); n > most {
			most = n
		}
	}}
	bs = driver(vc)
	if _, err := parse(strings.NewReader(b.String()), "", ParseOptions{bursts: bs}); err != nil {
		t.Fatal(err)
	}
	if most == 0 || most > 2*viewBatchRecords {
		t.Fatalf("a view batch grew to %d records (bound %d)", most, 2*viewBatchRecords)
	}
}

// viewSome views only the elements named want.
type viewSome struct {
	*viewAll
	want string
	fn   func()
}

func (c *viewSome) Start(el any, d int) (xdmburst.Action, error) {
	if el.(*Node).Name().Local == c.want {
		c.view()
		c.fn()
	}
	return xdmburst.Keep, nil
}

// The leaves a stream tells -- text, comments, processing instructions --
// are taken back in place (dropLeaf), not through RemoveLastChild's general
// clean-up, which a leaf never needs and which cost a drain about a tenth
// of its time. With positions tracked the tree has source tables to clear,
// and every leaf goes the general way; what is read is the same.
func TestDriveDropsLeavesInPlace(t *testing.T) {
	var b strings.Builder
	b.WriteString("<a>")
	for i := range 1000 {
		fmt.Fprintf(&b, "<r i='%d'>t%d<!--c--><?p x?></r>\n", i, i)
	}
	b.WriteString("</a>")
	for _, track := range []bool{false, true} {
		c := &keepAll{}
		bs := driver(c)
		tree, err := parse(strings.NewReader(b.String()), "", ParseOptions{bursts: bs, TrackPositions: track})
		if err != nil {
			t.Fatal(err)
		}
		want := c.leaves
		if track {
			want = 0
		}
		if c.leaves != 4*1000 || bs.leafDrops != want || tree.n > 1 {
			t.Fatalf("track=%v: %d leaves told, %d dropped in place (want %d), %d records left",
				track, c.leaves, bs.leafDrops, want, tree.n)
		}
	}
}

// quietWatch is keepAll with a watch it never turns on (xdmburst.Watching).
type quietWatch struct {
	keepAll
	watched int
}

func (c *quietWatch) Watcher(func())               {}
func (c *quietWatch) Watch(any, int, bool, uint64) { c.watched++ }

// A client whose watch is off is not called for every node: a stream with no
// observer paid a call and an ordinal per node for nothing.
func TestDriveWatchOffCostsNothing(t *testing.T) {
	src := "<a>" + strings.Repeat("<r i='1'>t<!--c--><s/></r>\n", 100) + "</a>"
	c := &quietWatch{}
	if _, err := parse(strings.NewReader(src), "", ParseOptions{bursts: driver(c)}); err != nil {
		t.Fatal(err)
	}
	if c.watched != 0 || c.leaves == 0 {
		t.Fatalf("watch called %d times with the watch off (%d leaves told)", c.watched, c.leaves)
	}
}
