package xpath

import "github.com/knroy/go-xml/xdm"

// SchemaTypes reports the types an imported schema contributes to the static
// context.
//
// XPath 2.0 has an *in-scope schema definitions* component that this engine
// otherwise leaves empty: without it, only the built-in xs: types exist, and
// "instance of my:partNumberType" is XPST0051 no matter what the stylesheet
// imported. A stylesheet with xsl:import-schema is precisely the case where
// that component is not empty.
//
// It is an interface here rather than an *xsd.Schema because xsd imports
// xpath — schema documents contain XPath expressions in their assertions and
// selectors — so the dependency cannot run the other way. The xslt package
// supplies the implementation, which is a few lines over the schema's own
// type table.
//
// A resolver that also implements this is asked about a name only after the
// built-in table has declined it, so a schema cannot redefine xs:integer.
type SchemaTypes interface {
	// LookupSchemaType reports whether name is a type in the static context,
	// and which primitive an atomic value of it erases to.
	//
	// The primitive is what the type *system* needs: "instance of" and "treat
	// as" compare against the type hierarchy, and a value of a derived atomic
	// type is a value of its primitive with facets applied. A complex type,
	// or a list or union with no single primitive, returns ok with a zero
	// code and false for atomic — enough to stop XPST0051 without claiming
	// the value is comparable as an atomic.
	LookupSchemaType(name xdm.QName) (prim xdm.TypeCode, atomic, ok bool)

	// LookupSchemaDeclaration reports whether name is a global element or
	// attribute declaration in the static context.
	//
	// It is what schema-element() and schema-attribute() need: both name a
	// *declaration* rather than a type, and both are XPST0008 when no schema
	// declares the name.
	LookupSchemaDeclaration(name xdm.QName, attribute bool) bool

	// SubstitutionGroupMembers returns the global element declarations that
	// may substitute for name, transitively and not including name itself.
	//
	// schema-element(E) matches E and every member of E's substitution
	// group, so a schema that declares "surname" as substitutable for "last"
	// makes schema-element(z:last) match a z:surname element. Resolving the
	// members here rather than at match time is what keeps the node test
	// self-contained: nothing carries a schema into the evaluator, and the
	// group is fixed once the schema is imported.
	//
	// An implementation with no schema, or a name with no members, returns
	// nil.
	SubstitutionGroupMembers(name xdm.QName) []xdm.QName

	// SchemaDeclarationType returns the local name of the type a global
	// element or attribute declaration names, and whether there is one.
	//
	// It is a LOCAL name, unlike the type name in an element() test, which is
	// resolved to a namespace-qualified annotation key at parse time. The
	// difference is deliberate: this string is produced by an implementation
	// of this interface, which has no obligation to know about annotation
	// keys, so the comparison against it (in nodeTypeMatches, reached through
	// KindTest.DeclaredType) accepts a bare name as a local-part match. That
	// is narrower ground than it sounds: the check exists only to tell a
	// global declaration from a LOCAL declaration of the same name in the
	// same schema, where the namespace is not in question.
	//
	// schema-element(E) matches a node only when it was validated against
	// E's *declaration*, and a node may carry E's name while having been
	// validated against a local declaration of a different type — which is
	// the case the suite draws the line on. The evaluator sees only the
	// compiled test, so the declared type is resolved here, while the schema
	// is still reachable, and compared against the node's annotation at
	// match time.
	//
	// An anonymous type has no name to return, so a declaration using one
	// returns false and the test falls back to checking only that the node
	// was validated at all.
	SchemaDeclarationType(name xdm.QName, attribute bool) (string, bool)

	// ValidateSchemaValue checks a lexical value against a named simple type
	// in the static context, reporting whether the name is a simple type at
	// all and, if so, whether the value is in its value space.
	//
	// "castable as my:hatsize" is that question. The engine can cast to the
	// built-in the type derives from, but the facets the schema author wrote
	// live only in the schema — so without asking, a cast to a restriction of
	// xs:integer accepted every integer and the restriction meant nothing.
	ValidateSchemaValue(name xdm.QName, value string) (known bool, err error)
}

// SchemaUnionTypes reports the member types of a pure union type.
//
// It is separate from SchemaTypes rather than a method on it because it is
// optional: an implementation that does not know about unions simply does not
// implement it, and every union then matches nothing, which is the behaviour
// before this existed. Folding it into SchemaTypes would have broken every
// implementation of that interface instead.
type SchemaUnionTypes interface {
	// SchemaUnionMemberTypes returns the built-in atomic types a pure union
	// type admits, transitively, and whether name is a pure union type at
	// all.
	//
	// "Pure" is XPath 3.1 2.5's term and its constraints are the caller's to
	// enforce: variety union, no facets, no list type in the transitive
	// membership, and no member union carrying facets. A union failing any of
	// them returns false — XSD 1.1 fixed a 1.0 error here, and matching such
	// a type is unsafe rather than merely unsupported.
	SchemaUnionMemberTypes(name xdm.QName) ([]xdm.TypeCode, bool)
}

