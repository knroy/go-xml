package xquery

import (
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xsd"
)

// The in-scope schema definitions reach the parser through xpath.SchemaTypes,
// which the static context implements because the static context is already
// the xpath.NamespaceResolver every expression in the module is compiled
// against. That is the whole seam: xpath asks the resolver it was handed
// whether it also knows about schema components, so installing the schema on
// the static context is what makes "8 cast as hat:hatsize" resolve.
//
// It is the same arrangement the xslt package uses for xsl:import-schema, over
// the same *xsd.Schema and with the same method bodies -- see
// xslt/stylesheet.go. The duplication is deliberate rather than a helper in a
// third package: the two packages hold their schema on different types, and a
// shared implementation would have to be in xsd, which cannot import xpath
// because a schema's assertions and selectors ARE XPath expressions.
//
// A nil schema answers "not known" to every question, so a query with no
// "import schema" behaves exactly as it did before: the built-in table
// declines the name and nothing else is consulted.

// LookupSchemaType implements xpath.SchemaTypes.
//
// A type an imported schema defines is in the static context, so a query may
// write "instance of hat:hatsize" exactly as it writes "instance of
// xs:integer". Without this the name is XPST0051.
func (sc *staticContext) LookupSchemaType(name xdm.QName) (xdm.TypeCode, bool, bool) {
	if sc.schema == nil {
		return 0, false, false
	}
	t, ok := sc.schema.Types[name]
	if !ok {
		return 0, false, false
	}
	// Only an atomic simple type erases to a primitive. A complex type, a
	// list or a union is a real type -- the name resolves -- but there is no
	// single code that describes its values, so it is reported as known and
	// non-atomic rather than guessed at.
	st, ok := t.(*xsd.SimpleType)
	if !ok || st.Variety != xsd.VarietyAtomic || st.Primitive == nil {
		return 0, false, true
	}
	if st.Primitive.Name.Local == "NOTATION" && st.Primitive.Name.URI == xsd.NSSchema {
		// A value of a type derived from xs:NOTATION is a QName, not a
		// string: XML Schema gives xs:NOTATION the same value space as
		// xs:QName, so two notation values are equal when their expanded
		// names are equal however they were spelled.
		return xdm.TypeQName, true, true
	}
	// The nearest built-in *ancestor*, not the XSD primitive. They differ
	// wherever the built-in hierarchy has steps below a primitive: a
	// restriction of xs:integer has xs:decimal as its primitive, so erasing
	// to the primitive made "cast as hat:hatsize" produce an xs:decimal, and
	// "instance of xs:integer" then answered false for the value the cast had
	// just produced. This is xslt's LookupSchemaType rule and it is the same
	// rule for the same reason.
	for cur := st; cur != nil; {
		if cur.Name.URI == xsd.NSSchema && cur.Name.Local != "" {
			if code, ok := xpath.BuiltinAtomicTypeCode(cur.Name.Local); ok {
				return code, true, true
			}
		}
		base, ok := cur.Base.(*xsd.SimpleType)
		if !ok || base == cur {
			break
		}
		cur = base
	}
	code, ok := xpath.BuiltinAtomicTypeCode(st.Primitive.Name.Local)
	if !ok {
		return 0, false, true
	}
	return code, true, true
}

// LookupSchemaDeclaration implements xpath.SchemaTypes.
//
// schema-element(hat:a) names the global element declaration hat:a, so the
// question is whether the imported schema declares that name -- not whether
// any element happens to be called it.
func (sc *staticContext) LookupSchemaDeclaration(name xdm.QName, attribute bool) bool {
	if sc.schema == nil {
		return false
	}
	if attribute {
		_, ok := sc.schema.Attributes[name]
		return ok
	}
	_, ok := sc.schema.Elements[name]
	return ok
}

// SchemaDeclarationType implements xpath.SchemaTypes.
//
// Only a *named* type is reported. A declaration using an inline anonymous
// type has no name for the node test to compare against, and inventing one
// would make the comparison fail for every node rather than pass for the
// right ones.
func (sc *staticContext) SchemaDeclarationType(name xdm.QName, attribute bool) (string, bool) {
	if sc.schema == nil {
		return "", false
	}
	var t xsd.Type
	if attribute {
		d, ok := sc.schema.Attributes[name]
		if !ok || d == nil || d.Type == nil {
			return "", false
		}
		t = d.Type
	} else {
		d, ok := sc.schema.Elements[name]
		if !ok || d == nil || d.Type == nil {
			return "", false
		}
		t = d.Type
	}
	local := t.TypeName().Local
	if local == "" {
		return "", false
	}
	return local, true
}

