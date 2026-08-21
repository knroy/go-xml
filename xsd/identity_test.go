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

// TestFieldAttributeWildcardHonoursPrefix separates the two grammatical
// attribute wildcards a field may use. "@*" selects every attribute, so an
// element carrying more than one fails Identity-constraint Satisfied clause 3.
// "@p:*" is the narrower form and selects only the attributes in p's
// namespace, so a second attribute from another namespace is no obstacle.
//
// idL102 pins the difference: its field is "@myNS:*" over elements that also
// carry xsi:nil, and treating the prefixed form as a bare "@*" selected two
// nodes and rejected a document whose key holds.
func TestFieldAttributeWildcardHonoursPrefix(t *testing.T) {
	schema := func(field string) string {
		return `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
		           xmlns:t="urn:t" targetNamespace="urn:t"
		           elementFormDefault="qualified"
		           attributeFormDefault="qualified">
		  <xs:element name="root">
		    <xs:complexType>
		      <xs:sequence>
		        <xs:element name="item" maxOccurs="unbounded" nillable="true">
		          <xs:complexType>
		            <xs:attribute name="id" type="xs:string"/>
		          </xs:complexType>
		        </xs:element>
		      </xs:sequence>
		    </xs:complexType>
		    <xs:key name="k">
		      <xs:selector xpath=".//t:item"/>
		      <xs:field xpath="` + field + `"/>
		    </xs:key>
		  </xs:element>
		</xs:schema>`
	}
	const doc = `
	<t:root xmlns:t="urn:t" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
	  <t:item t:id="1" xsi:nil="true"/>
	  <t:item t:id="2" xsi:nil="true"/>
	</t:root>`

	// xsi:nil is not in urn:t, so "@t:*" selects exactly the id attribute.
	assertValid(t, schema("@t:*"), doc)

	// The unprefixed wildcard selects xsi:nil too, which is two nodes.
	assertInvalid(t, schema("@*"), doc, "cvc-identity-constraint.3")
}

// TestPrimitivesDeriveFromAnyAtomicType covers Part 2 §3.4.1: xs:anyAtomicType
// is the {base type definition} of all 19 primitives, sitting between
// xs:anySimpleType and them.
//
// It was defined but never inserted into the chain — created after the
// primitives, which kept anySimpleType as their base. Nothing named it, so the
// gap was invisible until an element declared xs:anyAtomicType met an
// xsi:type: simple050 does exactly that, and xsi:type="xs:date" was refused as
// a type not derived from the declaration.
func TestPrimitivesDeriveFromAnyAtomicType(t *testing.T) {
	const schema = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="e" type="xs:anyAtomicType"/>
	</xs:schema>`
	const ns = ` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"` +
		` xmlns:xs="http://www.w3.org/2001/XMLSchema"`

	// A primitive and a type restricted from one both substitute.
	assertValid(t, schema, `<e`+ns+` xsi:type="xs:date">2010-11-10</e>`)
	assertValid(t, schema, `<e`+ns+` xsi:type="xs:int">42</e>`)

	// The value still has to hold against the named type.
	assertInvalid(t, schema, `<e`+ns+` xsi:type="xs:date">not-a-date</e>`,
		"cvc-datatype-valid")

	// A list type is not atomic, so it does not derive from anyAtomicType.
	assertInvalid(t, schema, `<e`+ns+` xsi:type="xs:NMTOKENS">a b</e>`,
		"cvc-elt.4.3")
}
