package xdm

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/knroy/go-xml/v2/internal/xdmclone"
)

// dumpTree writes everything Copy carries about n and its subtree, with the
// links walked both ways.
func dumpTree(b *strings.Builder, n *Node) {
	line, col, ok := n.Position()
	fmt.Fprintf(b, "%v %v %q base=%q doc=%q typing=%+v env=%p pos=%d:%d:%v parent=%v ns=%v\n",
		n.Kind(), n.Name(), n.Value(), n.BaseURI(), n.DocumentURI(), TypingOf(n), n.TypeEnv(),
		line, col, ok, n.Parent() != nil, n.InScopeNamespaces())
	for a := range n.Attrs() {
		dumpTree(b, a)
	}
	back := 0
	for c := n.LastChild(); c != nil; c = c.PrevSibling() {
		back++
	}
	fmt.Fprintf(b, "back=%d\n", back)
	for c := range n.Children() {
		dumpTree(b, c)
	}
}

func dump(n *Node) string {
	var b strings.Builder
	dumpTree(&b, n)
	return b.String()
}

// TestDetachedCloneIsCopy checks the bulk clone's Detached mode against Copy,
// which XQuery's validate used to make its operand's copy with, for a
// document and for an element inside one, with typing, type environments
// and base URIs present to be dropped or kept.
func TestDetachedCloneIsCopy(t *testing.T) {
	tree, err := ParseString(`<!DOCTYPE r [<!ENTITY e "x">]><r xmlns:p="urn:p" xml:base="http://ex/a/">
  <p:e a="1" b="2">t<!--c--><?pi x?></p:e>
  <f xml:base="sub/"><g/>text</f>
</r>`, ParseOptions{AllowDOCTYPE: true, TrackPositions: true, BaseURI: "http://ex/d.xml", DocumentURI: "http://ex/d.xml"})
	if err != nil {
		t.Fatal(err)
	}
	r := tree.Root.FirstChild()
	env := NewTypeEnvironment()
	r.FirstChild().NextSibling().ApplyTyping(Typing{TypeAnnotation: "t", IsID: true})
	r.FirstChild().NextSibling().SetTypeEnv(env)
	r.FirstChild().NextSibling().AttrAt(0).ApplyTyping(Typing{TypeAnnotation: "a"})
	for _, n := range []*Node{tree.Root, r, r.FirstChild().NextSibling(), r.LastChild().PrevSibling()} {
		m := xdmclone.Clone(n, xdmclone.Options{Detached: true})
		got := m(n).(*Node)
		if want := dump(Copy(n)); dump(got) != want {
			t.Errorf("%v: detached clone differs from Copy\nwant\n%s\ngot\n%s", n.Name(), want, dump(got))
		}
		if got.Tree() != nil {
			t.Errorf("%v: detached clone is in a document tree", n.Name())
		}
	}
}

