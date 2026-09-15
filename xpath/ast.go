package xpath

import (
	"github.com/knroy/go-xml/xdm"
)

// Expr is a node in the XPath abstract syntax tree.
//
// Evaluation is a method on the AST rather than a separate visitor. XPath
// evaluation is a simple recursive walk with no multi-pass analysis, so a
// visitor would add an indirection layer without buying anything; the one
// place a second pass would help (static typing) is not implemented, and the
// spec permits a dynamically-typed implementation.
type Expr interface {
	// Eval evaluates the expression in ctx and returns a sequence.
	Eval(ctx *Context) (xdm.Sequence, error)
	// String returns a source-like rendering, used in error messages and to
	// make test failures readable.
	String() string
}

// --- Literals and simple terms ---------------------------------------------

// Literal is a constant atomic value.
type Literal struct{ Val *xdm.Atomic }

// VarRef is a variable reference, $name.
type VarRef struct{ Name xdm.QName }

// ContextItem is the "." expression.
type ContextItem struct{}

// --- Operators --------------------------------------------------------------

// BinaryOp is any infix operator. Keeping them in one node with an Op field
// rather than one type per operator keeps the parser's precedence ladder short
// and puts all the operand-conversion rules in one evaluator function, which
// is where they are easiest to check against the spec's tables.
type BinaryOp struct {
	Op          string
	Left, Right Expr
	// ResolveQName binds a prefix in the static context of this operator, for
	// the one conversion that needs it. A general comparison casts an
	// untypedAtomic operand to the *other* operand's type, and when that type
	// is xs:QName the lexical form carries a prefix whose namespace lives in
	// the static context -- which the runtime Context deliberately does not
	// carry, since namespaces are a static property. Capturing the resolver
	// on the node is what makes the binding available where the cast happens.
	// Nil for every operator other than a comparison, and for a comparison
	// parsed without a namespace resolver.
	ResolveQName func(prefix string) (string, bool)
}

// UnaryOp is prefix + or -.
type UnaryOp struct {
	Op      string
	Operand Expr
}

// --- Paths ------------------------------------------------------------------

// Axis identifies one of the thirteen XPath axes.
type Axis int

const (
	AxisChild Axis = iota
	AxisDescendant
	AxisAttribute
	AxisSelf
	AxisDescendantOrSelf
	AxisFollowingSibling
	AxisFollowing
	AxisParent
	AxisAncestor
	AxisPrecedingSibling
	AxisPreceding
	AxisAncestorOrSelf
	AxisNamespace
)

var axisNames = map[string]Axis{
	"child": AxisChild, "descendant": AxisDescendant, "attribute": AxisAttribute,
	"self": AxisSelf, "descendant-or-self": AxisDescendantOrSelf,
	"following-sibling": AxisFollowingSibling, "following": AxisFollowing,
	"parent": AxisParent, "ancestor": AxisAncestor,
	"preceding-sibling": AxisPrecedingSibling, "preceding": AxisPreceding,
	"ancestor-or-self": AxisAncestorOrSelf, "namespace": AxisNamespace,
}

var axisStrings = func() map[Axis]string {
	m := make(map[Axis]string, len(axisNames))
	for k, v := range axisNames {
		m[v] = k
	}
	return m
}()

// IsReverse reports whether the axis is a reverse axis. Reverse axes number
// their positions backwards, which changes what position() means inside a
// predicate — the one place the distinction is observable.
func (a Axis) IsReverse() bool {
	switch a {
	case AxisParent, AxisAncestor, AxisAncestorOrSelf, AxisPreceding, AxisPrecedingSibling:
		return true
	}
	return false
}

// PrincipalKind is the node kind an axis selects when the node test is a
// name or wildcard: attributes on the attribute axis, namespaces on the
// namespace axis, elements everywhere else.
func (a Axis) PrincipalKind() xdm.NodeKind {
	switch a {
	case AxisAttribute:
		return xdm.KindAttribute
	case AxisNamespace:
		return xdm.KindNamespace
	}
	return xdm.KindElement
}

