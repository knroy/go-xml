package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Clause 2.2 of Selector and Fields Value OK admits the unabbreviated axis
// wherever the abbreviation is allowed, and XPath allows whitespace around
// "::". Recognising only the closed, abbreviated spelling failed the whole
// schema over a step it should have accepted.
func TestIdentityConstraintUnabbreviatedAxes(t *testing.T) {
	for _, path := range []string{
		"e",
		"child::e",
		"child :: e",
		"child::  e",
		"a/child::b",
		"child::a/child::b",
	} {
		p := &icPathParser{src: path}
		if _, err := p.parseAlternative(); err != nil {
			t.Errorf("selector %q: %v", path, err)
		}
	}
	for _, path := range []string{
		"@a",
		"attribute::a",
		"attribute :: a",
		"e/@a",
		"e/attribute::a",
	} {
		p := &icPathParser{src: path, field: true}
		if _, err := p.parseAlternative(); err != nil {
			t.Errorf("field %q: %v", path, err)
		}
	}
}

// An axis name is only an axis when "::" follows it. An element may be named
// "child" or "attribute", and consuming those as axes would silently select
// the wrong node rather than fail.
func TestAxisNameIsStillAnElementName(t *testing.T) {
	for _, path := range []string{"child", "attribute", "child/attribute"} {
		p := &icPathParser{src: path}
		alt, err := p.parseAlternative()
		if err != nil {
			t.Fatalf("selector %q: %v", path, err)
		}
		if len(alt.Steps) == 0 {
			t.Errorf("selector %q parsed to no steps; the name was eaten "+
				"as an axis", path)
		}
		if alt.Attribute != nil {
			t.Errorf("selector %q parsed as an attribute step", path)
		}
	}
}

// schemaLocation is a hint. A schema naming a document that cannot be fetched
// still loads, losing only what that document would have contributed — the
// alternative failed the whole schema over a location the spec never promised
// would resolve.
func TestUnresolvableSchemaLocationIsNotFatal(t *testing.T) {
	const doc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	              xmlns:o="http://example.com/other">
	  <xs:import namespace="http://example.com/other"
	             schemaLocation="http://127.0.0.1/must-not-resolve.xyzzy"/>
	  <xs:element name="e" type="xs:string"/>
	</xs:schema>`
	s, err := parseSchemaString(t, doc)
	if err != nil {
		t.Fatalf("an unresolvable import failed the schema: %v", err)
	}
	if _, ok := s.Elements[xdm.QName{Local: "e"}]; !ok {
		t.Error("the declaration beside the failed import was lost")
	}
}

// A reference that genuinely needed the missing components still fails — at
// the reference, naming what is missing, rather than at the import.
func TestReferenceIntoAnUnresolvedImportStillFails(t *testing.T) {
	const doc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	              xmlns:o="http://example.com/other">
	  <xs:import namespace="http://example.com/other"
	             schemaLocation="http://127.0.0.1/must-not-resolve.xyzzy"/>
	  <xs:element name="e" type="o:missing"/>
	</xs:schema>`
	if _, err := parseSchemaString(t, doc); err == nil {
		t.Fatal("a reference to a type from an unresolved import was accepted")
	}
}
