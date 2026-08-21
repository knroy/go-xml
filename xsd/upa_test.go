package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// checkSchema loads a schema and reports whether the component constraints
// hold.
//
// Loading applies them itself, so a violating schema fails to load and that
// failure is the answer. The explicit CheckConstraints call still runs for a
// schema that did load, because the options may select a rule the load did not
// use — LaxUPA in particular, which the loader was not given.
func checkSchema(t *testing.T, src string, opts CheckOptions) error {
	t.Helper()
	s, err := parseSchemaString(t, src)
	if err != nil {
		return err
	}
	return s.CheckConstraints(opts)
}

// TestUPADetectsAmbiguousChoice covers the simplest violation: two branches of
// a choice that accept the same element.
func TestUPADetectsAmbiguousChoice(t *testing.T) {
	err := checkSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:choice>
	      <xs:element name="a" type="xs:string"/>
	      <xs:element name="a" type="xs:string"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`, CheckOptions{})
	if err == nil {
		t.Fatal("two identical branches of a choice violate UPA")
	}
	if !strings.Contains(err.Error(), "cos-nonambig") {
		t.Errorf("error %q does not cite cos-nonambig", err)
	}
}

// TestUPADetectsOptionalPrefix covers the case the spec's own working group
// used as its example:
//
//	<sequence>
//	  <element ref="a" minOccurs="0"/>
//	  <element ref="b" minOccurs="0"/>
//	  <element ref="a" maxOccurs="2"/>
//	</sequence>
//
// After an optional "a", the automaton cannot tell whether a following "a" is
// the third particle or a repetition of the first.
func TestUPADetectsOptionalPrefix(t *testing.T) {
	err := checkSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:sequence>
	      <xs:element name="a" type="xs:string" minOccurs="0"/>
	      <xs:element name="b" type="xs:string" minOccurs="0"/>
	      <xs:element name="a" type="xs:string" maxOccurs="2"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`, CheckOptions{})
	if err == nil {
		t.Fatal("the optional-prefix sequence violates UPA")
	}
}

// TestUPAElementAgainstWildcard covers the case XSD 1.1 later relaxed: in 1.0
// an element competing with a wildcard that admits it is a violation, and 1.1
// instead prefers the element. A 1.0 validator must not silently adopt the 1.1
// rule, because it changes which schemas are accepted.
func TestUPAElementAgainstWildcard(t *testing.T) {
	err := checkSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:choice>
	      <xs:element name="a" type="xs:string"/>
	      <xs:any namespace="##any" processContents="skip"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`, CheckOptions{})
	if err == nil {
		t.Fatal("an element competing with a wildcard violates UPA in XSD 1.0")
	}
}

// TestUPAAcceptsUnambiguous confirms the check does not fire on ordinary
// content models, which matters more than any individual rejection: a UPA check
// that rejects valid schemas is worse than none.
func TestUPAAcceptsUnambiguous(t *testing.T) {
	for _, src := range []string{
		`<xs:complexType name="t"><xs:sequence>
		   <xs:element name="a" type="xs:string"/>
		   <xs:element name="b" type="xs:string"/>
		 </xs:sequence></xs:complexType>`,

		`<xs:complexType name="t"><xs:choice>
		   <xs:element name="a" type="xs:string"/>
		   <xs:element name="b" type="xs:string"/>
		 </xs:choice></xs:complexType>`,

		// A repeated element is not ambiguous with itself: the same
		// particle matching twice is one particle, not two.
		`<xs:complexType name="t"><xs:sequence>
		   <xs:element name="a" type="xs:string" maxOccurs="unbounded"/>
		 </xs:sequence></xs:complexType>`,

		// Optional elements with distinct names are fine.
		`<xs:complexType name="t"><xs:sequence>
		   <xs:element name="a" type="xs:string" minOccurs="0"/>
		   <xs:element name="b" type="xs:string" minOccurs="0"/>
		   <xs:element name="c" type="xs:string"/>
		 </xs:sequence></xs:complexType>`,

		// Two wildcards over disjoint namespaces do not overlap.
		`<xs:complexType name="t"><xs:choice>
		   <xs:any namespace="urn:a"/>
		   <xs:any namespace="urn:b"/>
		 </xs:choice></xs:complexType>`,
	} {
		full := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` +
			src + `</xs:schema>`
		if err := checkSchema(t, full, CheckOptions{}); err != nil {
			t.Errorf("this model is unambiguous but was rejected:\n%s\n%v", src, err)
		}
	}
}

