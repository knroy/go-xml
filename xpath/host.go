package xpath

import "github.com/knroy/go-xml/xdm"

// This file exports the small pieces of the expression engine that a host
// language built on top of it needs and cannot reasonably reimplement.
//
// XQuery is the caller. Its switch expression is defined in terms of
// fn:deep-equal and its typeswitch in terms of a SequenceType written in the
// query, and both of those are decided here — by code that is already
// conformant and that has no business being duplicated in a second package
// where it would drift. Nothing here adds behaviour; each is a one-line
// forward to what fn:deep-equal, "instance of" and the sequence-type parser
// already do.

// DeepEqualSequences reports whether two sequences are deep-equal, which is
// what fn:deep-equal answers.
//
// ctx supplies the collation string comparison uses; a nil ctx takes the
// default one.
func DeepEqualSequences(ctx *Context, a, b xdm.Sequence) (bool, error) {
	if ctx == nil {
		ctx = NewContext(nil, nil)
	}
	return deepEqual(ctx, a, b)
}

// ParseSequenceType parses a SequenceType [79] written in src, resolving any
// prefix in it with ns.
//
// XQuery's typeswitch writes a SequenceType in a position the expression
// grammar has no production for, so the host has to parse one on its own. It
// does so by asking this rather than by growing a second type parser, which
// would have to track every schema type, every kind test and every occurrence
// rule that this one already knows.
//
// The whole of src must be the type: trailing text is a syntax error rather
// than something to be ignored, so that "xs:integer)" is refused where the
// caller's brace matching went wrong.
//
// The type is parsed at XPath 3.1 whatever version the host is at, so a type
// written in an XQuery 1.0 module's typeswitch may name map() or array() and
// is accepted. Taking a Version would be the honest signature, but this is
// exported and the divergence is permissive -- an earlier module is allowed a
// type it should not have had, and is not given a wrong answer -- so the
// widening is left in place rather than paid for with a breaking change.
func ParseSequenceType(src string, ns NamespaceResolver) (SequenceType, error) {
	if ns == nil {
		ns = defaultResolver{}
	}
	lex := NewLexer(src)
	lex.version = XPath31
	toks, err := lex.Tokens()
	if err != nil {
		return SequenceType{}, err
	}
	// A braced URI literal in a type — Q{http://example.com}e — is rewritten
	// by the lexer to a synthetic prefix, which only this wrapper can
	// resolve. parseWith does the same thing for the same reason.
	if len(lex.bracedURIs) > 0 {
		ns = wrapBraced(ns, lex.bracedURIs)
	}
	p := &Parser{toks: toks, src: src, ns: ns, version: XPath31}
	st, err := p.parseSequenceType()
	if err != nil {
		return SequenceType{}, err
	}
	if !p.atEOF() {
		return SequenceType{}, p.errorf("unexpected %q after a sequence type",
			p.cur().Val)
	}
	return st, nil
}
