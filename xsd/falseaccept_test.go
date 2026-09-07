package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Invalid schemas this package used to load without complaint.
//
// Each case here was a false accept measured against the W3C suite, and each
// pairs with a schema that is *valid* for the same rule. The pairs are the
// point: a rule that rejects the invalid one and the valid one with it has
// not fixed anything, it has moved the error to the other direction, where it
// breaks working schemas instead of admitting broken ones.

// mustLoad is the assembly-time counterpart to mustParseSchema. The
// substitution-group closure and the facet checks both run in Load rather than
// ParseSchema, so a test for either has to go through here.
func mustLoad(t *testing.T, src string, v Version) error {
	t.Helper()
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test schema as XML: %v", err)
	}
	_, err = Load(doc.Root, "", Options{Version: v})
	return err
}

// TestBase64BinaryPaddingLexicalSpace covers base64Binary_enumeration003.
//
// Part 2 §3.2.16 spells the base64Binary grammar out quantum by quantum: a
// final quantum written "B16 B64 B16 =" must end in one of the sixteen
// characters whose low two bits are zero, because the "=" declares those bits
// absent. "M0SyLMT=" ends in "T", which is index 19, so the bits it declares
// absent were written as 11.
//
// encoding/base64 accepts that string, which is why the gap existed: Go
// discards the surplus bits rather than insisting they were zero. XSD insists.
func TestBase64BinaryPaddingLexicalSpace(t *testing.T) {
	const schema = `<xsd:schema xmlns:xsd='http://www.w3.org/2001/XMLSchema'>
  <xsd:element name='test'>
    <xsd:simpleType>
      <xsd:restriction base="xsd:base64Binary">
        <xsd:enumeration value="M0SyLMT="/>
      </xsd:restriction>
    </xsd:simpleType>
  </xsd:element>
</xsd:schema>`

	for _, v := range []Version{Version10, Version11} {
		err := mustLoad(t, schema, v)
		if err == nil {
			t.Errorf("version %v: a schema whose xs:enumeration value "+
				`"M0SyLMT=" is outside the base64Binary lexical space `+
				"was accepted", v)
		}
	}
}

// TestBase64BinaryLexicalSpaceAccepts is the other half of the pair.
//
// Every value here is one the suite labels valid, and three of them are the
// siblings of the case above -- base64Binary_enumeration003's own two legal
// values and the "abc=" of base64Binary_enumeration003_257, which the suite
// expects to load. The wrapped literal is the one that matters most in
// practice: base64Binary's whiteSpace facet is collapse, and MIME-style base64
// arrives from real encoders broken across lines.
func TestBase64BinaryLexicalSpaceAccepts(t *testing.T) {
	for _, value := range []string{
		"MS0yLTM=", // ends in M, index 12, low two bits zero
		"MyS0LTM=",
		"abc=",
		"YWJjZA==",    // two-padding quantum, "d" is in B04
		"",            // the empty literal encodes zero octets
		"YWJj",        // no padding at all
		"MS0y\n LTM=", // wrapped and indented, as a MIME encoder emits it
	} {
		schema := `<xsd:schema xmlns:xsd='http://www.w3.org/2001/XMLSchema'>
  <xsd:element name='test'>
    <xsd:simpleType>
      <xsd:restriction base="xsd:base64Binary">
        <xsd:enumeration value="` + value + `"/>
      </xsd:restriction>
    </xsd:simpleType>
  </xsd:element>
</xsd:schema>`
		for _, v := range []Version{Version10, Version11} {
			if err := mustLoad(t, schema, v); err != nil {
				t.Errorf("version %v: xs:enumeration value %q is in the "+
					"base64Binary lexical space but was rejected: %v",
					v, value, err)
			}
		}
	}
}

// TestSubstitutionBlockedSeversTheChain covers elemZ027_c.
//
// a→b→c→d, with block="substitution" on the intermediate b. Nothing may
// substitute for b, so a's only route to d is severed and a is not in d's
// substitution group. The restriction of `base` then replaces ref="d" with a
// choice over ref="a", which no longer has a corresponding particle.
//
// The suite states the rule in as many words: "no substitutionGroup members
// should be added if head element has block=substitution".
func TestSubstitutionBlockedSeversTheChain(t *testing.T) {
	const schema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="a" substitutionGroup="b" type="xs:anyType"/>
  <xs:element name="b" substitutionGroup="c" type="xs:anyType" block="substitution"/>
  <xs:element name="c" substitutionGroup="d" type="xs:anyType"/>
  <xs:element name="d" type="xs:anyType"/>
  <xs:complexType name="base">
    <xs:sequence><xs:element ref="d"/></xs:sequence>
  </xs:complexType>
  <xs:complexType name="derived">
    <xs:complexContent>
      <xs:restriction base="base">
        <xs:sequence><xs:choice><xs:element ref="a"/></xs:choice></xs:sequence>
      </xs:restriction>
    </xs:complexContent>
  </xs:complexType>
  <xs:element name="doc" type="base"/>
</xs:schema>`

	for _, v := range []Version{Version10, Version11} {
		err := mustLoad(t, schema, v)
		if err == nil {
			t.Errorf("version %v: a restriction substituting an element whose "+
				`only route to the head runs through a block="substitution" `+
				"member was accepted", v)
		}
	}
}

// TestSubstitutionChainUnblockedStillWalks is the other half of that pair, and
// the reason the prune is written for DerivationSubstitution alone.
//
// The same four elements with the block removed: a does reach d, so the same
// restriction is legal and the schema must load. If the prune were written for
// any blocked member rather than this one kind, this would start failing.
func TestSubstitutionChainUnblockedStillWalks(t *testing.T) {
	const schema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="a" substitutionGroup="b" type="xs:anyType"/>
  <xs:element name="b" substitutionGroup="c" type="xs:anyType"/>
  <xs:element name="c" substitutionGroup="d" type="xs:anyType"/>
  <xs:element name="d" type="xs:anyType"/>
  <xs:complexType name="base">
    <xs:sequence><xs:element ref="d"/></xs:sequence>
  </xs:complexType>
  <xs:complexType name="derived">
    <xs:complexContent>
      <xs:restriction base="base">
        <xs:sequence><xs:choice><xs:element ref="a"/></xs:choice></xs:sequence>
      </xs:restriction>
    </xs:complexContent>
  </xs:complexType>
  <xs:element name="doc" type="base"/>
</xs:schema>`

	for _, v := range []Version{Version10, Version11} {
		if err := mustLoad(t, schema, v); err != nil {
			t.Errorf("version %v: a transitively reachable substitution group "+
				"member was refused: %v", v, err)
		}
	}
}

