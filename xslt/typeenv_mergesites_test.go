package xslt

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// The two mergeSchema CALL SITES in importschema.go -- the F&O JSON schema at
// :57 and the resolve-by-namespace path at :102 -- fold a separately loaded
// *xsd.Schema into the stylesheet's aggregate. Each has to carry the imported
// schema's TypeEnvironment across with its components, because the aggregate
// is built by xsd.NewSchema and starts with an EMPTY environment: a merge that
// copied only the type definitions would leave every derivation fact behind in
// the schema it came from.
//
// Neither site was observable when mergeSchema's Merge line first landed.
// namespaceSensitiveType was the only consumer reading the environment then,
// and it takes a *xsd.Schema rather than a node, so it could be pinned through
// xsl:import-schema's INLINE path alone. Stamping the environment ON THE NODE
// is what opens these two: a node validated by the aggregate now carries the
// aggregate's environment, so "instance of" walking that node's derivation
// chain is a direct reading of whether the merge brought the facts along.
//
// Both tests below therefore assert through a derivation chain that has to be
// WALKED -- a type whose own name is not the one asked about -- rather than
// through a type's own name, which the per-node resolved fields answer without
// consulting any environment at all.

// jsonTypedSheet imports the F&O function namespace with no location, which is
// importschema.go's SchemaForJSON branch, and names j:mapType in a sequence
// type so the import has to have supplied it.
const jsonTypedSheet = `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:j="http://www.w3.org/2005/xpath-functions"
                xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:import-schema namespace="http://www.w3.org/2005/xpath-functions"/>
  <xsl:output omit-xml-declaration="yes"/>
  <xsl:template match="/">
    <out><xsl:value-of select="
      json-to-xml('{&quot;a&quot;:{&quot;b&quot;:1}}', map{'validate':true()})/j:map/j:map
        instance of element(*, j:mapType)"/></out>
  </xsl:template>
</xsl:stylesheet>`

