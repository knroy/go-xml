// Package xsd implements XML Schema 1.0 and 1.1 validation.
//
// The package is organised around the spec's own vocabulary. XSD is defined in
// two layers: a *schema component model* (Part 1 §3), which is an abstract data
// structure, and a set of *validation rules* over that model. The XML syntax of
// a .xsd file is a third thing again — a concrete representation that maps onto
// components. Keeping the three apart is what makes the spec tractable, so the
// types here are named after components rather than after elements: a
// ComplexType is not an <xs:complexType>, it is what one maps to.
//
// # What is implemented
//
// XSD 1.0 and 1.1, both targeting the W3C xsdtests suite: the component model,
// schema assembly through include, import, redefine and override, content
// models, simple types and facets, xsi:type and xsi:nil, substitution groups,
// wildcards, identity constraints, and document-level ID/IDREF.
//
// 1.1 is opt-in through Options.Version, because it changes which documents
// are valid and a 1.0 schema must not acquire its behaviour by accident. It
// adds assertions, conditional type assignment with xs:alternative and
// inheritable attributes, open content, xs:override, the notNamespace and
// notQName wildcard forms, explicitTimezone, and conditional inclusion through
// the versioning attributes. The 1.1 constructs are always *parsed* — a schema
// that uses one is not made valid by pretending it is absent — but they are
// only honoured under Version11.
//
// One constraint is deliberately not checked: Particle Valid (Restriction).
// See CheckConstraints.
//
// # Concurrency
//
// A Schema is immutable once loaded and safe to validate from any number of
// goroutines. Its one piece of lazily-built state, the content-model cache, is
// synchronised for that reason. A Schema still being assembled is not safe to
// share, and neither is a document tree being validated: parse one per
// goroutine.
//
// # Errata
//
// The 2nd Edition text folds in the 1st Edition errata, and this implementation
// follows the corrected text. Three corrections change behaviour enough to name
// here, because the uncorrected reading is the intuitive one:
//
//   - E1-26: an xs:all group may carry minOccurs="0". The original text
//     required {min occurs}=1.
//   - E2-30: a pattern facet on a list type matches the whole space-separated
//     literal, not each item separately.
//   - E1-51: the ur-type's attribute wildcard is processContents="lax", not
//     strict.
package xsd

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// Namespaces used throughout the spec.
const (
	// NSSchema is the XML Schema namespace: the one <xs:schema> lives in.
	NSSchema = "http://www.w3.org/2001/XMLSchema"
	// NSInstance is the schema-instance namespace, holding xsi:type,
	// xsi:nil, xsi:schemaLocation and xsi:noNamespaceSchemaLocation.
	NSInstance = "http://www.w3.org/2001/XMLSchema-instance"
	// NSXML is the namespace of xml:lang, xml:space, xml:base and xml:id,
	// which a schema may reference without importing.
	NSXML = "http://www.w3.org/XML/1998/namespace"
)

// A Component is any of the schema components of Part 1 §3.
//
// The interface exists to give the component kinds a common type for
// diagnostics and for the schema-level tables; it deliberately says nothing
// about validation, because the components are data and the validation rules
// are separate.
type Component interface {
	// ComponentKind names the kind for error messages.
	ComponentKind() string
}

// Derivation methods, used by the {final}, {block}, {prohibited substitutions}
// and {disallowed substitutions} property sets.
//
// These are sets rather than single values, and — a detail worth stating
// because conflating them is a classic bug — an element declaration carries
// *two* of them with different meanings. {disallowed substitutions} controls
// what may substitute for the element in an instance; {substitution group
// exclusions} controls what may join its substitution group at schema
// construction time. They are consulted at different moments.
type Derivation uint8

// The derivation methods. DerivationSubstitution is only meaningful in
// {disallowed substitutions}; the others apply to types as well.
const (
	DerivationExtension Derivation = 1 << iota
	DerivationRestriction
	DerivationList
	DerivationUnion
	DerivationSubstitution
)

// DerivationSet is a set of derivation methods. The zero value is the empty
// set, which is the correct default for every property that uses it.
type DerivationSet uint8

