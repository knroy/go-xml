package xsd

import "testing"

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
