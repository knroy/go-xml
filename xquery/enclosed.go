package xquery

import (
	"fmt"
	"strings"
)

// findEnclosed returns the index of the "}" closing the enclosed expression
// that opens at src[open], which must be a "{".
//
// This is the whole of the boundary between the two syntaxes: everything
// between the braces is XPath and is handed to that parser, and everything
// outside them is XQuery's own. Getting it wrong in either direction produces
// a parse error a long way from its cause, so the cases it has to survive are
// worth naming.
//
// A brace inside a string literal is not a brace. "}" is an ordinary
// character in 'a}b', and a doubled quote escapes a quote rather than ending
// the literal, so the scan has to know which quote opened the string and
// whether the next one closes it or is an escaped pair.
//
// A brace inside a comment is not a brace either, and XQuery comments nest:
// (: a (: b :) c :) is one comment, so a depth count is needed rather than a
// search for the first ":)".
//
// Braces nest through further constructors: <a>{ <b>{$x}</b> }</a> reaches
// this function once, and the inner pair must not close the outer. Doubled
// braces are the escape for a literal brace in element content — {{ and }} —
// but only outside an enclosed expression: inside one they are two ordinary
// braces of an inner map constructor, and treating them as an escape would
// swallow the map.
func findEnclosed(src string, open int) (int, error) {
	if open >= len(src) || src[open] != '{' {
		return 0, fmt.Errorf("XPST0003: expected %q at offset %d", "{", open)
	}
	depth := 0
	for i := open; i < len(src); i++ {
		// A string literal, a comment, a pragma or a string constructor is
		// not syntax: a brace inside any of them is an ordinary character.
		// skipNonSyntax is the one place that knows which regions those are.
		if end, ok, err := skipNonSyntax(src, i); ok {
			if err != nil {
				return 0, err
			}
			i = end
			continue
		}
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		case '<':
			// A direct constructor's content is markup, not expression text,
			// and the two disagree about the quote: an apostrophe is an
			// ordinary character in "<f>f's value</f>" where in an expression
			// it opens a literal. Reading the constructor as expression text
			// sent skipString off looking for the partner of that apostrophe
			// and it ran to the end of the query — K2-Axes-1 is exactly that
			// query. Only the markup is stepped over here; a "{" inside it
			// puts the scan back on expression text, which is what makes
			// "<a>{ 'x}y' }</a>" still find its own brace.
			if end, ok := skipDirectConstructor(src, i); ok {
				i = end
			}
		}
	}
	return 0, fmt.Errorf("XPST0003: unterminated enclosed expression at offset %d", open)
}

// skipDirectConstructor returns the index of the last character of the direct
// element constructor that opens at src[i], reporting false when src[i] does
// not begin one.
//
// It steps over markup only: attribute values, whose quotes do delimit, and
// element content, whose quotes do not. An enclosed expression inside either
// is handed back to findEnclosed, so the two functions alternate through a
// constructor that nests expressions and elements to any depth.
//
// A false report is deliberately cheap: "a < b" is a comparison and there is
// no constructor to skip, so the caller carries on reading expression text and
// the "<" is just an operator.
func skipDirectConstructor(src string, i int) (int, bool) {
	if i+1 >= len(src) || !isNameStartByte(src[i+1]) {
		return 0, false
	}
	depth := 0
	for j := i; j < len(src); j++ {
		switch {
		case src[j] == '<' && j+1 < len(src) && src[j+1] == '/':
			// An end tag closes the innermost open element. Its name cannot
			// contain a quote or a brace, so it is enough to run to the ">".
			k := j
			for k < len(src) && src[k] != '>' {
				k++
			}
			if k >= len(src) {
				return 0, false
			}
			depth--
			j = k
			if depth == 0 {
				return j, true
			}
		case src[j] == '<' && j+1 < len(src) && src[j+1] == '!':
			// A comment or CDATA section: neither holds anything the scan
			// cares about, so run to its end.
			var close string
			if strings.HasPrefix(src[j:], "<!--") {
				close = "-->"
			} else if strings.HasPrefix(src[j:], "<![CDATA[") {
				close = "]]>"
			} else {
				return 0, false
			}
			k := strings.Index(src[j:], close)
			if k < 0 {
				return 0, false
			}
			j += k + len(close) - 1
		case src[j] == '<' && j+1 < len(src) && src[j+1] == '?':
			k := strings.Index(src[j:], "?>")
			if k < 0 {
				return 0, false
			}
			j += k + 1
		case src[j] == '<':
			if j+1 >= len(src) || !isNameStartByte(src[j+1]) {
				return 0, false
			}
			// A start tag. Quotes inside it delimit attribute values, and an
			// attribute value may itself hold an enclosed expression, so the
			// tag is walked rather than skipped wholesale.
			k, selfClosing, ok := skipStartTag(src, j)
			if !ok {
				return 0, false
			}
			j = k
			if !selfClosing {
				depth++
			} else if depth == 0 {
				return j, true
			}
		case src[j] == '{':
			// Element content is back to expression text inside the braces.
			end, err := findEnclosed(src, j)
			if err != nil {
				return 0, false
			}
			j = end
		}
	}
	return 0, false
}

