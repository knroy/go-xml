package xsd

import (
	"testing"

	"github.com/knroy/go-xml/v2/internal/xdmclone"
	"github.com/knroy/go-xml/v2/xdm"
)

// layerSchema adds what copySchema lacks: whitespace facets on an element's
// and a defaulted attribute's value, a fixed attribute, a named mixed type,
// a lax wildcard, and a defaulted attribute in a namespace the instance binds
// to no prefix, which XSD 1.1 namespace fixup declares.
const layerSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    xmlns:w="urn:w" targetNamespace="urn:w" elementFormDefault="qualified">
  <xs:simpleType name="tok"><xs:restriction base="xs:string">
    <xs:whiteSpace value="collapse"/></xs:restriction></xs:simpleType>
  <xs:simpleType name="rep"><xs:restriction base="xs:string">
    <xs:whiteSpace value="replace"/></xs:restriction></xs:simpleType>
  <xs:complexType name="para" mixed="true"><xs:sequence>
    <xs:element name="i" type="xs:string" minOccurs="0" maxOccurs="unbounded"/>
  </xs:sequence></xs:complexType>
  <xs:element name="doc"><xs:complexType><xs:sequence>
    <xs:element name="e" maxOccurs="unbounded"><xs:complexType><xs:simpleContent>
      <xs:extension base="w:tok">
        <xs:attribute name="k" type="xs:ID"/>
        <xs:attribute name="r" form="qualified" type="w:rep" default="  a&#9;b  "/>
        <xs:attribute name="f" type="xs:token" fixed="x"/>
      </xs:extension></xs:simpleContent></xs:complexType></xs:element>
    <xs:element name="p" type="w:para"/>
    <xs:element name="lax"><xs:complexType><xs:sequence>
      <xs:any processContents="lax" maxOccurs="unbounded"/></xs:sequence></xs:complexType></xs:element>
  </xs:sequence></xs:complexType></xs:element>
</xs:schema>`

const layerDoc = `<doc xmlns="urn:w">
  <e k="a1">  one   two  </e>
  <e k="a2" xmlns:w="urn:w" w:r="x&#9;y">t</e>
  <p> lead <i>in</i> tail </p>
  <lax><free xmlns="urn:x"><g>1</g></free> <e>bad</e></lax>
