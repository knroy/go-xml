// Package xquery implements XQuery 3.1.
//
// XQuery and XPath share an expression language, and this package does not
// reimplement it: expressions are compiled by the xpath package, which is at
// 100% of the QT3 suite for XPath 2.0, 3.0 and 3.1. What lives here is what
// XQuery has and XPath does not — constructors, FLWOR, the prolog, and the
// handful of expressions that are XQuery's alone.
//
// # Why the parser reads the source rather than a token stream
//
// A direct element constructor puts XML syntax inside expression syntax, and
// an enclosed expression puts expression syntax back inside XML:
//
//	<a>{ $x + 1 }</a>
//
// Whether "a" is a tag name or a name test, and whether "+" is an operator or
// literal text, is not decidable without knowing where in the nesting the
// reader is. XPath's lexer runs to completion before its parser starts, and
// its parser backtracks by rewinding an index into the finished token slice —
// a design that is correct for XPath and cannot answer the question above.
//
// Rather than make that lexer re-entrant, which would put a conformant
// component at risk for a language it does not implement, this parser reads
// constructor syntax directly from the source and hands each enclosed
// expression to xpath as a substring. XML syntax stays here; expression
// syntax stays there. BaseX takes the same approach; Saxon instead uses a
// mode-switching tokeniser, which suits a codebase whose parser was written
// for both languages from the start.
package xquery
