package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// DocBook 5.0's XSD does not load, and these lock in the finding that it
// should not.
//
// Thirty-eight files under
// testdata/xslt30-test/tests/misc/docbook/docbook-xsl-1.79.1/slides/schema/xsd
// fail with 568 errors — 281 cos-element-consistent and 287 cos-nonambig — and
// refusing a schema that widely deployed is strong enough evidence of a bug
// here that it was investigated as one. It is not a bug: the three clusters
// are genuine §3.8.6 violations, and the suite says so for each. The full
// argument is in docs/known-gaps.md; these tests keep the boundary it
// establishes from moving in either direction.
//
// The reason the XSD is invalid is that DocBook's NORMATIVE schema is RELAX
// NG. relaxng/index.rng defines db.indexterm as a <choice> of three
// <element name="indexterm"> patterns told apart only by the value of a
// required class attribute. RELAX NG resolves that by inspecting the
// attribute; §3.8.6 requires the particle be determined "without examining the
// content or attributes of that item". The XSD files are a lossy machine
// translation of a construct XSD cannot express.

// TestDocBookSameNameDifferentTypeIsRefused is the minimised form of the
// cos-element-consistent cluster, and of the same-name half of cos-nonambig.
//
// This is DocBook's db.indexterm shape: a choice of two named groups, each
// declaring a LOCAL element of the same name with a DIFFERENT type.
// db.firstterm/db._firstterm (glossary.xsd) and the five info declarations
// (pool.xsd) are the same shape. It is msData/modelGroups/mgR022.xsd almost
// verbatim, which the suite expects invalid with status="accepted", under the
// documentation "2 particles with idendical element declarations (different
// type)" (sic).
//
// Both constraints fire, and both are meant to: EDC because the name means two
// things here, UPA because the automaton would have to choose between the two
// particles by looking at the element's attributes.
func TestDocBookSameNameDifferentTypeIsRefused(t *testing.T) {
	const src = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           xmlns:t="urn:t" targetNamespace="urn:t">
	  <xs:group name="a">
	    <xs:sequence><xs:element name="x" type="xs:string"/></xs:sequence>
	  </xs:group>
	  <xs:group name="b">
	    <xs:sequence><xs:element name="x" type="xs:int"/></xs:sequence>
	  </xs:group>
	  <xs:complexType name="ct">
	    <xs:choice>
	      <xs:group ref="t:a"/>
	      <xs:group ref="t:b"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`

	err := checkSchema(t, src, CheckOptions{})
	if err == nil {
		t.Fatal("two same-named local declarations of different types " +
			"violate cos-element-consistent; mgR022 expects invalid")
	}
	if !strings.Contains(err.Error(), "cos-element-consistent") {
		t.Errorf("want cos-element-consistent, got: %v", err)
	}

	// The permissive reading must not rescue it. LaxUPA waives only the
	// case where two competing particles are references to the SAME
	// element declaration; these are two declarations with two types, so
	// nothing about it applies — and EDC is not a UPA option at all.
	// Loading DocBook with LaxUPA set moves 568 errors to 567, which is
	// what this asserts in miniature.
	if err := checkSchema(t, src, CheckOptions{LaxUPA: true}); err == nil {
		t.Fatal("LaxUPA must not waive cos-element-consistent")
	}

	// Nor may the 1.1 relaxation. XSD 1.1 switched off element-against-
	// WILDCARD competition only; element-against-element is untouched, and
	// EDC is not versioned except for its type-table half.
	tree, perr := xdm.ParseString(src, xdm.ParseOptions{})
	if perr != nil {
		t.Fatalf("parsing the test schema as XML: %v", perr)
	}
	if _, err := Load(tree.Root, "", Options{Version: Version11}); err == nil {
		t.Fatal("XSD 1.1 relaxed wildcard competition, not this")
	}
}

// TestDocBookSameNameSameTypeStillLoads is the control the previous test needs.
//
// It matters more than the rejection: a check that refuses everything is not a
// check. msData/modelGroups/mgQ003 is this schema and the suite expects it
// VALID, while mgR003 — identical but for the second declaration's type — is
// expected invalid. The pair differs only in the type, which is exactly the
// distinction checkElementDeclarationsConsistent draws, so this is the tightest
// available evidence that the line is in the right place.
func TestDocBookSameNameSameTypeStillLoads(t *testing.T) {
	if err := checkSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	  <xs:complexType name="ct">
	    <xs:sequence>
	      <xs:element name="x" type="xs:string"/>
	      <xs:element name="x" type="xs:string"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`, CheckOptions{}); err != nil {
		t.Fatalf("mgQ003 is expected valid: %v", err)
	}
}

