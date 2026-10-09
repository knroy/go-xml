package xsd

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

const copySchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    xmlns:t="urn:t" targetNamespace="urn:t" elementFormDefault="qualified">
  <xs:simpleType name="u"><xs:union memberTypes="xs:integer xs:NCName"/></xs:simpleType>
  <xs:simpleType name="l"><xs:list itemType="xs:integer"/></xs:simpleType>
  <xs:element name="q" type="xs:QName"/>
  <xs:element name="r"><xs:complexType><xs:sequence>
    <xs:element name="item" maxOccurs="unbounded"><xs:complexType><xs:simpleContent>
      <xs:extension base="t:u">
        <xs:attribute name="id" type="xs:ID"/>
        <xs:attribute name="ref" type="xs:IDREFS"/>
        <xs:attribute name="d" type="xs:string" default="dflt"/>
        <xs:anyAttribute namespace="http://www.w3.org/XML/1998/namespace" processContents="skip"/>
      </xs:extension></xs:simpleContent></xs:complexType></xs:element>
    <xs:element name="nums" type="t:l"/>
    <xs:element name="n" type="xs:int" nillable="true"/>
    <xs:element name="m"><xs:complexType mixed="true"><xs:sequence>
      <xs:element name="b" type="xs:string"/></xs:sequence></xs:complexType></xs:element>
    <xs:element ref="t:q"/>
    <xs:element name="any" type="xs:anyType"/>
    <xs:element name="free"><xs:complexType><xs:sequence>
      <xs:any processContents="skip"/></xs:sequence></xs:complexType></xs:element>
    <xs:element name="pic"><xs:complexType>
      <xs:attribute name="src" type="xs:ENTITY"/></xs:complexType></xs:element>
    <xs:element name="asserted"><xs:complexType><xs:sequence>
      <xs:element name="v" type="xs:integer" maxOccurs="unbounded"/></xs:sequence>
      <xs:assert test="sum(t:v) gt 0"/></xs:complexType></xs:element>
  </xs:sequence>
  <xs:anyAttribute namespace="http://www.w3.org/XML/1998/namespace" processContents="skip"/></xs:complexType></xs:element>
</xs:schema>`

// copyDoc fills in the body of a document exercising every PSVI property the
// validator records, plus the DTD, base URI and namespace context it reads.
func copyDoc(item1, n, v string) string {
	return `<!DOCTYPE r [<!NOTATION gif SYSTEM "gif"><!ENTITY pic SYSTEM "pic.gif" NDATA gif>]>
<r xmlns="urn:t" xmlns:p="urn:p" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
   xml:base="http://ex/a/">
  <item id="i1">` + item1 + `</item>
  <item ref="i1 i2" id="i2" xml:base="sub/">abc</item>
  <!-- c --><?pi x?>
  <nums> 1 2  3 </nums>
  ` + n + `
  <m> x <b>y</b> z </m>
  <q>p:local</q>
  <any xsi:type="xs:integer" xmlns:xs="http://www.w3.org/2001/XMLSchema">42</any>
  <free><x>k</x></free>
  <pic src="pic"/>
  <asserted><v>` + v + `</v> <v>0</v></asserted>
