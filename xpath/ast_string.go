package xpath

import (
	"fmt"
	"strconv"
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
		!declaredTypeMatches(n, t.DeclaredType) {
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
		if !nodeTypeMatches(n, t.TypeName) {
			return false
		}
		// The "?" of element(name, type?) is what admits a NILLED element.
		// XDM's element test rule has two parts: the annotation must match,
		// and — when the test carries no "?" — the node's dm:nilled property
		// must be false. Without the second part element(t:test, t:testType)
		// and element(t:test, t:testType?) match exactly the same nodes, so
		// the higher-priority "not nilled" template in validation-2001 won
		// for the nilled document too.
		//
		// Only an element has the property; an attribute test carrying "?"
		// is not something the grammar produces, and asking the question of
		// a non-element would answer false and reject a node the annotation
		// already matched.
		if !t.TypeNillable && n.Kind == xdm.KindElement && nodeIsNilled(n) {
			return false
		}
		return true
	}
	return true
}

// nodeIsNilled reports the data model's dm:nilled property for an element.
//
// It is a PSVI property, so it takes both an xsi:nil marking AND evidence
// that the element was validated: xsi:nil on an element whose declaration is
// not nillable is an error rather than a nilled element, and only validation
// distinguishes them. A non-empty type annotation is the evidence available
// in the data model — an untyped tree leaves it empty on every node.
//
// fn:nilled and the element() kind test must agree on this, which is why the
// rule lives in one place rather than being written out at each.
func nodeIsNilled(n *xdm.Node) bool {
	if n == nil || n.Kind != xdm.KindElement || n.TypeAnnotation == "" {
		return false
	}
	a := n.Attr(xdm.NSXSI, "nil")
	return a != nil && (a.Value == "true" || a.Value == "1")
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
	// SplitAnnotationName, not SplitQName: want is an annotation key, and a
	// qualified one is in Clark notation, which a prefix-splitter mis-reads
	// into nonsense rather than rejecting.
	w := xdm.AnnotationLocal(want)
	qualified := xdm.IsQualifiedAnnotation(want)
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
	// The roots of the hierarchy are built-ins, so they only answer for an
	// UNQUALIFIED want. A schema type that happens to be called "anyType" in
	// its own namespace is an ordinary named type, not the root.
	if n.TypeAnnotation == "" {
		if qualified {
			return false
		}
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
	if !qualified {
		switch w {
		case "anyType", "anySimpleType", "anyAtomicType":
			return true
		}
	}
	if schemaTypeNameMatches(n.TypeAnnotation, want) {
		return true
	}
	// The built-in hierarchy is not in the schema's derivation table — nothing
	// registers that xs:ID restricts xs:NCName — so it is walked separately.
	// Subtype substitution applies to it just the same: an attribute
	// annotated xs:ID satisfies attribute(*, xs:string).
	// The built-in table is keyed by bare local names, so it is consulted
	// with the annotation's local part — but only when the want side names a
	// built-in too. A qualified want is a schema type and is never reachable
	// through the built-in hierarchy; letting it in here would restore the
	// local-name conflation this change removes.
	a := n.TypeAnnotation
	if !xdm.IsQualifiedAnnotation(a) {
		_, a = xdm.SplitQName(a)
	}
	if !qualified && !xdm.IsQualifiedAnnotation(a) && derivedSubtypeOf(a, w) {
		return true
	}
	// A schema type ultimately grounded in a built-in reaches it through the
	// registered chain; from there the built-in table takes over. Walking both
	// in one pass is what lets a restriction of xs:token satisfy
	// attribute(*, xs:string).
	// The walk goes up the chain the schema recorded. Each step is compared
	// as a whole key first — that is how a QUALIFIED want is satisfied by a
	// value annotated with a type derived from it — and only an unqualified
	// step is offered to the built-in table.
	for i := 0; i < 32 && a != ""; i++ {
		a = xdm.DerivedBase(a)
		if a == "" {
			break
		}
		if a == want {
			return true
		}
		if !qualified && !xdm.IsQualifiedAnnotation(a) && derivedSubtypeOf(a, w) {
			return true
		}
	}
	return false
}

// declaredTypeMatches is nodeTypeMatches for a schema-element() or
// schema-attribute() declaration's type, which arrives as a bare LOCAL name
// rather than as an annotation key.
//
// The distinction from an element() test's type argument is real. That
// argument is written by the query author, is resolved against the static
// context at parse time, and is compared as an expanded name — two types
// sharing a local part in different namespaces must not match each other.
// This one is reported by a SchemaTypes implementation, which the interface
// only obliges to produce a local name, so the comparison drops to local
// parts. It loses nothing that matters here: the check exists to distinguish a
// node validated against the GLOBAL declaration of E from one validated
// against a LOCAL declaration of E in the same schema, and both types live in
// the same target namespace, so the namespace was never the discriminator.
func declaredTypeMatches(n *xdm.Node, want string) bool {
	if nodeTypeMatches(n, want) {
		return true
	}
	// A qualified annotation is offered again by its local part, which is the
	// form the declaration reported. Only the annotation is re-spelled; want
	// stays as given, so a bare want never gains reach it did not have.
	if !xdm.IsQualifiedAnnotation(n.TypeAnnotation) {
		return false
	}
	local := xdm.AnnotationLocal(n.TypeAnnotation)
	if local == want {
		return true
	}
	// The derivation chain is walked for the same reason nodeTypeMatches
	// walks it, and bounded for the same reason: a schema whose derivations
	// formed a cycle must not spin here.
	a := n.TypeAnnotation
	for i := 0; i < 32 && a != ""; i++ {
		a = xdm.DerivedBase(a)
		if a != "" && xdm.AnnotationLocal(a) == want {
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
			return base + "(" + t.Name.Lexical() + ", " + t.TypeNameLexical + ")"
		}
		return base + "(" + t.Name.Lexical() + ")"
	}
	if t.TypeName != "" {
		return base + "(*, " + t.TypeNameLexical + ")"
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

func (e *NamedFunctionRef) String() string {
	return e.Name.Lexical() + "#" + strconv.Itoa(e.Arity)
}

func (e *InlineFunctionExpr) String() string {
	var ps []string
	for _, p := range e.Params {
		s := "$" + p.Name.Lexical()
		if p.Type != nil {
			s += " as " + p.Type.String()
		}
		ps = append(ps, s)
	}
	out := "function(" + strings.Join(ps, ", ") + ")"
	if e.Result != nil {
		out += " as " + e.Result.String()
	}
	return out + " { " + e.Body.String() + " }"
}

func (e *ArgumentPlaceholder) String() string { return "?" }

func (e *DynamicCall) String() string {
	var as []string
	for _, a := range e.Args {
		as = append(as, a.String())
	}
	return e.Target.String() + "(" + strings.Join(as, ", ") + ")"
}

func (e *LetExpr) String() string {
	var parts []string
	for _, b := range e.Bindings {
		parts = append(parts, "$"+b.Var.Lexical()+" := "+b.Seq.String())
	}
	return "let " + strings.Join(parts, ", ") + " return " + e.Return.String()
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
	case t.IsMapTest:
		// Rendered because convertForParam names the declared type in its
		// XPTY0004 message, and "item()" there says nothing about why a map
		// of the wrong shape was refused.
		if t.MapKey == nil || t.MapValue == nil {
			base = "map(*)"
		} else {
			base = "map(" + t.MapKey.String() + ", " + t.MapValue.String() + ")"
		}
	case t.ListItemFacet != "":
		// Rendered as the list type itself, not as its item type: an error
		// naming xs:NMTOKEN where the stylesheet wrote xs:NMTOKENS points at
		// the wrong type. xs:ENTITIES is not its item type plus an "S", so
		// the name is looked up rather than derived.
		base = "xs:" + listTypeOfItemFacet(t.ListItemFacet)
	case t.SchemaType != "":
		base = t.SchemaType
	case t.FacetName != "":
		// A derived type is rendered as written. The type code alone is the
		// primitive it derives from, so xs:NCName would otherwise print as
		// xs:string — which a subtype comparison would then read as the wider
		// type and wrongly accept.
		base = "xs:" + t.FacetName
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
