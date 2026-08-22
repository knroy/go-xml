// Package dtd validates an XML document against the DTD in its internal
// subset.
//
// It lives outside xdm because it needs the content-model automaton in xsd,
// and xdm is what xsd is built on — putting it there would invert the
// dependency. The split also keeps the parser's job clear: xdm reads a
// document and applies the two declarations whose absence is visible in the
// data model (attribute defaults and internal entities), while deciding
// whether the document *satisfies* its DTD is validation and belongs here.
//
// # Scope
//
// A DTD is a smaller language than XSD, and almost all of it maps onto
// machinery that already exists:
//
//   - <!ELEMENT> content models are a strict subset of what xsd's Glushkov
//     automaton compiles — DTD has sequence, choice, and the ?, * and +
//     quantifiers, and no numeric occurrence bounds at all.
//   - <!ATTLIST> required/implied/fixed maps onto attribute use.
//   - ID, IDREF and IDREFS are the same document-scoped uniqueness and
//     reference checks XSD defines.
//
// What DTD has that XSD does not is the *external* subset, which is a file
// reference. Fetching one is the attack AllowDOCTYPE exists to gate, so it is
// not read: a DOCTYPE naming an external subset validates against whatever its
// internal subset declares, and Validate says so rather than pretending the
// document was fully checked.
package dtd
