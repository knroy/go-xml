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
// tree. The bulk clone gives a document copy a source part of its own, with
// the external subset and without positions, and every reader of those fields
// must cope with nil: a constructed fragment's clone has none.
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

	c := xdmclone.Clone(tree.Root, xdmclone.Options{})(tree.Root).(*Node)
	if dump(tree.Root) != before || c.StringValue() != tree.Root.StringValue() {
		t.Errorf("clone %q changed the original:\n%s", c.StringValue(), dump(tree.Root))
	}
	ct := c.Tree()
	if ct.source == nil || ct.source == tree.source || ct.extSubset() != "<!-- ext -->" {
		t.Fatalf("clone's source part: %+v (original %p)", ct.source, tree.source)
	}
	if ct.HasPositions() {
		t.Errorf("clone has positions")
	}
	if _, _, ok := tree.Root.FirstChild().Position(); !ok {
		t.Errorf("cloning lost the original's positions")
	}

	// A fragment has no source part, and its clone none either.
	frag := NewNode(KindElement, QName{Local: "e"}, "")
	frag.AppendText("x")
	if frag.tree.source != nil {
		t.Errorf("a constructed fragment has a source part")
	}
	fc := xdmclone.Clone(frag, xdmclone.Options{})(frag).(*Node)
	if fc.tree.source != nil || fc.StringValue() != "x" {
		t.Errorf("fragment clone: source %p, value %q", fc.tree.source, fc.StringValue())
	}
	if _, _, ok := fc.Position(); ok {
		t.Errorf("fragment clone reports a position")
	}
}

// An element with more attributes than a record's 16-bit count holds keeps
// its count in the tree's rarely set part; parsing, cloning and building
// must all read it back. (The count moved there so that Tree stays in the
// 320-byte size class beside the element-name index.)
func TestManyAttributesCount(t *testing.T) {
	const n = 0xFFFE + 10
	var b strings.Builder
	b.WriteString("<r")
	for i := range n {
		fmt.Fprintf(&b, " a%d=''", i)
	}
	b.WriteString("/>")
	tree, err := ParseString(b.String(), ParseOptions{MaxBytes: -1, MaxNodes: -1})
	if err != nil {
		t.Fatal(err)
	}
	r := tree.Root.FirstChild()
	if got := r.NumAttrs(); got != n {
		t.Fatalf("parsed: %d attributes, want %d", got, n)
	}
	c := xdmclone.Clone(tree.Root, xdmclone.Options{})(tree.Root).(*Node)
	if got := c.FirstChild().NumAttrs(); got != n {
		t.Errorf("cloned: %d attributes, want %d", got, n)
	}
	built := NewTree()
	e := built.Root.AppendElement(QName{Local: "r"})
	for i := range n {
		e.AppendAttr(QName{Local: fmt.Sprintf("a%d", i)}, "")
	}
	if got := e.NumAttrs(); got != n {
		t.Errorf("built: %d attributes, want %d", got, n)
	}
	if s := unsafe.Sizeof(Tree{}); s > 320 {
		t.Errorf("Tree is %d bytes, want at most 320", s)
	}
}
