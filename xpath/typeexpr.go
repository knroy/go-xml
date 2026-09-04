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
	// xs:numeric matches any value whose type is or derives from one of the
	// three numeric primitives, which is why this asks IsNumeric rather than
	// comparing against a fixed list: xs:short is an xs:integer is an
	// xs:decimal, and the suite asserts it is an xs:numeric.
	if t.IsNumericType {
		a, ok := it.(*xdm.Atomic)
		return ok && a.Type.IsNumeric()
	}
	// A list type has no item type of its own in this engine: its tokens
	// become the atomic type the list is of, so an item is an instance of it
	// when it is a string whose value is a legal token.
	if t.ListItemFacet != "" {
		a, ok := it.(*xdm.Atomic)
		if !ok || a.Type != xdm.TypeString {
			return false
		}
		_, err := applyStringFacet(a, t.ListItemFacet)
		return err == nil
	}
	// A map test matches a map and nothing else, even though a map is also a
	// function item: the relation runs one way only.
	if t.IsMapTest {
		m, ok := it.(*xdm.MapItem)
		return ok && mapItemMatches(t, m)
	}
	// An array test matches an array and nothing else, even though an array is
	// also a function item — the same one-way relation a map test has.
	if t.IsArrayTest {
		arr, ok := it.(*xdm.ArrayItem)
		if !ok {
			return false
		}
		if t.ArrayMember == nil {
			return true // array(*)
		}
		// Every member is a sequence, and each must satisfy the declared
		// member type on its own. That is why "[(), 'A'] instance of
		// array(xs:string)" is false while "array(xs:string?)" is true: the
		// empty member fails the required cardinality, not the item type.
		for _, m := range arr.Members() {
			if !t.ArrayMember.Matches(m) {
				return false
			}
		}
		return true
	}
	// A function test matches any function item, and a map or an array along
	// with it — the data model says those *are* functions of arity one, so a
	// map is an instance of "function(xs:anyAtomicType) as item()*". Their
	// signatures depend on what they hold, so each is judged by its own rule
	// rather than through the recorded-signature path a plain function uses.
	if t.IsFunctionTest {
		switch v := it.(type) {
		case *xdm.FunctionItem:
			return functionItemMatches(t, v)
		case *xdm.MapItem:
			return mapMatchesFunctionTest(t, v)
		case *xdm.ArrayItem:
			return arrayMatchesFunctionTest(t, v)
		}
		return false
	}
	// Nothing else matches a function item, or a map: neither is a node or an
	// atomic value, so every other item type excludes them. Only item() is
	// left, which is what the "no test at all" condition below says.
	switch it.(type) {
	case *xdm.FunctionItem, *xdm.MapItem, *xdm.ArrayItem:
		return t.ItemType == nil && !t.HasAtomicType && t.SchemaType == "" &&
			!t.IsArrayTest
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
		if schemaTypeNameMatches(a.Derived(), t.SchemaType) {
			return true
		}
		// A pure union type matches by MEMBERSHIP rather than by annotation.
		// XPath 3.1 2.5.5 gives derives-from a clause of its own for it —
		// "ET is a pure union type of which AT is a member type" — which is
		// a question about the value's actual type, not about what validated
		// it. So the plain xs:date fn:current-date returns is an instance of
		// a union over xs:date, xs:time and xs:dateTime, having never been
		// near the schema that declares the union.
		for _, m := range t.SchemaUnionMembers {
			if a.Type == m {
				return true
			}
		}
		return false

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

// derivedSubtypeOfThroughSchema is derivedSubtypeOf continued across the join
// between a schema's derivation chain and the built-in hierarchy.
//
// The built-in table knows that xs:int restricts xs:long restricts xs:integer;
// the schema's registry knows that a type it declares restricts xs:int. Only
// one of the two knows any given step, so a chain that crosses from one to the
// other is invisible to either alone — which is why "data() instance of
// xs:long" answered false in type-functions-0202 for an element whose type is
// a restriction of xs:int, while "instance of xs:int" answered true. The same
// break made a restriction of xs:negativeInteger miss xs:nonPositiveInteger.
//
// nodeTypeMatches already walks the two together for a NODE's annotation; this
// is the same walk for an atomic VALUE's, and the reason is identical.
//
// Only an unqualified step is offered to the built-in table: a qualified name
// is a schema type and is never reachable through the built-in hierarchy, and
// letting one in would compare local parts across namespaces.
func derivedSubtypeOfThroughSchema(derived, facet string) bool {
	if derivedSubtypeOf(derived, facet) {
		return true
	}
	// A visited set rather than a step count: a schema whose derivations
	// somehow formed a cycle must not loop here, and a cycle is exactly what
	// a repeated name identifies. The count this replaced answered a definite
	// "not a subtype" on running out of steps, so a legal acyclic chain of 33
	// user-defined types decided the subtype relation wrongly.
	seen := map[string]bool{derived: true}
	for derived != "" {
		derived = xdm.DerivedBase(derived)
		if derived == "" || seen[derived] {
			return false
		}
		seen[derived] = true
		if !xdm.IsQualifiedAnnotation(derived) && derivedSubtypeOf(derived, facet) {
			return true
		}
	}
	return false
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
	case "numeric":
		// The union xs:double | xs:float | xs:decimal, and so xs:integer too
		// by derivation from xs:decimal. It is a union rather than a type, so
		// the code it parsed to says nothing; the membership test is the
		// question of whether the value is numeric at all.
		return a.Type.IsNumeric()
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
		return derivedSubtypeOfThroughSchema(a.Derived(), facet)
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
	// test is always false — except where the operand is empty, which the
	// general rules below settle first. "() cast as xs:error?" is empty, and
	// "xs:error(()) cast as xs:error" is XPTY0004: a cardinality mismatch is
	// decided before the target type gets a say, so answering FORG0001 there
	// (xs-error-040) reported the wrong reason for the wrong stage.
	if e.Type.IsErrorType && len(v) != 0 {
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

	// Casting to xs:numeric is the identity on a value that already is one and
	// a cast to xs:double on anything else. That is why the target cannot be
	// reduced to a type code at parse time: "xs:short(256) cast as xs:numeric"
	// keeps its xs:short type, which no single code would preserve, while
	// "true() cast as xs:numeric" produces an xs:double.
	if e.Type.IsNumericType {
		a := atoms[0].(*xdm.Atomic)
		if a.Type.IsNumeric() {
			if e.Castable {
				return xdm.One(xdm.NewBoolean(true)), nil
			}
			return xdm.One(a), nil
		}
		out, err := CastAtomic(a, xdm.TypeDouble)
		if e.Castable {
			return xdm.One(xdm.NewBoolean(err == nil)), nil
		}
		if err != nil {
			return nil, err
		}
		return xdm.One(out), nil
	}

	// A cast to a built-in list type produces one item per whitespace-
	// separated token, so it cannot go through CastAtomic, which maps one
	// item to one. See listtype.go.
	if e.Type.ListItemFacet != "" {
		out, err := castToListType(atoms[0].(*xdm.Atomic), e.Type.ListItemFacet)
		if e.Castable {
			return xdm.One(xdm.NewBoolean(err == nil)), nil
		}
		if err != nil {
			return nil, err
		}
		return out, nil
	}

	// A cast to a named pure union type tries the member types in order and
	// takes the first that accepts the value, so the result is an instance of
	// that member rather than of the union. See castToUnion.
	if len(e.Type.SchemaUnionMembers) > 0 {
		out, err := castToUnion(atoms[0].(*xdm.Atomic), e.Type)
		if e.Castable {
			return xdm.One(xdm.NewBoolean(err == nil)), nil
		}
		if err != nil {
			return nil, err
		}
		return xdm.One(out), nil
	}

	// A cast to a schema-defined list type is decided entirely by the schema:
	// SchemaValueValid applies the item type to each whitespace-separated
	// token and the list's own facets to the whole, which is exactly the
	// validity question a cast asks. The cast itself is not implemented --
	// only castability is -- because the resulting sequence has no place in
	// the atomic-valued shape this expression returns.
	if e.Type.SchemaListType {
		var verr error
		if e.Type.SchemaValueValid != nil {
			verr = e.Type.SchemaValueValid(atoms[0].(*xdm.Atomic).String())
		}
		if e.Castable {
			return xdm.One(xdm.NewBoolean(verr == nil)), nil
		}
		if verr != nil {
			return nil, verr
		}
		// The value is valid, so the result is one item per whitespace-
		// separated token, each cast to the item type -- the same shape
		// castToListType produces for a built-in list type.
		toks := collapseXMLSpaceFields(atoms[0].(*xdm.Atomic).String())
		out := make(xdm.Sequence, 0, len(toks))
		for _, tok := range toks {
			if e.Type.SchemaListItemType == 0 {
				// An item type with no built-in code: the tokens keep their
				// lexical form, which is all that can be said about them
				// without the schema's own value constructor.
				out = append(out, xdm.NewString(tok))
				continue
			}
			v, err := CastAtomic(xdm.NewString(tok), e.Type.SchemaListItemType)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
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
		//
		// A QName-valued type is the exception, and has to be: xs:QName and
		// xs:NOTATION have QNames rather than strings for their value space,
		// so "n:wav" and "test:wav" are the SAME value whenever both prefixes
		// bind the same namespace. Comparing lexical forms matched prefixes
		// instead of namespaces, so notation-0002's "$q castable as n:nota"
		// was false against an enumeration the schema wrote with test: --
		// the very confusion ValidateExpandedQNameValue exists to avoid. The
		// cast has already resolved the prefix, so its result is handed over
		// in the Clark spelling no lexical QName can have. This mirrors what
		// the constructor path does in qnamedyn.go.
		lex := src.String()
		if e.Type.AtomicType == xdm.TypeQName {
			q := out.QName()
			// A cast from a STRING produces a QName with no URI, because
			// CastToDerived has no static context to resolve the prefix
			// against. The bindings were captured where the type name was
			// written, so the expansion happens here instead.
			if e.Type.SchemaExpandQName != nil && src.Type != xdm.TypeQName {
				if expanded, ok := e.Type.SchemaExpandQName(src.String()); ok {
					q = &expanded
					out = xdm.NewQNameValue(expanded)
				}
			}
			if q != nil {
				lex = "{" + q.URI + "}" + q.Local
			}
		}
		if verr := e.Type.SchemaValueValid(lex); verr != nil {
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
	// A visited set rather than a step count: a schema whose derivations
	// somehow formed a cycle would otherwise not terminate, and a repeated
	// name identifies that cycle exactly. The count this replaced stopped
	// after 32 links and returned false, which is a definite negative on a
	// legal chain rather than a refusal.
	seen := map[string]bool{a: true}
	for a != "" {
		a = xdm.DerivedBase(a)
		if a == "" || seen[a] {
			return false
		}
		seen[a] = true
		if a == w {
			return true
		}
	}
	return false
}

// castToUnion casts a value to a named pure union type.
//
// XPath 3.1 3.14.2 admits any simple type in the in-scope schema types as a
// cast target, and F&O gives the union case its own rule: the member types are
// tried in the order they are declared, and the first one that accepts the
// value produces the result. So the result is an instance of a *member*, never
// of the union itself — "'2008-11-14' cast as dateUnion" over a union of
// xs:date, xs:time and xs:dateTime is an xs:date, which is what
// import-schema-192 asserts.
//
// A value already of a member type is returned untouched. That is not merely
// an optimisation: casting it onwards would canonicalise it away, and XPath
// 3.1 2.5.5 makes such a value an instance of the union already, so a cast has
// nothing left to do.
//
// The union's own facets are deliberately not consulted here, because there
// are none to consult: SchemaUnionMembers is nil unless the union is *pure* in
// XPath 3.1 2.5's sense, which requires an empty {facets} property. An impure
// union never reaches this function — it is refused as a cast target instead.
// That is the strict direction XSD 1.1 3.16.6.3 requires: a member does not
// necessarily satisfy the facets a union adds, and casting to such a union as
// though it did would reintroduce the XSD 1.0 error 1.1 corrected.
func castToUnion(a *xdm.Atomic, st SequenceType) (*xdm.Atomic, error) {
	// An item that is already an instance of one of the members needs no
	// conversion. xs:untypedAtomic is excluded: it is the type a cast is
	// there to resolve, and it is never itself a member.
	if a.Type != xdm.TypeUntypedAtomic {
		for _, m := range st.SchemaUnionMembers {
			if a.Type == m {
				return a, nil
			}
		}
	}
	// The lexical form is what the member types are tried against, because a
	// union is defined over the lexical space: "12:00:00" is an xs:time and
	// not an xs:date, and only the written form says so.
	lex := a.String()
	for _, m := range st.SchemaUnionMembers {
		out, err := CastAtomic(a, m)
		if err != nil {
			continue
		}
		// The built-in cast settles the lexical form of the member. The
		// union's *declared* validity is a further question when the member
		// is a restriction carrying facets the type code cannot express, so
		// the schema is asked as well when it is reachable.
		if st.SchemaValueValid != nil {
			if st.SchemaValueValid(lex) != nil {
				continue
			}
		}
		return out, nil
	}
	return nil, xdm.Errorf("FORG0001",
		"%q is not castable to %s: no member type accepts it", lex, st)
}
