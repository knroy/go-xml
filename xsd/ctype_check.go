package xsd

import (
	"github.com/knroy/go-xml/xdm"
)

// checkLocalTargetNamespace enforces src-attribute.6 and src-element.4, the
// XSD 1.1 restrictions on naming a foreign namespace from a local declaration.
//
// XSD 1.1 added a targetNamespace attribute to local <element> and <attribute>
// so a schema can place a component in a namespace other than its own. The
// feature exists for one narrow purpose: a restriction of a type imported from
// another namespace has to be able to name that type's children, which live in
// the other namespace. The spec therefore fences it in tightly, and the fence
// is what makes the feature safe — without it any schema could inject
// declarations into a namespace it does not own.
//
// The two constraints are worded identically (src-attribute.6 / src-element.4):
// given targetNamespace,
//
//	.1  name is present;
//	.2  form is absent;
//	.3  if the ancestor <schema> has no targetNamespace, or a different one,
//	    then .3.1 there is a <complexType> ancestor, and .3.2 a <restriction>
//	    sits between the declaration and that nearest <complexType>, whose
//	    base does not match xs:anyType.
//
// Clause .3 is the substantive one: it permits the foreign name only where it
// is restricting an existing foreign declaration, never where it would create
// one. Restricting xs:anyType is excluded because anyType has no declared
// children to restrict, so such a declaration would be an injection too.
//
// Pinned by ibmData S3_2_3: si03/si06 (form present), si04/si07 (no complexType
// ancestor), si01/si05/si08 (restriction of anyType or no restriction at all),
// si02 (extension, not restriction) and si09 (extension of an imported type).
// All nine loaded clean before this check existed.
func (p *parser) checkLocalTargetNamespace(el *xdm.Node, kind string) {
	tns := el.Attr("", "targetNamespace")
	if tns == nil {
		return
	}
	// The attribute is not in the 1.1 schema-for-schemas equivalent for 1.0,
	// where it is simply a foreign attribute in no namespace and ignored.
	if p.schema.Version < Version11 {
		return
	}

	code := "src-attribute"
	clause := "6"
	if kind == "element" {
		code = "src-element"
		clause = "4"
	}

	if el.Attr("", "name") == nil {
		p.errs = append(p.errs, errorAt(el, code+"."+clause+".1",
			"a targetNamespace on a local %s declaration requires a name", kind))
		return
	}
	if el.Attr("", "form") != nil {
		p.errs = append(p.errs, errorAt(el, code+"."+clause+".2",
			"form may not be combined with targetNamespace on a local %s "+
				"declaration; targetNamespace already fixes the namespace", kind))
	}

	// Naming the document's own target namespace is always allowed: it says
	// nothing that form="qualified" could not have said, so clause .3 — which
	// guards against reaching into a namespace the document does not own —
	// does not apply.
	if p.doc.hasTargetNS && p.doc.targetNS == tns.Value {
		return
	}

	// Walk out to the nearest <complexType>, remembering the last
	// <restriction> crossed on the way. Clause .3.2 wants a <restriction>
	// *between* the declaration and that complexType, so the search stops
	// where the complexType does.
	var restriction *xdm.Node
	var ct *xdm.Node
	for cur := el.Parent; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement || cur.Name.URI != NSSchema {
			continue
		}
		if cur.Name.Local == "complexType" {
			ct = cur
			break
		}
		if cur.Name.Local == "restriction" {
			restriction = cur
		}
	}

	if ct == nil {
		p.errs = append(p.errs, errorAt(el, code+"."+clause+".3.1",
			"a local %s declaration naming foreign namespace %q must appear "+
				"inside a complexType", kind, tns.Value))
		return
	}
	if restriction == nil {
		p.errs = append(p.errs, errorAt(el, code+"."+clause+".3.2",
			"a local %s declaration naming foreign namespace %q must appear "+
				"inside a restriction; only restricting an existing foreign "+
				"declaration may name another namespace", kind, tns.Value))
		return
	}

	// A restriction of xs:anyType restricts nothing that has children, so a
	// foreign name under it would be creating a declaration in that namespace
	// rather than restricting one.
	base := restriction.Attr("", "base")
	if base == nil {
		return
	}
	name, err := p.resolveQName(restriction, "base", base.Value)
	if err != nil {
		// An unresolvable base is reported by the normal base handling; do
		// not double-report it here.
		return
	}
	if name.URI == NSSchema && name.Local == "anyType" {
		p.errs = append(p.errs, errorAt(el, code+"."+clause+".3.2",
			"a local %s declaration naming foreign namespace %q may not appear "+
				"in a restriction of xs:anyType, which has no declaration in "+
				"that namespace to restrict", kind, tns.Value))
	}
}