// Has reports whether d is in the set.
func (s DerivationSet) Has(d Derivation) bool { return uint8(s)&uint8(d) != 0 }

// With returns the set with d added.
func (s DerivationSet) With(d Derivation) DerivationSet {
	return DerivationSet(uint8(s) | uint8(d))
}

// All is the set of every derivation method, the meaning of final="#all".
const All = DerivationSet(0xff)

// String renders the set the way a schema author wrote it.
func (s DerivationSet) String() string {
	if s == 0 {
		return ""
	}
	if s == All {
		return "#all"
	}
	var out []byte
	add := func(d Derivation, name string) {
		if s.Has(d) {
			if len(out) > 0 {
				out = append(out, ' ')
			}
			out = append(out, name...)
		}
	}
	add(DerivationExtension, "extension")
	add(DerivationRestriction, "restriction")
	add(DerivationList, "list")
	add(DerivationUnion, "union")
	add(DerivationSubstitution, "substitution")
	return string(out)
}

// Scope distinguishes a global declaration from a local one.
//
// The distinction is not cosmetic: a global element declaration can be the head
// of a substitution group and can be the validation root, while a local one is
// reachable only through the type that contains it. Two local declarations with
// the same name in different types are different components.
type Scope uint8

// The scopes a declaration can have.
const (
	ScopeGlobal Scope = iota
	ScopeLocal
)

// ValueConstraint is the {value constraint} property: a default or fixed value.
//
// The distinction matters at validation time, not just at defaulting time: a
// fixed value must equal the value in the instance, while a default supplies
// one when the instance has none.
type ValueConstraint struct {
	// Fixed distinguishes fixed="v" from default="v".
	Fixed bool
	// Lexical is the value as written in the schema document. It is stored
	// unparsed because the type it must be parsed against is not always
	// known when the schema is read.
	Lexical string
	// Value is the parsed form, filled in once the type is resolved.
	Value *xdm.Atomic
}

// ElementDecl is an Element Declaration (§3.3.1).
type ElementDecl struct {
	Name       xdm.QName
	Type       Type
	// unresolved names a type reference that no definition matched. The
	// spec makes this an error only where the declaration is actually used
	// (missing001: "Error only if the element declaration is needed for
	// validation"), so it is carried here and reported at use rather than
	// failing the schema at load.
	unresolved string
	Scope      Scope
	Nillable   bool
	Constraint *ValueConstraint
	Abstract   bool

	// SubstitutionGroup is the {substitution group affiliation}: the head
	// this declaration may substitute for. Only a global declaration may
	// have one (erratum E1-36 requires the head be global too).
	//
	// It is the first of SubstitutionGroups, kept as its own field because
	// almost every use has exactly one head.
	SubstitutionGroup *ElementDecl

	// SubstitutionGroups holds every head, which XSD 1.1 permits to be a
	// list where 1.0 allowed only one.
	SubstitutionGroups []*ElementDecl

	// DisallowedSubstitutions is {disallowed substitutions}, from block=.
	// It controls substitution in an instance.
	DisallowedSubstitutions DerivationSet

	// SubstitutionGroupExclusions is {substitution group exclusions}, from
	// final=. It controls what may derive from this declaration's type and
	// still substitute. Kept separate from DisallowedSubstitutions because
	// the two are consulted at different times and conflating them is a
	// classic source of wrong answers.
	SubstitutionGroupExclusions DerivationSet

	// IdentityConstraints holds the key, keyref and unique children.
	IdentityConstraints []*IdentityConstraint

	// Alternatives are the XSD 1.1 <xs:alternative> children, in order.
	// The first whose test holds selects the type; conditional type
	// assignment is the other half of what 1.1 needs XPath for.
	Alternatives []*TypeAlternative

	// substitutable caches the transitive substitution group members. It is
	// computed after the whole schema is assembled, because a member may be
	// declared in a document that has not been read yet.
	substitutable []*ElementDecl
}

// ComponentKind implements Component.
func (*ElementDecl) ComponentKind() string { return "element declaration" }

