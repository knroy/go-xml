package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// evalList runs an expression at XPath 3.1 and returns the result sequence.
func evalList(t *testing.T, expr string) (xdm.Sequence, error) {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	return Eval(expr, ctx, nil)
}

// TestBuiltinListTypeItemAnnotation asserts the return types F&O 3.0 17.3
// declares for the three built-in list-type constructors:
//
//	xs:NMTOKENS($arg as xs:anyAtomicType?) as xs:NMTOKEN*
//	xs:ENTITIES($arg as xs:anyAtomicType?) as xs:ENTITY*
//	xs:IDREFS($arg   as xs:anyAtomicType?) as xs:IDREF*
//
// and the same for the cast form, which 18.3.6 defines as producing "a
// sequence of zero or more atomic values each of which is an instance of the
// item type of L".
//
// The item type is asserted through "instance of", not by reading back an
// annotation field: an item type is exactly what the sequence type matches,
// and that is the fact the specification states.
func TestBuiltinListTypeItemAnnotation(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		// The constructor form.
		{`xs:IDREFS("a b c") instance of xs:IDREF*`, "true"},
		{`xs:NMTOKENS("a b c") instance of xs:NMTOKEN*`, "true"},
		{`xs:ENTITIES("a b c") instance of xs:ENTITY*`, "true"},
		// The cast form.
		{`("a b c" cast as xs:IDREFS) instance of xs:IDREF*`, "true"},
		{`("a b c" cast as xs:NMTOKENS) instance of xs:NMTOKEN*`, "true"},
		{`("a b c" cast as xs:ENTITIES) instance of xs:ENTITY*`, "true"},
		// One item per whitespace-separated token, and the tokens
		// themselves survive: 18.3.6's own example is
		// cast "A B C D" as xs:NMTOKENS giving four values.
		{`count(xs:NMTOKENS("A B C D"))`, "4"},
		{`string-join("a b c" cast as xs:IDREFS, "|")`, "a|b|c"},
		// An xs:IDREF is still an xs:string, since it is derived from one.
		{`xs:IDREFS("a b c") instance of xs:string*`, "true"},
	} {
		got, err := evalList(t, tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if s := got[0].(*xdm.Atomic).String(); s != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.expr, s, tc.want)
		}
	}
}

// TestBuiltinListTypeConstructorIsTheCast asserts the three ways the
// constructor functions had drifted from the cast F&O 3.0 17.3 defines them to
// be ("the semantics are equivalent to casting to the corresponding types from
// xs:string").
//
// Each row states a rule of XML Schema or of 18.3.6 rather than an observation:
// the minLength=1 facet all three types carry, the item type's own facet, and
// the restriction of the source to xs:string or xs:untypedAtomic.
func TestBuiltinListTypeConstructorIsTheCast(t *testing.T) {
	for _, tc := range []struct{ expr, code string }{
		// minLength = 1: a lexical form with no tokens is not a value of
		// any of the three types.
		{`xs:IDREFS("")`, "FORG0001"},
		{`xs:NMTOKENS("")`, "FORG0001"},
		{`xs:ENTITIES(" ")`, "FORG0001"},
		{`"" cast as xs:IDREFS`, "FORG0001"},
		// The item type's facet applies to every token. xs:ENTITY is an
		// NCName, and "12" is not one.
		{`xs:ENTITIES(" a b c 12 ")`, "FORG0001"},
		{`xs:IDREFS("a 12")`, "FORG0001"},
		// 18.3.6: "the supplied value must be of type xs:string or
		// xs:untypedAtomic". An xs:anyURI is neither, so this is a type
		// error rather than a one-token list.
		{`xs:ENTITIES(xs:anyURI("abcd"))`, "XPTY0004"},
		{`xs:IDREFS(xs:anyURI("abcd"))`, "XPTY0004"},
		{`xs:anyURI("abcd") cast as xs:IDREFS`, "XPTY0004"},
	} {
		_, err := evalList(t, tc.expr)
		if err == nil {
			t.Errorf("%s: want error %s, got success", tc.expr, tc.code)
			continue
		}
		if !strings.Contains(err.Error(), tc.code) {
			t.Errorf("%s\n got %v\nwant error %s", tc.expr, err, tc.code)
		}
	}
}

// TestBuiltinListTypeAcceptedSources pins the sources a cast to a list type
// must accept, so that the 18.3.6 operand gate is not read as narrower than it
// is.
//
// xs:NMTOKEN is DERIVED from xs:string, so a value of it satisfies "must be of
// type xs:string" -- cbcl-castable-NMTOKENS-009 requires
// "xs:NMTOKEN('a') castable as xs:NMTOKENS" to be true. An empty argument to
// the constructor is the empty sequence, which is the whole reason 17.3 gives
// the signatures a "*" return type rather than a "+".
func TestBuiltinListTypeAcceptedSources(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{`xs:NMTOKEN("a") castable as xs:NMTOKENS`, "true"},
		{`xs:untypedAtomic("a b c") castable as xs:NMTOKENS`, "true"},
		{`"a b c" castable as xs:NMTOKENS`, "true"},
		{`xs:anyURI("abcd") castable as xs:IDREFS`, "false"},
		{`count(xs:IDREFS(()))`, "0"},
		// Two XML-space-separated tokens, both valid NMTOKENs.
		{`"a b" castable as xs:NMTOKENS`, "true"},
	} {
		got, err := evalList(t, tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if s := got[0].(*xdm.Atomic).String(); s != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.expr, s, tc.want)
		}
	}
}