// TestJSONSchemaMergeCarriesTheTypeEnvironment pins importschema.go's
// SchemaForJSON merge -- and it asserts on the SCHEMA rather than on a node,
// deliberately, because a node assertion here would pass whatever the merge
// did.
//
// The reason is worth stating, because it looked like the same shape as the
// resolve-by-namespace site below and is not. fn:json-to-xml with
// validate=true does NOT type its result against the stylesheet's aggregate:
// xslt/jsonvalidate.go loads its own xsd.SchemaForJSON() and assesses against
// that, which F&O 3.1 section 17.5.3 requires -- the annotations are those
// "that result from validation against the schema given at C.2", so a
// stylesheet that never wrote xsl:import-schema still gets a typed result.
// The node therefore carries THAT schema's environment, and every instance-of
// question about it is answered without consulting the aggregate at all. A
// behavioural assertion through such a node was written first here and passed
// with the merge line deleted, which is the definition of a test that proves
// nothing.
//
// What the merge at :57 actually decides is the STATIC half: whether the
// stylesheet may NAME j:mapType in a sequence type, and whether the aggregate
// can answer what that name derives from. That is the aggregate's environment,
// and asking it directly is the only assertion that distinguishes the merge
// happening from the merge not happening.
//
// The question asked is a derivation LINK -- what j:mapWithinMapType derives
// from -- rather than a type's own presence, because the Types map is copied
// by a separate loop that would keep the name alive with the environment gone.
// Asserting on the link is asserting on the environment and on nothing else.
func TestJSONSchemaMergeCarriesTheTypeEnvironment(t *testing.T) {
	stree, err := xdm.ParseString(jsonTypedSheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	sheet, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	env := sheet.schema.TypeEnv()
	mapType := xdm.AnnotationName(xdm.NSFN, "mapType")
	inner := xdm.AnnotationName(xdm.NSFN, "mapWithinMapType")

	// The type DEFINITIONS arrive by their own loop, so their presence says
	// nothing about the environment. Pinning it anyway separates "the import
	// did not happen" from "the import happened and dropped the environment",
	// which are different bugs with the same symptom below.
	if !sheet.schema.HasElementDeclaration(xdm.QName{URI: xdm.NSFN, Local: "map"}) {
		t.Fatal("precondition: the stylesheet's aggregate has no j:map " +
			"declaration, so the F&O JSON schema was never merged into it " +
			"at all -- the failure below would not be about the environment")
	}

	if got, _, _ := env.Len(); got == 0 {
		t.Fatal("the stylesheet's aggregate schema holds no derivation facts " +
			"at all: mergeSchema copied the F&O JSON schema's components and " +
			"left its TypeEnvironment behind")
	}
	if base := env.DerivedBase(inner); base != mapType {
		t.Errorf("the aggregate's environment says %s derives from %q, want "+
			"%q -- mergeSchema did not carry the F&O JSON schema's "+
			"TypeEnvironment across, so the aggregate holds the type name "+
			"without knowing what it restricts", inner, base, mapType)
	}

	// The transform is run for its own sake: naming j:mapType in a sequence
	// type is a static error unless the import supplied it, so a stylesheet
	// that compiles and runs is evidence the static half works. The BOOLEAN
	// is deliberately not asserted -- see the note above on why a node here
	// answers from a different schema's environment.
	dtree, err := xdm.ParseString(`<doc/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the source: %v", err)
	}
	if _, err := sheet.Transform(context.Background(), dtree.Root,
		TransformOptions{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
}

// nsResolverSchema defines {urn:mergesite}Outer as a restriction of
// {urn:mergesite}Inner, which in turn restricts xs:decimal.
//
// The two-link chain is the point. A one-link type would be answered by the
// node's own DerivedPrimitive field, which validation records directly and
// which no environment is consulted for; asking about the type one link ABOVE
// the annotation forces the walk that reads the environment.
const nsResolverSchema = `<xs:schema
    xmlns:xs="http://www.w3.org/2001/XMLSchema"
    xmlns:m="urn:mergesite" targetNamespace="urn:mergesite"
    elementFormDefault="qualified">
  <xs:simpleType name="Inner"><xs:restriction base="xs:decimal"/></xs:simpleType>
  <xs:simpleType name="Outer"><xs:restriction base="m:Inner"/></xs:simpleType>
  <xs:element name="e" type="m:Outer"/>
</xs:schema>`

// nsOnlyResolver answers a request for the urn:mergesite namespace with the
// schema above and refuses everything else, which is the shape
// tryResolveSchemaByNamespace drives: namespace given, location empty.
type nsOnlyResolver struct{}

func (nsOnlyResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	if namespace == "urn:mergesite" && location == "" {
		return io.NopCloser(strings.NewReader(nsResolverSchema)),
			"mergesite.xsd", nil
	}
	return nil, "", nil
}

// nsResolvedSheet imports that namespace with no schema-location, so the only
// way its components can arrive is tryResolveSchemaByNamespace followed by
// mergeSchema. It then validates an element against the imported declaration
// and asks whether the validated node is an instance of the type one link
// ABOVE its own.
const nsResolvedSheet = `
<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:m="urn:mergesite"
                xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:import-schema namespace="urn:mergesite"/>
  <xsl:output omit-xml-declaration="yes"/>
  <xsl:template match="/">
    <out><xsl:value-of select="/m:e instance of element(*, m:Inner)"/></out>
  </xsl:template>
</xsl:stylesheet>`

// TestNamespaceResolvedSchemaMergeCarriesTheTypeEnvironment pins
// importschema.go's resolve-by-namespace merge.
//
// The source document is validated against the imported schema before the
// transform, which is what stamps the aggregate's environment onto the node.
// The instance-of test then walks {urn:mergesite}Outer's chain looking for
// {urn:mergesite}Inner, a step that exists only in the environment the merge
// was supposed to bring across.
func TestNamespaceResolvedSchemaMergeCarriesTheTypeEnvironment(t *testing.T) {
	stree, err := xdm.ParseString(nsResolvedSheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	sheet, err := Compile(stree.Root,
		CompileOptions{SchemaResolver: nsOnlyResolver{}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	outer := xdm.AnnotationName("urn:mergesite", "Outer")
	inner := xdm.AnnotationName("urn:mergesite", "Inner")
	if got := sheet.schema.TypeEnv().DerivedBase(outer); got != inner {
		t.Fatalf("precondition: the aggregate's environment says %s derives "+
			"from %q, want %q -- mergeSchema did not carry the resolved "+
			"schema's TypeEnvironment across", outer, got, inner)
	}

	// The instance is validated by the stylesheet's own aggregate, so the node
	// is stamped with the aggregate's environment and the instance-of walk
	// below reads exactly what the merge produced.
	dtree, err := xdm.ParseString(
		`<m:e xmlns:m="urn:mergesite">10</m:e>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the source: %v", err)
	}
	if err := sheet.schema.Validate(dtree.Root,
		xsd.ValidateOptions{Annotate: true}); err != nil {
		t.Fatalf("validating the source against the aggregate: %v", err)
	}
	if got := dtree.Root.Children[0].TypeAnnotation; got != outer {
		t.Fatalf("precondition: validation annotated the element %q, want %q",
			got, outer)
	}

	out, err := sheet.Transform(context.Background(), dtree.Root,
		TransformOptions{})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if !strings.Contains(out.String(), "true") {
		t.Errorf("an element validated as %s is not an instance of "+
			"element(*, %s), which it restricts: got %q, want a result "+
			"containing \"true\" (the resolved schema's derivation facts did "+
			"not reach the stylesheet's aggregate schema)", outer, inner, out.String())
	}
}