// AttributeDecl is an Attribute Declaration (§3.2.1).
//
// An attribute's type is always a simple type: XSD has no way to give an
// attribute element content.
type AttributeDecl struct {
	Name       xdm.QName
	Type       *SimpleType
	Scope      Scope
	Constraint *ValueConstraint

	// builtin marks a declaration this implementation supplies rather than
	// one read from a document — the xsi attributes and the four in the XML
	// namespace. A schema may declare the latter itself, so a supplied
	// declaration gives way to a real one instead of colliding with it.
	builtin bool
}

// ComponentKind implements Component.
func (*AttributeDecl) ComponentKind() string { return "attribute declaration" }

// AttributeUse is an Attribute Use (§3.5.1).
//
// This is the component that a <xs:attribute> inside a complex type maps to. It
// is distinct from the declaration because the same declaration can be used
// with different requiredness or a different value constraint in different
// types.
type AttributeUse struct {
	Required   bool
	Decl       *AttributeDecl
	Constraint *ValueConstraint

	// Inheritable is the XSD 1.1 {inheritable}: the attribute is visible to
	// conditional type assignment on descendant elements, not only on the
	// element carrying it. It is how a schema lets an ancestor's xml:lang
	// choose a descendant's type.
	Inheritable bool

	// Prohibited marks use="prohibited": the attribute is removed rather
	// than declared. Such a use is not part of the type's {attribute uses}
	// — it exists only so that inheritance can tell "this name was ruled
	// out" from "this name was never mentioned", which is what stops the
	// base's use being inherited straight back.
	Prohibited bool
}

// ComponentKind implements Component.
func (*AttributeUse) ComponentKind() string { return "attribute use" }

// Type is a type definition: either a SimpleType or a ComplexType.
//
// The interface is deliberately narrow. Almost every validation rule needs to
// know a type's name, its base, and how it may be derived from; the rules that
// need more do a type switch, because what they need differs entirely between
// the two kinds.
type Type interface {
	Component

	// TypeName is the {name} and {target namespace}. An anonymous type has
	// the zero QName, which is why this cannot simply be a field access.
	TypeName() xdm.QName

	// BaseType is {base type definition}. For xs:anyType this is xs:anyType
	// itself — a deliberate self-loop in the spec, so any walk up the base
	// chain must test for it rather than for nil.
	BaseType() Type

	// Final is {final}: the derivations this type forbids.
	Final() DerivationSet

	// isType keeps the interface closed to this package.
	isType()
}

// ContentKind discriminates the {content type} of a complex type (§3.4.1).
//
// The spec models {content type} as a three-way sum: empty, a simple type, or a
// particle paired with mixed/element-only. Go has no sum type, so the kind is
// explicit and the payload fields are only meaningful for the matching kind.
type ContentKind uint8

// The content kinds.
const (
	// ContentEmpty permits no children at all.
	ContentEmpty ContentKind = iota
	// ContentSimple permits character data validated against a simple type,
	// and no element children.
	ContentSimple
	// ContentElementOnly permits element children matching a particle, and
	// no character data other than whitespace.
	ContentElementOnly
	// ContentMixed permits element children matching a particle, with
	// character data interleaved freely.
	ContentMixed
)

// String names the kind for diagnostics.
func (k ContentKind) String() string {
	switch k {
	case ContentEmpty:
		return "empty"
	case ContentSimple:
		return "simple"
	case ContentElementOnly:
		return "element-only"
	case ContentMixed:
		return "mixed"
	}
	return fmt.Sprintf("ContentKind(%d)", uint8(k))
}

