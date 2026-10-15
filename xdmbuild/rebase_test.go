package xdmbuild_test

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xdmbuild"
)

// rebaseFull is Rebase without the early stop for a subtree that already has
// the right base: the walk every result is checked against.
func rebaseFull(n *xdm.Node, parentBase string) {
	if n == nil || n.Kind() != xdm.KindElement {
		return
	}
	base := parentBase
	for a := range n.Attrs() {
		if a.Name().URI == xdm.NSXML && a.Name().Local == "base" {
			base = xdmbuild.ResolveAgainst(parentBase, a.Value())
			if a.Value() != "" && base == a.Value() {
				base = a.Value()
			}
			break
		}
	}
	if base == "" {
		return
	}
	n.SetBaseURI(base)
	for ch := range n.Children() {
		rebaseFull(ch, base)
	}
}

// Rebase stops early only where the full walk would change nothing: every
// node's base URI after a copy into a new parent is the same either way.
func TestRebaseStopMatchesFullWalk(t *testing.T) {
	const a = "http://a.example/d/doc.xml"
	cases := []struct{ name, src, srcBase, parentBase string }{
		{"no bases, same parent base", `<r><s><t/></s><u/></r>`, a, a},
		{"no bases, new parent base", `<r><s><t/></s><u/></r>`, a, "http://b.example/m/"},
		{"no bases, parent without base", `<r><s><t/></s></r>`, a, ""},
		{"nested xml:base, same parent base", `<r><s xml:base="x/"><t xml:base="y/"><v/></t></s></r>`, a, a},
		{"nested xml:base, new parent base", `<r><s xml:base="x/"><t xml:base="/y/"><v/></t></s></r>`, a, "http://b.example/m/"},
		{"absolute xml:base deep", `<r><s><t xml:base="http://c.example/"><v/></t></s></r>`, a, a},
		{"root xml:base, new parent base", `<r xml:base="z/"><s/></r>`, a, "http://b.example/m/"},
		{"source without base", `<r><s><t/></s></r>`, "", "http://b.example/m/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := xdm.ParseString(tc.src, xdm.ParseOptions{BaseURI: tc.srcBase})
			if err != nil {
				t.Fatal(err)
			}
			for _, src := range []*xdm.Node{doc.Root.FirstChild(), doc.Root.FirstChild().FirstChild()} {
				var got, want strings.Builder
				for i, rebase := range []func(*xdm.Node, string){xdmbuild.Rebase, rebaseFull} {
					p := xdm.NewNode(xdm.KindElement, xdm.QName{Local: "p"}, "")
					if tc.parentBase != "" {
						p.SetBaseURI(tc.parentBase)
					}
					c := p.AppendCopy(src)
					rebase(c, p.BaseURI())
					if i == 0 {
						dump(&got, p)
					} else {
						dump(&want, p)
					}
				}
				if got.String() != want.String() {
					t.Errorf("copy of %s:\n got %s\nwant %s", src.Name().Local, got.String(), want.String())
				}
			}
		})
	}
}

// A subtree with a base of its own below the top, set without any xml:base
// attribute, is walked: stopping at the top would leave it stale.
func TestRebaseWalksOwnBaseBelow(t *testing.T) {
	p := xdm.NewNode(xdm.KindElement, xdm.QName{Local: "p"}, "")
	p.SetBaseURI("http://a.example/")
	r := p.AppendElement(xdm.QName{Local: "r"})
	s := r.AppendElement(xdm.QName{Local: "s"})
	s.SetBaseURI("http://stale.example/")
	if r.DescendantsInheritBase() {
		t.Fatal("DescendantsInheritBase is true with an own base below")
	}
	xdmbuild.Rebase(r, "http://a.example/")
	if got := s.BaseURI(); got != "http://a.example/" {
		t.Errorf("base-uri(s) = %q, want http://a.example/", got)
	}
}

// An xml:base attribute below the top that no base URI reflects yet -- a
// constructed element before its rebase -- is walked too.
func TestRebaseWalksXMLBaseBelow(t *testing.T) {
	p := xdm.NewNode(xdm.KindElement, xdm.QName{Local: "p"}, "")
	p.SetBaseURI("http://a.example/")
	r := p.AppendElement(xdm.QName{Local: "r"})
	s := r.AppendElement(xdm.QName{Local: "s"})
	s.AppendAttr(xdm.QName{URI: xdm.NSXML, Prefix: "xml", Local: "base"}, "sub/")
	if r.DescendantsInheritBase() {
		t.Fatal("DescendantsInheritBase is true with an xml:base below")
	}
	xdmbuild.Rebase(r, "http://a.example/")
	if got := s.BaseURI(); got != "http://a.example/sub/" {
		t.Errorf("base-uri(s) = %q, want http://a.example/sub/", got)
	}
}