// skipStartTag returns the index of the ">" ending the start tag that opens at
// src[i], and whether the tag closed the element itself.
func skipStartTag(src string, i int) (end int, selfClosing bool, ok bool) {
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\'', '"':
			e, err := skipString(src, j)
			if err != nil {
				return 0, false, false
			}
			j = e
		case '{':
			e, err := findEnclosed(src, j)
			if err != nil {
				return 0, false, false
			}
			j = e
		case '/':
			if j+1 < len(src) && src[j+1] == '>' {
				return j + 1, true, true
			}
		case '>':
			return j, false, true
		}
	}
	return 0, false, false
}

// skipString returns the index of the quote closing the literal that opens at
// src[i].
//
// The doubled quote is XQuery's only escape inside a literal: a two-character
// run of the opening quote stands for one of that character, so the six-byte
// literal spelling "it" + two apostrophes + "s" is one string holding an
// apostrophe. A literal opened with one quote is unaffected by the other, so
// 'a"b' and "a'b" both hold what they look like they hold.
//
// The example is spelled out in words rather than written literally because
// gofmt rewrites a doubled ASCII apostrophe in a comment into a typographic
// quote, which would make this paragraph describe the wrong escape.
func skipString(src string, i int) (int, error) {
	q := src[i]
	for j := i + 1; j < len(src); j++ {
		if src[j] != q {
			continue
		}
		if j+1 < len(src) && src[j+1] == q {
			// A doubled quote is an escaped one; step over both.
			j++
			continue
		}
		return j, nil
	}
	return 0, fmt.Errorf("XPST0003: unterminated string literal at offset %d", i)
}

// skipComment returns the index of the ")" closing the comment that opens at
// src[i], which must be "(:".
//
// Comments nest, so this counts depth rather than looking for the first
// closing pair. A quote inside a comment is not a string: (: it's :) is a
// well-formed comment, and treating the apostrophe as opening a literal would
// run to the end of the query looking for its partner.
func skipComment(src string, i int) (int, error) {
	depth := 0
	for j := i; j+1 < len(src); j++ {
		switch {
		case src[j] == '(' && src[j+1] == ':':
			depth++
			j++
		case src[j] == ':' && src[j+1] == ')':
			depth--
			j++
			if depth == 0 {
				return j, nil
			}
		}
	}
	return 0, fmt.Errorf("XPST0003: unterminated comment at offset %d", i)
}

// findParen returns the index of the ")" closing the parenthesis that opens at
// src[open].
//
// It is findEnclosed's counterpart for the parenthesised operand of a switch
// or typeswitch, and it survives the same three things: a parenthesis inside a
// string literal, a comment, which itself opens with a parenthesis, and
// arbitrary nesting. Braces and brackets are counted too, because a map or
// array constructor inside the operand may hold a parenthesis of its own that
// is already balanced within it.
func findParen(src string, open int) (int, error) {
	if open >= len(src) || src[open] != '(' {
		return 0, fmt.Errorf("XPST0003: expected %q at offset %d", "(", open)
	}
	depth := 0
	for i := open; i < len(src); i++ {
		// A comment and a pragma both open with "(", so this has to come
		// before the depth count: neither opens a parenthesis the ")" that
		// closes the operand could ever match.
		if end, ok, err := skipNonSyntax(src, i); ok {
			if err != nil {
				return 0, err
			}
			i = end
			continue
		}
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, nil
			}
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
	}
	return 0, fmt.Errorf("XPST0003: unbalanced %q at offset %d", "(", open)
}

// skipStringConstructor returns the index of the last byte of the "]“" that
// closes the string constructor opening at src[i], which must be "“[".
//
// The scanners that walk an expression looking for a comma, a bracket or a
// keyword all have to step over one of these whole, for the reason they step
// over a string literal: §3.11.6 exists so that a query can carry text with
// quotes, braces and brackets in it without escaping any of them, so every
// byte between the delimiters is content and none of it is syntax. Reading
// "“[[\"']]“" as a bracket and then a string literal is what left
// string-constructor-021's regex looking unterminated.
//
// The one thing inside that is not content is an interpolation, "`{ ... }`",
// whose body is an expression and may hold a nested string constructor of its
// own; a depth count is kept so that the inner one's "]“" does not close the
// outer.
func skipStringConstructor(src string, i int) (int, error) {
	if !strings.HasPrefix(src[i:], "``[") {
		return 0, fmt.Errorf("XPST0003: expected %q at offset %d", "``[", i)
	}
	start := i
	i += 3
	for i < len(src) {
		switch {
		case strings.HasPrefix(src[i:], "]``"):
			return i + 2, nil
		case strings.HasPrefix(src[i:], "`{"):
			end, err := findEnclosed(src, i+1)
			if err != nil {
				return 0, err
			}
			// [180] StringConstructorInterpolation closes with "}`", so the
			// backtick after the brace belongs to the delimiter.
			i = end + 1
			if i < len(src) && src[i] == '`' {
				i++
			}
		default:
			i++
		}
	}
	return 0, fmt.Errorf(
		"XPST0003: unterminated string constructor at offset %d", start)
}