// ComplexType is a Complex Type Definition (§3.4.1).
type ComplexType struct {
	Name      xdm.QName
	Base      Type
	Abstract  bool
	FinalSet  DerivationSet
	Prohibits DerivationSet

	// DerivationMethod is {derivation method}: extension or restriction.
	DerivationMethod Derivation

	// Content is the {content type} discriminator; SimpleContent and
	// Particle carry the payload for the kinds that have one.
	Content       ContentKind
	SimpleContent *SimpleType
	Particle      *Particle

	AttributeUses     []*AttributeUse
	AttributeWildcard *Wildcard

	// Assertions are the XSD 1.1 <xs:assert> co-constraints on this type
	// (§3.4.1 {assertions}). They evaluate XPath 2.0 against the element
	// being validated, which is the feature that makes XSD 1.1 unavailable
	// to implementations without an XPath engine.
	Assertions []*Assertion

	// OpenContent is the XSD 1.1 {open content}, which permits elements the
	// content model does not name. Nil means the type is closed.
	OpenContent *OpenContent

	// declaredOpenContent records that the type wrote its own
	// <xs:openContent>, so that a document-level <xs:defaultOpenContent>
	// does not override it.
	declaredOpenContent bool
}

// OpenContentMode says where an open content wildcard may match (XSD 1.1
// §3.4.1).
type OpenContentMode uint8

// The open content modes.
const (
	// OpenNone is a closed content model: the default.
	OpenNone OpenContentMode = iota
	// OpenInterleave permits the wildcard to match anywhere among the
	// content model's own elements.
	OpenInterleave
	// OpenSuffix permits it only after everything the content model
	// requires.
	OpenSuffix
)

// OpenContent is an <xs:openContent> or <xs:defaultOpenContent> (XSD 1.1).
//
// It is how 1.1 lets a schema say "and anything else may appear here" without
// writing a wildcard into every content model, which is what makes a schema
// forward-compatible with documents produced against a later version of it.
type OpenContent struct {
	Mode     OpenContentMode
	Wildcard *Wildcard
}

// ComponentKind implements Component.
func (*ComplexType) ComponentKind() string { return "complex type definition" }

// TypeName implements Type.
func (t *ComplexType) TypeName() xdm.QName { return t.Name }

// BaseType implements Type.
func (t *ComplexType) BaseType() Type { return t.Base }

// Final implements Type.
func (t *ComplexType) Final() DerivationSet { return t.FinalSet }

func (*ComplexType) isType() {}

// Variety discriminates a simple type (Part 2 §4.1.1).
type Variety uint8

// The simple type varieties.
const (
	// VarietyAtomic is a single indivisible value.
	VarietyAtomic Variety = iota
	// VarietyList is a whitespace-separated sequence of item-type values.
	VarietyList
	// VarietyUnion is a value drawn from any of several member types.
	VarietyUnion
)

// String names the variety for diagnostics.
func (v Variety) String() string {
	switch v {
	case VarietyAtomic:
		return "atomic"
	case VarietyList:
		return "list"
	case VarietyUnion:
		return "union"
	}
	return fmt.Sprintf("Variety(%d)", uint8(v))
}

// SimpleType is a Simple Type Definition (Part 2 §4.1.1).
//
// Part 1 §3.14.1 also defines this component, but that section is marked
// non-normative and disagrees with Part 2 about {final}. Part 2 governs here.
type SimpleType struct {
	// unresolved names a type reference within this definition that no
	// definition matched — a list's item type or a union's member. As with
	// an element declaration, the spec makes this an error only where the
	// type is used (missing006: "Error only if the list type is needed for
	// validation"), so it is reported against the value that reaches it.
	unresolved string

	Name     xdm.QName
	Base     Type
	FinalSet DerivationSet
	Variety  Variety

	// Primitive is the primitive type this one erases to, for atomic
	// varieties. A primitive type is its own primitive.
	Primitive *SimpleType

	// ItemType is {item type definition}, meaningful only for VarietyList.
	ItemType *SimpleType

	// MemberTypes is {member type definitions}, meaningful only for
	// VarietyUnion. Order is significant: validation takes the *first*
	// member that accepts the value, not the best or longest match.
	MemberTypes []*SimpleType

	// Facets are the constraining facets applied at this derivation step.
	// A value must satisfy these and every ancestor's; see facet.go for how
	// they combine, which is not simply union.
	Facets *FacetSet

	// builtin marks the types this package defines rather than a schema
	// document. Built-ins are exempt from a few schema-construction rules.
	builtin bool
}

// ComponentKind implements Component.
func (*SimpleType) ComponentKind() string { return "simple type definition" }

