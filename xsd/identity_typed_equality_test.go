package xsd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Identity-constraint equality is defined field-by-field on VALUES, not on
// spellings. XSD 1.0 Part 1 §3.11.4 clause 4 compares key sequences with the
// datatype's equality relation, so for xs:decimal 3.0 and 3 are one value and
// two elements carrying them violate xs:unique.
//
// keyString canonicalises each field before the sequence is joined, so the
// joined string is a value encoding rather than a lexical one. These cases pin
// that property per primitive: they are the ones a lexical comparison gets
// wrong, and every one of them is a VERDICT (valid/invalid), not an internal
// probe.
func typedEqSchema(kind, typ string) string {
	return fmt.Sprintf(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="e" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="v" type="xs:%s"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:%s name="c"><xs:selector xpath="e"/><xs:field xpath="@v"/></xs:%s>
  </xs:element>
</xs:schema>`, typ, kind, kind)
}

func typedEqValidate(t *testing.T, kind, typ string, vals ...string) bool {
	t.Helper()
	st, err := xdm.ParseString(typedEqSchema(kind, typ), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load schema (%s %s): %v", kind, typ, err)
	}
	var sb strings.Builder
	sb.WriteString("<root>")
	for _, v := range vals {
		fmt.Fprintf(&sb, `<e v=%q/>`, v)
	}
	sb.WriteString("</root>")
	tr, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse instance: %v", err)
	}
	return s.Validate(tr.Root, ValidateOptions{}) != nil
}

// TestIdentityTypedEquality asserts, as verdicts, that two lexically distinct
// spellings of one value collide under both unique and key.
func TestIdentityTypedEquality(t *testing.T) {
	for _, tc := range []struct {
		typ, a, b string
		same      bool
	}{
		// The case the audit named.
		{"decimal", "3.0", "3", true},
		{"decimal", "1.0", "1.00", true},
		{"decimal", "+1", "1", true},
		{"decimal", "0", "-0", true},
		{"decimal", "0.0", "-0.0", true},
		{"decimal", "3.0", "3.5", false},
		// integer is a decimal by derivation, so leading zeros are spelling.
		{"integer", "007", "7", true},
		{"integer", "+7", "7", true},
		{"integer", "7", "8", false},
		// boolean has two spellings per value.
		{"boolean", "1", "true", true},
		{"boolean", "0", "false", true},
		{"boolean", "true", "false", false},
		// float and double carry the same rational canonicalisation.
		{"float", "1.0", "1", true},
		{"double", "1.0E1", "10", true},
		{"double", "+INF", "INF", true},
		{"double", "INF", "-INF", false},
	} {
		name := fmt.Sprintf("%s/%s_vs_%s", tc.typ, tc.a, tc.b)
		t.Run(name, func(t *testing.T) {
			for _, kind := range []string{"unique", "key"} {
				got := typedEqValidate(t, kind, tc.typ, tc.a, tc.b)
				if got != tc.same {
					verb := "collide"
					if !tc.same {
						verb = "stay distinct"
					}
					t.Errorf("%s xs:%s: %q and %q should %s: invalid=%v want %v",
						kind, tc.typ, tc.a, tc.b, verb, got, tc.same)
				}
			}
		})
	}
}

// A keyref must resolve against a key written with a different spelling of the
// same value: this is the read direction of the same equality.
func TestIdentityTypedEqualityKeyref(t *testing.T) {
	const schema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="k" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="v" type="xs:decimal"/></xs:complexType>
      </xs:element>
      <xs:element name="r" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="v" type="xs:decimal"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:key name="kk"><xs:selector xpath="k"/><xs:field xpath="@v"/></xs:key>
    <xs:keyref name="rr" refer="kk"><xs:selector xpath="r"/><xs:field xpath="@v"/></xs:keyref>
  </xs:element>
</xs:schema>`
	st, err := xdm.ParseString(schema, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, tc := range []struct {
		key, ref string
		invalid  bool
	}{
		{"5", "5.0", false},
		{"5.0", "5", false},
		{"05.00", "5", false},
		{"5", "6", true},
	} {
		doc := fmt.Sprintf(`<root><k v=%q/><r v=%q/></root>`, tc.key, tc.ref)
		tr, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		if got := s.Validate(tr.Root, ValidateOptions{}) != nil; got != tc.invalid {
			t.Errorf("key %q ref %q: invalid=%v want %v", tc.key, tc.ref, got, tc.invalid)
		}
	}
}

// Values drawn from different primitives are never equal however their
// spellings compare: the boolean 1 is not the decimal 1. The primitive tag
// keyString prepends is what carries this, so a change to the encoding that
// dropped the tag would show here.
func TestIdentityDistinctPrimitivesNeverEqual(t *testing.T) {
	const schema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:simpleType name="bd">
    <xs:union memberTypes="xs:boolean xs:decimal"/>
  </xs:simpleType>
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="e" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="v" type="bd"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:unique name="c"><xs:selector xpath="e"/><xs:field xpath="@v"/></xs:unique>
  </xs:element>
</xs:schema>`
	st, err := xdm.ParseString(schema, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// A union resolves "1" to its first viable member, boolean; "1.5" is a
	// decimal. The pair must not collide, and neither must boolean 1 with a
	// decimal that also spells 1 in another element -- but a union picks
	// boolean for both, so the observable case is boolean/decimal mixing.
	for _, tc := range []struct {
		a, b    string
		invalid bool
	}{
		{"1", "true", true},   // both boolean, one value
		{"1", "1.5", false},   // boolean 1 vs decimal 1.5
		{"1.5", "1.50", true}, // both decimal, one value
	} {
		doc := fmt.Sprintf(`<root><e v=%q/><e v=%q/></root>`, tc.a, tc.b)
		tr, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got := s.Validate(tr.Root, ValidateOptions{}) != nil; got != tc.invalid {
			t.Errorf("%q vs %q: invalid=%v want %v", tc.a, tc.b, got, tc.invalid)
		}
	}
}

// The key sequence is joined on U+001F, so a field value containing that byte
// could in principle forge a field boundary and make ("a\x1fb", "c") collide
// with ("a", "b\x1fc").
//
// It is unreachable today: U+001F is not a legal XML 1.0 character, so the
// parser refuses the document before a key is ever built. This pins that
// premise rather than asserting it -- the separator's safety is a property of
// the parser, not of identity.go, and if XML 1.1 support ever makes such a
// document parse, this test fails and says what has to change. docs/todo.md
// records the same dependency.
func TestIdentityKeySeparatorUnreachableInXML10(t *testing.T) {
	for _, doc := range []string{
		"<root><e v=\"a\x1fb\"/><e v=\"c\"/></root>",
		"<root><e v=\"a\"/><e v=\"b\x1fc\"/></root>",
		"<root><e>a\x1fb</e></root>",
	} {
		if _, err := xdm.ParseString(doc, xdm.ParseOptions{}); err == nil {
			t.Errorf("U+001F parsed as XML 1.0 character data: the key separator "+
				"in identity.go is no longer unreachable and the joined key "+
				"sequence must become a tuple (see docs/todo.md)\n%q", doc)
		}
	}
}
