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

// Binding is one "$var in expr" clause of a for or quantified expression.
type Binding struct {
	Var xdm.QName
	Seq Expr
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
