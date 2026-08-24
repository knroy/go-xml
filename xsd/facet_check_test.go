package xsd

import "testing"

// TestBoundFacetMayRestateBaseBound covers "maxExclusive valid restriction"
// clause 1 and its three siblings: it is an error only if the declared bound is
// *greater than* the base's, so re-stating a bound with the same value is
// legal.
//
// The value-space check that runs alongside those clauses used to validate the
// facet value against the base outright, bounding facets included. A bound
// equal to its parent's then failed the parent's own strict comparison — the
// facet contradicted itself — and d3_4_28v09 was rejected. The value space is
// the datatype's; how a bound relates to the base's bounds is the separate
// clause with the separate comparison.
func TestBoundFacetMayRestateBaseBound(t *testing.T) {
	// Re-stating maxExclusive and minExclusive identically: legal.
	mustLoadOK(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="a">
	    <xs:restriction base="xs:int">
	      <xs:maxExclusive value="100"/>
	      <xs:minExclusive value="0"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="b">
	    <xs:restriction base="a">
	      <xs:maxExclusive value="100"/>
	      <xs:minExclusive value="0"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`)

	// The same for the inclusive pair, whose endpoints are in the value
	// space of the base as restricted.
	mustLoadOK(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="a">
	    <xs:restriction base="xs:int">
	      <xs:maxInclusive value="100"/>
	      <xs:minInclusive value="0"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="b">
	    <xs:restriction base="a">
	      <xs:maxInclusive value="100"/>
	      <xs:minInclusive value="0"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`)

	// Widening is still an error: the clause the value-space check must not
	// duplicate is the one that catches this.
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="a">
	    <xs:restriction base="xs:int">
	      <xs:maxExclusive value="100"/>
	    </xs:restriction>
	  </xs:simpleType>
	  <xs:simpleType name="b">
	    <xs:restriction base="a">
	      <xs:maxExclusive value="101"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`, "maxExclusive-valid-restriction")

	// A value outside the base's value space is still an error: that is
	// what the check is for.
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="b">
	    <xs:restriction base="xs:int">
	      <xs:maxExclusive value="notanint"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`, "maxExclusive-valid-restriction")
}

