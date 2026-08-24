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

	// elements, attributes and notations are only ever displaced by an
	// xs:override: xs:redefine may replace types and groups alone, while
	// override may replace any global component.
	elements   map[xdm.QName]*ElementDecl
	attributes map[xdm.QName]*AttributeDecl
	notations  map[xdm.QName]*NotationDecl
}

// checkRedefine enforces the Schema Representation Constraints on the children
// of an <xs:redefine> (§4.2.2, "Individual Component Redefinition").
//
// It runs before the redefinition is read, while the redefined document's
// components are still installed under their own names — which is what makes
// clauses 6.2.1 and 7.2.1 ("the name must resolve in the redefined document")
// answerable at all.
//
// The clauses implemented here are the ones that are decidable from the source
// form alone. Clause 6.2.2 and 7.2.2 — that a redefinition without a
// self-reference must be a valid restriction of the original — are a derivation
// check, and live with the derivation machinery instead.
func (a *assembler) checkRedefine(el *xdm.Node, doc *schemaDoc) {
	// §4.2.2 clause 2 makes the children of one redefine a set of
	// *distinct* redefinitions: two children redefining the same name say
	// two different things about one component, and the second silently
	// replacing the first is exactly the misreading to refuse (schT2).
	seen := map[string]bool{}
	for _, c := range el.ChildElements() {
		if c.Name.URI != NSSchema {
			continue
		}
		switch c.Name.Local {
		case "simpleType", "complexType", "group", "attributeGroup":
		default:
			continue
		}
		key := c.Name.Local + " " + c.AttrValue("name")
		if c.Name.Local == "simpleType" || c.Name.Local == "complexType" {
			// Types share one symbol space.
			key = "type " + c.AttrValue("name")
		}
		if seen[key] {
			a.p.errs = append(a.p.errs, errorAt(c, "src-redefine.2",
				"the redefine has more than one child redefining %q",
				c.AttrValue("name")))
		}
		seen[key] = true
	}

	for _, c := range el.ChildElements() {
		if c.Name.URI != NSSchema {
			continue
		}
		name := c.AttrValue("name")
		if name == "" {
			continue
		}
		self := xdm.QName{URI: doc.targetNS, Local: name}

		switch c.Name.Local {
		case "simpleType", "complexType":
			// src-redefine.5: a type definition among the children
			// of a redefine must have a <restriction> or
			// <extension> whose base is the type itself. That is
			// what makes it a *redefinition* rather than an
			// unrelated declaration wearing the same name.
			base, ok := a.redefineBase(c)
			if !ok {
				a.p.errs = append(a.p.errs, errorAt(c, "src-redefine.5",
					"the %s %q inside a redefine must derive by "+
						"restriction or extension", c.Name.Local, name))
				continue
			}
			q, err := a.p.resolveQName(c, "base", base)
			if err != nil {
				a.p.errs = append(a.p.errs, err)
				continue
			}
			if q != self {
				a.p.errs = append(a.p.errs, errorAt(c, "src-redefine.5",
					"the %s %q inside a redefine must have base %q, not %q",
					c.Name.Local, name, self.Local, base))
			}

		case "group":
			// src-redefine.6.1.1: at most one self-reference.
			// src-redefine.6.1.2: that self-reference must have
			// minOccurs = maxOccurs = 1 — a redefinition that could
			// repeat or omit itself would not name a fixed
			// component.
			// src-redefine.6.2.1: with no self-reference, the name
			// must be one the redefined document actually defines.
			refs := selfGroupRefs(c, doc, a.p, self, "group")
			if len(refs) > 1 {
				a.p.errs = append(a.p.errs, errorAt(c, "src-redefine.6.1.1",
					"the group %q inside a redefine refers to itself "+
						"%d times; at most one is allowed", name, len(refs)))
				continue
			}
			if len(refs) == 1 {
				r := refs[0]
				if r.AttrValue("minOccurs") != "" && r.AttrValue("minOccurs") != "1" ||
					r.AttrValue("maxOccurs") != "" && r.AttrValue("maxOccurs") != "1" {
					a.p.errs = append(a.p.errs, errorAt(r, "src-redefine.6.1.2",
						"the self-reference in the redefinition of group %q "+
							"must have minOccurs = maxOccurs = 1", name))
				}
				continue
			}
			if !a.definedIn(el, "group", name) {
				a.p.errs = append(a.p.errs, errorAt(c, "src-redefine.6.2.1",
					"the group %q is not defined in the redefined schema", name))
			}

		case "attributeGroup":
			// src-redefine.7.1: at most one self-reference.
			// src-redefine.7.2.1: with none, the name must be one
			// the redefined document defines.
			refs := selfGroupRefs(c, doc, a.p, self, "attributeGroup")
			if len(refs) > 1 {
				a.p.errs = append(a.p.errs, errorAt(c, "src-redefine.7.1",
					"the attribute group %q inside a redefine refers to "+
						"itself %d times; at most one is allowed",
					name, len(refs)))
				continue
			}
			if len(refs) == 1 {
				continue
			}
			if !a.definedIn(el, "attributeGroup", name) {
				a.p.errs = append(a.p.errs, errorAt(c, "src-redefine.7.2.1",
					"the attribute group %q is not defined in the "+
						"redefined schema", name))
			}
		}
	}
}

