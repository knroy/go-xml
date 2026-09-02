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
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				return 0, err
			}
			i = end
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					return 0, err
				}
				i = end
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
		switch src[i] {
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					return 0, err
				}
				i = end
				continue
			}
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
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				return 0, err
			}
			i = end
		}
	}
	return 0, fmt.Errorf("XPST0003: unbalanced %q at offset %d", "(", open)
}
