package xdmbuild_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xdmbuild"
)

// dump writes n's subtree with what a copy has to carry: kind, name, value,
// base URI and the namespaces each element declares itself.
func dump(sb *strings.Builder, n *xdm.Node) {
	fmt.Fprintf(sb, "(%v %s %q %s", n.Kind(), n.Name().Clark(), n.Value(), n.BaseURI())
	for p, u := range n.DeclaredNamespaces() {
		fmt.Fprintf(sb, " ns:%s=%s", p, u)
	}
	for a := range n.Attrs() {
		fmt.Fprintf(sb, " @%s=%q", a.Name().Clark(), a.Value())
	}
	for c := range n.Children() {
		dump(sb, c)
	}
	sb.WriteString(")")
}

// AppendCopyOf is AppendNode(xdm.Copy(n)) without the intermediate copy: the
// same tree, a new identity, and fewer allocations.
func TestAppendCopyOfMatchesCopyThenAppend(t *testing.T) {
	doc, err := xdm.ParseString(`<doc xmlns:q="urn:q" xml:base="http://a.example/d/">`+
		`<e xmlns:p="urn:p" xml:base="x/" p:k="v"><p:f>in</p:f><!--c--><?pi d?></e>tail</doc>`,
		xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	top := doc.Root.FirstChild()
	src := []*xdm.Node{top.FirstChild(), top.LastChild()} // <e>, "tail"
	src = append(src, src[0].LastChild(), src[0].LastChild().PrevSibling())

	build := func(appendOne func(*xdmbuild.Builder, *xdm.Node)) (*xdm.Node, *xdm.Node) {
		b := xdmbuild.New(xqueryLike{})
		el := b.StartElement(qn("out"))
		el.Open().SetBaseURI("http://b.example/o/")
		el.AppendText("pre-")
		for _, n := range src {
			appendOne(el, n)
		}
		root := b.ToTree()
		return root, root.FirstChild().FirstChild().NextSibling()
	}
	once, e1 := build(func(b *xdmbuild.Builder, n *xdm.Node) { b.AppendCopyOf(n) })
	twice, _ := build(func(b *xdmbuild.Builder, n *xdm.Node) { b.AppendNode(xdm.Copy(n)) })

	var a, w strings.Builder
	dump(&a, once)
	dump(&w, twice)
	if a.String() != w.String() {
		t.Fatalf("AppendCopyOf built\n%s\nwant\n%s", a.String(), w.String())
	}
	if e1.Is(src[0]) || e1.Name().Local != "e" {
		t.Fatalf("copy of <e> is %v, identical to the source: %v", e1.Name(), e1.Is(src[0]))
	}
	if got := e1.BaseURI(); got != "http://b.example/o/x/" {
		t.Errorf("copy base URI %q, want it rebased under the new parent", got)
	}

	allocs := func(appendOne func(*xdmbuild.Builder)) float64 {
		return testing.AllocsPerRun(50, func() {
			b := xdmbuild.New(xqueryLike{})
			appendOne(b.StartElement(qn("out")))
		})
	}
	one := allocs(func(b *xdmbuild.Builder) { b.AppendCopyOf(src[0]) })
	two := allocs(func(b *xdmbuild.Builder) { b.AppendNode(xdm.Copy(src[0])) })
	if one >= two {
		t.Errorf("AppendCopyOf made %.0f allocations, copy-then-append %.0f: want fewer", one, two)
	}
}