// checkComplexDerivationFinal enforces cos-ct-extends.1.1 and
// derivation-ok-restriction.1: a complex type may not be derived by a method
// its base type's {final} forbids.
//
// {final} is the author's statement that a type is a leaf for a given
// derivation method — final="extension" means no one may extend it,
// final="restriction" no one may restrict it, final="#all" neither. The
// property was parsed into FinalSet and carried on every ComplexType, but
// nothing ever read it back for complex-type derivation, so every such
// declaration was inert: a type marked final could still be derived from
// freely.
//
// Pinned by msData/complexType ctI006 and ctI011 (final="#all" and
// final="extension" then derived), ctN002 (final="extension", extended) and
// ctO002/ctO005 (final="restriction", restricted). All loaded clean before
// this.
func (p *parser) checkComplexDerivationFinal(t *ComplexType) {
	base, ok := t.Base.(*ComplexType)
	if !ok || base == t {
		return
	}
	switch t.DerivationMethod {
	case DerivationExtension:
		if base.Final().Has(DerivationExtension) {
			p.errs = append(p.errs, errorAt(nil, "cos-ct-extends.1.1",
				"complex type %q may not extend %q, whose final attribute "+
					"forbids extension", t.Name, base.Name))
		}
	case DerivationRestriction:
		if base.Final().Has(DerivationRestriction) {
			p.errs = append(p.errs, errorAt(nil, "derivation-ok-restriction.1",
				"complex type %q may not restrict %q, whose final attribute "+
					"forbids restriction", t.Name, base.Name))
		}
	}
}

// checkContentDerivationForm enforces src-ct.1 and src-ct.2 (§3.4.3), the two
// constraints on what a <complexContent> or <simpleContent> may name as its
// base.
//
// The two are the schema-form counterpart of the derivation rules: they say
// which *shape* of base each content form may sit on, before any question of
// whether the derivation itself is legal. Nothing read them back, so a schema
// could extend a simple type through <complexContent> and add a content model
// to it, or restrict xs:string through <simpleContent>, and load clean.
//
// src-ct.1: with <complexContent>, the base must be a complex type definition.
// A simple type has no content model to extend or restrict.
//
// src-ct.2.1: with <simpleContent>, the base must be one of
//
//	.1 a complex type whose {content type} is a simple type definition; or
//	.2 a complex type whose {content type} is mixed with an emptiable
//	   particle, and the derivation is *restriction* (and then src-ct.2.2
//	   requires a <simpleType> among the restriction's children, which is
//	   what supplies the derived content type); or
//	.3 a simple type definition, and the derivation is *extension*.
//
// The source form is recoverable from the component: readSimpleContent is the
// only path that sets {content type} to ContentSimple, so Content ==
// ContentSimple says <simpleContent> was written, and a type whose base is not
// xs:anyType-by-default with any other content came through readComplexContent.
//
// Pinned by msData/complexType ctJ002 and ctJ003 (complexContent extension of
// a simple type, src-ct.1), ctD001 and ctM001 (simpleContent *restriction* of
// a simple type, 2.1.3 wants extension), ctE003 and ctE004 (simpleContent
// extension of a complex type with element-only and with mixed content,
// neither 2.1.1 nor 2.1.2), ctK002 (simpleContent extension of a mixed
// emptiable type, which is 2.1.2's shape but restriction-only) and ctD004
// (simpleContent restriction of xs:anyType with no simpleType child, so
// src-ct.2.2 has nothing to build the content type from). All eight loaded
// clean before this check existed.
func (p *parser) checkContentDerivationForm(t *ComplexType) {
	if t == nil || t.Name.URI == NSSchema {
		return
	}
	base := t.Base
	if base == nil || base == Type(t) {
		return
	}

	if t.Content == ContentSimple {
		switch b := base.(type) {
		case *SimpleType:
			// 2.1.3: a simple type base is permitted only by extension.
			if t.DerivationMethod != DerivationExtension {
				p.errs = append(p.errs, errorAt(nil, "src-ct.2.1",
					"simpleContent restriction of %q is not permitted: a "+
						"simple type may only be the base of an extension",
					b.Name))
			}
		case *ComplexType:
			if b.Content == ContentSimple {
				return // 2.1.1
			}
			// 2.1.2: mixed with an emptiable particle, by restriction,
			// and with a simpleType child to name the derived content.
			if t.DerivationMethod != DerivationRestriction {
				p.errs = append(p.errs, errorAt(nil, "src-ct.2.1",
					"simpleContent extension of %q is not permitted: its "+
						"content type is not a simple type definition",
					b.Name))
				return
			}
			if b.Content != ContentMixed ||
				(b.Particle != nil && !particleEmptiable(b.Particle)) {
				p.errs = append(p.errs, errorAt(nil, "src-ct.2.1",
					"simpleContent restriction of %q is not permitted: its "+
						"content type is neither a simple type definition nor "+
						"mixed content with an emptiable particle", b.Name))
				return
			}
			if t.SimpleContent == nil {
				p.errs = append(p.errs, errorAt(nil, "src-ct.2.2",
					"a simpleContent restriction of the mixed type %q must "+
						"have a simpleType child to name its content type",
					b.Name))
			}
		}
		return
	}

	// src-ct.1: <complexContent> demands a complex base. A type written
	// without either wrapper takes xs:anyType as its base and is not
	// reached here, because that base is a complex type.
	if b, ok := base.(*SimpleType); ok {
		p.errs = append(p.errs, errorAt(nil, "src-ct.1",
			"complexContent %s of %q is not permitted: the base of a "+
				"complexContent must be a complex type definition",
			derivationWord(t.DerivationMethod), b.Name))
	}
}