// TypeName implements Type.
func (t *SimpleType) TypeName() xdm.QName { return t.Name }

// BaseType implements Type.
func (t *SimpleType) BaseType() Type { return t.Base }

// Final implements Type.
func (t *SimpleType) Final() DerivationSet { return t.FinalSet }

func (*SimpleType) isType() {}

// Compositor is the kind of a model group (§3.8.1).
type Compositor uint8

// The compositors.
const (
	// CompositorSequence requires its particles in order.
	CompositorSequence Compositor = iota
	// CompositorChoice requires exactly one of its particles.
	CompositorChoice
	// CompositorAll requires each particle at most once, in any order.
	// XSD 1.0 constrains where an all group may appear and what it may
	// contain; see All Group Limited (§3.8.6).
	CompositorAll
)

// String names the compositor for diagnostics.
func (c Compositor) String() string {
	switch c {
	case CompositorSequence:
		return "sequence"
	case CompositorChoice:
		return "choice"
	case CompositorAll:
		return "all"
	}
	return fmt.Sprintf("Compositor(%d)", uint8(c))
}

// ModelGroup is a Model Group (§3.8.1).
type ModelGroup struct {
	Compositor Compositor
	Particles  []*Particle
}

// ComponentKind implements Component.
func (*ModelGroup) ComponentKind() string { return "model group" }

// ModelGroupDef is a Model Group Definition (§3.7.1) — a named <xs:group>.
//
// It is a separate component from the group it names because a reference to it
// is a reference to the *definition*, and erratum E1-29 makes that distinction
// observable: particles reached through two references to the same definition
// are still distinct particles for the purpose of Unique Particle Attribution.
type ModelGroupDef struct {
	Name  xdm.QName
	Group *ModelGroup
}

// ComponentKind implements Component.
func (*ModelGroupDef) ComponentKind() string { return "model group definition" }

// Unbounded is the {max occurs} of a particle written maxOccurs="unbounded".
const Unbounded = -1

// Particle is a Particle (§3.9.1).
//
// A particle is the occurrence-constrained use of a term. It has exactly three
// properties and, notably, no annotation — it is pure structure.
type Particle struct {
	MinOccurs int
	// MaxOccurs is Unbounded for maxOccurs="unbounded".
	MaxOccurs int
	Term      Term
}

// ComponentKind implements Component.
func (*Particle) ComponentKind() string { return "particle" }

// Term is what a particle constrains: an element declaration, a wildcard, or a
// model group.
type Term interface {
	Component
	isTerm()
}

func (*ElementDecl) isTerm() {}
func (*Wildcard) isTerm()    {}
func (*ModelGroup) isTerm()  {}

// ProcessContents says how strictly a wildcard's matched content is validated
// (§3.10.1).
type ProcessContents uint8

// The processContents modes.
const (
	// ProcessStrict requires a declaration to be found and the content to
	// be valid against it.
	ProcessStrict ProcessContents = iota
	// ProcessLax validates against a declaration if one is found, and
	// otherwise accepts. This is the ur-type's mode (erratum E1-51).
	ProcessLax
	// ProcessSkip accepts without looking for a declaration.
	ProcessSkip
)

// String names the mode for diagnostics.
func (p ProcessContents) String() string {
	switch p {
	case ProcessStrict:
		return "strict"
	case ProcessLax:
		return "lax"
	case ProcessSkip:
		return "skip"
	}
	return fmt.Sprintf("ProcessContents(%d)", uint8(p))
}

// NamespaceConstraintKind discriminates a wildcard's {namespace constraint}.
type NamespaceConstraintKind uint8

// The namespace constraint kinds.
const (
	// NSAny is ##any: every namespace, and unqualified names.
	NSAny NamespaceConstraintKind = iota
	// NSNot is ##other: every namespace except the named one — and *not*
	// unqualified names. The exclusion of the absent namespace is explicit
	// in Wildcard allows Namespace Name clause 2.3 and is easy to miss.
	NSNot
	// NSEnumerated is an explicit list, in which the empty string stands
	// for the absent namespace (##local).
	NSEnumerated
)