// SubstitutionGroupMembers implements xpath.SchemaTypes.
//
// The schema has already computed the transitive closure and cached it on the
// head declaration, so this is a lookup rather than a walk. Only the names are
// handed back: the node test compares names, and returning declarations would
// leak xsd types into the xpath package, which cannot import xsd.
func (sc *staticContext) SubstitutionGroupMembers(name xdm.QName) []xdm.QName {
	if sc.schema == nil {
		return nil
	}
	head, ok := sc.schema.Elements[name]
	if !ok {
		return nil
	}
	members := head.SchemaElementMembers()
	if len(members) == 0 {
		return nil
	}
	out := make([]xdm.QName, 0, len(members))
	for _, d := range members {
		// Only the URI and local name: a QName is compared as a whole struct,
		// and the prefix a schema was written with is rarely the query's.
		out = append(out, xdm.QName{URI: d.Name.URI, Local: d.Name.Local})
	}
	return out
}

// ValidateSchemaValue implements xpath.SchemaTypes.
//
// "8 castable as hat:hatsize" is that question. The engine can cast to the
// built-in the type derives from, but the facets the schema author wrote live
// only in the schema -- so without asking, a cast to a restriction of
// xs:integer accepted every integer and the restriction meant nothing.
func (sc *staticContext) ValidateSchemaValue(name xdm.QName, value string) (bool, error) {
	if sc.schema == nil || !sc.schema.HasSimpleType(name) {
		return false, nil
	}
	return true, sc.schema.ValidateValue(value, name)
}

// SchemaTypeIsList implements xpath.SchemaListTypes.
//
// It exists so that "castable as" against a schema-defined list type answers
// true or false rather than raising the atomic-target static error. The
// validity of a particular value is still ValidateSchemaValue's answer; this
// only says which kind of type the name denotes.
func (sc *staticContext) SchemaTypeIsList(name xdm.QName) (xdm.QName, bool) {
	if sc.schema == nil {
		return xdm.QName{}, false
	}
	return sc.schema.IsListSimpleType(name)
}

// SchemaUnionMemberTypes implements xpath.SchemaUnionTypes.
//
// XPath 3.1 §2.5.5 makes union membership a clause of derives-from in its own
// right, so a value whose type is one of a pure union's members is an instance
// of the union without ever having been validated against it. LookupSchemaType
// cannot express that -- it returns one primitive, and a union has none -- so
// the members are resolved here instead.
//
// Purity is enforced rather than assumed. §2.5 admits a union as an item type
// only when it carries no facets and has no list type anywhere in its
// transitive membership; a union failing either returns false and so matches
// nothing. That is deliberately the strict direction: XSD 1.1 §3.16.6.3 fixed
// an XSD 1.0 error by which a member could substitute for a faceted union it
// does not actually satisfy, and being permissive here would reintroduce it.
//
// This is xslt's SchemaUnionMemberTypes over a different holder of the same
// *xsd.Schema. See the note at the top of this file on why the two are not
// shared.
func (sc *staticContext) SchemaUnionMemberTypes(name xdm.QName) ([]xdm.TypeCode, bool) {
	if sc.schema == nil {
		return nil, false
	}
	t, ok := sc.schema.Types[name]
	if !ok {
		return nil, false
	}
	st, ok := t.(*xsd.SimpleType)
	if !ok || st.Variety != xsd.VarietyUnion {
		return nil, false
	}
	seenType := map[*xsd.SimpleType]bool{}
	var walk func(u *xsd.SimpleType) ([]xdm.TypeCode, bool)
	walk = func(u *xsd.SimpleType) ([]xdm.TypeCode, bool) {
		// A union whose members form a cycle is not a schema this can answer
		// for, and re-entering a member already on the path is what
		// identifies one. A depth bound would also refuse legal chains, and
		// (nil, false) reads as "not a union I can answer for" -- which for a
		// legal deep union is the wrong answer.
		if seenType[u] || !u.Facets.IsEmpty() {
			return nil, false
		}
		seenType[u] = true
		var out []xdm.TypeCode
		for _, m := range u.MemberTypes {
			if m == nil {
				return nil, false
			}
			switch m.Variety {
			case xsd.VarietyUnion:
				sub, pure := walk(m)
				if !pure {
					return nil, false
				}
				out = append(out, sub...)
			case xsd.VarietyAtomic:
				// The member is matched by the built-in it erases to, because
				// that is the code an unannotated value carries. A member that
				// is itself a restriction contributes its nearest built-in
				// ancestor, which is what LookupSchemaType resolves for the
				// same reason.
				code, isAtomic, ok := sc.LookupSchemaType(m.Name)
				if !ok || !isAtomic {
					if c, found := xpath.BuiltinAtomicTypeCode(m.Name.Local); found &&
						m.Name.URI == xsd.NSSchema {
						out = append(out, c)
						continue
					}
					return nil, false
				}
				out = append(out, code)
			default:
				// VarietyList. §2.5 excludes a union with a list type
				// anywhere in its transitive membership outright.
				return nil, false
			}
		}
		return out, true
	}
	members, pure := walk(st)
	if !pure || len(members) == 0 {
		return nil, false
	}
	return members, true
}

