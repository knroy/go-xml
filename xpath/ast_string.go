package xpath

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// Matches implements NodeTest for name tests.
//
// A name test only ever selects the principal node kind of its axis. That is
// why "@*" selects attributes while "*" selects elements even though both are
// the same test: the axis decides the kind, the test decides the name.
func (t *NameTest) Matches(n *xdm.Node, principal xdm.NodeKind) bool {
	if n.Kind != principal {
		return false
	}
	if !t.AnyURI && n.Name.URI != t.Name.URI {
		return false
	}
	if !t.AnyLocal && n.Name.Local != t.Name.Local {
		return false
	}
	return true
}

func (t *NameTest) String() string {
	switch {
	case t.AnyURI && t.AnyLocal:
		return "*"
	case t.AnyURI:
		return "*:" + t.Name.Local
	case t.AnyLocal:
		return t.Name.Prefix + ":*"
	}
	return t.Name.Lexical()
}

// Matches implements NodeTest for kind tests. Kind tests ignore the axis's
// principal kind: "text()" on the child axis selects text nodes even though
// the principal kind is element.
func (t *KindTest) Matches(n *xdm.Node, _ xdm.NodeKind) bool {
	if t.Any {
		return true
	}
	if n.Kind != t.Kind {
		return false
	}
	if t.Content != nil {
		// document-node(element(invoice)): the document matches only if it
		// has EXACTLY ONE element child and that child matches. A parsed
		// document has exactly one, but a temporary tree built by two
		// xsl:copy-of instructions has two, and taking the first one found
		// made document-node(schema-element(address)) answer true for a
		// document the type cannot describe at all.
		var only *xdm.Node
		for _, c := range n.Children {
			if c.Kind == xdm.KindElement {
				if only != nil {
					return false
				}
				only = c
			}
		}
		if only == nil {
			return false
		}
		return t.Content.Matches(only, only.Kind)
	}
	if t.HasName && t.Name != nil {
		if n.Kind == xdm.KindPI {
			// A PI test names the target, which has no namespace.
			return n.Name.Local == t.Name.Local
		}
		if n.Name.URI != t.Name.URI || n.Name.Local != t.Name.Local {
			// schema-element(E) matches E and anything substitutable for it,
			// so a name that is not E itself may still be a member of E's
			// substitution group. The members were resolved when the test was
			// parsed; an ordinary name test has none and falls straight out.
			if !t.substitutes(n.Name) {
				return false
			}
		}
	}
	if t.SchemaDeclared && n.TypeAnnotation == "" {
		// schema-element(E) and schema-attribute(A) match by *declaration*,
		// not by name: the node must have been validated against E, not
		// merely called E. An unvalidated node carries no annotation, so
		// it cannot have been. This is what makes
		// input-type-annotations="strip" observable — with the annotations
		// gone the test must stop matching — and it is the same rule
		// schemaDeclaredMatches applies to match patterns.
		return false
	}
	if t.SchemaDeclared && t.DeclaredType != "" && t.Name != nil &&
		n.Name.URI == t.Name.URI && n.Name.Local == t.Name.Local &&
		!nodeTypeMatches(n, t.DeclaredType) {
		// The node was validated, but against a declaration whose type is
		// not the global one's — a local element declaration of the same
		// name, which a schema may perfectly well have. Matching on the name
		// alone would make schema-element(E) select it, which is the
		// distinction nodetest-021 and nodetest-034 are drawn to catch.
		//
		// The check applies only to a node actually named E. A substitution
		// group member is declared with its own type, related to E's by a
		// rule the group already enforces, and comparing it against E's
		// annotation name here rejected the members schema-element(E) exists
		// to admit.
		return false
	}
	if t.TypeName != "" {
		return nodeTypeMatches(n, t.TypeName)
	}
	return true
}

// substitutes reports whether name is a member of the substitution group the
// test's schema-element() declaration heads.
func (t *KindTest) substitutes(name xdm.QName) bool {
	for _, m := range t.SubstitutionGroup {
		if m.URI == name.URI && m.Local == name.Local {
			return true
		}
	}
	return false
}

