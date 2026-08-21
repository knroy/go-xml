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
		// document-node(element(invoice)): the document matches only if its
		// element child does. A document has exactly one, so the first one
		// found settles it.
		for _, c := range n.Children {
			if c.Kind == xdm.KindElement {
				return t.Content.Matches(c, c.Kind)
			}
		}
		return false
	}
	if t.HasName && t.Name != nil {
		if n.Kind == xdm.KindPI {
			// A PI test names the target, which has no namespace.
			return n.Name.Local == t.Name.Local
		}
		return n.Name.URI == t.Name.URI && n.Name.Local == t.Name.Local
	}
	return true
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
		return base + "(" + t.Name.Lexical() + ")"
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
