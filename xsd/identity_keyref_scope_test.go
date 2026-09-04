package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The invariants a keyref evaluation must keep, pinned before the traversal
// underneath it was changed.
//
// checkKeyref used to discover its targets by walking the whole subtree once
// per enclosing scope. Pruning that walk and carrying the discovered targets
// upward is only sound if the checks themselves still run once per (target,
// scope) pair: a node under a nested keyref scope is a target of that scope AND
// of every enclosing one, and each resolves against its own key table. These
// cases are the ones where those two scopes disagree, so a change that
// collapsed them into one would be caught here rather than in a conformance
// total.

func loadKeyrefSchema(t *testing.T, src string) *Schema {
	t.Helper()
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	return s
}

func validateStr(t *testing.T, s *Schema, doc string) error {
	t.Helper()
	tr, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse instance: %v", err)
	}
	return s.Validate(tr.Root, ValidateOptions{})
}

// A keyref declared on a self-embedding element is a distinct scope at every
// level. A reference that resolves in the outer scope must still FAIL in the
// inner one when the key it names lies outside that inner subtree, so the
// enclosing scope's success cannot be allowed to stand in for the inner
// scope's check.
func TestKeyrefInnerScopeCheckedIndependently(t *testing.T) {
	s := loadKeyrefSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
  </xs:element>
  <xs:element name="r">
    <xs:complexType><xs:sequence>
      <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType>
          <xs:attribute name="id" type="xs:string"/>
          <xs:attribute name="ref" type="xs:string"/>
        </xs:complexType>
      </xs:element>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@id"/></xs:key>
    <xs:keyref name="kr" refer="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@ref"/></xs:keyref>
  </xs:element>
</xs:schema>`)

	// The inner r's leaf refers to "outer", whose key lives in the OUTER r
	// only. The outer scope resolves it; the inner scope must not.
	doc := `<root><r><leaf id="outer"/><r><leaf id="inner" ref="outer"/></r></r></root>`
	if err := validateStr(t, s, doc); err == nil {
		t.Error("a keyref resolving only outside its own scope was accepted; " +
			"the inner scope's check was skipped")
	}

	// Control: the same shape with the reference satisfied inside the inner
	// scope is valid at both levels.
	ok := `<root><r><leaf id="outer"/><r><leaf id="inner" ref="inner"/></r></r></root>`
	if err := validateStr(t, s, ok); err != nil {
		t.Errorf("a reference satisfied in every enclosing scope was rejected: %v", err)
	}
}

// The other direction: a keyref target that lies BETWEEN two scopes of the
// same constraint must be checked by the outer scope. If pruning stopped the
// outer walk at the inner scope's element and the inner scope did not report
// what it saw upward, such a target would never be checked at all.
func TestKeyrefTargetBetweenScopesStillChecked(t *testing.T) {
	s := loadKeyrefSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element ref="r" minOccurs="0"/>
    </xs:sequence></xs:complexType>
  </xs:element>
  <xs:element name="r">
    <xs:complexType><xs:sequence>
      <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType>
          <xs:attribute name="id" type="xs:string"/>
          <xs:attribute name="ref" type="xs:string"/>
        </xs:complexType>
      </xs:element>
      <xs:element name="mid" minOccurs="0">
        <xs:complexType><xs:sequence>
          <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded">
            <xs:complexType>
              <xs:attribute name="id" type="xs:string"/>
              <xs:attribute name="ref" type="xs:string"/>
            </xs:complexType>
          </xs:element>
          <xs:element ref="r" minOccurs="0"/>
        </xs:sequence></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@id"/></xs:key>
    <xs:keyref name="kr" refer="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@ref"/></xs:keyref>
  </xs:element>
</xs:schema>`)

	// The leaf inside <mid> is under no inner scope, so only the outer r
	// checks it. Its reference names nothing.
	doc := `<root><r><leaf id="a"/><mid><leaf id="b" ref="nosuch"/><r><leaf id="c" ref="c"/></r></mid></r></root>`
	if err := validateStr(t, s, doc); err == nil {
		t.Error("an unresolvable keyref between two scopes was accepted; " +
			"the outer walk lost the gap between scopes")
	}
}

