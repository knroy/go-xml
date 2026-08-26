package xpath

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// Matches reports whether seq conforms to the sequence type.
func (t SequenceType) Matches(seq xdm.Sequence) bool {
	if t.Empty {
		return len(seq) == 0
	}
	// Check cardinality before item types: "*" and "?" permit an empty
	// sequence, and an empty sequence trivially satisfies any item type.
	switch t.Occurrence {
	case "", "+":
		if len(seq) == 0 {
			return false
		}
	}
	switch t.Occurrence {
	case "", "?":
		if len(seq) > 1 {
			return false
		}
	}
	for _, it := range seq {
		if !t.matchesItem(it) {
			return false
		}
	}
	return true
}

// MatchesItem reports whether a single item conforms to the sequence type's
// item type, ignoring the occurrence indicator.
//
// The function conversion rules need this separately from Matches: subtype
// substitution says an item that already conforms is passed through
// untouched, and only an item that does not conform is a candidate for
// atomisation, casting or promotion.
func (t SequenceType) MatchesItem(it xdm.Item) bool { return t.matchesItem(it) }

func (t SequenceType) matchesItem(it xdm.Item) bool {
	// xs:error has no instances, so no item is one.
	if t.IsErrorType {
		return false
	}
	// A function test matches a function item and nothing else. A typed test
	// additionally fixes the arity; its parameter and return types are not
	// recorded, because a function item carries an arity and not a signature,
	// so a typed test is the any-function test plus that check.
	if t.IsFunctionTest {
		fn, ok := it.(*xdm.FunctionItem)
		if !ok {
			return false
		}
		return !t.HasFunctionArity || fn.Arity == t.FunctionArity
	}
	// Nothing else matches a function item: it is neither a node nor an
	// atomic value, so every other item type excludes it.
	if _, isFn := it.(*xdm.FunctionItem); isFn {
		return t.ItemType == nil && !t.HasAtomicType && t.SchemaType == ""
	}

	switch {
	case t.SchemaType != "":
		// A type from an imported schema. Like the built-in derived types,
		// it matches only a value annotated as that type or as one derived
		// from it — an untyped value that merely *would* be valid against
		// it is not an instance of it, which is the whole point of the
		// distinction between validating and asking.
		a, ok := it.(*xdm.Atomic)
		if !ok {
			// A node carries its type annotation rather than a code; an
			// unvalidated node is xs:untyped and is an instance of no
			// named type.
			return false
		}
		// A union-typed value is an instance of the union *and* of the
		// member that accepted it. The two are siblings in no hierarchy the
		// upward walk can cross, so the member is offered as a second
		// starting point rather than being reached from the first.
		if m := a.DerivedMember(); m != "" && schemaTypeNameMatches(m, t.SchemaType) {
			return true
		}
		return schemaTypeNameMatches(a.Derived(), t.SchemaType)

	case t.HasAtomicType:
		a, ok := it.(*xdm.Atomic)
		if !ok {
			return false
		}
		return atomicTypeMatchesFacet(a, t.AtomicType, t.FacetName)

	case t.ItemType != nil:
		n, ok := it.(*xdm.Node)
		if !ok {
			return false
		}
		// A kind test in a sequence type is checked against the node's own
		// kind, so the principal kind passed here is the node's own.
		return t.ItemType.Matches(n, n.Kind)
	}
	// item(): matches anything.
	return true
}

// atomicTypeMatches implements the subtype relation over atomic types.
//
// The hierarchy this engine models is shallow: xs:integer is a subtype of
// xs:decimal, and the duration subtypes derive from xs:duration. The string
// subtypes are collapsed onto xs:string, so "instance of xs:token" is
// equivalent to "instance of xs:string" here — a divergence that only shows up
// for schema-validated input, which this engine does not produce.
func atomicTypeMatches(actual, want xdm.TypeCode) bool {
	if actual == want {
		return true
	}
	switch want {
	case xdm.TypeDecimal:
		return actual == xdm.TypeInteger
	case xdm.TypeDuration:
		return actual == xdm.TypeYearMonthDuration || actual == xdm.TypeDayTimeDuration
	}
	return false
}

