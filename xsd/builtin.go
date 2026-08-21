package xsd

import (
	"sync"

	"github.com/knroy/go-xml/xdm"
)

// The built-in type hierarchy (Part 2 §3).
//
// These types are present in every schema without being declared, so they are
// constructed once and shared. Sharing is safe because nothing mutates a
// built-in after construction — a schema that tries to redefine one is
// rejected in declareType rather than being allowed to change the shared value.
var (
	builtinOnce sync.Once
	builtinMap  map[xdm.QName]Type
)

// builtinTypes returns the built-in type definitions, keyed by expanded name.
func builtinTypes() map[xdm.QName]Type {
	builtinOnce.Do(buildBuiltins)
	return builtinMap
}

// xsName builds the expanded name of a built-in type.
func xsName(local string) xdm.QName {
	return xdm.QName{URI: NSSchema, Local: local}
}

func buildBuiltins() {
	builtinMap = make(map[xdm.QName]Type, 48)

	// xs:anyType is the ur-type. Its base type definition is *itself* — a
	// deliberate self-loop in the spec (§3.4.7) — so every walk up a base
	// chain has to stop at self rather than at nil.
	anyType := &ComplexType{
		Name:             xsName("anyType"),
		DerivationMethod: DerivationRestriction,
		Content:          ContentMixed,
		Particle: &Particle{
			MinOccurs: 0, MaxOccurs: Unbounded,
			Term: &Wildcard{Kind: NSAny, ProcessContents: ProcessLax},
		},
		// Erratum E1-51: the ur-type's attribute wildcard is lax, not
		// strict. Without that, no skip or lax wildcard could ever be a
		// valid restriction of it.
		AttributeWildcard: &Wildcard{Kind: NSAny, ProcessContents: ProcessLax},
	}
	anyType.Base = anyType
	builtinMap[anyType.Name] = anyType

	// xs:anySimpleType is the simple ur-type: the base of every atomic
	// primitive, with no facets of its own.
	anySimple := &SimpleType{
		Name:    xsName("anySimpleType"),
		Base:    anyType,
		Variety: VarietyAtomic,
		Facets:  &FacetSet{},
		builtin: true,
	}
	builtinMap[anySimple.Name] = anySimple

	preserve, collapse := WhitePreserve, WhiteCollapse

	// The 19 primitives. Every one except xs:string collapses whitespace,
	// and the facet is fixed on all of them.
	primitive := func(local string, ws WhiteSpace) *SimpleType {
		t := &SimpleType{
			Name:    xsName(local),
			Base:    anySimple,
			Variety: VarietyAtomic,
			Facets:  &FacetSet{WhiteSpace: &ws, WhiteSpaceFixed: true},
			builtin: true,
		}
		t.Primitive = t
		builtinMap[t.Name] = t
		return t
	}

	str := primitive("string", preserve)
	primitive("boolean", collapse)
	decimal := primitive("decimal", collapse)
	primitive("float", collapse)
	primitive("double", collapse)
	primitive("duration", collapse)
	primitive("dateTime", collapse)
	primitive("time", collapse)
	primitive("date", collapse)
	primitive("gYearMonth", collapse)
	primitive("gYear", collapse)
	primitive("gMonthDay", collapse)
	primitive("gDay", collapse)
	primitive("gMonth", collapse)
	primitive("hexBinary", collapse)
	primitive("base64Binary", collapse)
	primitive("anyURI", collapse)
	primitive("QName", collapse)
	notation := primitive("NOTATION", collapse)
	_ = notation

	// derive builds a type restricting another, inheriting its primitive.
	derive := func(local string, base *SimpleType, f *FacetSet) *SimpleType {
		if f == nil {
			f = &FacetSet{}
		}
		t := &SimpleType{
			Name:      xsName(local),
			Base:      base,
			Variety:   VarietyAtomic,
			Primitive: base.Primitive,
			Facets:    f,
			builtin:   true,
		}
		builtinMap[t.Name] = t
		return t
	}

	replace := WhiteReplace
	normalized := derive("normalizedString", str,
		&FacetSet{WhiteSpace: &replace, WhiteSpaceFixed: true})
	token := derive("token", normalized,
		&FacetSet{WhiteSpace: &collapse, WhiteSpaceFixed: true})

	derive("language", token, nil)
	nmtoken := derive("NMTOKEN", token, nil)
	name := derive("Name", token, nil)
	ncName := derive("NCName", name, nil)
	id := derive("ID", ncName, nil)
	idref := derive("IDREF", ncName, nil)
	entity := derive("ENTITY", ncName, nil)
	_ = id

	// The list types. Each has minLength 1, so an empty list is invalid.
	one := uint64(1)
	list := func(local string, item *SimpleType) {
		t := &SimpleType{
			Name:     xsName(local),
			Base:     anySimple,
			Variety:  VarietyList,
			ItemType: item,
			Facets: &FacetSet{
				MinLength:       &one,
				WhiteSpace:      &collapse,
				WhiteSpaceFixed: true,
			},
			builtin: true,
		}
		builtinMap[t.Name] = t
	}
	list("NMTOKENS", nmtoken)
	list("IDREFS", idref)
	list("ENTITIES", entity)

	// The integer branch. Each step narrows the one above it, and the
	// bounds are held as lexical strings for the same reason a schema
	// author's bounds are: the value space is arbitrary precision, so a
	// machine integer could not hold xs:unsignedLong's maximum.
	zero := uint64(0)
	integer := derive("integer", decimal, &FacetSet{FractionDigits: &zero})

	bounded := func(local string, base *SimpleType, min, max string) *SimpleType {
		f := &FacetSet{}
		if min != "" {
			f.MinInclusive = &min
		}
		if max != "" {
			f.MaxInclusive = &max
		}
		return derive(local, base, f)
	}

	nonPositive := bounded("nonPositiveInteger", integer, "", "0")
	bounded("negativeInteger", nonPositive, "", "-1")

	long := bounded("long", integer, "-9223372036854775808", "9223372036854775807")
	intType := bounded("int", long, "-2147483648", "2147483647")
	short := bounded("short", intType, "-32768", "32767")
	bounded("byte", short, "-128", "127")

	nonNegative := bounded("nonNegativeInteger", integer, "0", "")
	bounded("positiveInteger", nonNegative, "1", "")
	unsignedLong := bounded("unsignedLong", nonNegative, "", "18446744073709551615")
	unsignedInt := bounded("unsignedInt", unsignedLong, "", "4294967295")
	unsignedShort := bounded("unsignedShort", unsignedInt, "", "65535")
	bounded("unsignedByte", unsignedShort, "", "255")

	// XSD 1.1 additions. They are defined unconditionally because a type is
	// either in the schema namespace or it is not; whether a *schema* may
	// use them is a version question, and refusing to define them here
	// would only turn a version error into a confusing "no such type".
	//
	// xs:dateTimeStamp is xs:dateTime with explicitTimezone="required" —
	// the type that says "an instant, not a wall-clock reading".
	required := TimezoneRequired
	dateTime := builtinMap[xsName("dateTime")].(*SimpleType)
	derive("dateTimeStamp", dateTime, &FacetSet{ExplicitTimezone: &required})

	// The two duration subtypes XPath has always had and XSD 1.0 left out.
	duration := builtinMap[xsName("duration")].(*SimpleType)
	derive("yearMonthDuration", duration, nil)
	derive("dayTimeDuration", duration, nil)

	// xs:anyAtomicType sits between anySimpleType and the primitives.
	anyAtomic := &SimpleType{
		Name:    xsName("anyAtomicType"),
		Base:    anySimple,
		Variety: VarietyAtomic,
		Facets:  &FacetSet{},
		builtin: true,
	}
	builtinMap[anyAtomic.Name] = anyAtomic

	// xs:error has an empty value space: nothing is ever valid against it,
	// which is how a 1.1 schema says "this branch must not be taken".
	errType := &SimpleType{
		Name:    xsName("error"),
		Base:    anySimple,
		Variety: VarietyUnion,
		Facets:  &FacetSet{},
		builtin: true,
	}
	builtinMap[errType.Name] = errType
}

