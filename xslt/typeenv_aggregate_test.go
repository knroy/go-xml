package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// XTTE1545 is decided by walking a type's derivation chain up to xs:QName or
// xs:NOTATION, and namespaceSensitiveType walked the PROCESS-GLOBAL derivation
// table to do it. That table is keyed by type name alone, so a second schema
// defining the same name for a different type overwrites the first's entry --
// and the overwrite then decides the question for a stylesheet that has never
// heard of the second schema.
//
// The direction of the error is what makes it serious. Losing the chain
// returns false, the PERMISSIVE verdict, so the forbidden validation goes
// ahead: XSLT 2.0 section 19.2 says a constructed attribute may not be
// validated against a type derived from xs:QName, because its value is a
// string with no namespace context for the prefix to resolve against.
//
// The stylesheet is compiled ONCE and transformed twice, with the shadowing
// schema loaded in between. That ordering is the whole point: compiling again
// would re-register this stylesheet's own "qn" into the global table and paper
// over the collision, which is exactly how this bug hides in a test that
// compiles per run.
//
// The assertion is on the ERROR CODE AND REASON rather than on any output,
// for the same reason xsd/annotation_isolation_test.go asserts on the type
// code: both schemas define "qn", and only the verdict distinguishes them.
// Asserting merely that an error occurred would pass while the bug is live --
// the forbidden validation proceeds and then fails downstream with XTTE1540
// "uses a prefix with no in-scope namespace declaration", a different fault
// reported for a different reason.

// qnameAggregateSheet imports a schema defining "qn" as a restriction of
// xs:QName and copies an attribute against it. The schema carries no
// targetNamespace, so "qn" is registered under its bare local name -- which is
// what namespaceSensitiveType looks up, since it walks QName.Local.
const qnameAggregateSheet = `
<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
                xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xsl:import-schema>
    <xs:schema>
      <xs:simpleType name="qn"><xs:restriction base="xs:QName"/></xs:simpleType>
    </xs:schema>
  </xsl:import-schema>
  <xsl:output omit-xml-declaration="yes"/>
  <xsl:template match="/">
    <out><xsl:copy-of select="/doc/@a" type="qn"/></out>
  </xsl:template>
</xsl:stylesheet>`

// shadowingQnSchema defines the SAME name over a different base. Loading it
// rewrites the process-global entry for "qn" to xs:string, which is not
// namespace-sensitive at all.
const shadowingQnSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:simpleType name="qn"><xs:restriction base="xs:string"/></xs:simpleType>
</xs:schema>`

// TestNamespaceSensitiveTypeReadsTheStylesheetsOwnSchema is the regression.
func TestNamespaceSensitiveTypeReadsTheStylesheetsOwnSchema(t *testing.T) {
	stree, err := xdm.ParseString(qnameAggregateSheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the stylesheet: %v", err)
	}
	sheet, err := Compile(stree.Root, CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	transform := func() error {
		dtree, err := xdm.ParseString(`<doc a="p:v" xmlns:p="urn:p"/>`,
			xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parsing the source: %v", err)
		}
		_, err = sheet.Transform(context.Background(), dtree.Root,
			TransformOptions{})
		return err
	}

	// Precondition: with nothing shadowing it, the refusal is reported. This
	// pins that the test reaches the code under test at all -- without it, a
	// change that broke the whole path would read as a pass.
	err = transform()
	if err == nil || !strings.Contains(err.Error(), "XTTE1545") {
		t.Fatalf("precondition: copying an attribute against a restriction of "+
			"xs:QName must be XTTE1545; got %v", err)
	}

	// An unrelated schema redefines "qn" over xs:string, exactly as a host
	// sharing one process between a stylesheet and a schema would load it.
	str, err := xdm.ParseString(shadowingQnSchema, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the shadowing schema: %v", err)
	}
	if _, err := xsd.Load(str.Root, "shadow.xsd", xsd.Options{}); err != nil {
		t.Fatalf("loading the shadowing schema: %v", err)
	}
	if got := xdm.DerivedBase("qn"); got != "string" {
		t.Fatalf("precondition: the shadowing schema did not take the global "+
			"entry: DerivedBase(%q) = %q, want %q", "qn", got, "string")
	}

	// The compiled stylesheet is unchanged and its own schema still says
	// xs:QName, so its verdict must be unchanged too.
	err = transform()
	if err == nil {
		t.Fatal("an unrelated schema redefining \"qn\" over xs:string " +
			"suppressed XTTE1545 entirely: the stylesheet validated a " +
			"constructed attribute against its own restriction of xs:QName, " +
			"which XSLT 2.0 section 19.2 forbids")
	}
	if !strings.Contains(err.Error(), "XTTE1545") ||
		!strings.Contains(err.Error(), "derived from xs:QName") {
		t.Errorf("after an unrelated schema redefined \"qn\", the verdict "+
			"changed: got %v\nwant XTTE1545 ... derived from xs:QName "+
			"(the stylesheet's own schema still restricts xs:QName)", err)
	}
}