// SchemaUnionMemberNames reports the annotation keys of a union type's member
// types, transitively.
//
// It is separate from SchemaUnionMemberTypes because it answers a different
// question for a different consumer. That method erases each member to the
// built-in atomic type code an unannotated VALUE would carry, which is what
// "instance of" compares against. A NODE carries the name it was validated
// against instead, so an attribute validated as a union's xs:integer member is
// annotated "integer" and matching it against the union needs the names.
//
// It is also deliberately looser about purity: a union used here need not be
// pure. Purity exists to stop a member VALUE from standing in for a faceted
// union it may not satisfy — the XSD 1.0 error XSD 1.1 3.16.6.3 corrected —
// but a node reaching this point was actually validated against the union, so
// the schema has already decided it satisfies whatever facets the union adds.
// The unsafe substitution purity guards against cannot arise.
//
// It is STRICTER in the one way that matters instead: only atomic members are
// reported. A union over list types annotates the node with the union itself
// rather than with a member, so naming its members would admit no node that
// should match and would admit sibling members that should not.
type SchemaUnionNames interface {
	SchemaUnionMemberNames(name xdm.QName) ([]string, bool)
}

// schemaUnionMemberNamesOf resolves a lexical type name to the annotation keys
// of a union's members, through the same prefix bindings as everything else.
func schemaUnionMemberNamesOf(lex string, ns NamespaceResolver) ([]string, bool) {
	su, ok := ns.(SchemaUnionNames)
	if !ok {
		return nil, false
	}
	name, ok := resolveTypeQName(lex, ns)
	if !ok {
		return nil, false
	}
	return su.SchemaUnionMemberNames(name)
}

// schemaUnionMembersOf resolves a lexical type name to the member types of a
// pure union, through the same prefix bindings as everything else.
func schemaUnionMembersOf(lex string, ns NamespaceResolver) ([]xdm.TypeCode, bool) {
	su, ok := ns.(SchemaUnionTypes)
	if !ok {
		return nil, false
	}
	name, ok := resolveTypeQName(lex, ns)
	if !ok {
		return nil, false
	}
	return su.SchemaUnionMemberTypes(name)
}

// schemaValueValid checks a value against a named schema simple type,
// resolving the name through the same prefix bindings as everything else.
//
// known is false when the name is not a simple type in the static context, in
// which case the caller keeps whatever answer the built-in cast gave.
func schemaValueValid(lex string, ns NamespaceResolver, value string) (known bool, err error) {
	st, ok := ns.(SchemaTypes)
	if !ok {
		return false, nil
	}
	prefix, local := xdm.SplitQName(lex)
	name := xdm.QName{Local: local}
	if prefix != "" {
		uri, found := ns.ResolvePrefix(prefix)
		if !found {
			return false, nil
		}
		name.URI = uri
	} else {
		name.URI = ns.DefaultElementNamespace()
	}
	return st.ValidateSchemaValue(name, value)
}

// schemaDeclarationType resolves the declared type of a schema-element() or
// schema-attribute() name, through the same prefix bindings as the name.
func schemaDeclarationType(lex string, ns NamespaceResolver, attribute bool) (string, bool) {
	st, ok := ns.(SchemaTypes)
	if !ok {
		return "", false
	}
	prefix, local := xdm.SplitQName(lex)
	name := xdm.QName{Local: local}
	if prefix != "" {
		uri, found := ns.ResolvePrefix(prefix)
		if !found {
			return "", false
		}
		name.URI = uri
	} else if !attribute {
		name.URI = ns.DefaultElementNamespace()
	}
	return st.SchemaDeclarationType(name, attribute)
}

// schemaSubstitutionGroup returns the names schema-element(lex) admits besides
// lex itself, resolved through the same prefix bindings as the name.
func schemaSubstitutionGroup(lex string, ns NamespaceResolver) []xdm.QName {
	st, ok := ns.(SchemaTypes)
	if !ok {
		return nil
	}
	prefix, local := xdm.SplitQName(lex)
	name := xdm.QName{Local: local}
	if prefix != "" {
		uri, found := ns.ResolvePrefix(prefix)
		if !found {
			return nil
		}
		name.URI = uri
	} else {
		name.URI = ns.DefaultElementNamespace()
	}
	return st.SubstitutionGroupMembers(name)
}

