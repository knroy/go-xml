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
func (xsOnlyResolver) DefaultElementNamespace() string  { return "" }
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
	prefix, local := xdm.SplitQName(lex)
	name := xdm.QName{Local: local}
	if prefix != "" {
		uri, found := ns.ResolvePrefix(prefix)
		if !found {
			return 0, false, false
		}
		// Only the URI: a QName is a map key here, and Go compares the
		// whole struct including the prefix. Carrying the prefix through
		// made every lookup miss, because a schema stores a type under the
		// prefix *it* was written with, which is rarely the stylesheet's.
		name.URI = uri
	} else {
		name.URI = ns.DefaultElementNamespace()
	}
	return st.LookupSchemaType(name)
}
