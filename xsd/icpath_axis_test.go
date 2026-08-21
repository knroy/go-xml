package xsd

import (
	"os"
	"path/filepath"
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

// A reference that genuinely needed the missing components still fails — but
// against the instance that reaches it, not at load. The schema keeps working
// for everything that did not depend on the import.
func TestReferenceIntoAnUnresolvedImportStillFails(t *testing.T) {
	const doc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	              xmlns:o="http://example.com/other">
	  <xs:import namespace="http://example.com/other"
	             schemaLocation="http://127.0.0.1/must-not-resolve.xyzzy"/>
	  <xs:element name="e" type="o:missing"/>
	</xs:schema>`
	if _, err := parseSchemaString(t, doc); err != nil {
		t.Fatalf("the schema did not load: %v", err)
	}
	if err := validateString(t, doc, `<e/>`); err == nil {
		t.Fatal("using a type from an unresolved import was accepted")
	}
}

// A list or union naming a type that does not exist loads, and fails against
// the value that reaches it — missing006 is "Error only if the list type is
// needed for validation". Checking at use also keeps a half-built list from
// being walked, whose ItemType is nil.
func TestUnresolvedListItemTypeIsReportedAtUse(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="l">
	    <xs:list itemType="absent"/>
	  </xs:simpleType>
	  <xs:element name="good" type="xs:integer"/>
	  <xs:element name="bad" type="l"/>
	</xs:schema>`
	if _, err := parseSchemaString(t, schema); err != nil {
		t.Fatalf("the schema did not load: %v", err)
	}
	if err := validateString(t, schema, `<good>1</good>`); err != nil {
		t.Errorf("the sound declaration failed: %v", err)
	}
	if err := validateString(t, schema, `<bad>x</bad>`); err == nil {
		t.Error("a list with a missing item type validated a value")
	}
}

// A substitution group head that does not exist is not an error for the
// declaration naming it. The affiliation decides only what the element may
// substitute *for*, so using the declaration directly asks nothing of the
// head — and nothing can substitute for a head that does not exist either.
func TestMissingSubstitutionGroupHeadIsHarmless(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="bad" substitutionGroup="rotten"/>
	</xs:schema>`
	if _, err := parseSchemaString(t, schema); err != nil {
		t.Fatalf("the schema did not load: %v", err)
	}
	if err := validateString(t, schema, `<bad>3</bad>`); err != nil {
		t.Errorf("an element naming a missing head was rejected: %v", err)
	}
}

// A chameleon include converts the whole included document to the including
// namespace, not only its declarations. A reference written unprefixed named a
// component in the same document, and it must go on naming it after the move —
// leaving it in the absent namespace pointed it at nothing.
func TestChameleonIncludeConvertsReferences(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "mod.xsd")
	if err := os.WriteFile(mod, []byte(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root" type="ct"/>
	  <xs:complexType name="ct">
	    <xs:group ref="g"/>
	    <xs:attribute ref="a"/>
	  </xs:complexType>
	  <xs:group name="g">
	    <xs:sequence><xs:element name="kid" type="xs:string"/></xs:sequence>
	  </xs:group>
	  <xs:attribute name="a" type="st"/>
	  <xs:simpleType name="st">
	    <xs:restriction base="xs:string"/>
	  </xs:simpleType>
	</xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.xsd")
	if err := os.WriteFile(main, []byte(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           targetNamespace="http://foo.com" xmlns="http://foo.com">
	  <xs:include schemaLocation="mod.xsd"/>
	</xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadFiles([]string{main},
		Options{Resolver: &FileResolver{}})
	if err != nil {
		t.Fatalf("the chameleon include did not resolve: %v", err)
	}
	// Every component landed in the including namespace, references
	// included, so the declaration is findable there.
	if _, ok := s.Elements[xdm.QName{URI: "http://foo.com", Local: "root"}]; !ok {
		t.Error("the included declaration is not in the including namespace")
	}
}

// One file can be named by more than one resolved path. Deduplicating on the
// path string alone makes two documents of it, and every global in it then
// collides with itself — which is what msData's schZ012 does, importing
// "Schz012_b.xsd" from a document read as "schZ012_b.xsd".
func TestSameFileReachedByTwoSpellings(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "Lib.xsd")
	if err := os.WriteFile(lib, []byte(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="shared" type="xs:string"/>
	</xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.xsd")
	if err := os.WriteFile(main, []byte(`
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:include schemaLocation="Lib.xsd"/>
	  <xs:include schemaLocation="./Lib.xsd"/>
	</xs:schema>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFiles([]string{main},
		Options{Resolver: &FileResolver{}}); err != nil {
		t.Fatalf("one file reached twice was read as two: %v", err)
	}
}

// A path may run out after the axis. "attribute::" names an axis and then
// stops, and everything the attribute branch does next indexes the source, so
// the exhausted case has to be answered before the indexing rather than by a
// panic. The suite reaches this through a schema whose field is written
// xpath="attribute::", which is invalid and must be reported as such — a
// crash takes the caller down instead of failing the one schema.
func TestIdentityConstraintTruncatedAxisIsAnError(t *testing.T) {
	for _, path := range []string{
		"attribute::",
		"attribute:: ",
		"e/attribute::",
	} {
		p := &icPathParser{src: path, field: true}
		if _, err := p.parseAlternative(); err == nil {
			t.Errorf("field %q: want an error, got none", path)
		}
	}
}
