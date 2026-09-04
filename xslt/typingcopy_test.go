package xslt

import (
	"context"
	"fmt"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// The property these tests pin: a node COPIED by the engine must answer every
// PSVI question exactly as the node it was copied from.
//
// The four fields that used to be dropped -- DerivedPrimitive and ListItem in
// particular -- are the per-node record of what the type annotation MEANS
// according to the schema that validated the node. A copy that keeps only the
// annotation NAME has to ask the process-global registries what that name
// means, and those answer for whichever schema loaded most recently. So the
// loss is invisible until a second schema defines the same QName differently,
// at which point the copy silently retypes and the original does not.
//
// That is exactly the shape used here: validate against a schema deriving
// {urn:copyprobe}T from xs:decimal, copy the node through the path under test,
// then load a second schema deriving the same name from xs:string. The
// ORIGINAL is already known to be immune (xsd.TestDerivedPrimitiveIsolated
// FromLaterSchema pins that); these ask whether the COPY is.

const typingProbeNS = "urn:copyprobe"

// typingProbeSchema derives {urn:copyprobe}T from the named built-in and
// {urn:copyprobe}L as a list of it, and declares a document shape carrying
// both -- an element of the atomic type and one of the list type, plus an
// attribute of each, since attributes travel by a different line of code at
// every copy site.
func typingProbeSchema(base string) string {
	return fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    xmlns:p="%s" targetNamespace="%s" elementFormDefault="qualified">
  <xs:simpleType name="T"><xs:restriction base="xs:%s"/></xs:simpleType>
  <xs:simpleType name="L"><xs:list itemType="xs:%s"/></xs:simpleType>
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="atom">
        <xs:complexType><xs:simpleContent><xs:extension base="p:T">
          <xs:attribute name="a" type="p:T"/>
        </xs:extension></xs:simpleContent></xs:complexType>
      </xs:element>
      <xs:element name="list">
        <xs:complexType><xs:simpleContent><xs:extension base="p:L">
          <xs:attribute name="a" type="p:L"/>
        </xs:extension></xs:simpleContent></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
  </xs:element>
</xs:schema>`, typingProbeNS, typingProbeNS, base, base)
}

const typingProbeDoc = `<p:root xmlns:p="urn:copyprobe">` +
	`<p:atom a="10">10</p:atom>` +
	`<p:list a="10 20">10 20</p:list>` +
	`</p:root>`

// runTypingProbe validates typingProbeDoc against the decimal schema, runs
// src over it, and returns the result's root element.
//
// The second schema -- the one redefining the same QNames over xs:string -- is
// loaded AFTER the transform, so that everything the engine did was done while
// the registries still agreed with the node. The assertions then run with the
// registries disagreeing, which is what separates a node that recorded its own
// answer from one that merely kept a name to look up.
func runTypingProbe(t *testing.T, src string) *xdm.Node {
	t.Helper()
	res := transformTypingProbe(t, src, typingProbeSchema("decimal"))
	// Redefine both names over xs:string. Nothing about the already-produced
	// result has changed, so nothing about its typed values may change either.
	loadTypingProbeSchema(t, typingProbeSchema("string"))
	return res
}

func loadTypingProbeSchema(t *testing.T, src string) *xsd.Schema {
	t.Helper()
	s, err := xsd.LoadFile("p.xsd", xsd.Options{
		Resolver: &xsd.MapResolver{ByLocation: map[string]string{"p.xsd": src}},
	})
	if err != nil {
		t.Fatalf("loading the schema: %v", err)
	}
	return s
}

func transformTypingProbe(t *testing.T, src, schemaSrc string) *xdm.Node {
	t.Helper()
	schema := loadTypingProbeSchema(t, schemaSrc)
	tree, err := xdm.ParseString(typingProbeDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the source: %v", err)
	}
	if err := schema.Validate(tree.Root, xsd.ValidateOptions{Annotate: true}); err != nil {
		t.Fatalf("validating the source: %v", err)
	}
	sheet, err := Compile(mustParse(t, src), CompileOptions{
		SchemaResolver: &xsd.MapResolver{
			ByLocation: map[string]string{"p.xsd": schemaSrc}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out, err := sheet.Transform(context.Background(), tree.Root, TransformOptions{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	root := out.Tree()
	if root == nil {
		t.Fatal("Transform produced no tree")
	}
	return root
}

// findProbe returns the first descendant element with the given local name.
func findProbe(t *testing.T, root *xdm.Node, local string) *xdm.Node {
	t.Helper()
	var walk func(*xdm.Node) *xdm.Node
	walk = func(n *xdm.Node) *xdm.Node {
		if n.Kind == xdm.KindElement && n.Name.Local == local {
			return n
		}
		for _, c := range n.Children {
			if f := walk(c); f != nil {
				return f
			}
		}
		return nil
	}
	f := walk(root)
	if f == nil {
		t.Fatalf("no <%s> in the result", local)
	}
	return f
}

// checkAtomDecimal asserts that a copied atom element's ATTRIBUTE still
// atomises as xs:decimal, which is what DerivedPrimitive records.
//
// The attribute rather than the element, and deliberately so. The validator
// records DerivedPrimitive on an attribute (its declared type is the named
// simple type p:T) but leaves it empty on the ELEMENT, whose annotation is the
// anonymous complex type's simple-content base -- so the element has no
// per-node answer to preserve and correctly falls back to the registry. Only a
// node that HAS the field can show whether a copy kept it, so the assertions
// here are on the nodes that have it. That is a property of what the validator
// records, not of the copy under test.
//
// The lexical form is identical either way -- "10" is "10" -- so the TYPE is
// the whole of the difference, and it is what every comparison downstream
// reads. Under string ordering "10" lt "9" is true where the numeric answer is
// false, which is how this was originally found.
func checkAtomDecimal(t *testing.T, atom *xdm.Node) {
	t.Helper()
	a := atom.Attr(typingProbeNS, "a")
	if a == nil {
		a = atom.Attr("", "a")
	}
	if a == nil {
		t.Fatal("the copied element has no @a")
	}
	if a.DerivedPrimitive != "decimal" {
		t.Errorf("copied attribute lost DerivedPrimitive: %q, want %q",
			a.DerivedPrimitive, "decimal")
	}
	if got := a.Atomize(); got.Type != xdm.TypeDecimal {
		t.Errorf("copied attribute atomised as %v, want %v -- the copy lost "+
			"DerivedPrimitive and fell back to the registry, which a later "+
			"schema had redefined over xs:string", got.Type, xdm.TypeDecimal)
	}
}

// checkListDecimal asserts that a copied list-typed element and its attribute
// still split into decimals, which is what ListItem records.
//
// Arity survives the token split whatever the item type says, so counting the
// items cannot see this bug: two items either way. The ITEM TYPE is what
// corrupts, and that is what is asserted.
func checkListDecimal(t *testing.T, list *xdm.Node) {
	t.Helper()
	checkListNodeDecimal(t, list, "element")
	a := list.Attr(typingProbeNS, "a")
	if a == nil {
		a = list.Attr("", "a")
	}
	if a == nil {
		t.Fatal("the copied list element has no @a")
	}
	checkListNodeDecimal(t, a, "attribute")
}

func checkListNodeDecimal(t *testing.T, n *xdm.Node, what string) {
	t.Helper()
	if n.ListItem != "decimal" {
		t.Errorf("copied list %s lost ListItem: %q, want %q", what,
			n.ListItem, "decimal")
	}
	seq, ok := n.AtomizeList()
	if !ok {
		t.Fatalf("the copied list %s did not atomise as a list at all", what)
	}
	if len(seq) != 2 {
		t.Fatalf("copied list %s gave %d items, want 2", what, len(seq))
	}
	for i, it := range seq {
		at, ok := it.(*xdm.Atomic)
		if !ok {
			t.Fatalf("list item %d is not atomic", i)
		}
		if at.Type != xdm.TypeDecimal {
			t.Errorf("copied list %s item %d is %v, want %v -- the copy lost "+
				"ListItem and asked the registry, which a later schema had "+
				"redefined over xs:string", what, i, at.Type, xdm.TypeDecimal)
		}
	}
}

// TestCopyOfPreservesResolvedTyping covers xsl:copy-of, which reaches
// xdmbuild.DeepCopy.
func TestCopyOfPreservesResolvedTyping(t *testing.T) {
	root := runTypingProbe(t, `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	  <xsl:import-schema namespace="urn:copyprobe" schema-location="p.xsd"/>
	  <xsl:template match="/">
	    <xsl:copy-of select="/*/*" validation="preserve"/>
	  </xsl:template>
	</xsl:stylesheet>`)
	checkAtomDecimal(t, findProbe(t, root, "atom"))
	checkListDecimal(t, findProbe(t, root, "list"))
}

// TestStripSpaceCopyPreservesResolvedTyping covers the strip-space pass in
// xslt/transform.go, which copies the whole source tree before the transform
// runs. It is reached only when the stylesheet declares xsl:strip-space at
// all, which is why the loss went unnoticed there.
func TestStripSpaceCopyPreservesResolvedTyping(t *testing.T) {
	root := runTypingProbe(t, `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:p="urn:copyprobe">
	  <xsl:import-schema namespace="urn:copyprobe" schema-location="p.xsd"/>
	  <xsl:strip-space elements="p:root"/>
	  <xsl:template match="/">
	    <xsl:copy-of select="/*/*" validation="preserve"/>
	  </xsl:template>
	</xsl:stylesheet>`)
	checkAtomDecimal(t, findProbe(t, root, "atom"))
	checkListDecimal(t, findProbe(t, root, "list"))
}

// TestSnapshotPreservesResolvedTyping covers fn:snapshot, whose ancestor-spine
// copy in xslt/copyfuncs.go builds each ancestor's ATTRIBUTES by hand. The
// spine elements themselves are re-annotated xs:anyType by §18.4, so the
// attribute is the observable part.
func TestSnapshotPreservesResolvedTyping(t *testing.T) {
	root := runTypingProbe(t, `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:p="urn:copyprobe">
	  <xsl:import-schema namespace="urn:copyprobe" schema-location="p.xsd"/>
	  <xsl:template match="/">
	    <xsl:copy-of select="snapshot(/*/p:atom/text())/../.."
	        validation="preserve"/>
	  </xsl:template>
	</xsl:stylesheet>`)
	checkAtomDecimal(t, findProbe(t, root, "atom"))
}

// TestCopyItemAttributePreservesResolvedTyping covers copyItem's parentless
// attribute branch in xslt/copyfuncs.go, which is the one path that copies an
// attribute node on its own rather than as part of an element.
//
// It asserts on the copied ITEM rather than on a transform's output, because
// putting a parentless attribute into a result tree goes through
// xdmbuild.Builder.AddAttributeTyped, whose signature carries a type
// annotation STRING and nothing else. Everything but the name is dropped
// there, on every path, for every field including UnionMember -- a separate
// and pre-existing narrowing of the builder API, not of this copy site. The
// copy itself is what this test is about.
func TestCopyItemAttributePreservesResolvedTyping(t *testing.T) {
	src := &xdm.Node{Kind: xdm.KindAttribute,
		Name: xdm.QName{Local: "a"}, Value: "10 20"}
	src.SetTypeAnnotationResolved(
		xdm.AnnotationName(typingProbeNS, "L"), "anySimpleType", "decimal")
	src.UnionMember = xdm.AnnotationName(typingProbeNS, "M")
	src.IsIDREFS = true

	c, ok := copyItem(src).(*xdm.Node)
	if !ok {
		t.Fatal("copyItem did not return a node")
	}
	if c == src {
		t.Fatal("copyItem returned the source attribute rather than a copy")
	}
	if c.DerivedPrimitive != src.DerivedPrimitive || c.ListItem != src.ListItem ||
		c.UnionMember != src.UnionMember || c.TypeAnnotation != src.TypeAnnotation ||
		!c.IsIDREFS {
		t.Fatalf("copyItem dropped typing: annotation=%q UnionMember=%q "+
			"DerivedPrimitive=%q ListItem=%q IsIDREFS=%v",
			c.TypeAnnotation, c.UnionMember, c.DerivedPrimitive, c.ListItem,
			c.IsIDREFS)
	}

	// Observable: the copy still splits into decimals once a later schema has
	// redefined the same QName over xs:string, which the registry fallback
	// would not.
	loadTypingProbeSchema(t, typingProbeSchema("string"))
	checkListNodeDecimal(t, c, "attribute")
}

// TestStripClearsResolvedTyping is the other direction, and it is the reason
// the stripping variant is a named operation rather than "clear the
// annotation".
//
// input-type-annotations="strip" emptied TypeAnnotation by assignment and left
// DerivedPrimitive and ListItem in place. Atomisation gates on the annotation
// being non-empty, so nothing read them -- until anything re-annotated the
// node, at which point stale fields described a type it no longer claimed.
// The fields must go with the name.
func TestStripClearsResolvedTyping(t *testing.T) {
	res := transformTypingProbe(t, `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:p="urn:copyprobe" input-type-annotations="strip">
	  <xsl:import-schema namespace="urn:copyprobe" schema-location="p.xsd"/>
	  <xsl:template match="/">
	    <xsl:copy-of select="/*/*" validation="preserve"/>
	  </xsl:template>
	</xsl:stylesheet>`, typingProbeSchema("decimal"))
	for _, local := range []string{"atom", "list"} {
		n := findProbe(t, res, local)
		if n.TypeAnnotation != "" {
			t.Errorf("<%s> kept the annotation %q across a strip", local,
				n.TypeAnnotation)
		}
		if n.DerivedPrimitive != "" || n.ListItem != "" {
			t.Errorf("<%s> kept resolved typing across a strip: "+
				"DerivedPrimitive=%q ListItem=%q -- these describe an "+
				"annotation the node no longer carries", local,
				n.DerivedPrimitive, n.ListItem)
		}
		for _, a := range n.Attrs {
			if a.TypeAnnotation != "" || a.DerivedPrimitive != "" ||
				a.ListItem != "" || a.UnionMember != "" {
				t.Errorf("<%s>/@%s kept typing across a strip: "+
					"annotation=%q DerivedPrimitive=%q ListItem=%q "+
					"UnionMember=%q", local, a.Name.Local, a.TypeAnnotation,
					a.DerivedPrimitive, a.ListItem, a.UnionMember)
			}
		}
	}
}

// TestValidationStripClearsResolvedTyping is the same property for
// validation="strip", which strips a constructed tree in place rather than
// building a stripped copy. It went through a different function, and that
// function had the same defect.
func TestValidationStripClearsResolvedTyping(t *testing.T) {
	res := transformTypingProbe(t, `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:p="urn:copyprobe">
	  <xsl:import-schema namespace="urn:copyprobe" schema-location="p.xsd"/>
	  <xsl:template match="/">
	    <xsl:copy-of select="/*/*" validation="strip"/>
	  </xsl:template>
	</xsl:stylesheet>`, typingProbeSchema("decimal"))
	for _, local := range []string{"atom", "list"} {
		n := findProbe(t, res, local)
		if n.TypeAnnotation != "" || n.DerivedPrimitive != "" || n.ListItem != "" {
			t.Errorf(`<%s> survived validation="strip" with `+
				"annotation=%q DerivedPrimitive=%q ListItem=%q", local,
				n.TypeAnnotation, n.DerivedPrimitive, n.ListItem)
		}
	}
}

// TestStripKeepsIsIDAndClearsIsNilled pins the half of the stripping rule that
// is NOT "clear everything", and is the reason CopyTypingStrippedFrom is
// written out field by field.
//
// XSLT 2.0 §3.5 keeps is-id and is-idrefs across a strip in as many words --
// fn:id and fn:idref are defined over those properties rather than over the
// annotation, so a stripped document would otherwise go blind to its own IDs
// -- while the same section makes dm:nilled false for every element.
func TestStripKeepsIsIDAndClearsIsNilled(t *testing.T) {
	src := &xdm.Node{Kind: xdm.KindElement,
		Name:     xdm.QName{Local: "e"},
		IsID:     true,
		IsIDREFS: true,
		IsNilled: true,
	}
	src.SetTypeAnnotationResolved("{urn:probe}L", "decimal", "decimal")
	src.UnionMember = "{urn:probe}M"

	dst := &xdm.Node{Kind: xdm.KindElement, Name: src.Name}
	dst.CopyTypingStrippedFrom(src)

	if !dst.IsID || !dst.IsIDREFS {
		t.Errorf("stripping cleared is-id/is-idrefs (%v/%v); §3.5 says it "+
			"does not change them", dst.IsID, dst.IsIDREFS)
	}
	if dst.IsNilled {
		t.Error("stripping kept dm:nilled; §3.5 makes it false for every " +
			"element in a stripped tree")
	}
	if dst.TypeAnnotation != "" || dst.UnionMember != "" ||
		dst.DerivedPrimitive != "" || dst.ListItem != "" {
		t.Errorf("stripping kept part of the annotation: annotation=%q "+
			"UnionMember=%q DerivedPrimitive=%q ListItem=%q",
			dst.TypeAnnotation, dst.UnionMember, dst.DerivedPrimitive,
			dst.ListItem)
	}

	// And the preserving variant carries all seven, which is the contrast
	// that makes two named functions the right shape.
	keep := &xdm.Node{Kind: xdm.KindElement, Name: src.Name}
	keep.CopyTypingFrom(src)
	if keep.TypeAnnotation != src.TypeAnnotation ||
		keep.UnionMember != src.UnionMember ||
		keep.DerivedPrimitive != src.DerivedPrimitive ||
		keep.ListItem != src.ListItem || !keep.IsID || !keep.IsIDREFS ||
		!keep.IsNilled {
		t.Errorf("CopyTypingFrom did not carry every property: %+v", keep)
	}
}
