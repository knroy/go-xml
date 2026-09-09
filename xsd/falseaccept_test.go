package xsd

import (
	"os"
	"path/filepath"
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

// TestTypeRefNamingAnAttributeDecl covers MS-Element elemM002.
//
// <xsd:element name="myElem" type="foo"/> beside <xsd:attribute name="foo"/>.
// §3.3.2 requires type= to resolve to a *type definition*, and this one
// resolves to a component of the wrong kind.
//
// The deferral §3.3.3 grants an element declaration is what hid this: an
// unprefixed type= in a schema with no targetNamespace names the absent
// namespace, deferrableMiss answers true for it, and the reference was carried
// on the declaration instead of reported. The deferral exists because a
// document read later might supply the type; no document can turn an attribute
// declaration into one, so here there is nothing to wait for.
func TestTypeRefNamingAnAttributeDecl(t *testing.T) {
	const bad = `<xsd:schema xmlns:xsd='http://www.w3.org/2001/XMLSchema'>
  <xsd:element name='myElem' type='foo'/>
  <xsd:attribute name='foo'/>
</xsd:schema>`
	err := mustLoad(t, bad, Version10)
	if err == nil {
		t.Fatal("type= naming an attribute declaration was accepted")
	}
	if !strings.Contains(err.Error(), "not a type definition") {
		t.Errorf("rejected for the wrong reason: %v", err)
	}

	// The negative arm, which is the whole point of the pair. saxonData
	// Missing/missing001 writes type="absent" into the same absent
	// namespace of a schema that declares components there -- identical to
	// the case above in every respect deferrableMiss can see -- but
	// "absent" names *nothing at all*, so the deferral still applies and
	// the schema is expected to load. A check that rejects this one too
	// has not fixed the rule, it has broken the deferral.
	const good = `<xsd:schema xmlns:xsd='http://www.w3.org/2001/XMLSchema'>
  <xsd:element name='good' type='xsd:integer'/>
  <xsd:element name='bad' type='absent'/>
</xsd:schema>`
	if err := mustLoad(t, good, Version10); err != nil {
		t.Errorf("a genuinely missing type is deferrable and must load: %v", err)
	}

	// A name that resolves to a type is unaffected, including one declared
	// after the reference that names it.
	const fwd = `<xsd:schema xmlns:xsd='http://www.w3.org/2001/XMLSchema'>
  <xsd:element name='myElem' type='foo'/>
  <xsd:simpleType name='foo'>
    <xsd:restriction base='xsd:string'/>
  </xsd:simpleType>
</xsd:schema>`
	if err := mustLoad(t, fwd, Version10); err != nil {
		t.Errorf("a forward reference to a real type must resolve: %v", err)
	}
}

// TestKeyrefReferAcrossAnUnimportedNamespace covers MS-IdentityConstraint
// idC019.
//
// schema.identityConstraints is one flat map over the whole assembly, so a
// refer= lookup that ignores which document asked can reach a key the asking
// document has no licence to see. §4.2.6.1 src-resolve scopes the licence an
// <xs:import> grants to the document that wrote it.
//
// idC019 imports idC017a.xsd, whose targetNamespace is "diffNS" and whose
// keyref writes refer="keyName" *unprefixed* with no default namespace in
// scope -- so §3.11.2 resolves it to the absent namespace, not to diffNS.
// Nothing in diffNS declares that key. The match came from the importing
// document's own absent namespace, which idC017a.xsd never imports.
//
// resolveQName's own import check cannot catch this: an unprefixed name with
// no default namespace in scope returns early through chameleonQName, before
// the checkReferenceImported switch is reached.
func TestKeyrefReferAcrossAnUnimportedNamespace(t *testing.T) {
	// other.xsd is idC017a.xsd: it is in diffNS, imports nothing, and
	// refers to a key that only the importing document declares.
	const other = `<xsd:schema xmlns:xsd='http://www.w3.org/2001/XMLSchema'
	    xmlns:otherNS='diffNS' targetNamespace='diffNS'>
  <xsd:element name='otherSubRoot'>
    <xsd:complexType>
      <xsd:sequence><xsd:element ref='otherNS:otherElement'/></xsd:sequence>
    </xsd:complexType>
    <xsd:keyref name='keyrefName' refer='keyName'>
      <xsd:selector xpath='.//otherElement'/>
      <xsd:field xpath='@keyData'/>
    </xsd:keyref>
  </xsd:element>
  <xsd:element name='otherElement'>
    <xsd:complexType>
      <xsd:attribute name='keyData' type='xsd:string'/>
    </xsd:complexType>
  </xsd:element>
</xsd:schema>`
	const main = `<xsd:schema xmlns:xsd='http://www.w3.org/2001/XMLSchema'>
  <xsd:import namespace='diffNS' schemaLocation='other.xsd'/>
  <xsd:element name='root'>
    <xsd:complexType>
      <xsd:sequence><xsd:element ref='keyElement'/></xsd:sequence>
    </xsd:complexType>
    <xsd:key name='keyName'>
      <xsd:selector xpath='.//keyElement'/>
      <xsd:field xpath='@keyField'/>
    </xsd:key>
  </xsd:element>
  <xsd:element name='keyElement'>
    <xsd:complexType>
      <xsd:attribute name='keyField' type='xsd:string'/>
    </xsd:complexType>
  </xsd:element>
</xsd:schema>`

	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("other.xsd", other)
	mainPath := write("main.xsd", main)

	_, err := LoadFiles([]string{mainPath}, Options{Resolver: &FileResolver{}})
	if err == nil {
		t.Fatal("a keyref reached a key its document never imported")
	}
	if !strings.Contains(err.Error(), "names no key or unique constraint") {
		t.Errorf("rejected for the wrong reason: %v", err)
	}
}

// The negative arm for the rule above, kept separate because it is the case
// that decides whether the check is scoped correctly rather than merely
// strict. A keyref whose refer= names a key in its *own* target namespace
// must still resolve -- that is the ordinary shape, and by far the common one.
// So must one that names a key in a namespace the document does import.
func TestKeyrefReferWithinReachIsUnaffected(t *testing.T) {
	// Same target namespace, one document: the overwhelmingly common case.
	const own = `<xsd:schema xmlns:xsd='http://www.w3.org/2001/XMLSchema'
	    xmlns:t='urn:t' targetNamespace='urn:t'>
  <xsd:element name='root'>
    <xsd:complexType>
      <xsd:sequence>
        <xsd:element ref='t:keyElement'/>
        <xsd:element ref='t:keyrefElement'/>
      </xsd:sequence>
    </xsd:complexType>
    <xsd:key name='keyName'>
      <xsd:selector xpath='.//t:keyElement'/>
      <xsd:field xpath='@keyField'/>
    </xsd:key>
    <xsd:keyref name='keyrefName' refer='t:keyName'>
      <xsd:selector xpath='.//t:keyrefElement'/>
      <xsd:field xpath='@keyrefField'/>
    </xsd:keyref>
  </xsd:element>
  <xsd:element name='keyElement'>
    <xsd:complexType>
      <xsd:attribute name='keyField' type='xsd:string'/>
    </xsd:complexType>
  </xsd:element>
  <xsd:element name='keyrefElement'>
    <xsd:complexType>
      <xsd:attribute name='keyrefField' type='xsd:string'/>
    </xsd:complexType>
  </xsd:element>
</xsd:schema>`
	if err := mustLoad(t, own, Version10); err != nil {
		t.Errorf("a keyref naming a key in its own namespace must resolve: %v", err)
	}

	// And across a namespace the referring document *does* import: the
	// licence is present, so the reference is in reach.
	const lib = `<xsd:schema xmlns:xsd='http://www.w3.org/2001/XMLSchema'
	    xmlns:k='urn:k' targetNamespace='urn:k'>
  <xsd:element name='keyHolder'>
    <xsd:complexType>
      <xsd:sequence><xsd:element ref='k:keyElement'/></xsd:sequence>
    </xsd:complexType>
    <xsd:key name='keyName'>
      <xsd:selector xpath='.//k:keyElement'/>
      <xsd:field xpath='@keyField'/>
    </xsd:key>
  </xsd:element>
  <xsd:element name='keyElement'>
    <xsd:complexType>
      <xsd:attribute name='keyField' type='xsd:string'/>
    </xsd:complexType>
  </xsd:element>
</xsd:schema>`
	const user = `<xsd:schema xmlns:xsd='http://www.w3.org/2001/XMLSchema'
	    xmlns:k='urn:k' xmlns:u='urn:u' targetNamespace='urn:u'>
  <xsd:import namespace='urn:k' schemaLocation='lib.xsd'/>
  <xsd:element name='root'>
    <xsd:complexType>
      <xsd:sequence><xsd:element ref='u:refElement'/></xsd:sequence>
    </xsd:complexType>
    <xsd:keyref name='keyrefName' refer='k:keyName'>
      <xsd:selector xpath='.//u:refElement'/>
      <xsd:field xpath='@keyrefField'/>
    </xsd:keyref>
  </xsd:element>
  <xsd:element name='refElement'>
    <xsd:complexType>
      <xsd:attribute name='keyrefField' type='xsd:string'/>
    </xsd:complexType>
  </xsd:element>
</xsd:schema>`

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.xsd"), []byte(lib), 0o644); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(dir, "user.xsd")
	if err := os.WriteFile(userPath, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFiles([]string{userPath},
		Options{Resolver: &FileResolver{}}); err != nil {
		t.Errorf("a keyref naming a key in an imported namespace must resolve: %v", err)
	}
}
