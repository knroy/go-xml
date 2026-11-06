package xdm

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/internal/xdmburst"
)

// groundDeep grounds n empty elements under depth attribute-free ones, each
// at once (Ground), and returns the parse's error and its burster.
func groundDeep(depth, n int) (*burster, *groundAt, error) {
	g := &groundAt{d: depth + 2}
	bs := driver(g)
	_, err := parse(strings.NewReader(spineAttrsDoc(depth, 0, n)), "", ParseOptions{bursts: bs})
	return bs, g, err
}

// Small elements grounded at once under a deep spine share the copies of
// their ancestors (ground): the spine is copied once per batch, not once per
// copy, so 20,000 copies under 900 levels, 18M ancestor records apiece, are
// not refused.
func TestDriveGroundSharesTheSpine(t *testing.T) {
	const depth, n = 900, 20000
	bs, g, err := groundDeep(depth, n)
	if err != nil || g.grounded != n {
		t.Fatalf("%d grounded: %v", g.grounded, err)
	}
	// A batch holds burstBatchRecords records: the spine's copies (the
	// document node, depth elements and r) and the copies after them.
	batches := n/(burstBatchRecords-depth-2) + 1
	if max := (depth + 1) * batches; bs.spineCopies > max {
		t.Fatalf("%d ancestor records copied for %d records, want at most %d (%d batches)", bs.spineCopies, bs.burstRecs, max, batches)
	}
}

// Without the sharing, the same parse copies the spine once a copy and is
// refused (past a lower floor, to keep the test small): the test above is
// what the sharing saves.
func TestDriveGroundSharesTheSpineSabotage(t *testing.T) {
	defer func(f int, s bool) { spineCopiesFloor, groundShared = f, s }(spineCopiesFloor, groundShared)
	spineCopiesFloor, groundShared = 1<<20, false
	_, _, err := groundDeep(900, 20000)
	if !errors.Is(err, ErrResourceLimit) || !strings.Contains(err.Error(), "ancestor nodes for") {
		t.Fatalf("got %v, want the spine copies refused", err)
	}
}

// A parent of many attributes is copied once per batch too: a batch holds
// four times what the open path's copies take.
func TestDriveGroundManyAttrParentShared(t *testing.T) {
	const s, n = 5000, 20000
	seen := 0
	c := &groundKeep{fn: func(x *Node) {
		if x.Parent().Attr("", "a4999") == nil {
			t.Fatalf("copy %d: its parent's attributes are missing", seen)
		}
		seen++
	}, at: func(q QName) bool { return q.Local == "x" }}
	bs := driver(c)
	if _, err := parse(strings.NewReader(spineAttrsDoc(0, s, n)), "", ParseOptions{bursts: bs}); err != nil {
		t.Fatal(err)
	}
	if seen != n || bs.spineCopies > 4*(s+1) {
		t.Fatalf("%d copies; %d ancestor records copied for %d attributes", seen, bs.spineCopies, s)
	}
}

// groundKeep grounds every element at reports true for, at once, and keeps
// the others on the spine; fn is told of each copy.
type groundKeep struct {
	at func(QName) bool
	fn func(*Node)
}

func (c *groundKeep) Start(el any, _ int) (xdmburst.Action, error) {
	if c.at(el.(*Node).Name()) {
		return xdmburst.Ground, nil
	}
	return xdmburst.Keep, nil
}
func (c *groundKeep) Leaf(any, int) error { return nil }
func (c *groundKeep) End(_ any, _ int, g any) error {
	if g != nil {
		c.fn(g.(*Node))
	}
	return nil
}
func (c *groundKeep) Watch(any, int, bool, uint64) {}
func (c *groundKeep) Later(any) error              { return nil }

// groundSiblingsDoc is p sections, each of k elements x (some under a t,
// some with x inside) between text and comments, enough for several
// batches.
func groundSiblingsDoc(p, k int) string {
	var b strings.Builder
	b.WriteString(`<doc a="1">`)
	for i := range p {
		fmt.Fprintf(&b, `<s id="s%d" b="%d">lead`, i, i)
		for j := range k {
			switch j % 4 {
			case 0:
				fmt.Fprintf(&b, `<x i="%d-%d"><y/><y>t</y></x>`, i, j)
			case 1:
				fmt.Fprintf(&b, `txt<x i="%d-%d"/><!--c-->`, i, j)
			case 2:
				fmt.Fprintf(&b, `<t n="%d"><x i="%d-%d"><x i="in"/></x></t>`, j, i, j)
			default:
				fmt.Fprintf(&b, `<x i="%d-%d">v<?pi?></x>`, i, j)
			}
		}
		b.WriteString(`tail</s>`)
	}
	b.WriteString(`</doc>`)
	return b.String()
}

