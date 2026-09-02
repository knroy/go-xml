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
		case '`':
			// A string constructor's content is not syntax; step over it
			// whole, the way a string literal is stepped over.
			if strings.HasPrefix(src[i:], "``[") {
				end, err := skipStringConstructor(src, i)
				if err != nil {
					return 0, err
				}
				i = end
			}
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
		}
	}
	return 0, fmt.Errorf("XPST0003: unterminated enclosed expression at offset %d", open)
}

// skipString returns the index of the quote closing the literal that opens at
// src[i].
//
// The doubled quote is XQuery's only escape inside a literal: 'it''s' is one
// string holding an apostrophe. A literal opened with one quote is unaffected
// by the other, so 'a"b' and "a'b" both hold what they look like they hold.
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
		case '`':
			if strings.HasPrefix(src[i:], "``[") {
				end, err := skipStringConstructor(src, i)
				if err != nil {
					return 0, err
				}
				i = end
				continue
			}
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

// skipStringConstructor returns the index of the last byte of the "]``" that
// closes the string constructor opening at src[i], which must be "``[".
//
// The scanners that walk an expression looking for a comma, a bracket or a
// keyword all have to step over one of these whole, for the reason they step
// over a string literal: §3.11.6 exists so that a query can carry text with
// quotes, braces and brackets in it without escaping any of them, so every
// byte between the delimiters is content and none of it is syntax. Reading
// "``[[\"']]``" as a bracket and then a string literal is what left
// string-constructor-021's regex looking unterminated.
//
// The one thing inside that is not content is an interpolation, "`{ ... }`",
// whose body is an expression and may hold a nested string constructor of its
// own; a depth count is kept so that the inner one's "]``" does not close the
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