</r>`
}

func parseCopyDoc(t *testing.T, src string) *xdm.Tree {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{
		AllowDOCTYPE:   true,
		TrackPositions: true,
		BaseURI:        "http://ex/doc.xml",
		DocumentURI:    "http://ex/doc.xml",
	})
	if err != nil {
		t.Fatal(err)
	}
	// <x> sits under a skip wildcard, so validation leaves it as it found
	// it. Typing it here stands for an input annotated by an earlier
	// assessment: the copy must carry that typing over, as the in-place run
	// leaves it in place.
	for _, el := range tree.Root.ChildElements()[0].ChildElements() {
		if el.Name().Local == "free" {
			x := el.ChildElements()[0]
			x.ApplyTyping(xdm.Typing{TypeAnnotation: "string", DerivedPrimitive: "string", IsID: true})
			x.SetTypeEnv(stampEnv)
		}
	}
	return tree
}

// stampEnv is the type environment parseCopyDoc gives the node it stamps.
var stampEnv = xdm.NewTypeEnvironment()

// pickCopyTarget returns the document node, or its first descendant element
// with the given local name.
func pickCopyTarget(root *xdm.Node, local string) *xdm.Node {
	if local == "" {
		return root
	}
	for _, el := range root.ChildElements()[0].ChildElements() {
		if el.Name().Local == local {
			return el
		}
	}
	return nil
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// nodeFacts is everything about one node that validation can set or that a
// consumer of the validated tree reads.
func nodeFacts(n *xdm.Node) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%v %v %q base=%q doc=%q typing=%+v env=%p sv=%q",
		n.Kind(), n.Name(), n.Value(), n.BaseURI(), n.DocumentURI(), xdm.TypingOf(n),
		n.TypeEnv(), n.StringValue())
	if n.Kind() == xdm.KindElement || n.Kind() == xdm.KindAttribute {
		seq, err := xdm.AtomizeChecked(xdm.One(n))
		for _, it := range seq {
			if a, ok := it.(*xdm.Atomic); ok {
				fmt.Fprintf(&b, " %v:%s", a.Type, a.String())
			}
		}
		fmt.Fprintf(&b, " atomize-err=%v", err != nil)
	}
	if n.Kind() == xdm.KindElement {
		fmt.Fprintf(&b, " ns=%v", n.InScopeNamespaces())
	}
	fmt.Fprintf(&b, " #ns=%d #attrs=%d #kids=%d", n.NumNamespaceDecls(), n.NumAttrs(), n.NumChildren())
	return b.String()
}

// sameTree reports the first node at which a and b differ.
func sameTree(t *testing.T, label string, a, b *xdm.Node) {
	t.Helper()
	if fa, fb := nodeFacts(a), nodeFacts(b); fa != fb {
		t.Fatalf("%s: nodes differ\n  want %s\n  got  %s", label, fa, fb)
	}
	for i := range a.NumAttrs() {
		sameTree(t, label, a.AttrAt(i), b.AttrAt(i))
	}
	for i := range a.NumChildren() {
		sameTree(t, label, a.ChildAt(i), b.ChildAt(i))
	}
}

// TestValidateCopyMatchesInPlace is the differential: the same documents
// validated in place and through ValidateCopy must agree on every node's
// typing, values, URIs and namespaces, on the tree's DTD context, and on the
// error text including line and column; and the input to ValidateCopy must
// come out exactly as a fresh parse of it.
func TestValidateCopyMatchesInPlace(t *testing.T) {
	s := loadAssertionSchema(t, copySchema)
	cases := []struct {
		name, doc, target string
		valid             bool
	}{
		{"valid document", copyDoc("100", `<n xsi:nil="true"/>`, "1"), "", true},
		{"invalid document", copyDoc("1.5", `<n>x</n>`, "-9"), "", false},
		{"element inside a document", copyDoc("100", `<n xsi:nil="true"/>`, "1"), "q", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := ValidateOptions{MaxErrors: -1, SkipIDConstraints: c.target != ""}
			inPlace := parseCopyDoc(t, c.doc)
			inOpts := opts
			inOpts.Annotate = true
			errIn := s.Validate(pickCopyTarget(inPlace.Root, c.target), inOpts)
			if (errIn == nil) != c.valid {
				t.Fatalf("in place: %v", errIn)
			}

			input := parseCopyDoc(t, c.doc)
			target := pickCopyTarget(input.Root, c.target)
			got, errCp := s.ValidateCopy(target, opts)
			var ves *ValidationErrors
			if !c.valid && (!errors.As(errCp, &ves) || ves.Errors[0].Line == 0) {
				t.Fatalf("an invalid document's errors carry no position: %v", errCp)
			}
			if errText(errIn) != errText(errCp) {
				t.Fatalf("errors differ\n  in place: %s\n  copy:     %s", errText(errIn), errText(errCp))
			}
			if got == target || got.Name() != target.Name() {
				t.Fatalf("ValidateCopy returned %v, want a copy of %v", got, target)
			}
			top := got
			for top.Parent() != nil {
				top = top.Parent()
			}
			sameTree(t, "copy vs in place", inPlace.Root, top)
			sameTree(t, "input vs fresh parse", parseCopyDoc(t, c.doc).Root, input.Root)

			ct := top.Tree()
			if ct == nil || ct == input {
				t.Fatalf("copy tree = %p, want a new tree", ct)
			}
			if ct.DocType != inPlace.DocType || ct.XMLVersion != inPlace.XMLVersion {
				t.Errorf("tree: DocType/XMLVersion %q/%q, want %q/%q",
					ct.DocType, ct.XMLVersion, inPlace.DocType, inPlace.XMLVersion)
			}
			if sys, _, _, ok := ct.UnparsedEntity("pic"); !ok || sys == "" {
				t.Errorf("copy lost the unparsed entity: %q %v", sys, ok)
			}
		})
	}
}

// TestValidateCopyIsNotVacuous checks that the differential above compares
// trees in which every recorded property actually occurs, so that dropping
// any of them from the copy would show.
func TestValidateCopyIsNotVacuous(t *testing.T) {
	s := loadAssertionSchema(t, copySchema)
	in := parseCopyDoc(t, copyDoc("100", `<n xsi:nil="true"/>`, "1"))
	kidsBefore := in.Root.ChildElements()[0].NumChildren()
	got, err := s.ValidateCopy(in.Root, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		ty := xdm.TypingOf(n)
		seen["annotation"] = seen["annotation"] || ty.TypeAnnotation != ""
		seen["union member"] = seen["union member"] || ty.UnionMember != ""
		seen["primitive"] = seen["primitive"] || ty.DerivedPrimitive != ""
		seen["list item"] = seen["list item"] || ty.ListItem != ""
		seen["id"] = seen["id"] || ty.IsID
		seen["idrefs"] = seen["idrefs"] || ty.IsIDREFS
		seen["nilled"] = seen["nilled"] || ty.IsNilled
		seen["no typed value"] = seen["no typed value"] || ty.NoTypedValue
		seen["mixed"] = seen["mixed"] || ty.MixedContent
		seen["type env"] = seen["type env"] || n.TypeEnv() != nil
		seen["default attribute"] = seen["default attribute"] ||
			(n.Kind() == xdm.KindAttribute && n.Value() == "dflt")
		for a := range n.Attrs() {
			walk(a)
		}
		for c := range n.Children() {
			walk(c)
		}
	}
	walk(got)
	for _, p := range []string{"annotation", "union member", "primitive", "list item", "id",
		"idrefs", "nilled", "no typed value", "mixed", "type env", "default attribute"} {
		if !seen[p] {
			t.Errorf("no node of the validated copy has %s", p)
		}
	}
	if got.ChildElements()[0].NumChildren() >= kidsBefore {
		t.Errorf("ignorable whitespace was not stripped from the copy")
	}
	if got.DocumentURI() != "http://ex/doc.xml" || got.BaseURI() != "http://ex/doc.xml" {
		t.Errorf("copy URIs: document %q base %q", got.DocumentURI(), got.BaseURI())
	}
}

// TestValidateCopyConcurrent validates one tree from several goroutines. In
// place, each run strips whitespace from and annotates the shared tree, which
// -race reports; through ValidateCopy each run writes only its own copy.
func TestValidateCopyConcurrent(t *testing.T) {
	s := loadAssertionSchema(t, copySchema)
	in := parseCopyDoc(t, copyDoc("100", `<n xsi:nil="true"/>`, "1"))
	r := in.Root.ChildElements()[0]
	kids := r.NumChildren()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.ValidateCopy(in.Root, ValidateOptions{})
			if err != nil || got == in.Root || got.ChildElements()[0].TypeAnnotation() == "" {
				t.Errorf("ValidateCopy: %v, %p", err, got)
			}
		}()
	}
	wg.Wait()
	if r.NumChildren() != kids || r.TypeAnnotation() != "" {
		t.Errorf("input changed: %d children (was %d), annotation %q", r.NumChildren(), kids, r.TypeAnnotation())
	}
}