// Wildcard is a Wildcard (§3.10.1) — what <xs:any> and <xs:anyAttribute> map to.
type Wildcard struct {
	Kind            NamespaceConstraintKind
	ProcessContents ProcessContents

	// Namespace is the excluded namespace for NSNot, or the permitted set
	// for NSEnumerated. It is unused for NSAny.
	Namespace []string

	// ExcludesAbsent records whether the absent namespace is excluded by an
	// NSNot constraint.
	//
	// The two spellings differ here and the difference is easy to miss.
	// XSD 1.0's ##other excludes unqualified names unconditionally —
	// clause 2.3 of Wildcard allows Namespace Name — whereas XSD 1.1's
	// notNamespace excludes only what it lists, so an unqualified name is
	// permitted unless ##local appears. Applying ##other's rule to
	// notNamespace rejects every unqualified attribute the wildcard was
	// written to admit.
	ExcludesAbsent bool

	// DisallowedNames is XSD 1.1's {disallowed names} (§3.10.1): specific
	// expanded names the wildcard refuses even though their namespace is
	// admitted. It is the notQName attribute, and it is what lets a schema
	// say "anything from this namespace except these".
	//
	// The namespace constraint and this set are independent tests: a name
	// matches the wildcard only if the namespace admits it *and* it is not
	// disallowed. That ordering matters because notQName may name a
	// namespace the constraint would otherwise let through, which is the
	// only reason to write it.
	DisallowedNames []xdm.QName

	// DisallowDefined is ##defined: refuse any name for which the schema
	// has a global declaration of the matching kind. It is how a schema
	// writes "anything the schema does not already know about", which is
	// the useful form of an extension wildcard — one that cannot silently
	// shadow a declared element.
	DisallowDefined bool

	// DisallowDefinedSibling is ##definedSibling: refuse any name declared
	// by some other particle in the same content model. Unlike ##defined it
	// is local, and it applies to elements only — an attribute wildcard has
	// no siblings in the sense the keyword means, and the schema for
	// schemas does not permit it there.
	DisallowDefinedSibling bool

	// siblingNames are the element names declared alongside this wildcard
	// in its content model, resolved once the particle tree is complete.
	//
	// It is stored on the wildcard rather than looked up at validation time
	// because the content model that gives "sibling" its meaning is not
	// reachable from the wildcard, and computing it per element would
	// re-walk the particle tree for every item validated.
	siblingNames map[xdm.QName]bool
}

// ComponentKind implements Component.
func (*Wildcard) ComponentKind() string { return "wildcard" }

// Disallows reports whether a name is excluded by {disallowed names}.
//
// The definedNames callback answers ##defined, which needs the schema and so
// cannot be decided by the wildcard alone.
func (w *Wildcard) Disallows(name xdm.QName, defined func(xdm.QName) bool) bool {
	for _, n := range w.DisallowedNames {
		if n == name {
			return true
		}
	}
	if w.DisallowDefined && defined != nil && defined(name) {
		return true
	}
	if w.DisallowDefinedSibling && w.siblingNames[name] {
		return true
	}
	return false
}

// AllowsName reports whether the wildcard admits an expanded name, applying
// both the namespace constraint and {disallowed names}.
func (w *Wildcard) AllowsName(name xdm.QName, defined func(xdm.QName) bool) bool {
	return w.Allows(name.URI) && !w.Disallows(name, defined)
}

// Allows reports whether the wildcard permits a name in namespace ns, where the
// empty string means the absent namespace.
//
// The NSNot case is the one worth reading twice: ##other excludes the absent
// namespace as well as the named one, so an unqualified element never matches a
// ##other wildcard.
func (w *Wildcard) Allows(ns string) bool {
	switch w.Kind {
	case NSAny:
		return true
	case NSNot:
		if ns == "" {
			return !w.ExcludesAbsent
		}
		for _, n := range w.Namespace {
			if n == ns {
				return false
			}
		}
		return true
	case NSEnumerated:
		for _, n := range w.Namespace {
			if n == ns {
				return true
			}
		}
		return false
	}
	return false
}