// TestUPALaxAcceptsSameDeclaration covers the documented divergence.
//
// Saxon and XSV accept a model where two competing particles reference the same
// element declaration, on the grounds that the *declaration* is identifiable
// even though the particle is not. Michael Kay calls it "a known minor
// departure from the spec". The strict reading is the default; LaxUPA selects
// theirs.
func TestUPALaxAcceptsSameDeclaration(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:element name="a" type="xs:string"/>
	  <xs:complexType name="t">
	    <xs:choice>
	      <xs:element ref="a"/>
	      <xs:element ref="a"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`

	if err := checkSchema(t, src, CheckOptions{}); err == nil {
		t.Error("the strict reading should reject two references to one declaration")
	}
	// LaxUPA has to be given to the *loader*, because loading is what
	// applies the constraint now. Options.LaxUPA is the way to say it.
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing the test schema as XML: %v", err)
	}
	if _, err := Load(tree.Root, "", Options{LaxUPA: true}); err != nil {
		t.Errorf("LaxUPA should accept it: %v", err)
	}
}

// TestElementDeclarationsConsistent covers the constraint that is separate from
// UPA: the same element name meaning two different types in one content model.
func TestElementDeclarationsConsistent(t *testing.T) {
	err := checkSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:sequence>
	      <xs:element name="a" type="xs:string"/>
	      <xs:element name="b" type="xs:string"/>
	      <xs:element name="a" type="xs:int"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`, CheckOptions{})
	if err == nil {
		t.Fatal("one name with two types in a content model is inconsistent")
	}
	if !strings.Contains(err.Error(), "cos-element-consistent") {
		t.Errorf("error %q does not cite cos-element-consistent", err)
	}
}

// TestCheckConstraintsRunAtLoad records that loading applies them.
//
// They were opt-in once, on the Xerces schema-full-checking precedent. That
// precedent is about whether a validator pays the cost per document; it is the
// wrong analogy for a loader answering "is this a schema?", and answering yes
// for a content model the spec forbids is a false accept. mgR001..mgR022 and
// mgS002..mgS005 are schemaTest cases expected invalid for exactly these two
// constraints, and every one of them loaded clean while the checks were
// opt-in.
func TestCheckConstraintsRunAtLoad(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:choice>
	      <xs:element name="a" type="xs:string"/>
	      <xs:element name="a" type="xs:string"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`

	_, err := parseSchemaString(t, src)
	if err == nil {
		t.Fatal("a schema violating UPA should not load")
	}
	if !strings.Contains(err.Error(), "cos-nonambig") {
		t.Errorf("error %q does not cite cos-nonambig", err)
	}
}

// TestRecursiveGroupIsRefused covers a shape that appears in real schemas and
// crashed the compiler.
//
// A model group that reaches itself — <xs:group name="expr"> whose content
// references expr — makes the particle tree a cyclic graph, while Glushkov
// construction assumes a tree, so following the cycle recursed until the stack
// was gone.
//
// Model Group Correct clause 2 (§3.8.6) settles what to do about it: circular
// groups are disallowed outright, so such a document is not a schema and is
// refused when it is read, before any consumer of the component graph can walk
// into the cycle. This test used only to require that CheckConstraints
// terminate, on the view that a recursive group was legal and merely
// uncompilable; the suite disagrees — groupB013, groupB014 and groupB015 are
// all circular and all expected to be invalid.
func TestRecursiveGroupIsRefused(t *testing.T) {
	_, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="expr">
	    <xs:sequence>
	      <xs:element name="lit" type="xs:string" minOccurs="0"/>
	      <xs:group ref="expr" minOccurs="0"/>
	    </xs:sequence>
	  </xs:group>
	  <xs:complexType name="t"><xs:group ref="expr"/></xs:complexType>
	</xs:schema>`)
	if err == nil {
		t.Fatal("a circular model group should be refused at load")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error should name the circularity, got: %v", err)
	}
}

