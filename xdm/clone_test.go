package xdm

import (
	"fmt"
	"strings"
	"testing"

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
