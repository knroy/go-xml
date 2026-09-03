package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// unionMemberSchema declares the shape that carries a per-value union member:
// an element whose type is a COMPLEX type with simple content, extending a
// union. Validation records the union on the element's annotation and the
// member that actually accepted the value alongside it, because the union
// alone cannot say which of "29 MAY 1917" and "about 1920" is a StandardDate.
//
// This is gedSchema.xsd's shape, reduced to the part under test. It is the one
// in the W3C suite's validation-0201, whose stylesheet dispatches on
// "data(.) instance of StandardDate".
const unionMemberSchema = `<?xml version="1.0"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" elementFormDefault="qualified">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="Date" type="DateType" maxOccurs="unbounded"/>
    </xs:sequence></xs:complexType>
  </xs:element>
  <xs:complexType name="DateType">
    <xs:simpleContent><xs:extension base="GeneralDate"/></xs:simpleContent>
  </xs:complexType>
  <xs:simpleType name="GeneralDate">
    <xs:union memberTypes="StandardDate xs:string"/>
  </xs:simpleType>
  <xs:simpleType name="StandardDate">
    <xs:restriction base="xs:string">
      <xs:pattern value="[0-9][0-9] (JAN|FEB|MAR|APR|MAY|JUN|JUL|AUG|SEP|OCT|NOV|DEC) [0-9]{4}"/>
    </xs:restriction>
  </xs:simpleType>
</xs:schema>`

const unionMemberDoc = `<root>
  <Date>29 MAY 1917</Date>
  <Date>about 1920</Date>
</root>`

// runUnionMember validates unionMemberDoc, compiles src against the schema and
// transforms, returning the serialised result.
func runUnionMember(t *testing.T, src string) string {
	t.Helper()
	schema, err := xsd.LoadFile("s.xsd", xsd.Options{
		Resolver: &xsd.MapResolver{ByLocation: map[string]string{"s.xsd": unionMemberSchema}},
	})
	if err != nil {
		t.Fatalf("loading the schema: %v", err)
	}
	tree, err := xdm.ParseString(unionMemberDoc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the source: %v", err)
	}
	if err := schema.Validate(tree.Root, xsd.ValidateOptions{Annotate: true}); err != nil {
		t.Fatalf("validating the source: %v", err)
	}
	sheet, err := Compile(mustParse(t, src), CompileOptions{
		SchemaResolver: &xsd.MapResolver{ByLocation: map[string]string{"s.xsd": unionMemberSchema}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := sheet.Transform(context.Background(), tree.Root, TransformOptions{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	return res.String()
}

// TestUnionMemberSurvivesStripSpace is the regression this file exists for.
//
// xsl:strip-space copies the source tree, and the copy carried the element's
// type annotation but not the union member recorded beside it. A union's own
// derivation chain runs to xs:anySimpleType and stops, so atomising the copy
// found nothing to build a typed value from and produced xs:untypedAtomic --
// and "data(.) instance of StandardDate" was then false for a value the
// schema had validated as exactly that.
//
// The failure was silent and selective: the SAME pattern answered true on any
// path that had not been through a copy, so a stylesheet could sort by a key
// that saw the type and then dispatch on a pattern that did not. That is what
// made the W3C suite's validation-0201 emit "29 MAY 1917" where its expected
// output has "29 May 1917" -- the specific template never matched, and the
// unconditional one copied the source text through.
//
// The declaration under test is xsl:strip-space, which has nothing to do with
// types at all. Whitespace stripping is defined over which text nodes survive;
// declaring it must not quietly untype a document the caller validated.
func TestUnionMemberSurvivesStripSpace(t *testing.T) {
	const src = `
	<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	                xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:import-schema schema-location="s.xsd"/>
	  <xsl:strip-space elements="*"/>
	  <xsl:output method="xml" indent="no"/>
	  <xsl:template match="/"><out><xsl:apply-templates select="//Date"/></out></xsl:template>
	  <xsl:template match="Date[data(.) instance of StandardDate]">[standard]</xsl:template>
	  <xsl:template match="Date">[other]</xsl:template>
	</xsl:stylesheet>`

	got := runUnionMember(t, src)
	if !strings.Contains(got, "[standard][other]") {
		t.Errorf("got %s\nwant the first Date to match the StandardDate pattern "+
			"and the second not: [standard][other]", got)
	}
}

// TestUnionMemberWithoutStripSpace pins the behaviour that already worked, so
// that a future change cannot fix the copy by breaking the original.
func TestUnionMemberWithoutStripSpace(t *testing.T) {
	const src = `
	<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	                xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:import-schema schema-location="s.xsd"/>
	  <xsl:output method="xml" indent="no"/>
	  <xsl:template match="/"><out><xsl:apply-templates select="//Date"/></out></xsl:template>
	  <xsl:template match="Date[data(.) instance of StandardDate]">[standard]</xsl:template>
	  <xsl:template match="Date">[other]</xsl:template>
	</xsl:stylesheet>`

	got := runUnionMember(t, src)
	if !strings.Contains(got, "[standard][other]") {
		t.Errorf("got %s\nwant [standard][other]", got)
	}
}

// TestUnionMemberSurvivesCopyOf covers the same loss on the other copy path.
//
// xdmbuild.DeepCopy is what xsl:copy-of, fn:snapshot and xsl:merge all use,
// and it dropped the member for the same reason strip-space did. A copy of an
// assessed element is an assessed element -- validation-1202 already requires
// dm:nilled to survive a copy -- and the member is part of the same assessment.
//
// validation="preserve" is written explicitly because it is the setting under
// test. The default is "strip", which is entitled to discard the annotation
// and the member with it; the bug was that "preserve" discarded the member
// while keeping the annotation, leaving a node annotated as a union whose
// value could no longer be typed.
func TestUnionMemberSurvivesCopyOf(t *testing.T) {
	const src = `
	<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	                xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:import-schema schema-location="s.xsd"/>
	  <xsl:output method="xml" indent="no"/>
	  <xsl:template match="/">
	    <xsl:variable name="copied"><xsl:copy-of select="//Date" validation="preserve"/></xsl:variable>
	    <out>
	      <xsl:for-each select="$copied/Date">
	        <xsl:choose>
	          <xsl:when test="data(.) instance of StandardDate">[standard]</xsl:when>
	          <xsl:otherwise>[other]</xsl:otherwise>
	        </xsl:choose>
	      </xsl:for-each>
	    </out>
	  </xsl:template>
	</xsl:stylesheet>`

	got := runUnionMember(t, src)
	if !strings.Contains(got, "[standard][other]") {
		t.Errorf("got %s\nwant [standard][other]", got)
	}
}
