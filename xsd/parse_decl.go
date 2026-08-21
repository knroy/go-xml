package xsd

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// readElementDecl reads an <xs:element>.
//
// The same element serves two roles: at the top level it declares a global
// element, and inside a content model it is a particle. This reads the
// declaration; readParticle wraps it with occurrence bounds.
func (p *parser) readElementDecl(el *xdm.Node, scope Scope) *ElementDecl {
	d := &ElementDecl{Scope: scope}

	name := el.AttrValue("name")
	if name != "" {
		switch {
		case scope == ScopeLocal && el.Attr("", "targetNamespace") != nil:
			// XSD 1.1 lets a local declaration name its own
			// namespace, which is how a schema declares a component
			// belonging somewhere other than its own target
			// namespace without importing one. It overrides form
			// and elementFormDefault, both of which only choose
			// between the target namespace and none.
			d.Name = xdm.QName{
				URI:   el.AttrValue("targetNamespace"),
				Local: name,
			}
		case scope == ScopeGlobal || formQualified(el, p.doc.elementFormQualified):
			d.Name = p.qnameFor(name)
		default:
			// An unqualified local element is in the absent namespace,
			// whatever the document's target namespace is. This is what
			// elementFormDefault controls, and getting it backwards
			// makes every local element unmatchable.
			d.Name = xdm.QName{Local: name}
		}
	}

	d.Nillable = p.boolAttr(el, "nillable", false)
	d.Abstract = p.boolAttr(el, "abstract", false)
	d.Constraint = p.valueConstraint(el)

	block, err := p.derivationSet(el, "block")
	if err != nil {
		p.errs = append(p.errs, err)
	} else if el.Attr("", "block") != nil {
		d.DisallowedSubstitutions = block
	} else {
		d.DisallowedSubstitutions = p.doc.blockDefault
	}

	final, err := p.derivationSet(el, "final")
	if err != nil {
		p.errs = append(p.errs, err)
	} else if el.Attr("", "final") != nil {
		d.SubstitutionGroupExclusions = final
	} else {
		d.SubstitutionGroupExclusions = p.doc.finalDefault
	}

	// The type is either named by the type attribute or given inline, and
	// never both — src-element.3.
	typeAttr := el.Attr("", "type")
	inline := p.childElement(el, "simpleType", "complexType")
	switch {
	case typeAttr != nil && inline != nil:
		p.errs = append(p.errs, errorAt(el, "src-element.3",
			"an element declaration may not have both a type attribute "+
				"and an inline type definition"))
	case typeAttr != nil:
		p.resolveTypeRef(el, typeAttr.Value, func(t Type) { d.Type = t })
	case inline != nil:
		if inline.Name.Local == "simpleType" {
			d.Type = p.readSimpleType(inline)
		} else {
			d.Type = p.readComplexType(inline)
		}
	default:
		// No type: the declaration takes its substitution group head's
		// type if it has one, and xs:anyType otherwise. The head may not
		// be resolved yet, so this is deferred.
		p.fixups = append(p.fixups, func() error {
			if d.Type != nil {
				return nil
			}
			if d.SubstitutionGroup != nil && d.SubstitutionGroup.Type != nil {
				d.Type = d.SubstitutionGroup.Type
			} else {
				d.Type = p.schema.anyType()
			}
			return nil
		})
	}

	if sg := el.AttrValue("substitutionGroup"); sg != "" {
		if scope != ScopeGlobal {
			p.errs = append(p.errs, errorAt(el, "src-element.3",
				"only a top-level element declaration may have a "+
					"substitutionGroup"))
		} else {
			// XSD 1.1 permits a list of heads; 1.0 permits one. The
			// list form is parsed either way, because a schema using
			// it is not made valid by reading only the first name.
			for _, one := range splitFields(sg) {
				name, err := p.resolveQName(el, "substitutionGroup", one)
				if err != nil {
					p.errs = append(p.errs, err)
					continue
				}
				ref := one
				p.fixups = append(p.fixups, func() error {
					head, ok := p.schema.Elements[name]
					if !ok {
						return errorAt(el, "src-resolve",
							"substitutionGroup %q names no element declaration",
							ref)
					}
					if d.SubstitutionGroup == nil {
						d.SubstitutionGroup = head
					}
					d.SubstitutionGroups = append(d.SubstitutionGroups, head)
					return nil
				})
			}
		}
	}

	for _, c := range p.contentChildren(el) {
		if c.Name.URI != NSSchema {
			continue
		}
		switch c.Name.Local {
		case "key", "keyref", "unique":
			if ic := p.readIdentityConstraint(c); ic != nil {
				d.IdentityConstraints = append(d.IdentityConstraints, ic)
			}
		case "alternative":
			// XSD 1.1 conditional type assignment. Order matters:
			// the first alternative whose test holds wins.
			if alt := p.readAlternative(c); alt != nil {
				d.Alternatives = append(d.Alternatives, alt)
			}
		}
	}

	return d
}

