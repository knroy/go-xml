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

func (t SequenceType) matchesItem(it xdm.Item) bool {
	switch {
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
		// xs:NOTATION is abstract: no value can have it as its type, so
		// nothing is an instance of one. Asking is legal — it is only casting
		// that the spec forbids — and the answer is always false. Falling
		// through resolved it to xs:string and made every string match.
		return false
	case "":
		return atomicTypeMatches(a.Type, want)
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

	atoms := xdm.Atomize(v)
	switch len(atoms) {
	case 0:
		// Only "type?" permits an empty operand.
		if e.Type.Occurrence == "?" {
			if e.Castable {
				return xdm.One(xdm.NewBoolean(true)), nil
			}
			return xdm.Empty, nil
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
	srcIsQName := atoms[0].(*xdm.Atomic).Type == xdm.TypeQName
	if e.Type.AtomicType == xdm.TypeQName && !srcIsQName && !isLiteralOperand(e.Operand) {
		if e.Castable {
			return xdm.One(xdm.NewBoolean(false)), nil
		}
		return nil, xdm.ErrType(
			"cast: only a literal string is castable to xs:QName")
	}

	out, err := CastToDerived(atoms[0].(*xdm.Atomic), e.Type.AtomicType, e.Type.FacetName)
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
