package xslt

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The builder's typed entry point (audit finding 24) is only worth having if
// the sites that HOLD resolved typing actually use it. Two of them put an
// already-assessed attribute node into a result tree without going through an
// element copy, so nothing else in the suite pins what they forward:
//
//   - appendItemChecked in onempty.go, which re-offers an attribute that a
//     detached builder left in its item list, and
//   - the attribute branch of xsl:copy-of in instructions.go.
//
// Both previously passed an annotation NAME, so a union-typed or list-typed
// attribute arrived in the output with its member and its resolved primitive
// gone and had to be re-guessed from the process-global registries.
//
// The first is driven directly below. The second sits inside copyOfInstr.
// Execute, which needs a live runtime, and is covered by the schema-driven
// transforms in typingcopy_test.go instead.

// typedProbeNS is a namespace nothing registers a type in, so an annotation
// built from it can only be understood through the metadata carried on the
// node itself.
const typedProbeNS = "urn:go-xml:finding24:output"

// assessedAttr is an attribute node carrying every PSVI property, standing in
// for one a validator has just produced.
func assessedAttr() *xdm.Node {
	a := &xdm.Node{Kind: xdm.KindAttribute,
		Name: xdm.QName{Local: "a"}, Value: "10 20"}
	a.ApplyTyping(xdm.Typing{
		TypeAnnotation:   xdm.AnnotationName(typedProbeNS, "L"),
		UnionMember:      xdm.AnnotationName(typedProbeNS, "M"),
		DerivedPrimitive: "anySimpleType",
		ListItem:         "decimal",
		IsID:             true,
		IsIDREFS:         true,
	})
	return a
}

// TestAppendItemCheckedForwardsResolvedTyping pins onempty.go's re-offer path.
func TestAppendItemCheckedForwardsResolvedTyping(t *testing.T) {
	src := assessedAttr()
	out := newOutputBuilder()
	el := out.StartElement(xdm.QName{Local: "e"})
	if err := appendItemChecked(el, src); err != nil {
		t.Fatalf("appendItemChecked failed: %v", err)
	}
	got := onlyAttr(t, out)
	if want := xdm.TypingOf(src); got != want {
		t.Fatalf("appendItemChecked forwarded %+v, want %+v", got, want)
	}
}

// onlyAttr returns the typing of the single attribute the builder's element
// ended up with.
func onlyAttr(t *testing.T, out *outputBuilder) xdm.Typing {
	t.Helper()
	seq := out.Sequence()
	if len(seq) != 1 {
		t.Fatalf("builder produced %d items, want 1", len(seq))
	}
	n, ok := seq[0].(*xdm.Node)
	if !ok || n.Kind != xdm.KindElement {
		t.Fatalf("builder produced %T, want an element", seq[0])
	}
	if len(n.Attrs) != 1 {
		t.Fatalf("element carries %d attributes, want 1", len(n.Attrs))
	}
	return xdm.TypingOf(n.Attrs[0])
}

// TestCopyOfBareAttributeForwardsResolvedTyping pins the OTHER site: the
// attribute branch of copyOfInstr.Execute, which fires when xsl:copy-of
// selects an attribute on its own -- select="@a" rather than an element -- and
// has to re-offer it to the builder instead of appending it as a child.
//
// It is driven as a transform because the branch sits behind a live runtime.
// The probe harness from typingcopy_test.go does the work that makes the
// result meaningful: the source is validated against a schema deriving
// {urn:copyprobe}L from xs:decimal, the transform runs, and only THEN is a
// second schema loaded that redefines the same QName over xs:string. The
// registries now disagree with the node. An attribute that reached the output
// carrying its own resolved typing still splits into decimals; one that
// arrived with nothing but the name asks the registries and gets strings.
//
// The wrapper element carries xsl:validation="preserve" because it has to: a
// literal result element defaults to validation="strip", and stripping
// recurses into the element's ATTRIBUTES. Without it the attribute this test
// is about is stripped after the builder call, by design and correctly, and
// the test could see nothing either way.
//
// Reverting the call site to AddAttributeTyped makes this fail, which is what
// separates it from the element-copy tests in typingcopy_test.go -- those pass
// either way, because an element copy never goes through the builder's
// attribute entry point at all.
func TestCopyOfBareAttributeForwardsResolvedTyping(t *testing.T) {
	root := runTypingProbe(t, `<xsl:stylesheet version="3.0"
	    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	    xmlns:p="urn:copyprobe">
	  <xsl:import-schema namespace="urn:copyprobe" schema-location="p.xsd"/>
	  <xsl:template match="/">
	    <out xsl:validation="preserve"><xsl:copy-of
	        select="/*/p:list/@a" validation="preserve"/></out>
	  </xsl:template>
	</xsl:stylesheet>`)
	// Only the ATTRIBUTE is asserted on: <out> is a literal result element
	// with no type of its own, so checkListDecimal's element half does not
	// apply here. The attribute is the node that travelled through the
	// builder call under test.
	out := findProbe(t, root, "out")
	a := out.Attr(typingProbeNS, "a")
	if a == nil {
		a = out.Attr("", "a")
	}
	if a == nil {
		t.Fatal("the copied attribute did not reach <out>")
	}
	checkListNodeDecimal(t, a, "attribute")
}