// readAttributeDecl reads an <xs:attribute> as a declaration.
func (p *parser) readAttributeDecl(el *xdm.Node, scope Scope) *AttributeDecl {
	d := &AttributeDecl{Scope: scope}

	name := el.AttrValue("name")
	if name != "" {
		switch {
		case scope == ScopeLocal && el.Attr("", "targetNamespace") != nil:
			// As for elements: XSD 1.1 lets a local attribute name
			// the namespace it belongs to directly.
			d.Name = xdm.QName{
				URI:   el.AttrValue("targetNamespace"),
				Local: name,
			}
		case scope == ScopeGlobal || formQualified(el, p.doc.attributeFormQualified):
			d.Name = p.qnameFor(name)
		default:
			d.Name = xdm.QName{Local: name}
		}
	}
	d.Constraint = p.valueConstraint(el)

	typeAttr := el.Attr("", "type")
	inline := p.childElement(el, "simpleType")
	switch {
	case typeAttr != nil && inline != nil:
		p.errs = append(p.errs, errorAt(el, "src-attribute.4",
			"an attribute declaration may not have both a type attribute "+
				"and an inline simpleType"))
	case typeAttr != nil:
		p.resolveTypeRef(el, typeAttr.Value, func(t Type) {
			st, ok := t.(*SimpleType)
			if !ok {
				p.errs = append(p.errs, errorAt(el, "src-resolve",
					"attribute type %q is a complex type; attributes must "+
						"have a simple type", typeAttr.Value))
				return
			}
			d.Type = st
		})
	case inline != nil:
		d.Type = p.readSimpleType(inline)
	default:
		d.Type = p.schema.anySimpleType()
	}

	return d
}

// readAttributeUse reads an <xs:attribute> inside a complex type or attribute
// group, where it is an attribute *use* rather than a declaration.
//
// A use with ref= points at a global declaration; a use with name= carries an
// inline local one. The two forms are mutually exclusive.
func (p *parser) readAttributeUse(el *xdm.Node) *AttributeUse {
	use := &AttributeUse{}

	switch el.AttrValue("use") {
	case "required":
		use.Required = true
	case "prohibited":
		// A prohibited use removes an inherited attribute. It is kept
		// in the list, marked, so that inheritance can tell "this name
		// was ruled out" from "this name was never mentioned" —
		// dropping it entirely let the base's use be inherited straight
		// back, which is the opposite of prohibiting.
		use.Prohibited = true
	case "", "optional":
	default:
		p.errs = append(p.errs, errorAt(el, "",
			"use=%q is not one of required, optional or prohibited",
			el.AttrValue("use")))
	}

	use.Constraint = p.valueConstraint(el)
	use.Inheritable = p.boolAttr(el, "inheritable", false)

	if ref := el.AttrValue("ref"); ref != "" {
		if el.AttrValue("name") != "" {
			p.errs = append(p.errs, errorAt(el, "src-attribute.3.1",
				"an attribute use may not have both ref and name"))
			return nil
		}
		name, err := p.resolveQName(el, "ref", ref)
		if err != nil {
			p.errs = append(p.errs, err)
			return nil
		}
		p.fixups = append(p.fixups, func() error {
			decl, ok := p.schema.Attributes[name]
			if !ok {
				return errorAt(el, "src-resolve",
					"attribute ref %q names no attribute declaration", ref)
			}
			use.Decl = decl
			return nil
		})
		return use
	}

	use.Decl = p.readAttributeDecl(el, ScopeLocal)
	return use
}

