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
	for ac, bc := a.FirstChild(), b.FirstChild(); ac != nil; ac, bc = ac.NextSibling(), bc.NextSibling() {
		sameTree(t, label, ac, bc)
	}
}

// TestValidateCopyMatchesVerdict checks ValidateCopy against Validate: the
// same error text, line and column included; an input that comes out exactly
// as a fresh parse of it; and a copy that is a new tree carrying the DTD
// context, or, for an element below the document element, a parentless copy
// of that element alone.
func TestValidateCopyMatchesVerdict(t *testing.T) {
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
			verdict := parseCopyDoc(t, c.doc)
			errIn := s.Validate(pickCopyTarget(verdict.Root, c.target), opts)
			if (errIn == nil) != c.valid {
				t.Fatalf("verdict: %v", errIn)
			}
			sameTree(t, "Validate's input vs fresh parse", parseCopyDoc(t, c.doc).Root, verdict.Root)

			input := parseCopyDoc(t, c.doc)
			target := pickCopyTarget(input.Root, c.target)
			got, errCp := s.ValidateCopy(target, opts)
			var ves *ValidationErrors
			if !c.valid && (!errors.As(errCp, &ves) || ves.Errors[0].Line == 0) {
				t.Fatalf("an invalid document's errors carry no position: %v", errCp)
			}
			if errText(errIn) != errText(errCp) {
				t.Fatalf("errors differ\n  verdict: %s\n  copy:    %s", errText(errIn), errText(errCp))
			}
			if got == target || got.Name() != target.Name() {
				t.Fatalf("ValidateCopy returned %v, want a copy of %v", got, target)
			}
			sameTree(t, "input vs fresh parse", parseCopyDoc(t, c.doc).Root, input.Root)
			if c.target != "" {
				if got.Parent() != nil || got.TypeAnnotation() != "QName" ||
					got.InScopeNamespaces()["p"] != "" {
					t.Fatalf("element copy: parent %v, annotation %q, scope %v",
						got.Parent(), got.TypeAnnotation(), got.InScopeNamespaces())
				}
				return
			}
			ct := got.Tree()
			if ct == nil || ct == input {
				t.Fatalf("copy tree = %p, want a new tree", ct)
			}
			if ct.DocType != input.DocType || ct.XMLVersion != input.XMLVersion {
				t.Errorf("tree: DocType/XMLVersion %q/%q, want %q/%q",
					ct.DocType, ct.XMLVersion, input.DocType, input.XMLVersion)
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

// TestValidateCopyConcurrent validates one tree from several goroutines. Each
// run writes only its own copy, so -race has nothing to report and the input
// stays as it was.
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

// TestTypedCopySeesItsRecordedEdits checks that the checks a typed copy runs
// see the defaulted attributes and the stripped whitespace, which the copy
// records rather than writes: an assertion counts the default and finds no
// text, and a unique key collides on two defaulted values.
func TestTypedCopySeesItsRecordedEdits(t *testing.T) {
	s := loadAssertionSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="r"><xs:complexType><xs:sequence>
    <xs:element name="i" maxOccurs="unbounded"><xs:complexType>
      <xs:attribute name="k" type="xs:string" default="d"/></xs:complexType></xs:element>
    </xs:sequence>
    <xs:assert test="count(i/@k) eq count(i) and empty(text())"/></xs:complexType>
    <xs:unique name="u"><xs:selector xpath="i"/><xs:field xpath="@k"/></xs:unique>
  </xs:element>
</xs:schema>`)
	parse := func(src string) *xdm.Node {
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return tree.Root
	}
	got, err := s.ValidateCopy(parse("<r>\n  <i/>\n  <i k=\"x\"/>\n</r>"), ValidateOptions{})
	if err != nil {
		t.Fatalf("the assertion did not see the typed copy's edits: %v", err)
	}
	r := got.FirstChild()
	if r.NumChildren() != 2 || r.ChildAt(0).AttrValue("k") != "d" {
		t.Errorf("typed copy: %d children, first @k %q", r.NumChildren(), r.ChildAt(0).AttrValue("k"))
	}
	if _, err := s.ValidateCopy(parse(`<r><i/><i/></r>`), ValidateOptions{}); err == nil ||
		!strings.Contains(err.Error(), "cvc-identity-constraint") {
		t.Errorf("two defaulted key values did not collide: %v", err)
	}
}

// TestValidateWritesNothing pins the v2 contract: Validate only
// checks. The document that ValidateCopy types, defaults
// and strips (TestValidateCopyIsNotVacuous) comes back from Validate with no
// annotation, no defaulted attribute and its whitespace in place.
func TestValidateWritesNothing(t *testing.T) {
	s := loadAssertionSchema(t, copySchema)
	in := parseCopyDoc(t, copyDoc("100", `<n xsi:nil="true"/>`, "1"))
	kidsBefore := in.Root.ChildElements()[0].NumChildren()
	// parseCopyDoc stamps <x> as if an earlier assessment had typed it.
	before := map[*xdm.Node]xdm.Typing{}
	var snap func(n *xdm.Node)
	snap = func(n *xdm.Node) {
		before[n] = xdm.TypingOf(n)
		for a := range n.Attrs() {
			snap(a)
		}
		for c := range n.Children() {
			snap(c)
		}
	}
	snap(in.Root)
	if err := s.Validate(in.Root, ValidateOptions{}); err != nil {
		t.Fatal(err)
	}
	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		if got := xdm.TypingOf(n); got != before[n] {
			t.Errorf("Validate wrote typing to %s: %+v", n.Name().Local, got)
		}
		for a := range n.Attrs() {
			if a.Value() == "dflt" {
				t.Errorf("Validate added the defaulted attribute %s", a.Name().Local)
			}
			walk(a)
		}
		for c := range n.Children() {
			walk(c)
		}
	}
	walk(in.Root)
	if got := in.Root.ChildElements()[0].NumChildren(); got != kidsBefore {
		t.Errorf("Validate stripped whitespace: %d children, had %d", got, kidsBefore)
	}
}

// TestValidateConcurrentCheckOnly validates one tree from several goroutines
// checking only. Under -race it fails if a check-only run writes to the
// tree, as the union member, nilled and lax-wildcard restore writes did.
func TestValidateConcurrentCheckOnly(t *testing.T) {
	lax := loadAssertionSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="r"><xs:complexType><xs:sequence>
    <xs:any processContents="lax"/></xs:sequence></xs:complexType></xs:element>
</xs:schema>`)
	laxDoc, err := xdm.ParseString(`<r><x>k</x></r>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		s  *Schema
		in *xdm.Node
	}{
		{loadAssertionSchema(t, copySchema), parseCopyDoc(t, copyDoc("100", `<n xsi:nil="true"/>`, "1")).Root},
		{lax, laxDoc.Root},
	} {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := c.s.Validate(c.in, ValidateOptions{}); err != nil {
					t.Errorf("Validate: %v", err)
				}
			}()
		}
		wg.Wait()
	}
}

// TestCheckOnlyAssertionSeesUnwrittenTyping: an assertion evaluates over a
// copy of its element, and a check-only run used to reach it through the
// union member and dm:nilled it wrote onto the caller's tree. Those are now
// held aside and laid onto the copy, so the verdict is the annotating run's.
func TestCheckOnlyAssertionSeesUnwrittenTyping(t *testing.T) {
	s := loadAssertionSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:simpleType name="u"><xs:union memberTypes="xs:integer xs:NCName"/></xs:simpleType>
  <xs:element name="r"><xs:complexType><xs:sequence>
    <xs:element name="n" type="xs:int" nillable="true"/></xs:sequence>
    <xs:attribute name="a" type="u"/>
    <xs:assert test="data(@a) instance of xs:integer and nilled(n)"/>
  </xs:complexType></xs:element>
</xs:schema>`)
	for _, annotate := range []bool{false, true} {
		tree, err := xdm.ParseString(`<r a="7" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><n xsi:nil="true"/></r>`,
			xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if annotate {
			_, err = s.ValidateCopy(tree.Root, ValidateOptions{})
		} else {
			err = s.Validate(tree.Root, ValidateOptions{})
		}
		if err != nil {
			t.Errorf("typed copy=%v: %v", annotate, err)
		}
	}
}

// TestTypedCopyDefaultsInOrder pins where the bulk clone inserts defaulted
// attributes: on nested elements, next to dropped whitespace, and on the
// last record of the tree, so every insertion point is met in order.
func TestTypedCopyDefaultsInOrder(t *testing.T) {
	s := loadAssertionSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="e"><xs:sequence>
    <xs:element name="i" type="e" minOccurs="0" maxOccurs="unbounded"/></xs:sequence>
    <xs:attribute name="a" type="xs:string"/>
    <xs:attribute name="k" type="xs:string" default="d"/></xs:complexType>
  <xs:element name="i" type="e"/>
</xs:schema>`)
	tree, err := xdm.ParseString("<i a=\"0\">\n <i>\n  <i a=\"2\"/> <i k=\"x\"/>\n </i>\n <i/></i>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ValidateCopy(tree.Root, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		b.WriteString("<" + n.Name().Local)
		for a := range n.Attrs() {
			fmt.Fprintf(&b, " %s=%s:%s", a.Name().Local, a.Value(), a.TypeAnnotation())
		}
		b.WriteString(">")
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if c.Kind() != xdm.KindElement {
				fmt.Fprintf(&b, "%q", c.Value())
				continue
			}
			walk(c)
			if c.Parent() != n {
				t.Errorf("%s: wrong parent", c.Name().Local)
			}
		}
		b.WriteString("</>")
	}
	walk(got.FirstChild())
	const want = `<i a=0:string k=d:string><i k=d:string><i a=2:string k=d:string></><i k=x:string></></><i k=d:string></></>`
	if b.String() != want {
		t.Errorf("typed copy\n  got  %s\n  want %s", b.String(), want)
	}
}

// TestTypedCopyAnnotationMemo pins the per-type annotation memo: a second
// typed copy, written from the memo, carries every property the first,
// written while the memo was filled, does.
func TestTypedCopyAnnotationMemo(t *testing.T) {
	s := loadAssertionSchema(t, copySchema)
	doc := copyDoc("100", `<n xsi:nil="true"/>`, "1")
	first, err := s.ValidateCopy(parseCopyDoc(t, doc).Root, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ValidateCopy(parseCopyDoc(t, doc).Root, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sameTree(t, "first vs memoised copy", first, second)
	nums := pickCopyTarget(second, "nums")
	if got := xdm.TypingOf(nums); got.TypeAnnotation != "{urn:t}l" || got.ListItem != "integer" ||
		nums.TypeEnv() != s.TypeEnv() {
		t.Errorf("nums typing %+v, env %p", got, nums.TypeEnv())
	}
	if r := second.FirstChild(); !r.NoTypedValue() || r.TypeAnnotation() != "anyType" {
		t.Errorf("r: no typed value %v, annotation %q", r.NoTypedValue(), r.TypeAnnotation())
	}
}