// atomicTypeMatchesFacet adds the derived types to the subtype relation.
//
// A derived type is *narrower* than the primitive it erases to, so the test
// runs the opposite way from a cast: "1 instance of xs:int" is false, because
// the literal is an xs:integer and xs:integer is the parent of xs:int, not a
// subtype of it. This engine never produces a value annotated as a derived
// type — nothing validates against a schema — so the answer for any of them is
// false, and the honest way to say that is to say it explicitly rather than to
// let the erased code make it accidentally true.
func atomicTypeMatchesFacet(a *xdm.Atomic, want xdm.TypeCode, facet string) bool {
	switch facet {
	case "anyAtomicType", "anySimpleType":
		// The root of the atomic hierarchy: every atomic value is one.
		return true
	case "NOTATION":
		// xs:NOTATION is abstract: no value can have it as its type directly,
		// so an unannotated value is not an instance of one. Asking is legal
		// — it is only casting that the spec forbids — and falling through
		// resolved it to xs:string and made every string match.
		//
		// A value validated against a schema type *derived* from xs:NOTATION
		// is a different case: it is an instance of its own type and of every
		// type that type derives from, xs:NOTATION included. That is the only
		// way anything can be an instance of it, since the type is abstract.
		return schemaTypeNameMatches(a.Derived(), "NOTATION")
	case "":
		return atomicTypeMatches(a.Type, want)
	}
	// A value annotated with a schema type derived from the named built-in
	// satisfies it: subtype substitution. "instance of xs:int" is true for a
	// value validated against a restriction of xs:int, which is the ordinary
	// case for anything read out of a schema-validated document.
	if a.Derived() != "" && a.Derived() != facet &&
		schemaTypeNameMatches(a.Derived(), facet) {
		return true
	}
	if hasRangeFacet(facet) || hasStringFacet(facet) {
		// A derived type matches only a value that was built as that type or
		// as one below it. A plain xs:integer literal is the *parent* of
		// xs:int, so it is not an instance of it.
		return derivedSubtypeOf(a.Derived(), facet)
	}
	return atomicTypeMatches(a.Type, want)
}

// Eval implements Expr for "instance of".
func (e *InstanceOfExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	v, err := e.Operand.Eval(ctx)
	if err != nil {
		return nil, err
	}
	return xdm.One(xdm.NewBoolean(e.Type.Matches(v))), nil
}

// Eval implements Expr for "cast as" and "castable as".
func (e *CastExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	v, err := e.Operand.Eval(ctx)
	if err != nil {
		if e.Castable {
			// An error evaluating the operand is still an error; only cast
			// failures are converted to false.
			return nil, err
		}
		return nil, err
	}

	// xs:error has no instances, so a cast to it always fails and a castable
	// test is always false — except for the empty sequence under "?", which
	// the general rules below already handle.
	if e.Type.IsErrorType && !(len(v) == 0 && e.Type.Occurrence == "?") {
		if e.Castable {
			return xdm.One(xdm.NewBoolean(false)), nil
		}
		return nil, xdm.Errorf("FORG0001",
			"a value cannot be cast to xs:error, which has no instances")
	}

	atoms := xdm.Atomize(v)
	switch len(atoms) {
	case 0:
		// Only "type?" permits an empty operand.
		if e.Type.Occurrence == "?" {
			if e.Castable {
				return xdm.One(xdm.NewBoolean(true)), nil
			}
			return xdm.Empty(), nil
		}
		if e.Castable {
			return xdm.One(xdm.NewBoolean(false)), nil
		}
		return nil, xdm.ErrType("cast: empty sequence is not castable to %s", e.Type)
	case 1:
	default:
		if e.Castable {
			return xdm.One(xdm.NewBoolean(false)), nil
		}
		return nil, xdm.ErrType("cast: operand has %d items, want 1", len(atoms))
	}

	if !e.Type.HasAtomicType {
		return nil, fmt.Errorf("XPST0080: cast target must be an atomic type, got %s", e.Type)
	}

	// Casting to xs:QName is defined only from a *literal* string: a QName's
	// namespace comes from the static context, and only a literal is folded
	// where the prefix bindings are in scope. A computed string has no such
	// context, so "$var castable as xs:QName" is false however well-formed
	// the value looks — CastableAs648 is `for $var in "ABC" return $var
	// castable as xs:QName`, which was answering true.
	//
	// This is a static property of the operand, which is why it is decided
	// here rather than in CastToDerived: that function sees a value and
	// cannot tell a literal from a variable that happens to hold one.
	// The restriction is on *strings*: a value that is already an xs:QName
	// casts to xs:QName whatever expression produced it, since it carries its
	// own namespace binding and needs nothing from the static context.
	// K-SeqExprCastable-18 is `QName("", "lname") castable as xs:QName`.
	//
	// XPath 3.0 lifts the restriction: section 19.1 makes the cast defined
	// from any xs:string or xs:untypedAtomic, resolving the prefix against
	// the statically known namespaces of the expression rather than of the
	// literal. So the rule below applies to a 2.0 expression only.
	srcIsQName := atoms[0].(*xdm.Atomic).Type == xdm.TypeQName
	if !ctx.Version.atLeast30() &&
		e.Type.AtomicType == xdm.TypeQName && !srcIsQName && !isLiteralOperand(e.Operand) {
		if e.Castable {
			return xdm.One(xdm.NewBoolean(false)), nil
		}
		return nil, xdm.ErrType(
			"cast: only a literal string is castable to xs:QName")
	}

	src := atoms[0].(*xdm.Atomic)
	// XPath 3.0 admits xs:untypedAtomic as a source for a cast to xs:QName,
	// where 2.0 forbade it outright. The value is a lexical form either way,
	// so it is handed on as an xs:string: that keeps the one place that knows
	// the version here, rather than threading it through the cast tables. A
	// malformed name then fails as a bad lexical form (FORG0001) instead of
	// as a conversion that does not exist (XPTY0004).
	if ctx.Version.atLeast30() && e.Type.AtomicType == xdm.TypeQName &&
		src.Type == xdm.TypeUntypedAtomic {
		src = xdm.NewString(src.String())
	}
	out, err := CastToDerived(src, e.Type.AtomicType, e.Type.FacetName)
	if err == nil && e.Type.SchemaValueValid != nil {
		// The built-in cast succeeded, which settles the lexical form. The
		// schema type's own facets — a range, a pattern, an enumeration —
		// are a further constraint, and without applying them a cast to a
		// restriction of xs:integer accepted every integer.
		//
		// The *source* lexical form is checked rather than the cast result,
		// because a facet such as a pattern constrains the lexical space and
		// the cast may have canonicalised it away.
		if verr := e.Type.SchemaValueValid(src.String()); verr != nil {
			err = verr
			out = nil
		}
	}
	if err == nil && out != nil && e.Type.SchemaType != "" {
		// A constructor call on an imported schema type folds into this cast,
		// and the value it produces is an instance of that type — that is what
		// the constructor is for. Without recording the annotation the cast
		// produced a bare primitive, so "foo:testType(2000)" did not match a
		// declared type of "foo:testType" and every such variable raised
		// XTTE0570.
		out = out.WithDerived(e.Type.SchemaType)
	}
	if e.Castable {
		// "castable as" is precisely "would cast succeed", so the error is
		// consumed rather than propagated.
		return xdm.One(xdm.NewBoolean(err == nil)), nil
	}
	if err != nil {
		return nil, err
	}
	return xdm.One(out), nil
}