// schemaDeclared reports whether a schema in the static context declares name.
func schemaDeclared(lex string, ns NamespaceResolver, attribute bool) bool {
	st, ok := ns.(SchemaTypes)
	if !ok {
		return false
	}
	prefix, local := xdm.SplitQName(lex)
	name := xdm.QName{Local: local}
	if prefix != "" {
		uri, found := ns.ResolvePrefix(prefix)
		if !found {
			return false
		}
		name.URI = uri
	} else {
		// An unprefixed element declaration name takes the default element
		// namespace, exactly as an element name in a path step does.
		// An attribute is different: an unprefixed attribute name is always
		// in no namespace.
		if !attribute {
			name.URI = ns.DefaultElementNamespace()
		}
	}
	return st.LookupSchemaDeclaration(name, attribute)
}

// BuiltinAtomicTypeCode returns the type code for a built-in xs: type, given
// its local name.
//
// It exists so that a caller holding a schema's primitive type — the xsd
// package, which cannot be imported from here — can map it to the code this
// engine's values carry, without a second copy of the table in parser_path.go
// drifting away from the first.
func BuiltinAtomicTypeCode(local string) (xdm.TypeCode, bool) {
	return atomicTypeByName(local, xsOnlyResolver{})
}

// xsOnlyResolver binds the single prefix atomicTypeByName consults.
type xsOnlyResolver struct{}

func (xsOnlyResolver) ResolvePrefix(p string) (string, bool) {
	if p == "xs" {
		return xdm.NSXS, true
	}
	return "", false
}

// The names BuiltinAtomicTypeCode is asked about arrive as bare local parts --
// the caller holds a type it already knows to be in the XSD namespace and has
// dropped the prefix. Reporting XSD here says so, which is what lets
// atomicTypeByName apply the default-namespace rule to unprefixed names
// without refusing those lookups.
func (xsOnlyResolver) DefaultElementNamespace() string  { return xdm.NSXS }
func (xsOnlyResolver) DefaultFunctionNamespace() string { return xdm.NSFN }

// schemaTypeOf asks the resolver about a type name the built-in table did not
// recognise.
//
// The lexical name is resolved through the same prefix bindings as everything
// else, and a prefixed name must have its prefix bound — the caller has
// already reported XPST0081 when it is not.
//
// An unprefixed name is *not* in no namespace: a type name takes the default
// element/type namespace, which xpath-default-namespace sets. Treating it as
// unqualified made "instance of partNumberType" XPST0051 in a stylesheet that
// had imported the very schema defining it.
func schemaTypeOf(lex string, ns NamespaceResolver) (xdm.TypeCode, bool, bool) {
	st, ok := ns.(SchemaTypes)
	if !ok {
		return 0, false, false
	}
	name, ok := resolveTypeQName(lex, ns)
	if !ok {
		return 0, false, false
	}
	return st.LookupSchemaType(name)
}

// resolveTypeQName expands a lexical type name against a resolver's prefix
// bindings, reporting false for a prefix nothing binds.
//
// Only the URI is carried into the QName, never the prefix. A QName is a map
// key here and Go compares the whole struct, so carrying the prefix through
// made every lookup miss: a schema stores a type under the prefix IT was
// written with, which is rarely the stylesheet's.
func resolveTypeQName(lex string, ns NamespaceResolver) (xdm.QName, bool) {
	prefix, local := xdm.SplitQName(lex)
	name := xdm.QName{Local: local}
	if prefix == "" {
		name.URI = ns.DefaultElementNamespace()
		return name, true
	}
	uri, found := ns.ResolvePrefix(prefix)
	if !found {
		return xdm.QName{}, false
	}
	name.URI = uri
	return name, true
}

// annotationKeyOf resolves a lexical type name into the key the data model
// records type annotations under.
//
// The two sides of a type comparison reach it by different routes. The
// annotation on a value was produced by the schema layer, which knows the
// type's namespace outright. The name in a sequence type was written by the
// query author as a lexical QName, and its namespace is whatever the static
// context binds its prefix to — information that exists while parsing and is
// gone by evaluation time. Resolving here is what lets the comparison be on
// expanded names rather than on local parts, which is what the specification
// asks for and what the local-part comparison could not do: it answered true
// for any two types that happened to share a local name.
//
// An unprefixed name takes the default element/type namespace, matching
// schemaTypeOf — a type name is not in no namespace merely for being written
// without a prefix.
//
// A resolver that cannot resolve the prefix yields the lexical form unchanged.
// That is the pre-existing behaviour for an unbound prefix, which the caller
// has already reported as XPST0081 where it is an error.
func annotationKeyOf(lex string, ns NamespaceResolver) string {
	if lex == "" {
		return ""
	}
	prefix, local := xdm.SplitQName(lex)
	if ns == nil {
		return xdm.AnnotationName("", local)
	}
	if prefix == "" {
		return xdm.AnnotationName(ns.DefaultElementNamespace(), local)
	}
	uri, found := ns.ResolvePrefix(prefix)
	if !found {
		return lex
	}
	return xdm.AnnotationName(uri, local)
}