func derivationWord(d Derivation) string {
	if d == DerivationExtension {
		return "extension"
	}
	return "restriction"
}

// checkMixedConsistency enforces cos-ct-extends.1.4.3.2.2.1 and
// derivation-ok-restriction.5.4.1.2: a derived complex type's content must be
// mixed if and only if its base's is.
//
// Mixedness is not a property a derivation may change. An extension appends to
// the base's content model, so making the result mixed would let character
// data appear between the *base's* own children — content the base forbids,
// in an instance the base's own validator would reject, which is exactly what
// extension is not allowed to produce. A restriction may only narrow, and
// turning element-only content mixed widens it.
//
// The rule is conditioned on the base actually having a content model. Where
// the base is empty there is nothing to keep consistent: cos-ct-extends.1.4.3.2
// splits on that case first (clause 2.1 covers an empty base and imposes no
// mixedness condition), so an extension of an empty type is free to be mixed.
//
// Pinned by msData/complexType ctF006 (mixed restriction of an element-only
// base), ctF008 (mixed extension of an element-only base, no own particle) and
// ctF009 (the same with a particle of its own). All three loaded clean before
// this check existed.
func (p *parser) checkMixedConsistency(t *ComplexType) {
	if t == nil || t.Name.URI == NSSchema {
		return
	}
	base, ok := t.Base.(*ComplexType)
	if !ok || base == t || isUrType(base) {
		return
	}
	// Only the two content forms that have a content model take part.
	// Simple and empty content are governed by their own clauses.
	if t.Content != ContentMixed && t.Content != ContentElementOnly {
		return
	}
	if base.Content != ContentMixed && base.Content != ContentElementOnly {
		return
	}
	if (t.Content == ContentMixed) == (base.Content == ContentMixed) {
		return
	}
	// The two derivation methods are not symmetric, and reading them as one
	// "must match" rule cost three schemas that are fine: restricting a
	// mixed base to element-only content is a *narrowing* — it takes away
	// the character data the base allowed — and derivation-ok-restriction
	// permits it. What clause 5.4.1.2 forbids is the other direction, a
	// mixed restriction of an element-only base, which would admit content
	// the base rejects. addB150, ctZ010h, particlesL012 and cta0001 are all
	// the permitted direction.
	if t.DerivationMethod == DerivationRestriction {
		if base.Content == ContentMixed {
			return
		}
		p.errs = append(p.errs, errorAt(nil, "derivation-ok-restriction.5.4.1.2",
			"complex type %q restricts %q to mixed content, but the base's "+
				"content is element-only", t.Name, base.Name))
		return
	}
	// An extension is an iff: cos-ct-extends.1.4.3.2.2.1 requires the two
	// content types to be both mixed or both element-only. An extension
	// appends to the base's model, so either direction would change how the
	// base's own children may be interleaved with character data.
	p.errs = append(p.errs, errorAt(nil, "cos-ct-extends.1.4.3.2.2.1",
		"complex type %q has %s content but its base %q has %s content: an "+
			"extension may not change whether content is mixed",
		t.Name, contentWord(t.Content), base.Name, contentWord(base.Content)))
}