// TestFacetAndAnnotationChildren covers the schema-for-schemas content models
// that were missing from the table: every constraining facet takes
// "annotation?" and nothing else, and an <annotation> takes appinfo and
// documentation and nothing else.
//
// Without them a stray <xs:notation> nested inside a facet — or directly
// inside an annotation — loaded without complaint, which is the whole
// MS-Notations F-series (notatF003, F025, F041, F045, F049, F053).
func TestFacetAndAnnotationChildren(t *testing.T) {
	// A notation inside a facet is not a schema.
	for _, facet := range []string{
		`<xs:enumeration value="1"><xs:notation name="n" system="v"/></xs:enumeration>`,
		`<xs:length value="8"><xs:notation name="n" system="v"/></xs:length>`,
		`<xs:pattern value="0"><xs:notation name="n" system="v"/></xs:pattern>`,
		`<xs:maxInclusive value="0"><xs:notation name="n" system="v"/></xs:maxInclusive>`,
		`<xs:minInclusive value="0"><xs:notation name="n" system="v"/></xs:minInclusive>`,
	} {
		mustLoadFail(t, `
		<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="foo">
		    <xs:restriction base="xs:string">`+facet+`</xs:restriction>
		  </xs:simpleType>
		</xs:schema>`, "")
	}

	// Nor directly inside an annotation.
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:annotation><xs:notation name="foo" system=""/></xs:annotation>
	</xs:schema>`, "")

	// An annotation on a facet is still allowed, and so is a facet with no
	// children at all — the rule is "annotation only", not "nothing".
	mustLoadOK(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:simpleType name="foo">
	    <xs:restriction base="xs:string">
	      <xs:maxLength value="8">
	        <xs:annotation><xs:documentation>why</xs:documentation></xs:annotation>
	      </xs:maxLength>
	      <xs:minLength value="1"/>
	    </xs:restriction>
	  </xs:simpleType>
	</xs:schema>`)

	// An annotation's own appinfo and documentation hold open content, so
	// arbitrary elements inside *those* remain fine.
	mustLoadOK(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:annotation>
	    <xs:appinfo><anything xmlns=""><nested/></anything></xs:appinfo>
	    <xs:documentation><p xmlns="">text</p></xs:documentation>
	  </xs:annotation>
	  <xs:element name="e" type="xs:string"/>
	</xs:schema>`)
}

// TestRedefineAndOverrideChildren covers the two content models the
// schema-for-schemas table still lacked.
//
// <redefine> admits only the ·redefinable· components — simpleType,
// complexType, group, attributeGroup — interleaved freely with annotations,
// which is the one place an annotation is not restricted to the front.
// <notation> is not redefinable, so notatF055 is not a schema.
//
// <override> is the 1.1 element that admits anything appearing at the top of a
// schema, notation included, after a single leading annotation.
func TestRedefineAndOverrideChildren(t *testing.T) {
	// A notation is not redefinable.
	mustLoadFail(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:redefine schemaLocation="foo">
	    <xs:notation name="jpeg" public="image/jpeg" system="viewer.exe"/>
	  </xs:redefine>
	</xs:schema>`, "")

	// Annotations may sit between the redefinable components, not only
	// before them.
	mustLoadOK(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:redefine schemaLocation="empty.xsd">
	    <xs:annotation><xs:documentation>first</xs:documentation></xs:annotation>
	    <xs:simpleType name="a"><xs:restriction base="xs:string"/></xs:simpleType>
	    <xs:annotation><xs:documentation>between</xs:documentation></xs:annotation>
	    <xs:attributeGroup name="g"/>
	  </xs:redefine>
	</xs:schema>`)
}

// cos-list-of-atomic (Part 2 §4.1.5): a list's item type must be atomic, or a
// union whose members are ultimately atomic. The recursion through nested
// unions is not optional — the test suite's own catalog schema defines a list
// of a union of eight unions, and rejecting it took 91 instance tests with it.
func TestListOfAtomic(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		ok   bool
	}{
		{"a list whose item type is a list (stJ019)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="inner"><xs:list itemType="xs:integer"/></xs:simpleType>
		  <xs:simpleType name="outer"><xs:list itemType="inner"/></xs:simpleType>
		 </xs:schema>`, false},
		{"a list of a union of atomic types", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="u">
		   <xs:union memberTypes="xs:integer xs:date"/></xs:simpleType>
		  <xs:simpleType name="l"><xs:list itemType="u"/></xs:simpleType>
		 </xs:schema>`, true},
		{"a list of a union of unions of atomic types (xsts.xsd)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="u1">
		   <xs:union memberTypes="xs:integer xs:date"/></xs:simpleType>
		  <xs:simpleType name="u2">
		   <xs:union memberTypes="xs:token xs:boolean"/></xs:simpleType>
		  <xs:simpleType name="u"><xs:union memberTypes="u1 u2"/></xs:simpleType>
		  <xs:simpleType name="l"><xs:list itemType="u"/></xs:simpleType>
		 </xs:schema>`, true},
		{"a list of a union with a list member", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="inner"><xs:list itemType="xs:integer"/></xs:simpleType>
		  <xs:simpleType name="u">
		   <xs:union memberTypes="xs:integer inner"/></xs:simpleType>
		  <xs:simpleType name="l"><xs:list itemType="u"/></xs:simpleType>
		 </xs:schema>`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := loadVer(t, c.doc, Version10)
			if c.ok && err != nil {
				t.Errorf("the schema must load: %v", err)
			}
			if !c.ok && err == nil {
				t.Error("the schema loaded; cos-list-of-atomic must reject it")
			}
		})
	}
}

// §3.14.2: name and final are prohibited on a <simpleType> that is not a child
// of <schema> or <redefine>. stA008, stA009 and stA010 write the same named
// inline type inside a restriction, a list and a union.
func TestLocalSimpleTypeForm(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		ok   bool
	}{
		{"a named simpleType inside a restriction (stA008)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="p"><xs:restriction>
		   <xs:simpleType name="foo"><xs:restriction base="xs:string">
		    <xs:length value="4"/></xs:restriction></xs:simpleType>
		  </xs:restriction></xs:simpleType></xs:schema>`, false},
		{"a named simpleType inside a list (stA009)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="p"><xs:list>
		   <xs:simpleType name="foo"><xs:restriction base="xs:string"/></xs:simpleType>
		  </xs:list></xs:simpleType></xs:schema>`, false},
		{"a named simpleType inside a union (stA010)", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="p"><xs:union>
		   <xs:simpleType name="foo"><xs:restriction base="xs:string"/></xs:simpleType>
		  </xs:union></xs:simpleType></xs:schema>`, false},
		{"the same shapes without a name", `
		 <xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
		  <xs:simpleType name="p"><xs:union>
		   <xs:simpleType><xs:restriction base="xs:string"/></xs:simpleType>
		  </xs:union></xs:simpleType>
		  <xs:simpleType name="q" final="restriction">
		   <xs:restriction base="xs:string"/></xs:simpleType></xs:schema>`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := loadVer(t, c.doc, Version10)
			if c.ok && err != nil {
				t.Errorf("the schema must load: %v", err)
			}
			if !c.ok && err == nil {
				t.Error("the schema loaded; the local-form rule must reject it")
			}
		})
	}
}