// readNotation reads an <xs:notation>.
func (p *parser) readNotation(el *xdm.Node) *NotationDecl {
	name := el.AttrValue("name")
	if name == "" {
		p.errs = append(p.errs, errorAt(el, "",
			"a notation declaration must have a name"))
		return nil
	}
	return &NotationDecl{
		Name:   p.qnameFor(name),
		Public: el.AttrValue("public"),
		System: el.AttrValue("system"),
	}
}

// readAttributeGroupDef reads a top-level <xs:attributeGroup>.
func (p *parser) readAttributeGroupDef(el *xdm.Node) *AttributeGroupDef {
	name := el.AttrValue("name")
	if name == "" {
		p.errs = append(p.errs, errorAt(el, "",
			"a top-level attributeGroup must have a name"))
		return nil
	}
	g := &AttributeGroupDef{Name: p.qnameFor(name)}
	p.readAttributes(el, &g.AttributeUses, &g.AttributeWildcard, g)
	// A prohibited use is not one of an attribute group's attribute uses,
	// so it contributes nothing and removes nothing — only a use written
	// directly on the type does. The suite states it in those words:
	// "a prohibited attribute should not be in the attribute uses of an
	// attributeGroup", and attZ015 pins it with a group prohibiting an
	// attribute its base declares, against an instance that still carries
	// it and is expected valid.
	g.AttributeUses = dropProhibited(g.AttributeUses)
	return g
}

// readAttributes reads the attribute uses, attribute group references and
// attribute wildcard of a complex type or attribute group.
//
// References to attribute groups are flattened into the containing component's
// uses, which is what the spec's {attribute uses} property holds: a set of
// uses, with no record of which group they arrived through.
//
// The target is passed in rather than returned because a group may be defined
// after the reference to it, so the flattening happens in a fixup that runs
// later. Returning a slice and letting the caller assign it would leave the
// fixup appending to a variable nobody reads — which is exactly the bug this
// signature exists to prevent, and which silently dropped every
// attribute-group attribute.
// owner is the attribute group definition being read, or nil when the
// attributes belong to a complex type. A reference inside a group definition is
// recorded as an edge rather than flattened, so that the graph can be walked
// once every fixup has run — see groupUses.
func (p *parser) readAttributes(el *xdm.Node, target *[]*AttributeUse, into **Wildcard, owner *AttributeGroupDef) {
	var wildcard *Wildcard

	for _, c := range p.contentChildren(el) {
		if c.Name.URI != NSSchema {
			continue
		}
		switch c.Name.Local {
		case "attribute":
			if u := p.readAttributeUse(c); u != nil {
				*target = append(*target, u)
			}

		case "attributeGroup":
			ref := c.AttrValue("ref")
			if ref == "" {
				p.errs = append(p.errs, errorAt(c, "src-attribute_group.1",
					"an attributeGroup reference must have a ref"))
				continue
			}
			name, err := p.resolveQName(c, "ref", ref)
			if err != nil {
				p.errs = append(p.errs, err)
				continue
			}
			p.fixups = append(p.fixups, func() error {
				g, ok := p.schema.AttributeGroups[name]
				if !ok {
					return errorAt(c, "src-resolve",
						"attributeGroup ref %q names no attribute group", ref)
				}
				if owner != nil {
					// Inside another group definition:
					// record the edge and let the graph
					// walk resolve it later.
					owner.refs = append(owner.refs, g)
					return nil
				}
				// The graph is read in a second pass. The edges
				// between groups are themselves resolved by
				// fixups, and this one may run before them —
				// so reading now would see a group that has not
				// finished referencing the groups it names.
				p.fixups = append(p.fixups, func() error {
					*target = append(*target, groupUses(g, nil)...)
					// A referenced group's wildcard is the
					// containing component's too,
					// intersected with whatever else it
					// has. Dropping it left a type whose
					// only wildcard came through a group
					// with none at all.
					if w := groupWildcard(g, nil); w != nil {
						*into = intersectWildcards(*into, w)
					}
					return nil
				})
				return nil
			})

		case "anyAttribute":
			// Several attribute groups may each contribute a
			// wildcard, and the type's wildcard is their
			// *intersection* (§3.10.6 Attribute Wildcard
			// Intersection) — an attribute must satisfy all of them.
			// Keeping only the last one accepts attributes every
			// group but that one excluded.
			wildcard = intersectWildcards(wildcard, p.readWildcard(c))
		}
	}
	if wildcard != nil {
		*into = intersectWildcards(*into, wildcard)
	}
}