// SchemaUnionAtomicMemberTypes implements xpath.SchemaImpureUnionTypes.
//
// It is SchemaUnionMemberTypes without the purity gate and without the list
// members: the question a CAST asks is which members a source value may be
// converted into, not which members a value may stand in for. A union with a
// list member is impure and so answers nothing to the ItemType question, but a
// cast to it from an xs:date still has to see the xs:date member --
// cbcl-castable-impure-001. The list member is skipped rather than refusing
// the whole union, because a cast to a list type is defined only from a
// string-like source, and the caller applies that rule.
//
// A union derived by RESTRICTION carries its base's members, which is how
// s:restrictedUnion reaches the four dates s:approximateDate declares.
func (sc *staticContext) SchemaUnionAtomicMemberTypes(name xdm.QName) ([]xdm.TypeCode, bool) {
	if sc.schema == nil {
		return nil, false
	}
	t, ok := sc.schema.Types[name]
	if !ok {
		return nil, false
	}
	st, ok := t.(*xsd.SimpleType)
	if !ok {
		return nil, false
	}
	// A restriction of a union is itself of variety union in XSD's model, but
	// its own MemberTypes may be empty -- the members come from the base. Walk
	// down to the nearest ancestor that declares them.
	for st != nil && st.Variety == xsd.VarietyUnion && len(st.MemberTypes) == 0 {
		base, ok := st.Base.(*xsd.SimpleType)
		if !ok || base == st {
			break
		}
		st = base
	}
	if st == nil || st.Variety != xsd.VarietyUnion {
		return nil, false
	}
	seen := map[*xsd.SimpleType]bool{}
	var out []xdm.TypeCode
	var walk func(u *xsd.SimpleType)
	walk = func(u *xsd.SimpleType) {
		if u == nil || seen[u] {
			return
		}
		seen[u] = true
		for _, m := range u.MemberTypes {
			if m == nil {
				continue
			}
			switch m.Variety {
			case xsd.VarietyUnion:
				walk(m)
			case xsd.VarietyAtomic:
				if code, isAtomic, ok := sc.LookupSchemaType(m.Name); ok && isAtomic {
					out = append(out, code)
					continue
				}
				if c, found := xpath.BuiltinAtomicTypeCode(m.Name.Local); found &&
					m.Name.URI == xsd.NSSchema {
					out = append(out, c)
				}
			}
			// VarietyList contributes nothing: a cast to a list member is
			// reachable only from a string-like source, which the caller
			// decides without needing the member named here.
		}
	}
	walk(st)
	return out, true
}

// SchemaUnionListMemberItemType implements xpath.SchemaUnionListMemberType.
//
// It walks the same transitive membership as SchemaUnionAtomicMemberTypes and
// picks out the member that one deliberately skips: the list. The item type is
// resolved to a built-in code, because that is what the cast needs in order to
// build the sequence F&O 3.0 18.3.6 asks for -- one value per whitespace-
// separated token, each an instance of the list's item type.
//
// The first list member wins. A union with two of them is legal XSD but has no
// bearing here: the members are tried in declaration order, so the first is the
// one a value would be admitted by.
func (sc *staticContext) SchemaUnionListMemberItemType(name xdm.QName) (xdm.TypeCode, bool) {
	if sc.schema == nil {
		return 0, false
	}
	t, ok := sc.schema.Types[name]
	if !ok {
		return 0, false
	}
	st, ok := t.(*xsd.SimpleType)
	if !ok {
		return 0, false
	}
	// A restriction of a union declares no members of its own; the nearest
	// ancestor that does is the one to walk. This mirrors the descent in
	// SchemaUnionAtomicMemberTypes so the two agree on which union is meant.
	for st != nil && st.Variety == xsd.VarietyUnion && len(st.MemberTypes) == 0 {
		base, ok := st.Base.(*xsd.SimpleType)
		if !ok || base == st {
			break
		}
		st = base
	}
	if st == nil || st.Variety != xsd.VarietyUnion {
		return 0, false
	}
	seen := map[*xsd.SimpleType]bool{}
	var walk func(u *xsd.SimpleType) (xdm.TypeCode, bool)
	walk = func(u *xsd.SimpleType) (xdm.TypeCode, bool) {
		if u == nil || seen[u] {
			return 0, false
		}
		seen[u] = true
		for _, m := range u.MemberTypes {
			if m == nil {
				continue
			}
			switch m.Variety {
			case xsd.VarietyUnion:
				if c, ok := walk(m); ok {
					return c, true
				}
			case xsd.VarietyList:
				if item, isList := sc.schema.IsListSimpleType(m.Name); isList {
					if code, isAtomic, ok := sc.LookupSchemaType(item); ok && isAtomic {
						return code, true
					}
					if c, found := xpath.BuiltinAtomicTypeCode(item.Local); found &&
						item.URI == xsd.NSSchema {
						return c, true
					}
				}
			}
		}
		return 0, false
	}
	return walk(st)
}