// TestDocBookSameGlobalDeclarationTwiceLoads guards the trap that would make
// the cluster our bug rather than DocBook's.
//
// Two references to the same GLOBAL element declaration are the same
// declaration, so they cannot disagree about their type and EDC has nothing to
// compare. If this ever started failing, the "two different types" errors
// against DocBook would be an artefact of comparing a declaration with itself
// through two particles, and the finding would flip. It passes today, which is
// why the finding stands: DocBook's are distinct *ElementDecls with distinct
// types.
func TestDocBookSameGlobalDeclarationTwiceLoads(t *testing.T) {
	if err := checkSchema(t, `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           xmlns:t="urn:t" targetNamespace="urn:t">
	  <xs:element name="x" type="xs:string"/>
	  <xs:group name="a"><xs:sequence><xs:element ref="t:x"/></xs:sequence></xs:group>
	  <xs:group name="b"><xs:sequence><xs:element ref="t:x"/></xs:sequence></xs:group>
	  <xs:complexType name="ct">
	    <xs:sequence>
	      <xs:group ref="t:a"/>
	      <xs:group ref="t:b"/>
	    </xs:sequence>
	  </xs:complexType>
	</xs:schema>`, CheckOptions{}); err != nil {
		t.Fatalf("one global declaration reached twice is one "+
			"declaration and cannot conflict with itself: %v", err)
	}
}

// TestDocBookWildcardCompetesUnder10Only covers the nine wildcard errors.
//
// DocBook's db._any (pool.xsd) is a bare <xs:any processContents="skip"/> in a
// group, referenced from a <xs:choice> beside named element refs. Under XSD 1.0
// that competes with every element in the choice and UPA fires; under 1.1 it
// does not, because 1.1 resolves element-against-wildcard in favour of the
// element rather than calling the model ambiguous.
//
// Both halves are asserted, because the version is the whole question here.
// The DocBook files carry no vc:minVersion and no version="1.1", so they are
// 1.0 schemas and the 1.0 answer is the right one for them. Loading them as
// 1.1 to make them pass would answer a different question than the one asked.
func TestDocBookWildcardCompetesUnder10Only(t *testing.T) {
	const src = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           xmlns:t="urn:t" targetNamespace="urn:t">
	  <xs:element name="abstract" type="xs:string"/>
	  <xs:group name="any">
	    <xs:sequence><xs:any processContents="skip"/></xs:sequence>
	  </xs:group>
	  <xs:complexType name="ct">
	    <xs:choice>
	      <xs:element ref="t:abstract"/>
	      <xs:group ref="t:any"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`

	err := checkSchema(t, src, CheckOptions{})
	if err == nil {
		t.Fatal("an element competing with ##any violates UPA in XSD 1.0")
	}
	if !strings.Contains(err.Error(), "cos-nonambig") {
		t.Errorf("want cos-nonambig, got: %v", err)
	}

	// The 1.1 half has to go through the LOADER, not CheckConstraints.
	// checkSchema's helper parses at 1.0, so the constraint has already
	// fired by the time any CheckOptions are consulted — Options.Version is
	// what selects the rule, and it selects it during assembly.
	tree, perr := xdm.ParseString(src, xdm.ParseOptions{})
	if perr != nil {
		t.Fatalf("parsing the test schema as XML: %v", perr)
	}
	if _, err := Load(tree.Root, "", Options{Version: Version11}); err != nil {
		t.Errorf("XSD 1.1 no longer treats wildcard/element competition "+
			"as a UPA violation: %v", err)
	}
}

// TestDocBookSameNameUPAMessageIsNotEvidenceOfABug records why the oddest
// message in the set is correct.
//
// "element x and element x can both match the same element" names one QName
// against ITSELF, which reads like a bug — two particles that lead to the same
// declaration are not an ambiguity the processor has to resolve. But these are
// two DIFFERENT declarations that happen to share a name, and the suite's
// mgQ021 is exactly this shape with the SAME type on both and is still expected
// invalid: sharing a name is enough, agreeing on a type does not help.
//
// So the message is right, and LaxUPA's reading would be wrong as a default,
// not merely off by default. This asserts that LaxUPA does not waive it.
func TestDocBookSameNameUPAMessageIsNotEvidenceOfABug(t *testing.T) {
	const src = `
	<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
	           xmlns:t="urn:t" targetNamespace="urn:t">
	  <xs:group name="g">
	    <xs:sequence><xs:element name="x" type="xs:string"/></xs:sequence>
	  </xs:group>
	  <xs:complexType name="ct">
	    <xs:choice>
	      <xs:element name="x" type="xs:string"/>
	      <xs:group ref="t:g"/>
	    </xs:choice>
	  </xs:complexType>
	</xs:schema>`

	err := checkSchema(t, src, CheckOptions{})
	if err == nil {
		t.Fatal("mgQ021 is this shape and is expected invalid")
	}
	if !strings.Contains(err.Error(), "cos-nonambig") {
		t.Errorf("want cos-nonambig, got: %v", err)
	}
	// Two distinct declarations, so sameDeclaration is false and the
	// permissive reading has nothing to waive.
	if err := checkSchema(t, src, CheckOptions{LaxUPA: true}); err == nil {
		t.Fatal("LaxUPA waives only two particles for ONE declaration")
	}
}