// TestGroupCycleThroughTwoDefinitions covers indirect circularity.
//
// Clause 2 of Model Group Correct bans a self-reference "at any depth", which
// includes a cycle that passes through another definition rather than closing
// on itself directly. Checking only for a group that names itself would miss
// this, and it is the shape groupB015 uses.
func TestGroupCycleThroughTwoDefinitions(t *testing.T) {
	_, err := parseSchemaString(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="foo"><xs:sequence><xs:group ref="bar"/></xs:sequence></xs:group>
	  <xs:group name="bar"><xs:sequence><xs:group ref="foo"/></xs:sequence></xs:group>
	</xs:schema>`)
	if err == nil {
		t.Fatal("a cycle through two group definitions should be refused")
	}
}

// TestDisjointGroupReuseIsNotACycle guards the cycle search against reporting
// a group reached twice by different routes.
//
// Marking a group "seen" for the whole search rather than for the current
// descent would call this circular: base is reached once from left and once
// from right, and neither route revisits anything. It is an ordinary
// diamond, and a very common way to reuse a group.
func TestDisjointGroupReuseIsNotACycle(t *testing.T) {
	mustParseSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="base"><xs:sequence><xs:element name="a"/></xs:sequence></xs:group>
	  <xs:group name="left"><xs:sequence><xs:group ref="base"/></xs:sequence></xs:group>
	  <xs:group name="right"><xs:sequence><xs:group ref="base"/></xs:sequence></xs:group>
	  <xs:group name="top">
	    <xs:sequence><xs:group ref="left"/><xs:group ref="right"/></xs:sequence>
	  </xs:group>
	</xs:schema>`)
}