// nodeTypeMatches reports whether a node's type annotation satisfies the type
// named in an element() or attribute() kind test.
//
// The annotation is compared by name and by the derivation chain the schema
// recorded, so a node validated against a restriction of the named type
// matches it. An unvalidated node carries no annotation and is xs:untyped (an
// element) or xs:untypedAtomic (an attribute): those two names, and the roots
// of the hierarchy that every type derives from, are the only ones such a node
// satisfies. Answering true for anything else is what made "@bar instance of
// attribute(*, xs:NOTATION)" true for a DTD-declared attribute, which is the
// exact distinction the test exists to draw.
func nodeTypeMatches(n *xdm.Node, want string) bool {
	_, w := xdm.SplitQName(want)
	// xs:anyAtomicType is the root of the ATOMIC hierarchy, not of the type
	// hierarchy: an element's type is xs:untyped or a complex type, and
	// neither is an atomic type, so element(*, xs:anyAtomicType) is false for
	// every element. An attribute's is xs:untypedAtomic or a simple type, and
	// those are atomic. Treating anyAtomicType like anyType made
	// "$e instance of element(*, xs:anyAtomicType)" true, which is the exact
	// distinction type-0203 is written to draw.
	if w == "anyAtomicType" && n.Kind == xdm.KindElement {
		return false
	}
	if n.TypeAnnotation == "" {
		switch w {
		case "anyType", "anySimpleType", "anyAtomicType":
			return true
		case "untyped":
			return n.Kind == xdm.KindElement
		case "untypedAtomic":
			return n.Kind == xdm.KindAttribute
		}
		return false
	}
	switch w {
	case "anyType", "anySimpleType", "anyAtomicType":
		return true
	}
	if schemaTypeNameMatches(n.TypeAnnotation, want) {
		return true
	}
	// The built-in hierarchy is not in the schema's derivation table — nothing
	// registers that xs:ID restricts xs:NCName — so it is walked separately.
	// Subtype substitution applies to it just the same: an attribute
	// annotated xs:ID satisfies attribute(*, xs:string).
	_, a := xdm.SplitQName(n.TypeAnnotation)
	if derivedSubtypeOf(a, w) {
		return true
	}
	// A schema type ultimately grounded in a built-in reaches it through the
	// registered chain; from there the built-in table takes over. Walking both
	// in one pass is what lets a restriction of xs:token satisfy
	// attribute(*, xs:string).
	for i := 0; i < 32 && a != ""; i++ {
		a = xdm.DerivedBase(a)
		if a != "" && derivedSubtypeOf(a, w) {
			return true
		}
	}
	return false
}

func (t *KindTest) String() string {
	if t.Any {
		return "node()"
	}
	base := strings.TrimSuffix(t.Kind.String(), "()")
	if t.Content != nil {
		return base + "(" + t.Content.String() + ")"
	}
	if t.HasName && t.Name != nil {
		if t.TypeName != "" {
			return base + "(" + t.Name.Lexical() + ", " + t.TypeName + ")"
		}
		return base + "(" + t.Name.Lexical() + ")"
	}
	if t.TypeName != "" {
		return base + "(*, " + t.TypeName + ")"
	}
	return base + "()"
}

// --- Expr String methods ----------------------------------------------------

func (e *Literal) String() string {
	if e.Val.Type == xdm.TypeString {
		return "'" + e.Val.String() + "'"
	}
	return e.Val.String()
}

func (e *VarRef) String() string { return "$" + e.Name.Lexical() }

func (e *ContextItem) String() string { return "." }

func (e *BinaryOp) String() string {
	return "(" + e.Left.String() + " " + e.Op + " " + e.Right.String() + ")"
}

func (e *UnaryOp) String() string { return e.Op + e.Operand.String() }

func (e *Step) String() string {
	var sb strings.Builder
	// Print the abbreviated forms where they exist, so round-tripped
	// expressions look like what the author wrote.
	switch {
	case e.Axis == AxisChild:
	case e.Axis == AxisAttribute:
		sb.WriteString("@")
	default:
		sb.WriteString(axisStrings[e.Axis])
		sb.WriteString("::")
	}
	sb.WriteString(e.Test.String())
	for _, p := range e.Predicates {
		sb.WriteString("[" + p.String() + "]")
	}
	return sb.String()
}

func (e *PathExpr) String() string {
	var parts []string
	for _, s := range e.Steps {
		parts = append(parts, s.String())
	}
	body := strings.Join(parts, "/")
	if e.Root {
		return "/" + body
	}
	return body
}

func (e *FilterExpr) String() string {
	var sb strings.Builder
	sb.WriteString(e.Base.String())
	for _, p := range e.Predicates {
		sb.WriteString("[" + p.String() + "]")
	}
	return sb.String()
}

func (e *IfExpr) String() string {
	return fmt.Sprintf("if (%s) then %s else %s",
		e.Cond.String(), e.Then.String(), e.Else.String())
}

func (e *QuantifiedExpr) String() string {
	kw := "some"
	if e.Every {
		kw = "every"
	}
	return kw + " " + bindingsString(e.Bindings) + " satisfies " + e.Test.String()
}

func (e *ForExpr) String() string {
	return "for " + bindingsString(e.Bindings) + " return " + e.Return.String()
}

func bindingsString(bs []Binding) string {
	var parts []string
	for _, b := range bs {
		parts = append(parts, "$"+b.Var.Lexical()+" in "+b.Seq.String())
	}
	return strings.Join(parts, ", ")
}

func (e *FuncCall) String() string {
	var args []string
	for _, a := range e.Args {
		args = append(args, a.String())
	}
	name := e.Name.Local
	if e.Name.Prefix != "" {
		name = e.Name.Prefix + ":" + name
	}
	return name + "(" + strings.Join(args, ", ") + ")"
}

func (e *SequenceExpr) String() string {
	var parts []string
	for _, it := range e.Items {
		parts = append(parts, it.String())
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func (t SequenceType) String() string {
	if t.Empty {
		return "empty-sequence()"
	}
	base := "item()"
	switch {
	case t.HasAtomicType:
		base = t.AtomicType.String()
	case t.ItemType != nil:
		base = t.ItemType.String()
	}
	return base + t.Occurrence
}

func (e *InstanceOfExpr) String() string {
	return e.Operand.String() + " instance of " + e.Type.String()
}

func (e *CastExpr) String() string {
	kw := " cast as "
	if e.Castable {
		kw = " castable as "
	}
	return e.Operand.String() + kw + e.Type.String()
}

func (e *TreatExpr) String() string {
	return e.Operand.String() + " treat as " + e.Type.String()
}
