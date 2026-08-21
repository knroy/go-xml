package xsd

import (
	"github.com/knroy/go-xml/xdm"
)

// xs:redefine (§4.2.2).
//
// A redefine reads another document and replaces some of its components with
// new ones defined *in terms of the originals*. That circularity is the whole
// difficulty: inside <xs:redefine>, a type whose base is itself does not mean
// "derives from itself" but "derives from the definition being replaced".
//
// The spec resolves this by saying a redefine produces two components per
// redefined type: the original under an anonymous identity, and the
// redefinition under the name. This implements that directly — the included
// document's component is renamed out of the way before the redefinition is
// read, so the self-reference resolves to the original rather than to itself.

// redefineHold is a component displaced by a redefinition.
//
// It keeps the original under a key no schema document can write, which is what
// lets the self-reference inside <xs:redefine> resolve without the redefinition
// finding itself.
type redefineHold struct {
	types      map[xdm.QName]Type
	groups     map[xdm.QName]*ModelGroupDef
	attrGroups map[xdm.QName]*AttributeGroupDef
}

// prepareRedefine displaces the components a redefine is about to replace.
//
// It runs after the redefined document has been read and before the
// redefinition's own children are, which is the only window in which both the
// original and the replacement exist.
func (a *assembler) prepareRedefine(el *xdm.Node, doc *schemaDoc) *redefineHold {
	hold := &redefineHold{
		types:      map[xdm.QName]Type{},
		groups:     map[xdm.QName]*ModelGroupDef{},
		attrGroups: map[xdm.QName]*AttributeGroupDef{},
	}

	for _, c := range el.ChildElements() {
		if c.Name.URI != NSSchema {
			continue
		}
		name := c.AttrValue("name")
		if name == "" {
			continue
		}
		q := xdm.QName{URI: doc.targetNS, Local: name}

		switch c.Name.Local {
		case "simpleType", "complexType":
			if t, ok := a.schema.Types[q]; ok {
				hold.types[q] = t
				delete(a.schema.Types, q)
			}
		case "group":
			if g, ok := a.schema.ModelGroups[q]; ok {
				hold.groups[q] = g
				delete(a.schema.ModelGroups, q)
			}
		case "attributeGroup":
			if g, ok := a.schema.AttributeGroups[q]; ok {
				hold.attrGroups[q] = g
				delete(a.schema.AttributeGroups, q)
			}
		}
	}
	return hold
}

// applyRedefine reads the redefining declarations with the originals in place.
//
// The ordering is the whole of what makes redefine work, and it is not the
// obvious one. References are resolved by fixups that run after every document
// has been read, so it is not enough to swap the components around while the
// redefinition is being *parsed* — what matters is which component the map
// holds when the fixups run.
//
// So the self-reference is bound eagerly instead: the redefining declaration is
// read while the original is still installed, and the type reference inside it
// is resolved immediately against that original rather than being deferred.
// Only then is the replacement installed under the name.
func (a *assembler) applyRedefine(el *xdm.Node, doc *schemaDoc, hold *redefineHold) {
	// Put the originals back so that the redefining declarations can bind
	// to them.
	for q, t := range hold.types {
		a.schema.Types[q] = t
	}
	for q, g := range hold.groups {
		a.schema.ModelGroups[q] = g
	}
	for q, g := range hold.attrGroups {
		a.schema.AttributeGroups[q] = g
	}

	prev := a.p.doc
	a.p.doc = doc

	// Each replacement is built with the original still installed, and the
	// fixups it registers are run immediately so that they bind to the
	// original rather than to whatever holds the name later.
	type built struct {
		q xdm.QName
		t Type
		g *ModelGroupDef
		a *AttributeGroupDef
	}
	var results []built

	for _, c := range el.ChildElements() {
		if c.Name.URI != NSSchema {
			continue
		}
		name := c.AttrValue("name")
		if name == "" {
			continue
		}
		q := xdm.QName{URI: doc.targetNS, Local: name}

		mark := len(a.p.fixups)
		var b built
		b.q = q
		switch c.Name.Local {
		case "simpleType":
			b.t = a.p.readSimpleType(c)
		case "complexType":
			b.t = a.p.readComplexType(c)
		case "group":
			b.g = a.p.readModelGroupDef(c)
		case "attributeGroup":
			b.a = a.p.readAttributeGroupDef(c)
		default:
			continue
		}

		// Run just the fixups this declaration registered, while the
		// original is still what the name resolves to.
		for _, fn := range a.p.fixups[mark:] {
			if err := fn(); err != nil {
				a.p.errs = append(a.p.errs, err)
			}
		}
		a.p.fixups = a.p.fixups[:mark]

		results = append(results, b)
	}

	// Install the replacements only now, so that references from outside
	// the redefine — which resolve later — find the new definitions.
	for _, b := range results {
		switch {
		case b.t != nil:
			a.schema.Types[b.q] = b.t
		case b.g != nil:
			a.schema.ModelGroups[b.q] = b.g
		case b.a != nil:
			// A redefined attribute group does not inherit: its uses
			// are exactly those written inside the redefinition.
			a.schema.AttributeGroups[b.q] = b.a
		}
	}
	a.p.doc = prev
}