// TestUPAAndEDCRejectAtLoad pins the suite cases that went unreported while
// the two constraints were opt-in.
//
// Each shape is taken from a schemaTest the W3C expects invalid: mgS002 makes
// a sequence ambiguous by giving two branches of a choice the same first
// element, and mgR001 declares one name with two types inside an all group.
// Both loaded clean before, because nothing on the load path called
// CheckConstraints.
func TestUPAAndEDCRejectAtLoad(t *testing.T) {
	for _, tc := range []struct{ name, cite, src string }{
		{
			// mgS002: (a (bc | bd)) — after <a>, seeing <b> does
			// not say which branch is running.
			name: "ambiguous sequence",
			cite: "cos-nonambig",
			src: `
			<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
			  <xs:complexType name="foo">
			    <xs:sequence>
			      <xs:element name="a"/>
			      <xs:choice>
			        <xs:sequence>
			          <xs:element name="b"/>
			          <xs:element name="c"/>
			        </xs:sequence>
			        <xs:sequence>
			          <xs:element name="b"/>
			          <xs:element name="d"/>
			        </xs:sequence>
			      </xs:choice>
			    </xs:sequence>
			  </xs:complexType>
			</xs:schema>`,
		},
		{
			// mgR001: e1 is xs:string in one particle and a
			// complex type in the other.
			name: "one name two types",
			cite: "cos-element-consistent",
			src: `
			<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
			  <xs:complexType name="foo">
			    <xs:all>
			      <xs:element name="e1" type="xs:string"/>
			      <xs:element name="e1" type="xs:int"/>
			    </xs:all>
			  </xs:complexType>
			</xs:schema>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range []Version{Version10, Version11} {
				err := loadVersion(t, tc.src, v)
				if err == nil {
					t.Fatalf("version %v: the schema should not load", v)
				}
				if !strings.Contains(err.Error(), tc.cite) {
					t.Errorf("version %v: error %q does not cite %s", v, err, tc.cite)
				}
			}
		})
	}
}

// TestUPAWildcardRelaxationIsVersioned covers the one UPA rule that differs
// between versions.
//
// XSD 1.1 resolves an element competing with a wildcard in favour of the
// element instead of calling the model ambiguous — the suite names the feature
// xsd1_1-Wildcards-RelaxationOfUPA, "wildcard/element competition no longer
// violates UPA", and s3_10_1v04s is a version="1.1" schemaTest expected valid
// for this shape. Applying the 1.0 rule under 1.1 rejected 21 such schemas.
func TestUPAWildcardRelaxationIsVersioned(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:choice>
	      <xs:element name="a" type="xs:string"/>
	      <xs:any namespace="##any" processContents="skip"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`

	if err := loadVersion(t, src, Version10); err == nil {
		t.Error("1.0: an element competing with a wildcard violates UPA")
	}
	if err := loadVersion(t, src, Version11); err != nil {
		t.Errorf("1.1 relaxed this and should accept it: %v", err)
	}
}

// TestAllGroupLimited covers clause 1 of All Group Limited (§3.8.6): an all
// group is only ever a whole content model.
//
// The group-reference cases are the ones the parser cannot see, because a
// reference is bound by a fixup long after it is read — so an all group
// reached through <xs:group ref> escaped every occurrence check.
func TestAllGroupLimited(t *testing.T) {
	// mgA020: an all group referenced from inside a sequence.
	nested := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="foo">
	    <xs:sequence>
	      <xs:element name="a"/>
	      <xs:group ref="groupy"/>
	    </xs:sequence>
	  </xs:complexType>
	  <xs:group name="groupy">
	    <xs:all>
	      <xs:element name="b"/>
	    </xs:all>
	  </xs:group>
	</xs:schema>`
	for _, v := range []Version{Version10, Version11} {
		if err := loadVersion(t, nested, v); err == nil {
			t.Errorf("version %v: an all group inside a sequence is forbidden", v)
		}
	}

	// particlesEa025: the reference carries maxOccurs="2", which clause
	// 1.2 pins to 1.
	repeated := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="G">
	    <xs:all>
	      <xs:element name="a1"/>
	      <xs:element name="a2"/>
	    </xs:all>
	  </xs:group>
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:group ref="G" minOccurs="1" maxOccurs="2"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`
	for _, v := range []Version{Version10, Version11} {
		if err := loadVersion(t, repeated, v); err == nil {
			t.Errorf("version %v: an all group must have maxOccurs=1", v)
		}
	}
}

// TestAllExtensionIsVersioned covers mgA016, which the W3C expects invalid
// under 1.0 and valid under 1.1.
//
// XSD 1.1 §3.4.2.3.3 clause 2.2 merges an all-group base with an all-group
// extension into one all group. XSD 1.0 has no such clause: there the splice
// is a sequence, and a sequence holding an all group breaks All Group Limited,
// so extending an all group is simply illegal. The merge used to run for both
// versions, which made the 1.0 case load.
func TestAllExtensionIsVersioned(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="foo">
	    <xs:all>
	      <xs:element name="a"/>
	    </xs:all>
	  </xs:complexType>
	  <xs:complexType name="bar">
	    <xs:complexContent>
	      <xs:extension base="foo">
	        <xs:all>
	          <xs:element name="b"/>
	        </xs:all>
	      </xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	</xs:schema>`

	if err := loadVersion(t, src, Version10); err == nil {
		t.Error("1.0: extending an all group is not allowed")
	}
	if err := loadVersion(t, src, Version11); err != nil {
		t.Errorf("1.1 merges the two all groups and should accept it: %v", err)
	}
}

// TestGroupDefOccursValidated covers particlesEc009: the occurrence range on
// the <choice> or <sequence> inside a named <xs:group> must still be well
// formed.
//
// A group definition has no occurrence range of its own — only the reference
// to it does — so this path builds the model group directly and never calls
// readParticle, which is where occurs() validates the pair. The range was
// therefore discarded without ever being read, and <choice minOccurs="2">,
// whose maxOccurs defaults to 1, loaded clean.
func TestGroupDefOccursValidated(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="G">
	    <xs:choice minOccurs="2">
	      <xs:element name="a1"/>
	      <xs:element name="a2"/>
	    </xs:choice>
	  </xs:group>
	  <xs:element name="doc">
	    <xs:complexType>
	      <xs:group ref="G"/>
	    </xs:complexType>
	  </xs:element>
	</xs:schema>`

	for _, v := range []Version{Version10, Version11} {
		err := loadVersion(t, src, v)
		if err == nil {
			t.Fatalf("version %v: minOccurs=2 with a defaulted maxOccurs=1 is invalid", v)
		}
		if !strings.Contains(err.Error(), "p-props-correct") {
			t.Errorf("version %v: error %q does not cite p-props-correct", v, err)
		}
	}
}

// TestUPACountedRepetitionIsNotAmbiguous covers mgZ005, which the W3C expects
// valid: <b minOccurs="2" maxOccurs="2"/> followed by <b/> in a sequence.
//
// This automaton counts rather than duplicating positions, so the two b's are
// two positions on the same name — the shape UPA normally rejects. Here it is
// not ambiguous: while the counter is below its minimum the automaton must stay
// inside it, so the first two b's belong to the counted particle and the third
// to the plain one, with no choice to get wrong.
func TestUPACountedRepetitionIsNotAmbiguous(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:sequence>
	      <xs:element name="a" minOccurs="0"/>
	      <xs:element name="b" minOccurs="2" maxOccurs="2"/>
	      <xs:element name="b"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`
	for _, v := range []Version{Version10, Version11} {
		if err := loadVersion(t, src, v); err != nil {
			t.Errorf("version %v: a counted repetition is deterministic: %v", v, err)
		}
	}

	// The counter has to actually force something. With minOccurs="1" the
	// automaton may leave immediately, so the choice is real and UPA
	// applies as usual.
	ambiguous := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="t">
	    <xs:sequence>
	      <xs:element name="b" minOccurs="1" maxOccurs="2"/>
	      <xs:element name="b"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`
	if err := loadVersion(t, ambiguous, Version10); err == nil {
		t.Error("a counter that may be left at once leaves a real ambiguity")
	}
}

// TestExtensionOfEmptyBaseKeepsOwnParticle covers mgO014, which the W3C expects
// valid: a complex type extending <xs:sequence/> with a group whose term is an
// all group.
//
// A base matching only the empty sequence contributes nothing, so the effective
// content type is the extension's own particle. Splicing the two into a
// sequence anyway would put an all group inside a sequence and break All Group
// Limited for a schema that is fine.
func TestExtensionOfEmptyBaseKeepsOwnParticle(t *testing.T) {
	src := `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:group name="g">
	    <xs:all>
	      <xs:element name="e1"/>
	    </xs:all>
	  </xs:group>
	  <xs:complexType name="bar">
	    <xs:sequence/>
	  </xs:complexType>
	  <xs:complexType name="foo">
	    <xs:complexContent>
	      <xs:extension base="bar">
	        <xs:group ref="g"/>
	      </xs:extension>
	    </xs:complexContent>
	  </xs:complexType>
	</xs:schema>`
	for _, v := range []Version{Version10, Version11} {
		if err := loadVersion(t, src, v); err != nil {
			t.Errorf("version %v: an empty base contributes nothing: %v", v, err)
		}
	}
}