// facts is what a copy answers that a later copy in its batch could
// change: its siblings and its descendants', its ancestors' names,
// attributes and posture, and whether their content faults.
func facts(x *Node) string {
	var b strings.Builder
	name := func(n *Node) string {
		if n == nil {
			return "-"
		}
		return n.Kind().String() + ":" + n.Name().Local + "=" + n.Value()
	}
	fault := func(f func()) (s string) {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(*streamtrace.PostureError); !ok {
					panic(r)
				}
				s = "fault"
			}
		}()
		f()
		return "ok"
	}
	fmt.Fprintf(&b, "%s next %s prev %s|", name(x), name(x.NextSibling()), name(x.PrevSibling()))
	for d := range x.Descendants() {
		fmt.Fprintf(&b, "%s next %s prev %s|", name(d), name(d.NextSibling()), name(d.PrevSibling()))
	}
	for p := x.Parent(); p != nil; p = p.Parent() {
		fmt.Fprintf(&b, "^%s", p.Name().Local)
		for a := range p.Attrs() {
			fmt.Fprintf(&b, " %s=%s", a.Name().Local, a.Value())
		}
		fmt.Fprintf(&b, " kids:%s next:%s prev:%s", fault(func() { p.FirstChild() }),
			fault(func() { p.NextSibling() }), fault(func() { p.PrevSibling() }))
	}
	return b.String()
}

// A copy handed out of the shared batch answers as one in a tree of its own
// (deliver) does, at once and after every later copy has joined its batch:
// no sibling, its ancestors' content a posture fault. Its order, identity
// and generate-id, and its ancestors', are the originals' in the tree.
func TestDriveGroundSharedAnswersAsItsOwn(t *testing.T) {
	doc := groundSiblingsDoc(40, 60)
	isX := func(q QName) bool { return q.Local == "x" }
	run := func(shared bool) (copies []*Node, atOnce []string) {
		defer func(s bool) { groundShared = s }(groundShared)
		groundShared = shared
		c := &groundKeep{at: isX, fn: func(x *Node) {
			copies = append(copies, x)
			atOnce = append(atOnce, facts(x))
		}}
		if _, err := parse(strings.NewReader(doc), "", ParseOptions{bursts: driver(c)}); err != nil {
			t.Fatal(err)
		}
		return copies, atOnce
	}
	sc, sOnce := run(true)
	_, dOnce := run(false)
	if len(sc) != 40*60 || len(dOnce) != len(sc) {
		t.Fatalf("%d and %d copies", len(sc), len(dOnce))
	}
	trees := map[*Tree]bool{}
	for i, x := range sc {
		trees[x.tree] = true
		if sOnce[i] != dOnce[i] {
			t.Fatalf("copy %d answers\n%s\nwhere its own tree's answers\n%s", i, sOnce[i], dOnce[i])
		}
		if f := facts(x); f != sOnce[i] {
			t.Fatalf("copy %d, kept: answers\n%s\nwhere it answered\n%s", i, f, sOnce[i])
		}
	}
	if len(trees) < 2 || len(trees) > len(sc)/100 {
		t.Fatalf("%d copies in %d trees", len(sc), len(trees))
	}

	tree, err := ParseString(doc, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var orig []*Node
	for n := range tree.Root.Descendants() {
		if n.Kind() == KindElement && isX(n.Name()) && !isX(n.Parent().Name()) {
			orig = append(orig, n)
		}
	}
	if len(orig) != len(sc) {
		t.Fatalf("%d originals for %d copies", len(orig), len(sc))
	}
	gidEq := func(a, b *Node) bool { return a.generateID() == b.generateID() }
	for i := range sc {
		for _, j := range []int{i + 1, i + 2, i + 5, i + 61, len(sc) - 1 - i} {
			if j < 0 || j >= len(sc) {
				continue
			}
			a, b, oa, ob := sc[i], sc[j], orig[i], orig[j]
			pairs := [][4]*Node{{a, b, oa, ob}, {a.Parent(), b.Parent(), oa.Parent(), ob.Parent()},
				{a, b.Parent(), oa, ob.Parent()}, {a.Parent(), b, oa.Parent(), ob}, {a.Root(), b, oa.Root(), ob}}
			for k, p := range pairs {
				if p[0].Compare(p[1]) != p[2].Compare(p[3]) || p[0].Is(p[1]) != p[2].Is(p[3]) ||
					gidEq(p[0], p[1]) != gidEq(p[2], p[3]) || p[0].Identity() == p[1].Identity() != p[2].Is(p[3]) {
					t.Fatalf("copies %d and %d, pair %d: compare %d is %v gid %v where the tree's are %d %v %v",
						i, j, k, p[0].Compare(p[1]), p[0].Is(p[1]), gidEq(p[0], p[1]),
						p[2].Compare(p[3]), p[2].Is(p[3]), gidEq(p[2], p[3]))
				}
			}
		}
	}
}