func contentWord(c ContentKind) string {
	if c == ContentMixed {
		return "mixed"
	}
	return "element-only"
}

// checkAttributeWildcardRestriction enforces derivation-ok-restriction.4
// (§3.4.6), which defers to Wildcard Subset (§3.10.6): where a restriction
// declares an attribute wildcard, the base must have one, and the derived
// wildcard must be an intensional subset of it.
//
// A restriction may only narrow what its base accepts. An <xs:anyAttribute>
// on a restriction of a base that has none admits attributes the base type
// rejects outright, and one whose namespace constraint reaches wider than the
// base's does the same for the namespaces it adds — in both cases an instance
// valid against the derived type is invalid against the base, which is what
// restriction exists to make impossible.
//
// This runs before inheritAttributesNow, which is where the wildcard would be
// combined with the base's for an extension. A restriction never inherits its
// base's wildcard, so t.AttributeWildcard here is exactly what the schema
// document wrote — which is the value the constraint is about.
//
// Pinned by msData/complexType ctO005 (a restriction declaring
// anyAttribute="##other" over a base with no wildcard at all) and ctO007
// (##any over a base's ##other, where the derived wildcard is a strict
// superset). Both loaded clean before this check existed.
func (p *parser) checkAttributeWildcardRestriction(t *ComplexType) {
	if t == nil || t.Name.URI == NSSchema ||
		t.DerivationMethod != DerivationRestriction ||
		t.AttributeWildcard == nil {
		return
	}
	base, ok := t.Base.(*ComplexType)
	if !ok || base == t || isUrType(base) {
		return
	}
	if base.AttributeWildcard == nil {
		p.errs = append(p.errs, errorAt(nil, "derivation-ok-restriction.4.1",
			"complex type %q declares an attribute wildcard but its base %q "+
				"has none, so the wildcard cannot be a restriction of anything",
			t.Name, base.Name))
		return
	}
	if !wildcardSubset(t.AttributeWildcard, base.AttributeWildcard) {
		p.errs = append(p.errs, errorAt(nil, "derivation-ok-restriction.4.2",
			"the attribute wildcard of complex type %q is not a subset of the "+
				"attribute wildcard of its base %q", t.Name, base.Name))
	}
}

// checkLocalSimpleTypeForm is the simple-type half of the local-form rule
// already enforced for complex types (§3.14.2).
//
// The schema for schemas declares a <xs:simpleType> that is not a child of
// <xs:schema> or <xs:redefine> as a *localSimpleType*, on which name and final
// are prohibited. A name on a local simple type is the reading that misleads:
// it looks like a global definition and is not one, so nothing can ever refer
// to it by that name, and a schema author who writes it has written a
// definition that silently does nothing.
//
// Pinned by msData/simpleType stA008, stA009 and stA010 — the same inline
// <xs:simpleType name="fooType"> inside a restriction, a list and a union.
// All three loaded clean before this check existed.
func (p *parser) checkLocalSimpleTypeForm(el *xdm.Node) {
	if topLevelType(el) {
		return
	}
	for _, a := range []string{"name", "final"} {
		if el.Attr("", a) != nil {
			p.errs = append(p.errs, errorAt(el, "src-simple-type",
				"%s is not permitted on a simpleType that is not a child "+
					"of schema or redefine", a))
		}
	}
}
