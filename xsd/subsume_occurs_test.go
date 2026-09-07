package xsd

import "testing"

// Occurrence ranges under XSD 1.1 subsumption.
//
// These pin the answer to a question the 1.1 subsumption path was once
// suspected of getting wrong: `particleSubsumes` decides Content type
// restricts (Complex Content) (§3.4.6.4) by language inclusion over element
// *names*, and `declCompatible` restores the clauses inclusion cannot see —
// nillability, fixed values, block, type derivation — but compares no
// occurrence range. docs/known-gaps.md carried an entry proposing that the
// missing range comparison was a hole, and that threading each step's particle
// into `declCompatible` and applying Occurrence Range OK would close it.
//
// It would not. XSD 1.1 deleted Occurrence Range OK. §3.9.6 retains only
// Particle Correct, Particle Valid (Extension) and Particle Emptiable; there is
// no Particle Valid (Restriction) and no `range-ok`, and Appendix B.4's
// constraint index lists `cos-particle-extend` with no restriction counterpart.
// §3.4.6.4 is two clauses, and clause 1 is exactly language inclusion: "Every
// sequence of element information items which is locally valid with respect to
// R is also locally valid with respect to B." A derived particle's range is
// constrained only through the sequences it admits, never by comparison against
// a base particle's range.
//
// The cases below are the ones that distinguish the two readings, because in
// each the derived range is *wider* than the base particle it steps against
// while the two languages stay equal.

// TestSubsumeSubstitutionMemberKeepsRange is the shape of MS-Element/elemZ026.
//
// The base names a substitution-group head with maxOccurs="unbounded"; the
// derived names a concrete member of that group, also unbounded. Under 1.0 the
// head is rewritten as a choice whose members carry unit occurrence — the
// choice keeps the parent's range — so Elt:Elt compares the derived
// {1,unbounded} against a member's {1,1} and Occurrence Range OK rejects. That
// rewrite is a 1.0 device; XSD 1.1 has no text for it, and the languages are
// equal: (a{1,1}){1,unbounded} and a{1,unbounded} accept exactly the same
// sequences.
//
// So 1.1 must accept, and does. 1.0 rejects, and that split is the point: a
// range clause added to declCompatible would drag the 1.0 artifact into the
// 1.1 path.
func TestSubsumeSubstitutionMemberKeepsRange(t *testing.T) {
	const schema = `<schema xmlns="http://www.w3.org/2001/XMLSchema" targetNamespace="http://t" xmlns:t="http://t">
  <complexType name="bt" abstract="true"><sequence/></complexType>
  <complexType name="rbt"><complexContent><restriction base="t:bt"><sequence/></restriction></complexContent></complexType>
  <element name="head" type="t:bt" abstract="true"/>
  <element name="mem" type="t:rbt" substitutionGroup="t:head"/>
  <complexType name="ct"><sequence><element ref="t:head" maxOccurs="unbounded"/></sequence></complexType>
  <complexType name="rct"><complexContent><restriction base="t:ct">
    <sequence><element ref="t:mem" maxOccurs="unbounded"/></sequence>
  </restriction></complexContent></complexType>
</schema>`

	if err := mustLoad(t, schema, Version11); err != nil {
		t.Errorf("XSD 1.1 rejected a restriction naming a substitution-group "+
			"member with the base particle's own range, whose language "+
			"equals the base's: %v", err)
	}
}

// TestSubsumeSubstitutionMemberKeepsRangeNonAbstract is the same restriction
// with a head that may itself appear.
//
// The abstract case above reaches declCompatible with a single declaration on
// each side; here the base admits the name through two declarations — the head
// and the member — so stepNFA reports none and the clauses it guards are
// skipped. Both must be accepted, and pinning the pair keeps a range clause
// from being reintroduced on either route.
func TestSubsumeSubstitutionMemberKeepsRangeNonAbstract(t *testing.T) {
	const schema = `<schema xmlns="http://www.w3.org/2001/XMLSchema" targetNamespace="http://t" xmlns:t="http://t">
  <complexType name="bt"><sequence/></complexType>
  <element name="head" type="t:bt"/>
  <element name="mem" type="t:bt" substitutionGroup="t:head"/>
  <complexType name="ct"><sequence><element ref="t:head" maxOccurs="unbounded"/></sequence></complexType>
  <complexType name="rct"><complexContent><restriction base="t:ct">
    <sequence><element ref="t:mem" maxOccurs="unbounded"/></sequence>
  </restriction></complexContent></complexType>
</schema>`

	if err := mustLoad(t, schema, Version11); err != nil {
		t.Errorf("XSD 1.1 rejected a restriction to one member of a "+
			"non-abstract substitution group: %v", err)
	}
}

