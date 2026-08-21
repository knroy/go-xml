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