// TestCloneIsIndependent checks that the clone shares the original's stores
// without seeing what is added to either afterwards.
func TestCloneIsIndependent(t *testing.T) {
	tree, err := ParseString(`<r xmlns:p="urn:p"><a>one</a><b>two</b></r>`, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := dump(tree.Root)
	m := xdmclone.Clone(tree.Root, xdmclone.Options{})
	c := m(tree.Root).(*Node)
	if c.Tree() == nil || c.Tree() == tree || dump(c) != before {
		t.Fatalf("clone of the document:\n%s", dump(c))
	}
	ca := m(tree.Root.FirstChild().FirstChild()).(*Node)
	ca.FirstChild().SetValue("changed")
	ca.AddNamespace("q", "urn:q")
	ca.SetName(QName{Local: "renamed"})
	if dump(tree.Root) != before {
		t.Errorf("editing the clone changed the original:\n%s", dump(tree.Root))
	}
	if got := ca.StringValue(); got != "changed" || ca.Name().Local != "renamed" {
		t.Errorf("clone after edits: %q %v", got, ca.Name())
	}
}

// A clone copies the name index but not the parser's name cache, so a clone
// with more names than smallNames must still intern: the cache is made on
// first use instead of being dereferenced while nil.
func TestCloneInternsPastSmallNames(t *testing.T) {
	var b strings.Builder
	b.WriteString("<r>")
	for i := range 2 * smallNames {
		fmt.Fprintf(&b, "<e%d/>", i)
	}
	b.WriteString("</r>")
	tree, err := ParseString(b.String(), ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := xdmclone.Clone(tree.Root, xdmclone.Options{})(tree.Root).(*Node)
	c := got.Tree()
	if c == nil || c.nameIx == nil {
		t.Fatalf("clone has no name index to exercise (tree %v)", c)
	}
	old := c.intern(QName{Local: "e3"})
	if c.names[old] != (QName{Local: "e3"}) {
		t.Errorf("existing name interned at %d, which holds %v", old, c.names[old])
	}
	fresh := c.intern(QName{Local: "new"})
	if fresh == old || c.names[fresh] != (QName{Local: "new"}) {
		t.Errorf("new name interned at %d, which holds %v", fresh, c.names[fresh])
	}
}

// A tree's parse-only fields live behind Tree.source, nil for a constructed
// tree. The bulk clone gives a document copy a source part of its own (it
// holds a sync.Once and the offsets the copy writes), and every reader of
// those fields must cope with nil: a constructed tree cloned with Positions,
// a parsed one cloned without, positions copied in from another document.
func TestCloneSourcePart(t *testing.T) {
	if s := unsafe.Sizeof(Tree{}); s > 320 {
		t.Errorf("Tree is %d bytes, want at most 320 (a 320-byte size class)", s)
	}
	src := "<r>\n <a x='1'>t</a>\n <b/>\n</r>"
	tree, err := ParseString(src, ParseOptions{TrackPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	tree.ownSource().externalSubset = "<!-- ext -->"
	before := dump(tree.Root)

	withPos := xdmclone.Clone(tree.Root, xdmclone.Options{Positions: true})(tree.Root).(*Node)
	if dump(withPos) != before {
		t.Errorf("clone with positions:\n%s\nwant\n%s", dump(withPos), before)
	}
	c := withPos.Tree()
	if c.source == nil || c.source == tree.source || c.extSubset() != "<!-- ext -->" || !c.HasPositions() {
		t.Fatalf("clone's source part: %+v (original %p)", c.source, tree.source)
	}
	xdmclone.DropPositions(withPos)
	if dump(tree.Root) != before {
		t.Errorf("dropping the clone's positions changed the original:\n%s", dump(tree.Root))
	}
	if _, _, ok := withPos.FirstChild().Position(); ok || c.extSubset() != "<!-- ext -->" {
		t.Errorf("after DropPositions: position kept or external subset lost")
	}

	noPos := xdmclone.Clone(tree.Root, xdmclone.Options{})(tree.Root).(*Node)
	if noPos.Tree().HasPositions() {
		t.Errorf("clone without positions has positions")
	}

	// A constructed document holding a copy positioned in another document,
	// then cloned with positions: the foreign position travels.
	built := NewTree()
	var a *Node
	for ch := range tree.Root.FirstChild().Children() {
		if ch.Kind() == KindElement {
			a = ch
			break
		}
	}
	ca := built.Root.AppendCopy(a)
	CopyPosition(ca, a)
	built.Finalize()
	wantLine, wantCol, ok := a.Position()
	if !ok {
		t.Fatal("parsed element has no position")
	}
	got := xdmclone.Clone(built.Root, xdmclone.Options{Positions: true})(built.Root).(*Node)
	if l, c, ok := got.FirstChild().Position(); !ok || l != wantLine || c != wantCol {
		t.Errorf("foreign position in clone: %d:%d %v, want %d:%d", l, c, ok, wantLine, wantCol)
	}

	// A fragment has no source part, and its clone none either.
	frag := NewNode(KindElement, QName{Local: "e"}, "")
	frag.AppendText("x")
	if frag.tree.source != nil {
		t.Errorf("a constructed fragment has a source part")
	}
	fc := xdmclone.Clone(frag, xdmclone.Options{Positions: true})(frag).(*Node)
	if fc.tree.source != nil || fc.StringValue() != "x" {
		t.Errorf("fragment clone: source %p, value %q", fc.tree.source, fc.StringValue())
	}
	if _, _, ok := fc.Position(); ok {
		t.Errorf("fragment clone reports a position")
	}
}