// Step is one step of a path: an axis, a node test, and zero or more
// predicates.
type Step struct {
	Axis       Axis
	Test       NodeTest
	Predicates []Expr

	// Explicit records that the axis was written out ("child::x") rather
	// than abbreviated ("x"). Section 5.5.3 gives the abbreviated child axis
	// in a pattern a wider reach than the written one — it is evaluated on
	// the child-or-top axis, so "document-node()" alone matches the document
	// node while "child::document-node()" is legal but matches nothing,
	// since a document node is never a child.
	Explicit bool
}

// PathExpr is a sequence of steps evaluated left to right, each against the
// nodes produced by the previous one.
type PathExpr struct {
	// Root marks a path that starts at the document root ("/foo" rather
	// than "foo").
	Root  bool
	Steps []Expr // Step, or an arbitrary expression in "(...)/foo" form
}

// FilterExpr applies predicates to an arbitrary expression, as in "(1 to 10)[. mod 2 = 0]".
type FilterExpr struct {
	Base       Expr
	Predicates []Expr
}

// --- Node tests -------------------------------------------------------------

// NodeTest decides whether a node on an axis is selected.
type NodeTest interface {
	// Matches reports whether n is selected on an axis whose principal node
	// kind is principal.
	Matches(n *xdm.Node, principal xdm.NodeKind) bool
	String() string
}

// NameTest matches by expanded name.
type NameTest struct {
	// Name is the expanded name to match. Wildcards leave one or both parts
	// unconstrained; see AnyURI and AnyLocal.
	Name xdm.QName
	// AnyURI matches any namespace ("*" and "*:local").
	AnyURI bool
	// AnyLocal matches any local name ("*" and "prefix:*").
	AnyLocal bool
}

// KindTest matches by node kind: text(), comment(), node(), element(name),
// and so on.
type KindTest struct {
	Kind xdm.NodeKind
	// Any matches every kind: the node() test.
	Any bool
	// Name constrains element()/attribute()/processing-instruction() tests
	// that name a target.
	Name    *xdm.QName
	HasName bool
	// Content constrains the root element of a document-node() test:
	// document-node(element(invoice)) matches only a document whose element
	// child satisfies the inner test. Nil means the document's content is
	// unconstrained.
	Content NodeTest
	// TypeName is the second argument of element(name, type) and its
	// attribute() counterpart, resolved to the key the data model records
	// type annotations under: a namespace-qualified {uri}local for a schema
	// type, the bare local name for a built-in. The empty string means the
	// test carried no type argument and constrains only the name.
	//
	// It is resolved at parse time because the prefix binding lives in the
	// static context, which is gone by the time the test runs. Comparing the
	// lexical form instead forced the comparison down to local parts, and two
	// types sharing a local name in different namespaces then matched each
	// other.
	TypeName string
	// TypeNameLexical is TypeName as the author wrote it, kept only so that
	// String() renders the test back in the syntax it was parsed from.
	TypeNameLexical string
	// TypeUnionMembers are the annotation keys of TypeName's member types,
	// transitively, when TypeName is a union type.
	//
	// XPath 3.1 2.5.5 makes union membership a clause of derives-from in its
	// own right, and a node validated against a union is annotated with the
	// MEMBER that accepted it rather than with the union. So an attribute
	// declared as a union of my:partNumberType and xs:integer whose value is
	// "44" is annotated "integer", and attribute(*, my:partIntegerUnion)
	// matches it only by knowing the members. match-232 asserts exactly that.
	//
	// The relation runs one way only. These names admit a MEMBER-annotated
	// node to the UNION's test; they say nothing about the reverse, and a
	// node annotated with the union does not match a test naming one member.
	//
	// Resolved at parse time, while the schema is still reachable, for the
	// same reason SubstitutionGroup is. nil when TypeName is not a union.
	TypeUnionMembers []string
	// TypeNillable records the "?" of element(name, type?), which lets a
	// nilled element match even though its content is absent.
	TypeNillable bool
	// SchemaDeclared marks a schema-element() or schema-attribute() test,
	// which names a global declaration rather than an element name. It
	// matches the named declaration and, for an element, the members of its
	// substitution group.
	SchemaDeclared bool
	// SubstitutionGroup holds the other names schema-element(E) admits: the
	// members of E's substitution group, resolved from the imported schema
	// when the test was parsed.
	//
	// It is resolved at parse time because nothing carries a schema into the
	// evaluator, and the group is fixed once the schema is imported. Nil for
	// every test that is not a schema-element(), and for a declaration that
	// heads no group.
	SubstitutionGroup []xdm.QName
	// DeclaredType is the local name of the type the schema-element() or
	// schema-attribute() declaration names, resolved at parse time for the
	// same reason SubstitutionGroup is. A node whose annotation is neither
	// that type nor derived from it was validated against some *other*
	// declaration of the same name — a local one — and does not match.
	//
	// Empty when the declaration's type is anonymous, in which case there is
	// no name to compare and the test checks only that the node was
	// validated.
	DeclaredType string
}

