package xpath

// The version at which a *named function reference* resolves.
//
// "fn:concat#3" is XPath 3.0 syntax, so the parser gates it on the version it
// is parsing. That gate is right for a construct the module builds, but a
// function reference is not one: it names a function that either exists or
// does not, and which functions exist is already a property of the processor
// rather than of the module -- see Context.LibraryVersion, and
// Context.RegexVersion for the same separation applied to the regex dialect.
//
// The XSLT suite settles it the same way it settled those two. Five cases --
// system-property-023 and -024, regex-090 and -091, and for-each-group-090 --
// are version="2.0" stylesheets scoped XSLT30+ that write "#N" and expect a
// 3.0 processor to resolve it. No case anywhere in the suite requires a 2.0
// module to *reject* "#N", so nothing is traded away by admitting it.
//
// It is a parser floor rather than a Context field because a reference is
// resolved by the grammar: by the time a Context exists the expression has
// already failed to parse. The dial is set from the same processor version
// that feeds RegexVersion and LibraryVersion, so the three remain one
// mechanism asking one question.

// refVersion is the version at which p accepts a named function reference.
//
// The larger of the parsed version and the reference floor, so that raising
// one never lowers the other and a zero floor means "whatever version says".
func (p *Parser) refVersion() Version {
	if p.refFloor > p.version {
		return p.refFloor
	}
	return p.version
}