// readWildcard reads an <xs:any> or <xs:anyAttribute>.
func (p *parser) readWildcard(el *xdm.Node) *Wildcard {
	w := &Wildcard{}

	switch el.AttrValue("processContents") {
	case "", "strict":
		w.ProcessContents = ProcessStrict
	case "lax":
		w.ProcessContents = ProcessLax
	case "skip":
		w.ProcessContents = ProcessSkip
	default:
		p.errs = append(p.errs, errorAt(el, "",
			"processContents=%q is not one of strict, lax or skip",
			el.AttrValue("processContents")))
	}

	// XSD 1.1 notNamespace: the complement of a namespace list, which 1.0
	// could only express for a single namespace with ##other.
	if not := el.AttrValue("notNamespace"); not != "" {
		w.Kind = NSNot
		for _, word := range splitFields(not) {
			switch word {
			case "##targetNamespace":
				if p.doc.targetNS == "" {
					// In a no-namespace schema the target
					// namespace *is* the absent one, so
					// ##targetNamespace excludes unqualified
					// names. Appending "" to the list would
					// not do it: Allows answers the absent
					// namespace from ExcludesAbsent and
					// never reaches the list.
					w.ExcludesAbsent = true
					continue
				}
				w.Namespace = append(w.Namespace, p.doc.targetNS)
			case "##local":
				// Only an explicit ##local excludes unqualified
				// names from a notNamespace wildcard, unlike
				// ##other which excludes them always.
				w.ExcludesAbsent = true
			default:
				w.Namespace = append(w.Namespace, word)
			}
		}
		p.readDisallowedNames(el, w)
		return w
	}

	ns := el.AttrValue("namespace")
	if ns == "" {
		ns = "##any"
	}
	switch ns {
	case "##any":
		w.Kind = NSAny
	case "##other":
		// ##other is "not the target namespace", and by clause 2.3 of
		// Wildcard allows Namespace Name it excludes unqualified names
		// as well — which is what ExcludesAbsent records, and what
		// distinguishes it from XSD 1.1's notNamespace.
		w.Kind = NSNot
		w.Namespace = []string{p.doc.targetNS}
		w.ExcludesAbsent = true
	default:
		w.Kind = NSEnumerated
		for _, word := range splitFields(ns) {
			switch word {
			case "##targetNamespace":
				w.Namespace = append(w.Namespace, p.doc.targetNS)
			case "##local":
				// ##local is the absent namespace, spelled as the
				// empty string in the enumerated set.
				w.Namespace = append(w.Namespace, "")
			default:
				w.Namespace = append(w.Namespace, word)
			}
		}
	}
	p.readDisallowedNames(el, w)
	return w
}

