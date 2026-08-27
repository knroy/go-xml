package xpath

import (
	"github.com/knroy/go-xml/xdm"
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
func castToListType(a *xdm.Atomic, facet string) (xdm.Sequence, error) {
	toks := collapseXMLSpaceFields(a.String())
	if len(toks) == 0 {
		return nil, xdm.ErrCast(
			"FORG0001: %q has no tokens, so it is not a value of a list type", a.String())
	}
	out := make(xdm.Sequence, 0, len(toks))
	for _, tok := range toks {
		v, err := applyStringFacet(xdm.NewString(tok), facet)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// collapseXMLSpaceFields splits on the four XML whitespace characters alone.
// strings.Fields splits on every Unicode space, which would treat U+00A0 as a
// separator; a non-breaking space is an ordinary NMTOKEN character.
func collapseXMLSpaceFields(s string) []string {
	var out []string
	start := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
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