// TestSubstitutionMethodBlockDoesNotSeverTheChain pins the distinction the
// prune turns on, in the direction that would over-reject.
//
// block="extension" on the intermediate keeps that intermediate out of the
// head's group, but says nothing about what stands in for it: a member behind
// it may still reach the head by a method the block permits. Severing the walk
// for a method block -- rather than for substitution -- would lose those, so
// this asserts the walk continues.
func TestSubstitutionMethodBlockDoesNotSeverTheChain(t *testing.T) {
	const schema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:complexType name="dT"/>
  <xs:complexType name="cT">
    <xs:complexContent><xs:restriction base="dT"/></xs:complexContent>
  </xs:complexType>
  <xs:complexType name="bT">
    <xs:complexContent><xs:restriction base="cT"/></xs:complexContent>
  </xs:complexType>
  <xs:element name="d" type="dT"/>
  <xs:element name="c" type="cT" substitutionGroup="d" block="extension"/>
  <xs:element name="b" type="bT" substitutionGroup="c"/>
</xs:schema>`

	doc, err := xdm.ParseString(schema, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test schema as XML: %v", err)
	}
	s, err := Load(doc.Root, "", Options{Version: Version10})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	head := s.Elements[xdm.QName{Local: "d"}]
	if head == nil {
		t.Fatal("element d was not declared")
	}
	var names []string
	for _, m := range head.Substitutable() {
		names = append(names, m.Name.Local)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "b") {
		t.Errorf(`block="extension" on an intermediate severed the walk: d's `+
			"substitution group is [%s], and b should still reach it", joined)
	}
}

// TestUndeclaredSchemaNamespaceTypeIsAnError covers xsd015.e and xsd016.e.
//
// Both write type="abc" in a document whose default xmlns is the schema
// namespace, so the unprefixed QName resolves to {XMLSchema}abc. §3.3.3 lets a
// missing type be deferred to the point of use, but only where the namespace
// might still be supplied. The schema namespace never can be: its types are
// built in process, and no document adds to it.
//
// xsd015 is the sharper of the two -- it declares complexType "abc" in its own
// target namespace, and the test comment says what it is testing: "don't be
// fooled! it's not foo:abc".
func TestUndeclaredSchemaNamespaceTypeIsAnError(t *testing.T) {
	const withDecoy = `<schema xmlns="http://www.w3.org/2001/XMLSchema"
    xmlns:foo="foo" targetNamespace="foo" elementFormDefault="qualified">
  <complexType name="abc"><sequence><any/></sequence></complexType>
  <element name="root" type="abc"/>
</schema>`
	const bare = `<schema xmlns="http://www.w3.org/2001/XMLSchema"
    xmlns:foo="foo" targetNamespace="foo" elementFormDefault="qualified">
  <element name="root" type="abc"/>
</schema>`

	for name, src := range map[string]string{
		"a same-named type in the target namespace": withDecoy,
		"no such type anywhere":                     bare,
	} {
		for _, v := range []Version{Version10, Version11} {
			if err := mustLoad(t, src, v); err == nil {
				t.Errorf("version %v: type=\"abc\" resolves to {XMLSchema}abc, "+
					"which is not a builtin, and the schema was accepted (%s)",
					v, name)
			}
		}
	}
}

// TestBuiltinAndPrefixedTypesStillResolve is the other half of that pair.
//
// The rule must fall only on names the schema namespace does not define. A
// builtin named through the same default xmlns, and a type in the document's
// own target namespace named with a prefix, both still resolve.
func TestBuiltinAndPrefixedTypesStillResolve(t *testing.T) {
	const src = `<schema xmlns="http://www.w3.org/2001/XMLSchema"
    xmlns:foo="foo" targetNamespace="foo" elementFormDefault="qualified">
  <complexType name="abc"><sequence><any/></sequence></complexType>
  <element name="builtin" type="string"/>
  <element name="own" type="foo:abc"/>
</schema>`

	for _, v := range []Version{Version10, Version11} {
		if err := mustLoad(t, src, v); err != nil {
			t.Errorf("version %v: a builtin named through the default xmlns and "+
				"a prefixed reference to the document's own type must both "+
				"resolve: %v", v, err)
		}
	}
}