// --- Sequence-level constructs ---------------------------------------------

// IfExpr is "if (cond) then a else b". Both branches are required by the
// grammar; there is no one-armed form.
type IfExpr struct {
	Cond, Then, Else Expr
}

// QuantifiedExpr is "some $x in seq satisfies test" or the "every" form.
type QuantifiedExpr struct {
	Every    bool
	Bindings []Binding
	Test     Expr
}

// ForExpr is "for $x in seq return expr".
type ForExpr struct {
	Bindings []Binding
	Return   Expr
}

// Binding is one "$var in expr" clause of a for or quantified expression, or
// one "$var := expr" clause of a let expression.
type Binding struct {
	Var xdm.QName
	Seq Expr
}

// NamedFunctionRef is "fn:concat#3", added in XPath 3.0: a reference to a
// named function of a given arity, as a value.
//
// The arity is part of the reference because it is part of a function's
// identity — fn:concat#2 and fn:concat#3 are different functions — and because
// the name alone would not say which of an overloaded set is meant.
type NamedFunctionRef struct {
	Name  xdm.QName
	Arity int
	// Cast is the cast a reference to the CONSTRUCTOR FUNCTION of an imported
	// schema type stands for, with the argument reachable as
	// ConstructorArgVar. Nothing registers such a function in the library --
	// the set of them is not known until a schema is imported -- so the
	// reference is resolved here, while the parser's schema hook is still in
	// reach, exactly as foldSchemaConstructor resolves an ordinary call.
	// nil for every other reference.
	Cast Expr
}

// InlineFunctionExpr is "function($x as xs:integer) as xs:integer { $x + 1 }",
// added in XPath 3.0: a function written where a value is expected.
//
// The body closes over the variables in scope where it is written, which is
// what makes it more than a named function without a name.
type InlineFunctionExpr struct {
	Params []InlineParam
	// Result is the declared return type, or nil if none was written.
	Result *SequenceType
	Body   Expr
}

// InlineParam is one parameter of an inline function.
type InlineParam struct {
	Name xdm.QName
	// Type is the declared parameter type, or nil if none was written, in
	// which case it is item()*.
	Type *SequenceType
}

// ArgumentPlaceholder is the "?" of a partial function application,
// production [61]. It is never evaluated: a call whose argument list holds one
// yields a new function item rather than a result.
type ArgumentPlaceholder struct{}

// DynamicCall is "$f(1, 2)": a call on the function item an expression
// produces, rather than on a statically named function.
//
// It is the ArgumentList half of production [48], PostfixExpr ::= PrimaryExpr
// (Predicate | ArgumentList)*, which is why it wraps an arbitrary expression
// rather than a name.
type DynamicCall struct {
	Target Expr
	Args   []Expr
}

// LetExpr is "let $x := expr return expr", added in XPath 3.0.
//
// It is not a ForExpr with a different keyword. "for" iterates, binding its
// variable to one item at a time and concatenating the results; "let" binds
// the whole sequence once and evaluates its body once. "let $x := (1, 2)
// return count($x)" is 2, where the corresponding "for" is (1, 1).
type LetExpr struct {
	Bindings []Binding
	Return   Expr
}

// FuncCall is a function call. Resolution happens at evaluation time against
// the context's function library, so that a stylesheet's own xsl:function
// declarations are visible without a separate binding pass.
type FuncCall struct {
	Name xdm.QName
	Args []Expr
}