// redefineBase returns the base= of the single derivation step a redefined type
// definition must contain, and whether one was found.
//
// A simple type carries <restriction> directly; a complex type carries it under
// <simpleContent> or <complexContent>, and a complex type written with a bare
// content model has no derivation step at all (schK3).
func (a *assembler) redefineBase(c *xdm.Node) (string, bool) {
	for _, k := range c.ChildElements() {
		if k.Name.URI != NSSchema {
			continue
		}
		switch k.Name.Local {
		case "restriction", "extension":
			return k.AttrValue("base"), true
		case "simpleContent", "complexContent":
			for _, d := range k.ChildElements() {
				if d.Name.URI == NSSchema &&
					(d.Name.Local == "restriction" || d.Name.Local == "extension") {
					return d.AttrValue("base"), true
				}
			}
		}
	}
	return "", false
}

// selfGroupRefs collects the <group ref> or <attributeGroup ref> elements
// beneath a redefinition that name the redefinition itself.
//
// The search is over the whole subtree because §4.2.2 counts self-references
// wherever they appear, not only as immediate children: schR3 buries one in
// the middle of a <choice>.
func selfGroupRefs(c *xdm.Node, doc *schemaDoc, p *parser, self xdm.QName, kind string) []*xdm.Node {
	var out []*xdm.Node
	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		for _, k := range n.ChildElements() {
			if k.IsElement(NSSchema, kind) {
				if ref := k.AttrValue("ref"); ref != "" {
					if q, err := p.resolveQName(k, "ref", ref); err == nil && q == self {
						out = append(out, k)
					}
				}
			}
			walk(k)
		}
	}
	walk(c)
	return out
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
		elements:   map[xdm.QName]*ElementDecl{},
		attributes: map[xdm.QName]*AttributeDecl{},
		notations:  map[xdm.QName]*NotationDecl{},
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

		// An xs:override may replace any global component; xs:redefine
		// reaches only the four above. Without displacing these, the
		// replacement collides with the original and the whole schema
		// fails to load on a duplicate declaration.
		case "element":
			if d, ok := a.schema.Elements[q]; ok {
				hold.elements[q] = d
				delete(a.schema.Elements, q)
			}
		case "attribute":
			if d, ok := a.schema.Attributes[q]; ok {
				hold.attributes[q] = d
				delete(a.schema.Attributes, q)
			}
		case "notation":
			if d, ok := a.schema.Notations[q]; ok {
				hold.notations[q] = d
				delete(a.schema.Notations, q)
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

// definedIn reports whether the document a redefine names declares a global of
// the given kind and name.
//
// The search follows the redefined document's own includes and redefines,
// because §4.2.1 makes an included document's components components of the
// including one: a redefine may legitimately name something the document it
// points at got from elsewhere. Imports are not followed — those bring in a
// different namespace, which a redefine cannot reach.
func (a *assembler) definedIn(redefine *xdm.Node, kind, name string) bool {
	root, ok := a.redefined[redefine]
	if !ok {
		// The location did not resolve. Nothing is known about what it
		// declares, so nothing is asserted about it either: a rule
		// cannot fire on a document that was never read.
		return true
	}
	seen := map[*xdm.Node]bool{}
	var look func(n *xdm.Node) bool
	look = func(n *xdm.Node) bool {
		if n == nil || seen[n] {
			return false
		}
		seen[n] = true
		if n.Kind == xdm.KindDocument {
			els := n.ChildElements()
			if len(els) == 0 {
				return false
			}
			n = els[0]
		}
		for _, c := range n.ChildElements() {
			if c.Name.URI != NSSchema {
				continue
			}
			if c.Name.Local == kind && c.AttrValue("name") == name {
				return true
			}
			if c.Name.Local == "include" || c.Name.Local == "redefine" {
				if look(a.redefined[c]) {
					return true
				}
			}
		}
		return false
	}
	return look(root)
}