// readDisallowedNames reads XSD 1.1's notQName into {disallowed names}.
//
// The names resolve as ordinary QNames — see resolveNotQName — which is why
// this does not reuse resolveQName: that one drops the prefix into a map key
// for component lookup, and these names are compared against instance element
// names instead.
func (p *parser) readDisallowedNames(el *xdm.Node, w *Wildcard) {
	raw := el.AttrValue("notQName")
	if raw == "" {
		return
	}
	if p.schema.Version < Version11 {
		p.errs = append(p.errs, errorAt(el, "",
			"notQName requires XSD 1.1"))
		return
	}
	isAttr := el.Name.Local == "anyAttribute"
	for _, word := range splitFields(raw) {
		switch word {
		case "##defined":
			w.DisallowDefined = true
		case "##definedSibling":
			if isAttr {
				// The schema for schemas allows ##defined on an
				// attribute wildcard but not ##definedSibling:
				// attributes have no content model to be
				// siblings within.
				p.errs = append(p.errs, errorAt(el, "",
					"##definedSibling is not permitted on xs:anyAttribute"))
				continue
			}
			w.DisallowDefinedSibling = true
		default:
			name, err := p.resolveNotQName(el, word)
			if err != nil {
				p.errs = append(p.errs, err)
				continue
			}
			w.DisallowedNames = append(w.DisallowedNames, name)
		}
	}
}

// resolveNotQName resolves one notQName entry.
//
// The names resolve the ordinary way for a QName-valued attribute: an
// unprefixed name takes the default namespace declared in the schema document,
// and the absent namespace only where no default is in scope. That is what
// makes notQName="a b" in a document whose default namespace is X exclude
// {X}a and {X}b — the names the author was looking at when they wrote it.
func (p *parser) resolveNotQName(el *xdm.Node, value string) (xdm.QName, error) {
	prefix, local := "", value
	if i := strings.IndexByte(value, ':'); i >= 0 {
		prefix, local = value[:i], value[i+1:]
	}
	if local == "" || strings.ContainsRune(local, ':') {
		return xdm.QName{}, errorAt(el, "src-resolve",
			"notQName=%q is not a valid QName", value)
	}
	uri, ok := el.LookupPrefix(prefix)
	if !ok {
		if prefix == "" {
			return xdm.QName{Local: local}, nil
		}
		return xdm.QName{}, errorAt(el, "src-resolve",
			"notQName=%q uses undeclared prefix %q", value, prefix)
	}
	return xdm.QName{URI: uri, Local: local}, nil
}