// skipNonSyntax steps over the one non-syntax token that begins at src[i],
// returning the index of its last byte and reporting whether src[i] began one
// at all.
//
// This is the single place that knows what "not syntax" means, and it exists
// because every scanner in this package needs the same answer. A scanner that
// walks raw source looking for a comma, a bracket, a keyword or a "<" is
// counting characters that the grammar has not yet tokenised, and four regions
// of a query hold characters that look like grammar but are not:
//
//   - [222] StringLiteral. A brace, a comma or a parenthesis inside 'a}b' is an
//     ordinary character, and the doubled quote is the only escape.
//   - [91] Comment ::= "(:" (CommentContents | Comment)* ":)". Comments nest,
//     so "(: a (: b :) c :)" is one comment, and a quote inside one is not a
//     literal: "(: it's :)" is well formed.
//   - [105] Pragma ::= "(#" S? EQName (S PragmaContents)? "#)", where
//     PragmaContents is "(Char* - (Char* '#)' Char*))" -- arbitrary text that
//     no rule ever parses. A quote, a brace or a comma in a pragma is an
//     ordinary character.
//   - [180] StringConstructor, opened by two backticks and a bracket.
//     §3.11.6 exists precisely so a query can carry quotes, braces and
//     brackets without escaping any of them.
//
// The pragma is the case that motivated unifying these. Most copies of this
// scan handled comments and literals but were blind to "(#", so the quote in
// "1 eq (#p:x \" #) {1}" opened a string literal that never closed; the scan
// gave up, the expression was routed to the XPath parser, which has no pragma
// in its grammar at all, and the query was refused. Fixing three of the copies
// left the rest carrying the same bug, which is the shape of defect this
// helper is meant to make unrepresentable: there is now one answer to "what is
// not syntax here", and every scanner asks it.
//
// Errors. The copies disagreed about what to do with an unterminated region:
// most swallowed the error and carried on scanning, while parseExprItem
// returned it. Returning it is correct and is the rule here. A scan that
// cannot tell where a non-syntax region ends does not know where syntax
// resumes, so continuing means scanning a comment's or a pragma's contents as
// though they were grammar -- exactly the failure the pragma bug was. The
// error is reported and the caller decides; callers whose contract is a
// yes/no answer with no error channel (needsXQueryParser, blankEnclosedBody
// and their kin) still have to choose, and each chooses the answer that hands
// the text to a parser that will report the fault in context, rather than
// guessing at a boundary it cannot see.
//
// A "(" that is not followed by ":" or "#" is an ordinary parenthesis, and a
// backtick that does not open a string constructor is an ordinary byte. Both
// are reported as "not a non-syntax token", so the caller's own case for them
// still runs: the parenthesis it must count, the operator it must read.
func skipNonSyntax(src string, i int) (end int, ok bool, err error) {
	if i >= len(src) {
		return 0, false, nil
	}
	switch src[i] {
	case '\'', '"':
		end, err := skipString(src, i)
		return end, true, err
	case '(':
		if i+1 < len(src) && src[i+1] == ':' {
			end, err := skipComment(src, i)
			return end, true, err
		}
		if i+1 < len(src) && src[i+1] == '#' {
			end, err := skipPragma(src, i)
			return end, true, err
		}
	case '`':
		if strings.HasPrefix(src[i:], "``[") {
			end, err := skipStringConstructor(src, i)
			return end, true, err
		}
	}
	return 0, false, nil
}

// skipPragma returns the index of the ")" closing the pragma that opens at
// src[i], which must be "(#".
//
// [105] Pragma ::= "(#" S? EQName (S PragmaContents)? "#)" with
// PragmaContents ::= (Char* - (Char* '#)' Char*)), so the contents run to the
// *first* "#)" and nothing inside them nests or escapes. Unlike a comment,
// a pragma does not nest: "(# a (# b #)" ends at that one "#)".
func skipPragma(src string, i int) (int, error) {
	if !strings.HasPrefix(src[i:], "(#") {
		return 0, fmt.Errorf("XPST0003: expected %q at offset %d", "(#", i)
	}
	j := strings.Index(src[i+2:], "#)")
	if j < 0 {
		return 0, fmt.Errorf("XPST0003: unterminated pragma at offset %d", i)
	}
	return i + 2 + j + 1, nil
}
