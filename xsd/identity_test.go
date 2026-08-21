package xsd

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

const keySchema = `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType>
      <xs:sequence>
        <xs:element name="item" maxOccurs="unbounded">
          <xs:complexType>
            <xs:attribute name="id" type="xs:string"/>
            <xs:attribute name="ref" type="xs:string"/>
          </xs:complexType>
        </xs:element>
      </xs:sequence>
    </xs:complexType>
    <xs:key name="itemKey">
      <xs:selector xpath=".//item"/>
      <xs:field xpath="@id"/>
    </xs:key>
    <xs:keyref name="itemRef" refer="itemKey">
      <xs:selector xpath=".//item"/>
      <xs:field xpath="@ref"/>
    </xs:keyref>
  </xs:element>
</xs:schema>`

func TestIdentityKeyUniqueness(t *testing.T) {
	assertValid(t, keySchema, `<root><item id="a"/><item id="b"/></root>`)
	assertInvalid(t, keySchema, `<root><item id="a"/><item id="a"/></root>`,
		"cvc-identity-constraint")
}

func TestIdentityKeyRequiresField(t *testing.T) {
	// Every target of a key must be qualified: a missing field is a
	// failure, unlike for unique.
	assertInvalid(t, keySchema, `<root><item id="a"/><item/></root>`,
		"cvc-identity-constraint.4.2.1")
}

func TestIdentityKeyref(t *testing.T) {
	assertValid(t, keySchema, `<root><item id="a"/><item id="b" ref="a"/></root>`)
	assertInvalid(t, keySchema, `<root><item id="a" ref="nope"/></root>`,
		"cvc-identity-constraint.4.3")
}

const uniqueSchema = `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType>
      <xs:sequence>
        <xs:element name="item" maxOccurs="unbounded">
          <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
        </xs:element>
      </xs:sequence>
    </xs:complexType>
    <xs:unique name="itemUnique">
      <xs:selector xpath=".//item"/>
      <xs:field xpath="@id"/>
    </xs:unique>
  </xs:element>
</xs:schema>`

// TestIdentityUniquePermitsAbsentField covers the difference between unique and
// key: a target whose field selects nothing simply leaves the qualified node
// set, rather than failing.
func TestIdentityUniquePermitsAbsentField(t *testing.T) {
	assertValid(t, uniqueSchema, `<root><item id="a"/><item/><item/></root>`)
	assertInvalid(t, uniqueSchema, `<root><item id="a"/><item id="a"/></root>`,
		"cvc-identity-constraint")
}

// TestIdentityKeyrefIsSubtreeScoped is the rule most implementations get wrong.
//
// Node tables are assembled strictly recursively from the tables of
// descendants, so a keyref resolves only against keys defined *within the
// subtree* of the element carrying it. A key in a sibling subtree is not in
// scope, and implementing keyref as a document-wide map would wrongly accept
// this document.
func TestIdentityKeyrefIsSubtreeScoped(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="section" maxOccurs="unbounded">
	          <xs:complexType>
	            <xs:sequence>
	              <xs:element name="item" maxOccurs="unbounded">
	                <xs:complexType>
	                  <xs:attribute name="id" type="xs:string"/>
	                  <xs:attribute name="ref" type="xs:string"/>
	                </xs:complexType>
	              </xs:element>
	            </xs:sequence>
	          </xs:complexType>
	          <xs:key name="k">
	            <xs:selector xpath=".//item"/>
	            <xs:field xpath="@id"/>
	          </xs:key>
	          <xs:keyref name="kr" refer="k">
	            <xs:selector xpath=".//item"/>
	            <xs:field xpath="@ref"/>
	          </xs:keyref>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	// Every item carries an id, because a key requires its whole target
	// node set to be qualified — clause 4.2.1 — and an item without one
	// would fail for that reason rather than for scoping.
	//
	// A reference inside the same section resolves.
	assertValid(t, schema, `
	<root><section><item id="a"/><item id="b" ref="a"/></section></root>`)

	// A reference to an id in a *different* section must not resolve: the
	// key is scoped to its own section's subtree, and a document-wide map
	// would wrongly accept this.
	assertInvalid(t, schema, `
	<root>
	  <section><item id="a"/></section>
	  <section><item id="b" ref="a"/></section>
	</root>`, "cvc-identity-constraint.4.3")
}