// splitFields splits on XML whitespace only.
//
// strings.Fields splits on every Unicode space, which would treat U+00A0 as a
// separator. XML does not, so a namespace list containing one would be split
// into names that match nothing.
func splitFields(s string) []string {
	var out []string
	start := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// resolveTypeRef resolves a type named by an attribute, deferring until every
// document has been read.
func (p *parser) resolveTypeRef(el *xdm.Node, ref string, set func(Type)) {
	name, err := p.resolveQName(el, "type", ref)
	if err != nil {
		p.errs = append(p.errs, err)
		return
	}
	p.fixups = append(p.fixups, func() error {
		t, ok := p.schema.Types[name]
		if !ok {
			return errorAt(el, "src-resolve",
				"type %q names no type definition", ref)
		}
		set(t)
		return nil
	})
}

// intersectWildcards combines two attribute wildcards (§3.10.6).
//
// The intersection is what a type gets when more than one attribute group
// contributes a wildcard: an attribute has to be admitted by all of them. The
// spec allows the result to be inexpressible — the intersection of two
// different negations, for instance — and in that case it is a schema error
// rather than something to approximate. Here the inexpressible cases fall back
// to the narrower of the two operands, which never admits more than the
// intersection does.
func intersectWildcards(a, b *Wildcard) *Wildcard {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	// processContents takes the stronger of the two: strict over lax over
	// skip, so that combining does not weaken checking.
	pc := a.ProcessContents
	if b.ProcessContents < pc {
		pc = b.ProcessContents
	}

	// {disallowed names} unions: an attribute has to be admitted by both
	// operands, so either one refusing a name is enough to refuse it. That
	// is the opposite of the union case, where a name survives only if both
	// refuse it, and getting it backwards admits every name only one side
	// disallowed.
	disallowed := unionNames(a, b)
	definedToo := a.DisallowDefined || b.DisallowDefined
	siblingToo := a.DisallowDefinedSibling || b.DisallowDefinedSibling

	withNames := func(w *Wildcard) *Wildcard {
		w.DisallowedNames = disallowed
		w.DisallowDefined = definedToo
		w.DisallowDefinedSibling = siblingToo
		return w
	}

	switch {
	case a.Kind == NSAny:
		out := *b
		out.ProcessContents = pc
		return withNames(&out)
	case b.Kind == NSAny:
		out := *a
		out.ProcessContents = pc
		return withNames(&out)
	}

	// An enumerated set against anything: keep the members the other
	// admits.
	if a.Kind == NSEnumerated || b.Kind == NSEnumerated {
		enum, other := a, b
		if b.Kind == NSEnumerated && a.Kind != NSEnumerated {
			enum, other = b, a
		}
		var kept []string
		for _, ns := range enum.Namespace {
			if other.Allows(ns) {
				kept = append(kept, ns)
			}
		}
		return withNames(&Wildcard{
			Kind: NSEnumerated, Namespace: kept, ProcessContents: pc})
	}

	// Both negations: the union of what each excludes.
	out := &Wildcard{
		Kind:            NSNot,
		ProcessContents: pc,
		ExcludesAbsent:  a.ExcludesAbsent || b.ExcludesAbsent,
	}
	seen := map[string]bool{}
	for _, ns := range append(append([]string{}, a.Namespace...), b.Namespace...) {
		if !seen[ns] {
			seen[ns] = true
			out.Namespace = append(out.Namespace, ns)
		}
	}
	return withNames(out)
}

// unionNames returns every name either wildcard disallows.
func unionNames(a, b *Wildcard) []xdm.QName {
	if len(a.DisallowedNames) == 0 {
		return b.DisallowedNames
	}
	if len(b.DisallowedNames) == 0 {
		return a.DisallowedNames
	}
	seen := make(map[xdm.QName]bool, len(a.DisallowedNames)+len(b.DisallowedNames))
	var out []xdm.QName
	for _, n := range append(append([]xdm.QName{}, a.DisallowedNames...), b.DisallowedNames...) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// groupUses returns an attribute group's uses, including those it reaches
// through referenced groups.
//
// The seen set guards a cycle. A group that references itself is ill-formed,
// and the walk stopping is what keeps that a schema error rather than a hang.
func groupUses(g *AttributeGroupDef, seen map[*AttributeGroupDef]bool) []*AttributeUse {
	if g == nil {
		return nil
	}
	if seen == nil {
		seen = map[*AttributeGroupDef]bool{}
	}
	if seen[g] {
		return nil
	}
	seen[g] = true

	out := append([]*AttributeUse(nil), g.AttributeUses...)
	for _, ref := range g.refs {
		out = append(out, groupUses(ref, seen)...)
	}
	return out
}

// groupWildcard returns the intersection of a group's own attribute wildcard
// and those of the groups it references.
func groupWildcard(g *AttributeGroupDef, seen map[*AttributeGroupDef]bool) *Wildcard {
	if g == nil {
		return nil
	}
	if seen == nil {
		seen = map[*AttributeGroupDef]bool{}
	}
	if seen[g] {
		return nil
	}
	seen[g] = true

	out := g.AttributeWildcard
	for _, ref := range g.refs {
		if w := groupWildcard(ref, seen); w != nil {
			out = intersectWildcards(out, w)
		}
	}
	return out
}

// formQualified reports whether a local declaration's name is qualified.
//
// The form attribute overrides the document's default in *both* directions.
// Testing only for form="qualified" let the default win whenever it was
// already qualified, so form="unqualified" did nothing — and a local element
// written that way inside a qualified schema landed in the target namespace
// instead of the absent one, where no instance could match it.
func formQualified(el *xdm.Node, byDefault bool) bool {
	switch el.AttrValue("form") {
	case "qualified":
		return true
	case "unqualified":
		return false
	}
	return byDefault
}