// SchemaUnionMemberFacetNames implements xpath.SchemaUnionMemberFacets.
//
// The walk is SchemaUnionMemberTypes' exactly, collecting names in place of
// codes so that the two slices are index-parallel. It is separate rather than
// folded into that method because the interface is optional and its signature
// is fixed; see xpath.SchemaUnionMemberFacets for why a cast needs the names.
func (sc *staticContext) SchemaUnionMemberFacetNames(name xdm.QName) ([]string, bool) {
	if sc.schema == nil {
		return nil, false
	}
	t, ok := sc.schema.Types[name]
	if !ok {
		return nil, false
	}
	st, ok := t.(*xsd.SimpleType)
	if !ok || st.Variety != xsd.VarietyUnion {
		return nil, false
	}
	seenType := map[*xsd.SimpleType]bool{}
	var walk func(u *xsd.SimpleType) ([]string, bool)
	walk = func(u *xsd.SimpleType) ([]string, bool) {
		if seenType[u] || !u.Facets.IsEmpty() {
			return nil, false
		}
		seenType[u] = true
		var out []string
		for _, m := range u.MemberTypes {
			if m == nil {
				return nil, false
			}
			switch m.Variety {
			case xsd.VarietyUnion:
				sub, pure := walk(m)
				if !pure {
					return nil, false
				}
				out = append(out, sub...)
			case xsd.VarietyAtomic:
				// Only a BUILT-IN member has a facet name CastToDerived
				// understands. A schema-defined restriction contributes the
				// empty string, which leaves the cast on its erased code --
				// the behaviour before this existed, and the safe direction:
				// applying a facet name the table does not hold would be a
				// silent no-op anyway.
				if m.Name.URI == xsd.NSSchema {
					out = append(out, m.Name.Local)
					continue
				}
				out = append(out, "")
			default:
				return nil, false
			}
		}
		return out, true
	}
	names, pure := walk(st)
	if !pure || len(names) == 0 {
		return nil, false
	}
	return names, true
}

// SchemaUnionAtomicMemberFacetNames implements xpath.SchemaUnionMemberFacets.
//
// The walk is SchemaUnionAtomicMemberTypes' exactly, collecting names in place
// of codes so the two slices are index-parallel. See the note there on why a
// restriction of a union has to descend to the ancestor that declares the
// members, and xpath.SchemaUnionMemberFacets on why a cast needs the names.
func (sc *staticContext) SchemaUnionAtomicMemberFacetNames(name xdm.QName) ([]string, bool) {
	if sc.schema == nil {
		return nil, false
	}
	t, ok := sc.schema.Types[name]
	if !ok {
		return nil, false
	}
	st, ok := t.(*xsd.SimpleType)
	if !ok {
		return nil, false
	}
	for st != nil && st.Variety == xsd.VarietyUnion && len(st.MemberTypes) == 0 {
		base, ok := st.Base.(*xsd.SimpleType)
		if !ok || base == st {
			break
		}
		st = base
	}
	if st == nil || st.Variety != xsd.VarietyUnion {
		return nil, false
	}
	seen := map[*xsd.SimpleType]bool{}
	var out []string
	var walk func(u *xsd.SimpleType)
	walk = func(u *xsd.SimpleType) {
		if u == nil || seen[u] {
			return
		}
		seen[u] = true
		for _, m := range u.MemberTypes {
			if m == nil {
				continue
			}
			switch m.Variety {
			case xsd.VarietyUnion:
				walk(m)
			case xsd.VarietyAtomic:
				// Only a BUILT-IN member has a name CastToDerived understands;
				// a schema-defined one contributes the empty string, leaving
				// the cast on the erased code it always used.
				if _, isAtomic, ok := sc.LookupSchemaType(m.Name); ok && isAtomic {
					if m.Name.URI == xsd.NSSchema {
						out = append(out, m.Name.Local)
					} else {
						out = append(out, "")
					}
					continue
				}
				if _, found := xpath.BuiltinAtomicTypeCode(m.Name.Local); found &&
					m.Name.URI == xsd.NSSchema {
					out = append(out, m.Name.Local)
				}
			}
		}
	}
	walk(st)
	return out, true
}