// TestSubsumeWidenedRangeStillRejected is the direction a missing range
// comparison was feared to have opened.
//
// Here the derived model genuinely admits a sequence the base does not: the
// base allows at most two `a`, the derived allows three. No substitution group
// and no rewrite is involved, so the ranges are the only difference. Language
// inclusion sees it — the word "aaa" is accepted by the derived model and by
// nothing in the base — and reports the counterexample without ever comparing
// a bound to a bound.
//
// This is what makes the range clause unnecessary rather than merely harmful:
// inclusion already rejects every widening that changes the language, and a
// widening that does not change the language is not a violation.
func TestSubsumeWidenedRangeStillRejected(t *testing.T) {
	const schema = `<schema xmlns="http://www.w3.org/2001/XMLSchema" targetNamespace="http://t" xmlns:t="http://t">
  <element name="a" type="string"/>
  <complexType name="ct"><sequence><element ref="t:a" minOccurs="0" maxOccurs="2"/></sequence></complexType>
  <complexType name="rct"><complexContent><restriction base="t:ct">
    <sequence><element ref="t:a" minOccurs="0" maxOccurs="3"/></sequence>
  </restriction></complexContent></complexType>
</schema>`

	for _, v := range []Version{Version10, Version11} {
		if err := mustLoad(t, schema, v); err == nil {
			t.Errorf("version %v accepted a restriction whose maxOccurs=3 "+
				"admits a third element the base's maxOccurs=2 forbids", v)
		}
	}
}

// TestSubsumeUnboundedOverBoundedStillRejected is the unbounded form of the
// same widening, which is the one Occurrence Range OK spelled out as its own
// inequality.
//
// A derived maxOccurs="unbounded" against a bounded base is a real language
// widening whenever the base particle is not itself inside an unbounded
// repetition, and inclusion catches it for the same reason as above.
func TestSubsumeUnboundedOverBoundedStillRejected(t *testing.T) {
	const schema = `<schema xmlns="http://www.w3.org/2001/XMLSchema" targetNamespace="http://t" xmlns:t="http://t">
  <element name="a" type="string"/>
  <complexType name="ct"><sequence><element ref="t:a" minOccurs="0" maxOccurs="2"/></sequence></complexType>
  <complexType name="rct"><complexContent><restriction base="t:ct">
    <sequence><element ref="t:a" minOccurs="0" maxOccurs="unbounded"/></sequence>
  </restriction></complexContent></complexType>
</schema>`

	for _, v := range []Version{Version10, Version11} {
		if err := mustLoad(t, schema, v); err == nil {
			t.Errorf("version %v accepted a restriction whose unbounded "+
				"maxOccurs exceeds the base's 2", v)
		}
	}
}

// TestSubsumeNarrowedRangeAccepted is the legitimate restriction that the
// occurrence-range hole was originally reported against: MS-Element/elemZ026's
// inner type narrows maxOccurs from "unbounded" to 1.
//
// Narrowing is what a restriction is for, and both versions accept it. It is
// recorded here because the known-gaps entry named this narrowing as the false
// accept; it never was one, and a test that says so keeps the claim from being
// made a third time.
func TestSubsumeNarrowedRangeAccepted(t *testing.T) {
	const schema = `<schema xmlns="http://www.w3.org/2001/XMLSchema" targetNamespace="http://t" xmlns:t="http://t">
  <complexType name="bt" abstract="true">
    <sequence><element name="e" type="token" maxOccurs="unbounded"/></sequence>
  </complexType>
  <complexType name="rbt"><complexContent><restriction base="t:bt">
    <sequence><element name="e" type="token" maxOccurs="1"/></sequence>
  </restriction></complexContent></complexType>
</schema>`

	for _, v := range []Version{Version10, Version11} {
		if err := mustLoad(t, schema, v); err != nil {
			t.Errorf("version %v rejected a restriction narrowing maxOccurs "+
				"from unbounded to 1: %v", v, err)
		}
	}
}
