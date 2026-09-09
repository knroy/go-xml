package xquery_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xquery"
)

// unionsNS and unionsSchema are the shapes the four fixes below turn on, cut
// down from the QT3 suite's own unionListDefined.xsd: a PURE union (no facets
// of its own, atomic members), a pure union one of whose members is a
// pattern-restricted atomic, an IMPURE union (a list type among its members),
// and a union derived by RESTRICTION.
const unionsNS = "http://example.org/unions"

const unionsSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    xmlns:u="http://example.org/unions"
    targetNamespace="http://example.org/unions"
    elementFormDefault="qualified">
  <xs:simpleType name="intOrDate">
    <xs:union memberTypes="xs:integer xs:date"/>
  </xs:simpleType>
  <xs:simpleType name="ibString">
    <xs:restriction base="xs:string">
      <xs:pattern value="IB(\d)+"/>
    </xs:restriction>
  </xs:simpleType>
  <xs:simpleType name="ibOrInt">
    <xs:union memberTypes="u:ibString xs:integer"/>
  </xs:simpleType>
  <xs:simpleType name="decimals">
    <xs:list itemType="xs:decimal"/>
  </xs:simpleType>
  <xs:simpleType name="impure">
    <xs:union memberTypes="xs:date u:decimals"/>
  </xs:simpleType>
  <xs:simpleType name="approxDate">
    <xs:union memberTypes="xs:date xs:dateTime xs:gYear"/>
  </xs:simpleType>
  <xs:simpleType name="restricted">
    <xs:restriction base="u:approxDate">
      <xs:pattern value="20.*"/>
    </xs:restriction>
  </xs:simpleType>