// TestIdentityCompositeKey covers a key of more than one field, and the reason
// the fields are joined on a separator that cannot appear in a value.
func TestIdentityCompositeKey(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="item" maxOccurs="unbounded">
	          <xs:complexType>
	            <xs:attribute name="a" type="xs:string"/>
	            <xs:attribute name="b" type="xs:string"/>
	          </xs:complexType>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	    <xs:key name="k">
	      <xs:selector xpath=".//item"/>
	      <xs:field xpath="@a"/>
	      <xs:field xpath="@b"/>
	    </xs:key>
	  </xs:element>
	</xs:schema>`

	assertValid(t, schema, `<root><item a="x" b="y"/><item a="x" b="z"/></root>`)
	assertInvalid(t, schema, `<root><item a="x" b="y"/><item a="x" b="y"/></root>`,
		"cvc-identity-constraint")

	// ("x y", "") and ("x", "y") must not collide. Joining on a space
	// would make them the same key and wrongly reject this.
	assertValid(t, schema, `<root><item a="x y" b=""/><item a="x" b="y"/></root>`)
}

// TestIdentitySelectorElementField covers a field selecting an element's text
// rather than an attribute.
func TestIdentitySelectorElementField(t *testing.T) {
	schema := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:sequence>
	        <xs:element name="item" maxOccurs="unbounded">
	          <xs:complexType>
	            <xs:sequence><xs:element name="name" type="xs:string"/></xs:sequence>
	          </xs:complexType>
	        </xs:element>
	      </xs:sequence>
	    </xs:complexType>
	    <xs:key name="k">
	      <xs:selector xpath=".//item"/>
	      <xs:field xpath="name"/>
	    </xs:key>
	  </xs:element>
	</xs:schema>`

	assertValid(t, schema,
		`<root><item><name>a</name></item><item><name>b</name></item></root>`)
	assertInvalid(t, schema,
		`<root><item><name>a</name></item><item><name>a</name></item></root>`,
		"cvc-identity-constraint")
}

