package xslt

import (
	"context"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// loadTestSchema builds a one-element schema for the tests below.
func loadTestSchema(t *testing.T) *xsd.Schema {
	t.Helper()
	const doc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e" type="xs:string"/>
	</xs:schema>`
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	sch, err := xsd.Load(tree.Root, "test.xsd", xsd.Options{})
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	return sch
}

// TestSetSchemaIfAbsentSatisfiesStrictValidation is streamable-021, -042 and
// -043 in miniature.
//
// Each declares an environment whose <schema> supplies the declarations, then
// validates with validation="strict" while declaring no xsl:import-schema of
// its own -- streamable-021's is commented out. XSLT 2.0 section 3.14 makes an
// import satisfiable "using a schema that is already known to the processor",
// and the suite's own reference driver (run-tests.xsl, c:validated-document)
// synthesises one xsl:import-schema per environment <schema> unconditionally.
// Without a way to install that schema the strict validation had nothing to
// look in and every one failed XTSE1660.
//
// The assertion is on the error CODE: before the change the transform failed
// with XTSE1660, and the fix is only real if that exact code stops appearing.
func TestSetSchemaIfAbsentSatisfiesStrictValidation(t *testing.T) {
	// No xsl:import-schema anywhere in this stylesheet -- that is the point.
	const src = `
	<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
	   xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xsl:template name="main">
	    <xsl:element name="e" validation="strict">x</xsl:element>
	  </xsl:template>
	</xsl:stylesheet>`

	t.Run("without a seeded schema it is XTSE1660", func(t *testing.T) {
		sheet, err := Compile(mustParse(t, src), CompileOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "XTSE1660") {
				return
			}
			t.Fatalf("compile: %v", err)
		}
		_, err = sheet.Transform(context.Background(), nil,
			TransformOptions{InitialTemplate: "main"})
		if err == nil {
			t.Fatal("validation=strict with no schema: got success, want XTSE1660")
		}
		if !strings.Contains(err.Error(), "XTSE1660") {
			t.Fatalf("got %v, want an error mentioning XTSE1660", err)
		}
	})

	t.Run("a seeded schema satisfies it", func(t *testing.T) {
		sheet, err := Compile(mustParse(t, src), CompileOptions{})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if sheet.Schema() != nil {
			t.Fatal("stylesheet declares no xsl:import-schema, want a nil Schema()")
		}
		if !sheet.SetSchemaIfAbsent(loadTestSchema(t)) {
			t.Fatal("SetSchemaIfAbsent reported no seeding on an empty stylesheet")
		}
		if sheet.Schema() == nil {
			t.Fatal("Schema() is still nil after seeding")
		}
		_, err = sheet.Transform(context.Background(), nil,
			TransformOptions{InitialTemplate: "main"})
		if err != nil && strings.Contains(err.Error(), "XTSE1660") {
			t.Fatalf("seeded schema did not satisfy validation=strict: %v", err)
		}
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
	})
}

// TestSetSchemaIfAbsentDoesNotDisplace pins the other half: a declaration the
// stylesheet imported by name is the one it asked for, and a caller's schema
// must not replace it. The harness merges into such a schema instead.
func TestSetSchemaIfAbsentDoesNotDisplace(t *testing.T) {
	sheet := &Stylesheet{}
	own := loadTestSchema(t)
	if !sheet.SetSchemaIfAbsent(own) {
		t.Fatal("first seeding refused")
	}
	if sheet.SetSchemaIfAbsent(loadTestSchema(t)) {
		t.Fatal("SetSchemaIfAbsent displaced a schema that was already there")
	}
	if sheet.Schema() != own {
		t.Fatal("the original schema was replaced")
	}
	// A nil schema is never installed, and a nil receiver never panics.
	if sheet.SetSchemaIfAbsent(nil) {
		t.Fatal("a nil schema was reported as seeded")
	}
	var nilSheet *Stylesheet
	if nilSheet.SetSchemaIfAbsent(own) {
		t.Fatal("a nil stylesheet was reported as seeded")
	}
}
