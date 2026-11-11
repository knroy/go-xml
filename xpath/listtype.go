package xpath

import (
	"iter"

	"github.com/knroy/go-xml/v2/xdm"
)

// The three built-in list types, xs:NMTOKENS, xs:IDREFS and xs:ENTITIES.
//
// A list type's value space is a *sequence* of atomic values, one per
// whitespace-separated token, so no single type code stands for it and it
// cannot go in the atomic-type table the way xs:NMTOKEN does. The constructor
// functions have always existed (see registerListTypes); what is handled here
// is the name appearing in type position, as in "$x castable as xs:NMTOKENS".
//
// Each is a list of a string subtype with a minLength of 1, so the empty
// sequence of tokens is not a value of the type: castable-007 asserts that
// '', ' ' and ' \n ' are all not castable to xs:NMTOKENS while 'a b c' is.

// listItemFacet maps a built-in list type's local name to the facet name of
// its item type, which is what applyStringFacet validates a token against.
var listItemFacet = map[string]string{
	"NMTOKENS": "NMTOKEN",
	"IDREFS":   "IDREF",
	"ENTITIES": "ENTITY",
}

// listTypeName reports the item facet of a built-in list type named by lex,
// resolving the prefix against the xs: namespace exactly as isErrorTypeName
// does.
func listTypeName(lex string, ns NamespaceResolver) (string, bool) {
	prefix, local := xdm.SplitQName(lex)
	facet, ok := listItemFacet[local]
	if !ok {
		return "", false
	}
	if prefix == "" {
		if ns == nil || ns.DefaultElementNamespace() != xdm.NSXS {
			return "", false
		}
		return facet, true
	}
	uri, resolved := ns.ResolvePrefix(prefix)
	if !resolved || uri != xdm.NSXS {
		return "", false
	}
	return facet, true
}

// castToListType casts one atomic value to a built-in list type, returning the
// sequence of atomic values its tokens denote.
//
// The lexical form is split on XML whitespace and each token cast to the item
// type. A form with no tokens fails: the built-in list types all carry
// minLength 1.
//
// Each token is annotated with the item type it was validated against. F&O 3.0
// 17.3 gives the constructors the return types xs:NMTOKEN*, xs:IDREF* and
// xs:ENTITY*, and 18.3.6 says the result of casting to a list type is "a
// sequence of zero or more atomic values each of which is an instance of the
// item type of L". Without the annotation the tokens came back as plain
// xs:string, so "'a b c' cast as xs:IDREFS instance of xs:IDREF*" was false --
// CastAs-ListType-7 and its neighbours.
//
// The token count is reserved against the item budget before any item is
// built, as fn:tokenize does, so an oversized list is refused up front.
func castToListType(ctx *Context, a *xdm.Atomic, facet string) (xdm.Sequence, error) {
	s := a.String()
	n := 0
	for range xmlSpaceFields(s) {
		n++
	}
	if n == 0 {
		return nil, xdm.ErrCast(
			"%q has no tokens, so it is not a value of a list type", s)
	}
	out, err := makeSequence(ctx, n)
	if err != nil {
		return nil, err
	}
	for tok := range xmlSpaceFields(s) {
		v, err := applyStringFacet(xdm.NewString(tok), facet)
		if err != nil {
			return nil, err
		}
		out = append(out, v.WithDerived(facet))
	}
	return out, nil
}

// listTypeCastable answers "castable as" for a built-in list type without
// building the sequence: every token must satisfy the facet, and there must
// be at least one.
func listTypeCastable(a *xdm.Atomic, facet string) bool {
	n := 0
	for tok := range xmlSpaceFields(a.String()) {
		if _, err := applyStringFacet(xdm.NewString(tok), facet); err != nil {
			return false
		}
		n++
	}
	return n > 0
}

// listSourceValue is the operand-type gate F&O 3.0 18.3.6 puts on a cast to a
// list type: "the supplied value must be of type xs:string or
// xs:untypedAtomic". Anything else is XPTY0004 rather than a failed cast, so
// xs:ENTITIES(xs:anyURI("abcd")) is a type error and not the one-token list
// the string value would have made -- CastAs-ListType-17 and -18.
//
// The rule reaches the constructor functions as well as the cast, because 17.3
// defines their semantics as "equivalent to casting to the corresponding types
// from xs:string".
func listSourceValue(a *xdm.Atomic) (string, error) {
	switch a.Type {
	case xdm.TypeString, xdm.TypeUntypedAtomic:
		return a.String(), nil
	}
	return "", xdm.ErrType(
		"a value of type %s cannot be cast to a list type, "+
			"which requires xs:string or xs:untypedAtomic", a.TypeName())
}

// xmlSpaceFields yields the tokens of s split on the four XML whitespace
// characters alone, without allocating a slice of them. strings.Fields splits
// on every Unicode space, which would treat U+00A0 as a separator; a
// non-breaking space is an ordinary NMTOKEN character.
func xmlSpaceFields(s string) iter.Seq[string] {
	return func(yield func(string) bool) {
		start := -1
		for i := 0; i < len(s); i++ {
			switch s[i] {
			case ' ', '\t', '\n', '\r':
				if start >= 0 {
					if !yield(s[start:i]) {
						return
					}
					start = -1
				}
			default:
				if start < 0 {
					start = i
				}
			}
		}
		if start >= 0 {
			yield(s[start:])
		}
	}
}

// listTypeOfItemFacet is the inverse of listItemFacet, for rendering a list
// type by the name that was written.
func listTypeOfItemFacet(facet string) string {
	for name, f := range listItemFacet {
		if f == facet {
			return name
		}
	}
	return facet
}