// Every target must be checked, not merely one per distinct key sequence: two
// leaves sharing an unresolvable reference are two failures, and a design that
// deduplicated targets by sequence would report the second as already seen.
func TestKeyrefEveryTargetReported(t *testing.T) {
	s := loadKeyrefSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType>
          <xs:attribute name="id" type="xs:string"/>
          <xs:attribute name="ref" type="xs:string"/>
        </xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@id"/></xs:key>
    <xs:keyref name="kr" refer="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@ref"/></xs:keyref>
  </xs:element>
</xs:schema>`)

	doc := `<root><leaf id="a" ref="x"/><leaf id="b" ref="x"/></root>`
	err := validateStr(t, s, doc)
	if err == nil {
		t.Fatal("two unresolvable references were accepted")
	}
	if n := strings.Count(err.Error(), "cvc-identity-constraint.4.3"); n != 2 {
		t.Errorf("got %d keyref failures, want 2 (one per target); err=%v", n, err)
	}
}

// A keyref selecting nodes inside processContents="skip" content must ignore
// them. Pruning changes which nodes the walk reaches, so the skip check has to
// survive on whatever path now discovers targets.
func TestKeyrefSkipsSkippedContent(t *testing.T) {
	s := loadKeyrefSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType>
          <xs:attribute name="id" type="xs:string"/>
          <xs:attribute name="ref" type="xs:string"/>
        </xs:complexType>
      </xs:element>
      <xs:any namespace="##other" processContents="skip" minOccurs="0" maxOccurs="unbounded"/>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@id"/></xs:key>
    <xs:keyref name="kr" refer="k"><xs:selector xpath=".//leaf"/><xs:field xpath="@ref"/></xs:keyref>
  </xs:element>
</xs:schema>`)

	// The leaf inside <blob> was never assessed, so neither its key nor its
	// dangling reference participates.
	doc := `<root xmlns:o="urn:other"><leaf id="a" ref="a"/>` +
		`<o:blob><leaf id="a" ref="nosuch"/></o:blob></root>`
	if err := validateStr(t, s, doc); err != nil {
		t.Errorf("skipped content took part in a keyref: %v", err)
	}
}

// A keyref whose referent key is ambiguous in this scope must fail, and it must
// keep failing however many siblings define the sequence. This is the
// mergeEntry invariant seen from the keyref side; it is restated here because
// the keyref path is the one being rebuilt.
func TestKeyrefAgainstAmbiguousKeyStaysInvalid(t *testing.T) {
	s := loadKeyrefSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element ref="section" minOccurs="0" maxOccurs="unbounded"/>
      <xs:element name="use" minOccurs="0">
        <xs:complexType><xs:attribute name="ref" type="xs:string"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:keyref name="kr" refer="k"><xs:selector xpath=".//use"/><xs:field xpath="@ref"/></xs:keyref>
  </xs:element>
  <xs:element name="section">
    <xs:complexType><xs:sequence>
      <xs:element name="item" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//item"/><xs:field xpath="@id"/></xs:key>
  </xs:element>
</xs:schema>`)

	for _, n := range []int{1, 2, 3, 4, 5} {
		var b strings.Builder
		b.WriteString("<root>")
		for i := 0; i < n; i++ {
			b.WriteString(`<section><item id="A"/></section>`)
		}
		b.WriteString(`<use ref="A"/></root>`)
		invalid := validateStr(t, s, b.String()) != nil
		if want := n >= 2; invalid != want {
			t.Errorf("%d sections defining A: invalid=%v want %v", n, invalid, want)
		}
	}
}

// A keyref whose refer names a constraint with no table in scope fails, and
// only when it actually has a qualified target: a keyref with no targets is
// vacuously satisfied even where no key exists.
func TestKeyrefWithNoKeyInScope(t *testing.T) {
	s := loadKeyrefSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="use" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="ref" type="xs:string"/></xs:complexType>
      </xs:element>
      <xs:element ref="holder" minOccurs="0"/>
    </xs:sequence></xs:complexType>
    <xs:keyref name="kr" refer="k"><xs:selector xpath=".//use"/><xs:field xpath="@ref"/></xs:keyref>
  </xs:element>
  <xs:element name="holder">
    <xs:complexType><xs:sequence>
      <xs:element name="item" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//item"/><xs:field xpath="@id"/></xs:key>
  </xs:element>
</xs:schema>`)

	if err := validateStr(t, s, `<root/>`); err != nil {
		t.Errorf("a keyref with no targets should be vacuously satisfied: %v", err)
	}
	if err := validateStr(t, s, `<root><use ref="A"/></root>`); err == nil {
		t.Error("a keyref target with no key in scope was accepted")
	}
	if err := validateStr(t, s, `<root><use ref="A"/><holder><item id="A"/></holder></root>`); err != nil {
		t.Errorf("a keyref resolving into a nested key scope was rejected: %v", err)
	}
}

// A keyref field that selects nothing does not participate; a keyref field
// selecting more than one node is a clause 3 failure. Both are decided while
// the sequence is built, so they must survive wherever that build now happens.
func TestKeyrefFieldCardinality(t *testing.T) {
	s := loadKeyrefSchema(t, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="item" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="id" type="xs:string"/></xs:complexType>
      </xs:element>
      <xs:element name="use" minOccurs="0" maxOccurs="unbounded">
        <xs:complexType><xs:attribute name="ref" type="xs:string"/></xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:key name="k"><xs:selector xpath=".//item"/><xs:field xpath="@id"/></xs:key>
    <xs:keyref name="kr" refer="k"><xs:selector xpath=".//use"/><xs:field xpath="@ref"/></xs:keyref>
  </xs:element>
</xs:schema>`)

	// No @ref at all: the field selects nothing, so the node is not a
	// qualified target and nothing is checked against the key.
	if err := validateStr(t, s, `<root><item id="A"/><use/></root>`); err != nil {
		t.Errorf("a keyref whose field selects nothing should not participate: %v", err)
	}
}