</xs:schema>`

func withUnions() xquery.Options {
	return xquery.Options{Schemas: []xquery.Schema{
		{Namespace: unionsNS, Source: unionsSchema},
	}}
}

// unionQuery prefixes a body with the import the tests all share.
func unionQuery(body string) string {
	return fmt.Sprintf("import schema namespace u = %q;\n%s", unionsNS, body)
}

// TestCastToImpureAndRestrictedUnion is the first root cause: the PURITY rule
// of XPath 3.1 §2.5 was being applied to a cast target as well as to an
// ItemType, so a union carrying facets or holding a list type was refused with
// XPST0003 instead of being answered.
//
// §3.14.2 admits any simple type in the in-scope schema types as a SingleType,
// and a cast has the lexical form in hand, so the union's own facets can
// actually be put to the schema -- which is what the "restricted" rows below
// assert. cbcl-castable-impure-001 and cbcl-castable-restricted-union-001 are
// the suite's form of the true rows.
//
// The negative rows are the point of the fix as much as the positive ones: a
// value the union does NOT admit must still be refused, and by the union's own
// pattern rather than by its members alone. "1901-01-01" is a perfectly good
// xs:date and a member of approxDate; only the "20.*" pattern rejects it.
func TestCastToImpureAndRestrictedUnion(t *testing.T) {
	for _, c := range []struct {
		body string
		want string
	}{
		// Impure: xs:date is a member, and the list member no longer makes
		// the whole union unanswerable.
		{`xs:date("2001-01-01") castable as u:impure`, "true"},
		{`"1.5 2.5" castable as u:impure`, "true"},
		// NEGATIVE: neither a date nor a list of decimals.
		{`"not-a-date" castable as u:impure`, "false"},
		// Restricted: the union's own pattern decides.
		{`xs:date("2001-01-01") castable as u:restricted`, "true"},
		// NEGATIVE: a valid member value the restriction's pattern refuses.
		{`xs:date("1901-01-01") castable as u:restricted`, "false"},
		{`"1999" castable as u:restricted`, "false"},
	} {
		got, err := run(t, unionQuery(c.body), withUnions())
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.body, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.body, got, c.want)
		}
	}
}

// TestCastToImpureUnionRaisesFORG0001 is the cast half of the rule above.
// "castable as" answers false where "cast as" must RAISE, and it must raise
// the dynamic error a failed cast owes rather than the static error a refused
// target owed before.
func TestCastToImpureUnionRaisesFORG0001(t *testing.T) {
	_, err := run(t, unionQuery(`xs:date("1901-01-01") cast as u:restricted`), withUnions())
	if err == nil {
		t.Fatal("a value the union's pattern refuses must not cast")
	}
	if !strings.Contains(err.Error(), "FORG0001") {
		t.Fatalf("want FORG0001 for a failed cast, got %v", err)
	}
}

// TestImpureUnionIsStillNotAnItemType is the BOUNDARY the fix above must not
// cross. §2.5 admits only a PURE union as an ItemType, because "instance of"
// and a function signature have to answer from a value's own annotation, and
// a member of a faceted union does not necessarily satisfy the union's facets
// -- the XSD 1.0 error XSD 1.1 §3.16.6.3 corrected.
//
// So the two contexts genuinely differ, and this asserts they still do: the
// same union that is now a legal CAST target is still refused in a signature.
// This is the positive fact, not the absence of one -- the parameter binding
// must fail, and with the type error it owes.
func TestImpureUnionIsStillNotAnItemType(t *testing.T) {
	src := unionQuery(
		`declare function local:f($a as u:impure) as xs:boolean { true() };
		 local:f(xs:date("2001-01-01"))`)
	_, err := run(t, src, withUnions())
	if err == nil {
		t.Fatal("an impure union must not be usable as an item type")
	}
	if !strings.Contains(err.Error(), "XPTY0004") {
		t.Fatalf("want XPTY0004 for an impure union in a signature, got %v", err)
	}
	// A PURE union in the same position still works, which is what makes the
	// refusal above a rule about purity rather than about schema types.
	got, err := run(t, unionQuery(
		`declare function local:f($a as u:approxDate) as xs:boolean { true() };
		 local:f(xs:date("2001-01-01"))`), withUnions())
	if err != nil {
		t.Fatalf("a pure union must still be a usable item type: %v", err)
	}
	if got != "true" {
		t.Fatalf("pure union signature: got %s, want true", got)
	}
}

// TestSchemaImportVisibleToLaterPrologDecls is the second root cause. A type
// name resolves against the static context at the moment the PARSER reads it,
// and a function signature is parsed where it stands -- so following the
// imports at the END of the prolog left every signature in the prolog unable
// to see a schema the line above it had imported.
//
// The suite's form is Castable-UnionType-36. §4.11 requires imports to precede
// variable and function declarations, so there is no ordering in which loading
// eagerly could see an import a declaration ahead of it should not have.
func TestSchemaImportVisibleToLaterPrologDecls(t *testing.T) {
	src := unionQuery(
		`declare function local:f($a as u:approxDate) as xs:boolean
		     { $a castable as xs:string };
		 local:f(xs:date("2001-01-01"))`)
	got, err := run(t, src, withUnions())
	if err != nil {
		t.Fatalf("a prolog signature must see a schema imported above it: %v", err)
	}
	if got != "true" {
		t.Fatalf("got %s, want true", got)
	}
	// NEGATIVE: without the import the same signature is still a static
	// error, so the test above is measuring the import and not a name that
	// resolves for some other reason.
	_, err = run(t,
		`declare function local:f($a as u:approxDate) as xs:boolean { true() };
		 local:f(1)`, xquery.Options{})
	if err == nil {
		t.Fatal("an unimported type name in a signature must be a static error")
	}
}

// TestCastToUnionConvertsBeforeValidating is the third root cause. A cast
// CONVERTS a value; validation asks whether a value is already in a type's
// lexical space. castToUnion was putting the OPERAND's lexical form to the
// schema after the member cast had already produced a different one, so
// "123.12 cast as u:intOrDate" asked whether "123.12" is an xs:integer --
// it is not -- and raised, where the suite (CastAs-UnionType-3) expects the
// integer 123 the decimal-to-integer cast produces.
func TestCastToUnionConvertsBeforeValidating(t *testing.T) {
	got, err := run(t, unionQuery(`123.12 cast as u:intOrDate`), withUnions())
	if err != nil {
		t.Fatalf("a decimal must cast to an integer member: %v", err)
	}
	if got != "123" {
		t.Fatalf("got %s, want 123", got)
	}
	// The same through the constructor function, which is defined as the cast.
	got, err = run(t, unionQuery(`u:intOrDate(123.12)`), withUnions())
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if got != "123" {
		t.Fatalf("constructor: got %s, want 123", got)
	}
	// NEGATIVE: a value no member accepts however it is converted still fails.
	_, err = run(t, unionQuery(`"zzz" cast as u:intOrDate`), withUnions())
	if err == nil {
		t.Fatal("a value no member accepts must not cast")
	}
	if !strings.Contains(err.Error(), "FORG0001") {
		t.Fatalf("want FORG0001, got %v", err)
	}
}

// TestCastToUnionAppliesMemberFacets is the fourth root cause. castToUnion
// returned an item untouched when its type code already matched a member's,
// which is right only when the schema has nothing further to say. A member
// that is a RESTRICTION carries facets its type code cannot express, so
// "already an xs:string" is not "already a member of a union over a
// pattern-restricted xs:string" -- the pattern still has to hold.
//
// CastAs-UnionType-5a is the suite's form: "AD123456789" is a perfectly good
// xs:string, and the union must still refuse it.
func TestCastToUnionAppliesMemberFacets(t *testing.T) {
	_, err := run(t, unionQuery(`"AD123456789" cast as u:ibOrInt`), withUnions())
	if err == nil {
		t.Fatal("a string the member's pattern refuses must not cast")
	}
	if !strings.Contains(err.Error(), "FORG0001") {
		t.Fatalf("want FORG0001, got %v", err)
	}
	// POSITIVE: a string the pattern DOES admit still casts, so the check
	// above is the pattern applying and not the shortcut merely being gone.
	got, err := run(t, unionQuery(`"IB123" cast as u:ibOrInt`), withUnions())
	if err != nil {
		t.Fatalf("a string the member's pattern admits must cast: %v", err)
	}
	if got != "IB123" {
		t.Fatalf("got %s, want IB123", got)
	}
}

// TestSchemaConstructorForImpureUnion is the constructor half of the first
// fix. Importing a schema makes a constructor available for each simple type
// it defines, and the constructor is DEFINED as a cast -- so a type that is a
// legal cast target has a constructor, impure union included.
func TestSchemaConstructorForImpureUnion(t *testing.T) {
	got, err := run(t, unionQuery(`u:restricted("2001-01-01")`), withUnions())
	if err != nil {
		t.Fatalf("an impure union must have a constructor: %v", err)
	}
	if got != "2001-01-01" {
		t.Fatalf("got %s, want 2001-01-01", got)
	}
	// NEGATIVE: the constructor is the cast, so it refuses what the cast does.
	_, err = run(t, unionQuery(`u:restricted("1901-01-01")`), withUnions())
	if err == nil {
		t.Fatal("the constructor must refuse what the cast refuses")
	}
	if !strings.Contains(err.Error(), "FORG0001") {
		t.Fatalf("want FORG0001, got %v", err)
	}
}

// TestCastToUnionWithListMemberNeedsStringSource is the fifth root cause, and
// the one that keeps the first fix from being too permissive. F&O 3.0 §18.3
// defines a cast to a LIST type from xs:string and xs:untypedAtomic only, so a
// source that is neither may reach a union's ATOMIC members and no others.
//
// Validation alone cannot see the difference: it is handed a lexical form, and
// "1" is a perfectly good one-item list of decimals whatever produced it. The
// SOURCE's own type is what decides -- which is why the suite asks for true
// from xs:untypedAtomic("1 2 3") (cbcl-castable-impure-005) and false from
// xs:decimal("1") (-009) against the very same union.
func TestCastToUnionWithListMemberNeedsStringSource(t *testing.T) {
	for _, c := range []struct {
		body string
		want string
	}{
		// A string-like source reaches every member, the list one included.
		{`"1.5 2.5" castable as u:impure`, "true"},
		{`xs:untypedAtomic("1 2 3") castable as u:impure`, "true"},
		{`"" castable as u:impure`, "true"},
		// An xs:date reaches the ATOMIC member and needs no list at all.
		{`xs:date("2001-01-01") castable as u:impure`, "true"},
		// NEGATIVE: an xs:decimal matches no atomic member, and the list
		// member is out of its reach however good a list "1" would make.
		{`xs:decimal("1") castable as u:impure`, "false"},
		{`xs:decimal("1") castable as u:impure?`, "false"},
	} {
		got, err := run(t, unionQuery(c.body), withUnions())
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.body, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.body, got, c.want)
		}
	}
}