// Eval implements Expr for "treat as".
//
// treat does not convert: it asserts. That distinction matters because "treat
// as" is how a stylesheet author narrows a static type without paying for a
// conversion, and silently converting would hide the error it exists to raise.
func (e *TreatExpr) Eval(ctx *Context) (xdm.Sequence, error) {
	v, err := e.Operand.Eval(ctx)
	if err != nil {
		return nil, err
	}
	if !e.Type.Matches(v) {
		return nil, fmt.Errorf("XPDY0050: value does not match the treat type %s", e.Type)
	}
	return v, nil
}

// isLiteralOperand reports whether e is a string literal, for the xs:QName
// cast rule.
//
// A parenthesised literal is still a literal — "('ABC') cast as xs:QName" is
// the same expression — so the wrapper is unwrapped rather than treated as a
// computed value.
func isLiteralOperand(e Expr) bool {
	for {
		switch t := e.(type) {
		case *Literal:
			return true
		case *SequenceExpr:
			// Parentheses fold into a one-item SequenceExpr, and
			// "('ABC') cast as xs:QName" is the same expression as the bare
			// literal. More than one item is not a single literal.
			if len(t.Items) != 1 {
				return false
			}
			e = t.Items[0]
		default:
			return false
		}
	}
}

// schemaTypeNameMatches compares a value's type annotation with a schema type
// named in a sequence type.
//
// Both sides are annotation keys — namespace-qualified {uri}local for a schema
// type, the bare local name for a built-in — so the comparison is on EXPANDED
// names, which is what the specification asks for. It used to be on local
// parts with the prefix ignored, and the comment here used to record the
// consequence as an accepted limit: "two schemas that both define
// partNumberType in different namespaces would be conflated by this". They
// were, and not only in the hypothetical. The W3C's own schema-for-xslt20.xsd
// declares an xsl:QName that restricts xs:Name, so once it loaded, every
// xs:QName in the process compared equal to it.
//
// The derivation walk below is unchanged: a value is an instance of every type
// its own derives from, and the chain the schema recorded is now keyed by the
// same qualified names, so the walk stays exact end to end.
func schemaTypeNameMatches(annotation, want string) bool {
	if annotation == "" {
		return false
	}
	if annotation == want {
		return true
	}
	// SplitAnnotationName rather than SplitQName: an annotation key may be in
	// Clark notation, and SplitQName cuts at the first colon, so it would
	// read "{http://x}foo" as the prefix "{http" and the local part
	// "//x}foo" — silently wrong rather than an error.
	//
	// The local parts are compared only when BOTH sides are unqualified,
	// which is the built-in and no-namespace case where a bare key is already
	// unambiguous. A qualified key never matches on its local part alone;
	// that fallback is exactly what conflated namespaces.
	a := annotation
	w := want
	if !xdm.IsQualifiedAnnotation(a) && !xdm.IsQualifiedAnnotation(w) {
		_, a = xdm.SplitQName(a)
		_, w = xdm.SplitQName(w)
		if a != "" && a == w {
			return true
		}
	}
	// A value is an instance of every type its own derives from, so the
	// chain the schema recorded is walked upwards. Without this a value
	// annotated as a restriction of xs:NOTATION answered true for its own
	// type and false for xs:NOTATION, which is half the relation.
	//
	// The walk is bounded: a schema whose derivations somehow formed a cycle
	// would otherwise not terminate, and this runs on every comparison.
	for i := 0; i < 32 && a != ""; i++ {
		a = xdm.DerivedBase(a)
		if a != "" && a == w {
			return true
		}
	}
	return false
}