</doc>`

// typingFacts lists every node's facts in document order, attributes first.
func typingFacts(n *xdm.Node) []string {
	out := []string{cloneFacts(n)}
	for a := range n.Attrs() {
		out = append(out, cloneFacts(a))
	}
	for c := range n.Children() {
		out = append(out, typingFacts(c)...)
	}
	return out
}

func sameFacts(t *testing.T, label string, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: %d nodes, want %d", label, len(got), len(want))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("%s: node %d differs\n  want %s\n  got  %s", label, i, want[i], got[i])
		}
	}
}

// TestTypedCopyLayerMatchesTypedCopy checks the typed copy made from a typing
// layer over the caller's tree against the one made by typing a copy as the
// assessment goes, which is what ValidateCopy did before the layer and still
// does when xdmclone.NewLayer declines: the same tree, typing, defaults,
// fixups, stripped whitespace and error text, with the source left as it was.
func TestTypedCopyLayerMatchesTypedCopy(t *testing.T) {
	copyS := loadAssertionSchema(t, copySchema)
	layerS := loadAssertionSchema(t, layerSchema)
	parse := func(src string) *xdm.Node {
		tree, err := xdm.ParseString(src, xdm.ParseOptions{TrackPositions: true})
		if err != nil {
			t.Fatal(err)
		}
		return tree.Root
	}
	cases := []struct {
		name string
		s    *Schema
		in   func() *xdm.Node
		run  func(*Schema, *xdm.Node) (*xdm.Node, error)
	}{
		{"valid document", copyS, func() *xdm.Node {
			return parseCopyDoc(t, copyDoc("100", `<n xsi:nil="true"/>`, "1")).Root
		}, nil},
		{"invalid document", copyS, func() *xdm.Node {
			return parseCopyDoc(t, copyDoc("1.5", `<n>x</n>`, "-9")).Root
		}, nil},
		{"union member and nil", copyS, func() *xdm.Node {
			return parseCopyDoc(t, copyDoc("abc", `<n xsi:nil="true"></n>`, "3")).Root
		}, nil},
		{"document element", copyS, func() *xdm.Node {
			return parseCopyDoc(t, copyDoc("7", `<n>5</n>`, "1")).Root.ChildElements()[0]
		}, nil},
		{"element deep in a document", copyS, func() *xdm.Node {
			return pickCopyTarget(parseCopyDoc(t, copyDoc("7", `<n>5</n>`, "1")).Root, "item")
		}, nil},
		{"whitespace, defaults and fixup", layerS, func() *xdm.Node { return parse(layerDoc) }, nil},
		{"detached element", layerS, func() *xdm.Node {
			return xdm.Copy(parse(layerDoc).ChildElements()[0])
		}, nil},
		{"lax element", layerS, func() *xdm.Node { return parse(layerDoc).ChildElements()[0] },
			func(s *Schema, n *xdm.Node) (*xdm.Node, error) {
				return s.ValidateElementLaxCopy(n, ValidateOptions{MaxErrors: -1})
			}},
		{"parentless attribute against a type", copyS, func() *xdm.Node {
			return xdm.NewNode(xdm.KindAttribute, xdm.QName{Local: "k"}, " v1 ")
		}, func(s *Schema, n *xdm.Node) (*xdm.Node, error) {
			return s.ValidateAgainstTypeCopy(n, xsName("ID"), ValidateOptions{})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := c.run
			if run == nil {
				run = func(s *Schema, n *xdm.Node) (*xdm.Node, error) {
					return s.ValidateCopy(n, ValidateOptions{MaxErrors: -1})
				}
			}
			newLayer := xdmclone.NewLayer
			layers := 0
			xdmclone.NewLayer = func(top any) any { layers++; return newLayer(top) }
			in := c.in()
			top := in
			for top.Parent() != nil {
				top = top.Parent()
			}
			before := typingFacts(top)
			got, err := run(c.s, in)
			xdmclone.NewLayer = func(any) any { return nil }
			want, errWant := run(c.s, c.in())
			xdmclone.NewLayer = newLayer

			whole := in.Parent() == nil || in.Parent().Kind() == xdm.KindDocument ||
				in.Kind() == xdm.KindDocument
			if whole != (layers > 0) {
				t.Fatalf("whole tree %v, but %d layers made", whole, layers)
			}
			if errText(err) != errText(errWant) {
				t.Fatalf("errors differ\n  want %s\n  got  %s", errText(errWant), errText(err))
			}
			if (got == nil) != (want == nil) {
				t.Fatalf("result %v, want %v", got, want)
			}
			if got == nil {
				return
			}
			wantTop, gotTop := want, got
			for wantTop.Parent() != nil {
				wantTop = wantTop.Parent()
			}
			for gotTop.Parent() != nil {
				gotTop = gotTop.Parent()
			}
			if (wantTop == want) != (gotTop == got) {
				t.Fatalf("result is the top: %v, want %v", gotTop == got, wantTop == want)
			}
			sameFacts(t, "typed copy", typingFacts(wantTop), typingFacts(gotTop))
			sameFacts(t, "source after", before, typingFacts(top))
		})
	}
}

// TestTypedCopyLayerIsNotVacuous makes sure the cases above reach what the
// layer must carry: the typing that exists only in the copy.
func TestTypedCopyLayerIsNotVacuous(t *testing.T) {
	s := loadAssertionSchema(t, layerSchema)
	tree, err := xdm.ParseString(layerDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.ValidateCopy(tree.Root, ValidateOptions{MaxErrors: -1})
	doc := got.ChildElements()[0]
	if n := len(doc.ChildElements()); doc.NumChildren() != n {
		t.Errorf("ignorable whitespace kept: %d children, %d elements", doc.NumChildren(), n)
	}
	e := doc.ChildElements()[0]
	if e.TypeAnnotation() == "" || !e.Attr("", "k").IsID() {
		t.Errorf("e: annotation %q, @k is-id %v", e.TypeAnnotation(), e.Attr("", "k").IsID())
	}
	if r := e.Attr("urn:w", "r"); r == nil || r.Value() != "  a b  " || r.Name().Prefix == "" {
		t.Errorf("defaulted @w:r: %v", r)
	}
	if !doc.ChildElements()[2].MixedContent() {
		t.Errorf("p is not marked mixed")
	}
	for n := range tree.Root.Descendants() {
		if n.TreeHasTyping() {
			t.Fatalf("the source tree was given typing")
		}
	}
}