// SequenceExpr is a comma-separated sequence constructor.
type SequenceExpr struct{ Items []Expr }

// --- Type-related expressions ----------------------------------------------

// SequenceType is a type annotation: an item type plus an occurrence
// indicator.
type SequenceType struct {
	// Empty is the empty-sequence() type.
	Empty bool
	// ItemType is nil for item(), which matches anything.
	ItemType NodeTest
	// AtomicType names an atomic type when the item type is one.
	AtomicType    xdm.TypeCode
	HasAtomicType bool
	// IsFunctionTest marks a function(*) or function(...) as ... item type,
	// which matches a function item. A typed test additionally fixes the
	// arity and, where the function item records a signature of its own, the
	// parameter and return types it must be compatible with.
	IsFunctionTest   bool
	FunctionArity    int
	HasFunctionArity bool
	// FunctionParams and FunctionReturn are the typed test's declared
	// parameter and return types. They are matched only against a function
	// item that records its own signature; one that does not is judged on
	// arity alone. Every standard function now carries its manifest
	// signature, so that path is reached only by a function with no declared
	// type to read -- an untyped inline function, or a host extension.
	FunctionParams []SequenceType
	FunctionReturn *SequenceType
	// IsArrayTest marks an "array(*)" or "array(T)" item type, added in 3.1.
	//
	// An array is also a function item, so a function test can match one too;
	// the reverse does not hold, which is why this is a flag of its own rather
	// than a shape of IsFunctionTest. ArrayMember is the declared member type
	// of "array(T)", against which *every* member sequence is checked —
	// members are sequences, so "array(xs:string)" admits only arrays whose
	// members are each exactly one string.
	IsArrayTest bool
	ArrayMember *SequenceType
	// IsMapTest marks a "map(*)" or "map(K, V)" item type, added in 3.1, on
	// the same reasoning as IsArrayTest: a map is also a function item, so a
	// function test matches one, but a map test matches only a map.
	//
	// MapKey is the declared key type, an atomic type with no occurrence
	// indicator, and MapValue the declared value type, checked against every
	// entry's value sequence.
	IsMapTest bool
	MapKey    *SequenceType
	MapValue  *SequenceType
	// IsErrorType marks xs:error, the empty type. Nothing is an instance of
	// it, so it matches only the empty sequence — and only then because the
	// occurrence indicator permits it, never because an item conformed.
	IsErrorType bool
	// IsNumericType marks xs:numeric, the union of xs:double, xs:float and
	// xs:decimal that XPath 3.1 adds.
	//
	// It is a union rather than an atomic type, so it has no TypeCode of its
	// own: an item is an instance of it when its type is any of the three or
	// derived from one, which is what makes xs:short an xs:numeric. A cast to
	// it is the identity on a value that already is one and a cast to
	// xs:double on anything else, so the written name has to survive to the
	// cast rather than collapsing to a type code here.
	IsNumericType bool
	// ListItemFacet is the item type's facet name when the written type is
	// one of the built-in list types -- "NMTOKEN" for xs:NMTOKENS. A list
	// type's value is a sequence, so no TypeCode stands for it and the name
	// has to survive to the cast; see listtype.go.
	ListItemFacet string
	// FacetName is the derived type actually written, when it differs from
	// AtomicType — "byte" for xs:byte, which is an xs:integer with a range.
	// The code alone cannot express the bound, and dropping it made
	// "128 castable as xs:byte" answer true.
	FacetName string
	// SchemaType is the lexical name of a type that came from an imported
	// schema rather than from the built-in table.
	//
	// It is kept as written because that is what the schema's own type table
	// is keyed by: matching a value against it means asking the schema, not
	// the type codes here, and a derived type's identity is exactly its name.
	SchemaType string

	// SchemaValueValid checks a lexical value against the imported schema
	// type SchemaType names, when that name is a simple type. It is captured
	// at parse time, while the schema is still reachable through the
	// resolver, for the same reason a schema-element() test's substitution
	// group is: nothing carries a schema into the evaluator.
	//
	// nil when the type is not an imported simple type, in which case a cast
	// is decided entirely by the built-in the type derives from.
	SchemaValueValid func(value string) error

	// SchemaExpandQName resolves a lexical QName against the namespace
	// bindings in scope where the type name was written, when the type's
	// value space is the QName one.
	//
	// A cast to xs:QName or to a type derived from xs:NOTATION cannot be
	// completed without it: the namespace comes from the static context, and
	// by evaluation time that context is gone -- CastToDerived produces a
	// QName with no URI, which is why the parser folds the literal case. A
	// computed operand has no literal to fold, so the bindings are captured
	// here instead, alongside SchemaValueValid and for the same reason.
	//
	// nil unless the type is QName-valued.
	SchemaExpandQName func(lexical string) (xdm.QName, bool)

	// SchemaUnionMembers are the built-in atomic types a *pure union type*
	// from an imported schema admits, transitively.
	//
	// XPath 3.1 2.5.5 writes union membership as its own clause of
	// derives-from — "ET is a pure union type of which AT is a member type" —
	// so a value is an instance of the union whenever its actual type is one
	// of the members. No validation and no annotation are involved: the
	// xs:date that fn:current-date returns is an instance of a union over
	// xs:date, xs:time and xs:dateTime purely because xs:date is a member.
	//
	// Resolved at parse time, while the schema is still reachable, for the
	// same reason SchemaValueValid is. nil for anything that is not a pure
	// union, which is what keeps the impure cases 2.5 excludes — a union
	// carrying facets, or one with a list type anywhere in its transitive
	// membership — matching nothing rather than matching too much.
	SchemaUnionMembers []xdm.TypeCode

	// SchemaUnionMemberFacets are the member type NAMES of the same union, one
	// per entry of SchemaUnionMembers and in the same order, or nil when the
	// resolver cannot supply them.
	//
	// A CAST to a union produces an instance of the member that accepted the
	// value, and the erased code cannot say which derived type that was: a
	// union over xs:NCName and xs:QName reports xs:string for the first, so
	// the cast returned a bare string and "instance of xs:NCName" was false.
	// The name is what CastToDerived needs to apply the facet and annotate the
	// result. See SchemaUnionMemberFacets.
	SchemaUnionMemberFacets []string

	// SchemaListType marks a schema-defined simple type of variety list
	// written in type position.
	//
	// It is the schema-defined counterpart of ListItemFacet, which covers
	// only the three built-in list types the engine knows by name. Both say
	// the same thing -- the value is a sequence of whitespace-separated
	// tokens, not one atomic item -- and both exist so that a cast to such a
	// type is not rejected by the atomic-target rule. The difference is how
	// the tokens are checked: a built-in list applies a known item facet,
	// while a schema-defined one is validated in full by the schema through
	// SchemaValueValid, which already applies the item type AND the list's
	// own facets.
	SchemaListType bool

	// SchemaListItemType is the built-in atomic type each whitespace-
	// separated token of a SchemaListType value is cast to, or the zero code
	// when the item type is itself schema-defined and has no built-in code.
	// Castability does not depend on it -- the schema decides that through
	// SchemaValueValid -- but the cast's result sequence does.
	SchemaListItemType xdm.TypeCode

	// SchemaListItem is the item type of the same list, resolved as a cast
	// target in its own right, or nil when the resolver cannot supply it.
	//
	// SchemaListItemType is lossy in the way a cast cares about. XPath erases
	// every derived string type to xs:string, so a list of xs:IDREF built
	// three bare strings where F&O 3.0 18.3.6 owes three values "each of
	// which is an instance of the item type"; and a list whose item type is
	// a union has no code at all, so its tokens stayed the strings they were
	// handed in as. CastAs-UnionType-27 asks for xs:IDREF* and
	// CastAs-ListType-21 for an xs:NCName that is also an instance of the
	// union. Each token is cast to THIS type, through the same rules as a
	// cast written against it, so the facet is applied, the member chosen and
	// the annotation recorded. The code stays as the fallback for a resolver
	// that answers only the older question.
	SchemaListItem *SequenceType

	// SchemaSimpleType marks a named schema simple type that is a legal cast
	// target but has no shape any of the fields above can describe: an
	// *impure* union -- one carrying facets, or one holding a list type
	// anywhere in its transitive membership -- and a union derived by
	// restriction.
	//
	// The distinction from SchemaUnionMembers is which question is being
	// asked, not which types exist. XPath 3.1 2.5 admits only a *pure* union
	// as an ItemType, because "instance of" has to answer from a value's own
	// annotation and a faceted union's members do not necessarily satisfy its
	// facets -- the XSD 1.0 error 1.1 3.16.6.3 corrected. But 3.14.2 admits
	// any simple type in the in-scope schema types as a cast target, and a
	// cast has a lexical form in hand to validate, so the objection does not
	// arise: castability is the schema's own ValidateValue answer.
	// cbcl-castable-impure-001 asserts that
	// "xs:date('2001-01-01') castable as s:impureUnionType" is true, not an
	// error, and the purity rule still governs every ItemType position.
	//
	// SchemaValueValid is what decides a value; this field only says that the
	// name denotes such a type, so the atomic-target rule lets it through.
	SchemaSimpleType bool

	// SchemaSimpleListMembers are the LIST members of the impure union
	// SchemaSimpleType marks, in declaration order, each resolved as a cast
	// target of its own: SchemaListType set, SchemaListItem carrying the item
	// type, SchemaValueValid the member's own validity.
	//
	// A cast to a list type produces a SEQUENCE, one value per whitespace-
	// separated token (F&O 3.0 18.3.6). A union holding a list member is
	// impure, so such a cast arrives here rather than in the list-type branch
	// above, and without this the result was the single string handed in.
	// cbcl-castable-impure-010 is what that cost: the constructor
	// "s:impureUnionType('1 2 3')" owes three xs:decimal values, and a
	// three-item sequence is not castable to anything -- so the outer
	// "castable as s:impureUnionType" is false, where one string made it true.
	//
	// The members are whole types rather than one item code because the code
	// could not say which list admitted the value, nor what its items are
	// beyond a built-in primitive. A union over xs:IDREFS and a list of a
	// union (CastAs-UnionType-27 and -28) owes xs:IDREF values from the first
	// and union-member values from the second, and which one applies is
	// decided by trying them in order -- the rule for every union.
	//
	// Only a string-like source reaches a list member at all; an atomic
	// source is confined to SchemaSimpleAtomicMembers, which is the rule that
	// separates -005 from -009. nil when the union has no list member.
	SchemaSimpleListMembers []SequenceType

	// SchemaSimpleAtomicMembers are the built-in atomic types an impure union
	// admits directly, ignoring any list member.
	//
	// It is what separates cbcl-castable-impure-001 from -009. impureUnionType
	// is a union of xs:date and a list of xs:decimal, and the suite asks for
	// true from an xs:date and FALSE from an xs:decimal -- even though "1" is
	// a perfectly good one-item list of decimals. The difference is the SOURCE
	// type: F&O defines a cast to a list type from xs:string and
	// xs:untypedAtomic only, so a non-string source can reach a union's ATOMIC
	// members and nothing else. A string-like source may reach every member,
	// which is why "1 2 3" as an xs:untypedAtomic is castable (-005) and the
	// same value already typed as the union is not (-010).
	//
	// nil when the type has no atomic member, which refuses every non-string
	// source -- the right answer for a union over list types alone.
	SchemaSimpleAtomicMembers []xdm.TypeCode

	// SchemaSimpleAtomicFacets are those members' NAMES, one per entry and in
	// the same order, or nil when the resolver cannot supply them. It is
	// SchemaUnionMemberFacets for the impure case, and exists for the same
	// reason: the cast's result is an instance of the member that accepted the
	// value, and the erased code cannot say which derived type that was.
	SchemaSimpleAtomicFacets []string

	// Occurrence is "", "?", "*" or "+".
	Occurrence string
}

// InstanceOfExpr is "expr instance of type".
type InstanceOfExpr struct {
	Operand Expr
	Type    SequenceType
}

// CastExpr is "expr cast as type" and "expr castable as type".
type CastExpr struct {
	Operand  Expr
	Type     SequenceType
	Castable bool // true for "castable as", which yields a boolean
}

// TreatExpr is "expr treat as type": a static assertion that does not convert.
type TreatExpr struct {
	Operand Expr
	Type    SequenceType
}
