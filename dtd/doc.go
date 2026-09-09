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
// Two rules have no XSD counterpart and are checked here directly, because
// both point from an attribute at a declaration somewhere else in the DTD
// rather than at the attribute's own value space (XML 1.0 §3.3.1): every
// name in a NOTATION attribute's enumeration must be declared by a
// <!NOTATION>, and an ENTITY or ENTITIES attribute must name entities
// declared with an NDATA notation. Both are skipped when only half the DTD
// was read, since neither can distinguish an undeclared name from an unread
// declaration.
//
// What DTD has that XSD does not is the *external* subset, which is a file
// reference, along with the parameter entities and conditional sections that
// only exist there.
//
// # Two entry points
//
// Parse reads a DOCTYPE's internal subset and fetches nothing. It is what a
// caller wants for a document that arrived over the wire and whose DTD is
// wholly inline.
//
// Load reads both subsets. Fetching the external one is the attack
// AllowDOCTYPE exists to gate, so it happens only through a caller-supplied
// LoadOptions.Resolver — nil in the zero value, following
// xsd.Options.Resolver. With none, a DOCTYPE naming an external subset is
// REFUSED rather than validated against half a DTD: the external subset
// routinely holds every element declaration in the language, so reporting a
// document valid against the internal half alone would turn "I could not read
// the constraints" into "the constraints hold". LoadOptions.InternalSubsetOnly
// is how a caller asks for that partial reading deliberately.
package dtd
