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
  <xs:simpleType name="nsSensitive">
    <xs:union memberTypes="xs:date xs:QName"/>
  </xs:simpleType>
  <xs:simpleType name="sizeType">
    <xs:restriction base="xs:integer">
      <xs:minInclusive value="1"/>
      <xs:maxInclusive value="19"/>
    </xs:restriction>
  </xs:simpleType>
  <xs:simpleType name="ncnameOrQName">
    <xs:union memberTypes="xs:NCName xs:QName"/>
  </xs:simpleType>
  <xs:simpleType name="lowercaseName">
    <xs:restriction base="u:ncnameOrQName">
      <xs:pattern value="[a-z:]+"/>
    </xs:restriction>
  </xs:simpleType>
  <xs:simpleType name="idrefList">
    <xs:list itemType="xs:IDREF"/>
  </xs:simpleType>
  <xs:simpleType name="ncnameOrQNameList">
    <xs:list itemType="u:ncnameOrQName"/>
  </xs:simpleType>
  <xs:simpleType name="idrefsOrNames">
    <xs:union memberTypes="xs:IDREFS u:ncnameOrQNameList"/>
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
//
// That error is XPST0051, the code for a name that is in scope as a type but
// not as an ITEM type, and it is raised STATICALLY -- when the signature is
// compiled, not when a value reaches it. This assertion originally said
// XPTY0004, which is what the engine happened to raise before the ItemType
// purity check reached schema types: nothing refused the signature, so the
// refusal fell through to the value binding and read as an ordinary type
// mismatch. The suite settles which is right, and it is not the one that was
// asserted here. FunctionCall-032 declares "as lu:unionOfListType" and
// FunctionCall-039 declares "as lu:restrictedUnionType" -- the two shapes of
// impurity, a union holding a list and a union derived by restriction -- and
// both require XPST0051. No suite case asks for XPTY0004 in this position.
//
// The distinction is worth keeping straight because the code names the defect:
// XPTY0004 says a value was wrong, which invites a caller to pass a different
// value, and no value would have helped. XPST0051 says the signature is.
func TestImpureUnionIsStillNotAnItemType(t *testing.T) {
	src := unionQuery(
		`declare function local:f($a as u:impure) as xs:boolean { true() };
		 local:f(xs:date("2001-01-01"))`)
	_, err := run(t, src, withUnions())
	if err == nil {
		t.Fatal("an impure union must not be usable as an item type")
	}
	if !strings.Contains(err.Error(), "XPST0051") {
		t.Fatalf("want XPST0051 for an impure union in a signature, got %v", err)
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

// TestPureUnionReturnTypeConvertsUntypedAtomic covers the function CONVERSION
// rules where the declared type is a pure union.
//
// §3.1.5 casts an xs:untypedAtomic to whatever the declared type is, and for a
// union §3.14.2 defines that cast as trying the members in order. Neither rule
// has an exception for "the type has no atomic type code", but the engine's
// guard did: a union has no single primitive to erase to, so it failed the
// "is this an atomic type" test and was refused before the cast that was owed
// to it could be attempted.
//
// The two halves below are the two halves of the rule. The first is the
// conversion itself, and it asserts the RESULT TYPE rather than only the
// string, because the whole claim is that the value stopped being untyped: the
// same digits render identically whether the cast happened or not, so a string
// assertion alone would pass against the bug. The second is the boundary -- a
// lexical form no member admits must still be refused, so that the fix reads
// as "try the members" and not as "let anything through".
//
// FunctionCall-037 and -038 are the suite's form of the first half, both
// asserting the result is an xs:date.
func TestPureUnionReturnTypeConvertsUntypedAtomic(t *testing.T) {
	got, err := run(t, unionQuery(
		`declare function local:f($s as xs:string) as u:approxDate {
		   xs:untypedAtomic($s)
		 };
		 local:f("2012-12-12") instance of xs:date`), withUnions())
	if err != nil {
		t.Fatalf("a pure union return type must convert an untypedAtomic: %v", err)
	}
	if got != "true" {
		t.Fatalf("want the converted value to be an xs:date, got %q", got)
	}

	// A pure union converts by trying its members; a value none of them
	// admits has nowhere to land and owes the declared-type error.
	_, err = run(t, unionQuery(
		`declare function local:f($s as xs:string) as u:approxDate {
		   xs:untypedAtomic($s)
		 };
		 local:f("not-a-date")`), withUnions())
	if err == nil {
		t.Fatal("a lexical form no member of the union admits must be refused")
	}
	if !strings.Contains(err.Error(), "XPTY0004") {
		t.Fatalf("want XPTY0004 for a value outside the union, got %v", err)
	}
}

// TestNamespaceSensitiveUnionRefusesUntypedAtomic is the exclusion §3.1.5
// carves out of the conversion the test above enables.
//
// Casting an xs:untypedAtomic to xs:QName means resolving whatever prefix its
// string carries, and the only bindings in scope at a call are the callee's,
// which have nothing to do with where the value was written. §3.1.5 therefore
// refuses the case outright with XPTY0117 rather than letting it read as an
// ordinary mismatch -- the codes say different things, and this one says that
// no value the caller could have written would have worked.
//
// The union is what makes this a separate check: it carries no atomic type
// code of its own, so a test that asks only "is the declared type xs:QName"
// cannot see the QName member and would let the conversion proceed.
// FunctionCall-041 is the suite's form, and requires XPTY0117.
func TestNamespaceSensitiveUnionRefusesUntypedAtomic(t *testing.T) {
	_, err := run(t, unionQuery(
		`declare function local:f() as u:nsSensitive {
		   xs:untypedAtomic("xsi:type")
		 };
		 local:f()`), withUnions())
	if err == nil {
		t.Fatal("a namespace-sensitive union must refuse an untypedAtomic")
	}
	if !strings.Contains(err.Error(), "XPTY0117") {
		t.Fatalf("want XPTY0117 for a namespace-sensitive union, got %v", err)
	}

	// The same union still admits a value that IS already a QName, which is
	// what says XPTY0117 is about the CONVERSION and not about the type.
	got, err := run(t, unionQuery(
		`declare function local:f() as u:nsSensitive { xs:QName("xs:string") };
		 local-name-from-QName(local:f())`), withUnions())
	if err != nil {
		t.Fatalf("a QName already of the member type must be admitted: %v", err)
	}
	if got != "string" {
		t.Fatalf("want the QName through unconverted, got %q", got)
	}

	// A union with NO QName member must still convert, and this row is here
	// because of what it caught. u:nsSensitive is a union over xs:date AND
	// xs:QName, so a walk that returned true for ANY member -- rather than
	// for the QName one -- passes both assertions above: sabotaging the walk
	// to look for xs:date left the test green. u:approxDate has no QName
	// member anywhere in it, so it separates "this union is
	// namespace-sensitive" from "this union has members at all", and the
	// conversion the rules owe it must still happen.
	got, err = run(t, unionQuery(
		`declare function local:f() as u:approxDate {
		   xs:untypedAtomic("2012-12-12")
		 };
		 local:f() instance of xs:date`), withUnions())
	if err != nil {
		t.Fatalf("a union with no QName member must still convert: %v", err)
	}
	if got != "true" {
		t.Fatalf("want the non-sensitive union converted to xs:date, got %q", got)
	}
}

// TestSchemaListTypeIsNotAnItemType is the list half of the ItemType purity
// rule, over a SCHEMA-defined list rather than one of the three built-ins.
//
// §2.5.4 admits only a generalized atomic type in an ItemType, and a list is
// not one: its value is a sequence of tokens, and XPath has no way to say "a
// sequence of exactly the tokens this list admits". The engine already refused
// xs:NMTOKENS here; a schema-defined list reached the same position and was
// not refused, so the failure fell through to the value binding and arrived as
// XPTY0004 -- a dynamic error for a static defect.
//
// FunctionCall-034 declares "as lu:listType" and requires XPST0051.
func TestSchemaListTypeIsNotAnItemType(t *testing.T) {
	_, err := run(t, unionQuery(
		`declare function local:f($a as u:decimals) as xs:boolean { true() };
		 local:f(1)`), withUnions())
	if err == nil {
		t.Fatal("a schema-defined list must not be usable as an item type")
	}
	if !strings.Contains(err.Error(), "XPST0051") {
		t.Fatalf("want XPST0051 for a list type in a signature, got %v", err)
	}

	// The same list type is still a legal CAST target, which is the boundary
	// §3.14.2 draws against §2.5 and the reason this check belongs in the
	// ItemType positions alone rather than in the cast-target check.
	got, err := run(t,
		unionQuery(`xs:untypedAtomic("1.5 2.5") castable as u:decimals`),
		withUnions())
	if err != nil {
		t.Fatalf("a list type must remain a legal cast target: %v", err)
	}
	if got != "true" {
		t.Fatalf("want the list cast target still answerable, got %q", got)
	}
}

// canonNS and canonSchema mirror the QT3 suite's own derived.xsd: restrictions
// whose pattern facet admits ONLY the canonical lexical form of the type's
// primitive. They are the shapes CastableAs653-658 turn on.
const canonNS = "http://example.org/canon"

const canonSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    xmlns:c="http://example.org/canon"
    targetNamespace="http://example.org/canon"
    elementFormDefault="qualified">
  <xs:simpleType name="canonicalDecimal">
    <xs:restriction base="xs:decimal">
      <xs:pattern value="-?[0-9]+\.[0-9]+"/>
    </xs:restriction>
  </xs:simpleType>
  <xs:simpleType name="canonicalDouble">
    <xs:restriction base="xs:double">
      <xs:pattern value="-?[0-9]+\.[0-9]+E-?[0-9]+"/>
    </xs:restriction>
  </xs:simpleType>
  <xs:simpleType name="plainInteger">
    <xs:restriction base="xs:integer">
      <xs:pattern value="[0-9]+"/>
    </xs:restriction>
  </xs:simpleType>
</xs:schema>`

func withCanon() xquery.Options {
	return xquery.Options{Schemas: []xquery.Schema{
		{Namespace: canonNS, Source: canonSchema},
	}}
}

func canonQuery(body string) string {
	return fmt.Sprintf("import schema namespace c = %q;\n%s", canonNS, body)
}

// TestPatternFacetMatchesCanonicalRepresentation is the first root cause: a
// pattern facet was tested against the SOURCE value's fn:string form, where
// F&O 3.0 §18.3.3 requires the canonical lexical representation of the cast
// RESULT -- W3C bug 26865, which is why CastableAs653-658 carry the title
// "Pattern must match canonical representation (not the result of string())".
//
// The two disagree exactly here. string(xs:decimal(12)) is "12", which the
// pattern "-?[0-9]+\.[0-9]+" refuses; the canonical decimal is "12.0", which
// it admits. string(xs:double(93.7)) is "93.7"; the canonical double is
// "9.37E1", and the pattern demands the exponent.
func TestPatternFacetMatchesCanonicalRepresentation(t *testing.T) {
	for _, c := range []struct {
		body string
		want string
	}{
		// CastableAs653/654: an integer reaches a decimal pattern that only
		// the canonical "12.0" satisfies.
		{`12 castable as c:canonicalDecimal`, "true"},
		{`-12 castable as c:canonicalDecimal`, "true"},
		// CastableAs655-658: the canonical double always carries an exponent,
		// including for zero, where fn:string writes a bare "0".
		{`93.7 castable as c:canonicalDouble`, "true"},
		{`-93.7 castable as c:canonicalDouble`, "true"},
		{`0.0e0 castable as c:canonicalDouble`, "true"},
		{`-0.0e0 castable as c:canonicalDouble`, "true"},
		// NEGATIVE: the canonical form is not a licence to admit everything.
		// NaN and INF have no canonical form the pattern matches.
		{`xs:double("NaN") castable as c:canonicalDouble`, "false"},
		{`xs:double("INF") castable as c:canonicalDouble`, "false"},
		// NEGATIVE: xs:integer is a primitive in its own right for this rule
		// (F&O §18.3.1 names it one) and its canonical form is the bare
		// digits. Canonicalising it as a decimal would write "12.0" and break
		// a pattern written for integers.
		{`12 castable as c:plainInteger`, "true"},
		{`-12 castable as c:plainInteger`, "false"},
	} {
		got, err := run(t, canonQuery(c.body), withCanon())
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.body, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.body, got, c.want)
		}
	}
}

// TestCastToCanonicalPatternProducesTheValue is the cast half: "castable as"
// answering true obliges "cast as" to produce the value, annotated as the type
// that admitted it.
func TestCastToCanonicalPatternProducesTheValue(t *testing.T) {
	got, err := run(t, canonQuery(`12 cast as c:canonicalDecimal`), withCanon())
	if err != nil {
		t.Fatalf("a value whose canonical form matches must cast: %v", err)
	}
	if got != "12" {
		t.Fatalf("want the decimal 12, got %q", got)
	}
	got, err = run(t, canonQuery(
		`12 cast as c:canonicalDecimal instance of c:canonicalDecimal`), withCanon())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "true" {
		t.Fatalf("the cast result must be an instance of the target, got %q", got)
	}
}

// TestCastToCanonicalPatternRaisesOnTheFacet pins the negative direction as a
// specific error rather than as the absence of one: a value whose canonical
// form the pattern refuses must fail the cast, naming the facet it failed.
//
// "-12" IS the canonical xs:integer form, so the refusal here is the pattern's
// own doing and not a canonicalisation defect -- which is the point. The
// canonical form is what the facet is applied to; it is not a licence to pass.
func TestCastToCanonicalPatternRaisesOnTheFacet(t *testing.T) {
	_, err := run(t, canonQuery(`-12 cast as c:plainInteger`), withCanon())
	if err == nil {
		t.Fatal("a canonical form the pattern refuses must not cast")
	}
	if !strings.Contains(err.Error(), "pattern facet of plainInteger") {
		t.Fatalf("want the pattern facet named as the cause, got %v", err)
	}
	if !strings.Contains(err.Error(), `"-12"`) {
		t.Fatalf("want the canonical form that was tested reported, got %v", err)
	}
}

// TestImpureUnionListMemberCastsToASequence is the second root cause, and a
// different bug from the one above despite sharing the cast path.
//
// A union holding a list type is impure, so a cast to it is decided by the
// schema rather than by the pure-union member walk. The value was returned
// UNCHANGED -- the single string handed in -- where F&O 3.0 §18.3.6 makes a
// cast to a list type a SEQUENCE, one value per whitespace-separated token
// ("the effect ... is the same as ... validating it using L as the governing
// type, and atomizing the resulting node"; its own example has
// my:coordinates("2 -1") return two xs:integer values).
//
// The count is what the suite pins, and it is why cbcl-castable-impure-010 and
// -020 wanted false: the constructor owes THREE xs:decimal values, and a
// three-item sequence is castable to nothing -- with or without the "?" that
// separates -020 from -010, since neither admits three items.
func TestImpureUnionListMemberCastsToASequence(t *testing.T) {
	for _, c := range []struct {
		body string
		want string
	}{
		// The list member is reached, so the result is its items.
		{`count(u:impure("1 2 3"))`, "3"},
		{`u:impure("1 2 3") instance of xs:decimal+`, "true"},
		{`string-join(for $d in u:impure("1 2 3") return string($d), "|")`, "1|2|3"},
		// cbcl-castable-impure-010 and -020: a three-item sequence is not
		// castable, and the "?" does not change that.
		{`u:impure("1 2 3") castable as u:impure`, "false"},
		{`u:impure("1 2 3") castable as u:impure?`, "false"},
		// BOUNDARY: an ATOMIC member is tried first and keeps a single item,
		// so a date does not get shredded into a one-item list of decimals.
		// The item keeps the operand's own type -- an impure union
		// contributes no value space of its own, so the cast neither
		// canonicalises nor re-types; only the SHAPE is at issue here.
		{`count(u:impure("2001-01-01"))`, "1"},
		{`string(u:impure("2001-01-01"))`, "2001-01-01"},
		// One item, so it is still castable, which is what separates this
		// from -010 and -020 above.
		{`u:impure("2001-01-01") castable as u:impure`, "true"},
		// BOUNDARY: the source-type rule the list member depends on is
		// untouched. cbcl-castable-impure-005 is true from an untypedAtomic
		// and -009 false from an xs:decimal, because a cast to a list member
		// is defined from a string-like source only.
		{`xs:untypedAtomic("1 2 3") castable as u:impure`, "true"},
		{`xs:decimal("1") castable as u:impure`, "false"},
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

// TestPureListTypeConstructorIsUnchanged is the boundary the fix above must
// not cross: a type that is ITSELF a list already produced a sequence, through
// the SchemaListType branch, and still must.
func TestPureListTypeConstructorIsUnchanged(t *testing.T) {
	got, err := run(t, unionQuery(`count(u:decimals("1 2 3"))`), withUnions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "3" {
		t.Fatalf("a list constructor must yield one item per token, got %q", got)
	}
}

// TestAtomicTypeConstructorAppliesFacets asserts F&O 3.0 17.5 for an imported
// ATOMIC type: "The semantics of the constructor function xs:TYPE(arg) are
// identical to the semantics of arg cast as xs:TYPE?", and a value outside the
// type raises err:FORG0001.
//
// The constructor had been folded into a cast that carried only the primitive
// type code, with no handle on the schema, so u:sizeType -- an xs:integer
// restricted to 1..19 -- accepted every integer. The cast form checked the
// same facets correctly, which is exactly the disagreement 17.5 forbids, and
// is why every row below asserts the two forms together rather than the
// constructor alone. prod-CastExpr.schema/user-defined-2 is the suite's case.
func TestAtomicTypeConstructorAppliesFacets(t *testing.T) {
	// Outside the range: both forms owe FORG0001.
	for _, body := range []string{
		`u:sizeType(20)`,
		`20 cast as u:sizeType`,
		`u:sizeType(0)`,
		`0 cast as u:sizeType`,
		`u:sizeType("20")`,
	} {
		if _, err := run(t, unionQuery(body), withUnions()); err == nil {
			t.Errorf("%s: a value outside the facets must raise FORG0001", body)
		} else if !strings.Contains(err.Error(), "FORG0001") {
			t.Errorf("%s: want FORG0001, got %v", body, err)
		}
	}
	// Inside the range: both forms yield the value, so the check above is a
	// statement about the facets and not a constructor that refuses
	// everything.
	for _, c := range []struct{ body, want string }{
		{`u:sizeType(19)`, "19"},
		{`19 cast as u:sizeType`, "19"},
		{`u:sizeType(1)`, "1"},
		{`u:sizeType("15")`, "15"},
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

// TestSchemaConstructorIsReachableDynamically pins the root cause: the
// constructor function of an imported schema type is registered in no function
// library -- the set of them is not known until a schema is imported -- so it
// was folded into its cast only for a call written out in the source. Every
// DYNAMIC route to the same function found nothing and reported XPST0017 (or,
// for a partial application, went looking in the library and failed there).
//
// F&O 3.0 16.4.3 makes fn:function-lookup behave like a named function
// reference, and XPath 3.0 3.1.6 makes "f(?)" on an arity-1 function the same
// item "f#1" is; "f#1" already worked, so the other two must agree with it.
// CastAs-UnionType-8 and -9 and CastAs-ListType-24 are the suite's form.
func TestSchemaConstructorIsReachableDynamically(t *testing.T) {
	// Each row is a different way of naming the SAME function item. The
	// expected value is the one the written-out call u:sizeType("12") gives,
	// so a row that disagrees with it is the bug whatever it returns.
	for _, c := range []struct {
		what string
		body string
	}{
		{"named reference", `let $f := u:sizeType#1 return $f("12")`},
		{"partial application", `let $f := u:sizeType(?) return $f("12")`},
		{"function-lookup", `let $f := function-lookup(
			QName("` + unionsNS + `", "sizeType"), 1) return $f("12")`},
	} {
		got, err := run(t, unionQuery(c.body), withUnions())
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.what, err)
			continue
		}
		if got != "12" {
			t.Errorf("%s: got %q, want %q", c.what, got, "12")
		}
	}
	// The facets travel with it: a constructor reached dynamically is the
	// same cast, so a value the type refuses must still be refused. Without
	// this the rows above would pass on a constructor that erased the type.
	_, err := run(t, unionQuery(`let $f := function-lookup(
		QName("`+unionsNS+`", "sizeType"), 1) return $f("99")`), withUnions())
	if err == nil {
		t.Fatal("a value outside the type's range must not construct")
	}
	if !strings.Contains(err.Error(), "FORG0001") {
		t.Fatalf("want FORG0001 from the dynamic constructor, got %v", err)
	}
	// A name that is no type in the static context is still the empty
	// sequence, which is what F&O 16.1.1 owes for any name not in scope --
	// the fix must not turn every unknown name into a function.
	got, err := run(t, unionQuery(`empty(function-lookup(
		QName("`+unionsNS+`", "noSuchType"), 1))`), withUnions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "true" {
		t.Fatalf("an unknown name must look up to the empty sequence, got %s", got)
	}
}