// AttributeGroupDef is an Attribute Group Definition (§3.6.1).
type AttributeGroupDef struct {
	Name              xdm.QName
	AttributeUses     []*AttributeUse
	AttributeWildcard *Wildcard

	// refs are the attribute groups this one references. They are kept
	// rather than flattened at parse time because a group's own uses may
	// still be arriving when something reads it: the references resolve
	// through fixups whose order no reference can arrange. Reading through
	// the graph makes the order irrelevant, and the nesting depth with it.
	refs []*AttributeGroupDef
}

// ComponentKind implements Component.
func (*AttributeGroupDef) ComponentKind() string { return "attribute group definition" }

// IdentityConstraintKind discriminates key, keyref and unique (§3.11.1).
type IdentityConstraintKind uint8

// The identity constraint categories.
const (
	// ICKey requires the field values to be present and unique.
	ICKey IdentityConstraintKind = iota
	// ICUnique requires uniqueness but permits absence.
	ICUnique
	// ICKeyref requires each value to match one in a referenced key.
	ICKeyref
)

// String names the category for diagnostics.
func (k IdentityConstraintKind) String() string {
	switch k {
	case ICKey:
		return "key"
	case ICUnique:
		return "unique"
	case ICKeyref:
		return "keyref"
	}
	return fmt.Sprintf("IdentityConstraintKind(%d)", uint8(k))
}

// IdentityConstraint is an Identity-constraint Definition (§3.11.1).
type IdentityConstraint struct {
	Name     xdm.QName
	Kind     IdentityConstraintKind
	Selector *ICPath
	Fields   []*ICPath

	// Refer is the key or unique that a keyref points at. It is nil for
	// ICKey and ICUnique.
	Refer *IdentityConstraint

	// resolved is set on the placeholder standing for an XSD 1.1
	// <xs:key ref="..."/> once the name it references has been found. The
	// parser then substitutes the named component for the placeholder, so
	// that no copy of a constraint ever reaches validation.
	resolved *IdentityConstraint
}

// ComponentKind implements Component.
func (*IdentityConstraint) ComponentKind() string { return "identity-constraint definition" }

// ICPath is a compiled selector or field XPath.
//
// The subset the spec permits is far smaller than XPath 1.0, let alone the
// XPath 2.0 this repository implements. It is compiled by a dedicated parser
// rather than the general one, because accepting more than the subset would
// accept schemas that conforming processors reject.
type ICPath struct {
	// Source is the expression as written, kept for diagnostics.
	Source string
	// Alternatives are the "|"-separated branches.
	Alternatives []ICPathAlternative
}

// ICPathAlternative is one "|"-separated branch of a selector or field path.
type ICPathAlternative struct {
	// DescendantOrSelf records a leading ".//".
	DescendantOrSelf bool
	// Steps are the child-axis steps.
	Steps []ICStep
	// Attribute is the trailing "@name" step, which only a field may have
	// and only in final position.
	Attribute *xdm.QName

	// AttributeWildcard records that the attribute step was written "@*"
	// or "@prefix:*". Such a field is grammatical; whether it selects
	// exactly one node is decided per instance document, not here.
	AttributeWildcard bool
}

// ICStep is one step of an identity-constraint path.
type ICStep struct {
	// Wildcard records "*", which matches any element name.
	Wildcard bool
	// Name is the element name when Wildcard is false.
	Name xdm.QName
}

// NotationDecl is a Notation Declaration (§3.12.1).
type NotationDecl struct {
	Name   xdm.QName
	Public string
	System string
}

// ComponentKind implements Component.
func (*NotationDecl) ComponentKind() string { return "notation declaration" }

// String names a derivation method, so that a diagnostic reads "extension"
// rather than the bit value.
func (d Derivation) String() string {
	switch d {
	case DerivationExtension:
		return "extension"
	case DerivationRestriction:
		return "restriction"
	case DerivationSubstitution:
		return "substitution"
	case DerivationList:
		return "list"
	case DerivationUnion:
		return "union"
	}
	return "derivation"
}