// TestIdentityQNameKeyComparesResolved covers the one primitive whose value is
// not its spelling.
//
// A QName's value is a namespace URI and a local name; the prefix is only a way
// of writing the URI. Two prefixes bound to the same namespace therefore denote
// the same value, and a keyref written with one must find a key written with the
// other. sunData IdentityTestSuite/002 test.2.v pins this pair, and comparing
// the lexical form instead rejects it.
func TestIdentityQNameKeyComparesResolved(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:tn="foo"
	           targetNamespace="foo" elementFormDefault="qualified">
	  <xs:element name="root">
	    <xs:complexType>
	      <xs:choice maxOccurs="unbounded">
	        <xs:element name="key" type="xs:QName"/>
	        <xs:element name="ref" type="xs:QName"/>
	      </xs:choice>
	    </xs:complexType>
	    <xs:key name="k">
	      <xs:selector xpath=".//tn:key"/>
	      <xs:field xpath="."/>
	    </xs:key>
	    <xs:keyref name="kr" refer="tn:k">
	      <xs:selector xpath=".//tn:ref"/>
	      <xs:field xpath="."/>
	    </xs:keyref>
	  </xs:element>
	</xs:schema>`

	// p and q are two prefixes for one namespace, so p:abc and q:abc are the
	// same QName value and the keyref resolves.
	assertValid(t, schema, `<tn:root xmlns:tn="foo" xmlns:p="abc" xmlns:q="abc">`+
		`<tn:key>p:abc</tn:key><tn:ref>q:abc</tn:ref></tn:root>`)

	// The same local name in a different namespace is a different value.
	assertInvalid(t, schema, `<tn:root xmlns:tn="foo" xmlns:p="abc" xmlns:q="xyz">`+
		`<tn:key>p:abc</tn:key><tn:ref>q:abc</tn:ref></tn:root>`,
		"cvc-identity-constraint.4.3")

	// Two spellings of one value are a duplicate key, not two keys.
	assertInvalid(t, schema, `<tn:root xmlns:tn="foo" xmlns:p="abc" xmlns:q="abc">`+
		`<tn:key>p:abc</tn:key><tn:key>q:abc</tn:key></tn:root>`,
		"cvc-identity-constraint")
}

// TestIdentityConstraintNameMustBeAnNCName covers the `name = NCName` in the
// representation summary for key, keyref and unique. A prefixed name is not a
// constraint in some namespace — a constraint's namespace comes from the
// document's targetNamespace — it is a malformed name.
// MS-IdentityConstraint idA030 writes "a:b" and idA032 writes "1foo".
func TestIdentityConstraintNameMustBeAnNCName(t *testing.T) {
	for _, name := range []string{"a:b", "1foo", "", ":x"} {
		src := `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:a="myNamespace">
		  <xsd:element name="root">
		    <xsd:complexType>
		      <xsd:sequence><xsd:element name="i" type="xsd:string"/></xsd:sequence>
		    </xsd:complexType>
		    <xsd:unique name="` + name + `">
		      <xsd:selector xpath=".//i"/>
		      <xsd:field xpath="."/>
		    </xsd:unique>
		  </xsd:element>
		</xsd:schema>`
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Load(tree.Root, "s.xsd", Options{}); err == nil {
			t.Errorf("constraint name %q is not an NCName and should be refused", name)
		}
	}
}

// TestIdentityConstraintPathPrefixMustBeBound covers src-resolve for the
// selector and field XPaths. An unbound prefix silently resolved to the absent
// namespace, where the step matched nothing and the schema still loaded.
// MS-IdentityConstraint idI010 puts the unbound prefix in the selector and
// idJ011 in the field.
func TestIdentityConstraintPathPrefixMustBeBound(t *testing.T) {
	for _, c := range []struct{ selector, field string }{
		{"imp:iid", "@val"},
		{".//tid", "imp:iid"},
	} {
		src := `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema">
		  <xsd:element name="root">
		    <xsd:complexType>
		      <xsd:sequence><xsd:element name="tid" type="xsd:string"/></xsd:sequence>
		    </xsd:complexType>
		    <xsd:unique name="uid">
		      <xsd:selector xpath="` + c.selector + `"/>
		      <xsd:field xpath="` + c.field + `"/>
		    </xsd:unique>
		  </xsd:element>
		</xsd:schema>`
		tree, err := xdm.ParseString(src, xdm.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Load(tree.Root, "s.xsd", Options{}); err == nil {
			t.Errorf("selector=%q field=%q uses an unbound prefix and should be refused",
				c.selector, c.field)
		}
	}

	// A bound prefix is fine, which is what keeps the check from refusing
	// every namespace-aware constraint.
	const ok = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"
	            xmlns:t="urn:t" targetNamespace="urn:t" elementFormDefault="qualified">
	  <xsd:element name="root">
	    <xsd:complexType>
	      <xsd:sequence><xsd:element name="tid" type="xsd:string"/></xsd:sequence>
	    </xsd:complexType>
	    <xsd:unique name="uid">
	      <xsd:selector xpath=".//t:tid"/>
	      <xsd:field xpath="."/>
	    </xsd:unique>
	  </xsd:element>
	</xsd:schema>`
	tree, err := xdm.ParseString(ok, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(tree.Root, "s.xsd", Options{}); err != nil {
		t.Errorf("a bound prefix should load:\n%v", err)
	}
}