// TestCastToUnionYieldsTheMemberType pins the rule F&O 3.0 18.3.2 states and
// this engine's erased type codes cannot carry: the result of a cast to a
// union is an instance of the MEMBER that accepted the value.
//
// XPath erases every derived string type to xs:string, so a union over
// xs:NCName and xs:QName reports the members as {xs:string, xs:QName} and
// casting to the first produced a bare xs:string -- applying none of the
// member's facets and satisfying "instance of xs:NCName" not at all. The
// member's NAME is what closes the gap. CastAs-UnionType-18 and -26 assert
// exactly these two rows; -34 is the restricted-union row.
func TestCastToUnionYieldsTheMemberType(t *testing.T) {
	for _, c := range []struct {
		body string
		want string
	}{
		// A PURE union: the NCName member accepts it, so the result is one.
		{`u:ncnameOrQName("candlewick") instance of xs:NCName`, "true"},
		{`("candlewick" cast as u:ncnameOrQName) instance of xs:NCName`, "true"},
		// A union derived by RESTRICTION reaches its members the same way.
		{`u:lowercaseName("candlewick") instance of xs:NCName`, "true"},
		{`(xs:untypedAtomic("2001-01-01") cast as u:restricted)
			instance of xs:date`, "true"},
		// NEGATIVE: the member's own facet still decides. "xs:integer" holds a
		// colon, so no NCName accepts it and the QName member is what does --
		// a result that is NOT an xs:NCName.
		{`("xs:integer" cast as u:ncnameOrQName) instance of xs:NCName`, "false"},
		{`("xs:integer" cast as u:ncnameOrQName) instance of xs:QName`, "true"},
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

// TestCastToUnionResolvesQNameMembers is the namespace half of the same rule.
//
// A QName's namespace comes from the static context, and CastAtomic has none:
// it built a QName with no URI, so namespace-uri-from-QName on the result was
// empty and two prefixes for one namespace compared unequal. The bindings that
// apply are the ones in scope where the TYPE NAME was written -- which is why
// the negative row below must fail rather than pick up the caller's binding.
// CastAs-UnionType-10, -11 and -13..15 are the suite's form.
func TestCastToUnionResolvesQNameMembers(t *testing.T) {
	got, err := run(t, unionQuery(
		`namespace-uri-from-QName("xs:integer" cast as u:ncnameOrQName)`), withUnions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "http://www.w3.org/2001/XMLSchema" {
		t.Fatalf("the QName member must carry its namespace, got %q", got)
	}
	// NEGATIVE, and the sharper half: "pre" is bound where the FUNCTION ITEM
	// is applied, not where u:ncnameOrQName was written, so no member accepts
	// the value and the cast owes FORG0001. Resolving against the call site
	// instead would answer "http://example.com/ns" and score as a pass.
	_, err = run(t, unionQuery(`
		declare function local:f($f as function(*)) as item()* {
		  <a xmlns:pre="http://example.com/ns">{$f('pre:local')}</a>
		};
		local:f(u:ncnameOrQName#1)`), withUnions())
	if err == nil {
		t.Fatal("a prefix bound only at the call site must not resolve")
	}
	if !strings.Contains(err.Error(), "FORG0001") {
		t.Fatalf("want FORG0001 for an unresolvable prefix, got %v", err)
	}
}

// TestCastToUnionKeepsAnExistingQName is the case the member loop cannot get
// right on its own. A QName value carries a namespace binding no lexical form
// holds, and the loop takes the FIRST member the value casts to -- so a union
// over xs:NCName and xs:QName handed an actual QName matched the NCName member
// and cast the namespace away, leaving local-name-from-QName with no QName to
// read. CastAs-UnionType-20 and -33 are the suite's form.
func TestCastToUnionKeepsAnExistingQName(t *testing.T) {
	got, err := run(t, unionQuery(
		`local-name-from-QName(u:ncnameOrQName(node-name(<a/>)))`), withUnions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "a" {
		t.Fatalf("an xs:QName operand must stay a QName, got %q", got)
	}
	// NEGATIVE: the shortcut is for a union that HAS a QName member. A union
	// with none must still refuse a QName it cannot convert, rather than
	// passing it through untouched.
	got, err = run(t, unionQuery(
		`node-name(<a/>) castable as u:intOrDate`), withUnions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "false" {
		t.Fatalf("a union with no QName member must refuse one, got %s", got)
	}
}

// TestListCastYieldsInstancesOfTheItemType asserts F&O 3.0 §18.3.6: the result
// of casting to a list type L is "a sequence of zero or more atomic values
// each of which is an instance of the item type of L". The item type is the
// schema's, not a primitive it erases to -- so a list of xs:IDREF owes
// xs:IDREF values, and a list whose item type is a union owes, per §18.3.2,
// an instance of the member that accepted each token.
//
// Both halves had collapsed to xs:string. The cast built each token with the
// item type's erased CODE, which for xs:IDREF is xs:string and for a union is
// nothing at all, so "instance of xs:IDREF+" and "instance of xs:NCName" were
// both false over values the cast had just been asked to produce.
// prod-CastExpr.schema/CastAs-ListType-21 is the suite's union case.
func TestListCastYieldsInstancesOfTheItemType(t *testing.T) {
	for _, c := range []struct {
		body string
		want string
	}{
		// A derived string item type: the facet is applied and recorded.
		{`count("a b c" cast as u:idrefList)`, "3"},
		{`("a b c" cast as u:idrefList) instance of xs:IDREF+`, "true"},
		{`("a b c" cast as u:idrefList) instance of xs:string+`, "true"},
		// A union item type: each item is an instance of the member that
		// accepted it, and so of the union (XPath 3.1 §2.5.5).
		{`count("a b xs:integer" cast as u:ncnameOrQNameList)`, "3"},
		{`("a b xs:integer" cast as u:ncnameOrQNameList)[1] eq "a"`, "true"},
		{`("a b xs:integer" cast as u:ncnameOrQNameList)[1] instance of xs:NCName`, "true"},
		{`("a b xs:integer" cast as u:ncnameOrQNameList)[1] instance of u:ncnameOrQName`, "true"},
		{`("a b xs:integer" cast as u:ncnameOrQNameList) instance of u:ncnameOrQName+`, "true"},
		// The QName member resolves its prefix against the bindings in scope
		// where the type name was written, as a cast to the union does.
		{`("a b xs:integer" cast as u:ncnameOrQNameList)[3] instance of xs:QName`, "true"},
		{`namespace-uri-from-QName(("a b xs:integer" cast as u:ncnameOrQNameList)[3]) eq "http://www.w3.org/2001/XMLSchema"`, "true"},
		// BOUNDARY: a token no member admits fails the whole cast, so
		// "castable as" is false rather than a shorter sequence.
		{`"a b 1c" castable as u:ncnameOrQNameList`, "false"},
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

// TestUnionOfListsCastsThroughTheMemberThatAccepts asserts §18.3.2 for a
// union whose members are LIST types: the members are tried in order, and the
// result is the sequence the accepting member's cast produces (§18.3.6) --
// xs:IDREF values from xs:IDREFS, union-member values from a list of a union.
//
// The union had carried one item CODE for "the" list member, found by asking
// the schema's type table about xs:IDREFS -- a built-in the table does not
// hold -- so it resolved to nothing and the single string came back.
// prod-CastExpr.schema/CastAs-UnionType-27 and -28 are the suite's cases.
func TestUnionOfListsCastsThroughTheMemberThatAccepts(t *testing.T) {
	for _, c := range []struct {
		body string
		want string
	}{
		// The first member, xs:IDREFS, admits plain names.
		{`count("a b c" cast as u:idrefsOrNames)`, "3"},
		{`("a b c" cast as u:idrefsOrNames) instance of xs:IDREF+`, "true"},
		// xs:IDREFS refuses a token with a colon, so the second member takes
		// it, and every item is then an instance of THAT list's item type.
		{`count("a b xs:integer" cast as u:idrefsOrNames)`, "3"},
		{`("a b xs:integer" cast as u:idrefsOrNames) instance of u:ncnameOrQName+`, "true"},
		{`("a b xs:integer" cast as u:idrefsOrNames)[3] instance of xs:QName`, "true"},
		{`("a b xs:integer" cast as u:idrefsOrNames) instance of xs:IDREF+`, "false"},
		// The constructor is the same cast (F&O 3.0 17.5).
		{`count(u:idrefsOrNames("a b xs:integer"))`, "3"},
		// BOUNDARY: a value no member admits.
		{`"a b 1c" castable as u:idrefsOrNames`, "false"},
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
	// The failure is a failed cast, and F&O 3.0 18.3.1 names its code.
	_, err := run(t, unionQuery(`"a b 1c" cast as u:idrefsOrNames`), withUnions())
	if err == nil || !strings.Contains(err.Error(), "FORG0001") {
		t.Fatalf("a value no list member admits must raise FORG0001, got %v", err)
	}
}