// builtinAttributes returns the four xsi: attribute declarations.
//
// They are present in every schema without being declared, which is why a
// schema may write <xs:attribute ref="xsi:type"/> without importing anything.
func builtinAttributes() map[xdm.QName]*AttributeDecl {
	str, _ := builtinTypes()[xsName("string")].(*SimpleType)
	qname, _ := builtinTypes()[xsName("QName")].(*SimpleType)
	boolean, _ := builtinTypes()[xsName("boolean")].(*SimpleType)
	anyURI, _ := builtinTypes()[xsName("anyURI")].(*SimpleType)
	_ = str

	xsi := func(local string, t *SimpleType) (xdm.QName, *AttributeDecl) {
		n := xdm.QName{URI: NSInstance, Local: local}
		return n, &AttributeDecl{Name: n, Type: t, Scope: ScopeGlobal}
	}
	out := map[xdm.QName]*AttributeDecl{}
	for _, f := range []func() (xdm.QName, *AttributeDecl){
		func() (xdm.QName, *AttributeDecl) { return xsi("type", qname) },
		func() (xdm.QName, *AttributeDecl) { return xsi("nil", boolean) },
		func() (xdm.QName, *AttributeDecl) { return xsi("schemaLocation", anyURI) },
		func() (xdm.QName, *AttributeDecl) {
			return xsi("noNamespaceSchemaLocation", anyURI)
		},
	} {
		n, d := f()
		out[n] = d
	}
	return out
}

// anyType returns the ur-type definition.
func (s *Schema) anyType() Type {
	return s.Types[xsName("anyType")]
}

// anySimpleType returns the simple ur-type definition.
func (s *Schema) anySimpleType() *SimpleType {
	t, _ := s.Types[xsName("anySimpleType")].(*SimpleType)
	return t
}

// BuiltinType returns a built-in simple type by local name, or nil.
//
// It exists so that a caller validating a lone value against, say, xs:date does
// not have to construct a schema first.
func BuiltinType(local string) *SimpleType {
	t, _ := builtinTypes()[xsName(local)].(*SimpleType)
	return t
}
