package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xpath"
)

// avt is a compiled attribute value template: literal text interleaved with
// XPath expressions in braces, as in name="item-{position()}".
//
// A pure-literal AVT is the overwhelmingly common case (every ordinary
// attribute on a literal result element is one), so it is detected at compile
// time and evaluated without touching the XPath machinery.
type avt struct {
	// literal holds the whole value when there are no expressions.
	literal string
	isLit   bool
	parts   []avtPart
}

type avtPart struct {
	text string
	expr *xpath.Compiled // nil for a literal part
}

// compileAVT parses an attribute value template.
//
// Doubled braces are escapes: "{{" is a literal "{" and "}}" a literal "}".
// Getting this wrong matters because CSS and JSON in output attributes are
// full of braces, and mis-parsing them turns a stylesheet into a stream of
// XPath syntax errors.
func compileAVT(src string, ns xpath.NamespaceResolver) (*avt, error) {
	if !strings.ContainsAny(src, "{}") {
		return &avt{literal: src, isLit: true}, nil
	}

	a := &avt{}
	var lit strings.Builder

	for i := 0; i < len(src); {
		switch src[i] {
		case '{':
			if i+1 < len(src) && src[i+1] == '{' {
				lit.WriteByte('{')
				i += 2
				continue
			}
			// Flush the literal run before the expression.
			if lit.Len() > 0 {
				a.parts = append(a.parts, avtPart{text: lit.String()})
				lit.Reset()
			}
			end, err := findAVTClose(src, i+1)
			if err != nil {
				return nil, err
			}
			exprSrc := src[i+1 : end]
			comp, err := compileExpr(exprSrc, ns)
			if err != nil {
				return nil, fmt.Errorf("in attribute value template {%s}: %w", exprSrc, err)
			}
			a.parts = append(a.parts, avtPart{expr: comp})
			i = end + 1

		case '}':
			if i+1 < len(src) && src[i+1] == '}' {
				lit.WriteByte('}')
				i += 2
				continue
			}
			return nil, fmt.Errorf("XTSE0370: unmatched '}' in attribute value template %q", src)

		default:
			lit.WriteByte(src[i])
			i++
		}
	}
	if lit.Len() > 0 {
		a.parts = append(a.parts, avtPart{text: lit.String()})
	}

	// An AVT that turned out to have no expressions is still a literal.
	if len(a.parts) == 1 && a.parts[0].expr == nil {
		return &avt{literal: a.parts[0].text, isLit: true}, nil
	}
	if len(a.parts) == 0 {
		return &avt{literal: "", isLit: true}, nil
	}
	return a, nil
}

// findAVTClose locates the '}' closing an expression that starts at i,
// skipping braces inside string literals and XPath comments.
//
// An XPath expression can contain quoted strings with braces in them, as in
// {concat('{', $x)}, so scanning for the first '}' is wrong. The same is true
// of an XPath comment: "(: a } here :)" is commentary, not the end of the
// expression. XPath comments nest, so the comment is tracked with a depth
// counter rather than a scan to the first ":)" — "(: (: :) :)" ends at the
// second ":)", and stopping at the first would resume brace scanning inside
// text that is still commented out.
func findAVTClose(src string, i int) (int, error) {
	var quote byte
	comment := 0
	for ; i < len(src); i++ {
		c := src[i]
		switch {
		case quote != 0:
			if c == quote {
				// A doubled quote inside a string literal is an escape.
				if i+1 < len(src) && src[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
		case comment > 0:
			if c == '(' && i+1 < len(src) && src[i+1] == ':' {
				comment++
				i++
			} else if c == ':' && i+1 < len(src) && src[i+1] == ')' {
				comment--
				i++
			}
		case c == '(' && i+1 < len(src) && src[i+1] == ':':
			comment++
			i++
		case c == '\'' || c == '"':
			quote = c
		case c == '}':
			return i, nil
		}
	}
	return 0, fmt.Errorf("XTSE0350: unclosed '{' in attribute value template %q", src)
}

// eval renders the AVT in the given runtime context.
func (a *avt) eval(rt *runtime) (string, error) {
	if a.isLit {
		return a.literal, nil
	}
	var sb strings.Builder
	for _, p := range a.parts {
		if p.expr == nil {
			sb.WriteString(p.text)
			continue
		}
		seq, err := p.expr.Eval(rt.ctx)
		if err != nil {
			return "", err
		}
		// An AVT concatenates the string values of every item, space
		// separated, which is fn:string-join(seq, ' ').
		sb.WriteString(stringJoin(seq, " "))
	}
	return sb.String(), nil
}
